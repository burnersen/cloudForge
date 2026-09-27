package main

// Alle ffmpeg-Aufrufe an einer Stelle: Ausschnitte schneiden, kodieren,
// Qualität messen.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Pixelformate für die beiden Bittiefen aus der INI. Gemessen am 23.09.2026
// auf dem VPS: 8 Bit ist etwa 1,5-mal so schnell wie 10 Bit und kostet bei
// gleichem CRF 0,4 VMAF. 10 Bit beugt Streifen in Farbverläufen etwas besser vor.
const (
	pixelFormat8Bit  = "yuv420p"
	pixelFormat10Bit = "yuv420p10le"
	videoCodec       = "libsvtav1"
	tonCodec         = "libopus"
)

// zielPixelFormat liefert das Pixelformat zur eingestellten Bittiefe.
func zielPixelFormat(e Einstellungen) string {
	if e.Bittiefe == 10 {
		return pixelFormat10Bit
	}
	return pixelFormat8Bit
}

// ErrAbgebrochen heisst: der Nutzer hat aufgehört (Fenster zu, Strg+C).
// Das ist kein Fehler der Datei und wird deshalb nicht als solcher vermerkt.
var ErrAbgebrochen = errors.New("abgebrochen")

// EncodeAuftrag beschreibt einen einzelnen Kodiervorgang.
type EncodeAuftrag struct {
	Quelle string
	Ziel   string
	CRF    int

	// NurVideo lässt Ton und Untertitel weg — für die Auto-CQ-Proben, bei
	// denen nur das Bild gemessen wird.
	NurVideo bool

	// Umpacken kopiert das Bild unverändert statt es umzuwandeln (seit
	// 0.11.0, wie NVENCForge) — für Dateien, bei denen sich das Umwandeln
	// nicht lohnt. Ton, Untertitel, Kapitel und Metadaten laufen genau wie
	// beim Umwandeln, damit das Ergebnis dieselben Spuren hat.
	Umpacken bool

	// Tonspuren bestimmt je Spur, ob sie kopiert oder zu Opus gewandelt wird.
	// Leer bedeutet: alles kopieren.
	Tonspuren []Tonspur
}

// videoArgumente sind die Encoder-Einstellungen, die für jeden Lauf gelten —
// für die Auto-CQ-Proben genauso wie für die ganze Datei. Nur so misst die
// Probe, was die Datei später wirklich bekommt.
func videoArgumente(crf int, e Einstellungen) []string {
	return []string{
		"-c:v", videoCodec,
		"-preset", strconv.Itoa(e.Preset),
		"-crf", strconv.Itoa(crf),
		"-pix_fmt", zielPixelFormat(e),
		"-svtav1-params", svtParameter(e),
	}
}

// svtParameter baut die SVT-AV1-eigenen Einstellungen zusammen. Die beiden
// Schalter bleiben ungenannt, solange sie aus sind: dann rechnet SVT-AV1 mit
// seinen Werkswerten (4.2: tune PSNR, Variance Boost aus) genau wie bis 0.9.0.
func svtParameter(e Einstellungen) string {
	teile := []string{"lp=" + strconv.Itoa(e.Kerne)}
	if e.Tune0 {
		teile = append(teile, "tune=0")
	}
	if e.VarianceBoost {
		teile = append(teile, "enable-variance-boost=1")
	}
	return strings.Join(teile, ":")
}

// tonArgumente entscheidet je Tonspur zwischen Kopieren und Umwandeln.
//
// Kleine Spuren werden bewusst NICHT angefasst: ein zweites Mal verlustbehaftet
// zu kodieren kostet Qualität und bringt dort keinen nennenswerten Platz.
func tonArgumente(spuren []Tonspur, e Einstellungen) []string {
	if len(spuren) == 0 {
		return []string{"-c:a", "copy"}
	}

	var args []string
	for nummer, spur := range spuren {
		ziel := fmt.Sprintf("-c:a:%d", nummer)
		if spur.BitrateKbps() > e.TonSchwelleKbps {
			args = append(args, ziel, tonCodec,
				fmt.Sprintf("-b:a:%d", nummer), strconv.Itoa(e.TonZielKbps)+"k")
		} else {
			args = append(args, ziel, "copy")
		}
	}
	return args
}

// EncodeArgumente baut die vollständige ffmpeg-Befehlszeile.
func EncodeArgumente(auftrag EncodeAuftrag, e Einstellungen) []string {
	args := []string{"-nostdin", "-y", "-i", auftrag.Quelle}

	if auftrag.NurVideo {
		args = append(args, "-map", "0:v:0", "-an", "-sn")
		args = append(args, videoArgumente(auftrag.CRF, e)...)
		return append(args, auftrag.Ziel)
	}

	// Alle Spuren mitnehmen, aber an einer fehlenden Sorte nicht scheitern.
	args = append(args,
		"-map", "0:v", "-map", "0:a?", "-map", "0:s?",
		"-map_metadata", "0", "-map_chapters", "0",
	)
	if auftrag.Umpacken {
		args = append(args, "-c:v", "copy")
	} else {
		args = append(args, videoArgumente(auftrag.CRF, e)...)
	}
	args = append(args, tonArgumente(auftrag.Tonspuren, e)...)
	args = append(args, "-c:s", "copy")

	return append(args, auftrag.Ziel)
}

// Kodieren führt einen Encode aus. Ist gesamtSek bekannt und eine
// Rückmeldung angegeben, wird laufend gemeldet, wie weit er ist.
func Kodieren(ctx context.Context, auftrag EncodeAuftrag, e Einstellungen, gesamtSek float64, melde Rueckmeldung) error {
	args := EncodeArgumente(auftrag, e)
	if _, err := ffmpegLaufen(ctx, e.FFmpegPfad, args, gesamtSek, melde); err != nil {
		if auftrag.Umpacken {
			return fmt.Errorf("Umpacken fehlgeschlagen: %w", err)
		}
		return fmt.Errorf("Kodieren nach CRF %d fehlgeschlagen: %w", auftrag.CRF, err)
	}
	return nil
}

// ProbeSchneiden legt aus mehreren Ausschnitten der Quelle eine verlustfreie
// Vergleichsdatei an.
//
// Warum verlustfrei zwischenspeichern: Die Ausschnitte werden anschließend
// mehrfach kodiert und gemessen. Ohne Zwischenspeicher müsste die Quelle
// jedes Mal neu dekodiert werden. Und weil Referenz und Probe aus derselben
// Datei stammen, stimmen die Bildzeiten sicher überein — sonst misst man
// verschobene Bildpaare und bekommt sinnlose Werte.
//
// Warum FFVHuff und nicht mehr FFV1 (seit 0.10.0): Die Vergleichsdatei wird
// je Probe zweimal gelesen (Kodieren, Messen), und FFV1 ist beim Entpacken
// langsam. Gemessen 26.09.2026 auf netcup an 3 x 8 s 1080p50, 10 Bit:
// FFV1 17 s nur zum Entpacken, Probe 53 s; FFVHuff 4 s, Probe 33 s — und die
// kodierte Probe war BITGLEICH (gleiche Prüfsumme, gleicher VMAF-Wert),
// verlustfrei ist beides. Dafür ist die Datei dreimal so gross (1,8 GB
// statt 0,6 GB), was neben dem Film nicht ins Gewicht fällt. Rohdaten wären
// nur noch 4 s schneller gewesen, bei 7 GB.
func ProbeSchneiden(ctx context.Context, quelle, ziel string, fenster []Fenster, e Einstellungen) error {
	if len(fenster) == 0 {
		return fmt.Errorf("keine Messfenster angegeben")
	}

	// Die Vergleichsdatei hat dieselbe Bittiefe wie das Ergebnis. So misst
	// VMAF genau das, was der Encoder verliert. Eine 10-Bit-Quelle wird bei
	// bittiefe=8 also schon hier gerundet — diese Rundung sieht die Messung nicht.
	args := append(fensterArgumente(quelle, fenster),
		"-c:v", "ffvhuff",
		"-pix_fmt", zielPixelFormat(e),
		ziel)

	if _, err := ffmpegLaufen(ctx, e.FFmpegPfad, args, 0, nil); err != nil {
		return fmt.Errorf("Messausschnitte konnten nicht erzeugt werden: %w", err)
	}
	return nil
}

// fensterArgumente liest die Ausschnitte einer Quelle ein, hängt sie
// hintereinander und setzt die Bildzeiten neu, damit das Ergebnis bei null
// beginnt und lückenlos läuft — nur das Bild, ohne Ton und Untertitel. Es
// fehlen noch Encoder und Zieldatei; so dient es den Messausschnitten und der
// Grössenprobe gleichermassen.
func fensterArgumente(quelle string, fenster []Fenster) []string {
	args := []string{"-nostdin", "-y"}
	for _, f := range fenster {
		args = append(args,
			"-ss", zahlText(f.StartSek),
			"-t", zahlText(f.LaengeSek),
			"-i", quelle)
	}

	var kette strings.Builder
	for i := range fenster {
		fmt.Fprintf(&kette, "[%d:v:0]", i)
	}
	fmt.Fprintf(&kette, "concat=n=%d:v=1:a=0[zusammen];[zusammen]setpts=PTS-STARTPTS[fertig]", len(fenster))

	return append(args,
		"-filter_complex", kette.String(),
		"-map", "[fertig]",
		"-an", "-sn")
}

// VMAFMessen vergleicht eine kodierte Datei mit ihrer Referenz.
//
// Die Bilder werden nach ihrer NUMMER gepaart, nicht nach ihrer Zeit: beide
// Seiten bekommen dieselben künstlichen Zeitstempel 0, 1, 2, ... Nach Zeit
// gepaart, reicht schon das Runden auf Millisekunden im MKV, um stellenweise
// Nachbarbilder zu vergleichen — gemessen am 25.09.2026 an einem Ausschnitt:
// 90,8 statt richtig 98,2, und die Werte sahen trotzdem glaubwürdig aus.
// Die Probe entsteht Bild für Bild aus der Referenz, deshalb ist die Nummer
// hier der sichere Schlüssel.
//
// Gewertet wird nur jedes dritte Bild (n_subsample, seit 0.10.0, wie in
// NVENCForge): Gemessen 26.09.2026 an 1200 Bildern 1080p50 kam 97,404 statt
// 97,407 heraus, der Messschritt wurde 5 s schneller. Die Bildpaarung bleibt
// davon unberührt — gepaart wird weiter jedes Bild, nur gerechnet wird
// seltener.
//
// breite und hoehe sind die Masse der Quelle: Ist sie kleiner als 1080p, wird
// auf 1080p vergrössert gemessen (seit 0.12.0, siehe vmafMessgroesse).
func VMAFMessen(ctx context.Context, probe, referenz string, breite, hoehe int, e Einstellungen) (float64, error) {
	// Alle Kerne: ffmpeg läuft mit niedrigster Priorität (schonenderBefehl),
	// der Desktop behält trotzdem Vorrang.
	filter := vmafFilter(breite, hoehe, runtime.NumCPU())

	args := []string{
		"-nostdin", "-hide_banner", "-nostats",
		"-i", probe,
		"-i", referenz,
		"-lavfi", filter,
		"-f", "null", "-",
	}

	befehl := schonenderBefehl(ctx, e.FFmpegPfad, args...)
	var ausgabe strings.Builder
	befehl.Stdout = &ausgabe
	befehl.Stderr = &ausgabe

	if err := befehl.Run(); err != nil {
		if ctx.Err() != nil {
			return 0, ErrAbgebrochen
		}
		return 0, fmt.Errorf("VMAF-Messung fehlgeschlagen: %w (%s)", err, letzteZeilen(ausgabe.String(), 3))
	}
	return vmafAusAusgabe(ausgabe.String())
}

// Das VMAF-Standardmodell ist für ein Bild gebaut, das einen 1080p-Schirm füllt.
const (
	vmafSchirmLang = 1920
	vmafSchirmKurz = 1080
)

// vmafMessgroesse sagt, auf welche Grösse beide Seiten vor der Messung
// gebracht werden.
//
// Warum vergrössern (seit 0.12.0): VMAF bewertet jedes Bild so, als füllte es
// einen 1080p-Schirm. Ein kleineres Video in seiner eigenen Grösse gemessen
// kommt deshalb zu gut weg — geschaut wird es im Vollbild, und dort wird jeder
// Kodierfehler mit vergrössert. Gemessen am 27.09.2026 an fertigen Filmen,
// eigene Grösse gegen 1080p: 720p 96,2 gegen 92,9, 540p 95,2 gegen 88,4,
// 404p 97,0 gegen 87,8 — der Nutzer fand genau diese Dateien pixelig.
//
// Vergrössert wird wie beim Abspielen im Vollbild: bis die lange Kante 1920
// oder die kurze 1080 erreicht, im gleichen Seitenverhältnis. Hochkant zählt
// genauso, nur gedreht. Ab 1080p und bei unbekannten Massen bleibt alles, wie
// es ist. Anamorphe Quellen (nicht quadratische Bildpunkte) werden nicht eigens
// behandelt: Ihre Höhe stimmt, und nach der richtet sich der Sitzabstand, auf
// den das Modell eingestellt ist.
func vmafMessgroesse(breite, hoehe int) (zielBreite, zielHoehe int, vergroessern bool) {
	if breite <= 0 || hoehe <= 0 {
		return breite, hoehe, false
	}
	lang, kurz := breite, hoehe
	if kurz > lang {
		lang, kurz = kurz, lang
	}
	faktor := math.Min(float64(vmafSchirmLang)/float64(lang), float64(vmafSchirmKurz)/float64(kurz))
	if faktor <= 1 {
		return breite, hoehe, false
	}
	return geradeRunden(float64(breite) * faktor), geradeRunden(float64(hoehe) * faktor), true
}

// geradeRunden rundet auf die nächste gerade Zahl — das Farbformat 4:2:0
// braucht gerade Kantenlängen. Über den Schirm hinaus rundet es nie: Was
// höchstens 1080 ist, wird höchstens 1080.
func geradeRunden(wert float64) int {
	return int(math.Round(wert/2)) * 2
}

// vmafFilter baut den Messgraphen. Beide Seiten werden nach Bildnummer gepaart
// (siehe VMAFMessen) und, wo nötig, mit demselben Filter vergrössert — sonst
// würde der Unterschied der Filter mitgemessen statt der Kodierfehler.
func vmafFilter(breite, hoehe, threads int) string {
	const nachNummer = "settb=1/25,setpts=N" // Zeitbasis beliebig, Hauptsache gleich
	const jedesNteBild = 3

	vorbereitung := nachNummer
	if zielBreite, zielHoehe, vergroessern := vmafMessgroesse(breite, hoehe); vergroessern {
		vorbereitung += fmt.Sprintf(",scale=%d:%d:flags=bicubic", zielBreite, zielHoehe)
	}
	return fmt.Sprintf(
		"[0:v]%s[probe];[1:v]%s[ref];[probe][ref]libvmaf=n_threads=%d:n_subsample=%d",
		vorbereitung, vorbereitung, threads, jedesNteBild)
}

// vmafAusAusgabe sucht den Punktwert in der ffmpeg-Ausgabe.
func vmafAusAusgabe(ausgabe string) (float64, error) {
	const marke = "VMAF score: "

	stelle := strings.LastIndex(ausgabe, marke)
	if stelle < 0 {
		return 0, fmt.Errorf("kein VMAF-Wert in der Ausgabe gefunden (%s)", letzteZeilen(ausgabe, 3))
	}

	rest := ausgabe[stelle+len(marke):]
	if ende := strings.IndexAny(rest, "\r\n "); ende >= 0 {
		rest = rest[:ende]
	}

	wert, err := strconv.ParseFloat(strings.TrimSpace(rest), 64)
	if err != nil {
		return 0, fmt.Errorf("VMAF-Wert %q unlesbar", rest)
	}
	if wert < 0 || wert > 100 {
		return 0, fmt.Errorf("VMAF-Wert %v liegt ausserhalb von 0 bis 100", wert)
	}
	return wert, nil
}

// ffmpegLaufen startet ffmpeg und liefert, was es an Fehlermeldungen
// ausgegeben hat.
//
// Mit gesamtSek > 0 und einer Rückmeldung schreibt ffmpeg über -progress
// laufend seinen Stand, der hier in Prozent umgerechnet wird. Ohne beides
// läuft es still — so bei den kurzen Auto-CQ-Proben.
func ffmpegLaufen(ctx context.Context, ffmpegPfad string, args []string, gesamtSek float64, melde Rueckmeldung) (string, error) {
	mitFortschritt := melde != nil && gesamtSek > 0

	vorn := []string{"-loglevel", "error", "-nostats"}
	if mitFortschritt {
		vorn = append(vorn, "-progress", "pipe:1")
	}
	befehl := schonenderBefehl(ctx, ffmpegPfad, append(vorn, args...)...)

	var meldungen strings.Builder
	befehl.Stderr = &meldungen

	if !mitFortschritt {
		err := befehl.Run()
		return meldungen.String(), ffmpegFehlerDeuten(ctx, err, meldungen.String())
	}

	ausgabe, err := befehl.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("ffmpeg-Ausgabe nicht lesbar: %w", err)
	}
	if err := befehl.Start(); err != nil {
		return "", fmt.Errorf("ffmpeg laesst sich nicht starten: %w", err)
	}

	beginn := time.Now()
	var stand ffmpegFortschritt
	leser := bufio.NewScanner(ausgabe)
	for leser.Scan() {
		if !stand.zeileLesen(leser.Text()) {
			continue
		}
		anteil := math.Min(stand.zeitSek/gesamtSek, 1)
		melde(Stand{
			Anteil:       anteil,
			Tempo:        stand.tempo,
			Rest:         restzeitSchaetzen(time.Since(beginn), anteil),
			Position:     stand.zeitSek,
			Bild:         stand.bild,
			BilderProSek: stand.bilderProSek,
			BitrateKbps:  stand.bitrateKbps,
			Bytes:        stand.bytes,
		})
	}

	err = befehl.Wait()
	return meldungen.String(), ffmpegFehlerDeuten(ctx, err, meldungen.String())
}

// ffmpegFortschritt liest die Ausgabe von -progress. Sie kommt in Blöcken aus
// Zeilen wie "out_time_us=4920000" und "speed=0.62x", jeder Block endet mit
// "progress=continue" oder "progress=end".
//
// Am Anfang schreibt ffmpeg bei vielen Werten "N/A" oder 0. Solche Zeilen
// werden übergangen, damit der letzte gute Wert stehen bleibt.
type ffmpegFortschritt struct {
	zeitSek      float64
	tempo        float64
	bild         int64
	bilderProSek float64
	bitrateKbps  float64
	bytes        int64
}

// zeileLesen verarbeitet eine Zeile. true heisst: ein Block ist vollständig
// und kann gemeldet werden.
func (f *ffmpegFortschritt) zeileLesen(zeile string) bool {
	schluessel, wert, ok := strings.Cut(strings.TrimSpace(zeile), "=")
	if !ok {
		return false
	}
	wert = strings.TrimSpace(wert)

	switch schluessel {
	// out_time_ms ist trotz des Namens ebenfalls in Mikrosekunden — ein
	// bekannter Namensfehler in ffmpeg, der aus Rücksicht bestehen bleibt.
	case "out_time_us", "out_time_ms":
		if mikro, err := strconv.ParseInt(wert, 10, 64); err == nil && mikro >= 0 {
			f.zeitSek = float64(mikro) / 1e6
		}
	case "speed":
		if tempo, err := strconv.ParseFloat(strings.TrimSuffix(wert, "x"), 64); err == nil && tempo > 0 {
			f.tempo = tempo
		}
	case "frame":
		if bild, err := strconv.ParseInt(wert, 10, 64); err == nil && bild > 0 {
			f.bild = bild
		}
	case "fps":
		if fps, err := strconv.ParseFloat(wert, 64); err == nil && fps > 0 {
			f.bilderProSek = fps
		}
	case "bitrate": // "1834.5kbits/s"
		if kbps, err := strconv.ParseFloat(strings.TrimSuffix(wert, "kbits/s"), 64); err == nil && kbps > 0 {
			f.bitrateKbps = kbps
		}
	case "total_size":
		if bytes, err := strconv.ParseInt(wert, 10, 64); err == nil && bytes > 0 {
			f.bytes = bytes
		}
	case "progress":
		return true
	}
	return false
}

// schonenderBefehl startet ein rechenhungriges Programm mit niedrigster
// Priorität. Es darf dann alle Kerne nutzen, die gerade frei sind — sobald
// aber jemand am Desktop arbeitet, bekommt der Vorrang.
//
// Bewusst über das Programm "nice" und nicht über setpriority im eigenen
// Prozess: Unter Linux gilt die Priorität je Thread. Ein Go-Programm hat
// mehrere Threads, und ffmpeg würde die Priorität desjenigen erben, von dem
// aus es gerade gestartet wird — das wäre Zufall.
//
// Fehlt "nice" (nur auf dem Entwicklungsrechner), läuft es normal.
func schonenderBefehl(ctx context.Context, programm string, args ...string) *exec.Cmd {
	nice, err := exec.LookPath("nice")
	if err != nil {
		return exec.CommandContext(ctx, programm, args...)
	}
	return exec.CommandContext(ctx, nice, append([]string{"-n", "19", programm}, args...)...)
}

// ffmpegFehlerDeuten macht aus einem gescheiterten Lauf eine verständliche
// Meldung — "exit status 1" allein hilft niemandem weiter.
func ffmpegFehlerDeuten(ctx context.Context, err error, meldungen string) error {
	if err == nil {
		return nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("Zeitlimit ueberschritten")
	}
	if ctx.Err() != nil {
		return ErrAbgebrochen
	}

	// Schliesst jemand das Fenster, bekommen ffmpeg und dieses Programm das
	// Signal gleichzeitig — ffmpeg ist manchmal schneller tot, als hier der
	// Abbruch ankommt. Auch das ist kein Fehler der Datei.
	var exitFehler *exec.ExitError
	if errors.As(err, &exitFehler) {
		if status, ok := exitFehler.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			switch status.Signal() {
			case syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM:
				return ErrAbgebrochen
			}
		}
	}
	return fmt.Errorf("%w: %s", err, letzteZeilen(meldungen, 3))
}

// letzteZeilen kürzt eine Ausgabe auf die letzten paar Zeilen.
func letzteZeilen(text string, anzahl int) string {
	zeilen := strings.Split(strings.TrimSpace(text), "\n")
	if len(zeilen) > anzahl {
		zeilen = zeilen[len(zeilen)-anzahl:]
	}
	return strings.TrimSpace(strings.Join(zeilen, " | "))
}

// zahlText gibt eine Sekundenangabe für die Befehlszeile aus. Immer mit
// Punkt als Trennzeichen, unabhängig von der Spracheinstellung.
func zahlText(f float64) string {
	return strconv.FormatFloat(f, 'f', 3, 64)
}

// DateiGroesse liefert die Größe in Bytes, 0 wenn die Datei fehlt.
func DateiGroesse(pfad string) int64 {
	zustand, err := os.Stat(pfad)
	if err != nil {
		return 0
	}
	return zustand.Size()
}

// ProzentKleiner sagt, um wie viel Prozent das Ergebnis kleiner ist als die
// Quelle. Negative Werte bedeuten: es ist größer geworden.
func ProzentKleiner(quelleBytes, ergebnisBytes int64) float64 {
	if quelleBytes <= 0 {
		return 0
	}
	return (1 - float64(ergebnisBytes)/float64(quelleBytes)) * 100
}

// rundeAuf begrenzt einen Wert auf einen Bereich.
func rundeAuf(wert, min, max int) int {
	return int(math.Min(math.Max(float64(wert), float64(min)), float64(max)))
}
