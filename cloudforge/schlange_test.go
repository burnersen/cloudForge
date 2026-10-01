// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// testAblaeufe baut einen Ablauf je Platz. Die Dateien gibt es nicht — jede
// ist darum sofort "nicht mehr da" und fertig, ohne ffmpeg.
func testAblaeufe(t *testing.T, plaetze, dateien int) ([]*Ablauf, *dateiSchlange) {
	t.Helper()
	e := standardWerte()
	e.ParallelDateien = plaetze
	var ablaeufe []*Ablauf
	for _, anz := range NeueAnzeige().Plaetze(plaetze) {
		ablaeufe = append(ablaeufe, &Ablauf{Einstellungen: e, Anzeige: anz})
	}
	ordner := t.TempDir()
	pfade := make([]string, dateien)
	for i := range pfade {
		pfade[i] = filepath.Join(ordner, fmt.Sprintf("film%02d.mp4", i))
	}
	return ablaeufe, neueDateiSchlange(pfade, make([]int64, dateien))
}

// Jede Datei wird genau einmal bearbeitet, egal wie viele Plätze — und
// verbucht wird nie gleichzeitig (Bilanz und Gedächtnis sind nicht geteilt).
func TestDateienAbarbeitenJedeDateiGenauEinmal(t *testing.T) {
	for _, plaetze := range []int{1, 2, 3} {
		ablaeufe, schlange := testAblaeufe(t, plaetze, 7)
		gesehen := map[string]int{}
		var gleichzeitig, hoechstens atomic.Int32
		dateienAbarbeiten(context.Background(), schlange, ablaeufe,
			func(_ *Anzeige, pfad string, ergebnis DateiErgebnis) bool {
				if n := gleichzeitig.Add(1); n > hoechstens.Load() {
					hoechstens.Store(n)
				}
				defer gleichzeitig.Add(-1)
				gesehen[pfad]++
				if ergebnis.Eintrag.Meldung != string(NichtMehrDa) {
					t.Errorf("%s: 'nicht mehr da' erwartet, bekommen %q", pfad, ergebnis.Eintrag.Meldung)
				}
				return true
			})
		if len(gesehen) != 7 {
			t.Errorf("%d Plaetze: 7 Dateien erwartet, %d verbucht", plaetze, len(gesehen))
		}
		for pfad, anzahl := range gesehen {
			if anzahl != 1 {
				t.Errorf("%d Plaetze: %s %d-mal verbucht", plaetze, filepath.Base(pfad), anzahl)
			}
		}
		if hoechstens.Load() > 1 {
			t.Errorf("%d Plaetze: es wurde gleichzeitig verbucht", plaetze)
		}
	}
}

// Sagt das Verbuchen "nicht weiter" (Abbruch, Vorfahrt), fängt kein Platz
// mehr eine neue Datei an. Was schon läuft, wird noch verbucht.
func TestDateienAbarbeitenHaeltAn(t *testing.T) {
	for _, plaetze := range []int{1, 3} {
		ablaeufe, schlange := testAblaeufe(t, plaetze, 10)
		verbucht := 0
		dateienAbarbeiten(context.Background(), schlange, ablaeufe,
			func(*Anzeige, string, DateiErgebnis) bool {
				verbucht++
				return verbucht < 2
			})
		// Höchstens die zweite Datei plus je eine, die die anderen Plätze
		// gerade in Arbeit hatten.
		if verbucht < 2 || verbucht > 2+plaetze-1 {
			t.Errorf("%d Plaetze: 2 bis %d verbuchte Dateien erwartet, bekommen %d", plaetze, 2+plaetze-1, verbucht)
		}
		if _, _, ok := schlange.naechste(); ok {
			t.Errorf("%d Plaetze: die Schlange muss gestoppt sein", plaetze)
		}
	}
}

func TestDateiSchlangeReihenfolgeUndVorschau(t *testing.T) {
	s := neueDateiSchlange([]string{"gross", "mittel", "klein"}, []int64{30, 20, 10})
	if v := s.vorschau(); v != "gross" {
		t.Errorf("Vorschau vor dem Start: %q", v)
	}
	nummer, pfad, ok := s.naechste()
	if !ok || nummer != 1 || pfad != "gross" {
		t.Errorf("erste Datei: %d %q %v", nummer, pfad, ok)
	}
	if v, rest := s.vorschau(), s.nichtAngefangen(); v != "mittel" || len(rest) != 2 || rest[0] != 20 {
		t.Errorf("nach der ersten: Vorschau %q, wartend %v", v, rest)
	}
	s.naechste()
	s.naechste()
	if _, _, ok := s.naechste(); ok || s.vorschau() != "" || len(s.nichtAngefangen()) != 0 {
		t.Error("nach der letzten darf nichts mehr kommen")
	}
}
