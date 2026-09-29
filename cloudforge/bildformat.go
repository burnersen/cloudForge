// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

// Wie das Bild des Ergebnisses aussieht: seine Grösse (maxAufloesung, seit
// 0.17.0) und seine Farbangaben — beides wie in NVENCForge.

import (
	"fmt"
	"math"
	"strings"
)

// erlaubteMaxAufloesungen: kurze Kante des Ergebnisses, wie maxResolution in
// NVENCForge. 0 heisst: die Grösse der Quelle bleibt.
var erlaubteMaxAufloesungen = map[int]bool{0: true, 720: true, 1080: true, 1440: true, 2160: true}

// ergebnisMasse sagt, wie gross das Ergebnis wird. Ist maxAufloesung gesetzt,
// muss es in ein 16:9-Feld passen — bei 1080 höchstens 1920 × 1080, hochkant
// 1080 × 1920 —, das Seitenverhältnis bleibt, vergrössert wird nie. Genau
// wie NVENCForge; nur ohne die Schärfung danach (Nutzerwunsch 29.09.2026:
// sie kostet viel Zeit, und die Auflösung allein genügt ihm).
//
// Beide Kanten werden auf gerade Zahlen abgerundet (das Farbformat 4:2:0
// braucht sie) und so nie grösser als das Feld.
func ergebnisMasse(breite, hoehe, maxAufloesung int) (zielBreite, zielHoehe int, verkleinert bool) {
	if maxAufloesung <= 0 || breite <= 0 || hoehe <= 0 {
		return breite, hoehe, false
	}
	feldKurz := float64(maxAufloesung)
	feldLang := float64(maxAufloesung * 16 / 9)
	feldBreite, feldHoehe := feldLang, feldKurz
	if hoehe > breite {
		feldBreite, feldHoehe = feldKurz, feldLang
	}

	faktor := math.Min(feldBreite/float64(breite), feldHoehe/float64(hoehe))
	if faktor >= 1 {
		return breite, hoehe, false
	}
	return geradeAbrunden(float64(breite) * faktor), geradeAbrunden(float64(hoehe) * faktor), true
}

// geradeAbrunden rundet auf die nächste gerade Zahl darunter. Der kleine
// Zuschlag fängt Rechenungenauigkeiten ab (1079,9999… soll 1080 bleiben).
func geradeAbrunden(wert float64) int {
	return int(math.Floor(wert/2+1e-9)) * 2
}

// ergebnisMasseFuer ist ergebnisMasse für eine Quelle mit den Einstellungen.
func ergebnisMasseFuer(info VideoInfo, e Einstellungen) (breite, hoehe int, verkleinert bool) {
	return ergebnisMasse(info.Breite, info.Hoehe, e.MaxAufloesung)
}

// aufloesungText nennt die Auflösung für die Anzeige, beim Verkleinern mit
// Ziel: "2160p → 1080p". Wer nur umpackt, gibt 0 mit — das Bild bleibt dann,
// wie es ist.
func aufloesungText(info VideoInfo, maxAufloesung int) string {
	text := fmt.Sprintf("%dp", info.Hoehe)
	if _, hoehe, verkleinert := ergebnisMasse(info.Breite, info.Hoehe, maxAufloesung); verkleinert {
		text += fmt.Sprintf(" → %dp", hoehe)
	}
	return text
}

// verkleinernFilter ist der ffmpeg-Filter, der das Bild auf die Grösse des
// Ergebnisses bringt — leer, wenn es bleibt, wie es ist. Skaliert wird mit
// dem Standardverfahren von ffmpeg (bicubic), wie in NVENCForge.
func verkleinernFilter(info VideoInfo, e Einstellungen) string {
	breite, hoehe, verkleinert := ergebnisMasseFuer(info, e)
	if !verkleinert {
		return ""
	}
	return fmt.Sprintf("scale=%d:%d", breite, hoehe)
}

// farbArgumente übernimmt die Farbangaben der Quelle — genau das, was drin
// steht, wie buildColorOpts in NVENCForge. So bleibt ein HDR-Film als HDR
// gekennzeichnet (PQ oder HLG, BT.2020), und nichts wird dazuerfunden: Fehlt
// eine Angabe, fehlt sie auch im Ergebnis.
//
// Ausgelassen werden nur Werte, die nichts bedeuten ("unknown", "reserved"),
// und die Übertragungskurven bt470m/bt470bg: Die stehen oft fälschlich in
// alten PAL-Dateien und bringen manche Player dazu, das Bild falsch
// darzustellen (ebenfalls wie NVENCForge).
func farbArgumente(info VideoInfo) []string {
	brauchbar := func(wert string) bool {
		wert = strings.ToLower(strings.TrimSpace(wert))
		return wert != "" && wert != "unknown" && wert != "reserved"
	}
	var args []string
	if brauchbar(info.FarbPrimaer) {
		args = append(args, "-color_primaries", info.FarbPrimaer)
	}
	if kurve := strings.ToLower(strings.TrimSpace(info.FarbKurve)); brauchbar(kurve) &&
		kurve != "bt470m" && kurve != "bt470bg" {
		args = append(args, "-color_trc", info.FarbKurve)
	}
	if brauchbar(info.FarbMatrix) {
		args = append(args, "-colorspace", info.FarbMatrix)
	}
	if brauchbar(info.FarbBereich) {
		args = append(args, "-color_range", info.FarbBereich)
	}
	return args
}
