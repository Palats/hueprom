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
)

var (
	user = flag.String("user", "", "Hue username")
	port = flag.Int("port", 7362, "Port to serve on")
)

var (
	mSensorLastUpdated = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hue_sensor_lastupdated",
		Help: "Last update of the sense, in ms since epoch.",
	}, []string{"name", "uniqueid"})
)

func init() {
	prometheus.MustRegister(mSensorLastUpdated)
}

const timeFormat = "2006-01-02T15:04:05"

var indexTpl = template.Must(template.New("index").Parse(`
<html><body>
Philips Hue to Prometheus exporter.
</body></html>
`))

type Server struct {
	bridge *huego.Bridge
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

func (s *Server) loop(ctx context.Context) error {
	/*sensors, err := s.bridge.GetSensorsContext(ctx)
	if err != nil {
		return err
	}

	var sID int
	for _, s := range sensors {
		fmt.Printf("Sensor %d: %s [%s/%s]\n", s.ID, s.Name, s.Type, s.ModelID)
		if s.Name == "Hue dimmer switch 1" {
			sID = s.ID
			spew.Dump(s)
		}
	}
	if sID == 0 {
		glog.Exit("Sensor not found")
	}

	for {
		s, err := s.bridge.GetSensorContext(ctx, sID)
		if err != nil {
			glog.Exit(err)
		}
		buttonevent := s.State["buttonevent"]
		lastupdated := s.State["lastupdated"]
		fmt.Printf("buttonevent: %v, lastupdated: %v\n", buttonevent, lastupdated)
		time.Sleep(time.Second)
	}*/

	for {
		if err := s.step(ctx); err != nil {
			glog.Error(err)
		}

		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *Server) step(ctx context.Context) error {
	sensors, err := s.bridge.GetSensorsContext(ctx)
	if err != nil {
		return err
	}

	for _, s := range sensors {
		labels := prometheus.Labels{
			"name":     s.Name,
			"uniqueid": s.UniqueID,
		}
		lastupdated, ok := s.State["lastupdated"].(string)
		if !ok {
			glog.Errorf("unable to read %v as string", s.State["lastupdated"])
			continue
		}
		if lastupdated != "none" {
			t, err := time.Parse(timeFormat, lastupdated)
			if err != nil {
				glog.Errorf("unable to parse %q: %v\n", lastupdated, err)
				continue
			}
			mSensorLastUpdated.With(labels).Set(float64(t.UnixNano() / 1000))
		}
	}
	return nil
}

func find() {
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
		glog.Exit(err)
	}
	fmt.Println("user:", user)
	// bridge = bridge.Login(user)
	// light, _ := bridge.GetLight(3)
	// light.Off()
}

func dump(ctx context.Context) error {
	bridge := huego.New("192.168.88.104", *user)
	/*l, err := bridge.GetLights()
	if err != nil {
		panic(err)
	}
	fmt.Printf("Found %d lights", len(l))
	spew.Dump(l)*/
	sensors, err := bridge.GetSensorsContext(ctx)
	if err != nil {
		return err
	}
	spew.Dump(sensors)
	return nil
}

func serve(ctx context.Context) error {
	http.Handle("/metrics", promhttp.Handler())

	bridge := huego.New("192.168.88.104", *user)
	s := New(bridge)
	go func() {
		err := s.loop(ctx)
		glog.Exitf("Watching loop exited: %v", err)
	}()
	http.HandleFunc("/", s.Serve)

	addr := fmt.Sprintf(":%d", *port)
	hostname, err := os.Hostname()
	if err != nil {
		hostname = ""
	}
	fmt.Printf("Listening on http://%s%s\n", hostname, addr)
	return http.ListenAndServe(addr, nil)
}

func main() {
	ctx := context.Background()
	fmt.Println("Hueprom")
	flag.Parse()
	if err := serve(ctx); err != nil {
		glog.Error(err)
	}
}
