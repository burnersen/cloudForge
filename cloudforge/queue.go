// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

// Das Gedächtnis zwischen den Läufen — seit 0.11.0 bewusst klein.
//
// Bis 0.10.0 stand hier jede Datei mit Zeitpunkt, Ergebnis und Fehler, und
// "erledigt" hiess: steht in dieser Liste. Das wollte der Nutzer nicht
// (26.09.2026: "kein Logbuch, wann ich wann was konvertiert habe"): Die Liste
// hatte eine von ihm zurückgeholte Datei still blockiert und einen Fehler vom
// 23.09. angezeigt, als gehöre er zum aktuellen Lauf. Seitdem entscheiden
// allein die Ordner, was erledigt ist — originals, output und die Endungen
// der Ergebnisse (scan.go) —, genau wie in NVENCForge.
//
// Übrig bleiben zwei Dinge, die er ausdrücklich behalten wollte:
//   - die Gesamtbilanz: gesparte Bytes und Rechenzeit über alle Läufe,
//   - das Tempo der letzten Umwandlungen für die Zeitschätzung —
//     nur Zahlen, keine Dateinamen, kein Datum.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Was in einem Lauf aus einer Datei geworden ist.
const (
	StatusErledigt      = "erledigt"  // umgewandelt
	StatusUmgepackt     = "umgepackt" // verlustfrei nach MKV umgepackt (seit 0.11.0)
	StatusFehler        = "fehler"
	StatusUebersprungen = "uebersprungen"
)

// Eintrag beschreibt, was in einem Lauf aus einer Datei geworden ist. Er wird
// seit 0.11.0 nicht mehr gespeichert, sondern nur noch in die Summen und das
// Tempo übernommen (DateiFertig).
type Eintrag struct {
	Status        string
	QuelleBytes   int64
	ErgebnisBytes int64
	CRF           int
	VMAF          float64
	Meldung       string

	// Für die Zeitschätzung und die Gesamtbilanz.
	Bilder    int64   // Bilder im Film
	RechenSek float64 // ganze Bearbeitung, Holen bis Ablegen
}

// GespartBytes sagt, wie viel diese Datei gebracht hat. Beim Umpacken kann es
// ein wenig weniger als nichts sein — auch das wird ehrlich mitgezählt.
func (e Eintrag) GespartBytes() int64 {
	if (e.Status != StatusErledigt && e.Status != StatusUmgepackt) || e.ErgebnisBytes <= 0 {
		return 0
	}
	return e.QuelleBytes - e.ErgebnisBytes
}

// Zustand ist das Gedächtnis zwischen den Läufen (Datei neben der INI).
type Zustand struct {
	Fassung int `json:"fassung"`

	// Gesamtbilanz über alle Läufe.
	GesamtGespartBytes int64   `json:"gesamtGespartBytes"`
	GesamtRechenSek    float64 `json:"gesamtRechenSek"`
	GesamtDateien      int     `json:"gesamtDateien"`

	// Tempo der jüngsten Umwandlungen, älteste zuerst.
	Tempo []TempoProbe `json:"tempo"`

	pfad string
}

// TempoProbe ist eine gemessene Umwandlung: so viele Bilder und Bytes in so
// vielen Sekunden. Absichtlich ohne Namen und Datum.
type TempoProbe struct {
	Bilder      int64   `json:"bilder"`
	QuelleBytes int64   `json:"quelleBytes"`
	RechenSek   float64 `json:"rechenSek"`
}

// zustandFassung 2 seit 0.11.0; Fassung 1 war die Dateiliste bis 0.10.0.
const zustandFassung = 2

// ZustandLaden liest das Gedächtnis. Fehlt die Datei, beginnt es leer — der
// erste Lauf braucht keine Vorbereitung. Eine Datei aus der Zeit bis 0.10.0
// wird einmal umgerechnet und sofort in der kleinen Form gespeichert: Summen
// und Tempo bleiben, die Dateiliste verschwindet.
func ZustandLaden(pfad string) (*Zustand, error) {
	inhalt, err := os.ReadFile(pfad)
	if os.IsNotExist(err) {
		return &Zustand{Fassung: zustandFassung, pfad: pfad}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("Zustandsdatei nicht lesbar: %w", err)
	}

	z, umgestellt, err := zustandAusInhalt(inhalt)
	if err != nil {
		// Eine kaputte Datei darf den Lauf nicht verhindern. Sie wird
		// beiseitegelegt, damit man noch hineinsehen kann, und neu begonnen.
		beiseite := pfad + ".unlesbar"
		os.Rename(pfad, beiseite)
		return &Zustand{Fassung: zustandFassung, pfad: pfad},
			fmt.Errorf("Zustandsdatei war unlesbar und liegt jetzt als %s; die Gesamtbilanz beginnt neu",
				filepath.Base(beiseite))
	}
	z.pfad = pfad
	if umgestellt {
		if err := z.Speichern(); err != nil {
			return z, fmt.Errorf("Zustand konnte nicht umgestellt werden: %w", err)
		}
	}
	return z, nil
}

// eintragFassung1 ist ein Eintrag der alten Dateiliste — nur, was die
// Umrechnung braucht.
type eintragFassung1 struct {
	Status        string    `json:"status"`
	Zeitpunkt     time.Time `json:"zeitpunkt"`
	QuelleBytes   int64     `json:"quelleBytes"`
	ErgebnisBytes int64     `json:"ergebnisBytes"`
	Bilder        int64     `json:"bilder"`
	RechenSek     float64   `json:"rechenSek"`
}

// zustandAusInhalt liest beide Fassungen. umgestellt heisst: es war die alte
// Dateiliste, die Summen und das Tempo sind daraus neu berechnet.
func zustandAusInhalt(inhalt []byte) (z *Zustand, umgestellt bool, err error) {
	var kopf struct {
		Fassung   int                        `json:"fassung"`
		Eintraege map[string]eintragFassung1 `json:"eintraege"`
	}
	if err := json.Unmarshal(inhalt, &kopf); err != nil {
		return nil, false, err
	}
	if kopf.Fassung >= zustandFassung {
		z = &Zustand{}
		if err := json.Unmarshal(inhalt, z); err != nil {
			return nil, false, err
		}
		return z, false, nil
	}
	return ausDateiliste(kopf.Eintraege), true, nil
}

// ausDateiliste rechnet die alte Dateiliste in die kleine Form um: alle fertig
// umgewandelten Dateien in die Summen, die jüngsten mit Zeitmessung ins Tempo.
// Rechenzeiten gibt es erst seit 0.7.0 — ältere Dateien zählen zur Ersparnis,
// aber mit 0 Sekunden zur Zeit.
func ausDateiliste(eintraege map[string]eintragFassung1) *Zustand {
	z := &Zustand{Fassung: zustandFassung}
	var mitTempo []eintragFassung1
	for _, alt := range eintraege {
		if alt.Status != StatusErledigt {
			continue
		}
		z.GesamtDateien++
		if alt.ErgebnisBytes > 0 {
			z.GesamtGespartBytes += alt.QuelleBytes - alt.ErgebnisBytes
		}
		z.GesamtRechenSek += alt.RechenSek
		if alt.Bilder > 0 && alt.RechenSek > 0 && alt.QuelleBytes > 0 {
			mitTempo = append(mitTempo, alt)
		}
	}

	sort.Slice(mitTempo, func(i, j int) bool { return mitTempo[i].Zeitpunkt.Before(mitTempo[j].Zeitpunkt) })
	if len(mitTempo) > erfahrungAusLetzten {
		mitTempo = mitTempo[len(mitTempo)-erfahrungAusLetzten:]
	}
	for _, alt := range mitTempo {
		z.Tempo = append(z.Tempo, TempoProbe{Bilder: alt.Bilder, QuelleBytes: alt.QuelleBytes, RechenSek: alt.RechenSek})
	}
	return z
}

// Speichern schreibt das Gedächtnis weg. Erst in eine Nebendatei, dann
// umbenennen — so bleibt bei einem Stromausfall mitten im Schreiben
// wenigstens der vorherige Stand erhalten.
func (z *Zustand) Speichern() error {
	if err := os.MkdirAll(filepath.Dir(z.pfad), 0o755); err != nil {
		return err
	}

	inhalt, err := json.MarshalIndent(z, "", "  ")
	if err != nil {
		return err
	}

	tarnPfad := z.pfad + unfertigEndung
	if err := os.WriteFile(tarnPfad, inhalt, 0o644); err != nil {
		return fmt.Errorf("Zustand nicht schreibbar: %w", err)
	}
	if err := os.Rename(tarnPfad, z.pfad); err != nil {
		os.Remove(tarnPfad)
		return fmt.Errorf("Zustand nicht sicherbar: %w", err)
	}
	return nil
}

// DateiFertig übernimmt eine umgewandelte oder umgepackte Datei in die
// Gesamtbilanz — eine Umwandlung mit Zeitmessung auch ins Tempo — und sichert
// sofort. Übersprungenes und Gescheitertes zählt nicht: es hat nichts gespart.
func (z *Zustand) DateiFertig(eintrag Eintrag) error {
	if eintrag.Status != StatusErledigt && eintrag.Status != StatusUmgepackt {
		return nil
	}
	z.GesamtDateien++
	z.GesamtGespartBytes += eintrag.GespartBytes()
	z.GesamtRechenSek += eintrag.RechenSek

	// Umpacken ist viel schneller als Umwandeln und würde die Schätzung verbiegen.
	if eintrag.Status == StatusErledigt && eintrag.Bilder > 0 && eintrag.RechenSek > 0 && eintrag.QuelleBytes > 0 {
		z.Tempo = append(z.Tempo, TempoProbe{
			Bilder: eintrag.Bilder, QuelleBytes: eintrag.QuelleBytes, RechenSek: eintrag.RechenSek,
		})
		if len(z.Tempo) > erfahrungAusLetzten {
			z.Tempo = z.Tempo[len(z.Tempo)-erfahrungAusLetzten:]
		}
	}
	return z.Speichern()
}

// Erfahrung sagt, wie schnell die letzten fertigen Dateien durchliefen —
// gemessen über die ganze Bearbeitung vom Holen bis zum Ablegen. Daraus
// schätzt die Anzeige, wie lange eine Datei dauern wird.
//
// Warum lernen statt fester Werte: Das Tempo hängt am Server, am Preset, an
// der Bittiefe und am Qualitätsziel. Die festen Werte aus der Contabo-Zeit
// sagten am 25.09.2026 für einen Film 1 Std 58 Min voraus — auf dem neuen
// Server waren es 33 Min.
type Erfahrung struct {
	BilderProSek float64 // Bilder des Films je Sekunde Bearbeitung
	BytesProSek  float64 // Bytes der Quelle je Sekunde Bearbeitung
	Dateien      int     // aus wie vielen Dateien, 0 = noch keine: Startwerte
}

// erfahrungAusLetzten: Aus so vielen der jüngsten Dateien wird gemittelt.
// Wenige, damit eine geänderte Einstellung schnell durchschlägt.
const erfahrungAusLetzten = 5

// startErfahrung gilt, solange noch keine Datei mit Zeitmessung fertig ist.
// Gemessen am 25.09.2026 auf dem netcup-Server (EPYC 9645, 8 Kerne):
// Umwandeln bei preset 9, 10 Bit, CRF 20, 1080p mit 30 Bildern/s: 66,7
// Bilder/s. Ein ganzer Testfilm brauchte für Holen, Messen und Ablegen
// zusätzlich knapp 20 % der Zeit (27 von 33 Min waren Umwandeln) — also
// rund 54 Bilder/s über alles. Dieselbe Datei hatte 3,1 MB Quelle je Sekunde
// bei 61 Bildern/s, auf 54 Bilder/s umgerechnet 2,75 MB/s.
var startErfahrung = Erfahrung{BilderProSek: 54, BytesProSek: 2.75e6}

// Erfahrung wertet das gemerkte Tempo aus. Der Median statt des Mittelwerts:
// Eine einzelne Datei, die wegen einer Netzstörung doppelt so lange brauchte,
// soll die Schätzung nicht verbiegen.
func (z *Zustand) Erfahrung() Erfahrung {
	var bilder, bytes []float64
	for _, probe := range z.Tempo {
		if probe.RechenSek <= 0 || probe.Bilder <= 0 || probe.QuelleBytes <= 0 {
			continue // von Hand verändert oder beschädigt — lieber auslassen
		}
		bilder = append(bilder, float64(probe.Bilder)/probe.RechenSek)
		bytes = append(bytes, float64(probe.QuelleBytes)/probe.RechenSek)
	}
	if len(bilder) == 0 {
		return startErfahrung
	}
	return Erfahrung{BilderProSek: median(bilder), BytesProSek: median(bytes), Dateien: len(bilder)}
}

// ErfahrungLesen holt nur das Tempo aus der Zustandsdatei, ohne sie
// anzufassen — anders als ZustandLaden stellt es eine alte Datei nicht um und
// legt eine kaputte NICHT beiseite. Für -bericht, das nichts verändern darf.
func ErfahrungLesen(pfad string) Erfahrung {
	inhalt, err := os.ReadFile(pfad)
	if err != nil {
		return startErfahrung
	}
	z, _, err := zustandAusInhalt(inhalt)
	if err != nil {
		return startErfahrung
	}
	return z.Erfahrung()
}

// DauerFuerBilder schätzt die Bearbeitungszeit eines Films mit so vielen Bildern.
func (x Erfahrung) DauerFuerBilder(bilder float64) time.Duration {
	if x.BilderProSek <= 0 || bilder <= 0 {
		return 0
	}
	return time.Duration(bilder / x.BilderProSek * float64(time.Second))
}

// DauerFuerBytes schätzt die Bearbeitungszeit aus der Dateigrösse — für
// Dateien, deren Bildzahl noch niemand gelesen hat. Ungenauer, weil 50
// Bilder/s bei gleicher Grösse mehr Arbeit sind als 30, reicht aber für ein
// "ca." über die ganze Warteschlange.
func (x Erfahrung) DauerFuerBytes(bytes int64) time.Duration {
	if x.BytesProSek <= 0 || bytes <= 0 {
		return 0
	}
	return time.Duration(float64(bytes) / x.BytesProSek * float64(time.Second))
}

func median(werte []float64) float64 {
	if len(werte) == 0 {
		return 0
	}
	sortiert := append([]float64(nil), werte...)
	sort.Float64s(sortiert)
	mitte := len(sortiert) / 2
	if len(sortiert)%2 == 1 {
		return sortiert[mitte]
	}
	return (sortiert[mitte-1] + sortiert[mitte]) / 2
}

// StandardZustandPfad liegt neben der INI.
func StandardZustandPfad(e Einstellungen) string {
	return filepath.Join(filepath.Dir(e.ArbeitsOrdner), "cloudforge-zustand.json")
}
