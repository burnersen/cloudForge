// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestErgebnisMasse(t *testing.T) {
	faelle := []struct {
		name               string
		breite, hoehe, max int
		zielB, zielH       int
		verkleinert        bool
	}{
		{"4K auf 1080", 3840, 2160, 1080, 1920, 1080, true},
		{"Kino-4K (DCI) auf 1080", 4096, 2160, 1080, 1920, 1012, true},
		{"Breitbild 2,4:1 auf 1080", 3840, 1600, 1080, 1920, 800, true},
		{"hochkant 4K auf 1080", 2160, 3840, 1080, 1080, 1920, true},
		{"4:3 mit 1440 Zeilen auf 1080", 1920, 1440, 1080, 1440, 1080, true},
		{"1080p bleibt bei 1080", 1920, 1080, 1080, 1920, 1080, false},
		{"720p wird nie vergrössert", 1280, 720, 1080, 1280, 720, false},
		{"1080p auf 720", 1920, 1080, 720, 1280, 720, true},
		{"4K auf 1440", 3840, 2160, 1440, 2560, 1440, true},
		{"aus (0)", 3840, 2160, 0, 3840, 2160, false},
		{"Masse unbekannt", 0, 0, 1080, 0, 0, false},
	}
	for _, f := range faelle {
		b, h, v := ergebnisMasse(f.breite, f.hoehe, f.max)
		if b != f.zielB || h != f.zielH || v != f.verkleinert {
			t.Errorf("%s: %dx%d (verkleinert %v), erwartet %dx%d (%v)", f.name, b, h, v, f.zielB, f.zielH, f.verkleinert)
		}
		if b%2 != 0 || h%2 != 0 {
			t.Errorf("%s: %dx%d - 4:2:0 braucht gerade Kanten", f.name, b, h)
		}
	}
}

func TestVerkleinernFilterUndAnzeige(t *testing.T) {
	e := standardWerte()
	e.MaxAufloesung = 1080
	vierK := VideoInfo{Breite: 3840, Hoehe: 2160}
	if filter := verkleinernFilter(vierK, e); filter != "scale=1920:1080" {
		t.Errorf("Filter %q, erwartet scale=1920:1080 (ohne Schärfung)", filter)
	}
	if text := aufloesungText(vierK, e.MaxAufloesung); text != "2160p → 1080p" {
		t.Errorf("Anzeige %q", text)
	}
	if text := aufloesungText(vierK, 0); text != "2160p" {
		t.Errorf("beim Umpacken bleibt die Grösse: %q", text)
	}
	if filter := verkleinernFilter(VideoInfo{Breite: 1920, Hoehe: 1080}, e); filter != "" {
		t.Errorf("1080p braucht keinen Filter: %q", filter)
	}
}

func TestFarbArgumenteReichenNurDurchWasDaIst(t *testing.T) {
	hdr10 := VideoInfo{FarbPrimaer: "bt2020", FarbKurve: "smpte2084", FarbMatrix: "bt2020nc", FarbBereich: "tv"}
	soll := []string{"-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc", "-color_range", "tv"}
	if args := farbArgumente(hdr10); !slices.Equal(args, soll) {
		t.Errorf("HDR10: %v, erwartet %v", args, soll)
	}
	if args := farbArgumente(VideoInfo{}); len(args) != 0 {
		t.Errorf("ohne Angaben darf nichts erfunden werden: %v", args)
	}
	halb := VideoInfo{FarbPrimaer: "unknown", FarbKurve: "bt470bg", FarbMatrix: "bt709", FarbBereich: "reserved"}
	if args := farbArgumente(halb); !slices.Equal(args, []string{"-colorspace", "bt709"}) {
		t.Errorf("unbekannte Werte und bt470bg-Kurve gehören weg: %v", args)
	}
}

func TestEncodeArgumenteVerkleinernNurBeimUmwandeln(t *testing.T) {
	e := standardWerte()
	auftrag := EncodeAuftrag{Quelle: "q.mkv", Ziel: "z.mkv", CRF: 30,
		Verkleinern: "scale=1920:1080", Farbangaben: []string{"-color_trc", "smpte2084"}}

	umwandeln := strings.Join(EncodeArgumente(auftrag, e), " ")
	if !strings.Contains(umwandeln, "-vf scale=1920:1080") || !strings.Contains(umwandeln, "-color_trc smpte2084") {
		t.Errorf("Umwandeln ohne Verkleinern oder Farbangaben: %s", umwandeln)
	}
	if !strings.HasSuffix(umwandeln, " z.mkv") {
		t.Errorf("Zieldatei muss am Ende stehen: %s", umwandeln)
	}

	auftrag.Umpacken = true
	umpacken := strings.Join(EncodeArgumente(auftrag, e), " ")
	if strings.Contains(umpacken, "-vf") || strings.Contains(umpacken, "-color_trc") {
		t.Errorf("Umpacken muss das Bild lassen, wie es ist: %s", umpacken)
	}
}

func TestFensterArgumenteVerkleinern(t *testing.T) {
	text := strings.Join(fensterArgumente("q.mp4", []Fenster{{StartSek: 10, LaengeSek: 8}}, "scale=1920:1080"), " ")
	if !strings.Contains(text, "setpts=PTS-STARTPTS,scale=1920:1080[fertig]") {
		t.Errorf("Messstücke nicht verkleinert: %s", text)
	}
	ohne := strings.Join(fensterArgumente("q.mp4", []Fenster{{StartSek: 10, LaengeSek: 8}}, ""), " ")
	if !strings.Contains(ohne, "setpts=PTS-STARTPTS[fertig]") {
		t.Errorf("ohne Verkleinern muss die Kette bleiben wie bisher: %s", ohne)
	}
}

func TestMaxAufloesungLesen(t *testing.T) {
	for _, wert := range []string{"0", "720", "1080", "1440", "2160"} {
		var e Einstellungen
		if err := maxAufloesungLesen(&e, wert); err != nil || e.MaxAufloesung == 0 && wert != "0" {
			t.Errorf("%s muss gehen: %v (%d)", wert, err, e.MaxAufloesung)
		}
	}
	for _, wert := range []string{"1800", "-1", "1080p", ""} {
		var e Einstellungen
		if err := maxAufloesungLesen(&e, wert); err == nil {
			t.Errorf("%q muss abgelehnt werden", wert)
		}
	}
	if standardWerte().MaxAufloesung != 0 {
		t.Error("ab Werk muss maxAufloesung aus sein")
	}
}

// Mit echtem ffmpeg: Das Ergebnis hat die kleinere Grösse und dieselben
// Farbangaben wie die Quelle — gelesen so, wie CloudForge selbst liest.
func TestVerkleinernUndFarbenMitEchtemFFmpeg(t *testing.T) {
	ffmpeg := os.Getenv("CLOUDFORGE_TEST_FFMPEG")
	ffprobe := os.Getenv("CLOUDFORGE_TEST_FFPROBE")
	if ffmpeg == "" || ffprobe == "" {
		t.Skip("CLOUDFORGE_TEST_FFMPEG und CLOUDFORGE_TEST_FFPROBE nicht gesetzt")
	}
	ordner := t.TempDir()
	quelle := filepath.Join(ordner, "hdr.mkv")
	// Die Farbangaben stehen an den Bildern selbst (setparams), wie bei einem
	// echten Film nach dem Dekodieren — als Ausgabe-Schalter allein nahm
	// ffmpeg 8.1 für ein Testbild nur zwei der vier an.
	erzeugen := exec.Command(ffmpeg, "-nostdin", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=25:duration=2,"+
			"setparams=color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc:range=tv",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p10le", quelle)
	if ausgabe, err := erzeugen.CombinedOutput(); err != nil {
		t.Fatalf("Testvideo nicht erzeugbar: %v (%s)", err, ausgabe)
	}
	info, err := KopfdatenLesen(ffprobe, quelle)
	if err != nil {
		t.Fatal(err)
	}
	if info.FarbKurve != "smpte2084" || info.FarbPrimaer != "bt2020" {
		t.Fatalf("Farbangaben der Quelle nicht gelesen: %+v", info)
	}

	e := standardWerte()
	e.FFmpegPfad, e.FFprobePfad = ffmpeg, ffprobe
	e.MaxAufloesung = 720
	ziel := filepath.Join(ordner, "ergebnis.mkv")
	auftrag := EncodeAuftrag{Quelle: quelle, Ziel: ziel, CRF: 40,
		Verkleinern: verkleinernFilter(info, e), Farbangaben: farbArgumente(info)}
	if err := Kodieren(context.Background(), auftrag, e, 0, nil); err != nil {
		t.Fatalf("Umwandeln: %v", err)
	}

	ergebnis, err := KopfdatenLesen(ffprobe, ziel)
	if err != nil {
		t.Fatal(err)
	}
	if ergebnis.Breite != 1280 || ergebnis.Hoehe != 720 {
		t.Errorf("Ergebnis %dx%d, erwartet 1280x720", ergebnis.Breite, ergebnis.Hoehe)
	}
	if ergebnis.FarbPrimaer != info.FarbPrimaer || ergebnis.FarbKurve != info.FarbKurve ||
		ergebnis.FarbMatrix != info.FarbMatrix || ergebnis.FarbBereich != info.FarbBereich {
		t.Errorf("Farbangaben verloren: Quelle %s/%s/%s/%s, Ergebnis %s/%s/%s/%s",
			info.FarbPrimaer, info.FarbKurve, info.FarbMatrix, info.FarbBereich,
			ergebnis.FarbPrimaer, ergebnis.FarbKurve, ergebnis.FarbMatrix, ergebnis.FarbBereich)
	}
}
