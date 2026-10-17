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
		City     string `json:"city"`
		State    string `json:"state"`
		Country  string `json:"country"`
		Location struct {
			Coordinates []float64 `json:"coordinates"`
		} `json:"location"`
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
	e := Embed{Title: "☀️ วันนี้ใกล้คุณ · " + now.In(c.Location).Format("02/01"), Description: "ค่าตรวจวัดจากเมืองใกล้คุณ · IQAir", URL: "https://www.iqair.com/", Color: 0x56B4E9}
	for i, p := range locations {
		q := url.Values{"lat": {strconv.FormatFloat(p.Lat, 'f', 6, 64)}, "lon": {strconv.FormatFloat(p.Lon, 'f', 6, 64)}, "key": {c.APIKey}}
		data, err := get(ctx, client, c.AirURL+"?"+q.Encode())
		var r AirQualityResponse
		if err == nil {
			r, err = decodeAir(data, now)
		}
		if err != nil {
			e.Fields = append(e.Fields, EmbedField{Name: fmt.Sprintf("พื้นที่ %d", i+1), Value: "ข้อมูลไม่พร้อม"})
			continue
		}
		city := r.Data.City + ", " + r.Data.State
		p := r.Data.Current.Pollution
		w := r.Data.Current.Weather
		weather := ""
		if w.Tp != nil && fresh(w.TS, now, 6*time.Hour) {
			weather = fmt.Sprintf("\n🌡 **%d°C** · %s", *w.Tp, interpretWeatherIcon(w.Ic))
		}
		field := airField(city, *p.AQIUS, weather, p.TS, c.Location)
		field.Name = short(fmt.Sprintf("พื้นที่ %d · ข้อมูลเมือง %s", i+1, safeText(city)), 65)
		coords := r.Data.Location.Coordinates
		if len(coords) == 2 && coords[1] >= -90 && coords[1] <= 90 && coords[0] >= -180 && coords[0] <= 180 {
			field.Value += fmt.Sprintf("\nตำแหน่งข้อมูลห่างพิกัด %.1f กม.", distanceKM(locations[i], Coordinates{coords[1], coords[0]}))
		}
		field.Value = short(field.Value, 1400/len(locations)-len([]rune(field.Name)))
		field.Inline = len(locations) > 1
		if len(locations) == 1 && weather != "" {
			field = airField(city, *p.AQIUS, "", p.TS, c.Location)
			field.Name = "คุณภาพอากาศ · US AQI"
			field.Inline = true
			e.Description = "📍 " + safeText(city)
			if len(coords) == 2 && coords[1] >= -90 && coords[1] <= 90 && coords[0] >= -180 && coords[0] <= 180 {
				e.Description += fmt.Sprintf(" · ห่าง %.1f กม.", distanceKM(locations[i], Coordinates{coords[1], coords[0]}))
			}
			e.Description += "\nค่าจากเมืองใกล้คุณ ไม่ใช่จุดวัดตรงบ้าน"
			e.Fields = append(e.Fields, field, EmbedField{Name: "สภาพอากาศ", Value: fmt.Sprintf("🌡 **%d°C**\n%s\nวัดเมื่อ %s น.", *w.Tp, interpretWeatherIcon(w.Ic), w.TS.In(c.Location).Format("02/01 15:04")), Inline: true})
			colors := []int{0x57F287, 0xFEE75C, 0xE67E22, 0xED4245}
			band := 0
			for _, threshold := range []int{50, 100, 150} {
				if *p.AQIUS > threshold {
					band++
				}
			}
			e.Color = colors[band]
			continue
		}
		e.Fields = append(e.Fields, field)
	}
	return e, nil
}
