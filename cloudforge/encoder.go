// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

// Alle ffmpeg-Aufrufe an einer Stelle: Ausschnitte schneiden, kodieren,
// Qualität messen.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
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
)

// Videospuren werden mit grossem V gewählt: das sind nur die echten Filme,
// ohne eingebettete Titelbilder (Vorschaubild einer MP4, cover.jpg einer
// MKV). Mit kleinem v wollte SVT-AV1 auch das Titelbild umwandeln und brach
// ab ("maximum allowed frame rate is 240 fps", getestet 27.09.2026). Das
// Titelbild entfällt damit — MKV kann es über ffmpeg ohnehin nicht als echtes
// Titelbild ablegen, nur als zweite Videospur mit einem einzigen Bild.
const (
	alleFilmspuren       = "0:V"
	ersteFilmspur        = "0:V:0"
	ersteFilmspurAuswahl = "V:0" // ohne Dateinummer: ffprobe -select_streams, Filter-Eingänge
)

// Untertitel im MP4-Textformat kann MKV nicht aufnehmen: ffmpeg bricht dann
// sofort ab, beim Umwandeln wie beim Umpacken (getestet 27.09.2026). Sie
// werden deshalb nach SRT übertragen — Text und Zeiten bleiben gleich.
const (
	untertitelMP4Text = "mov_text"
	untertitelSRT     = "srt"
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

	// UntertitelCodecs nennt je Untertitelspur der Quelle ihr Format, in der
	// Reihenfolge der Datei (aus VideoInfo). Danach richtet sich, welche
	// Spur 1:1 kopiert und welche nach SRT übertragen wird.
	UntertitelCodecs []string

	// Verkleinern bringt das Bild auf die Grösse des Ergebnisses
	// (verkleinernFilter, seit 0.17.0); leer = Grösse der Quelle.
	// Farbangaben sind die der Quelle (farbArgumente). Beides gilt nur beim
	// Umwandeln — beim Umpacken bleibt das Bild ohnehin, wie es ist.
	Verkleinern string
	Farbangaben []string

	// MitFilmKorn legt das Filmkorn aus der INI (filmKorn) auf — nur beim
	// finalen Encode, nie bei Messproben oder der Grössenprobe (siehe
	// svtParameter).
	MitFilmKorn bool
}

// videoArgumente sind die Encoder-Einstellungen, die für jeden Lauf gelten —
// für die Auto-CQ-Proben genauso wie für die ganze Datei. Nur so misst die
// Probe, was die Datei später wirklich bekommt. Einzige Ausnahme ist das
// Filmkorn (mitFilmKorn), siehe svtParameter.
func videoArgumente(crf int, e Einstellungen, mitFilmKorn bool) []string {
	return []string{
		"-c:v", videoCodec,
		"-preset", strconv.Itoa(e.Preset),
		"-crf", strconv.Itoa(crf),
		"-pix_fmt", zielPixelFormat(e),
		"-svtav1-params", svtParameter(e, mitFilmKorn),
	}
}

// svtParameter baut die SVT-AV1-eigenen Einstellungen zusammen. Die beiden
// Schalter bleiben ungenannt, solange sie aus sind: dann rechnet SVT-AV1 mit
// seinen Werkswerten (4.2: tune PSNR, Variance Boost aus) genau wie bis 0.9.0.
// Stärke und Oktil gehen nur mit dem Variance Boost mit, dann aber immer
// ausdrücklich — so gilt, was in INI und Protokoll steht, auch wenn eine
// neuere SVT-Fassung andere Werkswerte mitbringt.
//
// Filmkorn (seit 0.19.0) nur mit mitFilmKorn, also nur im finalen Encode:
// Der Decoder (libdav1d) legt das Korn beim Abspielen auf, und VMAF würde es
// in einer Messprobe als Fehler werten — Auto-CQ nähme dann einen viel zu
// niedrigen CRF. Entrauscht wird die Quelle nie (film-grain-denoise=0, auch
// wenn das der Werkswert ist, steht es ausdrücklich da): Sonst kodiert der
// finale Encode ein geglättetes Bild, das nicht mehr zu den Messproben passt
// und wachsartig wirkt. Gemessen 01.10.2026: mit denoise=0 wird die Datei nur
// 0,2 bis 1,2 % grösser als ohne Korn — die Grössen-Vorhersage aus den Proben
// stimmt. Dafür dauert der Encode bei preset 9 das 8,5-fache (filmKorn in
// config.go).
//
// lp bleibt auch mit mehreren Dateien gleichzeitig (parallelDateien) bei
// kerne: SVT-AV1s lp ist keine Kernzahl, sondern ein Grad der Parallelität,
// und Aufteilen kostet. Gemessen 01.10.2026 auf netcup (8 Kerne) an 60 s
// 1080p50, preset 9, je Datei: allein lp 6 33,0 s; zu zweit je lp 6 28,3 s
// (+17 % Durchsatz), je lp 4 28,7 s, je lp 3 35,1 s (6 % langsamer als
// nacheinander); zu dritt je lp 3 33,1 s.
func svtParameter(e Einstellungen, mitFilmKorn bool) string {
	teile := []string{"lp=" + strconv.Itoa(e.Kerne)}
	if e.Tune0 {
		teile = append(teile, "tune=0")
	}
	if e.VarianceBoost {
		teile = append(teile, "enable-variance-boost=1",
			"variance-boost-strength="+strconv.Itoa(e.VarianceBoostStaerke),
			"variance-octile="+strconv.Itoa(e.VarianceOktil))
	}
	if mitFilmKorn && e.FilmKorn > 0 {
		teile = append(teile, "film-grain="+strconv.Itoa(e.FilmKorn), "film-grain-denoise=0")
	}
	return strings.Join(teile, ":")
}

// untertitelArgumente kopiert jede Untertitelspur 1:1 — ausser dem
// MP4-Textformat, das nach SRT übertragen wird (siehe untertitelMP4Text).
// Jede Spur bekommt ihre eigene Angabe, damit keine allgemeine Regel eine
// einzelne überdecken kann. Andere Formate (ASS mit Gestaltung, Bild-
// Untertitel) kann MKV so aufnehmen, wie sie sind.
func untertitelArgumente(codecs []string) []string {
	if len(codecs) == 0 {
		return []string{"-c:s", "copy"}
	}
	args := make([]string, 0, 2*len(codecs))
	for nummer, codec := range codecs {
		behandlung := "copy"
		if codec == untertitelMP4Text {
			behandlung = untertitelSRT
		}
		args = append(args, fmt.Sprintf("-c:s:%d", nummer), behandlung)
	}
	return args
}

// EncodeArgumente baut die vollständige ffmpeg-Befehlszeile.
func EncodeArgumente(auftrag EncodeAuftrag, e Einstellungen) []string {
	args := []string{"-nostdin", "-y", "-i", auftrag.Quelle}

	if auftrag.NurVideo {
		args = append(args, "-map", ersteFilmspur, "-an", "-sn")
		args = append(args, videoArgumente(auftrag.CRF, e, auftrag.MitFilmKorn)...)
		return append(args, auftrag.Ziel)
	}

	// Alle Spuren mitnehmen, aber an einer fehlenden Sorte nicht scheitern.
	// Anhänge (Schriften für gestaltete Untertitel in MKV) gehören dazu —
	// ohne sie zeigt ein Player die Untertitel in einer falschen Schrift.
	args = append(args,
		"-map", alleFilmspuren, "-map", "0:a?", "-map", "0:s?", "-map", "0:t?",
		"-map_metadata", "0", "-map_chapters", "0",
	)
	if auftrag.Umpacken {
		args = append(args, "-c:v", "copy")
	} else {
		if auftrag.Verkleinern != "" {
			args = append(args, "-vf", auftrag.Verkleinern)
		}
		args = append(args, videoArgumente(auftrag.CRF, e, auftrag.MitFilmKorn)...)
		args = append(args, auftrag.Farbangaben...)
	}
	// Ton immer 1:1 (seit 0.14.0, Nutzerwunsch): ein zweites Mal
	// verlustbehaftet zu kodieren kostet nur Qualität, und MKV nimmt jede
	// Tonspur so auf, wie sie ist.
	args = append(args, "-c:a", "copy")
	args = append(args, untertitelArgumente(auftrag.UntertitelCodecs)...)
	args = append(args, "-c:t", "copy")

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
func ProbeSchneiden(ctx context.Context, quelle, ziel string, fenster []Fenster, verkleinern string, e Einstellungen) error {
	if len(fenster) == 0 {
		return fmt.Errorf("keine Messfenster angegeben")
	}

	// Die Vergleichsdatei hat dieselbe Bittiefe wie das Ergebnis. So misst
	// VMAF genau das, was der Encoder verliert. Eine 10-Bit-Quelle wird bei
	// bittiefe=8 also schon hier gerundet — diese Rundung sieht die Messung nicht.
	// Ebenso die Grösse (seit 0.17.0): Wird verkleinert, ist schon die
	// Vergleichsdatei verkleinert — gemessen wird dann, was AV1 an der
	// kleineren Fassung verliert, wie in NVENCForge.
	args := append(fensterArgumente(quelle, fenster, verkleinern),
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
// Grössenprobe gleichermassen. verkleinern (verkleinernFilter) bringt die
// Stücke auf die Grösse des Ergebnisses, leer = unverändert.
func fensterArgumente(quelle string, fenster []Fenster, verkleinern string) []string {
	args := []string{"-nostdin", "-y"}
	for _, f := range fenster {
		args = append(args,
			"-ss", zahlText(f.StartSek),
			"-t", zahlText(f.LaengeSek),
			"-i", quelle)
	}

	var kette strings.Builder
	for i := range fenster {
		fmt.Fprintf(&kette, "[%d:%s]", i, ersteFilmspurAuswahl)
	}
	fmt.Fprintf(&kette, "concat=n=%d:v=1:a=0[zusammen];[zusammen]setpts=PTS-STARTPTS", len(fenster))
	if verkleinern != "" {
		kette.WriteString("," + verkleinern)
	}
	kette.WriteString("[fertig]")

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
// Gewertet wird nur jedes vierte Bild (n_subsample; 0.10.0 bis 0.18.0 jedes
// dritte, wie in NVENCForge; seit 0.19.0 vier auf Wunsch des Nutzers — ein
// Viertel weniger Rechenarbeit). Gemessen 26.09.2026 an 1200 Bildern 1080p50
// mit jedem dritten: 97,404 statt 97,407. Bei 5 Stücken zu 8 s mit 25
// Bildern/s bleiben 250 gewertete Bilder, das 5-%-Perzentil ist dann etwa das
// zwölftschlechteste. Die Bildpaarung bleibt davon unberührt — gepaart wird
// weiter jedes Bild, nur gerechnet wird seltener.
//
// Seit 0.19.0 liest CloudForge die Werte jedes gewerteten Bildes aus dem
// Protokoll von libvmaf und rechnet selbst Mittelwert und Perzentil aus
// (vmafAusProtokoll) — libvmaf selbst kennt kein Perzentil.
//
// breite und hoehe sind die Masse der Referenz (seit 0.17.0 die des Ergebnisses,
// also nach maxAufloesung): Ist sie kleiner als 1080p, wird
// auf 1080p vergrössert gemessen (seit 0.12.0, siehe vmafMessgroesse).
func VMAFMessen(ctx context.Context, probe, referenz string, breite, hoehe int, e Einstellungen) (VMAFWerte, error) {
	// Das Protokoll entsteht neben der Probe. Sein Name geht OHNE Pfad in den
	// Filter: Ein Doppelpunkt oder Komma im Pfad würde den Filter zerbrechen
	// (Universal-Lektion). Deshalb läuft ffmpeg im Ordner der Probe, und die
	// beiden Eingänge bekommen ihren vollen Pfad.
	ordner := filepath.Dir(probe)
	protokollPfad := filepath.Join(ordner, vmafProtokollName)
	defer os.Remove(protokollPfad)
	probeVoll, err := filepath.Abs(probe)
	if err != nil {
		return VMAFWerte{}, err
	}
	referenzVoll, err := filepath.Abs(referenz)
	if err != nil {
		return VMAFWerte{}, err
	}

	// Alle Kerne: ffmpeg läuft mit niedrigster Priorität (schonenderBefehl),
	// der Desktop behält trotzdem Vorrang. Auch mit mehreren Dateien
	// gleichzeitig nicht aufgeteilt — beim Kodieren hat Aufteilen gemessen
	// geschadet (siehe svtParameter), die Messung ist nur ein kurzer Teil.
	args := []string{
		"-nostdin", "-hide_banner", "-nostats",
		"-i", probeVoll,
		"-i", referenzVoll,
		"-lavfi", vmafFilter(breite, hoehe, runtime.NumCPU(), vmafProtokollName),
		"-f", "null", "-",
	}

	befehl := schonenderBefehl(ctx, e.FFmpegPfad, args...)
	befehl.Dir = ordner
	var ausgabe strings.Builder
	befehl.Stdout = &ausgabe
	befehl.Stderr = &ausgabe

	if err := befehl.Run(); err != nil {
		if ctx.Err() != nil {
			return VMAFWerte{}, ErrAbgebrochen
		}
		return VMAFWerte{}, fmt.Errorf("VMAF-Messung fehlgeschlagen: %w (%s)", err, letzteZeilen(ausgabe.String(), 3))
	}
	inhalt, err := os.ReadFile(protokollPfad)
	if err != nil {
		return VMAFWerte{}, fmt.Errorf("VMAF-Messung ohne Protokoll: %w (%s)", err, letzteZeilen(ausgabe.String(), 3))
	}
	return vmafAusProtokoll(inhalt, e.VMAFPerzentil)
}

// vmafProtokollName: libvmafs Protokoll mit dem Wert jedes gewerteten Bildes.
// Jede Datei hat ihren eigenen Arbeitsplatz, und ihre Messungen laufen
// nacheinander — ein fester Name genügt.
const vmafProtokollName = "vmaf-bildwerte.json"

// VMAFWerte ist das Ergebnis einer Messung.
type VMAFWerte struct {
	// Wert vergleicht Auto-CQ mit dem Ziel: das untere Perzentil aus
	// vmafPerzentil, bei vmafPerzentil=0 der Mittelwert.
	Wert float64
	// Mittel ist immer der Mittelwert — fürs Protokoll und zum Vergleich mit
	// den Läufen bis 0.18.0, die nur ihn kannten.
	Mittel float64
}

// vmafAusProtokoll liest das JSON-Protokoll von libvmaf (log_fmt=json) und
// rechnet Mittelwert und Perzentil über die gewerteten Bilder aus.
func vmafAusProtokoll(inhalt []byte, perzentil int) (VMAFWerte, error) {
	var protokoll struct {
		Frames []struct {
			Metrics struct {
				VMAF *float64 `json:"vmaf"`
			} `json:"metrics"`
		} `json:"frames"`
	}
	if err := json.Unmarshal(inhalt, &protokoll); err != nil {
		return VMAFWerte{}, fmt.Errorf("VMAF-Protokoll unlesbar: %w", err)
	}
	if len(protokoll.Frames) == 0 {
		return VMAFWerte{}, fmt.Errorf("VMAF-Protokoll enthaelt keine Bildwerte")
	}

	werte := make([]float64, 0, len(protokoll.Frames))
	summe := 0.0
	for nummer, bild := range protokoll.Frames {
		if bild.Metrics.VMAF == nil {
			return VMAFWerte{}, fmt.Errorf("VMAF-Protokoll: Bild %d ohne VMAF-Wert", nummer)
		}
		wert := *bild.Metrics.VMAF
		if wert < 0 || wert > 100 {
			return VMAFWerte{}, fmt.Errorf("VMAF-Wert %v liegt ausserhalb von 0 bis 100", wert)
		}
		werte = append(werte, wert)
		summe += wert
	}

	mittel := summe / float64(len(werte))
	if perzentil <= 0 {
		return VMAFWerte{Wert: mittel, Mittel: mittel}, nil
	}
	return VMAFWerte{Wert: unteresPerzentil(werte, perzentil), Mittel: mittel}, nil
}

// unteresPerzentil liefert den Wert, unter dem prozent Prozent der Werte
// liegen. Zwischen zwei Rangplätzen wird gerade verbunden (wie die
// Tabellenkalkulation mit QUANTIL bzw. numpy ab Werk) — so springt das
// Ergebnis nicht, wenn ein Bild mehr oder weniger gewertet wird. Die
// Reihenfolge von werte bleibt unverändert.
func unteresPerzentil(werte []float64, prozent int) float64 {
	sortiert := slices.Clone(werte)
	slices.Sort(sortiert)

	rang := float64(prozent) / 100 * float64(len(sortiert)-1)
	unten := int(rang)
	oben := min(unten+1, len(sortiert)-1)
	return sortiert[unten] + (sortiert[oben]-sortiert[unten])*(rang-float64(unten))
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
// protokoll ist ein Dateiname ohne Pfad (siehe VMAFMessen).
func vmafFilter(breite, hoehe, threads int, protokoll string) string {
	const nachNummer = "settb=1/25,setpts=N" // Zeitbasis beliebig, Hauptsache gleich
	const jedesNteBild = 4

	vorbereitung := nachNummer
	if zielBreite, zielHoehe, vergroessern := vmafMessgroesse(breite, hoehe); vergroessern {
		vorbereitung += fmt.Sprintf(",scale=%d:%d:flags=bicubic", zielBreite, zielHoehe)
	}
	return fmt.Sprintf(
		"[0:v]%s[probe];[1:v]%s[ref];[probe][ref]libvmaf=n_threads=%d:n_subsample=%d:log_fmt=json:log_path=%s",
		vorbereitung, vorbereitung, threads, jedesNteBild, protokoll)
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
