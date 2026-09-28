// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"strings"
	"testing"
	"time"
)

// Echte Ausgabe von ffmpeg n8.1 mit -progress pipe:1, ein Block.
const echterFortschrittsBlock = `frame=1502
fps=24.61
stream_0_0_q=32.0
bitrate=1834.5kbits/s
total_size=6881280
out_time_us=30040000
out_time_ms=30040000
out_time=00:00:30.040000
dup_frames=0
drop_frames=0
speed=0.492x
progress=continue`

func TestFfmpegFortschrittLiestEchtenBlock(t *testing.T) {
	var f ffmpegFortschritt
	gemeldet := 0
	for _, zeile := range strings.Split(echterFortschrittsBlock, "\n") {
		if f.zeileLesen(zeile) {
			gemeldet++
		}
	}

	if gemeldet != 1 {
		t.Errorf("ein Block soll genau einmal gemeldet werden, waren %d", gemeldet)
	}
	if f.zeitSek != 30.04 {
		t.Errorf("Zeit 30,04 s erwartet, bekommen %v", f.zeitSek)
	}
	if f.tempo != 0.492 {
		t.Errorf("Tempo 0,492 erwartet, bekommen %v", f.tempo)
	}
	// Seit 0.7.0 für die Übersicht wie bei NVENCForge:
	if f.bild != 1502 || f.bilderProSek != 24.61 || f.bitrateKbps != 1834.5 || f.bytes != 6881280 {
		t.Errorf("Bild/fps/Bitrate/Groesse falsch gelesen: %d / %v / %v / %d",
			f.bild, f.bilderProSek, f.bitrateKbps, f.bytes)
	}
}

func TestFfmpegFortschrittUebersteht(t *testing.T) {
	// Ganz am Anfang schreibt ffmpeg "N/A" — das darf nichts kaputt machen
	// und die letzten guten Werte nicht überschreiben.
	f := ffmpegFortschritt{zeitSek: 12, tempo: 0.5, bild: 300, bilderProSek: 60, bitrateKbps: 5000, bytes: 4096}
	for _, zeile := range []string{
		"out_time_us=N/A", "speed=N/A", "speed=  0x", "Unsinn", "", "=leer",
		"frame=0", "fps=0.00", "bitrate=N/A", "total_size=N/A",
	} {
		f.zeileLesen(zeile)
	}
	if f.zeitSek != 12 || f.tempo != 0.5 {
		t.Errorf("gute Werte wurden ueberschrieben: Zeit %v, Tempo %v", f.zeitSek, f.tempo)
	}
	if f.bild != 300 || f.bilderProSek != 60 || f.bitrateKbps != 5000 || f.bytes != 4096 {
		t.Errorf("gute Werte wurden ueberschrieben: %d / %v / %v / %d", f.bild, f.bilderProSek, f.bitrateKbps, f.bytes)
	}
	if !f.zeileLesen("progress=end") {
		t.Error("das Ende eines Blocks wurde nicht erkannt")
	}
}

func TestRestzeitSchaetzen(t *testing.T) {
	// Nach 10 Minuten bei 25 % bleiben noch 30 Minuten.
	if rest := restzeitSchaetzen(10*time.Minute, 0.25); rest != 30*time.Minute {
		t.Errorf("30 Minuten erwartet, bekommen %v", rest)
	}
	// Ganz am Anfang wäre jede Zahl geraten — dann lieber keine.
	if rest := restzeitSchaetzen(time.Minute, 0.005); rest != 0 {
		t.Errorf("unter 1 %% soll keine Restzeit geschaetzt werden, bekommen %v", rest)
	}
	if rest := restzeitSchaetzen(time.Hour, 1); rest != 0 {
		t.Errorf("bei 100 %% ist nichts mehr uebrig, bekommen %v", rest)
	}
}

func TestUhrText(t *testing.T) {
	faelle := map[time.Duration]string{
		45 * time.Second:                "45 Sek",
		38 * time.Minute:                "38 Min",
		59*time.Minute + 59*time.Second: "59 Min",
		76 * time.Minute:                "1 Std 16 Min",
		27*time.Hour + 5*time.Minute:    "27 Std 5 Min",
	}
	for dauer, erwartet := range faelle {
		if bekommen := uhrText(dauer); bekommen != erwartet {
			t.Errorf("%v: %q erwartet, %q bekommen", dauer, erwartet, bekommen)
		}
	}
}

func TestStandTextZeigtBalkenUndProzent(t *testing.T) {
	text := standText(Stand{Anteil: 0.5, Tempo: 0.62, Rest: 38 * time.Minute})

	for _, teil := range []string{"[##########----------]", " 50 %", "noch 38 Min", "Tempo 0,62x"} {
		if !strings.Contains(text, teil) {
			t.Errorf("%q fehlt in %q", teil, text)
		}
	}
}

func TestStandTextBleibtImRahmen(t *testing.T) {
	// ffmpeg meldet am Ende gelegentlich ein paar Bilder mehr als die
	// Spieldauer hergibt. Der Balken darf dann nicht über 100 % laufen.
	for _, anteil := range []float64{-0.3, 1.7} {
		text := standText(Stand{Anteil: anteil})
		if strings.Count(text, "#")+strings.Count(text, "-") != balkenBreite {
			t.Errorf("Anteil %v: Balken hat die falsche Laenge: %q", anteil, text)
		}
	}
}

func TestGroesseText(t *testing.T) {
	if g := groesseText(1946892133); g != "1,81 GB" {
		t.Errorf("1,81 GB erwartet, bekommen %q", g)
	}
	if g := groesseText(54 * 1024 * 1024); g != "54 MB" {
		t.Errorf("54 MB erwartet, bekommen %q", g)
	}
}
