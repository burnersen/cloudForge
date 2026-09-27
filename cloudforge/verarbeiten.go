package main

// Der Ablauf für eine einzelne Datei, von der Cloud bis zurück in die Cloud.
//
// Die Reihenfolge ist bewusst so gewählt, dass das Original erst ganz zum
// Schluss angefasst wird — nach der vollständigen Prüfung und nachdem das
// Ergebnis nachweislich am Ziel angekommen ist.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"
)

// Die fünf Schritte, die in der Anzeige erscheinen.
const (
	schritteGesamt = 5
	schrittHolen   = 1
	schrittMessen  = 2
	schrittWandeln = 3
	schrittPruefen = 4
	schrittAblegen = 5
)

// Ablauf führt eine Datei durch alle Schritte.
type Ablauf struct {
	Einstellungen Einstellungen
	Zustand       *Zustand
	Anzeige       *Anzeige

	// Naechste ist die Datei, die danach drankommt (leer bei der letzten).
	// Während umgewandelt wird, holt der Ablauf sie schon (vorab.go).
	Naechste string
	vorab    *VorabKopie
}

// DateiErgebnis fasst zusammen, was bei einer Datei herauskam.
type DateiErgebnis struct {
	Eintrag      Eintrag
	Dauer        time.Duration
	ErgebnisPfad string // wo das Ergebnis jetzt liegt
	OriginalPfad string // wo das Original jetzt liegt, leer wenn gelöscht
}

// autoCQText fasst die Wahl von Auto-CQ für die Anzeige zusammen.
func autoCQText(a AutoCQErgebnis) string {
	text := fmt.Sprintf("gewaehlt CRF %d, erwartet VMAF %s", a.CRF, komma(a.ErwarteterVMAF, 1))
	if a.AnteilQuelle > 0 {
		text += fmt.Sprintf(", ~%.0f %% der Quelle", a.AnteilQuelle*100)
	}
	if a.Gedeckelt {
		text += ", gedeckelt"
	}
	return text
}

// lohntNichtText erklärt, warum eine Datei nach der Qualitätsmessung nicht
// umgewandelt, sondern nur umgepackt wird.
func lohntNichtText(erwartetProzent, verlangtProzent float64) string {
	if erwartetProzent < 0 {
		return fmt.Sprintf("wuerde voraussichtlich %.0f%% GROESSER (Quelle schon stark komprimiert)",
			-erwartetProzent)
	}
	return fmt.Sprintf("voraussichtlich nur %.0f%% kleiner, verlangt sind %.0f%% (Quelle schon stark komprimiert)",
		erwartetProzent, verlangtProzent)
}

// umpackBytesProSek schätzt, wie schnell eine Datei durchläuft, die nur
// umgepackt wird: Das Holen aus der pCloud bestimmt das Tempo (gemessen am
// 25./26.09.2026: 19 bis 24 MB/s), das Umpacken selbst ist nur Kopieren.
const umpackBytesProSek = 20e6

// fehlerEintrag hält fest, woran eine Datei gescheitert ist. Ein Abbruch durch
// den Nutzer bekommt immer dieselbe Meldung, damit er erkannt wird (Abgebrochen).
func fehlerEintrag(quelleBytes int64, meldung string, err error) Eintrag {
	if errors.Is(err, ErrAbgebrochen) {
		meldung = ErrAbgebrochen.Error()
	} else if err != nil {
		meldung = meldung + ": " + err.Error()
	}
	return Eintrag{Status: StatusFehler, QuelleBytes: quelleBytes, Meldung: meldung}
}

// erfahrung liefert das bisher gemessene Tempo, ohne Zustand die Startwerte.
func (a *Ablauf) erfahrung() Erfahrung {
	if a.Zustand == nil {
		return startErfahrung
	}
	return a.Zustand.Erfahrung()
}

// Abgebrochen sagt, ob die Datei wegen eines Abbruchs durch den Nutzer
// nicht fertig wurde. So etwas wird nicht als Fehler vermerkt.
func (d DateiErgebnis) Abgebrochen() bool {
	return d.Eintrag.Status == StatusFehler && d.Eintrag.Meldung == ErrAbgebrochen.Error()
}

// EineDatei arbeitet eine Quelldatei vollständig ab.
func (a *Ablauf) EineDatei(ctx context.Context, quellPfad string) DateiErgebnis {
	beginn := time.Now()
	e := a.Einstellungen
	anz := a.Anzeige

	fertig := func(eintrag Eintrag) DateiErgebnis {
		return DateiErgebnis{Eintrag: eintrag, Dauer: time.Since(beginn)}
	}

	// Liegt diese Datei schon vorab geholt bereit? Was immer aus der Kopie
	// wird — übernommen oder nicht —, ihr Ordner verschwindet am Ende.
	vorab := a.vorabFuer(quellPfad)
	defer vorab.Verwerfen()

	// Lohnt sich die Datei überhaupt? Was sich nicht umzuwandeln lohnt, aber
	// eine brauchbare Videospur hat, wird seit 0.11.0 umgepackt statt liegen
	// gelassen — wie in NVENCForge.
	kandidat := KandidatPruefen(quellPfad, e)
	if kandidat.Fehler != nil {
		return fertig(Eintrag{Status: StatusFehler, Meldung: kandidat.Fehler.Error()})
	}
	if !kandidat.Lohnt() && !kandidat.NurUmpacken() {
		return fertig(Eintrag{
			Status:      StatusUebersprungen,
			QuelleBytes: kandidat.Info.GroesseBytes,
			Meldung:     string(kandidat.Grund),
		})
	}
	info := kandidat.Info

	bilder := info.DauerSek * info.FPS
	erfahrung := a.erfahrung()
	geschaetzt := erfahrung.DauerFuerBilder(bilder)
	if geschaetzt <= 0 { // Bildrate unbekannt
		geschaetzt = erfahrung.DauerFuerBytes(info.GroesseBytes)
	}
	vorhaben := "dauert etwa " + uhrText(geschaetzt)
	if kandidat.NurUmpacken() {
		geschaetzt = time.Duration(float64(info.GroesseBytes) / umpackBytesProSek * float64(time.Second))
		vorhaben = "wird nur umgepackt" // den Grund nennt umpackenUndAblegen
	}
	anz.Zeile("  %s  |  %dp mit %s Bildern/s  |  %s Film  |  %s",
		groesseText(info.GroesseBytes), info.Hoehe, komma(info.FPS, 0),
		uhrText(time.Duration(info.DauerSek)*time.Second), vorhaben)
	anz.DateiInfo(info.GroesseBytes, geschaetzt)

	fehler := func(meldung string, err error) DateiErgebnis {
		return fertig(fehlerEintrag(info.GroesseBytes, meldung, err))
	}

	// Ist genug Platz da? Sonst gar nicht erst anfangen. Eine vorab geholte
	// Kopie liegt schon auf der Platte und zählt nicht noch einmal.
	bedarf := info.GroesseBytes * 2
	if vorab != nil {
		bedarf = info.GroesseBytes
	}
	if err := PlatzPruefen(e.ArbeitsOrdner, bedarf, e); err != nil {
		return fehler("Platz", err)
	}

	// Ein eigener Arbeitsplatz für diese Datei, der am Ende zuverlässig
	// wieder verschwindet — auch wenn unterwegs etwas schiefgeht.
	arbeitsplatz, err := os.MkdirTemp(e.ArbeitsOrdner, arbeitsVorsilbe)
	if err != nil {
		return fehler("Arbeitsordner nicht anlegbar", err)
	}
	defer os.RemoveAll(arbeitsplatz)

	// 1. Holen. Ab hier wird nur noch lokal gearbeitet.
	anz.Schritt(schrittHolen, schritteGesamt, "Datei holen")
	lokaleQuelle := filepath.Join(arbeitsplatz, filepath.Base(quellPfad))
	woher, err := a.holen(ctx, vorab, quellPfad, lokaleQuelle)
	if err != nil {
		return fehler("Datei holen fehlgeschlagen", err)
	}
	anz.SchrittFertig(groesseText(info.GroesseBytes) + woher)

	umpacken := umpackAuftrag{quellPfad: quellPfad, lokaleQuelle: lokaleQuelle,
		arbeitsplatz: arbeitsplatz, info: info, beginn: beginn}

	// 2. Auto-CQ: den passenden CRF für genau diese Datei suchen — entfällt,
	//    wenn schon vorher feststeht, dass nur umgepackt wird.
	anz.Schritt(schrittMessen, schritteGesamt, "Qualitaet messen")
	if kandidat.NurUmpacken() {
		anz.SchrittFertig("entfaellt")
		umpacken.grund = string(kandidat.Grund)
		return a.umpackenUndAblegen(ctx, umpacken)
	}
	autoCQ, err := CRFFinden(ctx, lokaleQuelle, info, arbeitsplatz, e, anz.Messung)
	if err != nil {
		return fehler("Qualitaetsmessung fehlgeschlagen", err)
	}
	anz.SchrittFertig(autoCQText(autoCQ))
	if autoCQ.Gedeckelt || !autoCQ.ZielErreichbar || autoCQ.DeckelNichtEinhaltbar {
		anz.Zeile("  Hinweis: %s", autoCQ.Hinweis)
	}

	// Lohnt es sich? Aus den Messproben hochgerechnet — BEVOR eine halbe
	// Stunde gerechnet wird. Am 25.09.2026 lief eine dünne Quelle 20 Minuten,
	// bis die Anzeige zeigte, dass sie grösser wird. Dann nur umpacken. Knapp
	// an der Schwelle entscheidet seit 0.13.0 die genauere Grössenprobe.
	erwartet, ok, err := ersparnisVorhersagen(ctx, anz, schrittMessen, schritteGesamt,
		lokaleQuelle, info, autoCQ, arbeitsplatz, e)
	if err != nil {
		return fehler("Groessenprobe", err)
	}
	if ok && erwartet < e.MindestErsparnisProzent {
		umpacken.grund = lohntNichtText(erwartet, e.MindestErsparnisProzent)
		return a.umpackenUndAblegen(ctx, umpacken)
	}

	// 3. Die ganze Datei umwandeln. Die Notbremse verhindert, dass ein
	//    einzelnes Schwergewicht die Warteschlange tagelang blockiert.
	encodeCtx, aufgeben := context.WithTimeout(ctx, time.Duration(e.MaxStundenProDatei)*time.Hour)
	defer aufgeben()

	// Während umgewandelt wird, schon die nächste Datei holen — mit dem
	// Lauf-ctx, nicht mit encodeCtx: die Notbremse gilt nur dieser Datei.
	a.naechsteVorabHolen(ctx, info.GroesseBytes)

	anz.Schritt(schrittWandeln, schritteGesamt, "Umwandeln")
	lokalesErgebnis := filepath.Join(arbeitsplatz, "ergebnis.mkv")
	auftrag := EncodeAuftrag{
		Quelle:           lokaleQuelle,
		Ziel:             lokalesErgebnis,
		CRF:              autoCQ.CRF,
		UntertitelCodecs: info.UntertitelCodecs,
	}
	if err := Kodieren(encodeCtx, auftrag, e, info.DauerSek, anz.Stand); err != nil {
		return fehler("Umwandeln fehlgeschlagen", err)
	}
	ergebnisBytes := DateiGroesse(lokalesErgebnis)
	anz.SchrittFertig(fmt.Sprintf("%s statt %s", groesseText(ergebnisBytes), groesseText(info.GroesseBytes)))

	// Hat sich die Mühe gelohnt? Bei zu wenig Ersparnis wird das Umgewandelte
	// verworfen und stattdessen umgepackt — wie NVENCForge, wenn sein Ergebnis
	// nicht kleiner wurde. Die Quelle liegt ja noch hier, das geht schnell.
	// Das steht VOR der Prüfkette: die würde ein grösseres Ergebnis sonst als
	// Fehler werten, statt es durch das Umpacken zu ersetzen.
	gespartProzent := ProzentKleiner(info.GroesseBytes, ergebnisBytes)
	if gespartProzent < e.MindestErsparnisProzent {
		os.Remove(lokalesErgebnis)
		umpacken.grund = fmt.Sprintf("umgewandelt nur %.1f%% kleiner, verlangt sind %.0f%%",
			gespartProzent, e.MindestErsparnisProzent)
		return a.umpackenUndAblegen(ctx, umpacken)
	}

	// 4. Prüfkette. Erst wenn sie vollständig besteht, geht es weiter.
	anz.Schritt(schrittPruefen, schritteGesamt, "Pruefen")
	pruefung := ErgebnisPruefen(ctx, info, lokalesErgebnis, e, anz.Stand)
	if ctx.Err() != nil {
		return fehler("", ErrAbgebrochen)
	}
	if !pruefung.Bestanden {
		return fehler("Pruefung nicht bestanden - "+pruefung.ErsterFehler(), nil)
	}
	anz.SchrittFertig("alles in Ordnung")

	// 5. Ablegen, danach erst das Original.
	meldung, originalPfad, err := a.ablegen(ctx, lokalesErgebnis, kandidat.ZielPfad, quellPfad, ergebnisBytes)
	if err != nil {
		return fehler("Ablegen fehlgeschlagen", err)
	}
	ergebnis := fertig(Eintrag{
		Status:        StatusErledigt,
		QuelleBytes:   info.GroesseBytes,
		ErgebnisBytes: ergebnisBytes,
		CRF:           autoCQ.CRF,
		VMAF:          autoCQ.ErwarteterVMAF,
		Meldung:       meldung,
		Bilder:        int64(math.Round(bilder)),
		RechenSek:     time.Since(beginn).Seconds(),
	})
	ergebnis.ErgebnisPfad = kandidat.ZielPfad
	ergebnis.OriginalPfad = originalPfad
	return ergebnis
}

// ablegen bringt ein fertiges Ergebnis in den output-Ordner — unter
// Tarnnamen, erst am Ende umbenannt — und räumt danach das Original weg.
// Scheitert nur das Wegräumen, ist die Datei trotzdem fertig: das Ergebnis
// liegt dann schon sicher am Ziel, meldung sagt es.
func (a *Ablauf) ablegen(ctx context.Context, lokalesErgebnis, zielPfad, quellPfad string, ergebnisBytes int64) (meldung, originalPfad string, err error) {
	anz := a.Anzeige
	anz.Schritt(schrittAblegen, schritteGesamt, "Ergebnis ablegen")
	if err := Kopieren(ctx, lokalesErgebnis, zielPfad, anz.Stand); err != nil {
		return "", "", err
	}
	if angekommen := DateiGroesse(zielPfad); angekommen != ergebnisBytes {
		os.Remove(zielPfad)
		return "", "", fmt.Errorf("am Ziel kamen %d statt %d Bytes an - Ergebnis verworfen", angekommen, ergebnisBytes)
	}

	was, neuerOrt, err := OriginalWegraeumen(quellPfad, a.Einstellungen)
	if err != nil {
		meldung, neuerOrt = "fertig, aber das Original blieb liegen: "+err.Error(), quellPfad
	} else {
		meldung = was
	}
	anz.SchrittFertig(meldung)
	return meldung, neuerOrt, nil
}

// umpackAuftrag beschreibt eine schon geholte Datei, die nur umgepackt wird.
type umpackAuftrag struct {
	quellPfad    string // in der Cloud
	lokaleQuelle string // die geholte Kopie im Arbeitsplatz
	arbeitsplatz string
	info         VideoInfo
	grund        string // warum sich das Umwandeln nicht lohnt
	beginn       time.Time
}

// umpackenUndAblegen packt die geholte Datei verlustfrei nach MKV um, prüft
// das Ergebnis, legt es in den output-Ordner und räumt das Original weg — wie
// NVENCForge, wenn sich das Umwandeln nicht lohnt (Nutzerwunsch 26.09.2026).
// Das Bild bleibt Bit für Bit gleich, nur der Behälter wechselt. Danach gilt
// die Datei über ihr Ergebnis als erledigt und wird nie wieder gemessen.
func (a *Ablauf) umpackenUndAblegen(ctx context.Context, u umpackAuftrag) DateiErgebnis {
	e, anz := a.Einstellungen, a.Anzeige
	fertig := func(eintrag Eintrag) DateiErgebnis {
		return DateiErgebnis{Eintrag: eintrag, Dauer: time.Since(u.beginn)}
	}
	fehler := func(meldung string, err error) DateiErgebnis {
		return fertig(fehlerEintrag(u.info.GroesseBytes, meldung, err))
	}

	// Versteckter Vorlauf (ohne Neukodieren geschnittene MP4) lässt sich nicht
	// verlustfrei nach MKV bringen — dann bleibt das Original, wie vor 0.11.0.
	versteckt, err := VorlaufVersteckt(e.FFprobePfad, u.lokaleQuelle)
	if err != nil {
		return fehler("Vor dem Umpacken", err)
	}
	if versteckt {
		return fertig(Eintrag{
			Status:      StatusUebersprungen,
			QuelleBytes: u.info.GroesseBytes,
			Meldung: "lohnt nicht umzuwandeln: " + u.grund + " - und verlustfrei umpacken geht nicht: " +
				"die Datei beginnt mit verstecktem Vorlauf (ohne Neukodieren geschnitten) - Original bleibt",
		})
	}
	anz.Zeile("  Lohnt nicht umzuwandeln: %s - wird verlustfrei nach MKV umgepackt.", u.grund)

	// Auch hier schon die nächste Datei holen; läuft das schon, bleibt es dabei.
	a.naechsteVorabHolen(ctx, u.info.GroesseBytes)

	// 3. Umpacken: Bild unverändert, Ton und Untertitel wie beim Umwandeln.
	anz.Schritt(schrittWandeln, schritteGesamt, "Umpacken")
	lokalesErgebnis := filepath.Join(u.arbeitsplatz, "umgepackt.mkv")
	auftrag := EncodeAuftrag{Quelle: u.lokaleQuelle, Ziel: lokalesErgebnis,
		UntertitelCodecs: u.info.UntertitelCodecs, Umpacken: true}
	if err := Kodieren(ctx, auftrag, e, u.info.DauerSek, anz.Stand); err != nil {
		return fehler("Umpacken fehlgeschlagen", err)
	}
	ergebnisBytes := DateiGroesse(lokalesErgebnis)
	anz.SchrittFertig(fmt.Sprintf("%s statt %s", groesseText(ergebnisBytes), groesseText(u.info.GroesseBytes)))

	// 4. Dieselbe Prüfkette wie beim Umwandeln, nur ohne "kleiner geworden".
	anz.Schritt(schrittPruefen, schritteGesamt, "Pruefen")
	pruefung := UmpackErgebnisPruefen(ctx, u.info, lokalesErgebnis, e, anz.Stand)
	if ctx.Err() != nil {
		return fehler("", ErrAbgebrochen)
	}
	if !pruefung.Bestanden {
		return fehler("Pruefung nicht bestanden - "+pruefung.ErsterFehler(), nil)
	}
	anz.SchrittFertig("alles in Ordnung")

	// 5. Ablegen, danach erst das Original.
	zielPfad := UmpackPfadFuer(u.quellPfad, u.info.VideoCodec, e)
	meldung, originalPfad, err := a.ablegen(ctx, lokalesErgebnis, zielPfad, u.quellPfad, ergebnisBytes)
	if err != nil {
		return fehler("Ablegen fehlgeschlagen", err)
	}
	ergebnis := fertig(Eintrag{
		Status:        StatusUmgepackt,
		QuelleBytes:   u.info.GroesseBytes,
		ErgebnisBytes: ergebnisBytes,
		Meldung:       meldung,
		RechenSek:     time.Since(u.beginn).Seconds(),
	})
	ergebnis.ErgebnisPfad = zielPfad
	ergebnis.OriginalPfad = originalPfad
	return ergebnis
}
