package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestPassportPositionSamplesKeepActualPointTimeAndBound(t *testing.T) {
	s := newPassportPositionSampler()
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	p := roadCoveragePoint{DriveID: 7, Lat: 43.123456, Lon: -79.123456}
	s.add(p, at)
	s.add(p, at.Add(time.Hour))
	p.DriveID = 8
	s.add(p, at.Add(2*time.Hour))
	if len(s.samples) != 2 || s.samples[0].Point[0] != 43.123456 || !s.samples[0].At.Equal(at) {
		t.Fatal("sample rounded to centroid, lost identity, or fabricated time")
	}
	for i := 0; i < 50010; i++ {
		s.add(roadCoveragePoint{DriveID: int64(i + 100), Lat: 43, Lon: -79}, at)
	}
	if len(s.samples) != 50000 || s.complete {
		t.Fatal("unbounded samples or hidden truncation")
	}
}

func TestPassportUsesAdministrativeIdentityAndDistinctDrives(t *testing.T) {
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	km := 12.0
	points := []roadPassportEndpoint{
		{drive: 1, at: at, distance: &km, country: "Canada", state: "Ontario", city: "London", addressID: 1},
		{drive: 1, at: at.Add(time.Hour), distance: &km, country: "Canada", state: "Ontario", city: "London", addressID: 2},
		{drive: 2, at: at.AddDate(0, 0, 1), country: "United Kingdom", state: "England", city: "London", addressID: 3},
	}
	p := buildRoadPassport(points, &km, true)
	if !p.Complete || len(p.Regions) != 6 {
		t.Fatalf("region count %d", len(p.Regions))
	}
	cities := 0
	ids := map[string]bool{}
	for _, r := range p.Regions {
		if ids[r.ID] {
			t.Fatal("duplicate stable identity")
		}
		ids[r.ID] = true
		if r.Kind == "city" {
			cities++
			if len(r.Visits) != 1 {
				t.Fatal("two endpoints double counted")
			}
		}
		if r.Kind == "city" && r.Country == "Canada" {
			if !r.First.Equal(at) || !r.Last.Equal(at.Add(time.Hour)) || !r.Visits[0].At.Equal(r.Last) {
				t.Fatal("endpoint timestamps changed")
			}
		}
	}
	if cities != 2 {
		t.Fatal("same city names merged across countries")
	}
}
func TestPassportUnknownContextNeverGuessesSameNamedCity(t *testing.T) {
	at := time.Now().UTC()
	p := buildRoadPassport([]roadPassportEndpoint{
		{drive: 1, at: at, city: "Springfield", addressID: 7}, {drive: 2, at: at, city: "Springfield", addressID: 8},
	}, nil, false)
	if p.Complete || len(p.Regions) != 2 || p.Regions[0].ID == p.Regions[1].ID {
		t.Fatal("missing admin context merged")
	}
	for _, r := range p.Regions {
		if r.Country != "" || r.State != "" {
			t.Fatal("invented hierarchy")
		}
	}
}
func TestPassportIdentityDoesNotExposeCoordinatesOrAmbiguousDelimiters(t *testing.T) {
	a := passportIdentity("city", "A|B", "C", "D", 1)
	b := passportIdentity("city", "A", "B|C", "D", 1)
	if a == b || strings.Contains(a, "|") {
		t.Fatal("ambiguous identity")
	}
	if passportIdentity("city", " CANADA ", "Ontario", "Toronto", 1) != passportIdentity("city", "canada", "ontario", "toronto", 2) {
		t.Fatal("known hierarchy depended on address")
	}
}
func TestPassportDatelineCenterAndInvalidEndpoints(t *testing.T) {
	at := time.Now().UTC()
	a, b := [2]float64{25, 179}, [2]float64{25, -179}
	p := buildRoadPassport([]roadPassportEndpoint{{drive: 1, at: at, country: "X", point: &a}, {drive: 2, at: at, country: "X", point: &b}, {drive: 0, at: at, country: "Invalid"}}, nil, true)
	if len(p.Regions) != 1 || p.Regions[0].Center == nil || p.Regions[0].Center[1] < 170 {
		t.Fatal("dateline mean crossed the world")
	}
}

func TestPassportLocalPostgresEndpointRangeAndCarIsolation(t *testing.T) {
	database := openFriendPostgresFixture(t)
	fixtureExec(t, database, `ALTER TABLE drives ADD COLUMN distance numeric, ADD COLUMN start_address_id integer, ADD COLUMN end_address_id integer, ADD COLUMN start_position_id bigint, ADD COLUMN end_position_id bigint;
CREATE TEMP TABLE addresses(id integer PRIMARY KEY,country text,state text,city text);
INSERT INTO cars VALUES(1),(2);
INSERT INTO addresses VALUES(1,'Canada','Ontario','London'),(2,'United Kingdom','England','London');
INSERT INTO drives(id,car_id,start_date,end_date,distance,start_address_id,end_address_id,start_position_id,end_position_id) VALUES
(10,1,'2026-10-01 12:00','2026-10-01 13:00',100,1,1,1,2),
(11,1,'2026-10-02 12:00','2026-10-03 12:00',400,1,2,3,4),
(12,2,'2026-10-02 12:00','2026-10-03 12:00',999,2,2,5,6);
INSERT INTO positions(id,car_id,drive_id,date,latitude,longitude) VALUES
(1,1,10,'2026-10-01 12:00',43,-81),(2,1,10,'2026-10-01 13:00',43.1,-81),
(3,1,11,'2026-10-02 12:00',43,-81),(4,1,11,'2026-10-03 12:00',51,-0.1),
(5,2,12,'2026-10-02 12:00',51,-0.1),(6,2,12,'2026-10-03 12:00',51.1,-0.1);`)
	p := fetchRoadPassport(context.Background(), database, roadCoverageRequest{CarID: 1})
	if p == nil || !p.Complete || p.LongestKM == nil || *p.LongestKM != 400 || len(p.Regions) != 6 {
		t.Fatalf("passport result=%+v", p)
	}
	for _, r := range p.Regions {
		if r.Kind == "city" && r.Country == "Canada" {
			if len(r.Visits) != 2 || r.Visits[0].DriveID != 11 || r.First.Format(time.RFC3339) != "2026-10-01T12:00:00Z" || r.Last.Format(time.RFC3339) != "2026-10-02T12:00:00Z" {
				t.Fatal("endpoint grouping/scanner timestamps")
			}
		}
		for _, v := range r.Visits {
			if v.DriveID == 12 {
				t.Fatal("other car leak")
			}
		}
	}
	from := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	selected := fetchRoadPassport(context.Background(), database, roadCoverageRequest{CarID: 1, From: &from, To: &to})
	if selected == nil {
		t.Fatal("half-open passport range query failed")
	}
	for _, r := range selected.Regions {
		if len(r.Visits) != 1 || r.Visits[0].DriveID != 11 {
			t.Fatal("range semantics differ from drive start filtering")
		}
	}
	fixtureExec(t, database, `ALTER TABLE addresses DROP COLUMN country`)
	if fetchRoadPassport(context.Background(), database, roadCoverageRequest{CarID: 1}) != nil {
		t.Fatal("missing optional schema fabricated metadata")
	}
	basic, err := fetchRoadCoverage(context.Background(), database, roadCoverageRequest{CarID: 1})
	if err != nil || basic.DriveCount != 2 || !basic.CitiesAvailable || basic.Passport != nil {
		t.Fatalf("optional failure discarded core data: %+v %v", basic, err)
	}
}
