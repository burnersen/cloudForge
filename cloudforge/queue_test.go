package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fertigeUmwandlung baut einen Eintrag, der mit so vielen Bildern je Sekunde lief.
func fertigeUmwandlung(bilderProSek float64) Eintrag {
	return Eintrag{
		Status:        StatusErledigt,
		QuelleBytes:   6e9,
		ErgebnisBytes: 2e9,
		Bilder:        int64(bilderProSek * 2000),
		RechenSek:     2000,
	}
}

func testZustand(t *testing.T) *Zustand {
	t.Helper()
	return &Zustand{Fassung: zustandFassung, pfad: filepath.Join(t.TempDir(), "cloudforge-zustand.json")}
}

func TestErfahrungOhneTempoNimmtStartwerte(t *testing.T) {
	if x := testZustand(t).Erfahrung(); x != startErfahrung {
		t.Errorf("Startwerte erwartet, bekommen %+v", x)
	}
}

func TestErfahrungNimmtDenMedian(t *testing.T) {
	z := testZustand(t)
	// Eine Datei mit Netzstörung (10) darf den Median nicht verbiegen.
	for _, tempo := range []float64{50, 60, 70, 80, 10} {
		z.Tempo = append(z.Tempo, TempoProbe{Bilder: int64(tempo * 2000), QuelleBytes: 6e9, RechenSek: 2000})
	}
	x := z.Erfahrung()
	if x.BilderProSek != 60 || x.Dateien != 5 {
		t.Errorf("Median 60 aus 5 Dateien erwartet, bekommen %v aus %d", x.BilderProSek, x.Dateien)
	}
	if math.Abs(x.BytesProSek-3e6) > 1 {
		t.Errorf("3 MB/s erwartet (6 GB in 2000 s), bekommen %v", x.BytesProSek)
	}
}

// Die Gesamtbilanz zählt Umgewandeltes und Umgepacktes; das Tempo nur echte
// Umwandlungen, und davon nur die letzten fünf.
func TestDateiFertigZaehltSummenUndTempo(t *testing.T) {
	z := testZustand(t)
	for _, tempo := range []float64{500, 500, 50, 60, 70, 80, 90} {
		if err := z.DateiFertig(fertigeUmwandlung(tempo)); err != nil {
			t.Fatal(err)
		}
	}
	umgepackt := Eintrag{Status: StatusUmgepackt, QuelleBytes: 1e9, ErgebnisBytes: 1.01e9, RechenSek: 60}
	if err := z.DateiFertig(umgepackt); err != nil {
		t.Fatal(err)
	}
	for _, zaehltNicht := range []Eintrag{
		{Status: StatusUebersprungen, QuelleBytes: 5e9},
		{Status: StatusFehler, QuelleBytes: 5e9, RechenSek: 99},
	} {
		if err := z.DateiFertig(zaehltNicht); err != nil {
			t.Fatal(err)
		}
	}

	if z.GesamtDateien != 8 {
		t.Errorf("8 Dateien erwartet (7 umgewandelt + 1 umgepackt), bekommen %d", z.GesamtDateien)
	}
	if want := int64(7*4e9 - 0.01e9); z.GesamtGespartBytes != want {
		t.Errorf("gespart %d erwartet (das Umpacken kostete 10 MB), bekommen %d", want, z.GesamtGespartBytes)
	}
	if z.GesamtRechenSek != 7*2000+60 {
		t.Errorf("Rechenzeit %v erwartet, bekommen %v", 7*2000+60, z.GesamtRechenSek)
	}
	if len(z.Tempo) != erfahrungAusLetzten {
		t.Fatalf("%d Tempo-Proben erwartet, bekommen %d", erfahrungAusLetzten, len(z.Tempo))
	}
	if x := z.Erfahrung(); x.BilderProSek != 70 {
		t.Errorf("Median der letzten fünf (50..90) = 70 erwartet, bekommen %v — die alten 500 zählen nicht mehr", x.BilderProSek)
	}

	// Gespeichert wird sofort — und nur Summen und Zahlen, keine Namen.
	neu, err := ZustandLaden(z.pfad)
	if err != nil || neu.GesamtDateien != 8 || len(neu.Tempo) != erfahrungAusLetzten {
		t.Errorf("nicht richtig gespeichert: %+v (%v)", neu, err)
	}
}

// Die Dateiliste bis 0.10.0 wird beim ersten Laden umgerechnet: Summen und
// Tempo bleiben, die Liste mit Namen, Zeiten und Fehlern verschwindet.
func TestAlteDateilisteWirdUmgerechnet(t *testing.T) {
	pfad := filepath.Join(t.TempDir(), "cloudforge-zustand.json")
	um := func(stunde int) time.Time { return time.Date(2026, 9, 26, stunde, 0, 0, 0, time.Local) }
	alt := struct {
		Fassung   int                        `json:"fassung"`
		Eintraege map[string]eintragFassung1 `json:"eintraege"`
	}{Fassung: 1, Eintraege: map[string]eintragFassung1{
		// Aus der Zeit vor 0.7.0: ohne Zeitmessung — zählt nur zur Ersparnis.
		"/Daten/alt.mp4": {Status: StatusErledigt, Zeitpunkt: um(1), QuelleBytes: 6e9, ErgebnisBytes: 2e9},
		// Sechs mit Zeitmessung, die älteste fällt aus dem Tempo heraus.
		"/Daten/t1.mp4": {Status: StatusErledigt, Zeitpunkt: um(2), QuelleBytes: 6e9, ErgebnisBytes: 2e9, Bilder: 1000000, RechenSek: 2000},
		"/Daten/t2.mp4": {Status: StatusErledigt, Zeitpunkt: um(3), QuelleBytes: 6e9, ErgebnisBytes: 2e9, Bilder: 100000, RechenSek: 2000},
		"/Daten/t3.mp4": {Status: StatusErledigt, Zeitpunkt: um(4), QuelleBytes: 6e9, ErgebnisBytes: 2e9, Bilder: 120000, RechenSek: 2000},
		"/Daten/t4.mp4": {Status: StatusErledigt, Zeitpunkt: um(5), QuelleBytes: 6e9, ErgebnisBytes: 2e9, Bilder: 140000, RechenSek: 2000},
		"/Daten/t5.mp4": {Status: StatusErledigt, Zeitpunkt: um(6), QuelleBytes: 6e9, ErgebnisBytes: 2e9, Bilder: 160000, RechenSek: 2000},
		"/Daten/t6.mp4": {Status: StatusErledigt, Zeitpunkt: um(7), QuelleBytes: 6e9, ErgebnisBytes: 2e9, Bilder: 180000, RechenSek: 2000},
		// Weder Fehler noch Übersprungenes zählen zur Bilanz.
		"/Daten/Fehlerfilm_1.mp4": {Status: StatusFehler, Zeitpunkt: um(8), QuelleBytes: 5e9},
		"/Daten/duenn.mp4":        {Status: StatusUebersprungen, Zeitpunkt: um(9), QuelleBytes: 2e9},
	}}
	inhalt, err := json.Marshal(alt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pfad, inhalt, 0o644); err != nil {
		t.Fatal(err)
	}

	z, err := ZustandLaden(pfad)
	if err != nil {
		t.Fatalf("alte Datei nicht ladbar: %v", err)
	}
	if z.GesamtDateien != 7 || z.GesamtGespartBytes != 7*4e9 || z.GesamtRechenSek != 6*2000 {
		t.Errorf("Summen falsch: %d Dateien, %d gespart, %v s", z.GesamtDateien, z.GesamtGespartBytes, z.GesamtRechenSek)
	}
	if len(z.Tempo) != 5 || z.Tempo[0].Bilder != 100000 || z.Tempo[4].Bilder != 180000 {
		t.Errorf("Tempo = die 5 jüngsten, älteste zuerst, erwartet; bekommen %+v", z.Tempo)
	}

	neu, err := os.ReadFile(pfad)
	if err != nil {
		t.Fatal(err)
	}
	for _, darfNichtMehrDrinSein := range []string{"eintraege", ".mp4", "Fehlerfilm", "zeitpunkt"} {
		if strings.Contains(string(neu), darfNichtMehrDrinSein) {
			t.Errorf("%q steht noch in der umgestellten Datei:\n%s", darfNichtMehrDrinSein, neu)
		}
	}
}

func TestErfahrungSchaetztDauer(t *testing.T) {
	x := Erfahrung{BilderProSek: 50, BytesProSek: 2e6}
	if d := x.DauerFuerBilder(90000); d != 30*time.Minute {
		t.Errorf("30 Min erwartet, bekommen %v", d)
	}
	if d := x.DauerFuerBytes(6e9); d != 50*time.Minute {
		t.Errorf("50 Min erwartet, bekommen %v", d)
	}
	if x.DauerFuerBilder(0) != 0 || (Erfahrung{}).DauerFuerBilder(1000) != 0 {
		t.Error("ohne Bilder oder ohne Tempo gibt es keine Schaetzung")
	}
}

func TestErfahrungLesenFasstKaputteDateiNichtAn(t *testing.T) {
	// -bericht darf nichts verändern — auch keine kaputte Zustandsdatei
	// beiseitelegen, wie es ZustandLaden tut.
	pfad := filepath.Join(t.TempDir(), "cloudforge-zustand.json")
	if err := os.WriteFile(pfad, []byte("{kaputt"), 0o644); err != nil {
		t.Fatal(err)
	}

	if x := ErfahrungLesen(pfad); x != startErfahrung {
		t.Errorf("Startwerte erwartet, bekommen %+v", x)
	}
	if inhalt, err := os.ReadFile(pfad); err != nil || string(inhalt) != "{kaputt" {
		t.Errorf("die Datei wurde angefasst: %q, %v", inhalt, err)
	}
	if _, err := os.Stat(pfad + ".unlesbar"); err == nil {
		t.Error("die Datei wurde beiseitegelegt")
	}
}

// -bericht liest auch eine alte Dateiliste — stellt sie aber nicht um.
func TestErfahrungLesenStelltAlteDateiNichtUm(t *testing.T) {
	pfad := filepath.Join(t.TempDir(), "cloudforge-zustand.json")
	alt := `{"fassung":1,"eintraege":{"/Daten/a.mp4":{"status":"erledigt","quelleBytes":6000000000,` +
		`"ergebnisBytes":2000000000,"bilder":120000,"rechenSek":2000}}}`
	if err := os.WriteFile(pfad, []byte(alt), 0o644); err != nil {
		t.Fatal(err)
	}
	if x := ErfahrungLesen(pfad); x.BilderProSek != 60 || x.Dateien != 1 {
		t.Errorf("60 Bilder/s aus 1 Datei erwartet, bekommen %+v", x)
	}
	if inhalt, _ := os.ReadFile(pfad); string(inhalt) != alt {
		t.Error("ErfahrungLesen hat die alte Datei umgeschrieben")
	}
}

func TestStundeFilmTextMitStartwerten(t *testing.T) {
	// 54 Bilder/s: 30 Bilder/s -> 2000 s, 50 Bilder/s -> 3333 s.
	text := stundeFilmText(startErfahrung)
	for _, teil := range []string{"33 Min", "55 Min", "Startwert"} {
		if !strings.Contains(text, teil) {
			t.Errorf("%q fehlt in %q", teil, text)
		}
	}
}
