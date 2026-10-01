// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

// Die Anzeige für die eigentliche Arbeit: welcher Schritt gerade läuft, wie
// weit er ist und wie lange es noch dauert.
//
// Warum so viel Aufwand um eine Anzeige: Ein Film braucht weit über eine
// Stunde. Ein Fenster, das so lange nur einen Dateinamen zeigt, sieht aus wie
// abgestürzt — genau so ist es beim ersten echten Lauf am 22.09.2026
// passiert. Die Umwandlung lief sauber durch, wirkte aber kaputt.
//
// Drei Arten der Ausgabe:
//   - Übersicht (bildschirm.go): im Terminal während des Umwandelns, eine
//     feste Fläche wie bei NVENCForge, die sich nur aktualisiert.
//   - Terminal ohne Übersicht (etwa bei -analyse): eine Balkenzeile, die sich
//     selbst überschreibt.
//   - Datei oder Pipe: ruhige ganze Zeilen, der Balken nur alle 10 %.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Stand beschreibt, wie weit ein langer Vorgang gerade ist.
type Stand struct {
	Anteil float64       // 0 bis 1
	Tempo  float64       // Vielfaches der Echtzeit, 0 = unbekannt
	Rest   time.Duration // geschätzte Restzeit, 0 = unbekannt
	Text   string        // Zusatz, etwa "1,2 von 3,6 GB"

	// Nur beim Umwandeln bekannt, sonst 0.
	Position     float64 // erreichte Stelle im Film in Sekunden
	Bild         int64   // Nummer des zuletzt fertigen Bildes
	BilderProSek float64
	BitrateKbps  float64
	Bytes        int64 // bisher geschriebene Bytes des Ergebnisses
}

// Rueckmeldung nimmt den Stand eines Vorgangs entgegen. nil ist erlaubt und
// heisst: niemand will es wissen.
type Rueckmeldung func(Stand)

const (
	balkenBreite    = 20
	schrittSpalte   = 21                     // "5/5 Ergebnis ablegen" passt hinein
	neuZeichnenAlle = 250 * time.Millisecond // öfter bringt nichts, es flackert nur
	logStufeProzent = 10                     // in eine Datei nur alle 10 %
)

// Anzeige gibt den Verlauf aus — auf einem Terminal mit einem Balken, der sich
// selbst überschreibt, in einer Logdatei mit ruhigen ganzen Zeilen.
//
// Seit 0.19.0 können mehrere Dateien gleichzeitig laufen (parallelDateien).
// Jede bekommt dann ihren eigenen Platz (NeuerPlatz): eine Anzeige mit eigenem
// Schritt, Fortschritt und eigenen Zeilen, die sich Terminal, Übersicht und
// Protokoll mit den anderen teilt. Mit nur einem Platz sieht alles genau so
// aus wie vorher.
//
// Alle Methoden sind gegen gleichzeitigen Aufruf gesichert: Die Übersicht wird
// zusätzlich im Sekundentakt und bei jeder Größenänderung des Fensters aus
// einer eigenen Goroutine neu gezeichnet, und jeder Platz meldet aus seiner
// eigenen Goroutine.
type Anzeige struct {
	*anzeigeKern
	*platz
}

// anzeigeKern ist, was sich alle Plätze teilen. Sein Mutex schützt auch die
// Plätze.
type anzeigeKern struct {
	mu         sync.Mutex
	amTerminal bool
	zeileOffen bool

	uebersicht *uebersicht // nil, solange keine Übersicht offen ist

	// Seit 0.9.0 schreibt jede Anzeige zusätzlich ins Protokoll (nil = nicht).
	protokoll *Protokoll

	plaetze  []*platz // in der Reihenfolge, in der sie angelegt wurden
	parallel int      // so viele Dateien laufen höchstens gleichzeitig (Plaetze), 0 = eine
}

// platz ist alles, was zu genau einer laufenden Datei gehört.
type platz struct {
	datei          string // "Datei 2/5", steht im Fenstertitel
	schritt        string // "3/5 Umwandeln"
	schrittName    string // "Umwandeln"
	schrittLaeuft  bool
	beginn         time.Time
	letztesMal     time.Time
	letzteStufe    int
	protokollStufe int
	messungen      []string

	// vorsilbe steht vor jeder Protokollzeile, sobald mehrere Plätze
	// arbeiten — sonst wüsste man nicht, zu welcher Datei sie gehört.
	vorsilbe string

	// Für die Übersicht (bildschirm.go).
	dateiNummer     int
	dateiName       string // leer = dieser Platz hat gerade keine Datei
	dateiBeginn     time.Time
	dateiZeilen     []string // Info-Zeile und fertige Schritte der laufenden Datei
	stand           Stand
	standDa         bool
	umwandelnVorbei bool    // die Datei ist schon hinter dem Umwandeln
	laufendeMessung int     // CRF der gerade laufenden Messung, 0 = keine
	prognoseBytes   float64 // geglättete Hochrechnung der Ergebnisgrösse
	quelleBytes     int64
	dateiSchaetzung time.Duration // für die ganze laufende Datei
}

// ProtokollSetzen lässt alles, was die Anzeige ausgibt, auch ins Protokoll
// schreiben.
func (a *Anzeige) ProtokollSetzen(p *Protokoll) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.protokoll = p
}

// protokolliere schreibt ins Protokoll, falls eines gesetzt ist. Der
// Aufrufer hält a.mu.
func (a *Anzeige) protokolliere(text string) {
	a.protokoll.Zeile(a.mitVorsilbe(text))
}

// mitVorsilbe stellt die Dateinummer vor eine Zeile, wenn mehrere Dateien
// gleichzeitig laufen. Leerzeilen gliedern nur und bleiben leer.
func (a *Anzeige) mitVorsilbe(text string) string {
	if a.vorsilbe == "" {
		return text
	}
	zeilen := strings.Split(text, "\n")
	for i, zeile := range zeilen {
		if strings.TrimSpace(zeile) != "" {
			zeilen[i] = a.vorsilbe + zeile
		}
	}
	return strings.Join(zeilen, "\n")
}

// mehrerePlaetze sagt, ob mehrere Dateien gleichzeitig laufen können.
func (a *Anzeige) mehrerePlaetze() bool {
	return a.parallel > 1
}

// NeueAnzeige prüft einmal, wohin geschrieben wird, und richtet sich danach.
func NeueAnzeige() *Anzeige {
	kern := &anzeigeKern{amTerminal: schreibtAufTerminal()}
	p := &platz{}
	kern.plaetze = []*platz{p}
	return &Anzeige{anzeigeKern: kern, platz: p}
}

// Plaetze liefert die Anzeigen für anzahl gleichzeitig laufende Dateien. Bei
// einer ist das diese Anzeige selbst — dann ändert sich nichts. Bei mehreren
// bekommt jede einen eigenen Platz; der dieser Anzeige bleibt für Meldungen,
// die zu keiner Datei gehören.
func (a *Anzeige) Plaetze(anzahl int) []*Anzeige {
	if anzahl <= 1 {
		return []*Anzeige{a}
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	a.parallel = anzahl
	anzeigen := make([]*Anzeige, anzahl)
	for i := range anzeigen {
		p := &platz{}
		a.plaetze = append(a.plaetze, p)
		anzeigen[i] = &Anzeige{anzeigeKern: a.anzeigeKern, platz: p}
	}
	return anzeigen
}

// PlatzFrei meldet, dass dieser Platz keine Datei mehr bearbeitet. Die
// Übersicht zeigt ihn dann nicht mehr.
func (a *Anzeige) PlatzFrei() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dateiName, a.schrittLaeuft, a.dateiZeilen = "", false, nil
	if a.uebersicht != nil {
		a.uebersichtZeichnen()
	}
}

// Datei kündigt die nächste Datei an.
func (a *Anzeige) Datei(nummer, gesamt int, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	kopf := fmt.Sprintf("[%d/%d] %s", nummer, gesamt, name)
	a.datei = fmt.Sprintf("Datei %d/%d", nummer, gesamt)
	if a.mehrerePlaetze() {
		a.vorsilbe = fmt.Sprintf("[%d/%d] ", nummer, gesamt)
	}
	a.schrittLaeuft = false
	a.titel(a.datei)
	a.protokoll.Zeile(kopf) // nennt die Nummer schon selbst
	a.platz.neueDatei(nummer, name)

	if u := a.uebersicht; u != nil {
		u.dateiGesamt = gesamt
		u.protokoll = append(u.protokoll, "", kopf)
		a.uebersichtZeichnen()
		return
	}
	a.zeileSchliessen()
	fmt.Printf("\n%s\n", kopf)
}

// Schritt beginnt einen neuen Arbeitsschritt.
func (a *Anzeige) Schritt(nummer, gesamt int, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.zeileSchliessen()
	a.schrittName = name
	a.schritt = fmt.Sprintf("%d/%d %s", nummer, gesamt, name)
	a.schrittLaeuft = true
	a.beginn = time.Now()
	a.letztesMal = time.Time{}
	a.letzteStufe = -1
	a.protokollStufe = 0 // 0 % steht schon in der Zeile "... läuft"
	a.messungen = nil
	a.titel(fmt.Sprintf("%s - %s", a.schrittName, a.datei))
	a.protokolliere(fmt.Sprintf("  %s ...", a.schritt))

	a.platz.neuerSchritt()
	switch {
	case a.uebersicht != nil:
		a.uebersichtZeichnen()
	case a.amTerminal:
		a.zeichnen("...")
	default:
		fmt.Println(a.mitVorsilbe(fmt.Sprintf("  %s ...", a.schritt)))
	}
}

// Stand meldet, wie weit der laufende Schritt ist. Passt als Rueckmeldung.
func (a *Anzeige) Stand(s Stand) {
	a.mu.Lock()
	defer a.mu.Unlock()

	prozent := int(math.Round(begrenzt(s.Anteil) * 100))

	// Ins Protokoll nur alle 10 % — sonst stünden dort Tausende Zeilen je Film.
	if stufe := prozent / logStufeProzent; stufe > a.protokollStufe {
		a.protokollStufe = stufe
		a.protokolliere("      " + standText(s))
	}

	if a.amTerminal {
		fertig := s.Anteil >= 1
		if a.uebersicht != nil {
			a.platz.standMerken(s) // auch ungezeichnet merken, sonst zappelt die Prognose
		}
		if !fertig && time.Since(a.letztesMal) < neuZeichnenAlle {
			return
		}
		a.letztesMal = time.Now()
		a.titel(fmt.Sprintf("%d %% %s - %s", prozent, a.schrittName, a.datei))
		if a.uebersicht != nil {
			a.uebersichtZeichnen()
			return
		}
		a.zeichnen(standText(s))
		return
	}

	if stufe := prozent / logStufeProzent; stufe > a.letzteStufe {
		a.letzteStufe = stufe
		fmt.Println(a.mitVorsilbe("      " + standText(s)))
	}
}

// Messung meldet eine Qualitätsmessung von Auto-CQ. Ein negativer VMAF-Wert
// heisst: die Messung beginnt gerade. mittel ist der Mittelwert; weicht er
// vom gemessenen Wert ab (Messen am Perzentil, seit 0.19.0), nennt ihn das
// Protokoll dazu — die Übersicht bleibt beim entscheidenden Wert, sonst
// passen die Messungen nicht mehr in eine Zeile.
func (a *Anzeige) Messung(crf int, vmaf, mittel float64) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if vmaf < 0 {
		switch {
		case a.uebersicht != nil:
			a.laufendeMessung = crf
			a.uebersichtZeichnen()
		case a.amTerminal:
			laufend := fmt.Sprintf("misst CRF %d ...", crf)
			a.zeichnen(strings.Join(append(a.messungen, laufend), "   "))
		}
		return
	}

	a.messungen = append(a.messungen, fmt.Sprintf("CRF %d = %s", crf, komma(vmaf, 1)))
	zeile := fmt.Sprintf("      CRF %d ergibt VMAF %s", crf, komma(vmaf, 2))
	if mittel != vmaf {
		zeile += fmt.Sprintf(" (Mittel %s)", komma(mittel, 2))
	}
	a.protokolliere(zeile)
	switch {
	case a.uebersicht != nil:
		a.laufendeMessung = 0
		a.uebersichtZeichnen()
	case a.amTerminal:
		a.zeichnen(strings.Join(a.messungen, "   "))
	default:
		fmt.Println(a.mitVorsilbe(zeile))
	}
}

// SchrittFertig schliesst den laufenden Schritt mit seinem Ergebnis ab.
func (a *Anzeige) SchrittFertig(ergebnis string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	text := fmt.Sprintf("%s (%s)", ergebnis, uhrText(time.Since(a.beginn)))
	a.schrittLaeuft = false
	a.protokolliere(fmt.Sprintf("  %-*s %s", schrittSpalte, a.schritt, text))

	switch {
	case a.uebersicht != nil:
		a.dateiZeile(fmt.Sprintf("  %-*s %s", schrittSpalte, a.schritt, text))
		a.uebersichtZeichnen()
	case a.amTerminal:
		a.zeichnen(text)
		a.zeileSchliessen()
	default:
		fmt.Println(a.mitVorsilbe("      " + text))
	}
}

// Zeile gibt eine gewöhnliche Zeile aus. Eine offene Balkenzeile wird vorher
// abgeschlossen, damit nichts übereinander steht.
func (a *Anzeige) Zeile(format string, werte ...any) {
	a.mu.Lock()
	defer a.mu.Unlock()

	text := fmt.Sprintf(format, werte...)
	a.protokolliere(text)
	if a.uebersicht != nil {
		for _, zeile := range strings.Split(text, "\n") {
			a.dateiZeile(zeile)
		}
		a.uebersichtZeichnen()
		return
	}
	a.zeileSchliessen()
	fmt.Println(a.mitVorsilbe(text))
}

// Titel setzt den Fenstertitel. Er steht auch in der Taskleiste — so sieht
// man den Stand, ohne das Fenster aufzumachen.
func (a *Anzeige) Titel(text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.titel(text)
}

// titel setzt den Fenstertitel. Laufen mehrere Dateien gleichzeitig, bleibt
// er stehen: Jede würde ihn sonst im Viertelsekundentakt an sich reissen.
func (a *Anzeige) titel(text string) {
	if a.amTerminal && !a.mehrerePlaetze() {
		fmt.Printf("\033]0;CloudForge - %s\007", text)
	}
}

// zeichnen überschreibt die aktuelle Zeile. \033[K löscht den Rest der
// Zeile, falls der neue Text kürzer ist als der alte.
//
// Die Zeile wird auf die Fensterbreite gekürzt: Ist sie länger, bricht das
// Terminal sie um, und das \r springt nur noch an den Anfang der zweiten
// Hälfte — jede neue Meldung stünde dann hinter der alten statt darüber.
func (a *Anzeige) zeichnen(inhalt string) {
	breite, _ := fensterMasse()
	zeile := fmt.Sprintf("  %-*s %s", schrittSpalte, a.schritt, inhalt)
	fmt.Printf("\r%s\033[K", sichtbarKuerzen(zeile, breite-1))
	a.zeileOffen = true
}

func (a *Anzeige) zeileSchliessen() {
	if a.zeileOffen {
		fmt.Println()
		a.zeileOffen = false
	}
}

// standText baut die Fortschrittszeile: Balken, Prozent, Restzeit, Tempo.
func standText(s Stand) string {
	anteil := begrenzt(s.Anteil)
	voll := int(math.Round(anteil * balkenBreite))

	teile := []string{
		"[" + strings.Repeat("#", voll) + strings.Repeat("-", balkenBreite-voll) + "]",
		fmt.Sprintf("%3d %%", int(math.Round(anteil*100))),
	}
	if s.Rest > 0 {
		teile = append(teile, "noch "+uhrText(s.Rest))
	}
	if s.Tempo > 0 {
		teile = append(teile, "Tempo "+komma(s.Tempo, 2)+"x")
	}
	if s.Text != "" {
		teile = append(teile, s.Text)
	}
	return strings.Join(teile, "  ")
}

// restzeitSchaetzen rechnet aus dem bisherigen Tempo hoch. Ganz am Anfang
// wäre die Zahl reiner Zufall, deshalb erst ab 1 %.
func restzeitSchaetzen(verstrichen time.Duration, anteil float64) time.Duration {
	if anteil < 0.01 || anteil >= 1 {
		return 0
	}
	return time.Duration(float64(verstrichen) * (1 - anteil) / anteil)
}

// uhrText gibt eine Dauer so aus, wie man sie sagen würde.
func uhrText(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d Sek", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d Min", int(d.Minutes()))
	default:
		stunden := int(d.Hours())
		minuten := int(d.Minutes()) - stunden*60
		return fmt.Sprintf("%d Std %d Min", stunden, minuten)
	}
}

// groesseText gibt eine Dateigrösse in MB oder GB aus.
func groesseText(bytes int64) string {
	const gb = 1024 * 1024 * 1024
	if bytes >= gb {
		return komma(float64(bytes)/gb, 2) + " GB"
	}
	return fmt.Sprintf("%d MB", bytes/1024/1024)
}

// komma schreibt eine Zahl mit deutschem Dezimalkomma.
func komma(wert float64, stellen int) string {
	return strings.Replace(strconv.FormatFloat(wert, 'f', stellen, 64), ".", ",", 1)
}

func begrenzt(anteil float64) float64 {
	return math.Max(0, math.Min(anteil, 1))
}
