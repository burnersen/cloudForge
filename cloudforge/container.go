// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

// Behälter (Container) des Ergebnisses: MKV wie bisher oder MP4 (seit 0.21.0,
// Nutzerwunsch 12.10.2026). iPhone und andere Apple-Geräte öffnen ohne
// Zusatz-App nur MP4; das Bild darin ist dasselbe AV1.
//
// MP4 nimmt weniger auf als MKV. Was nicht verlustfrei hineinpasst oder auf
// dem iPhone nicht spielt, wird deshalb trotzdem MKV — Datei für Datei, mit
// dem Grund im Protokoll (Nutzerentscheidung 12.10.2026: lieber MKV, als den
// Ton umzuwandeln oder Spuren wegzulassen). So bleibt der Ton immer 1:1
// (seit 0.14.0).

import (
	"fmt"
	"strings"
)

// Werte des INI-Schlüssels container.
const (
	ContainerMKV = "mkv"
	ContainerMP4 = "mp4"
)

// containerUmschalten ist der Wunsch des Desktop-Symbols: "das andere".
const containerUmschalten = "umschalten"

const (
	endungMKV = ".mkv"
	endungMP4 = ".mp4"
)

// ergebnisEndungen: Unter diesen Endungen legt CloudForge Ergebnisse ab.
// Erkannt werden immer beide, egal was gerade eingestellt ist — sonst würde
// nach dem Umschalten ein früheres Ergebnis ein zweites Mal umgewandelt.
var ergebnisEndungen = []string{endungMKV, endungMP4}

// mp4Ton: Tonformate, die in MP4 auf iPhone und Mac ohne Zusatz-App spielen.
// Opus und FLAC kann ffmpeg zwar in MP4 schreiben, ob Apple sie dort
// abspielt, ist aber nicht geprüft — sie bleiben vorsichtshalber in MKV.
var mp4Ton = map[string]bool{
	"aac": true, "mp3": true, "ac3": true, "eac3": true, "alac": true,
}

// mp4Untertitel: Textuntertitel, die ffmpeg ins MP4-Textformat (mov_text)
// überträgt. Text und Zeiten bleiben, Gestaltung wie Schrift und Farbe fällt
// weg. Bild-Untertitel (Blu-ray, DVD) lassen sich nicht in Text verwandeln
// (getestet 12.10.2026: ffmpeg bricht ab).
var mp4Untertitel = map[string]bool{
	"mov_text": true, "subrip": true, "srt": true, "ass": true, "ssa": true,
	"webvtt": true, "text": true,
}

// mp4Bild: Bildformate, die beim Umpacken (Bild bleibt, wie es ist) in MP4
// auf dem iPhone spielen. Beim Umwandeln ist das Bild ohnehin AV1.
var mp4Bild = map[string]bool{"h264": true, "hevc": true, "av1": true}

// hevcAppleKennung: Ohne diese Kennung schreibt ffmpeg H.265 als "hev1" in
// die MP4, und das iPhone spielt es nicht ab (geprüft 12.10.2026: mit
// -tag:v hvc1 bleibt das Bild Bit für Bit gleich).
const hevcAppleKennung = "hvc1"

// ergebnisEndung entscheidet, in welchem Behälter DIESE Datei abgelegt wird.
// mkvGrund nennt, warum sie trotz container=mp4 MKV wird; sonst ist er leer.
func ergebnisEndung(info VideoInfo, e Einstellungen, umpacken bool) (endung, mkvGrund string) {
	if e.Container != ContainerMP4 {
		return endungMKV, ""
	}
	if grund := mp4Hindernis(info, umpacken); grund != "" {
		return endungMKV, grund
	}
	return endungMP4, ""
}

// mp4Hindernis nennt das Erste, was nicht in eine MP4 passt, oder "".
func mp4Hindernis(info VideoInfo, umpacken bool) string {
	if umpacken && !mp4Bild[info.VideoCodec] {
		return fmt.Sprintf("Bildformat %s", info.VideoCodec)
	}
	for _, spur := range info.Tonspuren {
		if !mp4Ton[spur.Codec] {
			return fmt.Sprintf("Tonspur im Format %s", spur.Codec)
		}
	}
	for _, codec := range info.UntertitelCodecs {
		if !mp4Untertitel[codec] {
			return fmt.Sprintf("Untertitel im Format %s", codec)
		}
	}
	// MP4 kennt keine Anhänge: ffmpeg bricht dann ab (getestet 12.10.2026).
	if info.Anhaenge > 0 {
		return "Anhaenge in der Datei (etwa Schriften fuer Untertitel)"
	}
	return ""
}

// istMP4 sagt, ob ein Pfad auf eine MP4 zeigt. Die Endung des Ziels ist die
// einzige Quelle dafür: ffmpeg wählt den Behälter ebenfalls nach ihr.
func istMP4(pfad string) bool {
	return strings.HasSuffix(strings.ToLower(pfad), endungMP4)
}

// containerName ist die Endung für Meldungen: ".mp4" -> "MP4".
func containerName(endung string) string {
	return strings.ToUpper(strings.TrimPrefix(endung, "."))
}

// containerZiel ermittelt aus dem aktuellen Wert und dem Wunsch ("mkv",
// "mp4" oder "umschalten") den neuen Wert.
func containerZiel(aktuell, wunsch string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(wunsch)) {
	case ContainerMKV:
		return ContainerMKV, nil
	case ContainerMP4:
		return ContainerMP4, nil
	case containerUmschalten:
		if aktuell == ContainerMP4 {
			return ContainerMKV, nil
		}
		return ContainerMP4, nil
	}
	return "", Hinweis(fmt.Sprintf("-container kennt nur %q, %q oder %q, nicht %q",
		ContainerMKV, ContainerMP4, containerUmschalten, wunsch))
}

// ContainerSchalten legt den Behälter in der INI fest und sagt im Fenster,
// was ab jetzt gilt (Desktop-Symbol "CloudForge: MKV/MP4 umschalten").
func ContainerSchalten(iniPfad string, e Einstellungen, wunsch string) error {
	neu, err := containerZiel(e.Container, wunsch)
	if err != nil {
		return err
	}
	if err := IniWertSetzen(iniPfad, "container", neu); err != nil {
		return err
	}
	fmt.Println()
	fmt.Println(containerSchaltText(e.Container, neu))
	return nil
}

// containerSchaltText erklärt die neue Einstellung in Worten.
func containerSchaltText(alt, neu string) string {
	var text strings.Builder
	if alt == neu {
		fmt.Fprintf(&text, "Ergebnisse werden schon als %s gespeichert - nichts geaendert.\n", strings.ToUpper(neu))
	} else {
		fmt.Fprintf(&text, "Umgeschaltet: Ergebnisse werden ab jetzt als %s gespeichert (vorher %s).\n",
			strings.ToUpper(neu), strings.ToUpper(alt))
	}
	text.WriteString("Das Bild bleibt AV1, Ton und Untertitel bleiben unveraendert.\n")
	if neu == ContainerMP4 {
		text.WriteString("Ausnahme: Eine Datei mit Ton, Untertiteln oder Anhaengen, die MP4 nicht\n" +
			"verlustfrei aufnehmen kann (etwa DTS-Ton oder Bild-Untertitel), wird\n" +
			"trotzdem MKV - der Grund steht dann im Protokoll.\n")
	}
	text.WriteString("Gilt ab dem naechsten Start. Ein Lauf, der gerade arbeitet, bleibt bei\n" +
		"seiner Einstellung. Schon fertige Ergebnisse bleiben, wie sie sind.")
	return text.String()
}
