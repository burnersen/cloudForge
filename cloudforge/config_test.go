// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// Die Werkswerte sind seit 0.7.1 die Wahl des Nutzers — eine Neuinstallation
// soll genau so arbeiten wie sein eingerichteter Server. Ziel 95 mit Ankern
// 22/32 seit 0.15.0 (Nutzerwahl 28.09.2026), die beiden SVT-Schalter testet
// er selbst.
func TestWerkswerteSindDieDesNutzers(t *testing.T) {
	e := standardWerte()
	if e.Preset != 9 || e.Bittiefe != 10 || e.ZielVMAF != 95 || e.AnkerNiedrig != 22 || e.AnkerHoch != 32 {
		t.Errorf("Werkswerte weichen ab: preset %d, bittiefe %d, zielVMAF %v, Anker %d/%d",
			e.Preset, e.Bittiefe, e.ZielVMAF, e.AnkerNiedrig, e.AnkerHoch)
	}
	// Seit 0.19.0 Variance Boost an (gegen Klötzchen in ruhigen Flächen),
	// seit 0.20.0 auch tune 0 (Nutzerwahl 03.10.2026 nach seinen Läufen).
	if !e.VarianceBoost || !e.Tune0 {
		t.Errorf("Variance Boost (%v) und tune 0 (%v) muessen ab Werk an sein", e.VarianceBoost, e.Tune0)
	}
	// Seit 0.18.0: Empfehlung der SVT-AV1-Doku für echte Filme (= Werk von SVT).
	if e.VarianceBoostStaerke != 2 || e.VarianceOktil != 5 {
		t.Errorf("Variance Boost Staerke %d / Oktil %d, erwartet 2 / 5", e.VarianceBoostStaerke, e.VarianceOktil)
	}
	// Den Kosten-Deckel (0.8.0 bis 0.11.1: 50 %) hat der Nutzer am 27.09.2026
	// abgeschaltet — Qualität geht vor. Mindestersparnis seit 0.20.0 7 %
	// (seine INI; 0.19.x 10, 0.11.3 bis 0.18.0 15, davor 30).
	if e.KostenDeckelProzent != 0 || e.MindestErsparnisProzent != 7 {
		t.Errorf("Deckel %v / Mindestersparnis %v, erwartet 0 / 7", e.KostenDeckelProzent, e.MindestErsparnisProzent)
	}
	// Seit 0.19.0 (Nutzerwahl 01.10.2026): gemessen am 5-%-Perzentil, ohne
	// Filmkorn (kostet viel Zeit), eine Datei nach der anderen.
	if e.VMAFPerzentil != 5 || e.FilmKorn != 0 || e.ParallelDateien != 1 {
		t.Errorf("Perzentil %d, Filmkorn %d, parallel %d - erwartet 5, 0, 1",
			e.VMAFPerzentil, e.FilmKorn, e.ParallelDateien)
	}
	// Ziel 92 am 5-%-Perzentil = im Schnitt so gross wie Mittelwert 96
	// (gemessen 01.10.2026 an 6 Filmen).
	if e.ZielVMAFPerzentil != 92 {
		t.Errorf("Ziel fuer das Perzentil %v, erwartet 92", e.ZielVMAFPerzentil)
	}
	// Seit 0.20.0 4 x 9 s (seine INI; 0.11.3 bis 0.19.1 5 x 8 s). Nie
	// zurück auf 3: Mit 3 Stellen lag die Vorhersage bis 10 Punkte daneben.
	if e.MessfensterAnzahl != 4 || e.MessfensterSek != 9 {
		t.Errorf("Messfenster %d x %v s, erwartet 4 x 9 s", e.MessfensterAnzahl, e.MessfensterSek)
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

// Seit 0.14.0 bringt der Start die INI in Werksform (wie NVENCForge 2.0):
// Werte des Nutzers bleiben, ausgemusterte Schlüssel und eigene Kommentare
// verschwinden, die alte Fassung liegt vollständig in der Sicherung. Ein
// vertippter Schlüssel wird genannt, ein ausgemusterter nicht.
func TestINIAufraeumenBehaeltWerteUndSichertDieAlteFassung(t *testing.T) {
	pfad := filepath.Join(t.TempDir(), "cloudforge.ini")
	alt := "# meine Notiz\npreset=8\nzielVMAF=97,5\nvarianceBoost=Ja\n" +
		"autoCrop=ja\ntonSchwelleKbps=800\ntonZielKbps=256\nzielVmaf=90\n"
	if err := os.WriteFile(pfad, []byte(alt), 0o644); err != nil {
		t.Fatal(err)
	}

	e, hinweise, err := EinstellungenLaden(pfad)
	if err != nil {
		t.Fatalf("INI nicht ladbar: %v", err)
	}
	if e.Preset != 8 || e.ZielVMAF != 97.5 || !e.VarianceBoost {
		t.Errorf("Werte des Nutzers verloren: preset %d, zielVMAF %v, varianceBoost %v",
			e.Preset, e.ZielVMAF, e.VarianceBoost)
	}

	neu, err := os.ReadFile(pfad)
	if err != nil {
		t.Fatal(err)
	}
	if string(neu) != iniText(e) {
		t.Errorf("INI ist nicht in Werksform:\n%s", neu)
	}
	for _, weg := range []string{"meine Notiz", "autoCrop", "tonSchwelleKbps", "tonZielKbps", "zielVmaf"} {
		if strings.Contains(string(neu), weg) {
			t.Errorf("%q steht noch in der aufgeraeumten INI", weg)
		}
	}
	sicherung, err := os.ReadFile(pfad + iniSicherungEndung)
	if err != nil {
		t.Fatalf("keine Sicherung: %v", err)
	}
	if string(sicherung) != alt {
		t.Errorf("Sicherung weicht von der alten INI ab:\n%s", sicherung)
	}

	alleHinweise := strings.Join(hinweise, "\n")
	if !strings.Contains(alleHinweise, "zielVmaf") {
		t.Errorf("der vertippte Schluessel wird nicht genannt: %v", hinweise)
	}
	for _, still := range []string{"autoCrop", "tonSchwelleKbps", "tonZielKbps"} {
		if strings.Contains(alleHinweise, still) {
			t.Errorf("ausgemusterter Schluessel %s wird als unbekannt gemeldet: %v", still, hinweise)
		}
	}
}

// Eine INI, die schon in Werksform ist, bleibt Byte für Byte, wie sie ist —
// ohne Sicherung und ohne Hinweis. Sonst schriebe jeder Start die Datei neu.
func TestINIInWerksformBleibtUnberuehrt(t *testing.T) {
	pfad := filepath.Join(t.TempDir(), "cloudforge.ini")
	e := standardWerte()
	e.ZielVMAF, e.QuellOrdner = 95.5, []string{"/home/b/Daten"}
	if err := os.WriteFile(pfad, []byte(iniText(e)), 0o644); err != nil {
		t.Fatal(err)
	}

	_, hinweise, err := EinstellungenLaden(pfad)
	if err != nil {
		t.Fatalf("INI nicht ladbar: %v", err)
	}
	if len(hinweise) != 0 {
		t.Errorf("keine Hinweise erwartet: %v", hinweise)
	}
	if _, err := os.Stat(pfad + iniSicherungEndung); err == nil {
		t.Error("ohne Aenderung darf keine Sicherung entstehen")
	}
	if nachher, _ := os.ReadFile(pfad); string(nachher) != iniText(e) {
		t.Errorf("die INI wurde veraendert:\n%s", nachher)
	}
}

// Unter Windows gespeichert: unsichtbare BOM am Anfang, Zeilenenden CRLF. Der
// erste Schlüssel muss trotzdem gelten und darf nicht als unbekannt gelten.
func TestINIVonWindowsEditor(t *testing.T) {
	e := standardWerte()
	unbekannt, err := iniWerteLesen(utf8BOM+"preset=7\r\nzielVMAF=95\r\n", &e)
	if err != nil {
		t.Fatalf("nicht lesbar: %v", err)
	}
	if e.Preset != 7 || e.ZielVMAF != 95 || len(unbekannt) != 0 {
		t.Errorf("preset %d, zielVMAF %v, unbekannt %v", e.Preset, e.ZielVMAF, unbekannt)
	}
}

// Ein ungültiger Wert hält den Start an (wie bisher) — und dann wird auch
// nichts aufgeräumt, damit der Nutzer seine Zeile so wiederfindet.
func TestINIMitUngueltigemWertWirdNichtAngefasst(t *testing.T) {
	pfad := filepath.Join(t.TempDir(), "cloudforge.ini")
	alt := "# Notiz\npreset=99\n"
	if err := os.WriteFile(pfad, []byte(alt), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := EinstellungenLaden(pfad); err == nil {
		t.Fatal("preset=99 muss ein Fehler sein")
	}
	if nachher, _ := os.ReadFile(pfad); string(nachher) != alt {
		t.Errorf("die INI wurde trotz Fehler veraendert:\n%s", nachher)
	}
	if _, err := os.Stat(pfad + iniSicherungEndung); err == nil {
		t.Error("trotz Fehler wurde eine Sicherung angelegt")
	}
}

// Jeder Schlüssel, den die Werksform schreibt, muss sich zurücklesen lassen —
// sonst ginge sein Wert beim Aufräumen still verloren. Alle Werte weichen
// dafür vom Werk ab.
func TestINIHinUndZurueck(t *testing.T) {
	vorher := Einstellungen{
		FFmpegPfad: "/x/ffmpeg", FFprobePfad: "/x/ffprobe",
		QuellOrdner: []string{"/a", "/b c"}, ArbeitsOrdner: "/arbeit",
		AusgabeOrdnerName: "aus", OriginalOrdnerName: "orig",
		OriginalBehandlung: OriginalLoeschen,
		Preset:             7, Kerne: 3, ParallelDateien: 2, Bittiefe: 8, VarianceBoost: true, Tune0: true,
		VarianceBoostStaerke: 3, VarianceOktil: 7, FilmKorn: 6,
		ZielVMAF: 95.5, VMAFPerzentil: 10, ZielVMAFPerzentil: 91.5,
		AnkerNiedrig: 18, AnkerHoch: 30, CRFMin: 12, CRFMax: 50,
		MessfensterAnzahl: 7, MessfensterSek: 9.5, PlateauToleranz: 0.3, PlateauMindestSpa: 4,
		KostenDeckelProzent: 40, MindestErsparnisProzent: 22,
		PlatzReserveGB: 33, Vollpruefung: true, MaxStundenProDatei: 5,
	}
	nachher := standardWerte()
	unbekannt, err := iniWerteLesen(iniText(vorher), &nachher)
	if err != nil {
		t.Fatalf("Werksform nicht lesbar: %v", err)
	}
	if len(unbekannt) != 0 {
		t.Errorf("die Werksform enthaelt unbekannte Zeilen: %v", unbekannt)
	}
	if !reflect.DeepEqual(vorher, nachher) {
		t.Errorf("Werte gingen verloren:\nvorher  %+v\nnachher %+v", vorher, nachher)
	}
	for schluessel := range ausgemusterteSchluessel {
		if bekannteSchluessel()[schluessel] {
			t.Errorf("%s ist ausgemustert, steht aber noch in iniAufbau", schluessel)
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
		args := videoArgumente(30, e, false)

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

	e, _, err := EinstellungenLaden(pfad)
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
	for _, zeile := range []string{"\nbittiefe=10\n", "\nvollpruefung=nein\n", "\nvarianceBoost=ja\n",
		"\nvarianceBoostStaerke=2\n", "\nvarianceOktil=5\n", "\ntune0=ja\n",
		"\nfilmKorn=0\n", "\nvmafPerzentil=5\n", "\nparallelDateien=1\n"} {
		if !strings.Contains(string(nachErstemStart), zeile) {
			t.Errorf("%q wurde nicht in die INI geschrieben:\n%s", strings.TrimSpace(zeile), nachErstemStart)
		}
	}

	if _, _, err := EinstellungenLaden(pfad); err != nil {
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
// Stärke und Oktil (seit 0.18.0) gehen nur mit dem Variance Boost mit.
func TestSvtParameterNennenNurEingeschalteteSchalter(t *testing.T) {
	faelle := []struct {
		varianceBoost, tune0 bool
		staerke, oktil       int
		erwartet             string
	}{
		{false, false, 2, 5, "lp=6"},
		{false, true, 3, 7, "lp=6:tune=0"}, // Feinregler ohne Variance Boost wirken nicht
		{true, false, 2, 5, "lp=6:enable-variance-boost=1:variance-boost-strength=2:variance-octile=5"},
		{true, true, 3, 7, "lp=6:tune=0:enable-variance-boost=1:variance-boost-strength=3:variance-octile=7"},
	}
	for _, fall := range faelle {
		e := standardWerte()
		e.VarianceBoost, e.Tune0 = fall.varianceBoost, fall.tune0
		e.VarianceBoostStaerke, e.VarianceOktil = fall.staerke, fall.oktil

		args := videoArgumente(30, e, false)
		stelle := slices.Index(args, "-svtav1-params")
		if stelle < 0 || stelle+1 >= len(args) {
			t.Fatalf("kein -svtav1-params in %v", args)
		}
		if args[stelle+1] != fall.erwartet {
			t.Errorf("Variance Boost %v (%d/%d), tune 0 %v: %q erwartet, bekommen %q",
				fall.varianceBoost, fall.staerke, fall.oktil, fall.tune0, fall.erwartet, args[stelle+1])
		}
	}
}

// Stärke und Oktil nehmen nur, was SVT-AV1 kennt. Eine 0 oder ein leerer
// Wert ist ein Fehler, der auf varianceBoost verweist — ausgeschaltet wird
// nur dort. Ein ungültiger Wert darf nichts verändern.
func TestVarianceReglerLesen(t *testing.T) {
	faelle := []struct {
		schluessel, wert string
		erwartet         int
		fehler           bool
	}{
		{"varianceBoostStaerke", "1", 1, false},
		{"varianceBoostStaerke", "4", 4, false},
		{"varianceBoostStaerke", "0", 0, true},
		{"varianceBoostStaerke", "5", 0, true},
		{"varianceBoostStaerke", "", 0, true},
		{"varianceBoostStaerke", "zwei", 0, true},
		{"varianceOktil", "1", 1, false},
		{"varianceOktil", "8", 8, false},
		{"varianceOktil", "0", 0, true},
		{"varianceOktil", "9", 0, true},
		{"varianceOktil", "", 0, true},
	}
	for _, fall := range faelle {
		e := standardWerte()
		vorher := standardWerte()
		err := wertUebernehmen(&e, fall.schluessel, fall.wert)

		if fall.fehler {
			if err == nil || !strings.Contains(err.Error(), "varianceBoost=ja/nein") {
				t.Errorf("%s=%q: Fehler mit Hinweis auf varianceBoost erwartet, bekommen %v",
					fall.schluessel, fall.wert, err)
			}
			if !reflect.DeepEqual(e, vorher) {
				t.Errorf("%s=%q: ungueltiger Wert hat die Einstellungen veraendert", fall.schluessel, fall.wert)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s=%q: unerwarteter Fehler %v", fall.schluessel, fall.wert, err)
		}
		bekommen := e.VarianceBoostStaerke
		if fall.schluessel == "varianceOktil" {
			bekommen = e.VarianceOktil
		}
		if bekommen != fall.erwartet {
			t.Errorf("%s=%q: %d erwartet, bekommen %d", fall.schluessel, fall.wert, fall.erwartet, bekommen)
		}
	}
}

// Das Protokoll nennt Stärke und Oktil nur, wenn der Variance Boost an ist —
// sonst sähe es aus, als hätten sie gewirkt.
func TestEinstellungenTextNenntFeinreglerNurMitVarianceBoost(t *testing.T) {
	e := standardWerte()
	e.VarianceBoost = false // ab Werk an (seit 0.19.0)
	e.VarianceBoostStaerke, e.VarianceOktil = 3, 7

	if text := einstellungenText(e); !strings.Contains(text, "Variance Boost aus,") {
		t.Errorf("Variance Boost aus erwartet: %q", text)
	}
	e.VarianceBoost = true
	if text := einstellungenText(e); !strings.Contains(text, "Variance Boost an (Staerke 3, Oktil 7),") {
		t.Errorf("Staerke und Oktil fehlen: %q", text)
	}
}

func TestINIMitBittiefe10(t *testing.T) {
	pfad := filepath.Join(t.TempDir(), "cloudforge.ini")
	if err := os.WriteFile(pfad, []byte("bittiefe=10\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	e, _, err := EinstellungenLaden(pfad)
	if err != nil {
		t.Fatalf("INI nicht ladbar: %v", err)
	}
	if e.Bittiefe != 10 {
		t.Errorf("Bittiefe 10 erwartet, bekommen %d", e.Bittiefe)
	}
}
