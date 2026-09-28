// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

// Grössenprobe (seit 0.13.0): Bevor umgewandelt wird, soll feststehen, ob
// sich das lohnt. Die Vorhersage aus den Qualitäts-Messstellen trifft meist,
// bei manchen Filmen aber nicht: Gemessen am 27.09.2026 lagen bei 4 von 22
// Filmen die 5 Messstellen 9 bis 13 Punkte daneben, immer zu günstig — ein
// Film wurde 13 Minuten umgewandelt und danach doch nur umgepackt. Innerhalb
// eines einzigen Films kostete dieselbe Einstellung je Stelle zwischen 38 und
// 122 % der Quelle. Mehr Stellen, gleichmässig über den ganzen Film verteilt,
// trafen an 4 nachgemessenen Filmen auf höchstens 5 Punkte.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

const (
	// groessenprobeStellen: so viele Stücke kodiert die Grössenprobe. 10 statt
	// der gemessenen 19 (Nutzerwahl 27.09.2026) — am Film mit dem grössten Fehler trafen 10
	// der 19 Stellen (jede zweite) die ganze Datei auf 2 Punkte.
	groessenprobeStellen = 10

	// grenzbereichPunkte: so nah muss die erste Vorhersage an der Schwelle
	// mindestProzent liegen, damit die Grössenprobe läuft. Der grösste
	// gemessene Fehler der 5 Messstellen war 13 Punkte. Filme, die klar
	// sparen oder klar nicht, kostet die Probe deshalb keine Minute.
	grenzbereichPunkte = 15.0
)

// imGrenzbereich sagt, ob die erste Vorhersage so nah an der Schwelle liegt,
// dass sie danebenliegen und die Entscheidung kippen könnte.
func imGrenzbereich(erwartetProzent, schwelleProzent float64) bool {
	return math.Abs(erwartetProzent-schwelleProzent) <= grenzbereichPunkte
}

// groessenprobeFenster verteilt anzahl Stücke gleichmässig über den GANZEN
// Film, jeweils mittig in seinem Abschnitt (bei 10 Stücken um 5 %, 15 %, …
// 95 %). Anders als bei der Qualitätsmessung gehören Vor- und Abspann dazu,
// denn gefragt ist die Grösse der ganzen Datei.
func groessenprobeFenster(dauerSek, laengeSek float64, anzahl int) []Fenster {
	if dauerSek <= 0 || laengeSek <= 0 || anzahl <= 0 {
		return nil
	}
	fenster := make([]Fenster, 0, anzahl)
	for i := 0; i < anzahl; i++ {
		mitte := dauerSek * (float64(i) + 0.5) / float64(anzahl)
		start := math.Max(0, math.Min(mitte-laengeSek/2, dauerSek-laengeSek))
		fenster = append(fenster, Fenster{StartSek: start, LaengeSek: laengeSek})
	}
	return fenster
}

// GroessenProbe kodiert die Stücke mit dem gewählten CRF und genau den
// Einstellungen der echten Umwandlung — ohne Qualitätsmessung, die steht ja
// schon fest — und liefert, welchen Anteil der Quelle das Bild dort kostet.
func GroessenProbe(ctx context.Context, quelle string, dauerSek float64, crf int, arbeitsOrdner string, e Einstellungen, melde Rueckmeldung) (float64, error) {
	fenster := groessenprobeFenster(dauerSek, e.MessfensterSek, groessenprobeStellen)
	if len(fenster) == 0 {
		return 0, fmt.Errorf("Spieldauer unbekannt - keine Groessenprobe moeglich")
	}

	quelleBytes, err := QuelleFensterBytes(ctx, e.FFprobePfad, quelle, fenster)
	if err != nil {
		return 0, err
	}
	if quelleBytes <= 0 {
		return 0, fmt.Errorf("Quellgroesse an den Probestellen unbekannt")
	}

	probe := filepath.Join(arbeitsOrdner, "groessenprobe.mkv")
	defer os.Remove(probe)

	args := append(fensterArgumente(quelle, fenster), videoArgumente(crf, e)...)
	args = append(args, probe)
	gesamtSek := 0.0
	for _, f := range fenster {
		gesamtSek += f.LaengeSek
	}
	if _, err := ffmpegLaufen(ctx, e.FFmpegPfad, args, gesamtSek, nurBalken(melde)); err != nil {
		return 0, fmt.Errorf("Groessenprobe fehlgeschlagen: %w", err)
	}
	return float64(DateiGroesse(probe)) / float64(quelleBytes), nil
}

// nurBalken reicht von einem ffmpeg-Stand nur Anteil und Zeiten weiter. Die
// Anzeige zeigt dann einen schlichten Balken — Bildnummer und Grössen-Prognose
// der Probestücke würden neben dem ganzen Film nur verwirren.
func nurBalken(melde Rueckmeldung) Rueckmeldung {
	if melde == nil {
		return nil
	}
	return func(s Stand) {
		melde(Stand{Anteil: s.Anteil, Tempo: s.Tempo, Rest: s.Rest})
	}
}

// ersparnisVorhersagen sagt voraus, wie viel kleiner die ganze Datei wird —
// VOR dem Umwandeln. Grundlage sind die Qualitäts-Messstellen; liegt deren
// Vorhersage knapp an der Schwelle, entscheidet die genauere Grössenprobe.
// Die Probe läuft als eigener Schritt (schritt von gesamt) mit Balken.
//
// ok=false: keine Vorhersage möglich; dann wird umgewandelt und hinterher
// geprüft wie vor 0.8.0. Ein Fehler kommt nur beim Abbruch zurück — scheitert
// die Probe selbst, gilt die erste Vorhersage, denn die Prüfung nach dem
// Umwandeln sichert weiterhin ab.
func ersparnisVorhersagen(ctx context.Context, anz *Anzeige, schritt, gesamt int, quelle string,
	info VideoInfo, autoCQ AutoCQErgebnis, arbeitsplatz string, e Einstellungen) (prozent float64, ok bool, err error) {

	erwartet, ok := erwarteteErsparnisProzent(info, autoCQ.AnteilQuelle)
	if !ok || !imGrenzbereich(erwartet, e.MindestErsparnisProzent) {
		return erwartet, ok, nil
	}

	anz.Schritt(schritt, gesamt, "Groesse pruefen")
	anteil, err := GroessenProbe(ctx, quelle, info.DauerSek, autoCQ.CRF, arbeitsplatz, e, anz.Stand)
	if errors.Is(err, ErrAbgebrochen) || ctx.Err() != nil {
		return 0, false, ErrAbgebrochen
	}
	if err != nil {
		anz.SchrittFertig("nicht moeglich, es gilt die erste Vorhersage")
		anz.Zeile("  Hinweis: %v", err)
		return erwartet, ok, nil
	}

	genauer, ok := erwarteteErsparnisProzent(info, anteil)
	anz.SchrittFertig(fmt.Sprintf("an %d Stellen ~%.0f %% der Quelle (erste Schaetzung ~%.0f %%)",
		groessenprobeStellen, anteil*100, autoCQ.AnteilQuelle*100))
	return genauer, ok, nil
}
