// friend-together-fixture is a loopback-only, synthetic interoperability server.
// It does not import the production main package, connect to TeslaMate/MQTT or
// accept real credentials. It is never run by the Companion installer.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	ft "mycarmate-states-api/internal/friendtogether"
)

type fixture struct {
	lane    int
	started time.Time
}

func (f fixture) OwnsCar(_ context.Context, id int64) (bool, error) { return id == 1, nil }
func (f fixture) ReadSource(_ context.Context, key ft.SourceKey, consent int64, p ft.PermissionSet) (ft.SourceSnapshot, error) {
	now := time.Now()
	at := now.UnixMilli()
	seconds := now.Sub(f.started).Seconds()
	lat := 43.65 + math.Mod(seconds+float64(f.lane*15), 600)*0.000005
	lon := -79.38
	speed, heading := 36.0, 0.0
	out := ft.SourceSnapshot{Source: key, State: ft.StateDriving, Motion: &ft.Motion{Latitude: &lat, Longitude: &lon, SpeedKPH: &speed, HeadingDegrees: &heading, ObservedAtMS: &at}}
	if p.Battery {
		soc, rangeKM := 75.0-float64(f.lane), 360.0-float64(f.lane*5)
		out.Battery = &ft.Battery{Percentage: &soc, RatedRangeKM: &rangeKM, ObservedAtMS: at}
	}
	if p.Navigation {
		dlat, dlon, km, minutes := 43.69, -79.38, 6.0, 12.0
		out.Navigation = &ft.NavigationObservation{Revision: 1, Latitude: &dlat, Longitude: &dlon, RemainingDistanceKM: &km, RemainingMinutes: &minutes, ObservedAtMS: at}
	}
	if p.Trajectory {
		out.Trajectory = []ft.TrajectoryPoint{{Latitude: lat, Longitude: lon, ObservedAtMS: at}}
	}
	return out, nil
}
func main() {
	address := flag.String("listen", "127.0.0.1:18961", "literal loopback address only")
	lane := flag.Int("lane", 0, "synthetic vehicle offset (0 or 1)")
	flag.Parse()
	host, _, err := net.SplitHostPort(*address)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() || *lane < 0 || *lane > 1 {
		log.Fatal("fixture requires literal loopback address and lane 0 or 1")
	}
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		log.Fatal("fixture listen failed")
	}
	stateDir, err := os.MkdirTemp("", "myt-friend-fixture-")
	if err != nil {
		log.Fatal("fixture temporary state failed")
	}
	defer os.RemoveAll(stateDir)
	origin := "http://" + listener.Addr().String()
	service, err := ft.NewHTTPService(ft.HTTPConfig{Enabled: true, GuestOrigin: origin, StatePath: filepath.Join(stateDir, "state.json"), AllowLoopbackHTTP: true, Source: fixture{lane: *lane, started: time.Now()}, AuthenticateOwner: func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer myt-local-fixture-only" }})
	if err != nil {
		log.Fatal("fixture service failed")
	}
	mux := http.NewServeMux()
	mux.Handle("/api/v1/friend-together/", service.OwnerHandler())
	mux.Handle("/friend/v1/", service.GuestHandler())
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() { <-ctx.Done(); _ = server.Close() }()
	fmt.Printf("Synthetic Friend Together fixture only: %s (car 1, lane %d)\n", origin, *lane)
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Fatal("fixture server failed")
	}
}
