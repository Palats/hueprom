# Philips Hue Prometheus exporter

This exports a handful of metrics about Philips Hue lights and sensors for Prometheus. It also generates sort of events for button press.

Metrics are polled regularly from the discovered Hue bridge. Metrics:
 - `hue_sensor_lastupdated`: Milliseconds since epoch.
 - `hue_sensor_buttonevent`: Set with the state of the buttonevent field of Hue API.
 - `hue_sensor_on`: 0/1
 - `hue_sensor_reachable`: 0/1, is the sensor visible from the Bridge network.
 - `hue_button_click`: Increased every time an event with Button&2 is seen.
 - `hue_light_on`: 0/1. based on the light state.
 - `hue_light_reachable`: 0/1, Is the light visible from the Bridge network.

All metrics are labelled by the `name` or `uniqueid` of each lights/sensors, as provided by the bridge.