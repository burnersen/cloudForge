// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"context"
	"math"
	"sort"
	"strings"
	"testing"
)

// quelleTestBytes: so gross ist die Quelle an den Messstellen in den Tests.
const quelleTestBytes = 100e6

// linear liefert zu einem CRF den Wert zwischen festen Stützpunkten.
func linear(punkte map[int]float64, crf int) float64 {
	crfs := make([]int, 0, len(punkte))
	for c := range punkte {
		crfs = append(crfs, c)
	}
	sort.Ints(crfs)
	if crf <= crfs[0] {
		return punkte[crfs[0]]
	}
	for i := 1; i < len(crfs); i++ {
		if crf <= crfs[i] {
			a, b := crfs[i-1], crfs[i]
			return punkte[a] + (punkte[b]-punkte[a])*float64(crf-a)/float64(b-a)
		}
	}
	return punkte[crfs[len(crfs)-1]]
}

// quellKurve baut eine Probemessung mit VMAF aus Stützpunkten und einer
// Grösse, die je CRF-Stufe um einen festen Faktor fällt — wie gemessen.
func quellKurve(vmaf map[int]float64, anteilBei19, faktorJeStufe float64) probeMessung {
	return func(crf int) (float64, int64, error) {
		anteil := anteilBei19 * math.Pow(faktorJeStufe, float64(crf-19))
		return linear(vmaf, crf), int64(anteil * quelleTestBytes), nil
	}
}

// duenneQuelle: eine dünne Web-Quelle vom 25.09.2026 (1080p25, 2,6 Mbit/s).
// Gemessen: CRF 19 = 118 % der Quelle, CRF 32 = 49 % -> Faktor 0,935 je Stufe.
var duenneQuelle = quellKurve(
	map[int]float64{16: 98.6, 19: 97.9, 24: 95.9, 28: 94.1, 32: 91.7, 36: 88.7, 40: 85.3, 44: 82.0},
	1.18, 0.935)

func deckelSuche(probe probeMessung, deckel float64) *crfSuche {
	e := standardWerte()
	// Fest, nicht die Werkswerte: die Erwartungen unten sind für Ziel 97 und
	// Anker 16/26 gerechnet (mit dem Werkswert 96 seit 0.11.3 fiel
	// TestDeckelAusOderQuelleUnbekannt; seit 0.15.0 sind die Werksanker 22/32).
	e.ZielVMAF = 97
	e.AnkerNiedrig, e.AnkerHoch = 16, 26
	e.KostenDeckelProzent = deckel
	return &crfSuche{ctx: context.Background(), e: e, probe: probe, quelleBytes: quelleTestBytes}
}

func TestDeckelGreiftBeiDuennerQuelle(t *testing.T) {
	s := deckelSuche(duenneQuelle, 50)
	erg, err := s.bestimmen()
	if err != nil {
		t.Fatalf("unerwarteter Fehler: %v", err)
	}
	if !erg.Gedeckelt || erg.AnteilQuelle > 0.5 {
		t.Fatalf("Deckel 50 %% haette greifen muessen: CRF %d, Anteil %.2f, gedeckelt %v",
			erg.CRF, erg.AnteilQuelle, erg.Gedeckelt)
	}
	// Der niedrigste CRF unter dem Deckel — eine Stufe besser wäre zu teuer.
	if erg.CRF != 32 {
		t.Errorf("CRF 32 erwartet (49 %% der Quelle), bekommen CRF %d (%.0f %%)", erg.CRF, erg.AnteilQuelle*100)
	}
	if len(erg.Messungen) > maxMessungen {
		t.Errorf("%d Messungen, erlaubt sind %d", len(erg.Messungen), maxMessungen)
	}
	if !strings.Contains(erg.Hinweis, "Gedeckelt") {
		t.Errorf("der Hinweis soll den Deckel erklaeren: %q", erg.Hinweis)
	}
}

func TestDeckelFindetBestenCRFAuchNachWeitemSprung(t *testing.T) {
	// Bis CRF 26 fällt die Grösse kaum, danach steil. Die erste Hochrechnung
	// aus den flachen Punkten schiesst deshalb weit über das Ziel hinaus
	// (bis crfMax). Danach muss eingegrenzt werden — auf den BESTEN CRF unter
	// dem Deckel, nicht auf den sparsamsten.
	knick := func(crf int) (float64, int64, error) {
		anteil := 1.2 * math.Pow(0.97, float64(min(crf, 26)-19))
		if crf > 26 {
			anteil *= math.Pow(0.8, float64(crf-26))
		}
		vmaf := linear(map[int]float64{16: 98.6, 19: 97.9, 26: 96.0, 44: 80.0}, crf)
		return vmaf, int64(anteil * quelleTestBytes), nil
	}
	erg, err := deckelSuche(knick, 50).bestimmen()
	if err != nil {
		t.Fatal(err)
	}
	// CRF 28 = 62 % der Quelle, CRF 29 = 49,7 %: 29 ist der beste unter 50 %.
	if erg.CRF != 29 || !erg.Gedeckelt {
		t.Errorf("CRF 29 erwartet, bekommen CRF %d (%.1f %%, gedeckelt %v)", erg.CRF, erg.AnteilQuelle*100, erg.Gedeckelt)
	}
	if len(erg.Messungen) > maxMessungen {
		t.Errorf("%d Messungen, erlaubt sind %d", len(erg.Messungen), maxMessungen)
	}
}

func TestDeckelSprungSpartProbenBeiGleichemErgebnis(t *testing.T) {
	// Der normale Weg (erst Qualität, dann Deckel) zum Vergleich.
	normal := deckelSuche(duenneQuelle, 50)
	vorher, err := normal.suchen()
	if err != nil {
		t.Fatal(err)
	}
	langsam, err := normal.deckeln(vorher)
	if err != nil {
		t.Fatal(err)
	}

	schnell, err := deckelSuche(duenneQuelle, 50).bestimmen()
	if err != nil {
		t.Fatal(err)
	}
	if schnell.CRF != langsam.CRF || !schnell.Gedeckelt {
		t.Errorf("gleiches Ergebnis erwartet: Sprung CRF %d, normal CRF %d", schnell.CRF, langsam.CRF)
	}
	if len(schnell.Messungen) >= len(langsam.Messungen) {
		t.Errorf("der Sprung soll Proben sparen: %d statt %d", len(schnell.Messungen), len(langsam.Messungen))
	}
	if !strings.Contains(schnell.Hinweis, "schon CRF 26") {
		t.Errorf("der Hinweis soll den Sprung erklaeren: %q", schnell.Hinweis)
	}
}

func TestKeinSprungWennDerSparsameAnkerDasZielHaelt(t *testing.T) {
	// Hält CRF 26 das Ziel schon, kann die Qualitätswahl JENSEITS von 26
	// landen und billiger sein — ein Sprung wäre dann falsch.
	gut := quellKurve(map[int]float64{16: 99.5, 26: 98.5, 30: 98.0, 31: 97.8, 44: 90}, 1.18, 0.935)
	s := deckelSuche(gut, 50)
	if _, gesprungen, err := s.deckelSprung(); err != nil || gesprungen {
		t.Errorf("kein Sprung erwartet (err %v)", err)
	}
}

func TestDeckelLaesstFetteQuelleInRuhe(t *testing.T) {
	// Ein Film mit 12 Mbit/s: für VMAF 98 nur ~42 % der Quelle. Der Deckel darf
	// nichts ändern und keine einzige zusätzliche Messung kosten.
	fett := quellKurve(map[int]float64{16: 99.0, 20: 98.2, 21: 98.0, 26: 97.0, 44: 85}, 0.46, 0.92)

	ohne, err := deckelSuche(fett, 0).bestimmen()
	if err != nil {
		t.Fatal(err)
	}
	mit, err := deckelSuche(fett, 50).bestimmen()
	if err != nil {
		t.Fatal(err)
	}
	if mit.Gedeckelt || mit.CRF != ohne.CRF || len(mit.Messungen) != len(ohne.Messungen) {
		t.Errorf("Deckel hat eingegriffen: CRF %d statt %d, %d statt %d Messungen",
			mit.CRF, ohne.CRF, len(mit.Messungen), len(ohne.Messungen))
	}
	if mit.AnteilQuelle <= 0 || mit.AnteilQuelle > 0.5 {
		t.Errorf("Anteil um 42 %% erwartet, bekommen %.2f", mit.AnteilQuelle)
	}
}

func TestDeckelNichtEinhaltbarLaesstQualitaetswahlStehen(t *testing.T) {
	// Selbst CRF 44 kostet noch 86 % der Quelle: der Deckel ist nicht
	// einzuhalten. Dann bleibt die Qualitätswahl (wie NVENCForge) — und
	// die Mindestersparnis sortiert die Datei danach aus.
	stur := quellKurve(map[int]float64{16: 98.6, 19: 97.9, 26: 96.0, 44: 90.0}, 1.4, 0.98)
	ohne, err := deckelSuche(stur, 0).bestimmen()
	if err != nil {
		t.Fatal(err)
	}
	mit, err := deckelSuche(stur, 50).bestimmen()
	if err != nil {
		t.Fatal(err)
	}
	if mit.Gedeckelt || mit.CRF != ohne.CRF {
		t.Errorf("Qualitaetswahl CRF %d erwartet, bekommen CRF %d (gedeckelt %v)", ohne.CRF, mit.CRF, mit.Gedeckelt)
	}
	if !strings.Contains(mit.Hinweis, "nicht einhaltbar") {
		t.Errorf("der Hinweis soll erklaeren, dass der Deckel nicht geht: %q", mit.Hinweis)
	}
	// Seit 0.11.1 wird der Hinweis angezeigt — dafür muss das Ergebnis es sagen.
	if !mit.DeckelNichtEinhaltbar {
		t.Error("DeckelNichtEinhaltbar muss gesetzt sein, sonst sieht der Nutzer den Grund nicht")
	}
	if ohne.DeckelNichtEinhaltbar {
		t.Error("ohne Deckel kann er auch nicht uneinhaltbar sein")
	}
	if mit.AnteilQuelle <= 1 {
		t.Errorf("der Anteil der Qualitaetswahl (ueber 100 %%) muss erhalten bleiben: %.2f", mit.AnteilQuelle)
	}
	if erwartet, ok := erwarteteErsparnisProzent(VideoInfo{GroesseBytes: 1e9}, mit.AnteilQuelle); !ok || erwartet >= 30 {
		t.Errorf("diese Datei muss vor dem Umwandeln aussortiert werden, Ersparnis %.0f %%", erwartet)
	}
}

func TestDeckelAusOderQuelleUnbekannt(t *testing.T) {
	aus, err := deckelSuche(duenneQuelle, 0).bestimmen()
	if err != nil {
		t.Fatal(err)
	}
	if aus.Gedeckelt || aus.AnteilQuelle <= 1 {
		t.Errorf("Deckel 0 = aus: nicht gedeckelt, Anteil ueber 100 %% erwartet: %v, %.2f", aus.Gedeckelt, aus.AnteilQuelle)
	}

	s := deckelSuche(duenneQuelle, 50)
	s.quelleBytes = 0 // ffprobe konnte die Quelle an den Stellen nicht lesen
	unbekannt, err := s.bestimmen()
	if err != nil {
		t.Fatal(err)
	}
	if unbekannt.Gedeckelt || unbekannt.AnteilQuelle != 0 {
		t.Errorf("ohne Quellgroesse weder Deckel noch Anteil: %v, %.2f", unbekannt.Gedeckelt, unbekannt.AnteilQuelle)
	}
}

func TestCrfFuerBytes(t *testing.T) {
	// Genau exponentiell: CRF 20 = 100 MB, CRF 30 = 50 MB -> 50 MB bei 30,
	// 40 MB etwas über 33, aufgerundet 34.
	a := Messung{CRF: 20, Bytes: 100e6}
	b := Messung{CRF: 30, Bytes: 50e6}
	if c := crfFuerBytes(a, b, 50e6); c != 30 {
		t.Errorf("CRF 30 erwartet, bekommen %d", c)
	}
	if c := crfFuerBytes(a, b, 40e6); c != 34 {
		t.Errorf("CRF 34 erwartet (aufgerundet), bekommen %d", c)
	}
	// Unbrauchbare Steigung: zwei Stufen weiter statt Unsinn.
	if c := crfFuerBytes(Messung{CRF: 20, Bytes: 50e6}, Messung{CRF: 30, Bytes: 60e6}, 40e6); c != 32 {
		t.Errorf("CRF 32 erwartet, bekommen %d", c)
	}
	if c := crfFuerBytes(Messung{}, b, 40e6); c != 32 {
		t.Errorf("ohne zweiten Punkt CRF 32 erwartet, bekommen %d", c)
	}
}

func TestFensterBytesAusPaketen(t *testing.T) {
	// Die Quelle beginnt bei 1,4 s. Fenster ab 10 s Filmzeit = ab 11,4 s
	// Paketzeit, 2 s lang.
	ausgabe := strings.Join([]string{
		"packet,11.300000,11.200000,1000", // 9,9 s Filmzeit - davor
		"packet,11.400000,11.300000,2000", // genau am Anfang - zählt
		"packet,N/A,12.000000,3000",       // ohne pts: dts zählt
		"packet,13.399000,13.300000,4000", // kurz vor dem Ende - zählt
		"packet,13.400000,13.300000,5000", // genau am Ende - nicht mehr
		"packet,12.000000,11.900000,N/A",  // unlesbare Grösse
		"format,1.400000",                 // kommt bei ffprobe erst am Schluss
	}, "\n")
	if b := fensterBytesAusPaketen(ausgabe, []Fenster{{StartSek: 10, LaengeSek: 2}}); b != 9000 {
		t.Errorf("9000 Bytes erwartet, bekommen %d", b)
	}
}

// Dicht beieinanderliegende Stellen: ffprobe springt für jede an das
// Schlüsselbild davor und liefert dieselben Pakete zweimal. Sie dürfen nur
// einmal zählen (Fund vom 27.09.2026: doppelt so viel Quelle gezählt).
func TestFensterBytesAusPaketenZaehltDoppelteNurEinmal(t *testing.T) {
	ersterBereich := []string{
		"packet,10.000000,9.960000,1000",
		"packet,12.000000,11.960000,2000",
		"packet,21.000000,20.960000,3000",
	}
	zweiterBereich := []string{ // ab dem Schlüsselbild bei 10 s nochmal gelesen
		"packet,10.000000,9.960000,1000",
		"packet,12.000000,11.960000,2000",
		"packet,21.000000,20.960000,3000",
		"packet,22.000000,21.960000,4000",
	}
	ausgabe := strings.Join(append(append(ersterBereich, zweiterBereich...), "format,0.000000"), "\n")
	fenster := []Fenster{{StartSek: 11, LaengeSek: 8}, {StartSek: 21, LaengeSek: 8}}
	if b := fensterBytesAusPaketen(ausgabe, fenster); b != 2000+3000+4000 {
		t.Errorf("9000 Bytes erwartet (jedes Paket einmal), bekommen %d", b)
	}
}

// Das Leseende muss ABSOLUT dastehen: "START%+DAUER" zählt ffprobe ab dem
// Schlüsselbild vor START und hörte bis 0.11.1 mitten im Fenster auf.
func TestLeseBereicheMitAbsolutemEnde(t *testing.T) {
	fenster := []Fenster{{StartSek: 344.7261, LaengeSek: 8}, {StartSek: 1, LaengeSek: 8}}
	// Datei beginnt bei 0: je 3 s Rand, vorne nie unter 0.
	if b := leseBereiche(fenster, 0); b != "341.726%355.726,0.000%12.000" {
		t.Errorf("bekommen %q", b)
	}
	// TS-Mitschnitt, der bei 600 s beginnt: alles um den Beginn verschoben,
	// genau wie ffmpeg "-ss" zählt.
	if b := leseBereiche(fenster[:1], 600); b != "941.726%955.726" {
		t.Errorf("mit Beginn 600 bekommen %q", b)
	}
}

func TestErwarteteErsparnis(t *testing.T) {
	// 1000 Bytes, davon 900 Bild (72 kbit/s × 0,1 s) und 100 Ton.
	info := VideoInfo{GroesseBytes: 1000, VideoBitrateKbps: 72, DauerSek: 0.1}

	if p, ok := erwarteteErsparnisProzent(info, 0.5); !ok || math.Abs(p-45) > 0.01 {
		t.Errorf("Bild halb so gross, Ton gleich: 45 %% erwartet, bekommen %.2f", p)
	}
	if p, _ := erwarteteErsparnisProzent(info, 1.2); p >= 0 {
		t.Errorf("groesseres Bild muss negativ herauskommen, bekommen %.2f", p)
	}
	ohneBitrate := VideoInfo{GroesseBytes: 1000}
	if p, _ := erwarteteErsparnisProzent(ohneBitrate, 0.5); math.Abs(p-50) > 0.01 {
		t.Errorf("ohne Bildbitrate alles als Bild: 50 %% erwartet, bekommen %.2f", p)
	}
	if _, ok := erwarteteErsparnisProzent(info, 0); ok {
		t.Error("ohne Anteil gibt es nichts zu rechnen")
	}
}

func TestLohntNichtText(t *testing.T) {
	if text := lohntNichtText(-18, 30); !strings.Contains(text, "18% GROESSER") {
		t.Errorf("Vergroesserung muss deutlich gesagt werden: %q", text)
	}
	if text := lohntNichtText(12, 30); !strings.Contains(text, "nur 12% kleiner") || !strings.Contains(text, "30%") {
		t.Errorf("unerwarteter Text: %q", text)
	}
}
