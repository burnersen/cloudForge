// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

//go:build linux

package main

import (
	"fmt"
	"syscall"
)

// FreierPlatzBytes liefert, wie viel auf dem Dateisystem des Pfades noch frei
// ist. Es zählt der für normale Benutzer verfügbare Platz (Bavail), nicht der
// gesamte freie — ein Teil ist für den Systemverwalter reserviert.
func FreierPlatzBytes(pfad string) (int64, error) {
	var zustand syscall.Statfs_t
	if err := syscall.Statfs(pfad, &zustand); err != nil {
		return 0, fmt.Errorf("freien Platz von %s nicht ermittelbar: %w", pfad, err)
	}
	return int64(zustand.Bavail) * int64(zustand.Bsize), nil
}
