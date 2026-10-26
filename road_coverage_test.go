package main

import (
	"context"
	"encoding/json"
	"math"
	"net/url"
	"testing"
	"time"
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
	if len(trails) != 4 { // short out-and-back drive 4 is preserved; drive 5 invalid
		t.Fatalf("trails=%d", len(trails))
	}
	if trails[0].Mode != "unknown" || len(trails[0].Points) != len(trails[0].Heat) {
		t.Fatalf("trail=%+v", trails[0])
	}
	if n := len(trails[0].Points); n > 5 || n < 2 {
		t.Fatalf("decimation produced %d points", n)
	}
}

func TestBuildRoadCoverageCapsPoints(t *testing.T) {
	var pts []roadCoveragePoint
	for i := 0; i < 2000; i++ {
		pts = append(pts, roadCoveragePoint{1, 25 + float64(i)*0.001, 121})
	}
	_, trails, truncated := buildRoadCoverage(pts, 200, 60, 50)
	if !truncated || len(trails[0].Points) > 50 {
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

func TestRoadCoverageStrictBudgetAcrossManyShortTrips(t *testing.T) {
	var points []roadCoveragePoint
	for drive := int64(1); drive <= 1000; drive++ {
		points = append(points, roadCoveragePoint{drive, 25, 121}, roadCoveragePoint{drive, 25.001, 121})
	}
	_, trails, truncated := buildRoadCoverage(points, 200, 60, 51)
	count := 0
	for _, trail := range trails {
		count += len(trail.Points)
		if len(trail.Points) < 2 {
			t.Fatal("singleton trail")
		}
	}
	if !truncated || count > 51 {
		t.Fatalf("points=%d truncated=%v", count, truncated)
	}
}

func TestRoadCoveragePreservesFinalPointAndBend(t *testing.T) {
	points := []roadCoveragePoint{{1, 25, 121}, {1, 25.0004, 121}, {1, 25.0004, 121.0004}, {1, 25.0005, 121.0004}}
	_, trails, _ := buildRoadCoverage(points, 200, 60, 80000)
	if len(trails) != 1 {
		t.Fatalf("trails=%d", len(trails))
	}
	p := trails[0].Points
	if p[0] != [2]float64{25, 121} || p[len(p)-1] != [2]float64{25.0005, 121.0004} {
		t.Fatalf("endpoints=%v", p)
	}
	if len(p) < 3 {
		t.Fatalf("corner shortcut=%v", p)
	}
}

func TestRoadCoverageInvalidPointAndLongGapSplit(t *testing.T) {
	points := []roadCoveragePoint{{1, 25, 121}, {1, 25.001, 121}, {1, math.NaN(), 121}, {1, 25.002, 121}, {1, 25.003, 121}, {1, 26, 121}, {1, 26.001, 121}}
	_, trails, _ := buildRoadCoverage(points, 200, 60, 80000)
	if len(trails) != 3 {
		t.Fatalf("bridged invalid or distance gap: trails=%d", len(trails))
	}
	for _, trail := range trails {
		for i := 1; i < len(trail.Points); i++ {
			a, b := trail.Points[i-1], trail.Points[i]
			if haversineM(a[0], a[1], b[0], b[1]) > 5000 {
				t.Fatal("bridged unknown area")
			}
		}
	}
}

func TestRoadCoverageTimeGapSplit(t *testing.T) {
	b := newRoadCoverageBuilder(200, 60, 80000)
	now := time.Unix(100000, 0)
	b.add(roadCoveragePoint{1, 25, 121}, now)
	b.add(roadCoveragePoint{1, 25.001, 121}, now.Add(time.Second*10))
	b.add(roadCoveragePoint{1, 25.002, 121}, now.Add(time.Minute*4))
	b.add(roadCoveragePoint{1, 25.003, 121}, now.Add(time.Minute*4+time.Second*10))
	_, trails, _ := b.finish()
	if len(trails) != 2 {
		t.Fatalf("time gap bridged: %d", len(trails))
	}
}

func TestRoadCoverageDescendingDriveOrderCountsVisits(t *testing.T) {
	points := []roadCoveragePoint{{3, 25, 121}, {3, 25.001, 121}, {2, 25, 121}, {2, 25.001, 121}, {1, 25, 121}, {1, 25.001, 121}}
	cells, trails, _ := buildRoadCoverage(points, 200, 60, 80000)
	if len(cells) == 0 || cells[0].Visits != 3 || len(trails) != 3 {
		t.Fatalf("cells=%v trails=%d", cells, len(trails))
	}
	for _, trail := range trails {
		for _, heat := range trail.Heat {
			if heat < 1 {
				t.Fatal("unpaired heat")
			}
		}
	}
}

func TestRoadCoverageSimplificationErrorIsBounded(t *testing.T) {
	points := make([]roadCoveragePoint, 200)
	for i := range points {
		points[i] = roadCoveragePoint{1, 25 + float64(i)*.00001, 121 + .0003*math.Sin(float64(i)/20)}
	}
	reduced := simplifyRoadPoints(points, 10)
	for _, p := range points {
		nearest := math.Inf(1)
		for i := 1; i < len(reduced); i++ {
			nearest = math.Min(nearest, roadSegmentDistance(p, reduced[i-1], reduced[i]))
		}
		if nearest > 10.0001 {
			t.Fatalf("error=%.2fm", nearest)
		}
	}
}

func TestRoadCoverageStreamingRawChunkIsBounded(t *testing.T) {
	b := newRoadCoverageBuilder(200, 60, 100)
	for i := 0; i < 100000; i++ {
		b.add(roadCoveragePoint{1, 25 + float64(i%100)*.000001, 121}, time.Time{})
		if len(b.pending) > 4096 {
			t.Fatal("unbounded raw points")
		}
	}
	_, trails, _ := b.finish()
	n := 0
	for _, trail := range trails {
		n += len(trail.Points)
	}
	if n > 100 {
		t.Fatalf("cap=%d", n)
	}
}

// Executes the actual production SQL and row scanner against a disposable,
// explicitly enabled loopback PostgreSQL fixture. No production DATABASE_* vars.
func TestRoadCoverageLocalPostgresRangeNullGPSAndPrivacy(t *testing.T) {
	database := openFriendPostgresFixture(t)
	fixtureExec(t, database, `ALTER TABLE drives ADD COLUMN distance numeric, ADD COLUMN start_address_id integer, ADD COLUMN end_address_id integer, ADD COLUMN start_position_id bigint, ADD COLUMN end_position_id bigint;
CREATE TEMP TABLE addresses(id integer PRIMARY KEY, city text);
INSERT INTO cars VALUES(1),(2);
INSERT INTO addresses VALUES(1,'Ottawa'),(2,'Kingston');
INSERT INTO drives(id,car_id,start_date,end_date,distance,start_address_id,end_address_id,start_position_id,end_position_id) VALUES
(1,1,'2026-10-01 01:00','2026-10-01 02:00',10,1,2,1,4),
(2,1,'2026-10-02 01:00','2026-10-02 02:00',20,1,2,5,6),
(3,1,'2026-10-03 01:00','2026-10-03 02:00',30,1,2,7,8),
(4,2,'2026-10-02 01:00','2026-10-02 02:00',999,1,2,9,10);
INSERT INTO positions(id,car_id,drive_id,date,latitude,longitude) VALUES
(1,1,1,'2026-10-01 01:00:00',45,-75),(2,1,1,'2026-10-01 01:00:10',45.001,-75),
(3,1,1,'2026-10-01 01:00:20',NULL,NULL),(4,1,1,'2026-10-01 01:00:30',45.002,-75),
(5,1,2,'2026-10-02 01:00:00',45,-75),(6,1,2,'2026-10-02 01:00:10',45.001,-75),
(7,1,3,'2026-10-03 01:00:00',45,-75),(8,1,3,'2026-10-03 01:00:10',45.001,-75),
(9,2,4,'2026-10-02 01:00:00',60,20),(10,2,4,'2026-10-02 01:00:10',60.001,20);`)
	all, err := fetchRoadCoverage(context.Background(), database, roadCoverageRequest{CarID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !all.CellsComplete || all.DriveCount != 3 || all.DistanceKM != 60 || len(all.Trails) != 3 || !all.PrivacyAnchorsAvailable || !all.CitiesAvailable || len(all.Cities) != 2 || len(all.Anchors) == 0 {
		t.Fatalf("actual SQL/scanner result=%+v", all)
	}
	light, err := fetchRoadCoverage(context.Background(), database, roadCoverageRequest{CarID: 1, PassportOnly: true})
	if err != nil || !light.CellsComplete || light.GeometryIncluded || len(light.Trails) != 0 || light.Truncated || light.OmittedTrails != 0 {
		t.Fatalf("lightweight result=%+v %v", light, err)
	}
	if light.DriveCount != all.DriveCount || light.DistanceKM != all.DistanceKM || len(light.Cells) != len(all.Cells) {
		t.Fatal("lightweight changed recorded statistics")
	}
	for i, c := range light.Cells {
		if c != all.Cells[i] {
			t.Fatal("lightweight changed the geographic grid or distinct-drive heat")
		}
	}
	// Null GPS terminates drive1; its isolated final point cannot form a bridge.
	for _, trail := range all.Trails {
		for _, point := range trail.Points {
			if point[0] > 46 {
				t.Fatal("other car leaked")
			}
		}
	}
	from := time.Date(2026, 10, 2, 1, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC)
	selected, err := fetchRoadCoverage(context.Background(), database, roadCoverageRequest{CarID: 1, From: &from, To: &to})
	if err != nil || selected.DriveCount != 1 || selected.DistanceKM != 20 || len(selected.Trails) != 1 || selected.Trails[0].DriveID != 2 {
		t.Fatalf("half-open drive start range: %+v err=%v", selected, err)
	}
	fixtureExec(t, database, `DROP TABLE addresses`)
	partial, err := fetchRoadCoverage(context.Background(), database, roadCoverageRequest{CarID: 1})
	if err != nil || partial.CitiesAvailable || !partial.PrivacyAnchorsAvailable || len(partial.Trails) == 0 {
		t.Fatalf("optional city schema: %+v err=%v", partial, err)
	}
}

func TestRoadCoveragePassportViewRequest(t *testing.T) {
	q, err := parseRoadCoverageRequest("1", url.Values{"view": {"passport"}})
	if err != nil || !q.PassportOnly {
		t.Fatal("passport option lost")
	}
	if _, err := parseRoadCoverageRequest("1", url.Values{"view": {"other"}}); err == nil {
		t.Fatal("unknown view silently accepted")
	}
}

func TestRoadCoverageCellCompletenessIsExplicitAndIndependentOfRouteOmission(t *testing.T) {
	unknown := roadCoverageData{}
	if unknown.CellsComplete {
		t.Fatal("unread grid cannot be complete")
	}
	complete := roadCoverageData{CellsComplete: true, Truncated: true, OmittedTrails: 12}
	data, err := json.Marshal(complete)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err = json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["cells_complete"] != true || wire["truncated"] != true {
		t.Fatal("grid completeness must not be inferred from route truncation", wire)
	}
}
