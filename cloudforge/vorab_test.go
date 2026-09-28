// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// vorabUmgebung legt eine "Cloud"-Datei und einen leeren Arbeitsordner an.
func vorabUmgebung(t *testing.T, groesse int) (quelle string, e Einstellungen) {
	t.Helper()
	wurzel := t.TempDir()
	quelle = filepath.Join(wurzel, "cloud", "film.mp4")
	if err := os.MkdirAll(filepath.Dir(quelle), 0o755); err != nil {
		t.Fatal(err)
	}
	inhalt := bytes.Repeat([]byte("CloudForge "), groesse/11+1)[:groesse]
	if err := os.WriteFile(quelle, inhalt, 0o644); err != nil {
		t.Fatal(err)
	}
	e = standardWerte()
	e.ArbeitsOrdner = filepath.Join(wurzel, "arbeit")
	e.PlatzReserveGB = 0
	if err := os.MkdirAll(e.ArbeitsOrdner, 0o755); err != nil {
		t.Fatal(err)
	}
	return quelle, e
}

// arbeitsOrdnerLeer sagt, ob nach dem Aufräumen nichts liegen geblieben ist.
func arbeitsOrdnerLeer(t *testing.T, ordner string) {
	t.Helper()
	eintraege, err := os.ReadDir(ordner)
	if err != nil {
		t.Fatal(err)
	}
	for _, eintrag := range eintraege {
		t.Errorf("liegen geblieben: %s", eintrag.Name())
	}
}

func TestVorabKopieWirdUebernommen(t *testing.T) {
	quelle, e := vorabUmgebung(t, 3<<20)
	vorab, grund := VorabHolenStarten(context.Background(), quelle, 0, e)
	if vorab == nil {
		t.Fatalf("Vorab-Kopie nicht gestartet: %s", grund)
	}

	ziel := filepath.Join(t.TempDir(), "film.mp4")
	if err := vorab.Uebernehmen(context.Background(), ziel, nil); err != nil {
		t.Fatalf("Uebernehmen gescheitert: %v", err)
	}
	vorab.Verwerfen() // nach der Übernahme nur noch der leere Ordner

	original, _ := os.ReadFile(quelle)
	kopie, err := os.ReadFile(ziel)
	if err != nil || !bytes.Equal(original, kopie) {
		t.Errorf("die uebernommene Kopie stimmt nicht mit der Quelle ueberein (%v)", err)
	}
	arbeitsOrdnerLeer(t, e.ArbeitsOrdner)
}

// Ändert sich die Quelle, nachdem sie vorab geholt wurde, darf die alte
// Fassung nicht umgewandelt werden — danach würde ja das NEUE Original
// weggeräumt.
func TestVorabKopieNachAenderungDerQuelleUnbrauchbar(t *testing.T) {
	quelle, e := vorabUmgebung(t, 1<<20)
	vorab, grund := VorabHolenStarten(context.Background(), quelle, 0, e)
	if vorab == nil {
		t.Fatalf("Vorab-Kopie nicht gestartet: %s", grund)
	}
	defer vorab.Verwerfen()
	<-vorab.fertig

	if err := os.WriteFile(quelle, []byte("inzwischen neu hochgeladen"), 0o644); err != nil {
		t.Fatal(err)
	}
	ziel := filepath.Join(t.TempDir(), "film.mp4")
	err := vorab.Uebernehmen(context.Background(), ziel, nil)
	if !errors.Is(err, errVorabUnbrauchbar) {
		t.Errorf("errVorabUnbrauchbar erwartet, bekommen: %v", err)
	}
	if _, statErr := os.Stat(ziel); statErr == nil {
		t.Error("die veraltete Kopie wurde trotzdem uebernommen")
	}
}

func TestVorabAbbruchWirdAlsAbbruchGemeldet(t *testing.T) {
	quelle, e := vorabUmgebung(t, 1<<20)
	ctx, abbrechen := context.WithCancel(context.Background())
	vorab, grund := VorabHolenStarten(ctx, quelle, 0, e)
	if vorab == nil {
		t.Fatalf("Vorab-Kopie nicht gestartet: %s", grund)
	}
	abbrechen()
	<-vorab.fertig // die Kopie ist zu Ende (abgebrochen oder gerade noch fertig) — der Abbruch zählt trotzdem

	err := vorab.Uebernehmen(ctx, filepath.Join(t.TempDir(), "film.mp4"), nil)
	if !errors.Is(err, ErrAbgebrochen) {
		t.Errorf("ErrAbgebrochen erwartet, bekommen: %v", err)
	}
	vorab.Verwerfen()
	arbeitsOrdnerLeer(t, e.ArbeitsOrdner)
}

func TestVorabVerwerfenRaeumtAuf(t *testing.T) {
	quelle, e := vorabUmgebung(t, 8<<20)
	vorab, grund := VorabHolenStarten(context.Background(), quelle, 0, e)
	if vorab == nil {
		t.Fatalf("Vorab-Kopie nicht gestartet: %s", grund)
	}
	vorab.Verwerfen() // mitten im Kopieren oder danach — beides muss sauber enden
	vorab.Verwerfen() // ein zweites Mal schadet nicht
	arbeitsOrdnerLeer(t, e.ArbeitsOrdner)

	var nichts *VorabKopie
	nichts.Verwerfen() // nil ist erlaubt, damit es per defer dastehen kann
}

// Die Kopie einer anderen Datei wird nicht übernommen, sondern weggeräumt.
func TestVorabFuerAndereDateiWirdVerworfen(t *testing.T) {
	quelle, e := vorabUmgebung(t, 1<<20)
	vorab, grund := VorabHolenStarten(context.Background(), quelle, 0, e)
	if vorab == nil {
		t.Fatalf("Vorab-Kopie nicht gestartet: %s", grund)
	}
	a := &Ablauf{Einstellungen: e, vorab: vorab}

	if bekommen := a.vorabFuer(filepath.Join(filepath.Dir(quelle), "anderer-film.mp4")); bekommen != nil {
		t.Error("die Kopie einer anderen Datei wurde herausgegeben")
	}
	if a.vorab != nil {
		t.Error("die Kopie haengt noch am Ablauf")
	}
	arbeitsOrdnerLeer(t, e.ArbeitsOrdner)
}

// Reicht der Platz nicht, wird gar nicht erst vorab geholt.
func TestVorabOhnePlatzStartetNicht(t *testing.T) {
	quelle, e := vorabUmgebung(t, 1<<20)
	e.PlatzReserveGB = 1 << 30 // 1 EB Reserve: mehr, als jede Platte hat (auch die 1 PB aus platz_other.go)
	if vorab, _ := VorabHolenStarten(context.Background(), quelle, 0, e); vorab != nil {
		vorab.Verwerfen()
		t.Error("trotz fehlenden Platzes wurde vorab geholt")
	}
	arbeitsOrdnerLeer(t, e.ArbeitsOrdner)
}

// Der Weg durch den Ablauf: während die eine Datei umgewandelt wird, startet
// die Kopie der nächsten; beim nächsten Holen wird sie übernommen.
func TestAblaufHoltNaechsteDateiVorab(t *testing.T) {
	quelle, e := vorabUmgebung(t, 2<<20)
	a := &Ablauf{Einstellungen: e, Anzeige: NeueAnzeige(), Naechste: quelle}

	a.naechsteVorabHolen(context.Background(), 0)
	vorab := a.vorabFuer(quelle)
	if vorab == nil {
		t.Fatal("die naechste Datei wurde nicht vorab geholt")
	}
	defer vorab.Verwerfen()

	ziel := filepath.Join(t.TempDir(), "film.mp4")
	woher, err := a.holen(context.Background(), vorab, quelle, ziel)
	if err != nil || woher != ", vorab geholt" {
		t.Errorf("Uebernahme erwartet, bekommen %q (%v)", woher, err)
	}
	vorab.Verwerfen()
	a.VorabVerwerfen()
	arbeitsOrdnerLeer(t, e.ArbeitsOrdner)
}

// Gilt die Vorab-Kopie nicht, wird ganz normal geholt — und zwar die NEUE
// Fassung der Quelle.
func TestHolenOhneGueltigeVorabKopieHoltNeu(t *testing.T) {
	quelle, e := vorabUmgebung(t, 1<<20)
	a := &Ablauf{Einstellungen: e, Anzeige: NeueAnzeige()}
	vorab, grund := VorabHolenStarten(context.Background(), quelle, 0, e)
	if vorab == nil {
		t.Fatalf("Vorab-Kopie nicht gestartet: %s", grund)
	}
	defer vorab.Verwerfen()
	<-vorab.fertig

	neu := []byte("inzwischen neu hochgeladen")
	if err := os.WriteFile(quelle, neu, 0o644); err != nil {
		t.Fatal(err)
	}
	ziel := filepath.Join(t.TempDir(), "film.mp4")
	woher, err := a.holen(context.Background(), vorab, quelle, ziel)
	if err != nil || woher != "" {
		t.Fatalf("normales Holen erwartet, bekommen %q (%v)", woher, err)
	}
	if geholt, _ := os.ReadFile(ziel); !bytes.Equal(geholt, neu) {
		t.Error("geholt wurde nicht die neue Fassung der Quelle")
	}
}
