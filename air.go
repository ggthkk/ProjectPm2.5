package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Coordinates struct{ Lat, Lon float64 }

func parseLocations(raw string) ([]Coordinates, error) {
	var out []Coordinates
	for _, s := range strings.Split(raw, ";") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		p := strings.Split(s, ",")
		if len(p) != 2 {
			return nil, fmt.Errorf("MAP must be lat,lon;lat,lon")
		}
		lat, e1 := strconv.ParseFloat(strings.TrimSpace(p[0]), 64)
		lon, e2 := strconv.ParseFloat(strings.TrimSpace(p[1]), 64)
		if e1 != nil || e2 != nil || !(lat >= -90 && lat <= 90) || !(lon >= -180 && lon <= 180) {
			return nil, fmt.Errorf("invalid coordinates in MAP")
		}
		out = append(out, Coordinates{lat, lon})
	}
	if len(out) == 0 || len(out) > 10 {
		return nil, fmt.Errorf("MAP must contain 1–10 locations")
	}
	return out, nil
}

type AirQualityResponse struct {
	Status string `json:"status"`
	Data   struct {
		City    string `json:"city"`
		State   string `json:"state"`
		Country string `json:"country"`
		Current struct {
			Pollution struct {
				AQIUS  *int      `json:"aqius"`
				MainUS string    `json:"mainus"`
				TS     time.Time `json:"ts"`
			} `json:"pollution"`
			Weather struct {
				Tp *int      `json:"tp"`
				Hu *int      `json:"hu"`
				Ws *float64  `json:"ws"`
				Wd *int      `json:"wd"`
				Ic string    `json:"ic"`
				TS time.Time `json:"ts"`
			} `json:"weather"`
		} `json:"current"`
	} `json:"data"`
}

func decodeAir(data []byte, now time.Time) (AirQualityResponse, error) {
	var r AirQualityResponse
	if json.Unmarshal(data, &r) != nil {
		return r, fmt.Errorf("invalid IQAir response")
	}
	p := r.Data.Current.Pollution
	if r.Status != "success" || r.Data.City == "" || p.AQIUS == nil || *p.AQIUS < 0 || p.TS.IsZero() {
		return r, fmt.Errorf("IQAir data unavailable (no valid observation)")
	}
	if !fresh(p.TS, now, 6*time.Hour) {
		return r, fmt.Errorf("IQAir observation is stale")
	}
	return r, nil
}
func interpretAQI(aqi int) string {
	switch {
	case aqi <= 50:
		return "ดี"
	case aqi <= 100:
		return "ปานกลาง"
	case aqi <= 150:
		return "มีผลต่อกลุ่มเสี่ยง"
	case aqi <= 200:
		return "มีผลต่อสุขภาพ"
	case aqi <= 300:
		return "มีผลต่อสุขภาพมาก"
	default:
		return "อันตราย"
	}
}
func interpretWeatherIcon(ic string) string {
	switch {
	case strings.HasPrefix(ic, "01"):
		return "ท้องฟ้าแจ่มใส"
	case strings.HasPrefix(ic, "02"):
		return "มีเมฆบางส่วน"
	case strings.HasPrefix(ic, "03"), strings.HasPrefix(ic, "04"):
		return "มีเมฆมาก"
	case strings.HasPrefix(ic, "09"), strings.HasPrefix(ic, "10"):
		return "มีฝน"
	case strings.HasPrefix(ic, "11"):
		return "ฝนฟ้าคะนอง"
	case strings.HasPrefix(ic, "13"):
		return "หิมะ"
	case strings.HasPrefix(ic, "50"):
		return "หมอก"
	default:
		return "ไม่มีข้อมูลสภาพอากาศ"
	}
}
func fetchAir(ctx context.Context, client *http.Client, c Config, now time.Time) (Embed, error) {
	if c.APIKey == "" || c.Locations == "" {
		return Embed{}, fmt.Errorf("IQAir needs API_KEY and MAP")
	}
	locations, err := parseLocations(c.Locations)
	if err != nil {
		return Embed{}, err
	}
	var lines []string
	seen := map[string]bool{}
	for i, p := range locations {
		q := url.Values{"lat": {strconv.FormatFloat(p.Lat, 'f', 6, 64)}, "lon": {strconv.FormatFloat(p.Lon, 'f', 6, 64)}, "key": {c.APIKey}}
		data, err := get(ctx, client, c.AirURL+"?"+q.Encode())
		var r AirQualityResponse
		if err == nil {
			r, err = decodeAir(data, now)
		}
		if err != nil {
			lines = append(lines, fmt.Sprintf("พื้นที่ %d: ข้อมูลไม่พร้อม", i+1))
			continue
		}
		city := r.Data.City + ", " + r.Data.State
		if seen[city] {
			continue
		}
		seen[city] = true
		p := r.Data.Current.Pollution
		w := r.Data.Current.Weather
		line := fmt.Sprintf("**%s**\nAQI US **%d · %s**", safeText(city), *p.AQIUS, interpretAQI(*p.AQIUS))
		if w.Tp != nil && fresh(w.TS, now, 6*time.Hour) {
			line += fmt.Sprintf(" · %d°C · %s", *w.Tp, interpretWeatherIcon(w.Ic))
		}
		line += "\nตรวจวัด " + p.TS.In(c.Location).Format("02/01 15:04") + " น."
		lines = append(lines, line)
	}
	return Embed{Title: "🌤 อากาศและคุณภาพอากาศ", Description: short(strings.Join(lines, "\n\n")+"\n\nแหล่งข้อมูล: IQAir · ค่าปัจจุบัน ไม่ใช่พยากรณ์ฝน", 1400), URL: "https://www.iqair.com/", Color: 0x56B4E9}, nil
}
