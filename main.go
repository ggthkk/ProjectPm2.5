package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

func splitLatLon(s string) (string, string) {
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return "", ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

// โครงสร้าง response ที่ต้องใช้
type AirQualityResponse struct {
	Data struct {
		City    string `json:"city"`
		State   string `json:"state"`
		Country string `json:"country"`
		Current struct {
			Pollution struct {
				AQIUS  int    `json:"aqius"`
				MainUS string `json:"mainus"`
			} `json:"pollution"`
			Weather struct {
				Tp int     `json:"tp"` // Temperature (°C)
				Hu int     `json:"hu"` // Humidity (%)
				Ws float64 `json:"ws"` // Wind speed (m/s)
				Wd int     `json:"wd"` // Wind direction (°)
				Ic string  `json:"ic"` // Weather icon code
			} `json:"weather"`
		} `json:"current"`
	} `json:"data"`
}

func interpretAQI(aqi int, pollutant string) string {
	msg := ""

	switch {
	case aqi <= 50:
		msg = "อากาศดีมาก หายใจได้สบาย ๆ 😌"
	case aqi <= 100:
		msg = "อากาศพอใช้ได้ ออกไปได้ แต่อย่าอยู่กลางแจ้งนานเกินไป 🙂"
	case aqi <= 150:
		msg = "อากาศเริ่มแย่ กลุ่มเสี่ยงควรเลี่ยงการออกนอกบ้าน 😷"
	case aqi <= 200:
		msg = "อากาศไม่ดีเลย ใส่หน้ากากถ้าออกนอกบ้าน และอยู่น้อยที่สุด 🤧"
	case aqi <= 300:
		msg = "อากาศแย่มาก อยู่ในบ้านดีที่สุด ห้ามออกถ้าไม่จำเป็น 🚫"
	default:
		msg = "อากาศอันตรายมาก!! ห้ามออกนอกบ้านเด็ดขาด ⚠️"
	}

	if pollutant == "p2" && aqi > 50 {
		msg += " (ค่าฝุ่น PM2.5 สูงและเป็นตัวหลักที่ทำให้อากาศแย่ลง!)"
	} else if pollutant == "o3" && aqi > 50 {
		msg += " (โอโซนเป็นสาเหตุหลัก!)"
	} else if pollutant == "pm10" && aqi > 50 {
		msg += " (ฝุ่น PM10 เป็นสาเหตุหลัก!)"
	} else if pollutant == "no2" && aqi > 50 {
		msg += " (ไนโตรเจนไดออกไซด์เป็นสาเหตุหลัก!)"
	} else if pollutant == "so2" && aqi > 50 {
		msg += " (ซัลเฟอร์ไดออกไซด์เป็นสาเหตุหลัก!)"
	} else if pollutant == "co" && aqi > 50 {
		msg += " (คาร์บอนมอนอกไซด์เป็นสาเหตุหลัก!)"
	} else if pollutant == "bc" && aqi > 50 {
		msg += " (คาร์บอนแบล็คเป็นสาเหตุหลัก!)"
	}
	return msg
}

func interpretWeatherIcon(ic string) string {
	switch {
	case strings.HasPrefix(ic, "01"):
		return "แดดจ้า ☀️"
	case strings.HasPrefix(ic, "02"):
		return "มีเมฆบางส่วน 🌤"
	case strings.HasPrefix(ic, "03"), strings.HasPrefix(ic, "04"):
		return "เมฆเยอะ ☁️"
	case strings.HasPrefix(ic, "09"), strings.HasPrefix(ic, "10"):
		return "ฝนตก 🌧"
	case strings.HasPrefix(ic, "11"):
		return "พายุฝนฟ้าคะนอง ⛈"
	case strings.HasPrefix(ic, "13"):
		return "หิมะตก ❄️ (แต่ไทยไม่น่าใช่ 😅)"
	case strings.HasPrefix(ic, "50"):
		return "หมอกลง 🌫"
	default:
		return "ไม่รู้สภาพอากาศ 🤷‍♂️"
	}
}

func escape(s string) string {
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}

func sendToDiscordEmbed(title, description string, color int) {
	webhookURL := os.Getenv("DISCORD_WEBHOOK_URL")
	if webhookURL == "" {
		fmt.Println("❌ DISCORD_WEBHOOK_URL not set")
		return
	}

	payload := fmt.Sprintf(`{
		"embeds": [
		  {
			"title": "%s",
			"description": "%s",
			"color": %d
		  }
		]
	  }`, escape(title), escape(description), color)

	resp, err := http.Post(webhookURL, "application/json", strings.NewReader(payload))
	if err != nil {
		fmt.Println("❌ Failed to send to Discord:", err)
		notifyDiscordError("HTTP Error", err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 204 {
		body, _ := ioutil.ReadAll(resp.Body)
		errorMsg := fmt.Sprintf("Code: %d\nBody: %s", resp.StatusCode, string(body))
		fmt.Println("❌ Discord response:", errorMsg)
		notifyDiscordError("Discord Error", errorMsg)
	}
}

// ✅ ฟังก์ชันแจ้ง error กลับไป Discord แบบ plain text (กัน embed พังซ้ำ)
func notifyDiscordError(title string, message string) {
	webhookURL := os.Getenv("DISCORD_WEBHOOK_URL")
	if webhookURL == "" {
		return
	}

	payload := fmt.Sprintf("{\"content\": \"🚨 **%s**\\n```\n%s\n```\"}", escape(title), escape(message))
	_, _ = http.Post(webhookURL, "application/json", strings.NewReader(payload))
}

func main() {
	err := godotenv.Load()
	if err != nil {
		fmt.Println("❌ Error loading .env file")
		return
	}

	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		fmt.Println("API_KEY not set")
		return
	}

	rawLocations := os.Getenv("map")
	if rawLocations == "" {
		fmt.Println("No locations provided in 'map'")
		return
	}

	locations := strings.Split(rawLocations, ";")

	for _, loc := range locations {
		loc = strings.TrimSpace(loc)
		if loc == "" {
			continue
		}

		lat, lon := splitLatLon(loc)
		if lat == "" || lon == "" {
			fmt.Println("Invalid lat/lon:", loc)
			continue
		}

		url := fmt.Sprintf("http://api.airvisual.com/v2/nearest_city?lat=%s&lon=%s&key=%s", lat, lon, apiKey)

		resp, err := http.Get(url)
		if err != nil {
			fmt.Println("❌ HTTP error:", err)
			continue
		}
		defer resp.Body.Close()

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			fmt.Println("❌ Read error:", err)
			continue
		}

		var result AirQualityResponse
		if err := json.Unmarshal(body, &result); err != nil {
			fmt.Println("❌ JSON parse error:", err)
			continue
		}

		city := result.Data.City
		state := result.Data.State
		country := result.Data.Country
		aqi := result.Data.Current.Pollution.AQIUS
		mainPollutant := result.Data.Current.Pollution.MainUS
		weather := result.Data.Current.Weather

		fmt.Println("----------------------------------------------------")
		fmt.Printf("📍 พิกัดใกล้ %s, %s, %s\n", city, state, country)
		fmt.Printf("🔴 AQI (US): %d\n", aqi)
		fmt.Println("🫁 สรุป:", interpretAQI(aqi, mainPollutant))
		fmt.Printf("🌡 อุณหภูมิ: %d°C\n", weather.Tp)
		fmt.Printf("💧 ความชื้น: %d%%\n", weather.Hu)
		fmt.Printf("💨 ลม: %.1f m/s ทิศ %d°\n", weather.Ws, weather.Wd)
		fmt.Printf("🌤 สภาพอากาศ: %s\n", interpretWeatherIcon(weather.Ic))
		fmt.Println("----------------------------------------------------")

		description := fmt.Sprintf(
			"🔴 AQI (US): %d\n"+
				"🫁 สรุป: %s\n"+
				"🌡 อุณหภูมิ: %d°C\n"+
				"💧 ความชื้น: %d%%\n"+
				"💨 ลม: %.1f m/s ทิศ %d°\n"+
				"🌤 สภาพอากาศ: %s",
			aqi,
			interpretAQI(aqi, mainPollutant),
			weather.Tp, weather.Hu, weather.Ws, weather.Wd,
			interpretWeatherIcon(weather.Ic),
		)

		color := 0x00ff00 // เขียว = ดี
		switch {
		case aqi > 300:
			color = 0x7E0023 // ม่วงเข้ม
		case aqi > 200:
			color = 0x99004C // แดงเข้ม
		case aqi > 150:
			color = 0xCC0033 // แดง
		case aqi > 100:
			color = 0xFF9933 // ส้ม
		case aqi > 50:
			color = 0xFFFF00 // เหลือง
		}

		sendToDiscordEmbed(fmt.Sprintf("📍 %s, %s, %s", city, state, country), description, color)

	}

}
