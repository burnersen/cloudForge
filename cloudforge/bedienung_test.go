package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// So kommt ein Pfad an, wenn man eine Datei oder einen Ordner in ein
// Terminalfenster zieht — je nach Terminal verschieden.
func TestPfadAusEingabe(t *testing.T) {
	faelle := []struct {
		eingabe, erwartet, woher string
	}{
		{"'/home/b/pCloudDrive/Daten' \n", "/home/b/pCloudDrive/Daten", "xfce4-terminal: einfache Anfuehrungszeichen, Leerzeichen am Ende"},
		{"'/home/b/Mein Ordner' ", "/home/b/Mein Ordner", "Leerzeichen im Namen, gequotet"},
		{`"/home/b/Mein Ordner"`, "/home/b/Mein Ordner", "doppelte Anfuehrungszeichen"},
		{`/home/b/Mein\ Ordner`, "/home/b/Mein Ordner", "Rueckstrich vor dem Leerzeichen"},
		{"file:///home/b/Mein%20Ordner", "/home/b/Mein Ordner", "als Adresse mit %20"},
		{"/home/b/Daten/", "/home/b/Daten", "Schraegstrich am Ende"},
		{"   ", "", "nur Leerzeichen"},
		{"", "", "gar nichts"},
	}

	for _, f := range faelle {
		// FromSlash, damit der Test auch auf dem Windows-Entwicklungsrechner
		// läuft; unter Linux ändert es nichts.
		erwartet := filepath.FromSlash(f.erwartet)
		if bekommen := pfadAusEingabe(f.eingabe); bekommen != erwartet {
			t.Errorf("%s: aus %q wurde %q, erwartet %q", f.woher, f.eingabe, bekommen, erwartet)
		}
	}
}

func TestIniWertSetzenErsetztNurDenSchluessel(t *testing.T) {
	pfad := filepath.Join(t.TempDir(), "test.ini")
	vorher := "# Kommentar bleibt\n# quellOrdner=auskommentiert bleibt\nquellOrdner=\nkerne=6\n"
	if err := os.WriteFile(pfad, []byte(vorher), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := IniWertSetzen(pfad, "quellOrdner", "/home/b/Daten"); err != nil {
		t.Fatalf("unerwarteter Fehler: %v", err)
	}

	nachher, _ := os.ReadFile(pfad)
	erwartet := "# Kommentar bleibt\n# quellOrdner=auskommentiert bleibt\nquellOrdner=/home/b/Daten\nkerne=6\n"
	if string(nachher) != erwartet {
		t.Errorf("INI falsch geaendert:\n--- bekommen ---\n%s--- erwartet ---\n%s", nachher, erwartet)
	}
}

func TestIniWertSetzenHaengtFehlendenSchluesselAn(t *testing.T) {
	pfad := filepath.Join(t.TempDir(), "test.ini")
	if err := os.WriteFile(pfad, []byte("kerne=6\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := IniWertSetzen(pfad, "quellOrdner", "/x"); err != nil {
		t.Fatal(err)
	}

	nachher, _ := os.ReadFile(pfad)
	if string(nachher) != "kerne=6\nquellOrdner=/x\n" {
		t.Errorf("bekommen: %q", nachher)
	}

	// Und die geänderte INI muss sich wieder einlesen lassen.
	e, err := EinstellungenLaden(pfad)
	if err != nil {
		t.Fatalf("geaenderte INI nicht lesbar: %v", err)
	}
	if len(e.QuellOrdner) != 1 || e.QuellOrdner[0] != "/x" {
		t.Errorf("quellOrdner nicht uebernommen: %v", e.QuellOrdner)
	}
}

// Die gefährlichste Funktion dieser Runde: sie löscht. Sie darf nur
// entfernen, was eindeutig von CloudForge stammt — steht in der INI
// versehentlich der Home-Ordner als Arbeitsordner, bleibt alles andere heil.
func TestArbeitsresteEntfernenLaesstFremdesStehen(t *testing.T) {
	ordner := t.TempDir()
	eigene := []string{"datei-123456", "datei-abc"}
	for _, name := range eigene {
		if err := os.MkdirAll(filepath.Join(ordner, name, "unterordner"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(ordner, "film.av1.mkv.unfertig"), []byte("halb"), 0o644)

	fremde := []string{"Urlaub.mp4", "datei-notizen.txt", "wichtig"}
	os.WriteFile(filepath.Join(ordner, "Urlaub.mp4"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(ordner, "datei-notizen.txt"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(ordner, "wichtig"), 0o755)

	if entfernt := ArbeitsresteEntfernen(ordner); entfernt != 3 {
		t.Errorf("3 Reste erwartet, %d entfernt", entfernt)
	}
	for _, name := range fremde {
		if _, err := os.Stat(filepath.Join(ordner, name)); err != nil {
			t.Errorf("FREMDES wurde geloescht: %s", name)
		}
	}
	for _, name := range append(eigene, "film.av1.mkv.unfertig") {
		if _, err := os.Stat(filepath.Join(ordner, name)); err == nil {
			t.Errorf("Rest blieb liegen: %s", name)
		}
	}
}

func TestKopierenMitMeldungKopiertVollstaendig(t *testing.T) {
	quelle := bytes.Repeat([]byte("CloudForge"), 1_000_000) // 10 MB, mehr als ein Puffer
	var ziel bytes.Buffer
	var letzter Stand

	err := kopierenMitMeldung(context.Background(), &ziel, bytes.NewReader(quelle),
		int64(len(quelle)), func(s Stand) { letzter = s })
	if err != nil {
		t.Fatalf("unerwarteter Fehler: %v", err)
	}
	if !bytes.Equal(ziel.Bytes(), quelle) {
		t.Errorf("Inhalt verfaelscht: %d statt %d Bytes", ziel.Len(), len(quelle))
	}
	if letzter.Anteil != 1 {
		t.Errorf("am Ende muss 100 %% gemeldet werden, zuletzt: %v", letzter.Anteil)
	}
}

func TestKopierenMitMeldungHoertBeimAbbruchAuf(t *testing.T) {
	ctx, abbrechen := context.WithCancel(context.Background())
	abbrechen()

	var ziel bytes.Buffer
	err := kopierenMitMeldung(ctx, &ziel, strings.NewReader("Inhalt"), 6, nil)
	if err != ErrAbgebrochen {
		t.Errorf("ErrAbgebrochen erwartet, bekommen: %v", err)
	}
	if ziel.Len() != 0 {
		t.Errorf("nach dem Abbruch wurde trotzdem geschrieben: %d Bytes", ziel.Len())
	}
}

func TestKopierenHinterlaesstBeimAbbruchNichts(t *testing.T) {
	ordner := t.TempDir()
	quelle := filepath.Join(ordner, "quelle.mp4")
	ziel := filepath.Join(ordner, "output", "ziel.mkv")
	os.WriteFile(quelle, []byte("Videodaten"), 0o644)

	ctx, abbrechen := context.WithCancel(context.Background())
	abbrechen()

	if err := Kopieren(ctx, quelle, ziel, nil); err != ErrAbgebrochen {
		t.Fatalf("ErrAbgebrochen erwartet, bekommen: %v", err)
	}
	for _, pfad := range []string{ziel, ziel + unfertigEndung} {
		if _, err := os.Stat(pfad); err == nil {
			t.Errorf("nach dem Abbruch liegt noch etwas herum: %s", pfad)
		}
	}
	if _, err := os.Stat(quelle); err != nil {
		t.Errorf("die QUELLE ist weg: %v", err)
	}
}

func TestDateiErgebnisAbgebrochen(t *testing.T) {
	abgebrochen := DateiErgebnis{Eintrag: Eintrag{Status: StatusFehler, Meldung: ErrAbgebrochen.Error()}}
	echterFehler := DateiErgebnis{Eintrag: Eintrag{Status: StatusFehler, Meldung: "Pruefung nicht bestanden"}}
	fertig := DateiErgebnis{Eintrag: Eintrag{Status: StatusErledigt}}

	if !abgebrochen.Abgebrochen() {
		t.Error("ein Abbruch wurde nicht als solcher erkannt")
	}
	if echterFehler.Abgebrochen() {
		t.Error("ein echter Fehler wurde als Abbruch gewertet - er wuerde nie gemeldet")
	}
	if fertig.Abgebrochen() {
		t.Error("eine fertige Datei wurde als Abbruch gewertet")
	}
}

func TestSperreLaesstSichHolenUndFreigeben(t *testing.T) {
	pfad := filepath.Join(t.TempDir(), "test.lock")
	ctx, abbrechen := context.WithTimeout(context.Background(), 5*time.Second)
	defer abbrechen()

	freigeben, err := SperreHolen(ctx, pfad, func() { t.Error("es haette nicht gewartet werden duerfen") })
	if err != nil {
		t.Fatalf("Sperre nicht bekommen: %v", err)
	}
	freigeben()

	// Nach dem Freigeben muss sie sofort wieder zu haben sein.
	nochmal, err := SperreHolen(ctx, pfad, func() { t.Error("Sperre wurde nicht freigegeben") })
	if err != nil {
		t.Fatalf("Sperre nach dem Freigeben nicht wieder zu haben: %v", err)
	}
	nochmal()
}
