// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Die Toleranz der Spieldauer muss zwei Dinge gleichzeitig leisten: harmlose
// Rundungsunterschiede durchlassen und einen abgebrochenen Encode fangen.
func TestDauerToleranzFuer(t *testing.T) {
	faelle := []struct {
		dauerSek   float64
		abweichung float64
		erlaubt    bool
		warum      string
	}{
		// Der echte Fehlalarm vom 21.09.2026: 120 s Quelle, 121,1 s Ergebnis.
		{120, 1.1, true, "gemessener Fehlalarm aus dem Cloud-Test"},
		{120, 1.0, true, "eine Sekunde auf zwei Minuten ist harmlos"},
		{30, 1.9, true, "kurze Datei, Mindesttoleranz greift"},
		{30, 2.5, false, "kurze Datei, deutlich zu viel"},

		// Ein 49-Minuten-Film: mehrere Sekunden sind normal.
		{2940, 10, true, "10 s auf 49 Minuten sind Rundung"},
		{2940, 14, true, "knapp unter 0,5 Prozent"},
		{2940, 20, false, "ueber 0,5 Prozent, das ist verdaechtig"},

		// Ein abgebrochener Encode muss auf jeden Fall auffallen.
		{2940, 1470, false, "die Haelfte fehlt — abgebrochener Encode"},
		{2940, 60, false, "eine Minute fehlt"},
	}

	for _, f := range faelle {
		erlaubt := dauerToleranzFuer(f.dauerSek)
		durchgelassen := f.abweichung <= erlaubt

		if durchgelassen != f.erlaubt {
			t.Errorf("%s: Dauer %.0f s, Abweichung %.1f s, Toleranz %.1f s — "+
				"durchgelassen=%v, erwartet=%v",
				f.warum, f.dauerSek, f.abweichung, erlaubt, durchgelassen, f.erlaubt)
		}
	}
}

func TestDauerToleranzWaechstMit(t *testing.T) {
	kurz := dauerToleranzFuer(60)
	lang := dauerToleranzFuer(7200)

	if lang <= kurz {
		t.Errorf("die Toleranz muss mit der Spieldauer wachsen: kurz %.1f, lang %.1f",
			kurz, lang)
	}
	if kurz < dauerToleranzMindestensSek {
		t.Errorf("die Mindesttoleranz %.1f wurde unterschritten: %.1f",
			dauerToleranzMindestensSek, kurz)
	}
}

func TestPruefErgebnisErsterFehler(t *testing.T) {
	ergebnis := PruefErgebnis{
		Schritte: []PruefSchritt{
			{Name: "Datei vorhanden", Bestanden: true, Befund: "54 MB"},
			{Name: "Spieldauer stimmt", Bestanden: false, Befund: "zu kurz"},
			{Name: "Tonspuren", Bestanden: false, Befund: "fehlen"},
		},
	}

	// Gemeldet wird der ERSTE Fehler, nicht der letzte — er ist die Ursache.
	if gemeldet := ergebnis.ErsterFehler(); gemeldet != "Spieldauer stimmt: zu kurz" {
		t.Errorf("falscher Fehler gemeldet: %q", gemeldet)
	}

	ohneFehler := PruefErgebnis{
		Schritte: []PruefSchritt{{Name: "alles gut", Bestanden: true}},
	}
	if gemeldet := ohneFehler.ErsterFehler(); gemeldet != "" {
		t.Errorf("ohne Fehler soll nichts gemeldet werden, bekommen: %q", gemeldet)
	}
}

// Prüft den Schalter vollpruefung an der echten Prüfkette. Dafür braucht es
// ffmpeg und ffprobe, deshalb läuft der Test nur, wenn beide angegeben sind
// (auf dem Server): CLOUDFORGE_TEST_FFMPEG und CLOUDFORGE_TEST_FFPROBE.
//
// Der Trick: Beim Prüfen zeigt ffmpegPfad auf eine Datei, die es nicht gibt.
// Versucht die Kette zu dekodieren, scheitert sie genau daran.
func TestVollpruefungSchalter(t *testing.T) {
	ffmpeg := os.Getenv("CLOUDFORGE_TEST_FFMPEG")
	ffprobe := os.Getenv("CLOUDFORGE_TEST_FFPROBE")
	if ffmpeg == "" || ffprobe == "" {
		t.Skip("CLOUDFORGE_TEST_FFMPEG und CLOUDFORGE_TEST_FFPROBE nicht gesetzt")
	}

	video := filepath.Join(t.TempDir(), "probe.mkv")
	erzeugen := exec.Command(ffmpeg, "-nostdin", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25:duration=2",
		"-f", "lavfi", "-i", "sine=duration=2",
		"-c:v", "ffv1", "-c:a", "pcm_s16le", video)
	if ausgabe, err := erzeugen.CombinedOutput(); err != nil {
		t.Fatalf("Testvideo nicht erzeugbar: %v (%s)", err, ausgabe)
	}

	quelle, err := KopfdatenLesen(ffprobe, video)
	if err != nil {
		t.Fatalf("Kopfdaten des Testvideos nicht lesbar: %v", err)
	}
	// Die "Quelle" doppelt so gross ausgeben, damit "Kleiner geworden" besteht.
	quelle.GroesseBytes = DateiGroesse(video) * 2

	e := standardWerte()
	e.FFprobePfad = ffprobe
	e.FFmpegPfad = filepath.Join(t.TempDir(), "gibt-es-nicht")

	e.Vollpruefung = false
	if p := ErgebnisPruefen(context.Background(), quelle, video, e, nil); !p.Bestanden {
		t.Errorf("vollpruefung=nein: die Kette ist gescheitert (%s)", p.ErsterFehler())
	}

	e.Vollpruefung = true
	p := ErgebnisPruefen(context.Background(), quelle, video, e, nil)
	if p.Bestanden {
		t.Fatal("vollpruefung=ja: die Dekodierpruefung wurde gar nicht versucht")
	}
	if !strings.HasPrefix(p.ErsterFehler(), "Vollstaendig abspielbar") {
		t.Errorf("vollpruefung=ja: falsche Stufe gescheitert (%s)", p.ErsterFehler())
	}
}
