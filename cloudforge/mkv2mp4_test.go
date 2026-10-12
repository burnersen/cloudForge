// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func dateiAnlegen(t *testing.T, pfad string) {
	t.Helper()
	if err := os.WriteFile(pfad, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Nur MKV direkt im Ordner — keine Unterordner, keine anderen Endungen.
func TestMkvDateienSuchen(t *testing.T) {
	ordner := t.TempDir()
	for _, name := range []string{"b.av1.mkv", "A.MKV", "c.mp4", "d.mkv.unfertig"} {
		dateiAnlegen(t, filepath.Join(ordner, name))
	}
	unter := filepath.Join(ordner, "output")
	if err := os.MkdirAll(unter, 0o755); err != nil {
		t.Fatal(err)
	}
	dateiAnlegen(t, filepath.Join(unter, "e.mkv"))

	gefunden, err := mkvDateienSuchen(ordner)
	if err != nil {
		t.Fatal(err)
	}
	erwartet := []string{filepath.Join(ordner, "A.MKV"), filepath.Join(ordner, "b.av1.mkv")}
	if !slices.Equal(gefunden, erwartet) {
		t.Errorf("bekommen %v, erwartet %v", gefunden, erwartet)
	}
}

func TestMp4NameFuer(t *testing.T) {
	faelle := map[string]string{
		"film.av1.mkv":    "film.av1.mp4", // bleibt als CloudForge-Ergebnis erkennbar
		"Mein Film.MKV":   "Mein Film.mp4",
		"a.b.c.mkv":       "a.b.c.mp4",
		"ohne.punkte.mkv": "ohne.punkte.mp4",
	}
	for mkv, erwartet := range faelle {
		if bekommen := mp4NameFuer(filepath.Join("x", mkv)); bekommen != erwartet {
			t.Errorf("%s: bekommen %s, erwartet %s", mkv, bekommen, erwartet)
		}
	}
	if !istErgebnisDatei(mp4NameFuer("film.av1.mkv")) {
		t.Error("die MP4 eines Ergebnisses muss Ergebnis bleiben, sonst wandelt CloudForge sie erneut um")
	}
}

// Die Originale werden gelöscht — nur ein ausdrückliches Ja startet.
func TestMkv2mp4Zugestimmt(t *testing.T) {
	for eingabe, erwartet := range map[string]bool{
		"j\n": true, "JA\n": true, " y \n": true, "yes": true,
		"\n": false, "": false, "n\n": false, "jein\n": false,
	} {
		if bekommen := mkv2mp4Zugestimmt(strings.NewReader(eingabe)); bekommen != erwartet {
			t.Errorf("%q: bekommen %v, erwartet %v", eingabe, bekommen, erwartet)
		}
	}
}

func TestMkv2mp4AnkuendigungKuerztLangeListen(t *testing.T) {
	var dateien []string
	for i := 0; i < mkv2mp4ListeHoechstens+5; i++ {
		dateien = append(dateien, filepath.Join("x", "film"+strings.Repeat("a", i)+".mkv"))
	}
	text := mkv2mp4Ankuendigung(dateien, filepath.Join("x", "output"))
	if !strings.Contains(text, "... und 5 weitere") || !strings.Contains(text, "GELOESCHT") {
		t.Errorf("Ankuendigung unvollstaendig:\n%s", text)
	}
}

// Ein CloudForge-Lauf, der parallel startet, darf die Arbeitsordner von
// mkv2mp4 nicht wegräumen.
func TestArbeitsresteLassenMkv2mp4InRuhe(t *testing.T) {
	ordner := t.TempDir()
	mkv := filepath.Join(ordner, mkv2mp4Vorsilbe+"123")
	alt := filepath.Join(ordner, arbeitsVorsilbe+"456")
	for _, o := range []string{mkv, alt} {
		if err := os.MkdirAll(o, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ArbeitsresteEntfernen(ordner)
	if !istOrdner(mkv) {
		t.Error("ArbeitsresteEntfernen hat den Arbeitsordner von mkv2mp4 geloescht")
	}
	if istOrdner(alt) {
		t.Error("eigene Reste von CloudForge muessen weiter verschwinden")
	}
}

// Mit echtem ffmpeg (nur auf dem Server): H.264+AAC wird MP4 in output,
// das Original verschwindet; Opus bleibt liegen; eine schon vorhandene MP4
// wird nicht überschrieben und ihr Original nicht gelöscht.
func TestMkvUmpackenMitEchtemFFmpeg(t *testing.T) {
	ffmpeg := os.Getenv("CLOUDFORGE_TEST_FFMPEG")
	ffprobe := os.Getenv("CLOUDFORGE_TEST_FFPROBE")
	if ffmpeg == "" || ffprobe == "" {
		t.Skip("CLOUDFORGE_TEST_FFMPEG und CLOUDFORGE_TEST_FFPROBE nicht gesetzt")
	}
	ctx := context.Background()
	ordner := t.TempDir()
	e := standardWerte()
	e.FFmpegPfad, e.FFprobePfad = ffmpeg, ffprobe
	e.ArbeitsOrdner = filepath.Join(ordner, "arbeit")
	e.PlatzReserveGB = 0
	ausgabe := filepath.Join(ordner, "output")
	for _, o := range []string{e.ArbeitsOrdner, ausgabe} {
		if err := os.MkdirAll(o, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	erzeugen := func(name, tonCodec string) string {
		pfad := filepath.Join(ordner, name)
		befehl := exec.Command(ffmpeg, "-nostdin", "-loglevel", "error", "-y",
			"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25:duration=3",
			"-f", "lavfi", "-i", "sine=duration=3",
			"-c:v", "libx264", "-c:a", tonCodec, "-shortest", pfad)
		if ausgabe, err := befehl.CombinedOutput(); err != nil {
			t.Fatalf("%s nicht erzeugbar: %v (%s)", name, err, ausgabe)
		}
		return pfad
	}
	anz := NeueAnzeige()

	gut := erzeugen("film.av1.mkv", "aac")
	if grund, err := mkvUmpacken(ctx, anz, gut, ausgabe, e); grund != "" || err != nil {
		t.Fatalf("umpacken: Grund %q, Fehler %v", grund, err)
	}
	ziel := filepath.Join(ausgabe, "film.av1.mp4")
	if DateiGroesse(ziel) == 0 {
		t.Error("MP4 fehlt in output")
	}
	if _, err := os.Stat(gut); !os.IsNotExist(err) {
		t.Error("das Original muss nach erfolgreichem Umpacken weg sein")
	}
	if info, err := KopfdatenLesen(ffprobe, ziel); err != nil || len(info.Tonspuren) != 1 {
		t.Errorf("MP4 unbrauchbar: %v, Ton %v", err, info.Tonspuren)
	}

	opus := erzeugen("opus.mkv", "libopus")
	if grund, err := mkvUmpacken(ctx, anz, opus, ausgabe, e); grund == "" || err != nil {
		t.Errorf("Opus muss liegen bleiben: Grund %q, Fehler %v", grund, err)
	}
	if DateiGroesse(opus) == 0 {
		t.Error("Opus-Original darf nicht geloescht werden")
	}

	doppelt := erzeugen("doppelt.mkv", "aac")
	dateiAnlegen(t, filepath.Join(ausgabe, "doppelt.mp4"))
	if grund, err := mkvUmpacken(ctx, anz, doppelt, ausgabe, e); grund == "" || err != nil {
		t.Errorf("vorhandene MP4: Grund %q, Fehler %v", grund, err)
	}
	if DateiGroesse(doppelt) == 0 || DateiGroesse(filepath.Join(ausgabe, "doppelt.mp4")) != 1 {
		t.Error("vorhandene MP4 ueberschrieben oder Original geloescht")
	}

	reste, _ := os.ReadDir(e.ArbeitsOrdner)
	if len(reste) != 0 {
		t.Errorf("Arbeitsordner nicht aufgeraeumt: %d Eintraege", len(reste))
	}
}
