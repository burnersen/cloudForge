// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

// Fortschrittsanzeige für lange Läufe.
//
// Warum eigens dafür Code: Eine Anzeige, die sich mit "\r" selbst überschreibt,
// sieht auf einem Terminal gut aus — in einer Logdatei entsteht daraus eine
// einzige, endlos lange Zeile. Deshalb prüft die Anzeige, wohin sie schreibt,
// und meldet sich bei einer Datei nur in ruhigen Abständen mit ganzen Zeilen.

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Wie oft sich die Anzeige meldet, wenn sie in eine Datei schreibt.
const logMeldungAlle = 50

// Fortschritt zählt Schritte und zeigt sie passend zum Ausgabeziel an.
type Fortschritt struct {
	gesamt     int
	erledigt   int
	amTerminal bool
	start      time.Time
}

// NeuerFortschritt legt eine Anzeige für die angegebene Gesamtzahl an.
func NeuerFortschritt(gesamt int) *Fortschritt {
	return &Fortschritt{
		gesamt:     gesamt,
		amTerminal: schreibtAufTerminal(),
		start:      time.Now(),
	}
}

// Schritt meldet einen erledigten Schritt.
func (f *Fortschritt) Schritt() {
	f.erledigt++

	if f.amTerminal {
		fmt.Printf("\r  %d von %d ...", f.erledigt, f.gesamt)
		return
	}
	if f.erledigt%logMeldungAlle == 0 || f.erledigt == f.gesamt {
		fmt.Printf("  %d von %d (%s)\n", f.erledigt, f.gesamt, f.restzeitText())
	}
}

// Fertig räumt die Anzeige ab, damit die nächste Ausgabe sauber beginnt.
func (f *Fortschritt) Fertig() {
	if f.amTerminal {
		fmt.Printf("\r%s\r", strings.Repeat(" ", 40))
	}
}

// restzeitText schätzt aus dem bisherigen Tempo, wie lange es noch dauert.
func (f *Fortschritt) restzeitText() string {
	if f.erledigt == 0 || f.erledigt >= f.gesamt {
		return "fertig"
	}
	verbraucht := time.Since(f.start)
	proSchritt := verbraucht / time.Duration(f.erledigt)
	rest := proSchritt * time.Duration(f.gesamt-f.erledigt)
	return "noch etwa " + dauerText(rest)
}

// dauerText gibt eine Dauer in einfachen Worten aus. Bei sehr langen Laufzeiten
// sagt "8 Wochen" mehr als "1344 Stunden".
func dauerText(d time.Duration) string {
	const tag = 24 * time.Hour
	const woche = 7 * tag

	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.0f Sekunden", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%.0f Minuten", d.Minutes())
	case d < tag:
		return fmt.Sprintf("%.1f Stunden", d.Hours())
	case d < 2*woche:
		return fmt.Sprintf("%.1f Tage", d.Hours()/24)
	default:
		return fmt.Sprintf("%.1f Wochen (%.0f Tage)", float64(d)/float64(woche), d.Hours()/24)
	}
}

// schreibtAufTerminal sagt, ob die Ausgabe an einem Terminal hängt oder in
// eine Datei bzw. Weiterleitung geht.
func schreibtAufTerminal() bool {
	zustand, err := os.Stdout.Stat()
	if err != nil {
		return false // im Zweifel die ruhige Variante
	}
	return zustand.Mode()&os.ModeCharDevice != 0
}
