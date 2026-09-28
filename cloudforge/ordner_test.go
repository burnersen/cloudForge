// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestIstErgebnisDatei(t *testing.T) {
	faelle := map[string]bool{
		"film.av1.mkv":   true,
		"film.h264.mkv":  true,
		"film.h265.mkv":  true,
		"film.remux.mkv": true,
		"FILM.AV1.MKV":   true, // Gross/klein egal
		// Vom Nutzer umbenannt (26.09.2026) — bleibt ein Ergebnis, wird nie umgewandelt.
		"Beispiel_1080P_OHNE-schalter.av1.mkv": true,
		"film.mkv":                             false,
		"film.mp4":                             false,
		"film.av1.mp4":                         false,
		"av1.mkv":                              false, // nur die Endung ".mkv" mit dem Namen "av1"
	}
	for name, erwartet := range faelle {
		if bekommen := istErgebnisDatei(filepath.Join("Daten", name)); bekommen != erwartet {
			t.Errorf("%s: %v erwartet, bekommen %v", name, erwartet, bekommen)
		}
	}
}

func TestUmpackSuffix(t *testing.T) {
	faelle := map[string]string{
		"h264": ".h264", "avc": ".h264", "hevc": ".h265", "H265": ".h265",
		"av1": ".av1", "mpeg2video": ".remux", "": ".remux",
	}
	for codec, erwartet := range faelle {
		if bekommen := umpackSuffix(codec); bekommen != erwartet {
			t.Errorf("%q: %s erwartet, bekommen %s", codec, erwartet, bekommen)
		}
	}
	e := standardWerte()
	if p := UmpackPfadFuer(filepath.Join("Daten", "film.mp4"), "h264", e); p != filepath.Join("Daten", "output", "film.h264.mkv") {
		t.Errorf("falscher Umpack-Pfad: %s", p)
	}
}

// legeAn erzeugt leere Dateien (samt Ordnern) unter wurzel.
func legeAn(t *testing.T, wurzel string, namen ...string) {
	t.Helper()
	for _, name := range namen {
		pfad := filepath.Join(wurzel, name)
		if err := os.MkdirAll(filepath.Dir(pfad), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pfad, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Die Suche nimmt nur echte Quellen: nichts aus output/originals und keine
// Ergebnisse, auch wenn sie woanders liegen — genau wie NVENCForge.
func TestSucheUebergehtErgebnisse(t *testing.T) {
	wurzel := t.TempDir()
	legeAn(t, wurzel,
		"neu.mp4",
		"verschoben.av1.mkv",         // Ergebnis, das der Nutzer woandershin gelegt hat
		"altbestand.h264.mkv",        // umgepackt
		"output/neu.av1.mkv",         // im output-Ordner
		"originals/frueher.mp4",      // Original
		"unterordner/noch-einer.mkv", // echte Quelle im Unterordner
	)
	gefunden, err := VideoDateienSuchen([]string{wurzel}, standardWerte())
	if err != nil {
		t.Fatal(err)
	}
	var namen []string
	for _, pfad := range gefunden {
		namen = append(namen, filepath.Base(pfad))
	}
	slices.Sort(namen)
	if !slices.Equal(namen, []string{"neu.mp4", "noch-einer.mkv"}) {
		t.Errorf("gefunden: %v", namen)
	}

	// Direkt hineingezogen gilt dasselbe.
	direkt, err := VideoDateienSuchen([]string{filepath.Join(wurzel, "verschoben.av1.mkv")}, standardWerte())
	if err != nil || len(direkt) != 0 {
		t.Errorf("ein hineingezogenes Ergebnis darf nicht gefunden werden: %v (%v)", direkt, err)
	}
}

// Erledigt ist, wofür ein Ergebnis im output-Ordner liegt — umgewandelt oder
// umgepackt. Ein umbenanntes Ergebnis zählt NICHT: so lässt sich eine Datei
// nochmal umwandeln (Nutzerwunsch 26.09.2026).
func TestErledigtNachOrdnern(t *testing.T) {
	wurzel := t.TempDir()
	legeAn(t, wurzel,
		"umgewandelt.mp4", "output/umgewandelt.av1.mkv",
		"umgepackt.mp4", "output/umgepackt.h264.mkv",
		"nochmal.mp4", "output/nochmal_OHNE-schalter.av1.mkv",
		"neu.mp4",
	)
	e := standardWerte()
	pfade := func(namen ...string) []string {
		var p []string
		for _, n := range namen {
			p = append(p, filepath.Join(wurzel, n))
		}
		return p
	}

	offen, erledigt := nachOrdnernAufteilen(pfade("neu.mp4", "nochmal.mp4", "umgepackt.mp4", "umgewandelt.mp4"), e)
	if !slices.Equal(offen, pfade("neu.mp4", "nochmal.mp4")) {
		t.Errorf("offen: %v", offen)
	}
	if !slices.Equal(erledigt, pfade("umgepackt.mp4", "umgewandelt.mp4")) {
		t.Errorf("erledigt: %v", erledigt)
	}
	if got := VorhandenesErgebnis(filepath.Join(wurzel, "umgepackt.mp4"), e); filepath.Base(got) != "umgepackt.h264.mkv" {
		t.Errorf("Ergebnis nicht gefunden: %q", got)
	}
}

func TestUmpackenKopiertDasBild(t *testing.T) {
	e := standardWerte()
	e.VarianceBoost, e.Tune0 = true, true // dürfen beim Umpacken keine Rolle spielen
	auftrag := EncodeAuftrag{Quelle: "q.mp4", Ziel: "z.mkv", Umpacken: true,
		UntertitelCodecs: []string{"subrip"}}
	args := EncodeArgumente(auftrag, e)

	stelle := slices.Index(args, "-c:v")
	if stelle < 0 || args[stelle+1] != "copy" {
		t.Errorf("-c:v copy erwartet: %v", args)
	}
	for _, darfNicht := range []string{"-crf", "-svtav1-params", "-preset", videoCodec} {
		if slices.Contains(args, darfNicht) {
			t.Errorf("%s gehört nicht zum Umpacken: %v", darfNicht, args)
		}
	}
	for _, muss := range []string{"0:a?", "0:s?", "0:t?", "-map_chapters", "-c:s:0"} {
		if !slices.Contains(args, muss) {
			t.Errorf("%s fehlt — Ton, Untertitel und Kapitel müssen mit: %v", muss, args)
		}
	}
}

func TestLaufBilanzZaehlt(t *testing.T) {
	var lauf laufBilanz
	lauf.dazu("a.mp4", DateiErgebnis{Eintrag: Eintrag{Status: StatusErledigt, QuelleBytes: 6e9, ErgebnisBytes: 2e9}, Dauer: 40 * time.Minute})
	lauf.dazu("b.mp4", DateiErgebnis{Eintrag: Eintrag{Status: StatusUmgepackt, QuelleBytes: 1e9, ErgebnisBytes: 0.99e9}, Dauer: 2 * time.Minute})
	lauf.dazu("c.mp4", DateiErgebnis{Eintrag: Eintrag{Status: StatusUebersprungen}, Dauer: time.Second})
	lauf.dazu("d.mp4", DateiErgebnis{Eintrag: Eintrag{Status: StatusFehler, Meldung: "pCloud-Lesefehler"}, Dauer: time.Minute})

	if lauf.umgewandelt != 1 || lauf.umgepackt != 1 || lauf.uebersprungen != 1 || lauf.fehler != 1 {
		t.Errorf("Zählung falsch: %+v", lauf)
	}
	if lauf.gespartBytes != 4e9+0.01e9 {
		t.Errorf("gespart %d erwartet, bekommen %d", int64(4e9+0.01e9), lauf.gespartBytes)
	}
	if lauf.rechenzeit != 43*time.Minute+time.Second {
		t.Errorf("Rechenzeit %v", lauf.rechenzeit)
	}
	if len(lauf.fehlerListe) != 1 || lauf.fehlerListe[0] != "d.mp4 — pCloud-Lesefehler" {
		t.Errorf("Fehlerliste: %v", lauf.fehlerListe)
	}
}

func TestNurUmpackenGruende(t *testing.T) {
	faelle := map[Uebersprungen]bool{
		SchonAV1: true, SchonSchlank: true, KleineAufloesung: true,
		ErgebnisDa: false, ZuKurz: false, ImOriginalOrdner: false, NichtMehrDa: false, NichtUebersprungen: false,
	}
	for grund, erwartet := range faelle {
		if bekommen := (Kandidat{Grund: grund}).NurUmpacken(); bekommen != erwartet {
			t.Errorf("%q: NurUmpacken %v erwartet", grund, erwartet)
		}
	}
	if (Kandidat{Grund: SchonSchlank, Fehler: os.ErrNotExist}).NurUmpacken() {
		t.Error("mit Lesefehler wird nichts umgepackt")
	}
}

// Unter 720p heisst: BEIDE Kanten liegen unter denen von 1280x720. Breitbild
// mit voller 720p-Breite und Hochkant-720p zählen nicht als klein.
func TestUnter720p(t *testing.T) {
	faelle := []struct {
		breite, hoehe int
		klein         bool
	}{
		{720, 404, true}, {720, 540, true}, {960, 540, true}, {720, 576, true},
		{404, 720, true}, // Hochkant, kleiner als 720p
		{1280, 720, false}, {1280, 536, false}, {720, 1280, false},
		{1920, 1080, false}, {3840, 2160, false},
		{0, 0, false}, {720, 0, false}, // Masse unbekannt: nicht als klein werten
	}
	for _, f := range faelle {
		if bekommen := unter720p(f.breite, f.hoehe); bekommen != f.klein {
			t.Errorf("%dx%d: unter720p = %v, erwartet %v", f.breite, f.hoehe, bekommen, f.klein)
		}
	}
}

func TestVorlaufInFlags(t *testing.T) {
	faelle := map[string]bool{
		"K__\n___\n___\n":      false, // normal: Schlüsselbild, dann gewöhnliche Bilder
		"KD_\n_D_\n_D_\nK__\n": true,  // versteckter Vorlauf (gemessen 26.09.2026)
		"":                     false,
		"K_C\n___\n":           false, // beschädigt ist nicht verworfen
	}
	for ausgabe, erwartet := range faelle {
		if bekommen := vorlaufInFlags(ausgabe); bekommen != erwartet {
			t.Errorf("%q: %v erwartet, bekommen %v", ausgabe, erwartet, bekommen)
		}
	}
}
