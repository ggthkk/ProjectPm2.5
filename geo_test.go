package main

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWaterUsesMapRadiusAcrossProvinceBorders(t *testing.T) {
	now := time.Now()
	c := testConfig(t)
	c.Locations, c.WaterRadiusKM = "13.8,100.7", 10
	level, msl := 4, "1.5"
	makeRow := func(id int, lat, lon float64, province string) WaterRow {
		r := WaterRow{Level: &level, MSL: &msl, Time: now.In(c.Location).Format("2006-01-02 15:04")}
		r.Station.ID, r.Station.Lat, r.Station.Lon = id, &lat, &lon
		r.Geo.ProvinceCode = province
		return r
	}
	near := makeRow(1, 13.81, 100.71, "99") // Proximity takes precedence over province defaults.
	far := makeRow(2, 14.8, 100.7, "10")
	missing := makeRow(3, 13.8, 100.7, "10")
	missing.Station.Lat = nil
	rows := []WaterRow{near, far, missing}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"waterlevel_data": map[string]any{"data": rows}})
	}))
	defer server.Close()
	c.WaterURL = server.URL
	got, err := fetchWater(context.Background(), server.Client(), c, now)
	if err != nil || len(got) != 1 || got[0].Station.ID != 1 || got[0].DistanceKM > 10 {
		t.Fatal(got, err)
	}
	c.Locations = "0,0"
	got, err = fetchWater(context.Background(), server.Client(), c, now)
	if err != nil || len(got) != 0 {
		t.Fatal("must not fall back to faraway provinces", got, err)
	}
}

func TestAirUsesRequestedCoordinatesAndLabelsCitySource(t *testing.T) {
	c := testConfig(t)
	c.APIKey, c.Locations = "test-only", "13.8,100.7"
	now := time.Now().UTC()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("lat") != "13.800000" || r.URL.Query().Get("lon") != "100.700000" {
			t.Error("requested MAP was not sent to IQAir")
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"city": "Source city", "state": "Source state", "location": map[string]any{"coordinates": []float64{100.71, 13.81}}, "current": map[string]any{"pollution": map[string]any{"aqius": 42, "ts": now}}}})
	}))
	defer server.Close()
	c.AirURL = server.URL
	e, err := fetchAir(context.Background(), server.Client(), c, now)
	if err != nil || len(e.Fields) != 1 || !strings.Contains(e.Fields[0].Name, "ข้อมูลเมือง Source city") || !strings.Contains(e.Fields[0].Value, "1.5 กม.") {
		t.Fatal(e, err)
	}
	if math.Abs(distanceKM(Coordinates{0, 0}, Coordinates{0, 1})-111.195) > 0.01 {
		t.Fatal("incorrect distance scale")
	}
}
