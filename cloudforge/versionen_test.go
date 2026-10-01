// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"os"
	"strings"
	"testing"
)

// Die Ausgaben stammen vom netcup-Server (01.10.2026), gekürzt.
func TestVersionNachMarke(t *testing.T) {
	faelle := []struct {
		name, ausgabe, marke, erwartet string
	}{
		{"ffmpeg", "ffmpeg version n8.1.3-20260925 Copyright (c) 2000-2026 the FFmpeg developers\nbuilt with gcc",
			ffmpegVersionMarke, "n8.1.3-20260925"},
		{"SVT-AV1", "Svt[info]: -------------------------------------------\n" +
			"Svt[info]: SVT [version]:\tSVT-AV1 Encoder Lib v4.2.0-151-g5ed9ffbcb\nSvt[info]: SVT [build]  :",
			svtVersionMarke, "v4.2.0-151-g5ed9ffbcb"},
		{"nichts gefunden", "Error opening input", svtVersionMarke, versionUnbekannt},
		{"Marke am Ende", "ffmpeg version ", ffmpegVersionMarke, versionUnbekannt},
	}
	for _, f := range faelle {
		if bekommen := versionNachMarke(f.ausgabe, f.marke); bekommen != f.erwartet {
			t.Errorf("%s: %q erwartet, bekommen %q", f.name, f.erwartet, bekommen)
		}
	}
}

func TestLibvmafVersionAusProtokoll(t *testing.T) {
	if v := libvmafVersionAusProtokoll([]byte(vmafProtokollBeispiel)); v != "86da14d" {
		t.Errorf("86da14d erwartet, bekommen %q", v)
	}
	for _, kaputt := range []string{"", "kein JSON", `{"frames": []}`} {
		if v := libvmafVersionAusProtokoll([]byte(kaputt)); v != versionUnbekannt {
			t.Errorf("%q: %q erwartet, bekommen %q", kaputt, versionUnbekannt, v)
		}
	}
}

// Mit echtem ffmpeg: alle drei Fassungen müssen sich ermitteln lassen.
func TestWerkzeugVersionenMitEchtemFFmpeg(t *testing.T) {
	ffmpeg := os.Getenv("CLOUDFORGE_TEST_FFMPEG")
	if ffmpeg == "" {
		t.Skip("CLOUDFORGE_TEST_FFMPEG nicht gesetzt")
	}
	e := standardWerte()
	e.FFmpegPfad = ffmpeg
	text := WerkzeugVersionen(e)
	if strings.Contains(text, versionUnbekannt) {
		t.Errorf("eine Fassung blieb unbekannt: %s", text)
	}
	t.Log(text)
}
