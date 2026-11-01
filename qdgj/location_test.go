package qdgj

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func locationNumber(value float64) *Number {
	number := Number(value)
	return &number
}

func locationDistance(a, b Location) float64 {
	latitudeDelta := (b.Latitude - a.Latitude) * math.Pi / 180
	longitudeDelta := (b.Longitude - a.Longitude) * math.Pi / 180
	h := math.Pow(math.Sin(latitudeDelta/2), 2) + math.Cos(a.Latitude*math.Pi/180)*math.Cos(b.Latitude*math.Pi/180)*math.Pow(math.Sin(longitudeDelta/2), 2)
	return 2 * 6371008.8 * math.Asin(math.Sqrt(math.Min(1, h)))
}

func TestRandomAllowedLocationStaysWithinRadius(t *testing.T) {
	for name, center := range map[string]Location{
		"ordinary":   {Longitude: 10.5, Latitude: 20.5},
		"date line":  {Longitude: 179.999, Latitude: 20},
		"north pole": {Longitude: 0, Latitude: 90},
		"south pole": {Longitude: 0, Latitude: -90},
	} {
		t.Run(name, func(t *testing.T) {
			place := AllowedLocation{Address: " fixture building ", Center: " fixture campus ", Longitude: locationNumber(center.Longitude), Latitude: locationNumber(center.Latitude), Radius: locationNumber(1000)}
			seen := make(map[Location]bool)
			for range 256 {
				point, err := randomAllowedLocation([]AllowedLocation{place})
				if err != nil || point.validateCoordinates() != nil {
					t.Fatalf("invalid sampled location: %+v (%v)", point, err)
				}
				if point.Address != "" {
					t.Fatalf("sample must resolve its address from coordinates, got %q", point.Address)
				}
				distance := locationDistance(center, point)
				if math.IsNaN(distance) || distance > 900.001 {
					t.Fatalf("sample outside radius margin: %.6f meters", distance)
				}
				seen[point] = true
			}
			if len(seen) < 2 {
				t.Fatal("sampled coordinates did not vary")
			}
		})
	}
}

func TestRandomAllowedLocationRejectsIncompletePlaces(t *testing.T) {
	valid := AllowedLocation{Address: "fixture", Longitude: locationNumber(10.5), Latitude: locationNumber(20.5), Radius: locationNumber(1000)}
	invalid := []AllowedLocation{{}}
	for _, change := range []func(*AllowedLocation){
		func(p *AllowedLocation) { p.Longitude = nil },
		func(p *AllowedLocation) { p.Latitude = nil },
		func(p *AllowedLocation) { p.Radius = nil },
		func(p *AllowedLocation) { p.Longitude = locationNumber(181) },
		func(p *AllowedLocation) { p.Latitude = locationNumber(-91) },
		func(p *AllowedLocation) { p.Longitude = locationNumber(math.NaN()) },
		func(p *AllowedLocation) { p.Latitude = locationNumber(math.Inf(1)) },
		func(p *AllowedLocation) { p.Radius = locationNumber(0) },
		func(p *AllowedLocation) { p.Radius = locationNumber(-1) },
		func(p *AllowedLocation) { p.Radius = locationNumber(math.NaN()) },
		func(p *AllowedLocation) { p.Radius = locationNumber(math.Inf(1)) },
		func(p *AllowedLocation) { p.Radius = locationNumber(1e10) },
	} {
		place := valid
		change(&place)
		invalid = append(invalid, place)
		if _, err := randomAllowedLocation([]AllowedLocation{place}); err == nil {
			t.Errorf("invalid place accepted: %+v", place)
		}
	}
	if _, err := randomAllowedLocation(nil); err == nil {
		t.Fatal("empty places accepted")
	}
	valid.Address, valid.Center = "", ""
	point, err := randomAllowedLocation(append(invalid, valid))
	if err != nil || point.Address != "" || point.validateCoordinates() != nil {
		t.Fatalf("complete place not selected: %+v (%v)", point, err)
	}
}

func TestSignInWithAllowedLocationRefreshesDetailAndSubmitsOnce(t *testing.T) {
	queries, submissions := 0, 0
	var previous Location
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/qdgj/" + queryPath:
			assertAuth(t, r, queryPath)
			queries++
			var query taskQuery
			if err := json.NewDecoder(r.Body).Decode(&query); err != nil || query.TaskID != "task" || query.BatchID != "batch" || query.Pagination != nil {
				t.Errorf("wrong detail query: %+v (%v)", query, err)
			}
			fmt.Fprintf(w, `{"code":200,"data":{"list":[{"rwid":"task","pcid":"batch","qddds":[{"qddd":"fixture building","qdzx":"fixture campus","jdzb":%d,"wdzb":20.5,"yxbj":1000}]}]}}`, 10+queries)
		case "/qdgj/" + signPath:
			assertAuth(t, r, signPath)
			submissions++
			var body signPayload
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			point := Location{Address: body.Address, Longitude: body.Longitude, Latitude: body.Latitude}
			center := Location{Longitude: float64(10 + queries), Latitude: 20.5}
			if point.Address != fixtureMapAddress(point.Longitude, point.Latitude) || locationDistance(center, point) > 900.001 || body.UserID != "fixture-user" || body.TaskID != "task" || body.BatchID != "batch" {
				t.Error("sign payload did not use current allowed place")
			}
			if point == previous {
				t.Error("sign payload reused previous coordinates")
			}
			previous = point
			if submissions == 1 {
				fmt.Fprint(w, `{"code":200,"data":{}}`)
			} else {
				fmt.Fprint(w, `{"code":409,"meta":{"message":"already signed"}}`)
			}
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := restoredGeocodingClient(t, server.URL)
	request := SignInRequest{TaskID: "task", BatchID: "batch", UseAllowedLocation: true}
	if _, err := client.SignIn(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if queries != 1 || submissions != 1 {
		t.Fatalf("wrong request count: queries=%d submissions=%d", queries, submissions)
	}
	_, err := client.SignIn(context.Background(), request)
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.Code != 409 || queries != 2 || submissions != 2 {
		t.Fatalf("failure retried or detail not refreshed: queries=%d submissions=%d err=%v", queries, submissions, err)
	}
}

func TestSignInAllowedLocationQueryFailureStopsSubmission(t *testing.T) {
	for _, response := range []string{
		`{"code":503,"meta":{"message":"fixture unavailable"}}`,
		`{"code":200,"data":{"list":[]}}`,
		`{"code":200,"data":{"list":[{"rwid":"task","pcid":"batch","qddds":[]}]}}`,
		`{"code":200,"data":{"list":[{"rwid":"task","pcid":"batch","qddds":[{"qddd":"fixture","jdzb":10,"wdzb":20}]}]}}`,
	} {
		t.Run(response, func(t *testing.T) {
			queries := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/qdgj/"+queryPath {
					t.Errorf("failed location lookup submitted sign: %s", r.URL.Path)
				}
				queries++
				fmt.Fprint(w, response)
			}))
			defer server.Close()
			client := restoredClient(t, server.URL)
			_, err := client.SignIn(context.Background(), SignInRequest{TaskID: "task", BatchID: "batch", UseAllowedLocation: true})
			if err == nil || queries != 1 {
				t.Fatalf("lookup failure ignored: queries=%d err=%v", queries, err)
			}
		})
	}
}
