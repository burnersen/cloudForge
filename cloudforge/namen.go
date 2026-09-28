// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

// Ergebnisnamen bereinigen (seit 0.14.0, Nutzerwunsch: wie NVENCForge). Aus
//
//	Anna.Beispiel.--.@Studio.--.Ein.Titel,.Teil.2,.XY1234.--.2023-08-20.720p.mp4
//
// wird das Ergebnis
//
//	Anna.Beispiel.Studio.Ein.Titel.Teil.2.XY1234.2023.08.20.720p.av1.mkv
//
// Die Regeln stammen unverändert aus NVENCForge (normalizeName,
// extractTrailingMarkers, cleanFileBaseName), damit beide Programme dieselben
// Namen erzeugen. Bereinigt wird nur der Name des Ergebnisses — Originale
// werden nie umbenannt.

import (
	"regexp"
	"strings"
	"unicode"
)

// markierungenWeg sind Angaben am Namensende, die über das Ergebnis nichts
// Richtiges mehr sagen: Herkunft (web, bluray, ...), der alte Behälter (mp4,
// mkv, ...) und Freigabe-Kürzel. Sie fallen weg.
var markierungenWeg = map[string]bool{
	"ts": true, "m2ts": true,
	"web": true, "webrip": true, "webdl": true, "dl": true,
	"bluray": true, "bdrip": true, "remux": true, "bdremux": true,
	"hdtv": true, "dvdrip": true, "dvd": true, "p2p": true,
	"mp4": true, "mkv": true, "avi": true, "mov": true,
	"m4v": true, "wmv": true, "flv": true,
	"mpg": true, "mpeg": true, "webm": true,
	"xvid": true, "divx": true,
	"proper": true, "repack": true, "internal": true,
}

// markierungenBehalten bleiben am Namensende stehen, kleingeschrieben.
var markierungenBehalten = map[string]bool{
	"h264": true, "h265": true,
	"x264": true, "x265": true,
	"hevc": true, "av1": true, "vp9": true,
	"720p": true, "1080p": true, "1440p": true, "2160p": true, "4k": true,
	"hdr": true, "hdr10": true, "sdr": true,
	"10bit": true, "8bit": true,
	"aac": true, "ac3": true, "eac3": true, "dts": true, "opus": true,
}

var (
	rauteMitZiffer = regexp.MustCompile(`#(\d)`)
	mehrerePunkte  = regexp.MustCompile(`\.{2,}`)
)

// namenBereinigen liefert den bereinigten Namen (ohne Dateiendung) — oder "",
// wenn nichts Brauchbares übrig bleibt. Dann gilt der alte Name.
func namenBereinigen(ohneEndung string) string {
	name, behalten := endMarkierungenTrennen(zeichenBereinigen(ohneEndung))
	if len(behalten) == 0 {
		return name
	}
	if name == "" {
		return strings.Join(behalten, ".")
	}
	return name + "." + strings.Join(behalten, ".")
}

// zeichenBereinigen lässt Buchstaben und Ziffern jeder Schrift stehen (auch
// Umlaute), macht aus allem anderen — Leerzeichen, Satz- und Sonderzeichen —
// einen Punkt und fasst mehrere Punkte zu einem zusammen. Aus "#1" wird "Nr1".
func zeichenBereinigen(name string) string {
	name = rauteMitZiffer.ReplaceAllString(name, "Nr$1")
	name = strings.ReplaceAll(name, "#", "")
	name = strings.Map(func(zeichen rune) rune {
		if unicode.IsLetter(zeichen) || unicode.IsDigit(zeichen) {
			return zeichen
		}
		return '.'
	}, name)
	name = mehrerePunkte.ReplaceAllString(name, ".")
	return strings.Trim(name, ".")
}

// endMarkierungenTrennen nimmt die Markierungen vom Namensende: überflüssige
// fallen weg, behaltene kommen in ihrer Reihenfolge zurück. Stand hinten
// mindestens eine behaltene, fallen überflüssige auch mitten im Namen weg.
func endMarkierungenTrennen(name string) (rest string, behalten []string) {
	for {
		punkt := strings.LastIndex(name, ".")
		if punkt == -1 {
			break
		}
		teil := strings.ToLower(name[punkt+1:])
		if markierungenWeg[teil] {
			name = name[:punkt]
			continue
		}
		if markierungenBehalten[teil] {
			behalten = append([]string{teil}, behalten...)
			name = name[:punkt]
			continue
		}
		break
	}

	if len(behalten) > 0 && strings.Contains(name, ".") {
		var teile []string
		for _, teil := range strings.Split(name, ".") {
			if teil != "" && !markierungenWeg[strings.ToLower(teil)] {
				teile = append(teile, teil)
			}
		}
		name = strings.Join(teile, ".")
	}

	// Besteht der ganze Rest nur noch aus einer Markierung, gilt für ihn dasselbe.
	if name != "" {
		klein := strings.ToLower(name)
		if markierungenWeg[klein] {
			name = ""
		} else if markierungenBehalten[klein] {
			behalten = append([]string{klein}, behalten...)
			name = ""
		}
	}
	return name, behalten
}
