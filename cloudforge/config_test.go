package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Die Werkswerte sind seit 0.7.1 die Wahl des Nutzers — eine Neuinstallation
// soll genau so arbeiten wie sein eingerichteter Server. Ziel 96 seit 0.11.3
// (Frage-Runde 27.09.2026), die beiden SVT-Schalter testet er selbst.
func TestWerkswerteSindDieDesNutzers(t *testing.T) {
	e := standardWerte()
	if e.Preset != 9 || e.Bittiefe != 10 || e.ZielVMAF != 96 || e.AnkerNiedrig != 16 || e.AnkerHoch != 26 {
		t.Errorf("Werkswerte weichen ab: preset %d, bittiefe %d, zielVMAF %v, Anker %d/%d",
			e.Preset, e.Bittiefe, e.ZielVMAF, e.AnkerNiedrig, e.AnkerHoch)
	}
	if e.VarianceBoost || e.Tune0 {
		t.Errorf("Variance Boost (%v) und tune 0 (%v) muessen ab Werk aus sein", e.VarianceBoost, e.Tune0)
	}
	// Den Kosten-Deckel (0.8.0 bis 0.11.1: 50 %) hat der Nutzer am 27.09.2026
	// abgeschaltet — Qualität geht vor. Mindestersparnis seit 0.11.3 15 %
	// (vorher 30), Messfenster 5 statt 3 (Vorhersage traf den Film besser).
	if e.KostenDeckelProzent != 0 || e.MindestErsparnisProzent != 15 {
		t.Errorf("Deckel %v / Mindestersparnis %v, erwartet 0 / 15", e.KostenDeckelProzent, e.MindestErsparnisProzent)
	}
	if e.MessfensterAnzahl != 5 || e.MessfensterSek != 8 {
		t.Errorf("Messfenster %d x %v s, erwartet 5 x 8 s", e.MessfensterAnzahl, e.MessfensterSek)
	}
	// Seit 0.9.0: Plateau-Toleranz 0,5 wie in NVENCForge.
	if e.PlateauToleranz != 0.5 {
		t.Errorf("Plateau-Toleranz %v, erwartet 0,5", e.PlateauToleranz)
	}
	// Die Anker müssen im erlaubten Bereich liegen, sonst setzt pruefeGrenzen
	// sie still zurück — oder klemmt Auto-CQ sie weg.
	if !(e.CRFMin <= e.AnkerNiedrig && e.AnkerNiedrig < e.AnkerHoch && e.AnkerHoch <= e.CRFMax) {
		t.Errorf("Anker %d/%d passen nicht in crfMin/crfMax %d/%d", e.AnkerNiedrig, e.AnkerHoch, e.CRFMin, e.CRFMax)
	}
}

// Seit 0.9.0 gibt es autoCrop und parallelerUpload nicht mehr (nie gebaut).
// Eine INI, die sie noch enthält, muss trotzdem laden — und sie dürfen beim
// Ergänzen nicht wieder auftauchen.
func TestINIMitEntferntenSchluesselnLaedt(t *testing.T) {
	pfad := filepath.Join(t.TempDir(), "cloudforge.ini")
	if err := os.WriteFile(pfad, []byte("preset=9\nautoCrop=ja\nparallelerUpload=ja\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := EinstellungenLaden(pfad)
	if err != nil {
		t.Fatalf("INI mit alten Schluesseln nicht ladbar: %v", err)
	}
	if e.Preset != 9 {
		t.Errorf("Wert des Nutzers verloren: preset %d", e.Preset)
	}
	inhalt, err := os.ReadFile(pfad)
	if err != nil {
		t.Fatal(err)
	}
	for _, alt := range []string{"autoCrop", "parallelerUpload"} {
		if n := strings.Count(string(inhalt), alt+"="); n != 1 {
			t.Errorf("%s steht %d-mal in der INI, erwartet einmal (die alte Zeile, nichts ergaenzt)", alt, n)
		}
	}
}

func TestBittiefeLesen(t *testing.T) {
	faelle := []struct {
		wert     string
		erwartet int
		fehler   bool
	}{
		{"8", 8, false},
		{"10", 10, false},
		{"9", 0, true},
		{"12", 0, true},
		{"acht", 0, true},
		{"", 0, true},
	}

	for _, fall := range faelle {
		e := standardWerte()
		vorher := e.Bittiefe
		err := wertUebernehmen(&e, "bittiefe", fall.wert)

		if fall.fehler {
			if err == nil {
				t.Errorf("%q: Fehler erwartet, aber angenommen", fall.wert)
			}
			if e.Bittiefe != vorher {
				t.Errorf("%q: ungueltiger Wert hat die Bittiefe auf %d geaendert", fall.wert, e.Bittiefe)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unerwarteter Fehler %v", fall.wert, err)
		}
		if e.Bittiefe != fall.erwartet {
			t.Errorf("%q: Bittiefe %d erwartet, bekommen %d", fall.wert, fall.erwartet, e.Bittiefe)
		}
	}
}

func TestVideoArgumenteNehmenDieBittiefe(t *testing.T) {
	faelle := []struct {
		bittiefe int
		format   string
	}{
		{8, "yuv420p"},
		{10, "yuv420p10le"},
	}

	for _, fall := range faelle {
		e := standardWerte()
		e.Bittiefe = fall.bittiefe
		args := videoArgumente(30, e)

		stelle := slices.Index(args, "-pix_fmt")
		if stelle < 0 || stelle+1 >= len(args) {
			t.Fatalf("bittiefe=%d: kein -pix_fmt in %v", fall.bittiefe, args)
		}
		if args[stelle+1] != fall.format {
			t.Errorf("bittiefe=%d: %s erwartet, bekommen %s", fall.bittiefe, fall.format, args[stelle+1])
		}
	}
}

// Eine INI aus 0.4.0 kennt bittiefe und vollpruefung noch nicht. Sie muss
// danach mit der Werks-Bittiefe (seit 0.7.1: 10, wie 0.4.0 fest eingebaut
// hatte) und ohne Vollprüfung laufen, beide Schlüssel sichtbar ergänzt
// bekommen und die Werte des Nutzers behalten — und ein zweiter Start darf
// nichts erneut anhängen.
func TestAlteINIBekommtNeueSchluesselErgaenzt(t *testing.T) {
	pfad := filepath.Join(t.TempDir(), "cloudforge.ini")
	if err := os.WriteFile(pfad, []byte("preset=12\nkerne=6\nzielVMAF=90\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	e, err := EinstellungenLaden(pfad)
	if err != nil {
		t.Fatalf("alte INI nicht ladbar: %v", err)
	}
	if e.Bittiefe != 10 {
		t.Errorf("Bittiefe 10 erwartet, bekommen %d", e.Bittiefe)
	}
	if e.Vollpruefung {
		t.Error("die Vollpruefung muss ohne Eintrag aus sein")
	}
	if e.Preset != 12 || e.ZielVMAF != 90 {
		t.Errorf("Werte des Nutzers verloren: preset %d, zielVMAF %v", e.Preset, e.ZielVMAF)
	}

	nachErstemStart, err := os.ReadFile(pfad)
	if err != nil {
		t.Fatal(err)
	}
	for _, zeile := range []string{"\nbittiefe=10\n", "\nvollpruefung=nein\n", "\nvarianceBoost=nein\n", "\ntune0=nein\n"} {
		if !strings.Contains(string(nachErstemStart), zeile) {
			t.Errorf("%q wurde nicht in die INI geschrieben:\n%s", strings.TrimSpace(zeile), nachErstemStart)
		}
	}

	if _, err := EinstellungenLaden(pfad); err != nil {
		t.Fatalf("zweiter Start scheitert: %v", err)
	}
	nachZweitemStart, err := os.ReadFile(pfad)
	if err != nil {
		t.Fatal(err)
	}
	if string(nachZweitemStart) != string(nachErstemStart) {
		t.Error("der zweite Start hat die INI noch einmal veraendert")
	}
}

func TestVollpruefungLesen(t *testing.T) {
	if standardWerte().Vollpruefung {
		t.Error("die Vollpruefung muss ab Werk aus sein")
	}

	faelle := []struct {
		wert     string
		erwartet bool
	}{
		{"ja", true},
		{"nein", false},
		{"", false},
	}
	for _, fall := range faelle {
		e := standardWerte()
		e.Vollpruefung = !fall.erwartet // Gegenteil vorbelegen, damit ein Nicht-Setzen auffällt
		if err := wertUebernehmen(&e, "vollpruefung", fall.wert); err != nil {
			t.Errorf("%q: unerwarteter Fehler %v", fall.wert, err)
		}
		if e.Vollpruefung != fall.erwartet {
			t.Errorf("%q: Vollpruefung %v erwartet, bekommen %v", fall.wert, fall.erwartet, e.Vollpruefung)
		}
	}
}

func TestEncoderSchalterLesen(t *testing.T) {
	faelle := []struct {
		wert     string
		erwartet bool
	}{
		{"ja", true},
		{"nein", false},
		{"", false},
	}
	for _, schluessel := range []string{"varianceBoost", "tune0"} {
		for _, fall := range faelle {
			e := standardWerte()
			// Gegenteil vorbelegen, damit ein Nicht-Setzen auffällt.
			e.VarianceBoost, e.Tune0 = !fall.erwartet, !fall.erwartet
			if err := wertUebernehmen(&e, schluessel, fall.wert); err != nil {
				t.Errorf("%s=%q: unerwarteter Fehler %v", schluessel, fall.wert, err)
			}
			bekommen := e.VarianceBoost
			if schluessel == "tune0" {
				bekommen = e.Tune0
			}
			if bekommen != fall.erwartet {
				t.Errorf("%s=%q: %v erwartet, bekommen %v", schluessel, fall.wert, fall.erwartet, bekommen)
			}
		}
	}
}

// Sind beide Schalter aus, muss SVT-AV1 genau dasselbe bekommen wie bis 0.9.0
// — sonst änderten sich die Ergebnisse, ohne dass der Nutzer etwas schaltet.
func TestSvtParameterNennenNurEingeschalteteSchalter(t *testing.T) {
	faelle := []struct {
		varianceBoost, tune0 bool
		erwartet             string
	}{
		{false, false, "lp=6"},
		{true, false, "lp=6:enable-variance-boost=1"},
		{false, true, "lp=6:tune=0"},
		{true, true, "lp=6:tune=0:enable-variance-boost=1"},
	}
	for _, fall := range faelle {
		e := standardWerte()
		e.VarianceBoost, e.Tune0 = fall.varianceBoost, fall.tune0

		args := videoArgumente(30, e)
		stelle := slices.Index(args, "-svtav1-params")
		if stelle < 0 || stelle+1 >= len(args) {
			t.Fatalf("kein -svtav1-params in %v", args)
		}
		if args[stelle+1] != fall.erwartet {
			t.Errorf("Variance Boost %v, tune 0 %v: %q erwartet, bekommen %q",
				fall.varianceBoost, fall.tune0, fall.erwartet, args[stelle+1])
		}
	}
}

func TestINIMitBittiefe10(t *testing.T) {
	pfad := filepath.Join(t.TempDir(), "cloudforge.ini")
	if err := os.WriteFile(pfad, []byte("bittiefe=10\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	e, err := EinstellungenLaden(pfad)
	if err != nil {
		t.Fatalf("INI nicht ladbar: %v", err)
	}
	if e.Bittiefe != 10 {
		t.Errorf("Bittiefe 10 erwartet, bekommen %d", e.Bittiefe)
	}
}
