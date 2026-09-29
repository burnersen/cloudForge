// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"slices"
	"strings"
	"testing"
)

// MP4-Textuntertitel kann MKV nicht aufnehmen (ffmpeg bricht ab), sie werden
// nach SRT übertragen. Alle anderen Formate bleiben 1:1 — gerade ASS, dessen
// Gestaltung SRT verlieren würde. Jede Spur bekommt ihre eigene Angabe.
func TestUntertitelArgumente(t *testing.T) {
	if got := untertitelArgumente(nil); !slices.Equal(got, []string{"-c:s", "copy"}) {
		t.Errorf("ohne Untertitel: %v", got)
	}

	got := untertitelArgumente([]string{"subrip", "mov_text", "ass", "hdmv_pgs_subtitle"})
	erwartet := []string{"-c:s:0", "copy", "-c:s:1", "srt", "-c:s:2", "copy", "-c:s:3", "copy"}
	if !slices.Equal(got, erwartet) {
		t.Errorf("bekommen %v, erwartet %v", got, erwartet)
	}
}

// wertNach liefert, was in args hinter der Angabe steht ("" wenn sie fehlt).
func wertNach(args []string, angabe string) string {
	stelle := slices.Index(args, angabe)
	if stelle < 0 || stelle+1 >= len(args) {
		return ""
	}
	return args[stelle+1]
}

// Seit 0.14.0: nur echte Filmspuren (grosses V, ohne Titelbilder, an denen
// SVT-AV1 scheiterte), Ton immer 1:1, Anhänge wie Schriften kommen mit. Das
// gilt fürs Umwandeln und fürs Umpacken gleich.
func TestEncodeArgumenteSpuren(t *testing.T) {
	e := standardWerte()
	faelle := map[string]EncodeAuftrag{
		"umwandeln": {Quelle: "q.mp4", Ziel: "z.mkv", CRF: 30, UntertitelCodecs: []string{"mov_text"}},
		"umpacken":  {Quelle: "q.mp4", Ziel: "z.mkv", Umpacken: true, UntertitelCodecs: []string{"mov_text"}},
	}
	for name, auftrag := range faelle {
		args := EncodeArgumente(auftrag, e)
		befehl := strings.Join(args, " ")

		if wertNach(args, "-map") != alleFilmspuren || strings.Contains(befehl, "0:v") {
			t.Errorf("%s: nur echte Filmspuren (%s) erwartet: %s", name, alleFilmspuren, befehl)
		}
		if wertNach(args, "-c:a") != "copy" || strings.Contains(befehl, "opus") {
			t.Errorf("%s: Ton muss 1:1 kopiert werden: %s", name, befehl)
		}
		if !slices.Contains(args, "0:t?") || wertNach(args, "-c:t") != "copy" {
			t.Errorf("%s: Anhaenge fehlen: %s", name, befehl)
		}
		if wertNach(args, "-c:s:0") != untertitelSRT {
			t.Errorf("%s: MP4-Textuntertitel muss nach SRT: %s", name, befehl)
		}
	}

	// Die Messproben nehmen nur die erste echte Filmspur, ohne Ton und Untertitel.
	probe := EncodeArgumente(EncodeAuftrag{Quelle: "q.mkv", Ziel: "p.mkv", CRF: 30, NurVideo: true}, e)
	if wertNach(probe, "-map") != ersteFilmspur || !slices.Contains(probe, "-an") || !slices.Contains(probe, "-sn") {
		t.Errorf("Messprobe: %v", probe)
	}
}

// Die Messfenster lesen dieselbe Spur wie die Messprobe — auch dort kein
// Titelbild, das zufällig als erste Videospur in der Datei steht.
func TestFensterArgumenteNehmenDieFilmspur(t *testing.T) {
	args := fensterArgumente("q.mp4", []Fenster{{StartSek: 10, LaengeSek: 8}, {StartSek: 50, LaengeSek: 8}}, "")
	kette := wertNach(args, "-filter_complex")
	if !strings.Contains(kette, "[0:V:0]") || !strings.Contains(kette, "[1:V:0]") || strings.Contains(kette, ":v:") {
		t.Errorf("Filter-Eingaenge muessen die echte Filmspur waehlen: %s", kette)
	}
}
