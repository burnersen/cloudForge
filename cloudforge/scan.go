package main

// Dateisuche und Vorauswahl. Hier wird entschieden, welche Datei überhaupt
// angefasst wird — bevor irgendetwas heruntergeladen oder gerechnet wird.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// videoEndungen sind die Formate, die das Programm annimmt. Der Nutzer zieht
// Ordner "aller Art" hinein, deshalb bewusst breit gefasst.
var videoEndungen = map[string]bool{
	".mp4": true, ".mkv": true, ".avi": true, ".mov": true, ".m4v": true,
	".ts": true, ".m2ts": true, ".mts": true, ".wmv": true, ".flv": true,
	".webm": true, ".mpg": true, ".mpeg": true, ".vob": true, ".divx": true,
	".ogv": true, ".rmvb": true, ".asf": true, ".3gp": true, ".mxf": true,
}

// zielSuffix hängt vor der Endung, damit ein Ergebnis auf den ersten Blick
// erkennbar ist: "film.mp4" wird zu "film.av1.mkv".
const zielSuffix = ".av1"
const zielEndung = ".mkv"

// ergebnisSuffixe kennzeichnen alles, was CloudForge selbst erzeugt: ".av1"
// beim Umwandeln, die übrigen beim verlustfreien Umpacken (seit 0.11.0, wie
// NVENCForge: dort heissen sie skipInputSuffixes). Eine Datei mit einer dieser
// Endungen vor ".mkv" wird nie wieder angefasst — so erkennt CloudForge seine
// Ergebnisse, auch wenn sie längst nicht mehr im output-Ordner liegen.
var ergebnisSuffixe = []string{".av1", ".h264", ".h265", ".remux"}

// istErgebnisDatei sagt, ob der Name nach einem Ergebnis von CloudForge
// aussieht ("film.av1.mkv", "film.h264.mkv", ...).
func istErgebnisDatei(pfad string) bool {
	name := strings.ToLower(filepath.Base(pfad))
	if !strings.HasSuffix(name, zielEndung) {
		return false
	}
	ohneEndung := strings.TrimSuffix(name, zielEndung)
	for _, suffix := range ergebnisSuffixe {
		if strings.HasSuffix(ohneEndung, suffix) {
			return true
		}
	}
	return false
}

// umpackSuffix benennt ein umgepacktes Ergebnis nach dem Codec, den es
// unverändert behält — genau wie NVENCForge (remuxSuffix).
func umpackSuffix(videoCodec string) string {
	switch strings.ToLower(strings.TrimSpace(videoCodec)) {
	case "hevc", "h265":
		return ".h265"
	case "h264", "avc":
		return ".h264"
	case "av1":
		return ".av1"
	default:
		return ".remux"
	}
}

// Grund, warum eine Datei nicht angefasst wird.
type Uebersprungen string

const (
	NichtUebersprungen Uebersprungen = ""
	SchonAV1           Uebersprungen = "ist bereits AV1"
	SchonSchlank       Uebersprungen = "Bitrate schon zu niedrig, nichts zu holen"
	ErgebnisDa         Uebersprungen = "Ergebnis liegt bereits im Ausgabeordner"
	KeineVideospur     Uebersprungen = "keine brauchbare Videospur"
	ZuKurz             Uebersprungen = "zu kurz zum Messen"
	NichtMehrDa        Uebersprungen = "Datei ist nicht mehr da"
	ImOriginalOrdner   Uebersprungen = "liegt im Ordner fuer Originale, wurde schon umgewandelt"
)

// Kandidat ist eine gefundene Datei samt Urteil.
type Kandidat struct {
	Info             VideoInfo
	Grund            Uebersprungen
	Fehler           error
	ZielPfad         string // wohin das Ergebnis in der Cloud soll
	SparSchaetzungMB float64
}

// Lohnt sagt, ob die Datei umgewandelt wird.
func (k Kandidat) Lohnt() bool { return k.Grund == NichtUebersprungen && k.Fehler == nil }

// NurUmpacken sagt, ob die Datei statt umgewandelt nur verlustfrei nach MKV
// umgepackt wird (seit 0.11.0, wie NVENCForge): Sie ist schon AV1, oder ihre
// Bitrate liegt so tief, dass ein Umwandeln nichts mehr herausholt.
func (k Kandidat) NurUmpacken() bool {
	return k.Fehler == nil && (k.Grund == SchonAV1 || k.Grund == SchonSchlank)
}

// mindestDauerSek: kürzere Dateien lassen sich nicht sinnvoll in Fenstern
// messen, für die lohnt der Aufwand ohnehin nicht.
const mindestDauerSek = 30

// VideoDateienSuchen geht die übergebenen Pfade durch. Ein Pfad darf eine
// einzelne Datei oder ein Ordner sein; Ordner werden vollständig mit allen
// Unterordnern durchsucht — so wie der Nutzer sie hineinzieht.
//
// Ausgabe- und Original-Ordner werden dabei übersprungen, sonst würde das
// Programm seine eigenen Ergebnisse erneut verarbeiten.
func VideoDateienSuchen(pfade []string, e Einstellungen) ([]string, error) {
	gesehen := make(map[string]bool)
	var gefunden []string

	for _, pfad := range pfade {
		zustand, err := os.Stat(pfad)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", pfad, err)
		}

		if !zustand.IsDir() {
			if istVideodatei(pfad) && !istErgebnisDatei(pfad) {
				if voll, err := filepath.Abs(pfad); err == nil && !gesehen[voll] {
					gesehen[voll] = true
					gefunden = append(gefunden, voll)
				}
			}
			continue
		}

		err = filepath.WalkDir(pfad, func(p string, eintrag fs.DirEntry, err error) error {
			if err != nil {
				// Ein unlesbarer Unterordner darf den ganzen Lauf nicht kippen.
				return nil
			}
			if eintrag.IsDir() {
				if eigenerOrdner(eintrag.Name(), e) {
					return filepath.SkipDir
				}
				return nil
			}
			if !istVideodatei(p) || istErgebnisDatei(p) {
				return nil
			}
			voll, err := filepath.Abs(p)
			if err != nil || gesehen[voll] {
				return nil
			}
			gesehen[voll] = true
			gefunden = append(gefunden, voll)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", pfad, err)
		}
	}

	sort.Strings(gefunden)
	return gefunden, nil
}

func istVideodatei(pfad string) bool {
	return videoEndungen[strings.ToLower(filepath.Ext(pfad))]
}

// eigenerOrdner erkennt die vom Programm selbst angelegten Ordner.
func eigenerOrdner(name string, e Einstellungen) bool {
	return strings.EqualFold(name, e.AusgabeOrdnerName) ||
		strings.EqualFold(name, e.OriginalOrdnerName)
}

// ZielPfadFuer liefert den Ort des Ergebnisses: ein Unterordner neben der
// Quelldatei, so wie beim Windows-Pendant.
func ZielPfadFuer(quellPfad string, e Einstellungen) string {
	verzeichnis := filepath.Dir(quellPfad)
	name := filepath.Base(quellPfad)
	ohneEndung := strings.TrimSuffix(name, filepath.Ext(name))
	return filepath.Join(verzeichnis, e.AusgabeOrdnerName, ohneEndung+zielSuffix+zielEndung)
}

// UmpackPfadFuer liefert den Ort eines umgepackten Ergebnisses:
// "film.mp4" mit H.264 wird zu "output/film.h264.mkv".
func UmpackPfadFuer(quellPfad, videoCodec string, e Einstellungen) string {
	verzeichnis := filepath.Dir(quellPfad)
	name := filepath.Base(quellPfad)
	ohneEndung := strings.TrimSuffix(name, filepath.Ext(name))
	return filepath.Join(verzeichnis, e.AusgabeOrdnerName, ohneEndung+umpackSuffix(videoCodec)+zielEndung)
}

// VorhandenesErgebnis liefert den Pfad eines Ergebnisses, das für diese
// Quelle schon im output-Ordner liegt — umgewandelt oder umgepackt, egal mit
// welcher Endung. Leer, wenn es keines gibt.
//
// Das ist seit 0.11.0 die EINZIGE Erinnerung daran, dass eine Datei erledigt
// ist (Nutzerwunsch 26.09.2026: kein "Logbuch", die Ordner entscheiden — wie
// in NVENCForge). Wer eine Datei nochmal umwandeln will, benennt ihr Ergebnis
// um oder löscht es.
func VorhandenesErgebnis(quellPfad string, e Einstellungen) string {
	verzeichnis := filepath.Dir(quellPfad)
	name := filepath.Base(quellPfad)
	ohneEndung := strings.TrimSuffix(name, filepath.Ext(name))
	for _, suffix := range ergebnisSuffixe {
		kandidat := filepath.Join(verzeichnis, e.AusgabeOrdnerName, ohneEndung+suffix+zielEndung)
		if _, err := os.Stat(kandidat); err == nil {
			return kandidat
		}
	}
	return ""
}

// OriginalPfadFuer liefert den Ort, an den das Original wandert.
func OriginalPfadFuer(quellPfad string, e Einstellungen) string {
	return filepath.Join(filepath.Dir(quellPfad), e.OriginalOrdnerName, filepath.Base(quellPfad))
}

// KandidatPruefen liest die Kopfdaten und fällt das Urteil, ob sich die Datei
// lohnt. Es wird nichts heruntergeladen und nichts verändert.
func KandidatPruefen(pfad string, e Einstellungen) Kandidat {
	k := Kandidat{ZielPfad: ZielPfadFuer(pfad, e), Info: VideoInfo{Pfad: pfad}}

	// Zwischen Hineinziehen und Drankommen kann Zeit vergehen — etwa wenn ein
	// anderes Fenster vorher dran war und die Datei schon verschoben hat.
	if _, err := os.Stat(pfad); os.IsNotExist(err) {
		k.Grund = NichtMehrDa
		return k
	}
	// Eine Datei aus dem originals-Ordner ist schon verarbeitet worden.
	// Direkt hineingezogen würde sie sonst ein zweites Mal umgewandelt.
	if strings.EqualFold(filepath.Base(filepath.Dir(pfad)), e.OriginalOrdnerName) {
		k.Grund = ImOriginalOrdner
		return k
	}

	info, err := KopfdatenLesen(e.FFprobePfad, pfad)
	if err != nil {
		k.Info = VideoInfo{Pfad: pfad}
		k.Fehler = err
		k.Grund = KeineVideospur
		return k
	}
	k.Info = info

	if VorhandenesErgebnis(pfad, e) != "" {
		k.Grund = ErgebnisDa
		return k
	}
	if strings.EqualFold(info.VideoCodec, "av1") {
		k.Grund = SchonAV1
		return k
	}
	if info.DauerSek < mindestDauerSek {
		k.Grund = ZuKurz
		return k
	}

	boden := BitratenBodenKbps(info.Hoehe, info.FPS)
	if info.VideoBitrateKbps <= boden {
		k.Grund = SchonSchlank
		return k
	}

	k.SparSchaetzungMB = sparSchaetzungMB(info, boden)
	return k
}

// BitratenBodenKbps ist die Bitrate, unterhalb derer sich das Umwandeln nicht
// mehr lohnt: AV1 bräuchte für dieselbe Qualität selbst etwa so viel.
//
// Die Werte sind aus der Messreihe vom 21.09.2026 abgeleitet (1080p50 mit
// 12 Mbit/s landete bei VMAF 94 auf rund 2,4 Mbit/s) und für die anderen
// Auflösungen nach Bildfläche fortgeschrieben — also GESCHÄTZT, nicht je
// Auflösung gemessen. Lieber zu vorsichtig: eine übersprungene Datei kostet
// nur etwas Ersparnis, eine unnötig umgewandelte kostet Stunden Rechenzeit.
//
// ⚠️ Nur ein grober Vorfilter, geschätzt für Ziel 94. Bei Ziel 98 liess er am
// 25.09.2026 eine 2,6-Mbit/s-Quelle durch, die grösser geworden wäre. Die
// eigentliche Entscheidung fällt seit 0.8.0 nach der Qualitätsmessung aus den
// Messproben (Kosten-Deckel + mindestErsparnisProzent, deckel.go).
func BitratenBodenKbps(hoehe int, fps float64) int {
	var boden int
	switch {
	case hoehe <= 480:
		boden = 400
	case hoehe <= 576:
		boden = 550
	case hoehe <= 720:
		boden = 800
	case hoehe <= 1080:
		boden = 1500
	case hoehe <= 1440:
		boden = 2800
	default:
		boden = 5000
	}

	// Hohe Bildraten brauchen mehr Bits für dasselbe Bild.
	if fps > 40 {
		boden = boden * 3 / 2
	}
	return boden
}

// sparSchaetzungMB schätzt grob, wie viel Platz die Datei bringen könnte.
// Dient nur dazu, die Warteschlange nach Ertrag zu sortieren — es ist
// ausdrücklich keine Zusage.
func sparSchaetzungMB(info VideoInfo, bodenKbps int) float64 {
	zielKbps := float64(info.VideoBitrateKbps) * erwarteteRestgroesse(info.VideoBitrateKbps, bodenKbps)
	if zielKbps < float64(bodenKbps) {
		zielKbps = float64(bodenKbps)
	}

	gespartKbit := (float64(info.VideoBitrateKbps) - zielKbps) * info.DauerSek
	gespartMB := gespartKbit * 1000 / 8 / 1024 / 1024
	if gespartMB < 0 {
		return 0
	}
	return gespartMB
}

// erwarteteRestgroesse liefert, welcher Anteil der Quellbitrate nach der
// Umwandlung voraussichtlich übrig bleibt. Fette Quellen schrumpfen stärker
// als schlanke — belegt am 21.09.2026 für den fetten Fall (12 Mbit/s auf
// rund 20 %), der schlanke Fall ist eine vorsichtige Annahme.
func erwarteteRestgroesse(quelleKbps, bodenKbps int) float64 {
	verhaeltnis := float64(quelleKbps) / float64(bodenKbps)
	switch {
	case verhaeltnis >= 6: // sehr fett, z. B. 12 Mbit/s bei 1080p
		return 0.20
	case verhaeltnis >= 3:
		return 0.35
	case verhaeltnis >= 2:
		return 0.50
	default: // schon nah am Boden
		return 0.70
	}
}
