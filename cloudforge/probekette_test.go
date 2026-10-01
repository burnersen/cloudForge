// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Die Auto-CQ-Probekette mit echtem ffmpeg: Ausschnitte als FFVHuff
// zwischenspeichern, mit beiden SVT-Schaltern kodieren, VMAF an jedem dritten
// Bild. Läuft nur, wo ffmpeg angegeben ist (auf dem Server:
// CLOUDFORGE_TEST_FFMPEG), sonst SKIP.
func TestProbeKetteMitEchtemFFmpeg(t *testing.T) {
	ffmpeg := os.Getenv("CLOUDFORGE_TEST_FFMPEG")
	if ffmpeg == "" {
		t.Skip("CLOUDFORGE_TEST_FFMPEG nicht gesetzt")
	}
	ctx := context.Background()
	ordner := t.TempDir()

	// 40 s, damit drei 8-s-Fenster hineinpassen wie bei einem Film.
	video := filepath.Join(ordner, "quelle.mkv")
	erzeugen := exec.Command(ffmpeg, "-nostdin", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25:duration=40",
		"-c:v", "ffv1", video)
	if ausgabe, err := erzeugen.CombinedOutput(); err != nil {
		t.Fatalf("Testvideo nicht erzeugbar: %v (%s)", err, ausgabe)
	}

	e := standardWerte()
	e.FFmpegPfad = ffmpeg
	e.VarianceBoost, e.Tune0 = true, true

	fenster := FensterWaehlen(40, e.MessfensterAnzahl, e.MessfensterSek)
	referenz := filepath.Join(ordner, "messreferenz.mkv")
	if err := ProbeSchneiden(ctx, video, referenz, fenster, "", e); err != nil {
		t.Fatalf("Messausschnitte: %v", err)
	}

	// Gegen sich selbst gemessen muss fast 100 herauskommen — sonst paart die
	// Messung falsche Bilder oder liest den falschen Wert (NVENCForge-Lektion).
	// 320x240 wird dafür seit 0.12.0 auf 1440x1080 vergrössert.
	const breite, hoehe = 320, 240
	selbstWerte, err := VMAFMessen(ctx, referenz, referenz, breite, hoehe, e)
	if err != nil {
		t.Fatalf("VMAF gegen sich selbst: %v", err)
	}
	selbst := selbstWerte.Wert // das 5-%-Perzentil (Werkseinstellung)
	if selbst < 99 {
		t.Errorf("gegen sich selbst gemessen %.2f, erwartet fast 100", selbst)
	}
	if _, err := os.Stat(filepath.Join(ordner, vmafProtokollName)); !os.IsNotExist(err) {
		t.Errorf("das VMAF-Protokoll muss nach der Messung weg sein (%v)", err)
	}

	probe := filepath.Join(ordner, "probe.mkv")
	auftrag := EncodeAuftrag{Quelle: referenz, Ziel: probe, CRF: 40, NurVideo: true}
	if err := Kodieren(ctx, auftrag, e, 0, nil); err != nil {
		t.Fatalf("Kodieren mit Variance Boost und tune 0: %v", err)
	}
	probeWerte, err := VMAFMessen(ctx, probe, referenz, breite, hoehe, e)
	if err != nil {
		t.Fatalf("VMAF der Probe: %v", err)
	}
	wert := probeWerte.Wert
	if wert <= 20 || wert >= selbst {
		t.Errorf("VMAF der Probe %.2f ist unplausibel (gegen sich selbst %.2f)", wert, selbst)
	}
	if probeWerte.Mittel < wert {
		t.Errorf("der Mittelwert %.2f kann nicht unter dem 5-%%-Perzentil %.2f liegen", probeWerte.Mittel, wert)
	}

	// Vergrössert fallen die Kodierfehler stärker auf. In eigener Grösse
	// gemessen (Masse unbekannt = nicht vergrössern) muss der Wert also höher
	// liegen — genau das war bis 0.11.x die Täuschung bei kleinen Videos.
	eigeneWerte, err := VMAFMessen(ctx, probe, referenz, 0, 0, e)
	if err != nil {
		t.Fatalf("VMAF in eigener Grösse: %v", err)
	}
	eigeneGroesse := eigeneWerte.Wert
	if wert >= eigeneGroesse {
		t.Errorf("vergrössert gemessen %.2f, in eigener Grösse %.2f - vergrössert muss strenger sein",
			wert, eigeneGroesse)
	}
}

// SVT-AV1 meldet einen unbekannten Parameter nur als Warnung und rechnet ohne
// ihn weiter — im Lauf (-loglevel error) fiele das nie auf. Jeder Parameter,
// den CloudForge setzen kann, muss deshalb hier angenommen werden. Anlass
// 30.09.2026: ein Vorschlag nannte "chroma-qpoffset", das es nicht gibt.
func TestSvtNimmtAlleParameterAn(t *testing.T) {
	ffmpeg := os.Getenv("CLOUDFORGE_TEST_FFMPEG")
	if ffmpeg == "" {
		t.Skip("CLOUDFORGE_TEST_FFMPEG nicht gesetzt")
	}
	e := standardWerte()
	e.VarianceBoost, e.Tune0 = true, true
	// Vom SVT-Werk (2/5) abweichend, damit die Übernahme sichtbar wird.
	e.VarianceBoostStaerke, e.VarianceOktil = 3, 7
	e.FilmKorn = 8 // seit 0.19.0, nur im finalen Encode

	args := []string{"-hide_banner", "-nostdin",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25:duration=1"}
	args = append(args, videoArgumente(40, e, true)...)
	args = append(args, "-f", "null", "-")
	befehl := exec.Command(ffmpeg, args...)
	befehl.Env = append(os.Environ(), "SVT_LOG=3") // die SVT-Übersicht auch dann, wenn jemand sie abgestellt hat
	ausgabe, err := befehl.CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg: %v (%s)", err, ausgabe)
	}
	if strings.Contains(string(ausgabe), "Error parsing option") {
		t.Errorf("SVT-AV1 kennt einen Parameter nicht:\n%s", ausgabe)
	}

	// SVT-AV1 4.2 nennt die Werte in seiner Übersicht:
	// "AQ mode / Variance Boost strength / octile / curve : 2 / 3 / 7 / 0" und
	// "film grain synth / denoising / level / adaptive blocksize : 1 / 0 / 8 / True".
	// Eine spätere Fassung darf die Zeilen anders schreiben — dann bleibt es
	// bei der Prüfung oben.
	for _, zeile := range strings.Split(string(ausgabe), "\n") {
		if strings.Contains(zeile, "Variance Boost strength / octile") && !strings.Contains(zeile, "/ 3 / 7") {
			t.Errorf("Staerke 3 / Oktil 7 kamen nicht an: %s", zeile)
		}
		if strings.Contains(zeile, "film grain synth / denoising / level") && !strings.Contains(zeile, ": 1 / 0 / 8") {
			t.Errorf("Filmkorn 8 ohne Entrauschen kam nicht an: %s", zeile)
		}
	}
}

// Die Messproben bekommen nie Filmkorn — nur der finale Encode.
func TestFilmKornNurImFinalenEncode(t *testing.T) {
	e := standardWerte()
	e.FilmKorn = 8

	probe := strings.Join(EncodeArgumente(EncodeAuftrag{Quelle: "q.mkv", Ziel: "p.mkv", CRF: 30, NurVideo: true}, e), " ")
	if strings.Contains(probe, "film-grain") {
		t.Errorf("Messprobe mit Filmkorn: %s", probe)
	}
	final := strings.Join(EncodeArgumente(EncodeAuftrag{Quelle: "q.mkv", Ziel: "e.mkv", CRF: 30, MitFilmKorn: true}, e), " ")
	if !strings.Contains(final, ":film-grain=8:film-grain-denoise=0") {
		t.Errorf("finaler Encode ohne Filmkorn: %s", final)
	}

	e.FilmKorn = 0
	if aus := strings.Join(EncodeArgumente(EncodeAuftrag{Quelle: "q.mkv", Ziel: "e.mkv", CRF: 30, MitFilmKorn: true}, e), " "); strings.Contains(aus, "film-grain") {
		t.Errorf("filmKorn=0 heisst aus: %s", aus)
	}
}

// Kosten-Deckel mit echtem ffprobe an einer Quelle mit LANGER GOP
// (Schlüsselbild alle 10 s wie bei vielen Downloads): An den Messstellen muss
// genau so viel gezählt werden wie in der vollen Paketliste. Bis 0.11.1 las
// ffprobe ab dem Schlüsselbild vor der Stelle nur 14 s weit und zählte die
// Quelle zu klein (26.09.2026: 15,8 statt 24,3 MB).
func TestQuelleFensterBytesMitLangerGOP(t *testing.T) {
	ffmpeg := os.Getenv("CLOUDFORGE_TEST_FFMPEG")
	ffprobe := os.Getenv("CLOUDFORGE_TEST_FFPROBE")
	if ffmpeg == "" || ffprobe == "" {
		t.Skip("CLOUDFORGE_TEST_FFMPEG und CLOUDFORGE_TEST_FFPROBE nicht gesetzt")
	}
	ctx := context.Background()

	quelle := filepath.Join(t.TempDir(), "quelle.mp4")
	erzeugen := exec.Command(ffmpeg, "-nostdin", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25:duration=60",
		"-c:v", "mpeg4", "-q:v", "5", "-g", "250", quelle)
	if ausgabe, err := erzeugen.CombinedOutput(); err != nil {
		t.Fatalf("Testvideo nicht erzeugbar: %v (%s)", err, ausgabe)
	}

	fenster := FensterWaehlen(60, 3, 8) // wie ab Werk: 3 Fenster zu 8 s
	gezaehlt, err := QuelleFensterBytes(ctx, ffprobe, quelle, fenster)
	if err != nil {
		t.Fatalf("QuelleFensterBytes: %v", err)
	}

	alle, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "format=start_time:packet=pts_time,dts_time,size",
		"-of", "csv=p=1", quelle).Output()
	if err != nil {
		t.Fatalf("volle Paketliste: %v", err)
	}
	richtig := fensterBytesAusPaketen(string(alle), fenster)
	if richtig <= 0 || gezaehlt != richtig {
		t.Errorf("an den Messstellen %d Bytes gezaehlt, richtig sind %d", gezaehlt, richtig)
	}
}

// Umpacken mit echtem ffmpeg: eine MP4 mit H.264 und AAC wird zur MKV, das
// Bild bleibt Bit für Bit gleich, und die Prüfkette ohne "kleiner" besteht.
func TestUmpackenMitEchtemFFmpeg(t *testing.T) {
	ffmpeg := os.Getenv("CLOUDFORGE_TEST_FFMPEG")
	ffprobe := os.Getenv("CLOUDFORGE_TEST_FFPROBE")
	if ffmpeg == "" || ffprobe == "" {
		t.Skip("CLOUDFORGE_TEST_FFMPEG und CLOUDFORGE_TEST_FFPROBE nicht gesetzt")
	}
	ctx := context.Background()
	ordner := t.TempDir()

	quelle := filepath.Join(ordner, "film.mp4")
	erzeugen := exec.Command(ffmpeg, "-nostdin", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25:duration=5",
		"-f", "lavfi", "-i", "sine=duration=5",
		"-c:v", "libx264", "-c:a", "aac", "-shortest", quelle)
	if ausgabe, err := erzeugen.CombinedOutput(); err != nil {
		t.Fatalf("Testvideo nicht erzeugbar: %v (%s)", err, ausgabe)
	}
	info, err := KopfdatenLesen(ffprobe, quelle)
	if err != nil {
		t.Fatalf("Kopfdaten: %v", err)
	}

	e := standardWerte()
	e.FFmpegPfad, e.FFprobePfad = ffmpeg, ffprobe
	ergebnis := filepath.Join(ordner, "film.h264.mkv")
	auftrag := EncodeAuftrag{Quelle: quelle, Ziel: ergebnis, UntertitelCodecs: info.UntertitelCodecs, Umpacken: true}
	if err := Kodieren(ctx, auftrag, e, info.DauerSek, nil); err != nil {
		t.Fatalf("Umpacken: %v", err)
	}

	if p := UmpackErgebnisPruefen(ctx, info, ergebnis, e, nil); !p.Bestanden {
		t.Errorf("Prüfkette fürs Umpacken gescheitert: %s", p.ErsterFehler())
	}
	// Die Grösse darf beim Umpacken keine Rolle spielen — beim Umwandeln schon.
	winzigeQuelle := info
	winzigeQuelle.GroesseBytes = 1
	if p := UmpackErgebnisPruefen(ctx, winzigeQuelle, ergebnis, e, nil); !p.Bestanden {
		t.Errorf("Umpacken darf an der Grösse nicht scheitern: %s", p.ErsterFehler())
	}
	if p := ErgebnisPruefen(ctx, winzigeQuelle, ergebnis, e, nil); p.Bestanden {
		t.Error("beim Umwandeln muss ein grösseres Ergebnis durchfallen")
	}
	bildPruefsumme := func(pfad string) string {
		aus, err := exec.Command(ffmpeg, "-nostdin", "-loglevel", "error", "-i", pfad,
			"-map", "0:v:0", "-c", "copy", "-f", "md5", "-").Output()
		if err != nil {
			t.Fatalf("Prüfsumme von %s: %v", pfad, err)
		}
		return string(aus)
	}
	if a, b := bildPruefsumme(quelle), bildPruefsumme(ergebnis); a != b {
		t.Errorf("das Bild hat sich beim Umpacken verändert: %s gegen %s", a, b)
	}
}

// Die Vorauswahl mit echtem ffprobe: ein Video unter 720p wird ohne Messung nur
// umgepackt (seit 0.12.0), ein 720p-Video nicht — auch wenn es kürzer ist als
// die Mindestdauer fürs Messen, gilt für das kleine die Umpack-Regel.
func TestKandidatPruefenKleinesVideoNurUmpacken(t *testing.T) {
	ffmpeg := os.Getenv("CLOUDFORGE_TEST_FFMPEG")
	ffprobe := os.Getenv("CLOUDFORGE_TEST_FFPROBE")
	if ffmpeg == "" || ffprobe == "" {
		t.Skip("CLOUDFORGE_TEST_FFMPEG und CLOUDFORGE_TEST_FFPROBE nicht gesetzt")
	}
	ordner := t.TempDir()
	e := standardWerte()
	e.FFmpegPfad, e.FFprobePfad = ffmpeg, ffprobe

	erzeugen := func(name, groesse string) string {
		pfad := filepath.Join(ordner, name)
		befehl := exec.Command(ffmpeg, "-nostdin", "-loglevel", "error",
			"-f", "lavfi", "-i", "testsrc2=size="+groesse+":rate=25:duration=5",
			"-c:v", "libx264", "-preset", "ultrafast", pfad)
		if ausgabe, err := befehl.CombinedOutput(); err != nil {
			t.Fatalf("Testvideo %s nicht erzeugbar: %v (%s)", groesse, err, ausgabe)
		}
		return pfad
	}

	klein := KandidatPruefen(erzeugen("klein.mp4", "720x540"), e)
	if klein.Grund != KleineAufloesung || !klein.NurUmpacken() {
		t.Errorf("720x540: Umpacken wegen kleiner Aufloesung erwartet, bekommen %q (Fehler %v)",
			klein.Grund, klein.Fehler)
	}
	hd := KandidatPruefen(erzeugen("hd.mp4", "1280x720"), e)
	if hd.Grund == KleineAufloesung {
		t.Errorf("1280x720 darf nicht als klein gelten")
	}
}

// Versteckter Vorlauf: eine ohne Neukodieren geschnittene MP4 wird erkannt,
// das ungeschnittene Original nicht — so wie am 26.09.2026 an einem echten Film gemessen.
func TestVorlaufVerstecktMitEchtemFFmpeg(t *testing.T) {
	ffmpeg := os.Getenv("CLOUDFORGE_TEST_FFMPEG")
	ffprobe := os.Getenv("CLOUDFORGE_TEST_FFPROBE")
	if ffmpeg == "" || ffprobe == "" {
		t.Skip("CLOUDFORGE_TEST_FFMPEG und CLOUDFORGE_TEST_FFPROBE nicht gesetzt")
	}
	ordner := t.TempDir()
	ganz := filepath.Join(ordner, "ganz.mp4")
	// Schlüsselbild nur alle 10 s, damit ein Schnitt mitten in eine Gruppe fällt.
	erzeugen := exec.Command(ffmpeg, "-nostdin", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25:duration=20",
		"-f", "lavfi", "-i", "sine=duration=20",
		"-c:v", "libx264", "-g", "250", "-c:a", "aac", "-shortest", ganz)
	if ausgabe, err := erzeugen.CombinedOutput(); err != nil {
		t.Fatalf("Testvideo nicht erzeugbar: %v (%s)", err, ausgabe)
	}
	geschnitten := filepath.Join(ordner, "geschnitten.mp4")
	schneiden := exec.Command(ffmpeg, "-nostdin", "-loglevel", "error",
		"-ss", "4", "-i", ganz, "-t", "10", "-c", "copy", geschnitten)
	if ausgabe, err := schneiden.CombinedOutput(); err != nil {
		t.Fatalf("Schnitt nicht erzeugbar: %v (%s)", err, ausgabe)
	}

	if v, err := VorlaufVersteckt(ffprobe, ganz); err != nil || v {
		t.Errorf("ungeschnittene Datei: kein Vorlauf erwartet, bekommen %v (%v)", v, err)
	}
	if v, err := VorlaufVersteckt(ffprobe, geschnitten); err != nil || !v {
		t.Errorf("ohne Neukodieren geschnitten: Vorlauf erwartet, bekommen %v (%v)", v, err)
	}
}

// Seit 0.14.0 mit echtem ffmpeg: Eine MP4 mit Textuntertiteln (mov_text) und
// eingebettetem Titelbild liess sich bis 0.13.0 weder umwandeln noch umpacken
// (27.09.2026 nachgestellt). Jetzt kommen die Untertitel als SRT an, das
// Titelbild entfällt, der Ton bleibt — und eine Schrift in einer MKV kommt mit.
func TestUntertitelTitelbildUndSchriftMitEchtemFFmpeg(t *testing.T) {
	ffmpeg := os.Getenv("CLOUDFORGE_TEST_FFMPEG")
	ffprobe := os.Getenv("CLOUDFORGE_TEST_FFPROBE")
	if ffmpeg == "" || ffprobe == "" {
		t.Skip("CLOUDFORGE_TEST_FFMPEG und CLOUDFORGE_TEST_FFPROBE nicht gesetzt")
	}
	ctx := context.Background()
	ordner := t.TempDir()
	e := standardWerte()
	e.FFmpegPfad, e.FFprobePfad = ffmpeg, ffprobe

	ffmpegLassen := func(was string, args ...string) {
		befehl := exec.Command(ffmpeg, append([]string{"-nostdin", "-loglevel", "error", "-y"}, args...)...)
		if ausgabe, err := befehl.CombinedOutput(); err != nil {
			t.Fatalf("%s nicht erzeugbar: %v (%s)", was, err, ausgabe)
		}
	}
	spurenZaehlen := func(pfad, art string) int {
		aus, err := exec.Command(ffprobe, "-v", "error", "-select_streams", art,
			"-show_entries", "stream=index", "-of", "csv=p=0", pfad).Output()
		if err != nil {
			t.Fatalf("Spuren von %s: %v", pfad, err)
		}
		return len(strings.Fields(string(aus)))
	}
	hilfsdatei := func(name, inhalt string) string {
		pfad := filepath.Join(ordner, name)
		if err := os.WriteFile(pfad, []byte(inhalt), 0o644); err != nil {
			t.Fatal(err)
		}
		return pfad
	}

	film := filepath.Join(ordner, "film.mp4")
	ffmpegLassen("Film", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25:duration=5",
		"-f", "lavfi", "-i", "sine=duration=5",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-shortest", film)
	titelbild := filepath.Join(ordner, "titelbild.jpg")
	ffmpegLassen("Titelbild", "-f", "lavfi", "-i", "color=c=red:size=200x200", "-frames:v", "1", titelbild)
	untertitel := hilfsdatei("text.srt", "1\n00:00:01,000 --> 00:00:03,000\nHallo\n")

	quelle := filepath.Join(ordner, "mit-allem.mp4")
	ffmpegLassen("MP4 mit Untertitel und Titelbild", "-i", film, "-i", untertitel, "-i", titelbild,
		"-map", "0", "-map", "1", "-map", "2", "-c", "copy", "-c:s", "mov_text",
		"-disposition:v:1", "attached_pic", quelle)
	info, err := KopfdatenLesen(ffprobe, quelle)
	if err != nil {
		t.Fatalf("Kopfdaten: %v", err)
	}
	if !slices.Equal(info.UntertitelCodecs, []string{untertitelMP4Text}) || spurenZaehlen(quelle, "v") != 2 {
		t.Fatalf("Testquelle falsch aufgebaut: Untertitel %v, %d Videospuren",
			info.UntertitelCodecs, spurenZaehlen(quelle, "v"))
	}

	for _, umpacken := range []bool{true, false} {
		ergebnis := filepath.Join(ordner, fmt.Sprintf("ergebnis-umpacken-%v.mkv", umpacken))
		auftrag := EncodeAuftrag{Quelle: quelle, Ziel: ergebnis, CRF: 50, Umpacken: umpacken,
			UntertitelCodecs: info.UntertitelCodecs}
		if err := Kodieren(ctx, auftrag, e, info.DauerSek, nil); err != nil {
			t.Errorf("umpacken=%v: %v", umpacken, err)
			continue
		}
		neu, err := KopfdatenLesen(ffprobe, ergebnis)
		if err != nil {
			t.Fatalf("umpacken=%v, Kopfdaten des Ergebnisses: %v", umpacken, err)
		}
		if !slices.Equal(neu.UntertitelCodecs, []string{"subrip"}) {
			t.Errorf("umpacken=%v: Untertitel %v, erwartet SRT (subrip)", umpacken, neu.UntertitelCodecs)
		}
		if n := spurenZaehlen(ergebnis, "v"); n != 1 {
			t.Errorf("umpacken=%v: %d Videospuren, erwartet nur den Film (Titelbild entfaellt)", umpacken, n)
		}
		if len(neu.Tonspuren) != 1 || neu.Tonspuren[0].Codec != "aac" {
			t.Errorf("umpacken=%v: Ton nicht 1:1: %+v", umpacken, neu.Tonspuren)
		}
		if p := UmpackErgebnisPruefen(ctx, info, ergebnis, e, nil); !p.Bestanden {
			t.Errorf("umpacken=%v: Pruefkette: %s", umpacken, p.ErsterFehler())
		}
	}

	schrift := hilfsdatei("schrift.ttf", "keine echte Schrift, nur ein Anhang")
	mkv := filepath.Join(ordner, "mit-schrift.mkv")
	ffmpegLassen("MKV mit Schrift", "-i", film, "-attach", schrift,
		"-metadata:s:t", "mimetype=application/x-truetype-font", "-map", "0", "-c", "copy", mkv)
	mkvErgebnis := filepath.Join(ordner, "schrift-ergebnis.mkv")
	auftrag := EncodeAuftrag{Quelle: mkv, Ziel: mkvErgebnis, Umpacken: true}
	if err := Kodieren(ctx, auftrag, e, info.DauerSek, nil); err != nil {
		t.Fatalf("MKV mit Schrift umpacken: %v", err)
	}
	if n := spurenZaehlen(mkvErgebnis, "t"); n != 1 {
		t.Errorf("die Schrift ging verloren: %d Anhaenge im Ergebnis", n)
	}
}
