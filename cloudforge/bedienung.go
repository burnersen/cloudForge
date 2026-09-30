// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

// Alles, was das Programm für Menschen ohne Technikkenntnisse bedienbar macht:
// verständliche Hinweise statt Fehlermeldungen, Nachfragen statt Abbrüche,
// Warten statt Durcheinander, und eine Nachricht, wenn es fertig ist.

import (
	"bufio"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Hinweis ist ein Ende ohne Fehler: der Nutzer hat etwas anders bedient als
// gedacht, und bekommt erklärt, wie es geht. Wird ohne "FEHLER" angezeigt.
type Hinweis string

func (h Hinweis) Error() string { return string(h) }

// eingabeVomTerminal sagt, ob jemand an der Tastatur sitzt, der antworten kann.
func eingabeVomTerminal() bool {
	zustand, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return zustand.Mode()&os.ModeCharDevice != 0
}

// pfadAusEingabe macht aus dem, was beim Hineinziehen in ein Terminalfenster
// ankommt, einen sauberen Pfad. Je nach Terminal kommt er in einfachen oder
// doppelten Anführungszeichen, mit Rückstrichen vor Leerzeichen oder als
// file://-Adresse — immer mit einem Leerzeichen am Ende.
func pfadAusEingabe(eingabe string) string {
	pfad := strings.TrimSpace(eingabe)
	if pfad == "" {
		return ""
	}

	gequotet := false
	if len(pfad) >= 2 {
		erstes, letztes := pfad[0], pfad[len(pfad)-1]
		if (erstes == '\'' && letztes == '\'') || (erstes == '"' && letztes == '"') {
			pfad = pfad[1 : len(pfad)-1]
			gequotet = true
		}
	}

	if strings.HasPrefix(pfad, "file://") {
		if adresse, err := url.Parse(pfad); err == nil {
			pfad = adresse.Path
		}
	} else if !gequotet {
		pfad = rueckstricheAufloesen(pfad)
	}

	return filepath.Clean(pfad)
}

// rueckstricheAufloesen macht aus "Mein\ Ordner" wieder "Mein Ordner".
func rueckstricheAufloesen(text string) string {
	var ergebnis strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] == '\\' && i+1 < len(text) {
			i++
		}
		ergebnis.WriteByte(text[i])
	}
	return ergebnis.String()
}

// automatikOrdnerErfragen fragt beim ersten Start der Automatik nach dem
// Ordner, statt mit einer Fehlermeldung abzubrechen, und merkt ihn sich.
func automatikOrdnerErfragen(iniPfad string) ([]string, error) {
	if !eingabeVomTerminal() {
		return nil, fmt.Errorf("fuer die Automatik ist kein Ordner eingetragen (quellOrdner= in %s)", iniPfad)
	}

	fmt.Println()
	fmt.Println("Fuer die Automatik ist noch kein Ordner eingestellt.")
	fmt.Println()
	fmt.Println("  Zieh den Ordner, der abgearbeitet werden soll, mit der Maus")
	fmt.Println("  in DIESES Fenster und druecke dann die Eingabetaste.")
	fmt.Println()
	fmt.Print("> ")

	zeile, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && strings.TrimSpace(zeile) == "" {
		return nil, Hinweis("keine Eingabe - die Automatik wurde nicht gestartet")
	}

	ordner := pfadAusEingabe(zeile)
	if ordner == "" || ordner == "." {
		return nil, Hinweis("kein Ordner angegeben - die Automatik wurde nicht gestartet")
	}
	if zustand, err := os.Stat(ordner); err != nil || !zustand.IsDir() {
		return nil, Hinweis(fmt.Sprintf("%q ist kein Ordner - bitte einen Ordner hineinziehen, keine Datei", ordner))
	}

	if err := IniWertSetzen(iniPfad, "quellOrdner", ordner); err != nil {
		return nil, err
	}
	fmt.Println()
	fmt.Println("Gemerkt. Die Automatik arbeitet ab jetzt immer diesen Ordner ab:")
	fmt.Printf("  %s\n", ordner)
	fmt.Printf("(Aendern: in %s die Zeile quellOrdner=)\n", iniPfad)
	return []string{ordner}, nil
}

// keineDateienHinweis erklärt, was zu tun ist, wenn das Symbol nur angeklickt
// statt mit Dateien bestückt wurde.
func keineDateienHinweis() error {
	return Hinweis("es wurden keine Dateien uebergeben.\n\n" +
		"  So geht es: Zieh eine oder mehrere Videodateien - oder einen ganzen\n" +
		"  Ordner - mit der Maus auf das Symbol \"CloudForge: Dateien umwandeln\".\n" +
		"  Nur anklicken reicht nicht, das Programm braucht die Dateien.\n\n" +
		"  Im Terminal geht auch:  cloudforge DATEI_ODER_ORDNER\n" +
		"  Alle Moeglichkeiten:    cloudforge -hilfe")
}

// laufSperren wartet, bis kein anderes CloudForge mehr rechnet, und sagt
// dabei, warum gewartet wird. Solange es wartet, hat es Vorfahrt: ein
// Hintergrundlauf des Zeitplans macht nach seiner aktuellen Datei Platz.
func laufSperren(ctx context.Context, e Einstellungen, anz *Anzeige) (func(), error) {
	abmelden := func() {}
	freigeben, err := SperreHolen(ctx, sperrPfad(e), func() {
		abmelden = VorfahrtAnmelden(vorfahrtPfad(e))
		anz.Titel("wartet")
		anz.Zeile("")
		anz.Zeile("CloudForge arbeitet gerade - in einem anderen Fenster oder im Hintergrund (Zeitplan).")
		anz.Zeile("Deine Dateien kommen danach von selbst dran - lass dieses Fenster einfach offen.")
		anz.Zeile("Ein Hintergrundlauf macht nach seiner aktuellen Datei fuer dich Platz.")
	})
	abmelden()
	return freigeben, err
}

// einstellungenText fasst zusammen, womit umgewandelt wird. Steht vor jedem
// Lauf im Fenster und im Protokoll: der Nutzer probiert die Schalter selbst
// aus, und SVT-AV1 schreibt sie nicht in die Datei — so lässt sich später
// jede Datei ihrer Einstellung zuordnen.
func einstellungenText(e Einstellungen) string {
	return fmt.Sprintf("Ziel-VMAF %s, Preset %d, %d Bit, Variance Boost %s, tune 0 %s",
		komma(e.ZielVMAF, 1), e.Preset, e.Bittiefe, varianceBoostText(e), anAus(e.Tune0))
}

// varianceBoostText nennt die Feinregler nur, wenn sie auch wirken.
func varianceBoostText(e Einstellungen) string {
	if !e.VarianceBoost {
		return "aus"
	}
	return fmt.Sprintf("an (Staerke %d, Oktil %d)", e.VarianceBoostStaerke, e.VarianceOktil)
}

func anAus(an bool) string {
	if an {
		return "an"
	}
	return "aus"
}

// laufHinweiseZeigen sagt vor einem Lauf, worauf man achten muss.
func laufHinweiseZeigen(e Einstellungen, erfahrung Erfahrung) {
	fmt.Printf("%s.\nOriginale: %s, %d Kerne.\n", einstellungenText(e), e.OriginalBehandlung, e.Kerne)
	fmt.Println()
	fmt.Println("  " + stundeFilmText(erfahrung))
	fmt.Println("  Dieses Fenster offen lassen - minimieren ist in Ordnung.")
	fmt.Println("  Den Remote-Desktop darfst du schliessen, aber NICHT abmelden.")
	fmt.Println("  Aufhoeren: Fenster schliessen oder Strg+C. Es geht nichts verloren.")
	fmt.Println(strings.Repeat("-", 78))
}

// stundeFilmText sagt in Worten, wie lange eine Stunde Film etwa dauert. Die
// Bildrate macht den grossen Unterschied: 50 Bilder/s sind fast doppelt so
// viel Arbeit wie 30 — deshalb beide Zahlen statt einer irreführenden.
func stundeFilmText(erfahrung Erfahrung) string {
	const stunde = 3600
	herkunft := "Startwert, wird nach jeder Datei genauer"
	if erfahrung.Dateien > 0 {
		herkunft = fmt.Sprintf("gemessen an den letzten %d Dateien", erfahrung.Dateien)
	}
	return fmt.Sprintf("Das braucht Zeit: eine Stunde Film dauert etwa %s (30 Bilder/s)\n"+
		"  bis %s (50 Bilder/s) - %s.",
		uhrText(erfahrung.DauerFuerBilder(stunde*30)), uhrText(erfahrung.DauerFuerBilder(stunde*50)), herkunft)
}

// sitzungsUmgebung liefert die Umgebung für Hilfsprogramme des Desktops
// (gio, notify-send). Über SSH fehlt die Adresse des Sitzungsbusses; dann
// wird der übliche Ort eingesetzt, sofern es ihn gibt.
func sitzungsUmgebung() []string {
	umgebung := os.Environ()
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") != "" {
		return umgebung
	}
	bus := fmt.Sprintf("/run/user/%d/bus", os.Getuid())
	if _, err := os.Stat(bus); err == nil {
		umgebung = append(umgebung, "DBUS_SESSION_BUS_ADDRESS=unix:path="+bus)
	}
	return umgebung
}

// fertigMelden setzt den Fenstertitel und zeigt eine Desktop-Nachricht —
// nach Stunden sitzt niemand mehr vor dem Fenster.
//
// Nur, wenn das Programm aus der Desktop-Sitzung heraus gestartet wurde.
// Ein Lauf über SSH soll keine Nachrichten auf fremde Bildschirme schicken.
// Der Zeitplan läuft in der Sitzung des Nutzers (ohne DISPLAY-Angabe) und
// meldet sich nur, wenn er wirklich etwas umgewandelt hat — nicht alle 30
// Minuten für nichts.
func fertigMelden(anz *Anzeige, lauf laufBilanz, hintergrund bool) {
	anz.Titel("fertig")

	if hintergrund && lauf.umgewandelt+lauf.umgepackt == 0 {
		return
	}
	if !hintergrund && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return
	}
	text := fmt.Sprintf("%d Datei(en) umgewandelt, %s gespart.", lauf.umgewandelt, groesseText(lauf.gespartBytes))
	if lauf.umgepackt > 0 {
		text = fmt.Sprintf("%d umgewandelt, %d umgepackt, %s gespart.",
			lauf.umgewandelt, lauf.umgepackt, groesseText(lauf.gespartBytes))
	}
	befehl := exec.Command("notify-send", "--icon=video-x-generic", "CloudForge ist fertig", text)
	befehl.Env = sitzungsUmgebung()
	_ = befehl.Run() // fehlt notify-send, gibt es eben keine Nachricht
}
