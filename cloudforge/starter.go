// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

// Legt die Starter für den Desktop an: ein Symbol zum Daraufziehen von
// Dateien und Ordnern, eines für den Automatik-Lauf und eines, das das
// Protokoll laufend zeigt.
//
// Warum ein Zwischenskript statt des Programms direkt: Ein Terminalfenster,
// das ein Programm startet, schliesst sich sofort wieder, wenn dieses fertig
// ist — man sähe das Ergebnis nie. Das Skript hält es offen, bis man die
// Eingabetaste drückt.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const huelleName = "cloudforge-fenster.sh"

// Merkmal, an dem XFCE (ab 4.18) einen vertrauenswürdigen Starter erkennt:
// die SHA-256-Prüfsumme des Dateiinhalts. Gemessen am 22.09.2026 mit XFCEs
// eigener Prüffunktion (libxfce4util 4.20): nach dem Setzen gilt der Starter
// als vertrauenswürdig, nach jeder Änderung am Inhalt nicht mehr.
const xfceVertrauensMerkmal = "metadata::xfce-exe-checksum"

// Schreibtisch-Ordner, falls das System keine Auskunft gibt.
var desktopOrdnerNamen = []string{"Desktop", "Schreibtisch", "Arbeitsfläche"}

type starterDatei struct {
	name   string
	inhalt string
}

// StarterAnlegen schreibt Hülle und Starter und markiert die Starter als
// vertrauenswürdig, damit der Desktop nicht bei jedem Start nachfragt.
func StarterAnlegen() error {
	programm, err := os.Executable()
	if err != nil {
		return fmt.Errorf("eigener Programmpfad nicht ermittelbar: %w", err)
	}
	if programm, err = filepath.Abs(programm); err != nil {
		return err
	}

	huellePfad := filepath.Join(filepath.Dir(programm), huelleName)
	if err := os.WriteFile(huellePfad, []byte(huelleInhalt(programm)), 0o755); err != nil {
		return fmt.Errorf("Hilfsskript nicht schreibbar: %w", err)
	}
	if err := os.Chmod(huellePfad, 0o755); err != nil {
		return err
	}
	fmt.Printf("\nHilfsskript:   %s\n", huellePfad)

	schreibtisch := desktopOrdner()
	ziele := starterZiele(schreibtisch)
	if len(ziele) == 0 {
		return fmt.Errorf("weder Schreibtisch noch Anwendungsmenue gefunden")
	}

	alleVertraut := true
	for _, ordner := range ziele {
		for _, starter := range starterListe(huellePfad) {
			pfad := filepath.Join(ordner, starter.name)
			if err := starterSchreiben(pfad, starter.inhalt); err != nil {
				return err
			}

			vermerk := "vertrauenswuerdig markiert"
			if err := alsVertrauenswuerdigMarkieren(pfad); err != nil {
				vermerk = "NICHT markiert (" + err.Error() + ")"
				alleVertraut = false
			}
			fmt.Printf("Starter:       %s  -  %s\n", pfad, vermerk)
		}
	}

	fmt.Println()
	if schreibtisch == "" {
		fmt.Println("Keinen Schreibtisch gefunden - die Starter liegen nur im Anwendungsmenue.")
		return nil
	}
	fmt.Println("Auf dem Schreibtisch liegen jetzt drei Symbole:")
	fmt.Println("  \"CloudForge: Dateien umwandeln\"  - Videos oder Ordner mit der Maus darauf ziehen")
	fmt.Println("  \"CloudForge: Automatik\"          - arbeitet einen festen Ordner ab")
	fmt.Println("  \"CloudForge: Protokoll ansehen\"  - zeigt laufend, was CloudForge tut")
	if !alleVertraut {
		fmt.Println()
		fmt.Println("Falls der Desktop beim ersten Mal nachfragt, ob er dem Starter")
		fmt.Println("vertrauen soll: \"Trotzdem starten\" bzw. \"Starten erlauben\" waehlen.")
	}
	return nil
}

// starterSchreiben legt die Datei an und macht sie ausführbar. Chmod steht
// extra da: os.WriteFile ändert die Rechte einer schon vorhandenen Datei nicht.
func starterSchreiben(pfad, inhalt string) error {
	if err := os.WriteFile(pfad, []byte(inhalt), 0o755); err != nil {
		return fmt.Errorf("%s nicht schreibbar: %w", pfad, err)
	}
	return os.Chmod(pfad, 0o755)
}

// desktopOrdner fragt das System, wo der Schreibtisch liegt — er heisst je
// nach Sprache anders. Ohne Antwort werden die üblichen Namen versucht.
func desktopOrdner() string {
	heim, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	if ausgabe, err := exec.Command("xdg-user-dir", "DESKTOP").Output(); err == nil {
		pfad := strings.TrimSpace(string(ausgabe))
		// Ohne eingerichteten Schreibtisch antwortet xdg-user-dir mit dem
		// Home-Ordner selbst — dort gehören keine Starter hin.
		if pfad != "" && filepath.Clean(pfad) != filepath.Clean(heim) && istOrdner(pfad) {
			return pfad
		}
	}

	for _, name := range desktopOrdnerNamen {
		if pfad := filepath.Join(heim, name); istOrdner(pfad) {
			return pfad
		}
	}
	return ""
}

// starterZiele sind der Schreibtisch und das Anwendungsmenü.
func starterZiele(schreibtisch string) []string {
	var ziele []string
	if schreibtisch != "" {
		ziele = append(ziele, schreibtisch)
	}
	if heim, err := os.UserHomeDir(); err == nil {
		menue := filepath.Join(heim, ".local", "share", "applications")
		if err := os.MkdirAll(menue, 0o755); err == nil {
			ziele = append(ziele, menue)
		}
	}
	return ziele
}

func istOrdner(pfad string) bool {
	zustand, err := os.Stat(pfad)
	return err == nil && zustand.IsDir()
}

// alsVertrauenswuerdigMarkieren setzt das Merkmal, an dem XFCE erkennt, dass
// der Starter ohne Rückfrage laufen darf.
//
// Muss NACH dem letzten Schreiben passieren: die Prüfsumme gilt nur für genau
// diesen Inhalt. Funktioniert nur auf einem Dateisystem mit Metadaten (der
// Home-Ordner ja, /tmp nein) und braucht den Sitzungsbus der Desktop-Sitzung.
func alsVertrauenswuerdigMarkieren(pfad string) error {
	inhalt, err := os.ReadFile(pfad)
	if err != nil {
		return err
	}
	summe := sha256.Sum256(inhalt)

	befehl := exec.Command("gio", "set", "-t", "string", pfad, xfceVertrauensMerkmal, hex.EncodeToString(summe[:]))
	befehl.Env = sitzungsUmgebung()
	if ausgabe, err := befehl.CombinedOutput(); err != nil {
		meldung := strings.TrimSpace(string(ausgabe))
		if meldung == "" {
			meldung = err.Error()
		}
		return fmt.Errorf("%s", meldung)
	}

	// GNOME benutzt ein eigenes Merkmal. Nur der Vollständigkeit halber
	// gesetzt — auf GNOME wurde das NICHT geprüft.
	gnome := exec.Command("gio", "set", pfad, "metadata::trusted", "true")
	gnome.Env = sitzungsUmgebung()
	_ = gnome.Run()
	return nil
}

func huelleInhalt(programm string) string {
	return `#!/bin/bash
# Von CloudForge angelegt. Haelt das Terminalfenster offen, damit das
# Ergebnis lesbar bleibt.

"` + programm + `" "$@"
ergebnis=$?

echo
if [ $ergebnis -ne 0 ]; then
    echo "CloudForge wurde mit einem Fehler beendet (siehe oben)."
fi
echo "Dieses Fenster schliesst sich mit der Eingabetaste."
read -r
`
}

// starterListe liefert die beiden .desktop-Dateien, immer in derselben
// Reihenfolge.
func starterListe(huellePfad string) []starterDatei {
	// %F uebergibt alle hineingezogenen Dateien und Ordner an das Programm.
	// Ohne diesen Platzhalter nimmt der Starter nichts entgegen.
	ziehen := `[Desktop Entry]
Type=Application
Version=1.0
Name=CloudForge: Dateien umwandeln
Comment=Videodateien oder Ordner mit der Maus hierher ziehen
Exec=` + huellePfad + ` %F
Icon=video-x-generic
Terminal=true
Categories=AudioVideo;Video;
MimeType=video/mp4;video/x-matroska;video/x-msvideo;video/quicktime;video/mpeg;video/webm;video/x-flv;video/x-ms-wmv;video/mp2t;inode/directory;
`

	automatik := `[Desktop Entry]
Type=Application
Version=1.0
Name=CloudForge: Automatik
Comment=Arbeitet den eingestellten Ordner ab
Exec=` + huellePfad + ` -automatik
Icon=media-playback-start
Terminal=true
Categories=AudioVideo;Video;
`

	// Seit 0.9.0: Der Zeitplan arbeitet ohne Fenster — ohne dieses Symbol
	// wäre nicht zu sehen, was er gerade tut.
	protokoll := `[Desktop Entry]
Type=Application
Version=1.0
Name=CloudForge: Protokoll ansehen
Comment=Zeigt laufend, was CloudForge tut - auch im Hintergrund
Exec=` + huellePfad + ` -protokoll
Icon=text-x-generic
Terminal=true
Categories=AudioVideo;Video;
`

	return []starterDatei{
		{"cloudforge-ziehen.desktop", ziehen},
		{"cloudforge-automatik.desktop", automatik},
		{"cloudforge-protokoll.desktop", protokoll},
	}
}
