// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

//go:build linux

package main

import (
	"os"
	"os/signal"
	"syscall"
	"unsafe"
)

// winsize ist der Aufbau, den der Kernel bei TIOCGWINSZ füllt.
type winsize struct {
	zeilen, spalten, pixelX, pixelY uint16
}

// terminalGroesse fragt den Kernel nach der Grösse des Terminals. ok ist
// false, wenn die Ausgabe gar kein Terminal ist.
func terminalGroesse() (breite, hoehe int, ok bool) {
	var masse winsize
	_, _, fehler := syscall.Syscall(syscall.SYS_IOCTL, os.Stdout.Fd(),
		uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&masse)))
	if fehler != 0 || masse.spalten == 0 || masse.zeilen == 0 {
		return 0, 0, false
	}
	return int(masse.spalten), int(masse.zeilen), true
}

// groessenAenderungen meldet jedes Grösser- oder Kleinerziehen des Fensters
// (Signal SIGWINCH). aufhoeren meldet sich wieder ab.
func groessenAenderungen() (meldungen <-chan os.Signal, aufhoeren func()) {
	kanal := make(chan os.Signal, 1)
	signal.Notify(kanal, syscall.SIGWINCH)
	return kanal, func() { signal.Stop(kanal) }
}
