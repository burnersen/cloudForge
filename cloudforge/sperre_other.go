// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

//go:build !linux

package main

import "context"

// Die Sperre gibt es in dieser Fassung nur, damit sich der Quelltext auf dem
// Entwicklungsrechner übersetzen lässt. Im Betrieb läuft CloudForge
// ausschliesslich unter Linux, und dort greift sperre_linux.go.

func SperreHolen(ctx context.Context, pfad string, beimWarten func()) (func(), error) {
	return func() {}, nil
}

func SperreVersuchen(pfad string) (freigeben func(), frei bool, err error) {
	return func() {}, true, nil
}

func VorfahrtAnmelden(pfad string) (abmelden func()) {
	return func() {}
}

func VorfahrtGewuenscht(pfad string) bool {
	return false
}
