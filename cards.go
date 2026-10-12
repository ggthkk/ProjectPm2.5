package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func waterBadge(level int) string {
	labels := map[int]string{0: "ไม่มีสถานะ", 1: "🟢 ปกติ", 2: "🟡 เฝ้าระวัง", 3: "🟠 เตือนภัย", 4: "🔴 วิกฤต", 5: "🔴 ล้นตลิ่ง"}
	if label, ok := labels[level]; ok {
		return label
	}
	return "ไม่มีสถานะ"
}

func waterEmbed(title string, rows []WaterRow, previous map[string]int, c Config) Embed {
	areas := make([]string, 0, len(c.Provinces))
	names := map[string]string{"10": "กรุงเทพฯ", "12": "นนทบุรี", "13": "ปทุมธานี"}
	for _, code := range c.Provinces {
		name := names[code]
		if name == "" {
			name = "จังหวัดรหัส " + code
		}
		areas = append(areas, name)
	}
	e := Embed{Title: title, Color: 0xFEE75C, Description: "พื้นที่: " + strings.Join(areas, " · ")}
	for _, r := range rows {
		level := waterLevel(r)
		status := waterBadge(level)
		if old, ok := previous[strconv.Itoa(r.Station.ID)]; ok {
			status = waterBadge(old) + " → " + status
		}
		value := "**" + status + "**"
		if r.MSL != nil {
			value += "\nระดับน้ำ **" + safeText(*r.MSL) + " ม.รทก.**"
		}
		when := r.Time
		if observed, err := time.ParseInLocation("2006-01-02 15:04", r.Time, c.Location); err == nil {
			when = observed.Format("02/01 15:04")
		}
		value += "\nวัดเมื่อ " + safeText(when) + " น."
		e.Fields = append(e.Fields, EmbedField{Name: short(safeText(r.Station.Name.TH+" · "+r.Geo.Province.TH), 120), Value: value})
		if level >= 4 {
			e.Color = 0xED4245
		}
	}
	e.Description += "\n[ดูแผนที่สถานี ↗](https://www.thaiwater.net/water/wl)"
	e.Fields = append(e.Fields, EmbedField{Name: "แหล่งข้อมูล · ThaiWater / สสน.", Value: "สถานะ ณ จุดตรวจวัด ไม่ยืนยันน้ำท่วมบ้านหรือถนน"})
	return e
}

func (a *App) ackWater(rows []WaterRow, now time.Time) {
	for _, r := range rows {
		id := strconv.Itoa(r.Station.ID)
		a.state.Water[id] = waterLevel(r)
		a.state.WaterNotified[id] = now
	}
}

func airField(city string, aqi int, weather string, observed time.Time, location *time.Location) EmbedField {
	badge := "🟢"
	if aqi > 50 {
		badge = "🟡"
	}
	if aqi > 100 {
		badge = "🟠"
	}
	if aqi > 150 {
		badge = "🔴"
	}
	return EmbedField{Name: short(safeText(city), 120), Value: fmt.Sprintf("%s **AQI %d · %s**%s\nวัดเมื่อ %s น.", badge, aqi, interpretAQI(aqi), weather, observed.In(location).Format("02/01 15:04"))}
}
