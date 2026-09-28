// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestImGrenzbereich(t *testing.T) {
	faelle := []struct {
		erwartet, schwelle float64
		grenz              bool
	}{
		{20, 20, true}, {5, 20, true}, {35, 20, true}, // Ränder gehören dazu
		{4.9, 20, false}, {35.1, 20, false},
		{18, 20, true},   // Fall vom 27.09.2026: erste Schätzung 18 %, echt 5 %
		{62, 20, false},  // klar sparsam: keine Extra-Minute
		{-23, 20, false}, // klar grösser: gleich umpacken
	}
	for _, f := range faelle {
		if bekommen := imGrenzbereich(f.erwartet, f.schwelle); bekommen != f.grenz {
			t.Errorf("Vorhersage %.1f bei Schwelle %.0f: Grenzbereich %v, erwartet %v",
				f.erwartet, f.schwelle, bekommen, f.grenz)
		}
	}
}

func TestGroessenprobeFensterGleichmaessigUeberDenGanzenFilm(t *testing.T) {
	fenster := groessenprobeFenster(1000, 8, 10)
	if len(fenster) != 10 {
		t.Fatalf("10 Stellen erwartet, %d bekommen", len(fenster))
	}
	for i, f := range fenster {
		mitte := f.StartSek + f.LaengeSek/2
		soll := 1000 * (float64(i) + 0.5) / 10 // 50, 150, … 950
		if math.Abs(mitte-soll) > 0.001 || f.LaengeSek != 8 {
			t.Errorf("Stelle %d: Mitte %.1f Länge %.1f, erwartet Mitte %.1f Länge 8", i, mitte, f.LaengeSek, soll)
		}
	}

	// Kurzer Film: Kein Stück darf vor dem Anfang oder hinter dem Ende liegen.
	for _, f := range groessenprobeFenster(40, 8, 10) {
		if f.StartSek < 0 || f.StartSek+f.LaengeSek > 40+1e-9 {
			t.Errorf("40-s-Film: Stück %.1f bis %.1f liegt ausserhalb", f.StartSek, f.StartSek+f.LaengeSek)
		}
	}

	if groessenprobeFenster(0, 8, 10) != nil || groessenprobeFenster(1000, 0, 10) != nil {
		t.Error("ohne Spieldauer oder Stücklänge darf es keine Stücke geben")
	}
}

// Die Probe zeigt nur einen schlichten Balken: Bildnummer, Position und
// Grösse der Probestücke hätten neben dem ganzen Film keine Bedeutung.
func TestNurBalkenGibtNurZeitenWeiter(t *testing.T) {
	if nurBalken(nil) != nil {
		t.Error("ohne Empfänger muss nil bleiben")
	}
	var bekommen Stand
	nurBalken(func(s Stand) { bekommen = s })(Stand{
		Anteil: 0.4, Tempo: 3, Rest: 5, Position: 32, Bild: 800, BilderProSek: 120, Bytes: 1 << 20,
	})
	if bekommen.Anteil != 0.4 || bekommen.Tempo != 3 || bekommen.Rest != 5 {
		t.Errorf("Anteil und Zeiten verloren: %+v", bekommen)
	}
	if istUmwandeln(bekommen) || bekommen.Bytes != 0 || bekommen.Bild != 0 {
		t.Errorf("Probe darf nicht wie eine Umwandlung angezeigt werden: %+v", bekommen)
	}
}

func TestFensterArgumenteHaengtAlleStueckeAneinander(t *testing.T) {
	args := fensterArgumente("film.mp4", groessenprobeFenster(1000, 8, 10))
	text := strings.Join(args, " ")
	if n := strings.Count(text, "-i film.mp4"); n != 10 {
		t.Errorf("10 Eingänge erwartet, %d gefunden", n)
	}
	if !strings.Contains(text, "concat=n=10:v=1:a=0") || !strings.Contains(text, "-map [fertig] -an -sn") {
		t.Errorf("Kette unvollständig: %s", text)
	}
}

// Klare Fälle kosten keine Probe; scheitert die Probe im Grenzbereich, gilt
// die erste Vorhersage — die Datei bleibt nicht hängen.
func TestErsparnisVorhersagenOhneProbeUndBeiFehler(t *testing.T) {
	ctx := context.Background()
	e := standardWerte()
	e.MindestErsparnisProzent = 20
	e.FFmpegPfad, e.FFprobePfad = "/gibt/es/nicht/ffmpeg", "/gibt/es/nicht/ffprobe"
	// 1 GB, ganz Bild (8000 kbit/s x 1000 s): Anteil 0,4 heisst 60 % kleiner.
	info := VideoInfo{GroesseBytes: 1e9, VideoBitrateKbps: 8000, DauerSek: 1000}

	// Ob die Probe begonnen hat, verrät das Protokoll ("Groesse pruefen").
	vorhersagen := func(anteil float64) (prozent float64, ok, probeLief bool, err error) {
		ordner := t.TempDir()
		protokoll, perr := ProtokollOeffnen(ordner)
		if perr != nil {
			t.Fatal(perr)
		}
		anz := NeueAnzeige()
		anz.ProtokollSetzen(protokoll)
		prozent, ok, err = ersparnisVorhersagen(ctx, anz, 1, 1, "film.mp4", info,
			AutoCQErgebnis{CRF: 30, AnteilQuelle: anteil}, t.TempDir(), e)
		protokoll.Schliessen()

		dateien, _ := filepath.Glob(filepath.Join(ordner, "*.txt"))
		for _, datei := range dateien {
			inhalt, _ := os.ReadFile(datei)
			probeLief = probeLief || strings.Contains(string(inhalt), "Groesse pruefen")
		}
		return prozent, ok, probeLief, err
	}

	klar, ok, probeLief, err := vorhersagen(0.4)
	if err != nil || !ok || math.Abs(klar-60) > 0.01 || probeLief {
		t.Errorf("klarer Fall: 60 %% ohne Probe erwartet, bekommen %.2f (ok %v, %v, Probe %v)", klar, ok, err, probeLief)
	}

	knapp, ok, probeLief, err := vorhersagen(0.82)
	if err != nil || !ok || math.Abs(knapp-18) > 0.01 || !probeLief {
		t.Errorf("knapper Fall mit gescheiterter Probe: erste Vorhersage 18 %% erwartet, bekommen %.2f (ok %v, %v, Probe %v)",
			knapp, ok, err, probeLief)
	}
}

// Die Grössenprobe mit echtem ffmpeg muss den Anteil der ganzen Umwandlung
// treffen. Bei gleichförmigem Testbild geht das eng — hier wird die
// Verdrahtung geprüft (Stellen, Quellbytes, Encoder-Einstellungen).
func TestGroessenProbeMitEchtemFFmpeg(t *testing.T) {
	ffmpeg := os.Getenv("CLOUDFORGE_TEST_FFMPEG")
	ffprobe := os.Getenv("CLOUDFORGE_TEST_FFPROBE")
	if ffmpeg == "" || ffprobe == "" {
		t.Skip("CLOUDFORGE_TEST_FFMPEG und CLOUDFORGE_TEST_FFPROBE nicht gesetzt")
	}
	ctx := context.Background()
	ordner := t.TempDir()

	quelle := filepath.Join(ordner, "film.mp4")
	erzeugen := exec.Command(ffmpeg, "-nostdin", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25:duration=100",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "18", quelle)
	if ausgabe, err := erzeugen.CombinedOutput(); err != nil {
		t.Fatalf("Testvideo nicht erzeugbar: %v (%s)", err, ausgabe)
	}

	e := standardWerte()
	e.FFmpegPfad, e.FFprobePfad = ffmpeg, ffprobe
	const crf = 35

	anteil, err := GroessenProbe(ctx, quelle, 100, crf, ordner, e, nil)
	if err != nil {
		t.Fatalf("Grössenprobe: %v", err)
	}

	ganz := filepath.Join(ordner, "ganz.mkv")
	if err := Kodieren(ctx, EncodeAuftrag{Quelle: quelle, Ziel: ganz, CRF: crf, NurVideo: true}, e, 0, nil); err != nil {
		t.Fatalf("ganze Umwandlung: %v", err)
	}
	quelleBytes, err := QuelleFensterBytes(ctx, ffprobe, quelle, []Fenster{{StartSek: 0, LaengeSek: 100}})
	if err != nil || quelleBytes <= 0 {
		t.Fatalf("Quellgrösse: %d (%v)", quelleBytes, err)
	}
	echt := float64(DateiGroesse(ganz)) / float64(quelleBytes)

	if math.Abs(anteil-echt) > 0.10 {
		t.Errorf("Grössenprobe %.1f %% der Quelle, ganze Umwandlung %.1f %% - mehr als 10 Punkte daneben",
			anteil*100, echt*100)
	}
}
