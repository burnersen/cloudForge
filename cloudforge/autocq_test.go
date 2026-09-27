package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
)

// Die Zahlen stammen aus der echten Messreihe vom 21.09.2026 auf dem VPS
// (SVT-AV1 preset 8, 1080p50, 6 Kerne):
//   CRF 24 -> 95,36   CRF 28 -> 94,64   CRF 32 -> 93,80   CRF 36 -> 92,75

func TestInterpolierenTrifftGemessenenPunkt(t *testing.T) {
	niedrig := Messung{CRF: 24, VMAF: 95.36}
	hoch := Messung{CRF: 32, VMAF: 93.80}

	faelle := []struct {
		ziel     float64
		erwartet int
		warum    string
	}{
		{94.64, 28, "genau der gemessene Mittelpunkt"},
		{95.36, 24, "genau der untere Anker"},
		{93.80, 32, "genau der obere Anker"},
		{94.00, 31, "das Ziel des Nutzers"},
	}

	for _, f := range faelle {
		if bekommen := interpolieren(niedrig, hoch, f.ziel); bekommen != f.erwartet {
			t.Errorf("Ziel %.2f (%s): CRF %d erwartet, %d bekommen",
				f.ziel, f.warum, f.erwartet, bekommen)
		}
	}
}

func TestInterpolierenVertraegtWaagerechteKurve(t *testing.T) {
	// Kommt vor, wenn ein Ausschnitt so leicht zu kodieren ist, dass der
	// CRF kaum noch etwas ändert. Ohne Sonderbehandlung gäbe es hier eine
	// Division durch fast null und einen unsinnigen CRF.
	niedrig := Messung{CRF: 22, VMAF: 98.00}
	hoch := Messung{CRF: 32, VMAF: 98.00}

	bekommen := interpolieren(niedrig, hoch, 94)
	if bekommen != 32 {
		t.Errorf("bei waagerechter Kurve soll der sparsamste Anker gewaehlt werden (32), bekommen: %d", bekommen)
	}
}

func TestInterpolierenVertraegtGleicheAnker(t *testing.T) {
	gleich := Messung{CRF: 28, VMAF: 94.0}
	if bekommen := interpolieren(gleich, gleich, 94); bekommen != 28 {
		t.Errorf("CRF 28 erwartet, %d bekommen", bekommen)
	}
}

func TestFensterWaehlenBleibtInDerDatei(t *testing.T) {
	faelle := []struct {
		dauer  float64
		anzahl int
		laenge float64
		name   string
	}{
		{3000, 3, 8, "langer Film"},
		{60, 3, 8, "kurze Datei"},
		{35, 3, 8, "gerade eben lang genug"},
		{31, 3, 8, "kuerzer als drei Fenster"},
	}

	for _, f := range faelle {
		fenster := FensterWaehlen(f.dauer, f.anzahl, f.laenge)
		if len(fenster) == 0 {
			t.Errorf("%s: kein Fenster geliefert", f.name)
			continue
		}
		for i, w := range fenster {
			if w.StartSek < 0 {
				t.Errorf("%s: Fenster %d beginnt bei %.1f", f.name, i, w.StartSek)
			}
			if ende := w.StartSek + w.LaengeSek; ende > f.dauer+0.001 {
				t.Errorf("%s: Fenster %d endet bei %.1f, Datei ist nur %.1f lang",
					f.name, i, ende, f.dauer)
			}
			if w.LaengeSek <= 0 {
				t.Errorf("%s: Fenster %d hat Laenge %.1f", f.name, i, w.LaengeSek)
			}
		}
	}
}

func TestFensterWaehlenMeidetAnfangUndEnde(t *testing.T) {
	// Vorspann und Abspann sagen nichts über den Film aus.
	fenster := FensterWaehlen(3000, 3, 8)
	if len(fenster) != 3 {
		t.Fatalf("3 Fenster erwartet, %d bekommen", len(fenster))
	}
	if fenster[0].StartSek < 3000*0.10 {
		t.Errorf("erstes Fenster liegt zu weit vorn: %.0f s", fenster[0].StartSek)
	}
	letztes := fenster[len(fenster)-1]
	if letztes.StartSek+letztes.LaengeSek > 3000*0.90 {
		t.Errorf("letztes Fenster liegt zu weit hinten: %.0f s", letztes.StartSek)
	}
}

func TestAufstiegLohnt(t *testing.T) {
	m := func(crf int, bytes int64) Messung { return Messung{CRF: crf, Bytes: bytes} }
	faelle := []struct {
		von, bis Messung
		mindest  float64
		erwartet bool
		warum    string
	}{
		{m(16, 1000), m(17, 900), 5, true, "eine Stufe, 10 Prozent kleiner"},
		{m(16, 1000), m(17, 960), 5, false, "eine Stufe, nur 4 Prozent kleiner"},
		{m(16, 1000), m(17, 950), 5, true, "eine Stufe, genau 5 Prozent"},
		{m(16, 1000), m(17, 1100), 5, false, "groesser geworden"},
		{m(16, 0), m(17, 500), 5, false, "Ausgangsgroesse unbekannt"},
		{m(16, 1000), m(16, 900), 5, false, "gar kein Aufstieg"},
		{m(16, 1000), m(19, 850), 5, true, "drei Stufen, im Mittel 5,3 Prozent je Stufe"},
		{m(16, 1000), m(19, 880), 5, false, "drei Stufen, im Mittel nur 4,2 Prozent je Stufe"},
	}

	for _, f := range faelle {
		if bekommen := aufstiegLohnt(f.von, f.bis, f.mindest); bekommen != f.erwartet {
			t.Errorf("%s: %v erwartet, %v bekommen", f.warum, f.erwartet, bekommen)
		}
	}
}

func TestProzentKleiner(t *testing.T) {
	if p := ProzentKleiner(1000, 250); math.Abs(p-75) > 0.001 {
		t.Errorf("75 Prozent erwartet, %.3f bekommen", p)
	}
	if p := ProzentKleiner(1000, 1500); p >= 0 {
		t.Errorf("bei groesserem Ergebnis soll der Wert negativ sein, ist %.1f", p)
	}
	if p := ProzentKleiner(0, 100); p != 0 {
		t.Errorf("ohne Ausgangsgroesse 0 erwartet, %.1f bekommen", p)
	}
}

func TestRundeAuf(t *testing.T) {
	if r := rundeAuf(50, 14, 44); r != 44 {
		t.Errorf("auf 44 begrenzen erwartet, %d bekommen", r)
	}
	if r := rundeAuf(2, 14, 44); r != 14 {
		t.Errorf("auf 14 anheben erwartet, %d bekommen", r)
	}
	if r := rundeAuf(28, 14, 44); r != 28 {
		t.Errorf("28 unveraendert erwartet, %d bekommen", r)
	}
}

// kurve baut eine erfundene Probemessung aus festen Punkten. Die Grösse sinkt
// mit jedem CRF um 8 % — ungefähr wie echt, für die Suche genügt "fällt".
func kurve(punkte map[int]float64) probeMessung {
	return func(crf int) (float64, int64, error) {
		vmaf, da := punkte[crf]
		if !da {
			return 0, 0, fmt.Errorf("CRF %d ist in der Testkurve nicht vorgesehen", crf)
		}
		return vmaf, int64(100e6 * math.Pow(0.92, float64(crf))), nil
	}
}

func testSuche(ziel float64, anker [2]int, punkte map[int]float64) *crfSuche {
	e := standardWerte()
	e.ZielVMAF = ziel
	e.AnkerNiedrig, e.AnkerHoch = anker[0], anker[1]
	return &crfSuche{ctx: context.Background(), e: e, probe: kurve(punkte)}
}

// Der Fall, der den Nutzer am 25.09.2026 gestört hat: Die lineare
// Hochrechnung landet knapp UNTER dem Ziel. Bis 0.6.0 wurde das angenommen
// (Toleranz 0,5) — jetzt muss feiner nachgemessen werden.
func TestSucheNimmtNieEinenWertUnterDemZiel(t *testing.T) {
	// Oben flach und krumm, wie gemessen: gerade Linie zwischen 16 und 26
	// sagt für 98,0 CRF 21 voraus, echt liegt 21 aber bei 97,7.
	punkte := map[int]float64{
		16: 99.0, 17: 98.75, 18: 98.5, 19: 98.25, 20: 97.95, 21: 97.7,
		22: 97.5, 23: 97.3, 24: 97.1, 25: 96.9, 26: 97.0,
	}
	ergebnis, err := testSuche(98, [2]int{16, 26}, punkte).suchen()
	if err != nil {
		t.Fatalf("unerwarteter Fehler: %v", err)
	}
	if ergebnis.ErwarteterVMAF < 98 {
		t.Errorf("Ergebnis CRF %d mit VMAF %.2f liegt unter dem Ziel 98", ergebnis.CRF, ergebnis.ErwarteterVMAF)
	}
	if ergebnis.CRF != 19 {
		t.Errorf("CRF 19 ist der sparsamste Wert ueber 98, bekommen CRF %d", ergebnis.CRF)
	}
	if len(ergebnis.Messungen) > maxMessungen {
		t.Errorf("%d Messungen, erlaubt sind %d", len(ergebnis.Messungen), maxMessungen)
	}
}

func TestSucheTrifftDasZielDirekt(t *testing.T) {
	// Gerade Kurve: die Hochrechnung stimmt, eine Gegenmessung reicht.
	punkte := map[int]float64{16: 99.0, 20: 98.2, 21: 98.0, 26: 97.0}
	ergebnis, err := testSuche(98, [2]int{16, 26}, punkte).suchen()
	if err != nil {
		t.Fatalf("unerwarteter Fehler: %v", err)
	}
	if ergebnis.CRF != 21 || len(ergebnis.Messungen) != 3 {
		t.Errorf("CRF 21 nach 3 Messungen erwartet, bekommen CRF %d nach %d", ergebnis.CRF, len(ergebnis.Messungen))
	}
}

func TestSucheWirdSparsamerWennDeutlichBesser(t *testing.T) {
	// Die Hochrechnung (22/32 -> CRF 29) misst 0,8 über dem Ziel; eine Stufe
	// sparsamer hält es noch und liegt näher dran.
	punkte := map[int]float64{22: 96.0, 32: 93.0, 29: 94.8, 30: 94.3}
	ergebnis, err := testSuche(94, [2]int{22, 32}, punkte).suchen()
	if err != nil {
		t.Fatalf("unerwarteter Fehler: %v", err)
	}
	if ergebnis.CRF != 30 {
		t.Errorf("CRF 30 (94,3) erwartet, bekommen CRF %d (%.2f)", ergebnis.CRF, ergebnis.ErwarteterVMAF)
	}
}

func TestSucheBleibtBeiSparsamerStufeUnterDemZielStehen(t *testing.T) {
	// Die sparsamere Stufe fällt unters Ziel — dann bleibt die gemessene.
	punkte := map[int]float64{22: 96.0, 32: 93.0, 29: 94.8, 30: 93.9}
	ergebnis, err := testSuche(94, [2]int{22, 32}, punkte).suchen()
	if err != nil {
		t.Fatalf("unerwarteter Fehler: %v", err)
	}
	if ergebnis.CRF != 29 {
		t.Errorf("CRF 29 erwartet, bekommen CRF %d (%.2f)", ergebnis.CRF, ergebnis.ErwarteterVMAF)
	}
}

func TestSucheEndetAuchBeiZickzackKurve(t *testing.T) {
	// Messrauschen: höhere CRF messen mal besser als niedrigere. Die Suche
	// darf sich nicht im Kreis drehen und muss trotzdem über dem Ziel landen.
	punkte := map[int]float64{}
	for crf := 14; crf <= 44; crf++ {
		punkte[crf] = 97.9 // knapp darunter ...
	}
	punkte[16] = 99.0 // ... nur der Anker hält das Ziel
	punkte[26] = 97.0
	ergebnis, err := testSuche(98, [2]int{16, 26}, punkte).suchen()
	if err != nil {
		t.Fatalf("unerwarteter Fehler: %v", err)
	}
	if ergebnis.ErwarteterVMAF < 98 {
		t.Errorf("Ergebnis CRF %d mit %.2f liegt unter dem Ziel", ergebnis.CRF, ergebnis.ErwarteterVMAF)
	}
	if len(ergebnis.Messungen) > maxMessungen {
		t.Errorf("%d Messungen, erlaubt sind %d", len(ergebnis.Messungen), maxMessungen)
	}
}

func TestSucheMeldetAbbruchBeimNachmessen(t *testing.T) {
	// Anker und Gegenmessung laufen durch, die Gegenmessung liegt unter dem
	// Ziel — und beim Nachmessen schliesst jemand das Fenster.
	ctx, abbrechen := context.WithCancel(context.Background())
	s := testSuche(98, [2]int{16, 26}, map[int]float64{16: 99.0, 26: 97.0, 21: 97.7})
	s.ctx = ctx
	echt := s.probe
	s.probe = func(crf int) (float64, int64, error) {
		if crf != 16 && crf != 26 && crf != 21 {
			abbrechen()
			return 0, 0, ErrAbgebrochen
		}
		return echt(crf)
	}
	if _, err := s.suchen(); !errors.Is(err, ErrAbgebrochen) {
		t.Errorf("Abbruch erwartet, bekommen: %v", err)
	}
}

func TestVMAFAusAusgabe(t *testing.T) {
	echteAusgabe := `[Parsed_libvmaf_2 @ 0x789de8002e40] VMAF score: 93.558683
[out#0/null @ 0x5f2] video:1234KiB`

	wert, err := vmafAusAusgabe(echteAusgabe)
	if err != nil {
		t.Fatalf("unerwarteter Fehler: %v", err)
	}
	if math.Abs(wert-93.558683) > 0.0001 {
		t.Errorf("93.558683 erwartet, %v bekommen", wert)
	}
}

func TestVMAFAusAusgabeMeldetFehlendenWert(t *testing.T) {
	faelle := map[string]string{
		"leere Ausgabe":     "",
		"nur Fehlermeldung": "Error opening input file",
		"Wert unlesbar":     "VMAF score: keine-zahl",
		"Wert ausserhalb":   "VMAF score: 250.0",
	}

	for name, ausgabe := range faelle {
		if _, err := vmafAusAusgabe(ausgabe); err == nil {
			t.Errorf("%s: Fehler erwartet, keiner gekommen", name)
		}
	}
}

// Kleinere Videos werden für die Messung so vergrössert, wie sie im Vollbild
// auf einem 1080p-Schirm erscheinen — die Masse der drei Filme vom 27.09.2026
// sind dabei (1280x720, 720x540, 720x404).
func TestVmafMessgroesse(t *testing.T) {
	faelle := []struct {
		name                  string
		breite, hoehe         int
		zielBreite, zielHoehe int
		vergroessern          bool
	}{
		{"720p", 1280, 720, 1920, 1080, true},
		{"540p im Format 4:3", 720, 540, 1440, 1080, true},
		{"404p, Höhe auf gerade Zahl gerundet", 720, 404, 1920, 1078, true},
		{"720p-Breitbild", 1280, 536, 1920, 804, true},
		{"krumme Breite", 853, 480, 1920, 1080, true},
		{"Hochkant 720p", 720, 1280, 1080, 1920, true},
		{"1080p bleibt", 1920, 1080, 1920, 1080, false},
		{"1080p-Breitbild bleibt", 1920, 800, 1920, 800, false},
		{"4K bleibt", 3840, 2160, 3840, 2160, false},
		{"Masse unbekannt", 0, 0, 0, 0, false},
	}
	for _, f := range faelle {
		breite, hoehe, vergroessern := vmafMessgroesse(f.breite, f.hoehe)
		if breite != f.zielBreite || hoehe != f.zielHoehe || vergroessern != f.vergroessern {
			t.Errorf("%s: %dx%d ergab %dx%d (vergroessern %v), erwartet %dx%d (%v)",
				f.name, f.breite, f.hoehe, breite, hoehe, vergroessern,
				f.zielBreite, f.zielHoehe, f.vergroessern)
		}
	}
}

// Beide Seiten müssen genau gleich vorbereitet werden: gepaart nach Bildnummer
// und — nur bei kleinen Videos — mit demselben Filter vergrössert.
func TestVmafFilterVergroessertBeideSeitenGleich(t *testing.T) {
	klein := vmafFilter(1280, 720, 8)
	if n := strings.Count(klein, "scale=1920:1080:flags=bicubic"); n != 2 {
		t.Errorf("720p: Vergrössern auf beiden Seiten erwartet, %d-mal gefunden: %s", n, klein)
	}
	if n := strings.Count(klein, "setpts=N"); n != 2 {
		t.Errorf("720p: Bildnummer-Paarung auf beiden Seiten erwartet, %d-mal gefunden: %s", n, klein)
	}

	gross := vmafFilter(1920, 1080, 8)
	if strings.Contains(gross, "scale=") {
		t.Errorf("1080p darf nicht vergrössert werden: %s", gross)
	}
	if !strings.Contains(gross, "libvmaf=n_threads=8:n_subsample=3") {
		t.Errorf("Messung selbst verändert: %s", gross)
	}
}

// protokollKurve baut eine Probemessung aus echten Messpunkten. Zwischen zwei
// gemessenen CRF wird gerade verbunden, ausserhalb gibt es keine Werte. Die
// Grösse fällt je Stufe um den Faktor (0,92 = 8 % kleiner, wie in kurve).
func protokollKurve(punkte map[int]float64, faktorJeStufe float64) probeMessung {
	return func(crf int) (float64, int64, error) {
		unten, oben := -1, -1
		for c := range punkte {
			if c <= crf && (unten < 0 || c > unten) {
				unten = c
			}
			if c >= crf && (oben < 0 || c < oben) {
				oben = c
			}
		}
		if unten < 0 || oben < 0 {
			return 0, 0, fmt.Errorf("CRF %d liegt ausserhalb der Messpunkte", crf)
		}
		vmaf := punkte[unten]
		if oben != unten {
			vmaf += float64(crf-unten) / float64(oben-unten) * (punkte[oben] - punkte[unten])
		}
		return vmaf, int64(100e6 * math.Pow(faktorJeStufe, float64(crf))), nil
	}
}

// Echte Kurven aus dem Protokoll vom 26.09.2026 (Ziel 98, von 1080p50-Material
// unerreichbar). Bis 0.9.0 stieg der Plateau-Weg Stufe für Stufe — das
// "17, 18, 19, 20" im Protokoll. Seit 0.10.0 springt er: dieselbe
// Wahl wie damals, aber weniger Proben à 50 s.
func TestPlateauSpringtUndWaehltWieVorher(t *testing.T) {
	faelle := []struct {
		name         string
		punkte       map[int]float64
		erwartetCRF  int // die Wahl des alten Stufe-für-Stufe-Wegs
		probenVorher int
		probenJetzt  int
	}{
		{"Film A", map[int]float64{16: 97.96, 17: 97.82, 18: 97.71, 19: 97.57, 20: 97.41, 23: 96.94, 24: 96.72, 26: 96.16}, 19, 6, 4},
		{"Film B", map[int]float64{16: 97.70, 17: 97.57, 18: 97.47, 19: 97.34, 20: 97.20, 21: 97.07, 26: 96.17}, 20, 7, 5},
		{"Film C", map[int]float64{16: 96.23, 17: 96.01, 18: 95.76, 19: 95.57, 26: 93.86}, 18, 5, 4},
		{"Film D", map[int]float64{16: 96.57, 17: 96.38, 18: 96.24, 19: 96.06, 26: 94.52}, 18, 5, 4},
		{"Film E", map[int]float64{16: 96.95, 17: 96.77, 18: 96.63, 19: 96.45, 20: 96.27, 23: 95.76, 24: 95.52, 26: 94.93}, 19, 6, 5},
		{"Film F", map[int]float64{16: 97.42, 17: 97.21, 18: 96.89, 24: 95.60, 25: 95.29, 26: 94.88}, 17, 4, 4},
	}

	for _, f := range faelle {
		s := testSuche(98, [2]int{16, 26}, nil)
		s.probe = protokollKurve(f.punkte, 0.92)
		ergebnis, err := s.suchen()
		if err != nil {
			t.Fatalf("%s: unerwarteter Fehler: %v", f.name, err)
		}
		if ergebnis.ZielErreichbar {
			t.Errorf("%s: Ziel 98 ist bei diesem Material nicht erreichbar", f.name)
		}
		if ergebnis.CRF != f.erwartetCRF {
			t.Errorf("%s: CRF %d erwartet (Wahl des alten Wegs), bekommen CRF %d (%.2f)",
				f.name, f.erwartetCRF, ergebnis.CRF, ergebnis.ErwarteterVMAF)
		}
		if len(ergebnis.Messungen) != f.probenJetzt {
			t.Errorf("%s: %d Proben erwartet (vorher %d), bekommen %d",
				f.name, f.probenJetzt, f.probenVorher, len(ergebnis.Messungen))
		}
	}
}

// Spart der Aufstieg kaum Platz, bleibt die beste Qualität — auch wenn der
// Boden gehalten wird.
func TestPlateauOhneErsparnisBleibtBeimBestenWert(t *testing.T) {
	s := testSuche(98, [2]int{16, 26}, nil)
	s.probe = protokollKurve(map[int]float64{16: 96.44, 19: 96.00, 20: 95.84, 26: 94.75}, 0.99) // 1 % je Stufe
	ergebnis, err := s.suchen()
	if err != nil {
		t.Fatalf("unerwarteter Fehler: %v", err)
	}
	if ergebnis.CRF != 16 {
		t.Errorf("CRF 16 erwartet (Aufstieg spart nur 1 %% je Stufe), bekommen CRF %d", ergebnis.CRF)
	}
}

// Der Aufstieg verlässt crfMax nie — auch wenn der sparsame Anker darüber liegt.
func TestPlateauBleibtInnerhalbVonCRFMax(t *testing.T) {
	s := testSuche(98, [2]int{16, 26}, nil)
	s.e.CRFMax = 20
	// Ohne Grenze fände die Suche CRF 25 (95,55 hält den Boden 95,5 noch).
	s.probe = protokollKurve(map[int]float64{16: 96.0, 20: 95.9, 25: 95.55, 26: 94.0}, 0.92)
	ergebnis, err := s.suchen()
	if err != nil {
		t.Fatalf("unerwarteter Fehler: %v", err)
	}
	if ergebnis.CRF != 20 {
		t.Errorf("CRF 20 (= crfMax) erwartet, bekommen CRF %d", ergebnis.CRF)
	}
}

// Jemand schliesst beim Aufstieg das Fenster — beim Eingrenzen oder schon bei
// der Probe an crfMax (wenn der sparsame Anker darüber liegt).
func TestPlateauMeldetAbbruch(t *testing.T) {
	for _, crfMax := range []int{44, 20} {
		ctx, abbrechen := context.WithCancel(context.Background())
		s := testSuche(98, [2]int{16, 26}, nil)
		s.ctx = ctx
		s.e.CRFMax = crfMax
		echt := protokollKurve(map[int]float64{16: 96.44, 20: 95.84, 26: 94.75}, 0.92)
		s.probe = func(crf int) (float64, int64, error) {
			if crf != 16 && crf != 26 {
				abbrechen()
				return 0, 0, ErrAbgebrochen
			}
			return echt(crf)
		}
		if _, err := s.suchen(); !errors.Is(err, ErrAbgebrochen) {
			t.Errorf("crfMax %d: Abbruch erwartet, bekommen: %v", crfMax, err)
		}
	}
}

// Leichtes Material, echte Messung vom 26.09.2026 (Filmstück, Ziel 97):
// Beide Anker liegen über dem Ziel, die Hochrechnung schiesst bis crfMax.
// Bis zum Illinois-Verfahren kroch die Eingrenzung danach 29, 30, 31, 32
// heran (7 Proben); jetzt springt sie — dieselbe Wahl CRF 31.
func TestEingrenzenKriechtNichtBeiLeichtemMaterial(t *testing.T) {
	s := testSuche(97, [2]int{16, 26}, nil)
	s.probe = protokollKurve(map[int]float64{
		16: 99.43, 26: 98.61, 29: 97.85, 30: 97.51, 31: 97.07, 32: 96.55, 44: 87.48,
	}, 0.92)
	ergebnis, err := s.suchen()
	if err != nil {
		t.Fatalf("unerwarteter Fehler: %v", err)
	}
	if ergebnis.CRF != 31 {
		t.Errorf("CRF 31 (97,07) erwartet, bekommen CRF %d (%.2f)", ergebnis.CRF, ergebnis.ErwarteterVMAF)
	}
	if len(ergebnis.Messungen) != 6 {
		t.Errorf("6 Proben erwartet (vorher 7), bekommen %d: %v", len(ergebnis.Messungen), ergebnis.Messungen)
	}
}
