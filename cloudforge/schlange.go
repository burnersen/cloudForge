// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

// Mehrere Dateien gleichzeitig (seit 0.19.0, parallelDateien): Jeder Platz
// holt sich die nächste Datei aus einer gemeinsamen Warteschlange — die
// dicksten zuerst, wie bisher. Was eine Datei ergab, verbucht immer nur der
// aufrufende Ablauf (Bilanz, Gedächtnis, Vorfahrt), eine Datei nach der
// anderen; der Platz wartet darauf und fängt erst danach die nächste an. Mit
// nur einem Platz ist das genau der Ablauf von früher.

import (
	"context"
	"path/filepath"
	"sync"
)

// dateiSchlange verteilt die offenen Dateien auf die Plätze.
type dateiSchlange struct {
	mu       sync.Mutex
	pfade    []string
	groessen []int64 // passend zu pfade, für die Restzeit
	weiter   int     // Index der nächsten noch nicht angefangenen Datei
	gestoppt bool    // nichts Neues mehr anfangen (Abbruch, Vorfahrt)
}

func neueDateiSchlange(pfade []string, groessen []int64) *dateiSchlange {
	return &dateiSchlange{pfade: pfade, groessen: groessen}
}

// naechste gibt die nächste Datei heraus, nummer zählt ab 1. ok ist false,
// wenn alle angefangen sind oder die Schlange gestoppt wurde.
func (s *dateiSchlange) naechste() (nummer int, pfad string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gestoppt || s.weiter >= len(s.pfade) {
		return 0, "", false
	}
	s.weiter++
	return s.weiter, s.pfade[s.weiter-1], true
}

// vorschau nennt die Datei, die als nächste drankommt — leer, wenn keine mehr.
func (s *dateiSchlange) vorschau() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.weiter >= len(s.pfade) {
		return ""
	}
	return s.pfade[s.weiter]
}

// nichtAngefangen liefert die Grössen der Dateien, die noch warten.
func (s *dateiSchlange) nichtAngefangen() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int64(nil), s.groessen[s.weiter:]...)
}

func (s *dateiSchlange) anzahl() int {
	return len(s.pfade)
}

// stoppen lässt keine neue Datei mehr anfangen; was läuft, läuft zu Ende.
func (s *dateiSchlange) stoppen() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gestoppt = true
}

// dateiMeldung bringt das Ergebnis einer Datei zum Verbuchen. Über antwort
// sagt der Hauptablauf, ob der Platz weitermachen soll.
type dateiMeldung struct {
	anz      *Anzeige
	pfad     string
	ergebnis DateiErgebnis
	antwort  chan bool
}

// Verbucher nimmt das Ergebnis einer Datei entgegen und sagt, ob es
// weitergehen soll. Er läuft nie gleichzeitig mit sich selbst.
type Verbucher func(anz *Anzeige, pfad string, ergebnis DateiErgebnis) (weiter bool)

// dateienAbarbeiten lässt jeden Ablauf (einen je Platz) Dateien aus der
// Schlange holen und bearbeiten, bis sie leer ist oder verbuchen anhält.
// Kehrt erst zurück, wenn alle Plätze fertig sind.
func dateienAbarbeiten(ctx context.Context, schlange *dateiSchlange, ablaeufe []*Ablauf, verbuchen Verbucher) {
	meldungen := make(chan dateiMeldung)
	var laufend sync.WaitGroup
	allein := len(ablaeufe) == 1
	for _, ablauf := range ablaeufe {
		laufend.Add(1)
		go func(ablauf *Ablauf) {
			defer laufend.Done()
			platzArbeiten(ctx, schlange, ablauf, allein, meldungen)
		}(ablauf)
	}
	go func() {
		laufend.Wait()
		close(meldungen)
	}()

	for m := range meldungen {
		weiter := verbuchen(m.anz, m.pfad, m.ergebnis)
		if !weiter {
			schlange.stoppen() // auch die anderen Plätze fangen nichts Neues an
		}
		m.antwort <- weiter
	}
}

// platzArbeiten ist die Schleife eines Platzes. allein: Es gibt nur diesen
// einen — dann holt er die nächste Datei schon während des Umwandelns
// (vorab.go), wie bis 0.18.0. Mit mehreren Plätzen nicht: Die anderen halten
// die Kerne beschäftigt, während einer herunterlädt, und welcher Platz die
// nächste Datei bekommt, steht vorher nicht fest.
func platzArbeiten(ctx context.Context, schlange *dateiSchlange, ablauf *Ablauf, allein bool, meldungen chan<- dateiMeldung) {
	anz := ablauf.Anzeige
	if !allein {
		defer anz.PlatzFrei()
	}
	for {
		nummer, pfad, ok := schlange.naechste()
		if !ok {
			return
		}
		anz.Datei(nummer, schlange.anzahl(), filepath.Base(pfad))
		anz.Warteschlange(restDauer(schlange.nichtAngefangen(), ablauf.erfahrung()))
		ablauf.Naechste = ""
		if allein {
			ablauf.Naechste = schlange.vorschau()
		}

		ergebnis := ablauf.EineDatei(ctx, pfad)
		antwort := make(chan bool)
		meldungen <- dateiMeldung{anz: anz, pfad: pfad, ergebnis: ergebnis, antwort: antwort}
		if !<-antwort {
			return
		}
	}
}
