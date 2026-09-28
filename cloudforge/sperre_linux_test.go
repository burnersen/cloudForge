// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

//go:build linux

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Der Hintergrundlauf wartet nie: ist die Sperre belegt, meldet er "nicht
// frei" und hört auf. Ist sie frei, bekommt er sie.
func TestSperreVersuchenWartetNicht(t *testing.T) {
	pfad := filepath.Join(t.TempDir(), "test.lock")

	halten, err := SperreHolen(context.Background(), pfad, func() {})
	if err != nil {
		t.Fatal(err)
	}
	beginn := time.Now()
	_, frei, err := SperreVersuchen(pfad)
	if err != nil || frei {
		t.Errorf("belegte Sperre muss 'nicht frei' melden: frei %v, Fehler %v", frei, err)
	}
	if time.Since(beginn) > time.Second {
		t.Errorf("hat gewartet (%v), statt sofort aufzuhoeren", time.Since(beginn))
	}

	halten()
	freigeben, frei, err := SperreVersuchen(pfad)
	if err != nil || !frei {
		t.Fatalf("freie Sperre nicht bekommen: frei %v, Fehler %v", frei, err)
	}
	freigeben()
}

func TestVorfahrt(t *testing.T) {
	pfad := filepath.Join(t.TempDir(), "cloudforge.vorfahrt")

	if VorfahrtGewuenscht(pfad) {
		t.Error("ohne Anmeldung darf keine Vorfahrt gelten")
	}
	abmelden := VorfahrtAnmelden(pfad)
	if !VorfahrtGewuenscht(pfad) {
		t.Error("das wartende Fenster (dieser Prozess) muss Vorfahrt bekommen")
	}
	abmelden()
	if VorfahrtGewuenscht(pfad) {
		t.Error("nach dem Abmelden darf keine Vorfahrt mehr gelten")
	}

	// Ein hart beendetes Fenster: die Prozessnummer gibt es nicht mehr.
	os.WriteFile(pfad, []byte("2147483646"), 0o644)
	if VorfahrtGewuenscht(pfad) {
		t.Error("eine Anmeldung ohne lebenden Prozess darf nicht gelten")
	}
	if _, err := os.Stat(pfad); err == nil {
		t.Error("die verwaiste Anmeldung haette entfernt werden muessen")
	}

	// Ein zweites Fenster hat übernommen: das erste darf dessen Anmeldung
	// beim Abmelden nicht löschen.
	erstesAbmelden := VorfahrtAnmelden(pfad)
	os.WriteFile(pfad, []byte("1"), 0o644) // Prozess 1 lebt immer
	erstesAbmelden()
	if !VorfahrtGewuenscht(pfad) {
		t.Error("die Anmeldung des zweiten Fensters wurde faelschlich entfernt")
	}
}

// Zwei Fenster gleichzeitig: das zweite muss warten, und es muss sich beim
// Warten abbrechen lassen, statt als unsichtbarer Prozess zurückzubleiben.
func TestZweitesFensterWartet(t *testing.T) {
	pfad := filepath.Join(t.TempDir(), "test.lock")

	erstesFreigeben, err := SperreHolen(context.Background(), pfad, func() {})
	if err != nil {
		t.Fatalf("erstes Fenster bekommt die Sperre nicht: %v", err)
	}

	ctx, abbrechen := context.WithTimeout(context.Background(), 3*time.Second)
	defer abbrechen()
	gewartet := false
	_, err = SperreHolen(ctx, pfad, func() { gewartet = true })

	if !gewartet {
		t.Error("das zweite Fenster hat nicht gemeldet, dass es wartet")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("das zweite Fenster haette beim Abbruch aufhoeren muessen, Fehler: %v", err)
	}

	// Ist das erste fertig, kommt das nächste sofort dran.
	erstesFreigeben()
	danach, err := SperreHolen(context.Background(), pfad, func() { t.Error("wartet, obwohl frei") })
	if err != nil {
		t.Fatalf("nach dem Freigeben nicht zu haben: %v", err)
	}
	danach()
}
