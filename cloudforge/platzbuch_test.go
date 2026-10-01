// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

const gibibyte = 1 << 30

// platzFuer stellt die Reserve so ein, dass zwischen 3 und 4 GiB frei
// bleiben — egal, wie viel die Platte des Testrechners wirklich hat.
func platzFuer(t *testing.T) (string, Einstellungen) {
	t.Helper()
	ordner := t.TempDir()
	frei, err := FreierPlatzBytes(ordner)
	if err != nil {
		t.Skipf("freier Platz nicht lesbar: %v", err)
	}
	if frei < 8*gibibyte {
		t.Skip("zu wenig Platz fuer den Test")
	}
	e := standardWerte()
	e.PlatzReserveGB = int(frei/gibibyte) - 3
	return ordner, e
}

// Zwei Dateien zu je 2 GiB passen nicht gleichzeitig in 3 bis 4 GiB: Die
// zweite wartet, bis die erste ihren Platz zurückgibt.
func TestPlatzbuchLaesstWarten(t *testing.T) {
	ordner, e := platzFuer(t)
	buch := NeuesPlatzbuch()

	erste, err := buch.Vormerken(context.Background(), ordner, 2*gibibyte, e, nil)
	if err != nil {
		t.Fatalf("die erste Datei passt: %v", err)
	}

	gewartet := make(chan bool, 1)
	fertig := make(chan error, 1)
	go func() {
		zweite, err := buch.Vormerken(context.Background(), ordner, 2*gibibyte, e, func() { gewartet <- true })
		if err == nil {
			zweite()
		}
		fertig <- err
	}()

	select {
	case <-gewartet:
	case err := <-fertig:
		t.Fatalf("die zweite Datei haette warten muessen, bekam aber sofort: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("die zweite Datei meldet kein Warten")
	}
	erste()
	erste() // mehrfach freigeben darf nichts doppelt zurückgeben
	select {
	case err := <-fertig:
		if err != nil {
			t.Errorf("nach der Freigabe muss die zweite Datei Platz bekommen: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("die zweite Datei wartet nach der Freigabe immer noch")
	}
	if buch.vorgemerkt != 0 {
		t.Errorf("am Ende darf nichts mehr vorgemerkt sein: %d", buch.vorgemerkt)
	}
}

// Reicht der Platz schon ohne fremde Vormerkungen nicht, hilft Warten nicht:
// dann ist es ein Fehler wie bisher.
func TestPlatzbuchZuWenigPlatzIstFehler(t *testing.T) {
	ordner, e := platzFuer(t)
	if _, err := NeuesPlatzbuch().Vormerken(context.Background(), ordner, 5*gibibyte, e, nil); err == nil {
		t.Error("5 GiB in 3 bis 4 GiB: Fehler erwartet")
	}
}

// Wer wartet, hört bei einem Abbruch sofort auf.
func TestPlatzbuchAbbruchBeimWarten(t *testing.T) {
	ordner, e := platzFuer(t)
	buch := NeuesPlatzbuch()
	erste, err := buch.Vormerken(context.Background(), ordner, 2*gibibyte, e, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer erste()

	ctx, abbrechen := context.WithCancel(context.Background())
	abbrechen()
	if _, err := buch.Vormerken(ctx, ordner, 2*gibibyte, e, nil); !errors.Is(err, ErrAbgebrochen) {
		t.Errorf("Abbruch erwartet, bekommen %v", err)
	}
}
