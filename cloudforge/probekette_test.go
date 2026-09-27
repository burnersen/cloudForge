package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
	if err := ProbeSchneiden(ctx, video, referenz, fenster, e); err != nil {
		t.Fatalf("Messausschnitte: %v", err)
	}

	// Gegen sich selbst gemessen muss fast 100 herauskommen — sonst paart die
	// Messung falsche Bilder oder liest den falschen Wert (NVENCForge-Lektion).
	selbst, err := VMAFMessen(ctx, referenz, referenz, e)
	if err != nil {
		t.Fatalf("VMAF gegen sich selbst: %v", err)
	}
	if selbst < 99 {
		t.Errorf("gegen sich selbst gemessen %.2f, erwartet fast 100", selbst)
	}

	probe := filepath.Join(ordner, "probe.mkv")
	auftrag := EncodeAuftrag{Quelle: referenz, Ziel: probe, CRF: 40, NurVideo: true}
	if err := Kodieren(ctx, auftrag, e, 0, nil); err != nil {
		t.Fatalf("Kodieren mit Variance Boost und tune 0: %v", err)
	}
	wert, err := VMAFMessen(ctx, probe, referenz, e)
	if err != nil {
		t.Fatalf("VMAF der Probe: %v", err)
	}
	if wert <= 20 || wert >= selbst {
		t.Errorf("VMAF der Probe %.2f ist unplausibel (gegen sich selbst %.2f)", wert, selbst)
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
	auftrag := EncodeAuftrag{Quelle: quelle, Ziel: ergebnis, Tonspuren: info.Tonspuren, Umpacken: true}
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
