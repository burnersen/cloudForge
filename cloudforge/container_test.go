// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// MP4 nur, wenn alles verlustfrei hineinpasst und auf dem iPhone spielt —
// sonst MKV mit Grund (Nutzerentscheidung 12.10.2026). Mit container=mkv gibt
// es nie einen Grund: dann ist MKV ja gewollt.
func TestErgebnisEndung(t *testing.T) {
	aac := []Tonspur{{Codec: "aac"}}
	faelle := []struct {
		name      string
		container string
		info      VideoInfo
		umpacken  bool
		endung    string
		mitGrund  bool
	}{
		{"MKV eingestellt", ContainerMKV, VideoInfo{VideoCodec: "h264", Tonspuren: aac}, false, endungMKV, false},
		{"MKV eingestellt, DTS", ContainerMKV, VideoInfo{VideoCodec: "h264", Tonspuren: []Tonspur{{Codec: "dts"}}}, false, endungMKV, false},
		{"passt", ContainerMP4, VideoInfo{VideoCodec: "h264",
			Tonspuren:        []Tonspur{{Codec: "aac"}, {Codec: "ac3"}, {Codec: "eac3"}, {Codec: "mp3"}, {Codec: "alac"}},
			UntertitelCodecs: []string{"subrip", "ass", "mov_text", "webvtt"}}, false, endungMP4, false},
		{"ohne Ton", ContainerMP4, VideoInfo{VideoCodec: "h264"}, false, endungMP4, false},
		{"DTS-Ton", ContainerMP4, VideoInfo{VideoCodec: "h264", Tonspuren: []Tonspur{{Codec: "aac"}, {Codec: "dts"}}}, false, endungMKV, true},
		{"Opus-Ton", ContainerMP4, VideoInfo{VideoCodec: "h264", Tonspuren: []Tonspur{{Codec: "opus"}}}, false, endungMKV, true},
		{"Bild-Untertitel", ContainerMP4, VideoInfo{VideoCodec: "h264", Tonspuren: aac,
			UntertitelCodecs: []string{"subrip", "hdmv_pgs_subtitle"}}, false, endungMKV, true},
		{"Schriften", ContainerMP4, VideoInfo{VideoCodec: "h264", Tonspuren: aac, Anhaenge: 2}, false, endungMKV, true},
		// Beim Umwandeln wird das Bild ohnehin AV1, nur beim Umpacken zählt es.
		{"Umwandeln aus MPEG-2", ContainerMP4, VideoInfo{VideoCodec: "mpeg2video", Tonspuren: aac}, false, endungMP4, false},
		{"Umpacken MPEG-2", ContainerMP4, VideoInfo{VideoCodec: "mpeg2video", Tonspuren: aac}, true, endungMKV, true},
		{"Umpacken H.265", ContainerMP4, VideoInfo{VideoCodec: "hevc", Tonspuren: aac}, true, endungMP4, false},
		{"Umpacken AV1", ContainerMP4, VideoInfo{VideoCodec: "av1", Tonspuren: aac}, true, endungMP4, false},
	}
	for _, f := range faelle {
		e := standardWerte()
		e.Container = f.container
		endung, grund := ergebnisEndung(f.info, e, f.umpacken)
		if endung != f.endung || (grund != "") != f.mitGrund {
			t.Errorf("%s: bekommen %q mit Grund %q, erwartet %q (Grund: %v)", f.name, endung, grund, f.endung, f.mitGrund)
		}
	}
}

func TestContainerZiel(t *testing.T) {
	faelle := []struct{ aktuell, wunsch, erwartet string }{
		{ContainerMKV, "umschalten", ContainerMP4},
		{ContainerMP4, "umschalten", ContainerMKV},
		{ContainerMKV, "MP4", ContainerMP4},
		{ContainerMP4, " mkv ", ContainerMKV},
		{ContainerMP4, "mp4", ContainerMP4},
	}
	for _, f := range faelle {
		if neu, err := containerZiel(f.aktuell, f.wunsch); err != nil || neu != f.erwartet {
			t.Errorf("%s + %q: bekommen %q (%v), erwartet %q", f.aktuell, f.wunsch, neu, err, f.erwartet)
		}
	}
	if _, err := containerZiel(ContainerMKV, "avi"); err == nil {
		t.Error("ein unbekannter Wunsch muss abgelehnt werden")
	}
}

// Das Symbol schaltet hin und her, und die INI behält es über den nächsten Start.
func TestContainerSchaltenSchreibtDieINI(t *testing.T) {
	pfad := filepath.Join(t.TempDir(), "cloudforge.ini")
	e, _, err := EinstellungenLaden(pfad) // legt die Werksform an
	if err != nil {
		t.Fatal(err)
	}
	if e.Container != ContainerMKV {
		t.Fatalf("ab Werk %q, erwartet mkv", e.Container)
	}
	for _, erwartet := range []string{ContainerMP4, ContainerMKV, ContainerMP4} {
		if err := ContainerSchalten(pfad, e, containerUmschalten); err != nil {
			t.Fatal(err)
		}
		if e, _, err = EinstellungenLaden(pfad); err != nil {
			t.Fatal(err)
		}
		if e.Container != erwartet {
			t.Errorf("nach dem Umschalten %q, erwartet %q", e.Container, erwartet)
		}
	}
}

func TestContainerLesen(t *testing.T) {
	e := standardWerte()
	if _, err := iniWerteLesen("container=MP4\n", &e); err != nil || e.Container != ContainerMP4 {
		t.Errorf("MP4: %q (%v)", e.Container, err)
	}
	if _, err := iniWerteLesen("container=avi\n", &e); err == nil {
		t.Error("ein Tippfehler muss auffallen, statt still MKV zu bedeuten")
	}
}

// In MP4: Textuntertitel ins MP4-Textformat, keine Anhänge, Inhaltsverzeichnis
// an den Anfang, H.265 mit Apple-Kennung. In MKV bleibt alles wie bisher.
func TestEncodeArgumenteMP4(t *testing.T) {
	e := standardWerte()
	umwandeln := EncodeArgumente(EncodeAuftrag{Quelle: "q.mkv", Ziel: "z.mp4", CRF: 30,
		UntertitelCodecs: []string{"subrip", "mov_text"}}, e)
	befehl := strings.Join(umwandeln, " ")
	if wertNach(umwandeln, "-c:s:0") != untertitelMP4Text || wertNach(umwandeln, "-c:s:1") != "copy" {
		t.Errorf("Untertitel muessen ins MP4-Textformat: %s", befehl)
	}
	if wertNach(umwandeln, "-movflags") != "+faststart" {
		t.Errorf("faststart fehlt: %s", befehl)
	}
	if slices.Contains(umwandeln, "0:t?") || slices.Contains(umwandeln, "-c:t") {
		t.Errorf("MP4 kann keine Anhaenge aufnehmen: %s", befehl)
	}
	if wertNach(umwandeln, "-c:a") != "copy" || slices.Contains(umwandeln, "-tag:v:0") {
		t.Errorf("Ton 1:1, beim Umwandeln keine Kennung: %s", befehl)
	}

	hevc := EncodeArgumente(EncodeAuftrag{Quelle: "q.mkv", Ziel: "z.mp4", Umpacken: true, QuellBildCodec: "hevc"}, e)
	if wertNach(hevc, "-tag:v:0") != hevcAppleKennung {
		t.Errorf("H.265 braucht in MP4 die Apple-Kennung: %v", hevc)
	}
	h264 := EncodeArgumente(EncodeAuftrag{Quelle: "q.mkv", Ziel: "z.mp4", Umpacken: true, QuellBildCodec: "h264"}, e)
	if slices.Contains(h264, "-tag:v:0") {
		t.Errorf("H.264 braucht keine Kennung: %v", h264)
	}

	mkv := EncodeArgumente(EncodeAuftrag{Quelle: "q.mp4", Ziel: "z.mkv", Umpacken: true, QuellBildCodec: "hevc"}, e)
	if slices.Contains(mkv, "-movflags") || slices.Contains(mkv, "-tag:v:0") || !slices.Contains(mkv, "0:t?") {
		t.Errorf("MKV muss bleiben wie bisher: %v", mkv)
	}
}

func TestVorhandenesErgebnisFindetMP4(t *testing.T) {
	wurzel := t.TempDir()
	e := standardWerte()
	ausgabe := filepath.Join(wurzel, e.AusgabeOrdnerName)
	if err := os.MkdirAll(ausgabe, 0o755); err != nil {
		t.Fatal(err)
	}
	ergebnis := filepath.Join(ausgabe, "film.av1.mp4")
	if err := os.WriteFile(ergebnis, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Auch mit container=mkv: ein MP4-Ergebnis von früher zählt.
	if got := VorhandenesErgebnis(filepath.Join(wurzel, "film.mkv"), e); got != ergebnis {
		t.Errorf("bekommen %q, erwartet %q", got, ergebnis)
	}
	if got := ZielPfadFuer(filepath.Join(wurzel, "film.mkv"), endungMP4, e); got != ergebnis {
		t.Errorf("Zielpfad %q, erwartet %q", got, ergebnis)
	}
}

func TestAusProbeZaehltAnhaenge(t *testing.T) {
	roh := probeAusgabe{
		Streams: []probeStream{
			{CodecType: "video", CodecName: "h264", Width: 1920, Height: 1080, AvgFrameRate: "25/1"},
			{CodecType: "subtitle", CodecName: "ass"},
			{CodecType: "attachment", CodecName: "ttf"},
			{CodecType: "attachment"},
		},
		Format: probeFormat{Duration: "60", Size: "1000000"},
	}
	info, err := ausProbe("film.mkv", roh)
	if err != nil {
		t.Fatal(err)
	}
	if info.Anhaenge != 2 {
		t.Errorf("%d Anhaenge gezaehlt, erwartet 2", info.Anhaenge)
	}
}

func TestStarterListeHatUmschalter(t *testing.T) {
	liste := starterListe("/home/x/cloudforge/cloudforge-fenster.sh")
	if len(liste) != 4 {
		t.Fatalf("%d Starter, erwartet 4", len(liste))
	}
	umschalter := liste[3]
	if umschalter.name != "cloudforge-container.desktop" ||
		!strings.Contains(umschalter.inhalt, "cloudforge-fenster.sh -container umschalten\n") ||
		!strings.Contains(umschalter.inhalt, "Terminal=true") {
		t.Errorf("Umschalt-Symbol falsch: %s\n%s", umschalter.name, umschalter.inhalt)
	}
}

func TestEinstellungenTextNenntContainer(t *testing.T) {
	e := standardWerte()
	if text := einstellungenText(e); !strings.HasSuffix(text, "Ergebnis als MKV") {
		t.Errorf("MKV fehlt: %s", text)
	}
	e.Container = ContainerMP4
	if text := einstellungenText(e); !strings.HasSuffix(text, "Ergebnis als MP4") {
		t.Errorf("MP4 fehlt: %s", text)
	}
}

// Die MP4-Kette mit echtem ffmpeg (nur auf dem Server, sonst SKIP):
// Umwandeln mit drei Tonformaten, Text-Untertiteln und Kapiteln; Umpacken von
// H.265 mit Apple-Kennung und unverändertem Bild; Anhänge führen zu MKV.
func TestMP4MitEchtemFFmpeg(t *testing.T) {
	ffmpeg := os.Getenv("CLOUDFORGE_TEST_FFMPEG")
	ffprobe := os.Getenv("CLOUDFORGE_TEST_FFPROBE")
	if ffmpeg == "" || ffprobe == "" {
		t.Skip("CLOUDFORGE_TEST_FFMPEG und CLOUDFORGE_TEST_FFPROBE nicht gesetzt")
	}
	ctx := context.Background()
	ordner := t.TempDir()
	e := standardWerte()
	e.FFmpegPfad, e.FFprobePfad = ffmpeg, ffprobe
	e.Container = ContainerMP4

	ffmpegLassen := func(was string, args ...string) {
		befehl := exec.Command(ffmpeg, append([]string{"-nostdin", "-loglevel", "error", "-y"}, args...)...)
		if ausgabe, err := befehl.CombinedOutput(); err != nil {
			t.Fatalf("%s nicht erzeugbar: %v (%s)", was, err, ausgabe)
		}
	}
	abfragen := func(pfad string, args ...string) string {
		aus, err := exec.Command(ffprobe, append(append([]string{"-v", "error"}, args...), "-of", "csv=p=0", pfad)...).Output()
		if err != nil {
			t.Fatalf("ffprobe %s: %v", pfad, err)
		}
		return strings.TrimSpace(string(aus))
	}
	hilfsdatei := func(name, inhalt string) string {
		pfad := filepath.Join(ordner, name)
		if err := os.WriteFile(pfad, []byte(inhalt), 0o644); err != nil {
			t.Fatal(err)
		}
		return pfad
	}
	kopfdaten := func(pfad string) VideoInfo {
		info, err := KopfdatenLesen(ffprobe, pfad)
		if err != nil {
			t.Fatalf("Kopfdaten %s: %v", pfad, err)
		}
		return info
	}
	bildPruefsumme := func(pfad string) string {
		aus, err := exec.Command(ffmpeg, "-nostdin", "-loglevel", "error", "-i", pfad,
			"-map", "0:v:0", "-c", "copy", "-f", "md5", "-").Output()
		if err != nil {
			t.Fatalf("Pruefsumme von %s: %v", pfad, err)
		}
		return string(aus)
	}

	// 1. Umwandeln: AAC, AC-3, E-AC-3, SRT, ASS und zwei Kapitel.
	srt := hilfsdatei("text.srt", "1\n00:00:01,000 --> 00:00:02,000\nHallo\n")
	ass := hilfsdatei("text.ass", "[Script Info]\nScriptType: v4.00+\n\n[V4+ Styles]\n"+
		"Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n"+
		"Style: Default,Arial,20,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,1,0,2,10,10,10,1\n\n"+
		"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n"+
		"Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,{\\b1}Hallo{\\b0} Welt\n")
	kapitel := hilfsdatei("kapitel.txt", ";FFMETADATA1\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=2000\ntitle=Eins\n"+
		"[CHAPTER]\nTIMEBASE=1/1000\nSTART=2000\nEND=4000\ntitle=Zwei\n")
	quelle := filepath.Join(ordner, "quelle.mkv")
	ffmpegLassen("Quelle", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25:duration=4",
		"-f", "lavfi", "-i", "sine=duration=4", "-i", srt, "-i", ass, "-i", kapitel,
		"-map", "0", "-map", "1", "-map", "1", "-map", "1", "-map", "2", "-map", "3", "-map_chapters", "4",
		"-c:v", "libx264", "-c:a:0", "aac", "-c:a:1", "ac3", "-c:a:2", "eac3", "-c:s", "copy", quelle)
	info := kopfdaten(quelle)
	endung, grund := ergebnisEndung(info, e, false)
	if endung != endungMP4 {
		t.Fatalf("Quelle muesste in MP4 passen, Grund: %s", grund)
	}
	ergebnis := filepath.Join(ordner, "ergebnis"+endung)
	auftrag := EncodeAuftrag{Quelle: quelle, Ziel: ergebnis, CRF: 50, UntertitelCodecs: info.UntertitelCodecs}
	if err := Kodieren(ctx, auftrag, e, info.DauerSek, nil); err != nil {
		t.Fatalf("Umwandeln nach MP4: %v", err)
	}
	if p := ErgebnisPruefen(ctx, info, ergebnis, e, nil); !p.Bestanden {
		t.Errorf("Pruefkette fuer MP4 gescheitert: %s", p.ErsterFehler())
	}
	if kennung := abfragen(ergebnis, "-select_streams", "v:0", "-show_entries", "stream=codec_tag_string"); kennung != "av01" {
		t.Errorf("AV1 in MP4 mit Kennung %q, erwartet av01", kennung)
	}
	if ton := abfragen(ergebnis, "-select_streams", "a", "-show_entries", "stream=codec_name"); strings.Fields(ton)[0] != "aac" || len(strings.Fields(ton)) != 3 {
		t.Errorf("Tonspuren %q, erwartet aac, ac3, eac3 unveraendert", ton)
	}
	if neu := kopfdaten(ergebnis); !slices.Equal(neu.UntertitelCodecs, []string{untertitelMP4Text, untertitelMP4Text}) {
		t.Errorf("Untertitel %v, erwartet zweimal mov_text", neu.UntertitelCodecs)
	}
	if kap := abfragen(ergebnis, "-show_chapters"); len(strings.Split(kap, "\n")) != 2 {
		t.Errorf("Kapitel fehlen: %q", kap)
	}
	inhalt, err := os.ReadFile(ergebnis)
	if err != nil {
		t.Fatal(err)
	}
	if moov, mdat := bytes.Index(inhalt, []byte("moov")), bytes.Index(inhalt, []byte("mdat")); moov < 0 || moov > mdat {
		t.Errorf("Inhaltsverzeichnis (moov %d) steht nicht vor den Daten (mdat %d) - faststart fehlt", moov, mdat)
	}

	// 2. Umpacken von H.265: Apple-Kennung, Bild Bit für Bit gleich.
	hevc := filepath.Join(ordner, "hevc.mkv")
	ffmpegLassen("H.265-Quelle", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25:duration=3",
		"-f", "lavfi", "-i", "sine=duration=3", "-c:v", "libx265", "-x265-params", "log-level=error",
		"-c:a", "aac", "-shortest", hevc)
	hevcInfo := kopfdaten(hevc)
	if endung, grund := ergebnisEndung(hevcInfo, e, true); endung != endungMP4 {
		t.Fatalf("H.265 muesste sich nach MP4 umpacken lassen, Grund: %s", grund)
	}
	umgepackt := filepath.Join(ordner, "umgepackt.mp4")
	umpacken := EncodeAuftrag{Quelle: hevc, Ziel: umgepackt, Umpacken: true,
		UntertitelCodecs: hevcInfo.UntertitelCodecs, QuellBildCodec: hevcInfo.VideoCodec}
	if err := Kodieren(ctx, umpacken, e, hevcInfo.DauerSek, nil); err != nil {
		t.Fatalf("Umpacken nach MP4: %v", err)
	}
	if p := UmpackErgebnisPruefen(ctx, hevcInfo, umgepackt, e, nil); !p.Bestanden {
		t.Errorf("Pruefkette fuers Umpacken gescheitert: %s", p.ErsterFehler())
	}
	if kennung := abfragen(umgepackt, "-select_streams", "v:0", "-show_entries", "stream=codec_tag_string"); kennung != hevcAppleKennung {
		t.Errorf("H.265 mit Kennung %q, erwartet %s", kennung, hevcAppleKennung)
	}
	if a, b := bildPruefsumme(hevc), bildPruefsumme(umgepackt); a != b {
		t.Errorf("das Bild hat sich beim Umpacken veraendert: %s gegen %s", a, b)
	}

	// 3. Ein Anhang (in MKV meist eine Schrift) passt nicht in MP4.
	mitAnhang := filepath.Join(ordner, "mit-anhang.mkv")
	ffmpegLassen("Quelle mit Anhang", "-i", quelle, "-attach", srt, "-metadata:s:t", "mimetype=text/plain",
		"-map", "0", "-c", "copy", mitAnhang)
	if endung, grund := ergebnisEndung(kopfdaten(mitAnhang), e, false); endung != endungMKV || grund == "" {
		t.Errorf("mit Anhang: %q (Grund %q), erwartet MKV mit Grund", endung, grund)
	}
}
