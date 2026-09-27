package main

// Auswertung der Kopfdaten einer Videodatei per ffprobe. Es werden nur die
// Kopfdaten gelesen, nicht der Inhalt — das ist auch über einen Cloud-Ordner
// billig und lädt die Datei nicht herunter.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Wie lange das Lesen der Kopfdaten höchstens dauern darf. Über einen
// Cloud-Ordner kann ffprobe sonst bei einer Netzstörung ewig hängen.
const kopfdatenZeitlimit = 90 * time.Second

// Tonspur beschreibt eine einzelne Tonspur der Datei.
type Tonspur struct {
	Index      int
	Codec      string
	Kanaele    int
	BitrateBps int
	Sprache    string
}

// BitrateKbps liefert die Bitrate in kbit/s, 0 wenn unbekannt.
func (t Tonspur) BitrateKbps() int { return t.BitrateBps / 1000 }

// VideoInfo sind alle Kopfdaten, die das Programm für seine Entscheidungen
// braucht.
type VideoInfo struct {
	Pfad         string
	GroesseBytes int64
	DauerSek     float64

	VideoCodec string
	Breite     int
	Hoehe      int
	FPS        float64

	// Bitrate der Videospur. Steht sie nicht in der Datei (bei MKV häufig),
	// wird sie aus Dateigröße und Dauer geschätzt; GeschaetzteBitrate sagt es.
	VideoBitrateKbps   int
	GeschaetzteBitrate bool

	Tonspuren        []Tonspur
	UntertitelAnzahl int
}

// GesamtBitrateKbps ist die Bitrate der ganzen Datei.
func (v VideoInfo) GesamtBitrateKbps() int {
	if v.DauerSek <= 0 {
		return 0
	}
	return int(float64(v.GroesseBytes) * 8 / v.DauerSek / 1000)
}

// GroesseMB liefert die Dateigröße in Megabyte.
func (v VideoInfo) GroesseMB() float64 {
	return float64(v.GroesseBytes) / 1024 / 1024
}

// --- Aufbau der ffprobe-Ausgabe (nur die Felder, die gebraucht werden) ---

type probeAusgabe struct {
	Streams []probeStream `json:"streams"`
	Format  probeFormat   `json:"format"`
}

type probeStream struct {
	Index        int               `json:"index"`
	CodecType    string            `json:"codec_type"`
	CodecName    string            `json:"codec_name"`
	Width        int               `json:"width"`
	Height       int               `json:"height"`
	Channels     int               `json:"channels"`
	BitRate      string            `json:"bit_rate"`
	AvgFrameRate string            `json:"avg_frame_rate"`
	Tags         map[string]string `json:"tags"`
}

type probeFormat struct {
	Duration string `json:"duration"`
	Size     string `json:"size"`
	BitRate  string `json:"bit_rate"`
}

// KopfdatenLesen ruft ffprobe auf und wertet das Ergebnis aus.
func KopfdatenLesen(ffprobePfad, dateiPfad string) (VideoInfo, error) {
	ctx, abbrechen := context.WithTimeout(context.Background(), kopfdatenZeitlimit)
	defer abbrechen()

	befehl := exec.CommandContext(ctx, ffprobePfad,
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		dateiPfad,
	)

	ausgabe, err := befehl.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return VideoInfo{}, fmt.Errorf("ffprobe hat nach %v nicht geantwortet (Netzstoerung?)", kopfdatenZeitlimit)
	}
	if err != nil {
		return VideoInfo{}, fmt.Errorf("ffprobe fehlgeschlagen: %w", fehlerText(err))
	}

	var roh probeAusgabe
	if err := json.Unmarshal(ausgabe, &roh); err != nil {
		return VideoInfo{}, fmt.Errorf("ffprobe-Ausgabe unverstaendlich: %w", err)
	}

	return ausProbe(dateiPfad, roh)
}

// vorlaufPakete: So viele Videopakete vom Anfang reichen, um versteckten
// Vorlauf zu erkennen — er steht immer ganz vorn und reicht höchstens bis zum
// nächsten Schlüsselbild.
const vorlaufPakete = 30

// VorlaufVersteckt sagt, ob die Videospur mit Bildern beginnt, die die Datei
// nur ausblendet: Eine MP4 mit Edit-Liste, typisch für Dateien, die ohne
// Neukodieren geschnitten wurden, muss vom letzten Schlüsselbild VOR dem
// Schnitt an speichern und markiert die Bilder davor als "verwerfen".
//
// Wichtig fürs Umpacken (seit 0.11.0): MKV kennt dieses Ausblenden nicht, die
// versteckten Bilder kämen wieder zum Vorschein, und Bild und Ton könnten
// gegeneinander verrutschen. Gemessen am 26.09.2026 an einem so geschnittenen
// Stück: 4 s Vorlauf, umgepackt 49,1 statt 45,1 s — bei einem langen Film läge
// das sogar innerhalb der erlaubten Abweichung der Spieldauer (0,5 %).
// Beim Umwandeln stört es nicht: das Dekodieren wendet die Edit-Liste an.
func VorlaufVersteckt(ffprobePfad, dateiPfad string) (bool, error) {
	ctx, abbrechen := context.WithTimeout(context.Background(), kopfdatenZeitlimit)
	defer abbrechen()

	befehl := exec.CommandContext(ctx, ffprobePfad,
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "packet=flags",
		"-read_intervals", "%+#"+strconv.Itoa(vorlaufPakete),
		"-of", "csv=p=0",
		dateiPfad,
	)
	ausgabe, err := befehl.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return false, fmt.Errorf("ffprobe hat nach %v nicht geantwortet (Netzstoerung?)", kopfdatenZeitlimit)
	}
	if err != nil {
		return false, fmt.Errorf("Anfang der Videospur nicht lesbar: %w", fehlerText(err))
	}
	return vorlaufInFlags(string(ausgabe)), nil
}

// vorlaufInFlags wertet die Paket-Kennzeichen aus ("K_" Schlüsselbild,
// "D" verwerfen, "C" beschädigt, "_" nichts) — als eigene Funktion, damit sie
// ohne ffprobe geprüft werden kann.
func vorlaufInFlags(ausgabe string) bool {
	for _, zeile := range strings.Split(ausgabe, "\n") {
		if strings.Contains(zeile, "D") {
			return true
		}
	}
	return false
}

// ausProbe baut aus der Rohausgabe die VideoInfo. Als eigene Funktion, damit
// sie ohne ffprobe geprüft werden kann.
func ausProbe(dateiPfad string, roh probeAusgabe) (VideoInfo, error) {
	info := VideoInfo{Pfad: dateiPfad}

	info.DauerSek, _ = strconv.ParseFloat(roh.Format.Duration, 64)
	info.GroesseBytes, _ = strconv.ParseInt(roh.Format.Size, 10, 64)

	videoGefunden := false
	for _, s := range roh.Streams {
		switch s.CodecType {
		case "video":
			// Nur die erste echte Videospur zählt. Eingebettete Vorschaubilder
			// melden sich ebenfalls als Video, haben aber keine Bildrate.
			if videoGefunden || bildrateLesen(s.AvgFrameRate) == 0 {
				continue
			}
			videoGefunden = true
			info.VideoCodec = s.CodecName
			info.Breite = s.Width
			info.Hoehe = s.Height
			info.FPS = bildrateLesen(s.AvgFrameRate)
			if bps, err := strconv.Atoi(s.BitRate); err == nil && bps > 0 {
				info.VideoBitrateKbps = bps / 1000
			}

		case "audio":
			bps, _ := strconv.Atoi(s.BitRate)
			info.Tonspuren = append(info.Tonspuren, Tonspur{
				Index:      s.Index,
				Codec:      s.CodecName,
				Kanaele:    s.Channels,
				BitrateBps: bps,
				Sprache:    s.Tags["language"],
			})

		case "subtitle":
			info.UntertitelAnzahl++
		}
	}

	if !videoGefunden {
		return info, fmt.Errorf("keine Videospur gefunden")
	}
	if info.DauerSek <= 0 {
		return info, fmt.Errorf("Spieldauer unbekannt oder null")
	}

	// Fehlt die Bitrate der Videospur, aus dem Rest schätzen: Gesamtgröße
	// abzüglich dessen, was der Ton belegt.
	if info.VideoBitrateKbps == 0 {
		info.VideoBitrateKbps = videoBitrateSchaetzen(info)
		info.GeschaetzteBitrate = true
	}

	return info, nil
}

// videoBitrateSchaetzen zieht die bekannten Tonbitraten von der Gesamtbitrate
// ab. Unbekannte Tonspuren werden mit einem vorsichtigen Wert angesetzt,
// damit die Schätzung eher zu hoch als zu niedrig ausfällt.
func videoBitrateSchaetzen(info VideoInfo) int {
	const unbekannteTonspurKbps = 192

	gesamt := info.GesamtBitrateKbps()
	tonSumme := 0
	for _, t := range info.Tonspuren {
		if kbps := t.BitrateKbps(); kbps > 0 {
			tonSumme += kbps
		} else {
			tonSumme += unbekannteTonspurKbps
		}
	}

	geschaetzt := gesamt - tonSumme
	if geschaetzt < 0 {
		return gesamt // Tonschätzung war zu hoch, lieber die Gesamtbitrate melden
	}
	return geschaetzt
}

// bildrateLesen wertet "50/1" oder "30000/1001" aus.
//
// Bewusst avg_frame_rate und nicht r_frame_rate: r_frame_rate ist nur die
// Taktrate des Containers und bei variabler Bildrate ein Vielfaches der
// echten Rate.
func bildrateLesen(bruch string) float64 {
	zaehlerText, nennerText, ok := strings.Cut(bruch, "/")
	if !ok {
		return 0
	}
	zaehler, err1 := strconv.ParseFloat(zaehlerText, 64)
	nenner, err2 := strconv.ParseFloat(nennerText, 64)
	if err1 != nil || err2 != nil || nenner == 0 {
		return 0
	}
	rate := zaehler / nenner
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate <= 0 {
		return 0
	}
	return rate
}

// fehlerText hängt die Fehlerausgabe des Programms an, sonst steht da nur
// "exit status 1" und niemand weiss, was los war.
func fehlerText(err error) error {
	var exitFehler *exec.ExitError
	if errors.As(err, &exitFehler) && len(exitFehler.Stderr) > 0 {
		meldung := strings.TrimSpace(string(exitFehler.Stderr))
		if len(meldung) > 300 {
			meldung = meldung[:300] + " …"
		}
		return fmt.Errorf("%v: %s", err, meldung)
	}
	return err
}
