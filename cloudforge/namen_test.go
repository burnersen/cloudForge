package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Die Regeln sind aus NVENCForge übernommen — dieselben Namen hinein,
// dieselben heraus. Der erste Fall hat den Aufbau des Beispiels, mit dem der
// Nutzer die Bereinigung am 27.09.2026 bestellt hat.
func TestNamenBereinigen(t *testing.T) {
	faelle := []struct{ roh, erwartet string }{
		{"Anna.Beispiel.--.@Studio.--.Ein.Titel,.Teil.2,.Extra,.XY1234.--.2023-08-20.720p",
			"Anna.Beispiel.Studio.Ein.Titel.Teil.2.Extra.XY1234.2023.08.20.720p"},
		{"Mein Film (2020) [1080p]", "Mein.Film.2020.1080p"},
		{"Über den Wolken – Teil 1", "Über.den.Wolken.Teil.1"},
		{"Folge #3 - C# lernen", "Folge.Nr3.C.lernen"},
		{"Film.2019.1080p.WEB-DL.x264", "Film.2019.1080p.x264"},
		{"Film.2019.720P.HEVC", "Film.2019.720p.hevc"},
		{"Web.Serie.Folge.1", "Web.Serie.Folge.1"}, // "Web" mitten im Namen bleibt
		{"Web.Serie.1080p", "Serie.1080p"},         // steht hinten eine behaltene Markierung, fällt es weg
		{"Schon.Sauber.720p", "Schon.Sauber.720p"},
		{"..Film..", "Film"},
		{"1080p", "1080p"},
		{"mp4", ""}, // nichts Brauchbares — dann gilt der alte Name
		{"---", ""},
		{"", ""},
	}
	for _, fall := range faelle {
		if got := namenBereinigen(fall.roh); got != fall.erwartet {
			t.Errorf("%q: bekommen %q, erwartet %q", fall.roh, got, fall.erwartet)
		}
	}
}

// Umgewandelt wie umgepackt bekommt das Ergebnis den bereinigten Namen. Bleibt
// vom Namen nichts übrig, heisst das Ergebnis wie die Quelle.
func TestErgebnisPfadeSindBereinigt(t *testing.T) {
	e := standardWerte()
	quelle := filepath.Join("Daten", "Mein Film (2020).mp4")
	ausgabe := filepath.Join("Daten", "output")

	if got := ZielPfadFuer(quelle, e); got != filepath.Join(ausgabe, "Mein.Film.2020.av1.mkv") {
		t.Errorf("umgewandelt: %s", got)
	}
	if got := UmpackPfadFuer(quelle, "h264", e); got != filepath.Join(ausgabe, "Mein.Film.2020.h264.mkv") {
		t.Errorf("umgepackt: %s", got)
	}
	if got := ZielPfadFuer(filepath.Join("Daten", "---.mp4"), e); got != filepath.Join(ausgabe, "---.av1.mkv") {
		t.Errorf("ohne brauchbaren Namen: %s", got)
	}
}

// Ein Ergebnis zählt unter dem neuen, bereinigten Namen — und unter dem alten,
// den CloudForge bis 0.13.0 vergab. Sonst würde nach dem Update alles, was
// schon fertig im output-Ordner liegt, ein zweites Mal umgewandelt.
func TestVorhandenesErgebnisKenntNeueUndAlteNamen(t *testing.T) {
	e := standardWerte()
	wurzel := t.TempDir()
	ausgabe := filepath.Join(wurzel, e.AusgabeOrdnerName)
	if err := os.MkdirAll(ausgabe, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Neuer.Film.2021.av1.mkv", "Alter Film (2019).h264.mkv"} {
		if err := os.WriteFile(filepath.Join(ausgabe, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	faelle := map[string]string{
		"Neuer Film (2021).mp4": "Neuer.Film.2021.av1.mkv",
		"Alter Film (2019).mp4": "Alter Film (2019).h264.mkv",
		"Noch offen.mp4":        "",
	}
	for quelle, erwartet := range faelle {
		got := VorhandenesErgebnis(filepath.Join(wurzel, quelle), e)
		if filepath.Base(got) != erwartet && !(erwartet == "" && got == "") {
			t.Errorf("%s: bekommen %q, erwartet %q", quelle, got, erwartet)
		}
	}
}
