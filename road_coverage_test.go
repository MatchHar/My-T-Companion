package main

import (
	"net/url"
	"testing"
)

func TestBuildRoadCoverageCountsDistinctDrivesPerCell(t *testing.T) {
	var pts []roadCoveragePoint
	for drive := int64(1); drive <= 3; drive++ {
		for i := 0; i < 50; i++ { // ~1.1 km north, 22 m steps
			pts = append(pts, roadCoveragePoint{DriveID: drive, Lat: 25.03 + float64(i)*0.0002, Lon: 121.56})
		}
	}
	// Drive 4 only covers the first cell, and revisits it twice in one drive.
	pts = append(pts, roadCoveragePoint{4, 25.03, 121.56}, roadCoveragePoint{4, 25.0301, 121.56}, roadCoveragePoint{4, 25.03, 121.56})
	pts = append(pts, roadCoveragePoint{5, 0, 0}) // invalid, ignored
	cells, trails, truncated := buildRoadCoverage(pts, 200, 60, 0)
	if truncated {
		t.Fatal("unexpected truncation")
	}
	if len(cells) < 5 || cells[0].Visits != 4 {
		t.Fatalf("cells=%d first=%+v", len(cells), cells[0])
	}
	for _, c := range cells[1:] {
		if c.Visits > 4 {
			t.Fatalf("visits counted per point instead of per drive: %+v", c)
		}
	}
	if len(trails) != 3 { // drive 4 decimates to one point; drive 5 invalid
		t.Fatalf("trails=%d", len(trails))
	}
	if trails[0].Mode != "unknown" || len(trails[0].Points) != len(trails[0].Heat) {
		t.Fatalf("trail=%+v", trails[0])
	}
	if n := len(trails[0].Points); n > 22 || n < 15 {
		t.Fatalf("decimation produced %d points", n)
	}
}

func TestBuildRoadCoverageCapsPoints(t *testing.T) {
	var pts []roadCoveragePoint
	for i := 0; i < 2000; i++ {
		pts = append(pts, roadCoveragePoint{1, 25 + float64(i)*0.001, 121})
	}
	_, trails, truncated := buildRoadCoverage(pts, 200, 60, 500)
	if !truncated || len(trails[0].Points) > 501 {
		t.Fatalf("truncated=%v points=%d", truncated, len(trails[0].Points))
	}
}

func TestParseRoadCoverageRequest(t *testing.T) {
	if _, err := parseRoadCoverageRequest("x", url.Values{}); err == nil {
		t.Fatal("bad car accepted")
	}
	q, err := parseRoadCoverageRequest("2", url.Values{"from": {"2026-01-01T00:00:00Z"}})
	if err != nil || q.CarID != 2 || q.From == nil || q.To != nil {
		t.Fatalf("%+v %v", q, err)
	}
	if _, err := parseRoadCoverageRequest("2", url.Values{"from": {"2026-02-01T00:00:00Z"}, "to": {"2026-01-01T00:00:00Z"}}); err == nil {
		t.Fatal("inverted range accepted")
	}
}

func TestRoadCoverageIsOwnerOnlyResource(t *testing.T) {
	if !ownerResourcePath.MatchString("cars/1/road-coverage") {
		t.Fatal("owner path missing")
	}
	if !roadCoveragePath.MatchString("/api/v1/cars/12/road-coverage") || roadCoveragePath.MatchString("/api/v1/cars/x/road-coverage") {
		t.Fatal("route regex")
	}
}
