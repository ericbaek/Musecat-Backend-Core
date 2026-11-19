package arcadearea

import (
	"strings"
	"testing"

	"github.com/ericbaek/musecat-backend-core/geo"
)

// A two-city fixture where XX-1/Old was renamed to XX-1/New.
const fixture = `{"type":"FeatureCollection","version":"fixture-v2","whole_country":[],
"renames":{"XX-1/Old":"XX-1/New"},
"admin1":{"XX-1":{"country":"XX","names":{"local":"One"},"aliases":["Province One"]},
          "XX-2":{"country":"XX","names":{"local":"Two"},"aliases":["Province Two"]}},
"cities":{"XX-1/New":{"country":"XX","admin1":"XX-1","names":{"local":"New"},"lat":0.5,"lon":0.5},
          "XX-2/Other":{"country":"XX","admin1":"XX-2","names":{"local":"Other"},"lat":0.5,"lon":1.5}},
"features":[
 {"type":"Feature","properties":{"country":"XX","admin1":"XX-1","admin2":"a","name":"A","city":"XX-1/New"},
  "geometry":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,1],[0,0]]]}},
 {"type":"Feature","properties":{"country":"XX","admin1":"XX-2","admin2":"b","name":"B","city":"XX-2/Other"},
  "geometry":{"type":"Polygon","coordinates":[[[1,0],[2,0],[2,1],[1,1],[1,0]]]}}]}`

func TestDeriveFollowsDatasetChanges(t *testing.T) {
	areas, err := geo.DecodeAreas(strings.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	loc := Location{Country: "XX", Lat: 0.5, Lon: 0.5, Valid: true, Address: "12 Main St, Province Two"}
	got := Derive(areas, loc, nil)
	if got.City != "XX-1/New" || got.Admin1 != "XX-1" || got.Status != geo.AreaResolved || !got.Mismatch || got.Version != "fixture-v2" {
		t.Fatalf("derived %+v", got)
	}
	// A manual pin on a renamed key follows the rename and keeps its area.
	pinned := Assignment{Admin1: "XX-1", City: "XX-1/Old", Status: geo.AreaResolved, Version: "fixture-v1"}
	loc.Address = "Province One"
	if got := Derive(areas, loc, &pinned); got.City != "XX-1/New" || got.Admin1 != "XX-1" || got.Mismatch || got.Version != "fixture-v2" {
		t.Fatalf("manual %+v", got)
	}
	// A retired key stays as stored for review rather than silently moving.
	retired := Assignment{Admin1: "XX-2", City: "XX-2/Gone", Status: geo.AreaResolved}
	if got := Derive(areas, loc, &retired); got.City != "XX-2/Gone" || !got.Mismatch {
		t.Fatalf("retired %+v", got)
	}
	if got := Derive(areas, Location{Country: "XX"}, nil); got.Status != geo.AreaUnmapped || got.Version != "fixture-v2" {
		t.Fatalf("no location %+v", got)
	}
}

func TestDecodeAreasRejectsBrokenRenames(t *testing.T) {
	broken := strings.Replace(fixture, `"XX-1/Old":"XX-1/New"`, `"XX-1/Old":"XX-1/Missing"`, 1)
	if _, err := geo.DecodeAreas(strings.NewReader(broken)); err == nil {
		t.Fatal("rename to an unknown city accepted")
	}
	unversioned := strings.Replace(fixture, `"version":"fixture-v2",`, "", 1)
	if _, err := geo.DecodeAreas(strings.NewReader(unversioned)); err == nil {
		t.Fatal("dataset without version accepted")
	}
}
