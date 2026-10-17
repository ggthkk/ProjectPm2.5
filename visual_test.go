package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/go-text/typesetting/font"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTruncatedThaiTextStaysInsideCardColumn(t *testing.T) {
	face, err := font.ParseTTF(bytes.NewReader(cardFont))
	if err != nil {
		t.Fatal(err)
	}
	c := cardCanvas{img: image.NewRGBA(image.Rect(0, 0, 1000, 400)), face: face}
	c.paragraph(strings.Repeat("ข้อความภาษาไทยที่ยาวและต้องตัดบรรทัด ", 20), 20, 0, 400, 36, 3, color.White)
	for y := 0; y < 400; y++ {
		for x := 440; x < 1000; x++ {
			if c.img.RGBAAt(x, y).A != 0 {
				t.Fatal("text overflow beyond column", x, y)
			}
		}
	}
}

func TestVisualWebhookUsesPNGAttachmentsAndRetainsLinks(t *testing.T) {
	e := Embed{Title: "การ์ดทดสอบภาษาไทย", Description: "ทดสอบวรรณยุกต์และข้อความ", URL: "https://example.com/story", Color: 0x5865F2, Visual: true, Fields: []EmbedField{{Name: "ต้นทาง", Value: "[อ่านต่อ](https://example.com/story)"}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer r.MultipartForm.RemoveAll()
		var p Payload
		if err := json.Unmarshal([]byte(r.FormValue("payload_json")), &p); err != nil {
			t.Error(err)
		}
		if len(p.Attachments) != 1 || p.Embeds[0].Image == nil || p.Embeds[0].Image.URL != "attachment://report-1.png" || !strings.Contains(p.Embeds[0].Description, e.URL) || len(p.Embeds[0].Fields) != 0 || len(p.AllowedMentions.Parse) != 0 {
			t.Error("invalid image payload")
		}
		f, _, err := r.FormFile("files[0]")
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer f.Close()
		cfg, err := png.DecodeConfig(f)
		if err != nil || cfg.Width != 1000 || cfg.Height > 1800 || cfg.Height < 200 {
			t.Error(cfg, err)
		}
		w.Write([]byte(`{"id":"image-receipt"}`))
	}))
	defer server.Close()
	id, ok, err := send(context.Background(), server.Client(), server.URL, []Embed{e})
	if err != nil || !ok || id != "image-receipt" || len(e.Fields) != 1 {
		t.Fatal(id, ok, err)
	}
}

func TestVisualTextNormalizesUnsupportedDisplayCharacters(t *testing.T) {
	got := visualText("📍 เมือง\n＠user **AQI**")
	if strings.ContainsAny(got, "\n＠📍*") || !strings.Contains(got, "เมือง @user AQI") {
		t.Fatal(got)
	}
}
