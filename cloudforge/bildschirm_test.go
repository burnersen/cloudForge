// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSichtbarKuerzenZaehltFarbcodesNicht(t *testing.T) {
	zeile := farbeGruen + "abcdef" + farbeAus

	gekuerzt := sichtbarKuerzen(zeile, 3)
	if sichtbareLaenge(gekuerzt) != 3 {
		t.Errorf("3 sichtbare Zeichen erwartet, bekommen %d in %q", sichtbareLaenge(gekuerzt), gekuerzt)
	}
	if !strings.HasPrefix(gekuerzt, farbeGruen+"abc") || !strings.HasSuffix(gekuerzt, farbeAus) {
		t.Errorf("Farbcode zerschnitten oder nicht zurueckgesetzt: %q", gekuerzt)
	}
	if sichtbarKuerzen(zeile, 6) != zeile {
		t.Errorf("eine passende Zeile darf nicht veraendert werden: %q", sichtbarKuerzen(zeile, 6))
	}
	if sichtbarKuerzen(zeile, 0) != "" {
		t.Error("bei Breite 0 bleibt nichts uebrig")
	}
}

func TestSichtbarKuerzenZaehltBalkenzeichenEinfach(t *testing.T) {
	// █ und ░ sind mehrere Bytes lang, aber nur ein Zeichen breit.
	if n := sichtbareLaenge(sichtbarKuerzen("████░░░░", 5)); n != 5 {
		t.Errorf("5 Zeichen erwartet, bekommen %d", n)
	}
}

// testUebersicht baut eine Anzeige mitten im Umwandeln der 2. von 5 Dateien,
// mit einem langen Verlauf — ohne dass etwas aufs Terminal geschrieben wird.
func testUebersicht(jetzt time.Time) *Anzeige {
	a := NeueAnzeige()
	a.amTerminal = true
	a.uebersicht = &uebersicht{dateiGesamt: 5}
	for i := 1; i <= 12; i++ {
		a.uebersicht.verlauf = append(a.uebersicht.verlauf, fmt.Sprintf(" OK     Film%02d.mp4  5,84 GB → 2,81 GB  (–52 %%)", i))
	}
	a.uebersicht.danachSchaetzung = 2 * time.Hour
	imUmwandeln(a.platz, jetzt, 2, "Beispielfilm_Teil2_1080P.mp4")
	return a
}

// imUmwandeln setzt einen Platz mitten ins Umwandeln einer Datei.
func imUmwandeln(p *platz, jetzt time.Time, nummer int, name string) {
	p.neueDatei(nummer, name)
	p.dateiBeginn = jetzt.Add(-15 * time.Minute)
	p.dateiZeilen = []string{
		"  5,04 GB  |  1080p mit 50 Bildern/s  |  54 Min Film  |  dauert etwa 55 Min",
		"  1/5 Datei holen       5,04 GB (3 Min)",
		"  2/5 Qualitaet messen  gewaehlt CRF 20, erwartet VMAF 98,2 (2 Min)",
	}
	p.quelleBytes = 5840 * mebibyte
	p.dateiSchaetzung = 55 * time.Minute
	p.standMerken(Stand{
		Anteil: 0.453, Position: 1487, Bild: 74350, BilderProSek: 74.8,
		BitrateKbps: 5100, Tempo: 2.47, Rest: 12 * time.Minute, Bytes: 1300 * mebibyte,
	})
	p.schritt, p.schrittName, p.schrittLaeuft = "3/5 Umwandeln", "Umwandeln", true
	p.beginn = jetzt.Add(-10 * time.Minute)
}

// Zwei Dateien gleichzeitig (parallelDateien=2, seit 0.19.0): Beide stehen mit
// Fortschritt da, die Gesamtzeile einmal darunter — auch im kleinen Fenster.
func TestUebersichtMitZweiDateien(t *testing.T) {
	jetzt := time.Now()
	a := testUebersicht(jetzt)
	a.platz.dateiName = "" // der Platz für Meldungen ohne Datei
	plaetze := a.Plaetze(2)
	imUmwandeln(plaetze[0].platz, jetzt, 3, "Erster_Film.mp4")
	imUmwandeln(plaetze[1].platz, jetzt, 4, "Zweiter_Film.mp4")
	a.uebersicht.fertig = 2

	text := strings.Join(a.uebersichtZeilen(80, 24, jetzt), "\n")
	for _, teil := range []string{"Erster_Film.mp4", "Zweiter_Film.mp4", "[3/5]", "[4/5]",
		"2 gleichzeitig, 2 von 5 fertig", "(2/5 fertig)", "Gesamt", "Aufhoeren"} {
		if !strings.Contains(text, teil) {
			t.Errorf("%q fehlt:\n%s", teil, text)
		}
	}
	if n := strings.Count(text, "Position"); n != 2 {
		t.Errorf("je Datei ein Fortschritt erwartet, %d gefunden:\n%s", n, text)
	}
	if n := strings.Count(text, "Gesamt"); n != 1 {
		t.Errorf("die Gesamtzeile gehoert genau einmal darunter, %d gefunden:\n%s", n, text)
	}
	for _, masse := range [][2]int{{20, 5}, {80, 14}, {120, 40}} {
		if zeilen := a.uebersichtZeilen(masse[0], masse[1], jetzt); len(zeilen) > masse[1] {
			t.Errorf("%dx%d: %d Zeilen passen nicht", masse[0], masse[1], len(zeilen))
		}
	}

	// Ist eine der beiden fertig und nichts mehr offen, sieht der Rest aus
	// wie mit einer einzigen Datei — mit der Zahl der fertigen Dateien.
	plaetze[0].PlatzFrei()
	text = strings.Join(a.uebersichtZeilen(80, 24, jetzt), "\n")
	if strings.Contains(text, "Erster_Film.mp4") || !strings.Contains(text, "Datei 4 von 5") {
		t.Errorf("nur noch die zweite Datei erwartet:\n%s", text)
	}
}

// Laufen mehrere Dateien, steht vor jeder Protokollzeile, zu welcher sie gehört.
func TestVorsilbeNurBeiMehrerenDateien(t *testing.T) {
	a := NeueAnzeige()
	a.Datei(1, 3, "a.mp4")
	if text := a.mitVorsilbe("  2/5 Qualitaet messen"); text != "  2/5 Qualitaet messen" {
		t.Errorf("mit einer Datei keine Vorsilbe erwartet: %q", text)
	}

	b := a.Plaetze(2)[1]
	b.Datei(2, 3, "b.mp4")
	if text := b.mitVorsilbe("  FERTIG\n\n  Ergebnis: x"); text != "[2/3]   FERTIG\n\n[2/3]   Ergebnis: x" {
		t.Errorf("Vorsilbe vor jeder nicht leeren Zeile erwartet: %q", text)
	}
}

func TestUebersichtPasstInJedesFenster(t *testing.T) {
	jetzt := time.Now()
	a := testUebersicht(jetzt)

	for _, masse := range [][2]int{{20, 5}, {40, 10}, {80, 14}, {80, 24}, {120, 40}, {200, 60}} {
		breite, hoehe := masse[0], masse[1]
		zeilen := a.uebersichtZeilen(breite, hoehe, jetzt)
		if len(zeilen) > hoehe {
			t.Errorf("%dx%d: %d Zeilen passen nicht in %d", breite, hoehe, len(zeilen), hoehe)
		}
		if len(zeilen) == 0 {
			t.Errorf("%dx%d: leere Uebersicht", breite, hoehe)
		}
	}
}

func TestUebersichtZeigtFortschrittWieNVENCForge(t *testing.T) {
	jetzt := time.Now()
	text := strings.Join(testUebersicht(jetzt).uebersichtZeilen(80, 24, jetzt), "\n")

	for _, teil := range []string{
		"CloudForge " + appVersion, "Datei 2 von 5",
		"█", "45,3 %", "Position", "0:24:47", "Laufzeit", "0:10:00", "Rest", "0:12:00",
		"Bilder/s", "74,8", "Bitrate", "5,1 Mbit", "Tempo", "2,47x",
		"5840 MB", "kleiner", "Gesamt", "(2/5)", "noch",
	} {
		if !strings.Contains(text, teil) {
			t.Errorf("%q fehlt in der Uebersicht:\n%s", teil, text)
		}
	}
}

func TestFortschrittPasstIn80Spalten(t *testing.T) {
	// Die Standardbreite eines Terminals: dort darf vom Fortschritt nichts
	// abgeschnitten werden.
	jetzt := time.Now()
	a := testUebersicht(jetzt)
	for _, zeile := range a.fortschrittsBlock(80, jetzt) {
		if n := sichtbareLaenge(zeile); n > 79 {
			t.Errorf("Zeile hat %d Zeichen, passt nicht in 80 Spalten: %q", n, zeile)
		}
	}
}

func TestKleinesFensterBehaeltDenFortschritt(t *testing.T) {
	// Wird das Fenster klein gezogen, fällt zuerst der Verlauf weg — der
	// Fortschritt muss bleiben.
	jetzt := time.Now()
	a := testUebersicht(jetzt)
	// Viele Zeilen für die laufende Datei — mehr als das Fenster fasst. Sie
	// dürfen den Fortschritt nicht nach unten hinausschieben.
	for i := 0; i < 10; i++ {
		a.dateiZeile(fmt.Sprintf("  Zusatzzeile %d", i))
	}
	text := strings.Join(a.uebersichtZeilen(80, 14, jetzt), "\n")
	for _, teil := range []string{"45,3 %", "Position", "Tempo", "Gesamt", "Aufhoeren"} {
		if !strings.Contains(text, teil) {
			t.Errorf("%q fehlt im kleinen Fenster:\n%s", teil, text)
		}
	}
	if strings.Contains(text, "Film01.mp4") {
		t.Errorf("der aelteste Verlauf haette zuerst wegfallen muessen:\n%s", text)
	}
}

func TestUebersichtBeimMessen(t *testing.T) {
	jetzt := time.Now()
	a := testUebersicht(jetzt)
	a.neuerSchritt() // Messen hat keinen Prozentwert
	a.schritt = "2/5 Qualitaet messen"
	a.messungen = []string{"CRF 16 = 99,0", "CRF 26 = 97,0"}
	a.laufendeMessung = 21

	text := strings.Join(a.uebersichtZeilen(80, 24, jetzt), "\n")
	for _, teil := range []string{"Messungen", "CRF 16 = 99,0", "misst CRF 21"} {
		if !strings.Contains(text, teil) {
			t.Errorf("%q fehlt:\n%s", teil, text)
		}
	}
	if strings.Contains(text, "Position") {
		t.Errorf("beim Messen gibt es keine Filmposition:\n%s", text)
	}
}

func TestUebersichtBeimKopieren(t *testing.T) {
	jetzt := time.Now()
	a := testUebersicht(jetzt)
	a.neuerSchritt()
	a.standMerken(Stand{Anteil: 0.5, Rest: 2 * time.Minute, Text: "2,92 GB von 5,84 GB"})

	text := strings.Join(a.uebersichtZeilen(80, 24, jetzt), "\n")
	for _, teil := range []string{"50,0 %", "Menge", "2,92 GB von 5,84 GB"} {
		if !strings.Contains(text, teil) {
			t.Errorf("%q fehlt:\n%s", teil, text)
		}
	}
}

func TestNachDemUmwandelnKeineRestzeitMehrFuerDieDatei(t *testing.T) {
	// Nach dem Umwandeln dauern Prüfen und Ablegen Sekunden. Die alte
	// Schätzung für die ganze Datei darf die Gesamtzeit dann nicht aufblähen.
	jetzt := time.Now()
	a := testUebersicht(jetzt)
	a.dateiSchaetzung = 5 * time.Hour // absichtlich viel zu hoch
	a.neuerSchritt()                  // Umwandeln ist vorbei

	if zeile := a.gesamtZeile(jetzt); !strings.Contains(zeile, "ca. 2 Std 0 Min") {
		t.Errorf("nur noch die 2 Std der folgenden Dateien erwartet: %q", zeile)
	}
}

func TestVerlaufKuerzen(t *testing.T) {
	verlauf := []string{"a", "b", "c", "d", "e", "f"}

	if z := verlaufKuerzen(verlauf, 20); len(z) != 7 || z[6] != "" {
		t.Errorf("alles plus Leerzeile erwartet, bekommen %q", z)
	}
	gekuerzt := verlaufKuerzen(verlauf, 5)
	if len(gekuerzt) != 5 || !strings.Contains(gekuerzt[0], "3 weitere") || gekuerzt[3] != "f" {
		t.Errorf("Hinweis + die 3 neuesten + Leerzeile erwartet, bekommen %q", gekuerzt)
	}
	if z := verlaufKuerzen(verlauf, 1); z != nil {
		t.Errorf("bei zu wenig Platz nichts erwartet, bekommen %q", z)
	}
}

func TestZeitText(t *testing.T) {
	faelle := map[time.Duration]string{
		0:                               "0:00:00",
		-5 * time.Second:                "0:00:00",
		3725 * time.Second:              "1:02:05",
		24*time.Minute + 47*time.Second: "0:24:47",
	}
	for dauer, erwartet := range faelle {
		if bekommen := zeitText(dauer); bekommen != erwartet {
			t.Errorf("%v: %q erwartet, %q bekommen", dauer, erwartet, bekommen)
		}
	}
}

func TestGlaettePrognose(t *testing.T) {
	if p := glaettePrognose(0, 500, 0.02); p != 500 {
		t.Errorf("ohne bisherigen Wert zaehlt der neue: %v", p)
	}
	if p := glaettePrognose(100, 500, 1); p != 500 {
		t.Errorf("am Ende zaehlt nur der neue Wert: %v", p)
	}
	// Ganz am Anfang mindestens 15 % Gewicht, sonst klebt die Anzeige fest.
	if p := glaettePrognose(100, 500, 0.02); p != 160 {
		t.Errorf("160 erwartet (15 %% Gewicht), bekommen %v", p)
	}
}

func TestKopfZeileRechtsbuendig(t *testing.T) {
	zeile := kopfZeile(80, "Datei 2 von 5")
	if n := sichtbareLaenge(zeile); n != 78 {
		t.Errorf("78 sichtbare Zeichen erwartet, bekommen %d: %q", n, zeile)
	}
	if !strings.HasSuffix(zeile, "Datei 2 von 5") {
		t.Errorf("Dateizaehler gehoert ans rechte Ende: %q", zeile)
	}
}
