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

// uebersicht hält alles, was die Fläche zeigt, zwischen zwei Zeichnungen fest.
// Geschützt über den Mutex der Anzeige.
type uebersicht struct {
	dateiNummer int
	dateiGesamt int
	dateiName   string
	dateiBeginn time.Time
	dateiZeilen []string // Info-Zeile und fertige Schritte der laufenden Datei
	verlauf     []string // je fertige Datei eine Zeile
	protokoll   []string // wird nach dem Schliessen als Text ausgegeben

	stand            Stand
	standDa          bool
	umwandelnVorbei  bool    // die Datei ist schon hinter dem Umwandeln
	laufendeMessung  int     // CRF der gerade laufenden Messung, 0 = keine
	prognoseBytes    float64 // geglättete Hochrechnung der Ergebnisgrösse
	quelleBytes      int64
	dateiSchaetzung  time.Duration // für die ganze laufende Datei
	danachSchaetzung time.Duration // für alle Dateien nach dieser

	stopp   chan struct{}
	beendet chan struct{}
}

func (u *uebersicht) neueDatei(nummer, gesamt int, name string) {
	u.dateiNummer, u.dateiGesamt, u.dateiName = nummer, gesamt, name
	u.dateiBeginn = time.Now()
	u.dateiZeilen = nil
	u.quelleBytes, u.dateiSchaetzung = 0, 0
	u.umwandelnVorbei = false
	u.neuerSchritt()
	u.protokoll = append(u.protokoll, "", fmt.Sprintf("[%d/%d] %s", nummer, gesamt, name))
}

func (u *uebersicht) neuerSchritt() {
	if u.standDa && istUmwandeln(u.stand) {
		u.umwandelnVorbei = true
	}
	u.stand, u.standDa = Stand{}, false
	u.laufendeMessung = 0
	u.prognoseBytes = 0
}

func (u *uebersicht) standMerken(s Stand) {
	u.stand, u.standDa = s, true
	if s.Bytes > 0 && s.Anteil >= 0.01 {
		u.prognoseBytes = glaettePrognose(u.prognoseBytes, float64(s.Bytes)/s.Anteil, s.Anteil)
	}
}

// dateiZeile nimmt eine Zeile in den Block der laufenden Datei und ins
// Protokoll auf. Leerzeilen gliedern nur das Protokoll.
func (u *uebersicht) dateiZeile(zeile string) {
	u.protokoll = append(u.protokoll, zeile)
	if u.dateiName != "" && strings.TrimSpace(zeile) != "" {
		u.dateiZeilen = append(u.dateiZeilen, zeile)
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
	if u := a.uebersicht; u != nil {
		u.quelleBytes, u.dateiSchaetzung = quelleBytes, geschaetzt
	}
}

// Warteschlange nennt die geschätzte Dauer aller Dateien nach der laufenden.
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
	u.verlauf = append(u.verlauf, fmt.Sprintf(" %s %s  %s", marke, u.dateiName, kurz))
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
func (a *Anzeige) uebersichtZeilen(breite, hoehe int, jetzt time.Time) []string {
	u := a.uebersicht

	kopf := []string{
		kopfZeile(breite, u),
		farbeGrau + " " + strings.Repeat("-", max(breite-3, 1)) + farbeAus,
	}

	var dateiKopf, dateiZeilen []string
	if u.dateiName != "" {
		dateiKopf = []string{fmt.Sprintf(" %s>> [%d/%d] %s%s",
			farbeFett, u.dateiNummer, u.dateiGesamt, u.dateiName, farbeAus)}
		dateiZeilen = append(dateiZeilen, u.dateiZeilen...)
		if a.schrittLaeuft {
			dateiZeilen = append(dateiZeilen,
				fmt.Sprintf("%s  %-*s laeuft ...%s", farbeGelb, schrittSpalte, a.schritt, farbeAus))
		}
	}

	block := a.fortschrittsBlock(breite, jetzt)
	if len(block) > 0 {
		block = append([]string{""}, block...)
	}
	fuss := []string{"", farbeGrau + " Aufhoeren: Fenster schliessen oder Strg+C - es geht nichts verloren." + farbeAus}

	platz := hoehe - len(kopf) - len(dateiKopf) - len(block) - len(fuss)
	dateiZeilen = letzte(dateiZeilen, platz)
	verlauf := verlaufKuerzen(u.verlauf, platz-len(dateiZeilen))

	zeilen := append([]string{}, kopf...)
	zeilen = append(zeilen, verlauf...)
	zeilen = append(zeilen, dateiKopf...)
	zeilen = append(zeilen, dateiZeilen...)
	zeilen = append(zeilen, block...)
	zeilen = append(zeilen, fuss...)
	if len(zeilen) > hoehe {
		zeilen = zeilen[:max(hoehe, 1)]
	}
	return zeilen
}

// fortschrittsBlock baut den unteren Teil, so nah an NVENCForge wie möglich:
// Balken, dann Position/Laufzeit/Rest, Bilder/s/Bitrate/Tempo und die
// Grössen-Prognose. Beim Kopieren gibt es nur Balken, Zeiten und Menge,
// beim Messen und Prüfen keinen Prozentwert.
func (a *Anzeige) fortschrittsBlock(breite int, jetzt time.Time) []string {
	u := a.uebersicht
	s := u.stand
	laufzeit := jetzt.Sub(a.beginn)

	var zeilen []string
	switch {
	case !a.schrittLaeuft:
	case u.standDa && istUmwandeln(s):
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
				marke("Bild", beschriftung), strconv.FormatInt(s.Bild, 10), u.groessenText()),
		)
	case u.standDa:
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
		if u.laufendeMessung > 0 {
			teile = append(teile, fmt.Sprintf("misst CRF %d ...", u.laufendeMessung))
		}
		if len(teile) > 0 {
			zeilen = append(zeilen, fmt.Sprintf("  %s %s", marke("Messungen", beschriftung), strings.Join(teile, "   ")))
		}
	}

	if gesamt := a.gesamtZeile(jetzt); gesamt != "" {
		zeilen = append(zeilen, gesamt)
	}
	return zeilen
}

// gesamtZeile zeigt den Stand über alle Dateien — nur, wenn es mehrere sind.
//
// Die Restzeit setzt sich zusammen aus dem Rest der laufenden Datei (beim
// Umwandeln live von ffmpeg, sonst aus der Schätzung) und der Schätzung für
// alle folgenden Dateien.
func (a *Anzeige) gesamtZeile(jetzt time.Time) string {
	u := a.uebersicht
	if u.dateiGesamt <= 1 || u.dateiName == "" {
		return ""
	}

	vergangen := jetzt.Sub(u.dateiBeginn)
	var dateiRest time.Duration
	switch {
	case u.umwandelnVorbei:
		// Prüfen und Ablegen dauern Sekunden — nichts mehr einzurechnen.
	case u.standDa && istUmwandeln(u.stand) && u.stand.Rest > 0:
		dateiRest = u.stand.Rest
	case u.dateiSchaetzung > vergangen:
		dateiRest = u.dateiSchaetzung - vergangen
	}

	dateiAnteil := 0.0
	if vergangen+dateiRest > 0 {
		dateiAnteil = float64(vergangen) / float64(vergangen+dateiRest)
	}
	anteil := (float64(u.dateiNummer-1) + dateiAnteil) / float64(u.dateiGesamt)

	rest := "-:--"
	if gesamt := dateiRest + u.danachSchaetzung; gesamt > 0 {
		rest = "ca. " + uhrText(gesamt)
	}
	return fmt.Sprintf("  %s [%s]  %s  %s  %s %s",
		farbeMagentaFett+fmt.Sprintf("%-*s", beschriftung, "Gesamt")+farbeAus,
		balkenText(anteil, gesamtBalken, farbeMagenta),
		farbeWeissFett+prozentText(anteil)+farbeAus,
		farbeGrau+fmt.Sprintf("(%d/%d)", u.dateiNummer, u.dateiGesamt)+farbeAus,
		farbeCyan+"noch"+farbeAus, farbeGelb+rest+farbeAus)
}

// groessenText zeigt, wie gross das Ergebnis voraussichtlich wird.
func (u *uebersicht) groessenText() string {
	quelleMB := float64(u.quelleBytes) / mebibyte
	prognoseMB := u.prognoseBytes / mebibyte

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

func kopfZeile(breite int, u *uebersicht) string {
	links := " " + farbeFett + "CloudForge " + appVersion + farbeAus
	rechts := ""
	if u.dateiGesamt > 0 {
		rechts = fmt.Sprintf("Datei %d von %d", u.dateiNummer, u.dateiGesamt)
	}
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
