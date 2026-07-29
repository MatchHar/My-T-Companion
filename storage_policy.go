package main

import (
	"sort"
	"time"
)

const (
	softwareDeliveredRetention    = 180 * 24 * time.Hour
	softwareDeliveredMaximum      = 1000
	chargingDeliveredRetention    = 14 * 24 * time.Hour
	chargingDeliveredMaximum      = 2000
	navigationDeliveredRetention  = 7 * 24 * time.Hour
	navigationDeliveredMaximum    = 2000
	chargingTransientMaximumAge   = 48 * time.Hour
	navigationTransientMaximumAge = 12 * time.Hour
)

type timestampedKey struct {
	key string
	at  time.Time
}

// pruneTimestampMap bounds delivery deduplication state by both age and count.
// Invalid timestamps are discarded instead of becoming immortal entries.
func pruneTimestampMap(values map[string]string, now time.Time, maximumAge time.Duration, maximumEntries int) {
	if values == nil {
		return
	}
	cutoff := now.Add(-maximumAge)
	kept := make([]timestampedKey, 0, len(values))
	for key, raw := range values {
		at, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil || at.Before(cutoff) {
			delete(values, key)
			continue
		}
		kept = append(kept, timestampedKey{key: key, at: at})
	}
	if len(kept) <= maximumEntries {
		return
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].at.After(kept[j].at) })
	for _, item := range kept[maximumEntries:] {
		delete(values, item.key)
	}
}

func timestampIsOlderThan(raw string, now time.Time, maximumAge time.Duration) bool {
	at, err := time.Parse(time.RFC3339Nano, raw)
	return err != nil || now.Sub(at) > maximumAge
}
