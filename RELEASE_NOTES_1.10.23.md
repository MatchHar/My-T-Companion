# My T Companion 1.10.23

- Docker Mosquitto that is Restarting or Exited is no longer treated as a live broker.
- HostBox-native VPS where `eclipse-mosquitto` cannot open its bind-mounted config fall back to the host broker on `:1883` (`host.docker.internal`).
- Stops re-applying `tcp://mosquitto:1883` from a previous HostBox deploy when that name would crash TeslaMateAPI.
- TeslaMate charges and trips still work if Companion is off or MQTT is on the host.
