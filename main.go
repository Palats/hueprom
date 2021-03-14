// Implements a Philips Hue to Promethus gateway
package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/amimof/huego"
	"github.com/davecgh/go-spew/spew"
	"github.com/golang/glog"
)

var (
	user = flag.String("user", "", "Hue username")
)

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

func main() {
	ctx := context.Background()
	flag.Parse()
	fmt.Println("Hueprom")

	bridge := huego.New("192.168.88.104", *user)
	/*l, err := bridge.GetLights()
	if err != nil {
		panic(err)
	}
	fmt.Printf("Found %d lights", len(l))
	spew.Dump(l)*/
	sensors, err := bridge.GetSensorsContext(ctx)
	if err != nil {
		glog.Exit(err)
	}
	// spew.Dump(sensors)

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
		s, err := bridge.GetSensorContext(ctx, sID)
		if err != nil {
			glog.Exit(err)
		}
		buttonevent := s.State["buttonevent"]
		lastupdated := s.State["lastupdated"]
		fmt.Printf("buttonevent: %v, lastupdated: %v\n", buttonevent, lastupdated)
		time.Sleep(time.Second)
	}
}
