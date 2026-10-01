// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

// Welche Fassungen von ffmpeg, SVT-AV1 und libvmaf gerade rechnen — steht seit
// 0.19.0 (Nutzerwunsch) zu Beginn jedes Laufs im Protokoll. Ein anderes
// SVT-AV1 kodiert anders, ein anderes libvmaf misst anders; so lässt sich
// später jede Veränderung bei Grösse oder Qualität einer Fassung zuordnen.
// ffmpeg selbst nennt die beiden Bibliotheken nicht, deshalb je ein
// winziger Probelauf (zusammen gut 0,1 s, gemessen 01.10.2026).

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	versionUnbekannt   = "unbekannt"
	versionZeitgrenze  = 30 * time.Second // hängt ein Probelauf, darf der Lauf nicht daran scheitern
	svtVersionMarke    = "SVT-AV1 Encoder Lib "
	ffmpegVersionMarke = "ffmpeg version "
)

// WerkzeugVersionen liefert z. B. "ffmpeg n8.1.3-20260925, SVT-AV1
// v4.2.0-151-g5ed9ffbcb, libvmaf 86da14d". Was sich nicht ermitteln lässt,
// heisst "unbekannt" — das Umwandeln geht trotzdem.
func WerkzeugVersionen(e Einstellungen) string {
	ctx, aufhoeren := context.WithTimeout(context.Background(), versionZeitgrenze)
	defer aufhoeren()

	return "ffmpeg " + ffmpegVersion(ctx, e.FFmpegPfad) +
		", SVT-AV1 " + svtVersion(ctx, e.FFmpegPfad) +
		", libvmaf " + libvmafVersion(ctx, e.FFmpegPfad)
}

func ffmpegVersion(ctx context.Context, ffmpeg string) string {
	ausgabe, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-version").Output()
	if err != nil {
		return versionUnbekannt
	}
	return versionNachMarke(string(ausgabe), ffmpegVersionMarke)
}

// svtVersion kodiert ein einziges schwarzes Bild — SVT-AV1 nennt seine
// Fassung dabei selbst. SVT_LOG=3 sorgt dafür, dass es das auch tut, wenn
// jemand die Meldungen von SVT abgestellt hat.
func svtVersion(ctx context.Context, ffmpeg string) string {
	befehl := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-hide_banner",
		"-f", "lavfi", "-i", "color=c=black:s=64x64:r=25:d=0.04",
		"-frames:v", "1", "-c:v", videoCodec, "-preset", "12", "-f", "null", "-")
	befehl.Env = append(os.Environ(), "SVT_LOG=3")
	ausgabe, err := befehl.CombinedOutput()
	if err != nil {
		return versionUnbekannt
	}
	return versionNachMarke(string(ausgabe), svtVersionMarke)
}

// libvmafVersion misst ein graues Bild gegen sich selbst — das Protokoll von
// libvmaf nennt die Fassung. Es entsteht in einem eigenen Ordner, aus demselben
// Grund wie in VMAFMessen: Ein Pfad im Filter könnte ihn zerbrechen.
func libvmafVersion(ctx context.Context, ffmpeg string) string {
	ordner, err := os.MkdirTemp("", "cloudforge-version-")
	if err != nil {
		return versionUnbekannt
	}
	defer os.RemoveAll(ordner)

	const bild = "color=c=gray:s=64x64:r=25:d=0.04"
	befehl := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", bild, "-f", "lavfi", "-i", bild,
		"-lavfi", "[0:v][1:v]libvmaf=log_fmt=json:log_path="+vmafProtokollName,
		"-f", "null", "-")
	befehl.Dir = ordner
	if err := befehl.Run(); err != nil {
		return versionUnbekannt
	}
	inhalt, err := os.ReadFile(filepath.Join(ordner, vmafProtokollName))
	if err != nil {
		return versionUnbekannt
	}
	return libvmafVersionAusProtokoll(inhalt)
}

// versionNachMarke liefert das Wort hinter marke, etwa "n8.1.3-20260925"
// aus "ffmpeg version n8.1.3-20260925 Copyright ...".
func versionNachMarke(ausgabe, marke string) string {
	_, rest, gefunden := strings.Cut(ausgabe, marke)
	if !gefunden {
		return versionUnbekannt
	}
	if felder := strings.Fields(rest); len(felder) > 0 {
		return felder[0]
	}
	return versionUnbekannt
}

func libvmafVersionAusProtokoll(inhalt []byte) string {
	var protokoll struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(inhalt, &protokoll); err != nil || protokoll.Version == "" {
		return versionUnbekannt
	}
	return protokoll.Version
}
