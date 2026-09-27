package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestProtokollSchreibtMitUhrzeit(t *testing.T) {
	ordner := t.TempDir()
	p, err := ProtokollOeffnen(ordner)
	if err != nil {
		t.Fatal(err)
	}
	jetzt := time.Now()
	p.zeileZu(jetzt, "erste Zeile\nzweite Zeile")
	p.Schliessen()
	p.Schliessen() // zweimal darf nichts kaputt machen

	inhalt, err := os.ReadFile(filepath.Join(ordner, jetzt.Format(protokollDatumFormat)+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	uhr := jetzt.Format("15:04:05")
	if !strings.Contains(string(inhalt), uhr+" erste Zeile\n"+uhr+" zweite Zeile\n") {
		t.Errorf("jede Zeile braucht ihre Uhrzeit:\n%s", inhalt)
	}

	if runtime.GOOS == "linux" {
		ziel, err := os.Readlink(filepath.Join(ordner, protokollAktuell))
		if err != nil || ziel != jetzt.Format(protokollDatumFormat)+".txt" {
			t.Errorf("aktuell.txt muss auf die heutige Datei zeigen: %q, %v", ziel, err)
		}
	}
}

func TestProtokollWechseltUmMitternacht(t *testing.T) {
	ordner := t.TempDir()
	p, err := ProtokollOeffnen(ordner)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Schliessen()

	morgen := time.Now().Add(24 * time.Hour)
	p.zeileZu(morgen, "am naechsten Tag")
	inhalt, err := os.ReadFile(filepath.Join(ordner, morgen.Format(protokollDatumFormat)+".txt"))
	if err != nil || !strings.Contains(string(inhalt), "am naechsten Tag") {
		t.Errorf("nach Mitternacht gehoert die Zeile in die neue Tagesdatei: %q, %v", inhalt, err)
	}
}

func TestAlteProtokolleWerdenGeloescht(t *testing.T) {
	ordner := t.TempDir()
	jetzt := time.Now()
	alt := jetzt.Add(-40*24*time.Hour).Format(protokollDatumFormat) + ".txt"
	frisch := jetzt.Add(-5*24*time.Hour).Format(protokollDatumFormat) + ".txt"
	fremd := "notizen.txt" // passt nicht ins Namensmuster: bleibt immer
	// Sieht aus wie ein altes Datum, ist aber keine Protokolldatei (keine
	// Endung) — nur die Namensprüfung schützt sie.
	ohneEndung := jetzt.Add(-400 * 24 * time.Hour).Format(protokollDatumFormat)
	for _, name := range []string{alt, frisch, fremd, ohneEndung} {
		if err := os.WriteFile(filepath.Join(ordner, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	alteProtokolleLoeschen(ordner, jetzt)

	if _, err := os.Stat(filepath.Join(ordner, alt)); err == nil {
		t.Error("das 40 Tage alte Protokoll haette weg sein muessen")
	}
	for _, name := range []string{frisch, fremd, ohneEndung} {
		if _, err := os.Stat(filepath.Join(ordner, name)); err != nil {
			t.Errorf("%s haette bleiben muessen: %v", name, err)
		}
	}
}

func TestAnzeigeSchreibtInsProtokoll(t *testing.T) {
	ordner := t.TempDir()
	p, err := ProtokollOeffnen(ordner)
	if err != nil {
		t.Fatal(err)
	}
	a := &Anzeige{} // wie in einer Logdatei: keine Übersicht, kein Terminal
	a.ProtokollSetzen(p)

	a.Datei(1, 2, "Film.mp4")
	a.Schritt(3, 5, "Umwandeln")
	for _, anteil := range []float64{0.05, 0.12, 0.13, 0.55, 1} {
		a.Stand(Stand{Anteil: anteil})
	}
	a.SchrittFertig("1,2 GB statt 2,4 GB")
	a.Messung(20, 98.21)
	p.Schliessen()

	inhalt, err := os.ReadFile(filepath.Join(ordner, time.Now().Format(protokollDatumFormat)+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(inhalt)
	for _, teil := range []string{"[1/2] Film.mp4", "3/5 Umwandeln ...", " 12 %", " 55 %", "100 %", "1,2 GB statt 2,4 GB", "CRF 20 ergibt VMAF 98,21"} {
		if !strings.Contains(text, teil) {
			t.Errorf("%q fehlt im Protokoll:\n%s", teil, text)
		}
	}
	// Nur der erste Wert jeder 10-%-Stufe: 13 % gehört zur Stufe von 12 %,
	// 5 % liegt noch in der Stufe 0, die "Umwandeln ..." schon abdeckt.
	for _, zuViel := range []string{" 13 %", "  5 %"} {
		if strings.Contains(text, zuViel) {
			t.Errorf("%q gehoert nicht ins Protokoll:\n%s", zuViel, text)
		}
	}
}

func TestNachGroesseOrdnen(t *testing.T) {
	ordner := t.TempDir()
	groessen := map[string]int{"klein.mp4": 10, "gross.mp4": 300, "mittel.mp4": 50}
	var pfade []string
	for _, name := range []string{"klein.mp4", "gross.mp4", "fehlt.mp4", "mittel.mp4"} {
		pfad := filepath.Join(ordner, name)
		if g, da := groessen[name]; da {
			if err := os.WriteFile(pfad, make([]byte, g), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		pfade = append(pfade, pfad)
	}

	sortiert, bytes := nachGroesseOrdnen(pfade)
	var namen []string
	for _, p := range sortiert {
		namen = append(namen, filepath.Base(p))
	}
	if strings.Join(namen, ",") != "gross.mp4,mittel.mp4,klein.mp4,fehlt.mp4" {
		t.Errorf("groesste zuerst, Unlesbares zuletzt erwartet, bekommen %v", namen)
	}
	if bytes[0] != 300 || bytes[3] != 0 {
		t.Errorf("Groessen passen nicht zur Reihenfolge: %v", bytes)
	}
}

func TestZeitplanDateien(t *testing.T) {
	dienst := zeitplanDienst("/home/nutzer/cloud forge/cloudforge", "/home/nutzer/cloud forge/cloudforge.ini")
	for _, teil := range []string{
		`ExecStart="/home/nutzer/cloud forge/cloudforge" -automatik -hintergrund -ini "/home/nutzer/cloud forge/cloudforge.ini"`,
		"Type=oneshot",
		"TimeoutStartSec=infinity", // sonst bricht systemd nach 90 s ab
	} {
		if !strings.Contains(dienst, teil) {
			t.Errorf("%q fehlt in der Dienst-Datei:\n%s", teil, dienst)
		}
	}
	timer := zeitplanTimer()
	for _, teil := range []string{"OnCalendar=*:0/30", "WantedBy=timers.target"} {
		if !strings.Contains(timer, teil) {
			t.Errorf("%q fehlt in der Timer-Datei:\n%s", teil, timer)
		}
	}
}
