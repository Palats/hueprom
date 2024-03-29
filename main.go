// Implements a Philips Hue to Prometheus gateway
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"text/template"
	"time"

	"github.com/amimof/huego"
	"github.com/davecgh/go-spew/spew"
	"github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	mSensorLastUpdated = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hue_sensor_lastupdated",
		Help: "Last update of the sensor, in ms since epoch.",
	}, []string{"name", "uniqueid"})
	mSensorButtonEvent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hue_sensor_buttonevent",
		Help: "Last update of the sensor, button info.",
	}, []string{"name", "uniqueid"})
	mSensorOn = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hue_sensor_on",
		Help: "Is the sensor seen as as on from the bridge.",
	}, []string{"name", "uniqueid"})
	mSensorReachable = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hue_sensor_reachable",
		Help: "Is the sensor reachable from the bridge.",
	}, []string{"name", "uniqueid"})
	mButtonClick = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hue_button_click",
		Help: "Number of time a button has been clicked.",
	}, []string{"name", "uniqueid", "button"})

	mLightOn = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hue_light_on",
		Help: "Is the light set to on on the Bridge.",
	}, []string{"name", "uniqueid"})
	mLightReachable = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hue_light_reachable",
		Help: "Is the light reachable.",
	}, []string{"name", "uniqueid"})
)

func init() {
	prometheus.MustRegister(mSensorLastUpdated)
	prometheus.MustRegister(mSensorButtonEvent)
	prometheus.MustRegister(mSensorOn)
	prometheus.MustRegister(mSensorReachable)
	prometheus.MustRegister(mButtonClick)
	prometheus.MustRegister(mLightOn)
	prometheus.MustRegister(mLightReachable)
}

const timeFormat = "2006-01-02T15:04:05"

var indexTpl = template.Must(template.New("index").Parse(`
<html><body>
Philips Hue to Prometheus exporter.
</body></html>
`))

func b2f(b bool) float64 {
	if b {
		return 1.0
	}
	return 0.0
}

type SensorState struct {
	Labels      prometheus.Labels
	Lastupdated time.Time
	Buttonevent int64
}

type Server struct {
	bridge *huego.Bridge

	// See scanSensor for the key.
	sensors map[string]*SensorState
}

func New(bridge *huego.Bridge) *Server {
	return &Server{
		bridge: bridge,
	}
}

// Serve a basic homepage.
func (s *Server) Serve(w http.ResponseWriter, r *http.Request) {
	fmt.Printf("# Request method=%s, url=%s\n", r.Method, r.URL.String())
	if err := indexTpl.Execute(w, nil); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// loop takes care of the background polling.
func (s *Server) loop(ctx context.Context, delay time.Duration) error {
	for {
		if err := s.scanLights(ctx); err != nil {
			glog.Error(err)
		}
		if err := s.scanSensors(ctx); err != nil {
			glog.Error(err)
		}

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *Server) scanLights(ctx context.Context) error {
	lights, err := s.bridge.GetLightsContext(ctx)
	if err != nil {
		return err
	}

	for _, l := range lights {
		labels := prometheus.Labels{
			"name":     l.Name,
			"uniqueid": l.UniqueID,
		}
		mLightOn.With(labels).Set(b2f(l.State.On))
		mLightReachable.With(labels).Set(b2f(l.State.Reachable))
	}
	return nil
}

func (s *Server) scanSensors(ctx context.Context) error {
	sensors, err := s.bridge.GetSensorsContext(ctx)
	if err != nil {
		return err
	}

	states := make(map[string]*SensorState)

	for _, sensor := range sensors {
		key := fmt.Sprintf("%q [%s]", sensor.Name, sensor.UniqueID)
		state := &SensorState{
			Labels: prometheus.Labels{
				"name":     sensor.Name,
				"uniqueid": sensor.UniqueID,
			},
		}
		states[key] = state

		if strLastupdated, ok := sensor.State["lastupdated"].(string); ok && strLastupdated != "none" {
			state.Lastupdated, err = time.Parse(timeFormat, strLastupdated)
			if err != nil {
				glog.Errorf("Sensor %s, unable to parse %q: %v\n", key, strLastupdated, err)
			}
		}

		if v, ok := sensor.State["buttonevent"]; ok && v != nil {
			floatButtonevent, ok := v.(float64)
			if !ok {
				glog.Errorf("Sensor %s, unable to read buttonevent %v as float", key, v)
			}
			state.Buttonevent = int64(floatButtonevent)
		}

		if state.Lastupdated.IsZero() {
			mSensorLastUpdated.Delete(state.Labels)
		} else {
			mSensorLastUpdated.With(state.Labels).Set(float64(state.Lastupdated.UnixNano() / 1000))
		}

		if state.Buttonevent == 0 {
			mSensorButtonEvent.Delete(state.Labels)
		} else {
			mSensorButtonEvent.With(state.Labels).Set(float64(state.Buttonevent))
		}

		isOn, ok := sensor.Config["on"].(bool)
		if ok {
			mSensorOn.With(state.Labels).Set(b2f(isOn))
		} else {
			mSensorOn.Delete(state.Labels)
		}

		isReachable, ok := sensor.Config["reachable"].(bool)
		if ok {
			mSensorReachable.With(state.Labels).Set(b2f(isReachable))
		} else {
			mSensorReachable.Delete(state.Labels)
		}

		// And deal with events.
		if oldState := s.sensors[key]; oldState != nil {
			if state.Lastupdated.Equal(oldState.Lastupdated) && state.Buttonevent == oldState.Buttonevent {
				continue

			}
			buttonID := fmt.Sprint(state.Buttonevent &^ 3)
			isLong := (state.Buttonevent & 1) != 0
			isReleased := (state.Buttonevent & 2) != 0
			glog.Infof("Sensor %s triggered, buttonevent: %v, button: %v, long: %v, release: %v", key, state.Buttonevent, buttonID, isLong, isReleased)
			labels := prometheus.Labels{
				"button": buttonID,
			}
			for k, v := range state.Labels {
				labels[k] = v
			}
			if isReleased {
				mButtonClick.With(labels).Inc()
			}
		}
	}

	// Remove metrics
	for key, oldState := range s.sensors {
		if states[key] == nil {
			glog.Infof("Sensor %s removed", key)
			mSensorLastUpdated.Delete(oldState.Labels)
			mSensorButtonEvent.Delete(oldState.Labels)
			mSensorOn.Delete(oldState.Labels)
			mSensorReachable.Delete(oldState.Labels)
		}
	}

	s.sensors = states
	return nil
}

func createUser(ctx context.Context) error {
	bridge, err := huego.Discover()
	if err != nil {
		glog.Exit(err)
	}
	fmt.Println("Found bridge:", bridge.Host, bridge.ID, bridge.User)
	user, err := bridge.CreateUser("github.com/Palats/hueprom") // Link button needs to be pressed
	if err != nil {
		// If not cliecked: Error *huego.APIError
		//  Address == ""
		//  Type == 101
		//  Description == "link button not pressed"
		v := err.(*huego.APIError)
		fmt.Println("addr:", v.Address, "type:", v.Type, "desc:", v.Description)
		return err
	}
	fmt.Println("user:", user)
	return nil
}

func dump(ctx context.Context, fl *Flags) error {
	bridge, err := fl.Bridge()
	if err != nil {
		return err
	}

	fmt.Println("# -------- Lights --------")
	lights, err := bridge.GetLights()
	if err != nil {
		return err
	}
	spew.Dump(lights)

	fmt.Println()
	fmt.Println("# -------- Sensors --------")
	sensors, err := bridge.GetSensorsContext(ctx)
	if err != nil {
		return err
	}
	spew.Dump(sensors)
	return nil
}

// serve implements the `server` command. It polls periodically the bridge and
// export the data as Prometheus metrics.
func serve(ctx context.Context, fl *Flags) error {
	http.Handle("/metrics", promhttp.Handler())

	bridge, err := fl.Bridge()
	if err != nil {
		return err
	}
	s := New(bridge)
	go func() {
		err := s.loop(ctx, fl.Poll)
		glog.Exitf("Watching loop exited: %v", err)
	}()
	http.HandleFunc("/", s.Serve)

	addr := fmt.Sprintf(":%d", fl.Port)
	hostname, err := os.Hostname()
	if err != nil {
		hostname = ""
	}
	fmt.Printf("Listening on http://%s%s\n", hostname, addr)
	return http.ListenAndServe(addr, nil)
}

// Flags hold the value of all command flags.
type Flags struct {
	User string
	Port int
	Poll time.Duration
}

// Bridge provides the default bridge instance, using the user from the flags.
func (fl *Flags) Bridge() (*huego.Bridge, error) {
	bridge, err := huego.Discover()
	if err != nil {
		return nil, err
	}
	glog.Infof("Bridge ID: %v", bridge.ID)
	glog.Infof("Bridge host: %v", bridge.Host)
	return bridge.Login(fl.User), nil
}

func main() {
	ctx := context.Background()
	fmt.Println("Hueprom")

	fl := &Flags{}

	cmdServe := &cobra.Command{
		Use:   "serve",
		Short: "Run the Prometheus metrics exporter",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return serve(ctx, fl)
		},
	}
	cmdServe.PersistentFlags().IntVar(&fl.Port, "port", 7362, "HTTP port to listen on")
	cmdServe.PersistentFlags().DurationVar(&fl.Poll, "poll", 100*time.Millisecond, "Hue API polling interval")

	cmdDump := &cobra.Command{
		Use:   "dump",
		Short: "Dump Hue state.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return dump(ctx, fl)
		},
	}

	cmdCreateUser := &cobra.Command{
		Use:   "create-user",
		Short: "Create a new user on the bridge. Click the button just before running that command.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return createUser(ctx)
		},
	}

	cmdRoot := &cobra.Command{
		Use: "app",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			// flag.Parse()
		},
	}
	cmdRoot.PersistentFlags().StringVar(&fl.User, "user", "", "Hue username")
	cmdRoot.AddCommand(cmdServe, cmdCreateUser, cmdDump)

	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	// Fake parse the default Go flags - that appease glog, which otherwise
	// complains on each line. goflag.CommandLine do get parsed in parsed
	// through pflag and `AddGoFlagSet`.
	flag.CommandLine.Parse(nil)

	cmdRoot.Execute()
}
