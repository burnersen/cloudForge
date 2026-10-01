// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

// Die Übersicht im Terminal während des Umwandelns — optisch wie NVENCForge
// unter Windows: eine feste, farbige Fläche, die sich nur aktualisiert.
//
// Warum der Wechselbildschirm (wie bei top oder htop): Eine Fläche, die per
// Cursor-Sprung im normalen Textverlauf überschrieben wird, zerfällt, sobald
// jemand das Fenster schmaler zieht — das Terminal bricht die alten Zeilen
// dann neu um, und die Sprünge treffen die falschen Stellen. So sah das
// Fenster am 25.09.2026 beim ersten Film auf dem netcup-Server aus. Auf dem
// Wechselbildschirm wird dagegen bei jeder Größenänderung alles neu
// gezeichnet und auf die Fensterbreite gekürzt. Beim Schliessen kommt der
// normale Bildschirm zurück, und dort steht der Verlauf als gewöhnlicher Text.

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

// Farben wie bei NVENCForge (dort über pterm): Balken hellgrün auf grau,
// Prozent fett weiss, Beschriftungen cyan, Restzeit gelb, Tempo grün,
// die Gesamtzeile magenta.
const (
	farbeAus         = "\033[0m"
	farbeFett        = "\033[1m"
	farbeGrau        = "\033[90m"
	farbeRot         = "\033[91m"
	farbeGruen       = "\033[92m"
	farbeGelb        = "\033[93m"
	farbeMagenta     = "\033[95m"
	farbeCyan        = "\033[36m"
	farbeWeissFett   = "\033[1;97m"
	farbeMagentaFett = "\033[1;95m"
)

const (
	wechselbildschirmAn  = "\033[?1049h\033[?25l" // umschalten, Cursor verstecken
	wechselbildschirmAus = "\033[?25h\033[?1049l"

	uebersichtTakt  = time.Second // damit die Laufzeit auch ohne Meldung weiterzählt
	standardBreite  = 80
	standardHoehe   = 24
	hauptBalkenMax  = 48 // wie NVENCForge
	hauptBalkenMin  = 10
	gesamtBalken    = 24 // wie NVENCForge
	beschriftung    = 10 // Spaltenbreite der cyan Beschriftungen
	beschriftungEnd = 6  // ... der letzten Spalte
	mebibyte        = 1024 * 1024
)

// uebersicht hält fest, was die Fläche über alle Dateien hinweg zeigt. Was
// zu einer laufenden Datei gehört, steht in deren platz (anzeige.go).
// Geschützt über den Mutex der Anzeige.
type uebersicht struct {
	dateiGesamt int
	fertig      int      // abgeschlossene Dateien, für die Gesamtzeile bei mehreren Plätzen
	verlauf     []string // je fertige Datei eine Zeile
	protokoll   []string // wird nach dem Schliessen als Text ausgegeben

	danachSchaetzung time.Duration // für alle Dateien, die noch nicht angefangen sind

	stopp   chan struct{}
	beendet chan struct{}
}

func (p *platz) neueDatei(nummer int, name string) {
	p.dateiNummer, p.dateiName = nummer, name
	p.dateiBeginn = time.Now()
	p.dateiZeilen = nil
	p.quelleBytes, p.dateiSchaetzung = 0, 0
	p.umwandelnVorbei = false
	p.neuerSchritt()
}

func (p *platz) neuerSchritt() {
	if p.standDa && istUmwandeln(p.stand) {
		p.umwandelnVorbei = true
	}
	p.stand, p.standDa = Stand{}, false
	p.laufendeMessung = 0
	p.prognoseBytes = 0
}

func (p *platz) standMerken(s Stand) {
	p.stand, p.standDa = s, true
	if s.Bytes > 0 && s.Anteil >= 0.01 {
		p.prognoseBytes = glaettePrognose(p.prognoseBytes, float64(s.Bytes)/s.Anteil, s.Anteil)
	}
}

// dateiZeile nimmt eine Zeile in den Block der laufenden Datei und ins
// Protokoll der Übersicht auf. Leerzeilen gliedern nur das Protokoll. Der
// Aufrufer hält a.mu, die Übersicht ist offen.
func (a *Anzeige) dateiZeile(zeile string) {
	a.uebersicht.protokoll = append(a.uebersicht.protokoll, a.mitVorsilbe(zeile))
	if a.dateiName != "" && strings.TrimSpace(zeile) != "" {
		a.dateiZeilen = append(a.dateiZeilen, zeile)
	}
}

// istUmwandeln erkennt einen Stand von ffmpeg — nur der kennt Filmposition
// und Bilder je Sekunde. Kopieren meldet beides nie.
func istUmwandeln(s Stand) bool {
	return s.Position > 0 || s.BilderProSek > 0
}

// UebersichtOeffnen schaltet im Terminal auf die feste Übersicht um. In einer
// Logdatei ändert sich nichts.
func (a *Anzeige) UebersichtOeffnen() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.amTerminal || a.uebersicht != nil {
		return
	}
	a.zeileSchliessen()
	u := &uebersicht{stopp: make(chan struct{}), beendet: make(chan struct{})}
	a.uebersicht = u

	fmt.Print(wechselbildschirmAn)
	a.uebersichtZeichnen()

	groesse, aufhoeren := groessenAenderungen()
	go a.uebersichtBegleiten(u, groesse, aufhoeren)
}

// uebersichtBegleiten zeichnet im Sekundentakt und bei jeder Grössenänderung
// des Fensters neu, bis die Übersicht geschlossen wird.
func (a *Anzeige) uebersichtBegleiten(u *uebersicht, groesse <-chan os.Signal, aufhoeren func()) {
	defer close(u.beendet)
	defer aufhoeren()

	takt := time.NewTicker(uebersichtTakt)
	defer takt.Stop()

	for {
		select {
		case <-u.stopp:
			return
		case <-takt.C:
		case <-groesse:
		}
		a.mu.Lock()
		if a.uebersicht == u {
			a.uebersichtZeichnen()
		}
		a.mu.Unlock()
	}
}

// UebersichtSchliessen kehrt zum normalen Bildschirm zurück und schreibt dort
// den Verlauf als gewöhnlichen Text hin — so steht nach dem Lauf alles da,
// was früher Zeile für Zeile erschienen wäre. Mehrfacher Aufruf schadet nicht.
func (a *Anzeige) UebersichtSchliessen() {
	a.mu.Lock()
	u := a.uebersicht
	a.uebersicht = nil
	a.mu.Unlock()

	if u == nil {
		return
	}
	// Erst die Begleit-Goroutine beenden, damit sie nicht noch einmal auf den
	// normalen Bildschirm zeichnet.
	close(u.stopp)
	<-u.beendet

	a.mu.Lock()
	defer a.mu.Unlock()
	fmt.Print(wechselbildschirmAus)
	for _, zeile := range u.protokoll {
		fmt.Println(zeile)
	}
}

// DateiInfo nennt Grösse und geschätzte Dauer der laufenden Datei — für die
// Grössen-Prognose und die Gesamt-Restzeit der Übersicht.
func (a *Anzeige) DateiInfo(quelleBytes int64, geschaetzt time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.quelleBytes, a.dateiSchaetzung = quelleBytes, geschaetzt
}

// Warteschlange nennt die geschätzte Dauer aller Dateien, die noch nicht
// angefangen sind.
func (a *Anzeige) Warteschlange(danach time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if u := a.uebersicht; u != nil {
		u.danachSchaetzung = danach
	}
}

// DateiAbgeschlossen trägt die laufende Datei mit einer Zeile in den Verlauf
// der Übersicht ein. Ohne Übersicht geschieht nichts — dort stehen die
// ausführlichen Zeilen ohnehin schon da.
func (a *Anzeige) DateiAbgeschlossen(status, kurz string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	u := a.uebersicht
	if u == nil {
		return
	}
	var marke string
	switch status {
	case StatusErledigt:
		marke = farbeGruen + "OK    " + farbeAus
	case StatusUmgepackt:
		marke = farbeCyan + "UMPACK" + farbeAus
	case StatusUebersprungen:
		marke = farbeGrau + "--    " + farbeAus
	default:
		marke = farbeRot + "FEHLER" + farbeAus
	}
	u.verlauf = append(u.verlauf, fmt.Sprintf(" %s %s  %s", marke, a.dateiName, kurz))
	u.fertig++
	a.uebersichtZeichnen()
}

// uebersichtZeichnen zeichnet die ganze Fläche neu, in einem einzigen
// Schreibvorgang, damit nichts flackert. Der Aufrufer hält a.mu.
func (a *Anzeige) uebersichtZeichnen() {
	breite, hoehe := fensterMasse()
	zeilen := a.uebersichtZeilen(breite, hoehe, time.Now())

	var bild strings.Builder
	bild.WriteString("\033[H")
	for i, zeile := range zeilen {
		if i > 0 {
			bild.WriteString("\n")
		}
		// Eine Spalte Luft: Wer in die letzte Spalte schreibt, löst bei
		// manchen Terminals einen Umbruch aus, und alles verrutscht.
		bild.WriteString(sichtbarKuerzen(zeile, breite-1))
		bild.WriteString("\033[K")
	}
	bild.WriteString("\033[J")
	fmt.Print(bild.String())
}

// uebersichtZeilen baut die Zeilen der Fläche für ein Fenster dieser Grösse.
// Passt nicht alles hinein, fällt zuerst der Verlauf der fertigen Dateien weg
// (die neuesten bleiben), dann die ältesten Zeilen der laufenden Datei. Der
// Fortschritt bleibt immer sichtbar.
//
// Laufen mehrere Dateien gleichzeitig (seit 0.19.0), steht jede mit Kopf,
// Zeilen und Fortschritt untereinander; den freien Platz für ihre Zeilen
// teilen sie sich gleichmässig. Die Gesamtzeile steht dann einmal darunter.
func (a *Anzeige) uebersichtZeilen(breite, hoehe int, jetzt time.Time) []string {
	u := a.uebersicht
	laufend := a.laufendePlaetze()

	kopf := []string{
		kopfZeile(breite, a.kopfRechts(laufend)),
		farbeGrau + " " + strings.Repeat("-", max(breite-3, 1)) + farbeAus,
	}

	var teile []dateiTeil
	var gesamt []string
	if len(laufend) <= 1 {
		// Eine Datei: die Gesamtzeile gehört zu ihrem Fortschritt, wie immer.
		p := a.platz
		if len(laufend) == 1 {
			p = laufend[0]
		}
		teile = []dateiTeil{a.mitPlatz(p).dateiTeil(breite, jetzt, true)}
	} else {
		for _, p := range laufend {
			teile = append(teile, a.mitPlatz(p).dateiTeil(breite, jetzt, false))
		}
		if zeile := a.gesamtZeile(jetzt); zeile != "" {
			gesamt = []string{"", zeile}
		}
	}
	fuss := []string{"", farbeGrau + " Aufhoeren: Fenster schliessen oder Strg+C - es geht nichts verloren." + farbeAus}

	frei := hoehe - len(kopf) - len(gesamt) - len(fuss)
	for _, t := range teile {
		frei -= len(t.kopf) + len(t.block)
	}
	jeDatei := frei / len(teile)
	sichtbar := 0
	for i := range teile {
		teile[i].zeilen = letzte(teile[i].zeilen, jeDatei)
		sichtbar += len(teile[i].zeilen)
	}
	verlauf := verlaufKuerzen(u.verlauf, frei-sichtbar)

	zeilen := append([]string{}, kopf...)
	zeilen = append(zeilen, verlauf...)
	for _, t := range teile {
		zeilen = append(zeilen, t.kopf...)
		zeilen = append(zeilen, t.zeilen...)
		zeilen = append(zeilen, t.block...)
	}
	zeilen = append(zeilen, gesamt...)
	zeilen = append(zeilen, fuss...)
	if len(zeilen) > hoehe {
		zeilen = zeilen[:max(hoehe, 1)]
	}
	return zeilen
}

// dateiTeil ist der Bereich einer laufenden Datei in der Übersicht.
type dateiTeil struct {
	kopf   []string // ">> [2/5] Name"
	zeilen []string // Info-Zeile, fertige Schritte, laufender Schritt
	block  []string // Fortschritt, mit einer Leerzeile davor
}

// dateiTeil baut den Bereich des Platzes dieser Anzeige. mitGesamt hängt die
// Gesamtzeile an den Fortschritt an.
func (a *Anzeige) dateiTeil(breite int, jetzt time.Time, mitGesamt bool) dateiTeil {
	var t dateiTeil
	if a.dateiName != "" {
		t.kopf = []string{fmt.Sprintf(" %s>> [%d/%d] %s%s",
			farbeFett, a.dateiNummer, a.uebersicht.dateiGesamt, a.dateiName, farbeAus)}
		t.zeilen = append(t.zeilen, a.dateiZeilen...)
		if a.schrittLaeuft {
			t.zeilen = append(t.zeilen,
				fmt.Sprintf("%s  %-*s laeuft ...%s", farbeGelb, schrittSpalte, a.schritt, farbeAus))
		}
	}

	block := a.platzFortschritt(breite, jetzt)
	if mitGesamt {
		block = a.fortschrittsBlock(breite, jetzt)
	}
	if len(block) > 0 {
		t.block = append([]string{""}, block...)
	}
	return t
}

// mitPlatz ist eine Anzeige für den Platz p — für die Übersicht, die alle
// Plätze zeichnet. Der Aufrufer hält a.mu.
func (a *Anzeige) mitPlatz(p *platz) *Anzeige {
	return &Anzeige{anzeigeKern: a.anzeigeKern, platz: p}
}

// laufendePlaetze sind die Plätze, die gerade eine Datei bearbeiten.
func (a *Anzeige) laufendePlaetze() []*platz {
	var laufend []*platz
	for _, p := range a.plaetze {
		if p.dateiName != "" {
			laufend = append(laufend, p)
		}
	}
	return laufend
}

// kopfRechts steht rechts in der Kopfzeile: welche Datei läuft — bei
// mehreren, wie viele gleichzeitig und wie viele schon fertig sind.
func (a *Anzeige) kopfRechts(laufend []*platz) string {
	u := a.uebersicht
	switch {
	case u.dateiGesamt == 0:
		return ""
	case len(laufend) > 1:
		return fmt.Sprintf("%d gleichzeitig, %d von %d fertig", len(laufend), u.fertig, u.dateiGesamt)
	case len(laufend) == 1:
		return fmt.Sprintf("Datei %d von %d", laufend[0].dateiNummer, u.dateiGesamt)
	case a.mehrerePlaetze():
		return fmt.Sprintf("%d von %d fertig", u.fertig, u.dateiGesamt)
	default:
		return fmt.Sprintf("Datei %d von %d", a.dateiNummer, u.dateiGesamt)
	}
}

// fortschrittsBlock ist der Fortschritt des Platzes mit der Gesamtzeile
// darunter — so steht er da, wenn nur eine Datei läuft.
func (a *Anzeige) fortschrittsBlock(breite int, jetzt time.Time) []string {
	zeilen := a.platzFortschritt(breite, jetzt)
	if gesamt := a.gesamtZeile(jetzt); gesamt != "" {
		zeilen = append(zeilen, gesamt)
	}
	return zeilen
}

// platzFortschritt baut den unteren Teil, so nah an NVENCForge wie möglich:
// Balken, dann Position/Laufzeit/Rest, Bilder/s/Bitrate/Tempo und die
// Grössen-Prognose. Beim Kopieren gibt es nur Balken, Zeiten und Menge,
// beim Messen und Prüfen keinen Prozentwert.
func (a *Anzeige) platzFortschritt(breite int, jetzt time.Time) []string {
	s := a.stand
	laufzeit := jetzt.Sub(a.beginn)

	var zeilen []string
	switch {
	case !a.schrittLaeuft:
	case a.standDa && istUmwandeln(s):
		zeilen = append(zeilen,
			hauptBalken(s.Anteil, breite),
			fmt.Sprintf("  %s %-8s   %s %-8s   %s %s",
				marke("Position", beschriftung), zeitText(sekunden(s.Position)),
				marke("Laufzeit", beschriftung), zeitText(laufzeit),
				marke("Rest", beschriftungEnd), farbeGelb+restText(s.Rest)+farbeAus),
			fmt.Sprintf("  %s %-8s   %s %-8s   %s %s",
				marke("Bilder/s", beschriftung), komma(s.BilderProSek, 1),
				marke("Bitrate", beschriftung), bitrateText(s.BitrateKbps),
				marke("Tempo", beschriftungEnd), farbeGruen+komma(s.Tempo, 2)+"x"+farbeAus),
			fmt.Sprintf("  %s %-8s   %s",
				marke("Bild", beschriftung), strconv.FormatInt(s.Bild, 10), a.platz.groessenText()),
		)
	case a.standDa:
		zeilen = append(zeilen,
			hauptBalken(s.Anteil, breite),
			fmt.Sprintf("  %s %-8s   %s %s",
				marke("Laufzeit", beschriftung), zeitText(laufzeit),
				marke("Rest", beschriftung), farbeGelb+restText(s.Rest)+farbeAus),
		)
		if s.Text != "" {
			zeilen = append(zeilen, fmt.Sprintf("  %s %s", marke("Menge", beschriftung), s.Text))
		}
	default:
		zeilen = append(zeilen, fmt.Sprintf("  %s %s", marke("Laufzeit", beschriftung), zeitText(laufzeit)))
		teile := append([]string{}, a.messungen...)
		if a.laufendeMessung > 0 {
			teile = append(teile, fmt.Sprintf("misst CRF %d ...", a.laufendeMessung))
		}
		if len(teile) > 0 {
			zeilen = append(zeilen, fmt.Sprintf("  %s %s", marke("Messungen", beschriftung), strings.Join(teile, "   ")))
		}
	}
	return zeilen
}

// gesamtZeile zeigt den Stand über alle Dateien — nur, wenn es mehrere sind.
//
// Die Restzeit setzt sich zusammen aus dem Rest der laufenden Datei (beim
// Umwandeln live von ffmpeg, sonst aus der Schätzung) und der Schätzung für
// alle folgenden Dateien. Laufen mehrere gleichzeitig, zählen die Reste aller
// laufenden, und die Summe teilt sich auf die gleichzeitig arbeitenden Plätze
// auf — eine grobe Schätzung, denn jede Datei läuft dann langsamer als allein.
func (a *Anzeige) gesamtZeile(jetzt time.Time) string {
	u := a.uebersicht
	laufend := a.laufendePlaetze()
	if u.dateiGesamt <= 1 || len(laufend) == 0 {
		return ""
	}

	var restSumme time.Duration
	anteilSumme := 0.0
	for _, p := range laufend {
		rest, anteil := p.restUndAnteil(jetzt)
		restSumme += rest
		anteilSumme += anteil
	}

	erledigt := laufend[0].dateiNummer - 1
	zaehler := fmt.Sprintf("(%d/%d)", laufend[0].dateiNummer, u.dateiGesamt)
	gleichzeitig := 1
	if a.mehrerePlaetze() {
		erledigt = u.fertig
		zaehler = fmt.Sprintf("(%d/%d fertig)", u.fertig, u.dateiGesamt)
		gleichzeitig = a.parallel
	}
	anteil := (float64(erledigt) + anteilSumme) / float64(u.dateiGesamt)

	rest := "-:--"
	if gesamt := (restSumme + u.danachSchaetzung) / time.Duration(gleichzeitig); gesamt > 0 {
		rest = "ca. " + uhrText(gesamt)
	}
	return fmt.Sprintf("  %s [%s]  %s  %s  %s %s",
		farbeMagentaFett+fmt.Sprintf("%-*s", beschriftung, "Gesamt")+farbeAus,
		balkenText(anteil, gesamtBalken, farbeMagenta),
		farbeWeissFett+prozentText(anteil)+farbeAus,
		farbeGrau+zaehler+farbeAus,
		farbeCyan+"noch"+farbeAus, farbeGelb+rest+farbeAus)
}

// restUndAnteil schätzt, wie lange die Datei dieses Platzes noch braucht und
// wie viel von ihr schon geschafft ist (0 bis 1).
func (p *platz) restUndAnteil(jetzt time.Time) (time.Duration, float64) {
	vergangen := jetzt.Sub(p.dateiBeginn)
	var rest time.Duration
	switch {
	case p.umwandelnVorbei:
		// Prüfen und Ablegen dauern Sekunden — nichts mehr einzurechnen.
	case p.standDa && istUmwandeln(p.stand) && p.stand.Rest > 0:
		rest = p.stand.Rest
	case p.dateiSchaetzung > vergangen:
		rest = p.dateiSchaetzung - vergangen
	}

	anteil := 0.0
	if vergangen+rest > 0 {
		anteil = float64(vergangen) / float64(vergangen+rest)
	}
	return rest, anteil
}

// groessenText zeigt, wie gross das Ergebnis voraussichtlich wird.
func (p *platz) groessenText() string {
	quelleMB := float64(p.quelleBytes) / mebibyte
	prognoseMB := p.prognoseBytes / mebibyte

	switch {
	case prognoseMB >= 1 && quelleMB > 0:
		gespart := quelleMB - prognoseMB
		if gespart < 0 {
			return fmt.Sprintf("%.0f MB  →  ~%.0f MB   %s(+%.0f MB groesser)%s",
				quelleMB, prognoseMB, farbeRot, -gespart, farbeAus)
		}
		return fmt.Sprintf("%.0f MB  →  ~%.0f MB   %s(–%.0f MB / %.0f %% kleiner)%s",
			quelleMB, prognoseMB, farbeGruen, gespart, gespart/quelleMB*100, farbeAus)
	case quelleMB > 0:
		return fmt.Sprintf("%.0f MB  →  %s...%s", quelleMB, farbeGrau, farbeAus)
	default:
		return ""
	}
}

func kopfZeile(breite int, rechts string) string {
	links := " " + farbeFett + "CloudForge " + appVersion + farbeAus
	luecke := max(breite-2-sichtbareLaenge(links)-len(rechts), 2)
	return links + strings.Repeat(" ", luecke) + rechts
}

func hauptBalken(anteil float64, breite int) string {
	laenge := max(min(hauptBalkenMax, breite-16), hauptBalkenMin)
	return fmt.Sprintf("  [%s]  %s", balkenText(anteil, laenge, farbeGruen),
		farbeWeissFett+prozentText(anteil)+farbeAus)
}

func balkenText(anteil float64, laenge int, farbe string) string {
	voll := int(begrenzt(anteil) * float64(laenge))
	return farbe + strings.Repeat("█", voll) + farbeGrau + strings.Repeat("░", laenge-voll) + farbeAus
}

func prozentText(anteil float64) string {
	return fmt.Sprintf("%5s %%", komma(begrenzt(anteil)*100, 1))
}

func marke(text string, breite int) string {
	return farbeCyan + fmt.Sprintf("%-*s", breite, text) + farbeAus
}

// zeitText schreibt eine Dauer als Uhrzeit: 0:24:47.
func zeitText(d time.Duration) string {
	sek := max(int(d.Seconds()), 0)
	return fmt.Sprintf("%d:%02d:%02d", sek/3600, sek/60%60, sek%60)
}

func restText(d time.Duration) string {
	if d <= 0 {
		return "-:--"
	}
	return zeitText(d)
}

func sekunden(s float64) time.Duration {
	return time.Duration(s * float64(time.Second))
}

func bitrateText(kbps float64) string {
	switch {
	case kbps <= 0:
		return "-"
	case kbps >= 1000:
		return komma(kbps/1000, 1) + " Mbit"
	default:
		return fmt.Sprintf("%.0f kbit", kbps)
	}
}

// glaettePrognose glättet die Hochrechnung der Ergebnisgrösse. Die ersten
// Werte zappeln stark (der Muxer schreibt schubweise), deshalb wird gemischt:
// je weiter der Lauf, desto stärker zählt der neue Wert, am Ende ganz.
// Übernommen aus NVENCForge (smoothOutputEstimate) — dort gemessen: ein festes
// Gewicht liess kurze Läufe an ihrer viel zu niedrigen Anfangsschätzung kleben.
func glaettePrognose(bisher, neu, anteil float64) float64 {
	if bisher <= 0 {
		return neu
	}
	gewicht := math.Min(math.Max(anteil, 0.15), 1)
	return bisher*(1-gewicht) + neu*gewicht
}

// letzte liefert die letzten n Zeilen.
func letzte(zeilen []string, n int) []string {
	if n <= 0 {
		return nil
	}
	if len(zeilen) <= n {
		return zeilen
	}
	return zeilen[len(zeilen)-n:]
}

// verlaufKuerzen passt den Verlauf der fertigen Dateien in den Platz ein,
// mit einer Leerzeile dahinter. Was nicht passt, wird als Anzahl genannt.
func verlaufKuerzen(verlauf []string, platz int) []string {
	if len(verlauf) == 0 || platz < 2 {
		return nil
	}
	if len(verlauf)+1 <= platz {
		return append(append([]string{}, verlauf...), "")
	}
	if platz < 3 {
		return nil
	}
	sichtbar := platz - 2
	hinweis := fmt.Sprintf("%s ... und %d weitere fertige Datei(en)%s", farbeGrau, len(verlauf)-sichtbar, farbeAus)
	return append(append([]string{hinweis}, verlauf[len(verlauf)-sichtbar:]...), "")
}

// fensterMasse liefert Breite und Höhe des Terminals. Lässt es sich nicht
// fragen (keine echte Konsole, Windows beim Testen), gelten 80 x 24.
func fensterMasse() (breite, hoehe int) {
	if b, h, ok := terminalGroesse(); ok {
		return b, h
	}
	return standardBreite, standardHoehe
}

// sichtbarKuerzen kürzt eine Zeile auf höchstens so viele sichtbare Zeichen.
// Farbcodes (\033[...m — andere Steuerfolgen kommen in Zeilen nicht vor)
// zählen nicht mit und werden nie zerschnitten. Wird gekürzt, setzt ein
// Farbcode am Ende die Farbe zurück, damit sie nicht in die nächste Zeile läuft.
func sichtbarKuerzen(zeile string, hoechstens int) string {
	if hoechstens < 1 {
		return ""
	}
	var ergebnis strings.Builder
	sichtbar := 0
	imCode := false
	for _, zeichen := range zeile {
		switch {
		case imCode:
			imCode = zeichen != 'm'
		case zeichen == '\033':
			imCode = true
		case sichtbar == hoechstens:
			ergebnis.WriteString(farbeAus)
			return ergebnis.String()
		default:
			sichtbar++
		}
		ergebnis.WriteRune(zeichen)
	}
	return ergebnis.String()
}

// sichtbareLaenge zählt die Zeichen, die man sieht — ohne Farbcodes.
func sichtbareLaenge(zeile string) int {
	anzahl := 0
	imCode := false
	for _, zeichen := range zeile {
		switch {
		case imCode:
			imCode = zeichen != 'm'
		case zeichen == '\033':
			imCode = true
		default:
			anzahl++
		}
	}
	return anzahl
}
