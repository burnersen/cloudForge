// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

//go:build !linux

package main

import "os"

// Nur für das Bauen und Testen auf dem Entwicklungsrechner: CloudForge läuft
// ausschliesslich unter Linux. Ohne Grösse gilt 80 x 24 (siehe fensterMasse).
func terminalGroesse() (breite, hoehe int, ok bool) {
	return 0, 0, false
}

// Ein nil-Kanal meldet nie etwas — die Übersicht zeichnet dann nur im Takt.
func groessenAenderungen() (meldungen <-chan os.Signal, aufhoeren func()) {
	return nil, func() {}
}
