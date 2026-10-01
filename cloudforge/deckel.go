// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

// Der Kosten-Deckel — übernommen aus NVENCForge (autoCQMaxSourcePercent).
//
// Warum es ihn braucht: Auto-CQ verfolgt das Qualitätsziel ohne Rücksicht auf
// die Grösse. Bei einer schon stark komprimierten Quelle ist das teuer —
// gemessen am 25.09.2026 an einer 1080p-Quelle mit 2,6 Mbit/s: für VMAF 97
// brauchte AV1 118 % der Quelle, die Datei wäre GRÖSSER geworden. Der Deckel
// begrenzt, wie viel von der Quelle das Ergebnis kosten darf; bei fetten
// Quellen (12 Mbit/s: 42 % bei VMAF 98) greift er gar nicht.
//
// Gemessen wird wie bei NVENCForge an den Messstellen selbst, nicht am
// Durchschnitt der Datei: Die Messproben werden mit genau dem Stück der Quelle
// verglichen, aus dem sie entstanden sind.

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
)

// QuelleFensterBytes zählt, wie viele Bytes die Videospur der Quelle in den
// Messfenstern belegt. 0 heisst: unbekannt, dann gibt es keinen Deckel.
func QuelleFensterBytes(ctx context.Context, ffprobePfad, pfad string, fenster []Fenster) (int64, error) {
	beginn, err := dateiBeginn(ctx, ffprobePfad, pfad)
	if err != nil {
		return 0, err
	}

	befehl := exec.CommandContext(ctx, ffprobePfad,
		"-v", "error",
		"-select_streams", ersteFilmspurAuswahl,
		"-read_intervals", leseBereiche(fenster, beginn),
		"-show_entries", "format=start_time:packet=pts_time,dts_time,size",
		"-of", "csv=p=1",
		pfad)
	ausgabe, err := befehl.Output()
	if ctx.Err() != nil {
		return 0, ErrAbgebrochen
	}
	if err != nil {
		return 0, fmt.Errorf("Quellgroesse an den Messstellen nicht lesbar: %w", fehlerText(err))
	}
	return fensterBytesAusPaketen(string(ausgabe), fenster), nil
}

// leseBereiche sagt ffprobe, welche Stellen es lesen soll: jedes Fenster mit
// etwas Rand davor und dahinter, genau abgegrenzt wird danach beim Zählen.
//
// Das Ende steht ABSOLUT da ("START%ENDE"). Bis 0.11.1 hiess es "START%+DAUER",
// und ffprobe rechnet +DAUER nicht ab START, sondern ab dem Schlüsselbild VOR
// START, auf das es springt. Liegt das weit davor (bei Downloads oft 5-10 s),
// hört das Lesen mitten im Fenster auf. Gemessen 26.09.2026 an einer 38-Min-
// Datei: 15,8 statt 24,3 MB gezählt, der Deckel hielt 32 % für 50 % und senkte
// die Qualität grundlos auf VMAF 92,5.
//
// Die Zeiten sind Zeitstempel der Datei, deshalb wird ihr Beginn aufgeschlagen:
// ffmpeg zählt "-ss" ab dem Beginn, ffprobe nicht.
func leseBereiche(fenster []Fenster, beginn float64) string {
	const rand = 3.0
	bereiche := make([]string, len(fenster))
	for i, f := range fenster {
		von := math.Max(beginn+f.StartSek-rand, 0)
		bis := beginn + f.StartSek + f.LaengeSek + rand
		bereiche[i] = zahlText(von) + "%" + zahlText(bis)
	}
	return strings.Join(bereiche, ",")
}

// dateiBeginn liest den Zeitstempel, bei dem die Datei beginnt — meist 0, bei
// Mitschnitten aus dem Fernsehen (TS) oft Minuten oder Stunden. Steht er nicht
// in der Datei, gilt 0 wie bei fensterBytesAusPaketen.
func dateiBeginn(ctx context.Context, ffprobePfad, pfad string) (float64, error) {
	befehl := exec.CommandContext(ctx, ffprobePfad,
		"-v", "error",
		"-show_entries", "format=start_time",
		"-of", "csv=p=0",
		pfad)
	ausgabe, err := befehl.Output()
	if ctx.Err() != nil {
		return 0, ErrAbgebrochen
	}
	if err != nil {
		return 0, fmt.Errorf("Beginn der Quelle nicht lesbar: %w", fehlerText(err))
	}
	beginn, err := strconv.ParseFloat(strings.TrimSpace(string(ausgabe)), 64)
	if err != nil {
		return 0, nil // "N/A"
	}
	return beginn, nil
}

// fensterBytesAusPaketen summiert die Paketgrössen, deren Zeitstempel in einem
// der Fenster liegt. Die Zeilen sehen so aus:
//
//	packet,612.040000,611.960000,48213
//	format,1.400000
//
// ffmpeg zählt "-ss" ab dem Beginn der Datei (format start_time), die Pakete
// tragen absolute Zeiten — deshalb wird der Beginn aufgeschlagen.
func fensterBytesAusPaketen(ausgabe string, fenster []Fenster) int64 {
	type paket struct {
		zeit  float64
		bytes int64
	}
	var pakete []paket
	beginn := 0.0

	for _, zeile := range strings.Split(ausgabe, "\n") {
		felder := strings.Split(strings.TrimSpace(zeile), ",")
		switch {
		case len(felder) == 2 && felder[0] == "format":
			if wert, err := strconv.ParseFloat(felder[1], 64); err == nil {
				beginn = wert
			}
		case len(felder) == 4 && felder[0] == "packet":
			zeit, err := strconv.ParseFloat(felder[1], 64)
			if err != nil { // pts fehlt ("N/A") - dann die Dekodierzeit
				if zeit, err = strconv.ParseFloat(felder[2], 64); err != nil {
					continue
				}
			}
			bytes, err := strconv.ParseInt(felder[3], 10, 64)
			if err != nil || bytes <= 0 {
				continue
			}
			pakete = append(pakete, paket{zeit, bytes})
		}
	}

	// Liegen Stellen dicht beieinander, springt ffprobe für jede an das
	// Schlüsselbild davor, und dieselben Pakete kommen mehrfach — gefunden am
	// 27.09.2026 mit 10 Stellen in einem 100-s-Film: doppelt so viel Quelle
	// gezählt, die Vorhersage viel zu günstig. Jedes Paket zählt nur einmal.
	gezaehlt := make(map[paket]bool, len(pakete))
	var summe int64
	for _, p := range pakete {
		if gezaehlt[p] {
			continue
		}
		gezaehlt[p] = true
		for _, f := range fenster {
			von := beginn + f.StartSek
			if p.zeit >= von && p.zeit < von+f.LaengeSek {
				summe += p.bytes
				break
			}
		}
	}
	return summe
}

// anteil sagt, wie gross eine Messprobe im Verhältnis zur Quelle an denselben
// Stellen ist: 0,5 heisst halb so gross. 0 = unbekannt.
func (s *crfSuche) anteil(m Messung) float64 {
	if s.quelleBytes <= 0 || m.Bytes <= 0 {
		return 0
	}
	return float64(m.Bytes) / float64(s.quelleBytes)
}

// deckeln wendet den Kosten-Deckel auf die Qualitätswahl an. Kostet sie mehr
// als erlaubt, wird der CRF gesucht, der gerade noch unter dem Deckel bleibt —
// die Qualität sinkt dann unter das Ziel, das ist der Sinn des Deckels.
//
// Ist der Deckel selbst beim sparsamsten erlaubten CRF nicht einzuhalten,
// bleibt die Qualitätswahl stehen (wie NVENCForge ab 1.27.0). Ob sich die
// Datei dann überhaupt lohnt, entscheidet danach mindestErsparnisProzent.
func (s *crfSuche) deckeln(erg AutoCQErgebnis) (AutoCQErgebnis, error) {
	gewaehlt, err := s.messen(erg.CRF) // schon gemessen, kommt aus dem Speicher
	if err != nil {
		return erg, nil
	}
	erg.AnteilQuelle = s.anteil(gewaehlt)

	deckel := s.e.KostenDeckelProzent / 100
	if deckel <= 0 || erg.AnteilQuelle <= 0 || erg.AnteilQuelle <= deckel {
		return erg, nil
	}

	treffer, err := s.deckelSuchen(deckel)
	if err != nil {
		return AutoCQErgebnis{}, err
	}
	erg.Messungen = s.messungen

	if s.anteil(treffer) > deckel {
		erg.Hinweis = fmt.Sprintf("Deckel %.0f %% nicht einhaltbar (selbst CRF %d braucht %.0f %% der Quelle) - "+
			"die Qualitaetswahl bleibt.", s.e.KostenDeckelProzent, treffer.CRF, s.anteil(treffer)*100)
		erg.DeckelNichtEinhaltbar = true
		return erg, nil
	}

	erg.Hinweis = fmt.Sprintf("Gedeckelt: Ziel %s haette %.0f %% der Quelle gekostet, erlaubt sind %.0f %%. "+
		"Stattdessen CRF %d mit %s.", vmafZielText(s.e), erg.AnteilQuelle*100,
		s.e.KostenDeckelProzent, treffer.CRF, vmafText(treffer.Wert, treffer.Mittel))
	erg.CRF, erg.ErwarteterVMAF = treffer.CRF, treffer.VMAF
	erg.AnteilQuelle = s.anteil(treffer)
	erg.Gedeckelt = true
	return erg, nil
}

// deckelSprung spart bei offensichtlich dünnen Quellen die Qualitätssuche.
//
// Liegt schon der sparsamere Anker UNTER dem Ziel und ÜBER dem Deckel, landet
// jede Qualitätswahl bei einem niedrigeren CRF — also bei einer noch grösseren
// Datei —, und der Deckel greift sicher. Dann wird gleich der Deckel-CRF
// gesucht. Anlass: Am 25.09.2026 brauchte ein 90-s-Stück einer 2,6-Mbit/s-
// Quelle zwölf Proben und 5 Minuten für ein Ziel, das der Deckel danach
// ohnehin verwarf.
//
// gesprungen = false heisst: der normale Weg gilt — weil der Deckel aus ist,
// die Quelle nicht dünn genug ist oder der Deckel gar nicht einzuhalten ist.
func (s *crfSuche) deckelSprung() (erg AutoCQErgebnis, gesprungen bool, err error) {
	deckel := s.e.KostenDeckelProzent / 100
	if deckel <= 0 || s.quelleBytes <= 0 {
		return AutoCQErgebnis{}, false, nil
	}
	niedrig, err := s.messen(s.e.AnkerNiedrig)
	if err != nil {
		return AutoCQErgebnis{}, false, err
	}
	hoch, err := s.messen(s.e.AnkerHoch)
	if err != nil {
		return AutoCQErgebnis{}, false, err
	}
	if hoch.VMAF >= s.ziel() || s.anteil(hoch) <= deckel {
		return AutoCQErgebnis{}, false, nil
	}

	treffer, err := s.deckelSuchen(deckel)
	if err != nil {
		return AutoCQErgebnis{}, false, err
	}
	if s.anteil(treffer) > deckel {
		return AutoCQErgebnis{}, false, nil // nicht einzuhalten: die Qualitätswahl entscheidet
	}

	return AutoCQErgebnis{
		CRF:            treffer.CRF,
		ErwarteterVMAF: treffer.VMAF,
		ZielErreichbar: niedrig.VMAF >= s.ziel(),
		AnteilQuelle:   s.anteil(treffer),
		Gedeckelt:      true,
		Hinweis: fmt.Sprintf("Gedeckelt: schon CRF %d kostet %.0f %% der Quelle und bleibt mit %s unter "+
			"dem Ziel %s, erlaubt sind %.0f %%. Stattdessen CRF %d mit %s.",
			hoch.CRF, s.anteil(hoch)*100, vmafText(hoch.Wert, hoch.Mittel), vmafZielText(s.e),
			s.e.KostenDeckelProzent, treffer.CRF, vmafText(treffer.Wert, treffer.Mittel)),
		Messungen: s.messungen,
	}, true, nil
}

// deckelSuchen findet den niedrigsten CRF (also die beste Qualität), dessen
// Probe höchstens den Deckel-Anteil der Quelle kostet.
//
// Die Grösse fällt mit dem CRF ungefähr exponentiell — deshalb wird im
// Logarithmus gerechnet: erst von den zwei sparsamsten Messungen aus
// hochgerechnet, sobald ein Punkt unter dem Deckel liegt, dazwischen
// eingegrenzt. Aufgerundet wird zur sparsameren Seite, damit der erste
// Versuch eher unter dem Deckel landet. Grenzen wie in einschachteln: jede
// Runde verkleinert den Bereich, und die Rundenzahl ist beschränkt.
func (s *crfSuche) deckelSuchen(deckel float64) (Messung, error) {
	zielBytes := deckel * float64(s.quelleBytes)

	for runde := 0; runde < maxMessungen; runde++ {
		zuTeuer, bezahlbar, gefunden := s.deckelKlammer(deckel)
		if gefunden && bezahlbar.CRF-zuTeuer.CRF <= 1 {
			return bezahlbar, nil
		}

		var naechster int
		if gefunden {
			naechster = crfFuerBytes(zuTeuer, bezahlbar, zielBytes)
			naechster = rundeAuf(naechster, zuTeuer.CRF+1, bezahlbar.CRF-1)
		} else {
			if zuTeuer.CRF >= s.e.CRFMax {
				return zuTeuer, nil // auch der sparsamste erlaubte CRF ist zu teuer
			}
			naechster = crfFuerBytes(s.zweitSparsamste(zuTeuer), zuTeuer, zielBytes)
			naechster = rundeAuf(naechster, zuTeuer.CRF+1, s.e.CRFMax)
		}

		if _, err := s.messen(naechster); err != nil {
			if s.ctx.Err() != nil {
				return Messung{}, ErrAbgebrochen
			}
			break // Grenze erreicht oder Probe gescheitert: das Beste bis hier gilt
		}
	}

	if _, bezahlbar, gefunden := s.deckelKlammer(deckel); gefunden {
		return bezahlbar, nil
	}
	zuTeuer, _, _ := s.deckelKlammer(deckel)
	return zuTeuer, nil
}

// deckelKlammer liefert den teuersten Punkt über dem Deckel mit dem höchsten
// CRF und den bezahlbaren mit dem niedrigsten CRF.
func (s *crfSuche) deckelKlammer(deckel float64) (zuTeuer, bezahlbar Messung, gefunden bool) {
	for _, m := range s.messungen {
		if s.anteil(m) > deckel {
			if m.CRF > zuTeuer.CRF {
				zuTeuer = m
			}
		} else if !gefunden || m.CRF < bezahlbar.CRF {
			bezahlbar, gefunden = m, true
		}
	}
	return zuTeuer, bezahlbar, gefunden
}

// zweitSparsamste liefert die Messung mit dem nächstniedrigeren CRF — der
// zweite Punkt für die Hochrechnung.
func (s *crfSuche) zweitSparsamste(sparsamste Messung) Messung {
	var zweite Messung
	for _, m := range s.messungen {
		if m.CRF < sparsamste.CRF && m.CRF > zweite.CRF {
			zweite = m
		}
	}
	return zweite
}

// crfFuerBytes rechnet aus zwei Messpunkten (a mit niedrigerem CRF) den CRF,
// bei dem die Probe zielBytes gross wäre — im Logarithmus, weil die Grösse
// je CRF-Stufe um einen festen Faktor fällt. Aufgerundet: lieber eine Stufe
// zu sparsam als über dem Deckel.
func crfFuerBytes(a, b Messung, zielBytes float64) int {
	const schritt = 2 // ohne brauchbare Steigung einfach zwei Stufen weiter
	if a.Bytes <= 0 || b.Bytes <= 0 || zielBytes <= 0 || b.CRF <= a.CRF || b.Bytes >= a.Bytes {
		return b.CRF + schritt
	}
	steigung := math.Log(float64(a.Bytes)/float64(b.Bytes)) / float64(b.CRF-a.CRF)
	crf := float64(a.CRF) + math.Log(float64(a.Bytes)/zielBytes)/steigung
	return int(math.Ceil(crf - 1e-9))
}

// erwarteteErsparnisProzent rechnet aus dem Anteil an den Messstellen hoch,
// um wie viel Prozent die ganze Datei kleiner wird. Nur das Bild wird neu
// kodiert — Ton und Untertitel bleiben gleich gross und werden deshalb
// unverändert eingerechnet. ok ist false, wenn es nichts zu rechnen gibt.
func erwarteteErsparnisProzent(info VideoInfo, anteilBild float64) (prozent float64, ok bool) {
	if anteilBild <= 0 || info.GroesseBytes <= 0 {
		return 0, false
	}
	bildBytes := float64(info.VideoBitrateKbps) * 1000 / 8 * info.DauerSek
	if bildBytes <= 0 || bildBytes > float64(info.GroesseBytes) {
		bildBytes = float64(info.GroesseBytes) // Aufteilung unbekannt: alles als Bild rechnen
	}
	rest := float64(info.GroesseBytes) - bildBytes
	return ProzentKleiner(info.GroesseBytes, int64(anteilBild*bildBytes+rest)), true
}
