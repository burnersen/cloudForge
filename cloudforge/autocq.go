// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

// Auto-CQ: findet für jede Datei den CRF-Wert, der das Qualitätsziel gerade
// noch hält und dabei so wenig Platz wie möglich braucht.
//
// Der Weg in Kurzform:
//  1. Ein paar Ausschnitte aus der Datei verlustfrei zwischenspeichern.
//  2. Zwei Anker-CRF kodieren und messen — daraus ergibt sich die Steigung
//     der Qualitätskurve für genau dieses Material.
//  3. Auf das Ziel hochrechnen und den gefundenen Wert gegenmessen.
//  4. Das Ziel ist eine UNTERGRENZE (seit 0.7.0, Wunsch des Nutzers): Liegt
//     die Gegenmessung darunter, wird feiner nachgemessen, bis ein Wert das
//     Ziel hält. Liegt sie deutlich darüber, wird eine Stufe sparsamer
//     versucht — genommen nur, wenn auch die das Ziel hält.
//  5. Ist das Ziel gar nicht erreichbar (bei AV1 häufig, die Kurve läuft
//     flach aus), so weit hochklettern, wie es kaum Qualität kostet und
//     spürbar Platz spart.
//  6. Kosten-Deckel (seit 0.8.0, deckel.go): Kostet das Ergebnis mehr als
//     kostenDeckelProzent der Quelle, wird der CRF so weit erhöht, dass es
//     darunter bleibt — wie in NVENCForge.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Fenster ist ein Ausschnitt aus der Datei, an dem gemessen wird.
type Fenster struct {
	StartSek  float64
	LaengeSek float64
}

// Messung hält, was ein CRF-Wert an den Ausschnitten gebracht hat.
type Messung struct {
	CRF   int
	VMAF  float64
	Bytes int64
}

// MessBericht meldet den Verlauf der Suche: einmal, wenn eine Messung
// beginnt (vmaf < 0), und einmal mit ihrem Ergebnis. nil ist erlaubt.
type MessBericht func(crf int, vmaf float64)

// AutoCQErgebnis ist das Urteil für eine Datei.
type AutoCQErgebnis struct {
	CRF            int
	ErwarteterVMAF float64
	ZielErreichbar bool
	Hinweis        string
	Messungen      []Messung
	Dauer          time.Duration

	// AnteilQuelle: so gross wird das Bild im Verhältnis zur Quelle, gemessen
	// an den Messstellen (0,42 = 42 %). 0 heisst unbekannt.
	AnteilQuelle float64
	Gedeckelt    bool // der Kosten-Deckel hat die Qualität unter das Ziel gesenkt

	// DeckelNichtEinhaltbar: selbst der sparsamste erlaubte CRF kostet mehr als
	// der Deckel — die Qualitätswahl bleibt, Hinweis sagt warum. Seit 0.11.1
	// wird der Hinweis dann auch angezeigt (vorher sah man nur "115 % der
	// Quelle" ohne Grund, 26.09.2026).
	DeckelNichtEinhaltbar bool
}

// Um so viel darf die Gegenmessung ÜBER dem Ziel liegen, bevor eine Stufe
// sparsamer versucht wird. Nach unten gibt es keine Toleranz mehr: Bis 0.6.0
// wurden auch Werte bis 0,5 unter dem Ziel angenommen — oben ist die Kurve
// aber so flach, dass 0,5 VMAF dort drei bis vier CRF-Stufen sind (gemessen
// 25.09.2026: CRF 20 -> 98,2, CRF 24 -> 97,5), und genau diesen Unterschied
// sah der Nutzer.
const gegenmessungToleranz = 0.5

// Sicherheitsgrenze, damit die Suche nie endlos läuft. 12 reicht für zwei
// Anker, die Gegenmessung, das Nachmessen zur Untergrenze und die Suche unter
// den Kosten-Deckel. Eine Probe kostet auf dem netcup-Server 10-20 Sekunden.
const maxMessungen = 12

// FensterWaehlen verteilt die Messausschnitte über die Datei.
//
// Anfang und Ende bleiben bewusst aussen vor: dort stehen oft Vorspann,
// Abspann oder schwarze Bilder, die nichts über den Film aussagen.
func FensterWaehlen(dauerSek float64, anzahl int, laengeSek float64) []Fenster {
	if anzahl < 1 {
		anzahl = 1
	}

	// Passt die geplante Menge nicht in die Datei, wird sie verkleinert.
	nutzbar := dauerSek * 0.75
	for anzahl > 1 && float64(anzahl)*laengeSek > nutzbar {
		anzahl--
	}
	if laengeSek > nutzbar {
		laengeSek = nutzbar
	}

	// Gleichmässig zwischen 15 % und 80 % der Spieldauer verteilen.
	const von, bis = 0.15, 0.80
	fenster := make([]Fenster, 0, anzahl)
	for i := 0; i < anzahl; i++ {
		anteil := von
		if anzahl > 1 {
			anteil = von + (bis-von)*float64(i)/float64(anzahl-1)
		}
		start := dauerSek * anteil
		if start+laengeSek > dauerSek {
			start = dauerSek - laengeSek
		}
		if start < 0 {
			start = 0
		}
		fenster = append(fenster, Fenster{StartSek: start, LaengeSek: laengeSek})
	}
	return fenster
}

// CRFFinden ist der Einstiegspunkt: es legt die Messausschnitte an und sucht
// den passenden CRF-Wert. arbeitsOrdner nimmt die Zwischendateien auf.
func CRFFinden(ctx context.Context, quelle string, info VideoInfo, arbeitsOrdner string, e Einstellungen, bericht MessBericht) (AutoCQErgebnis, error) {
	beginn := time.Now()

	fenster := FensterWaehlen(info.DauerSek, e.MessfensterAnzahl, e.MessfensterSek)
	referenz := filepath.Join(arbeitsOrdner, "messreferenz.mkv")
	defer os.Remove(referenz)

	if err := ProbeSchneiden(ctx, quelle, referenz, fenster, verkleinernFilter(info, e), e); err != nil {
		return AutoCQErgebnis{}, err
	}

	// Für den Kosten-Deckel: wie gross ist die Quelle genau an diesen Stellen?
	// Lässt sich das nicht lesen, läuft die Suche ohne Deckel weiter — die
	// Prüfung nach dem Umwandeln verhindert trotzdem ein zu grosses Ergebnis.
	quelleBytes, err := QuelleFensterBytes(ctx, e.FFprobePfad, quelle, fenster)
	if errors.Is(err, ErrAbgebrochen) {
		return AutoCQErgebnis{}, err
	}

	breite, hoehe, _ := ergebnisMasseFuer(info, e)
	sucher := &crfSuche{
		ctx:         ctx,
		e:           e,
		bericht:     bericht,
		probe:       echteProbe(ctx, referenz, arbeitsOrdner, breite, hoehe, e),
		quelleBytes: quelleBytes,
	}
	ergebnis, err := sucher.bestimmen()
	if err == nil && quelleBytes <= 0 && e.KostenDeckelProzent > 0 {
		ergebnis.Hinweis = strings.TrimSpace(ergebnis.Hinweis + " Quellgroesse an den Messstellen unbekannt - ohne Kosten-Deckel.")
	}
	ergebnis.Dauer = time.Since(beginn)
	return ergebnis, err
}

// bestimmen sucht erst die Qualität und wendet dann den Kosten-Deckel an —
// bei offensichtlich dünnen Quellen gleich den Deckel (deckelSprung).
func (s *crfSuche) bestimmen() (AutoCQErgebnis, error) {
	if ergebnis, gesprungen, err := s.deckelSprung(); err != nil || gesprungen {
		return ergebnis, err
	}
	ergebnis, err := s.suchen()
	if err != nil {
		return ergebnis, err
	}
	return s.deckeln(ergebnis)
}

// probeMessung kodiert die Messausschnitte mit einem CRF und misst sie.
type probeMessung func(crf int) (vmaf float64, bytes int64, err error)

// echteProbe misst mit ffmpeg an der Vergleichsdatei. breite und hoehe sind
// die Masse des Ergebnisses (nach maxAufloesung) — danach richtet sich, ob für
// die Messung vergrössert wird.
func echteProbe(ctx context.Context, referenz, ordner string, breite, hoehe int, e Einstellungen) probeMessung {
	return func(crf int) (float64, int64, error) {
		probe := filepath.Join(ordner, fmt.Sprintf("probe_crf%d.mkv", crf))
		defer os.Remove(probe)

		auftrag := EncodeAuftrag{Quelle: referenz, Ziel: probe, CRF: crf, NurVideo: true}
		if err := Kodieren(ctx, auftrag, e, 0, nil); err != nil {
			return 0, 0, err
		}
		wert, err := VMAFMessen(ctx, probe, referenz, breite, hoehe, e)
		if err != nil {
			return 0, 0, err
		}
		return wert, DateiGroesse(probe), nil
	}
}

// crfSuche hält den Zustand einer laufenden Suche zusammen.
type crfSuche struct {
	ctx       context.Context
	e         Einstellungen
	bericht   MessBericht
	messungen []Messung

	// probe ist im Betrieb echteProbe, in den Tests eine erfundene Kurve —
	// so lässt sich die Suche prüfen, ohne Stunden mit ffmpeg zu rechnen.
	probe probeMessung

	// quelleBytes: Grösse der Videospur der Quelle an den Messstellen, für
	// den Kosten-Deckel (deckel.go). 0 = unbekannt, dann kein Deckel.
	quelleBytes int64
}

func (s *crfSuche) melden(crf int, vmaf float64) {
	if s.bericht != nil {
		s.bericht(crf, vmaf)
	}
}

// messen kodiert die Ausschnitte mit einem CRF-Wert und misst die Qualität.
// Bereits gemessene Werte werden wiederverwendet.
func (s *crfSuche) messen(crf int) (Messung, error) {
	for _, m := range s.messungen {
		if m.CRF == crf {
			return m, nil
		}
	}
	if len(s.messungen) >= maxMessungen {
		return Messung{}, errZuVieleMessungen
	}

	s.melden(crf, -1)
	wert, bytes, err := s.probe(crf)
	if err != nil {
		return Messung{}, err
	}

	m := Messung{CRF: crf, VMAF: wert, Bytes: bytes}
	s.messungen = append(s.messungen, m)
	s.melden(crf, wert)
	return m, nil
}

// errZuVieleMessungen ist kein Fehler der Datei: die Suche nimmt dann das
// Beste, was sie bis dahin gefunden hat.
var errZuVieleMessungen = fmt.Errorf("Suche bricht nach %d Messungen ab", maxMessungen)

// suchen führt die eigentliche Suche durch.
func (s *crfSuche) suchen() (AutoCQErgebnis, error) {
	niedrig, err := s.messen(s.e.AnkerNiedrig)
	if err != nil {
		return AutoCQErgebnis{}, err
	}
	hoch, err := s.messen(s.e.AnkerHoch)
	if err != nil {
		return AutoCQErgebnis{}, err
	}

	// Liegt schon der bessere Anker unter dem Ziel, ist das Ziel mit diesem
	// Material nicht zu erreichen. Dann zählt nur noch, möglichst viel Platz
	// zu sparen, ohne das Bild spürbar zu verschlechtern.
	if niedrig.VMAF < s.e.ZielVMAF {
		return s.plateauWeg(niedrig, hoch)
	}

	gewaehlt := interpolieren(niedrig, hoch, s.e.ZielVMAF)
	gewaehlt = rundeAuf(gewaehlt, s.e.CRFMin, s.e.CRFMax)

	gemessen, err := s.messen(gewaehlt)
	if err != nil {
		return AutoCQErgebnis{}, err
	}

	switch {
	case gemessen.VMAF < s.e.ZielVMAF:
		// Das Ziel ist eine Untergrenze: so lange feiner nachmessen, bis ein
		// Wert es hält.
		if gemessen, err = s.einschachteln(s.e.ZielVMAF, gemessen); err != nil {
			return AutoCQErgebnis{}, err
		}
	case gemessen.VMAF-s.e.ZielVMAF > gegenmessungToleranz:
		// Deutlich besser als verlangt: einmal sparsamer versuchen. Mehr als
		// eine Stufe lohnt nicht — jede kostet einen kompletten Probelauf.
		if sparsamer, ok := s.sparsamerVersuchen(gemessen); ok {
			gemessen = sparsamer
		}
	}

	return AutoCQErgebnis{
		CRF:            gemessen.CRF,
		ErwarteterVMAF: gemessen.VMAF,
		ZielErreichbar: true,
		Hinweis:        s.hinweisZurWahl(gemessen),
		Messungen:      s.messungen,
	}, nil
}

// einschachteln sucht den sparsamsten CRF, der die grenze noch hält, nachdem
// die Messung unten darunter lag. Die grenze ist das Ziel — oder im
// plateauWeg der Boden knapp unter der besten erreichbaren Qualität.
//
// Warum nicht einfach Stufe für Stufe feiner: Oben ist die Kurve flach und
// krumm, eine Stufe bringt dort nur 0,1 bis 0,2 VMAF. Stattdessen wird
// zwischen dem sparsamsten bekannten Punkt ÜBER der Grenze und dem besten
// darunter neu hochgerechnet; jede Messung engt den Bereich ein, bis keine
// Stufe mehr dazwischen liegt. Einen Punkt über der Grenze gibt es immer:
// beim Ziel den besseren Anker (sonst wäre die Suche im plateauWeg), beim
// Boden ebenfalls den besseren Anker, von dem aus der Boden gerechnet ist.
//
// Zwei Sicherungen gegen eine endlose Suche: Die Klemme hält jeden neuen Wert
// streng zwischen den beiden Punkten, so wird der Bereich jede Runde kleiner.
// Die Rundengrenze ist das Netz darunter — schon gemessene CRF kommen aus dem
// Speicher und zählen nicht gegen maxMessungen. Ohne Klemme lief die Suche
// am 25.09.2026 bei einem absichtlich eingebauten Fehler tatsächlich ewig.
//
// Illinois-Verfahren (seit 0.10.0): Rückt nur der obere Punkt nach und der
// untere bleibt stehen, wird für die nächste Rechnung der Abstand des unteren
// zur Grenze halbiert. Ohne das kriecht die Suche bei krummen Kurven eine
// Stufe je Probe heran — gemessen am 26.09.2026 bei leichtem Material:
// 29, 30, 31, 32, weil der ferne Punkt (CRF 44) die Rechnung festhielt.
func (s *crfSuche) einschachteln(grenze float64, unten Messung) (Messung, error) {
	rechenUnten := unten // der untere Punkt, wie er in die Rechnung eingeht
	for runde := 0; runde < maxMessungen; runde++ {
		oben, ok := s.sparsamsterUeber(grenze)
		if !ok || unten.CRF-oben.CRF <= 1 {
			break
		}

		naechster := rundeAuf(interpolieren(oben, rechenUnten, grenze), oben.CRF+1, unten.CRF-1)
		neu, err := s.messen(naechster)
		switch {
		case s.ctx.Err() != nil:
			return Messung{}, ErrAbgebrochen
		case err != nil:
			// Grenze erreicht oder Probe gescheitert: der sichere Punkt über
			// der Grenze gilt — er kostet höchstens etwas Platz.
			return oben, nil
		case neu.VMAF < grenze:
			unten, rechenUnten = neu, neu
		default:
			// Hält der neue Wert die Grenze, findet sparsamsterUeber ihn in
			// der nächsten Runde von selbst; der untere Punkt blieb stehen.
			rechenUnten.VMAF = grenze + (rechenUnten.VMAF-grenze)/2
		}
	}

	if oben, ok := s.sparsamsterUeber(grenze); ok {
		return oben, nil
	}
	return unten, nil // nur zur Sicherheit, siehe oben — nie eine leere Messung (CRF 0) liefern
}

// sparsamsterUeber liefert unter allen Messungen, die die grenze halten, die
// mit dem höchsten CRF — also der kleinsten Datei.
func (s *crfSuche) sparsamsterUeber(grenze float64) (Messung, bool) {
	var beste Messung
	gefunden := false
	for _, m := range s.messungen {
		if m.VMAF >= grenze && (!gefunden || m.CRF > beste.CRF) {
			beste, gefunden = m, true
		}
	}
	return beste, gefunden
}

// sparsamerVersuchen geht eine Stufe sparsamer, wenn die Gegenmessung das Ziel
// deutlich übertrifft. Genommen wird die Stufe nur, wenn sie das Ziel hält
// und näher daran liegt.
func (s *crfSuche) sparsamerVersuchen(bisher Messung) (Messung, bool) {
	naechster := rundeAuf(bisher.CRF+1, s.e.CRFMin, s.e.CRFMax)
	if naechster == bisher.CRF {
		return bisher, false
	}

	neu, err := s.messen(naechster)
	if err != nil || neu.VMAF < s.e.ZielVMAF {
		return bisher, false
	}
	if abweichung(neu.VMAF, s.e.ZielVMAF) < abweichung(bisher.VMAF, s.e.ZielVMAF) {
		return neu, true
	}
	return bisher, false
}

// plateauWeg greift, wenn das Ziel unerreichbar ist.
//
// Bei AV1 läuft die Qualitätskurve nach oben flach aus: Ab einem gewissen
// Punkt bringt ein niedrigerer CRF fast keine Qualität mehr, kostet aber
// deutlich Platz. Statt sinnlos immer weiter herunterzugehen, wird von der
// besten erreichbaren Qualität aus so weit aufgestiegen, wie es
// plateauToleranz erlaubt — höchstens bis zum sparsamen Anker, und nur,
// wenn der Aufstieg auch wirklich Platz spart.
//
// Seit 0.10.0 springt der Aufstieg: Bis 0.9.0 wurde Stufe für Stufe gemessen,
// am 26.09.2026 bei 1080p50-Filmen 17, 18, 19, 20, 21 — bis zu fünf Proben à 50 s.
// Jetzt wird wie beim Nachmessen unter dem Ziel auf den Boden hochgerechnet
// und eingegrenzt. An den echten Kurven dieses Tages kommt dieselbe Stufe
// heraus, meist mit zwei Proben.
func (s *crfSuche) plateauWeg(niedrig, hoch Messung) (AutoCQErgebnis, error) {
	boden := niedrig.VMAF - s.e.PlateauToleranz
	rand, err := s.plateauRand(boden, niedrig, hoch)
	if err != nil {
		return AutoCQErgebnis{}, err
	}

	beste := niedrig
	if aufstiegLohnt(niedrig, rand, s.e.PlateauMindestSpa) {
		beste = rand
	}

	hinweis := fmt.Sprintf(
		"Ziel %s ist mit diesem Material nicht erreichbar (bestenfalls %s bei CRF %d). "+
			"Gewaehlt CRF %d mit %s — spart %.0f %% gegenueber dem besten Wert.",
		komma(s.e.ZielVMAF, 1), komma(niedrig.VMAF, 1), niedrig.CRF,
		beste.CRF, komma(beste.VMAF, 1), ProzentKleiner(niedrig.Bytes, beste.Bytes))

	return AutoCQErgebnis{
		CRF:            beste.CRF,
		ErwarteterVMAF: beste.VMAF,
		ZielErreichbar: false,
		Hinweis:        hinweis,
		Messungen:      s.messungen,
	}, nil
}

// plateauRand sucht die sparsamste Stufe zwischen den Ankern, die den Boden
// noch hält — nie über crfMax hinaus.
func (s *crfSuche) plateauRand(boden float64, niedrig, hoch Messung) (Messung, error) {
	obersteCRF := min(hoch.CRF, s.e.CRFMax)
	if obersteCRF <= niedrig.CRF {
		return niedrig, nil
	}

	oberste, err := s.messen(obersteCRF) // meist der sparsame Anker, dann aus dem Speicher
	switch {
	case s.ctx.Err() != nil:
		return Messung{}, ErrAbgebrochen
	case err != nil:
		return niedrig, nil // Probe gescheitert: die beste Qualität gilt
	case oberste.VMAF >= boden:
		return oberste, nil // der ganze Weg bis dorthin hält den Boden
	}
	return s.einschachteln(boden, oberste)
}

func (s *crfSuche) hinweisZurWahl(gewaehlt Messung) string {
	return fmt.Sprintf("CRF %d trifft %.2f (Ziel %.1f), aus %d Messungen.",
		gewaehlt.CRF, gewaehlt.VMAF, s.e.ZielVMAF, len(s.messungen))
}

// interpolieren rechnet aus zwei Messpunkten den CRF-Wert für das Ziel hoch.
//
// Zwischen zwei nah beieinander liegenden Ankern verläuft die Kurve nahezu
// gerade, deshalb genügt eine lineare Rechnung. Gegengemessen wird trotzdem.
func interpolieren(niedrig, hoch Messung, ziel float64) int {
	spanneCRF := float64(hoch.CRF - niedrig.CRF)
	spanneVMAF := hoch.VMAF - niedrig.VMAF

	// Fast waagerechte Kurve: jede Rechnung würde ins Leere laufen.
	if spanneCRF == 0 || abweichung(spanneVMAF, 0) < 0.01 {
		return hoch.CRF
	}

	steigung := spanneVMAF / spanneCRF // VMAF je CRF-Stufe, normalerweise negativ
	crf := float64(niedrig.CRF) + (ziel-niedrig.VMAF)/steigung

	return int(crf + 0.5)
}

// aufstiegLohnt sagt, ob der Aufstieg von von nach bis genug Platz spart: je
// CRF-Stufe im Mittel mindestens mindestProzent. Bis 0.9.0 musste jede
// einzelne Stufe das schaffen; seit der Aufstieg springt, werden die Stufen
// dazwischen nicht mehr gemessen — der Mittelwert ist für eine gleichmässig
// fallende Grösse dieselbe Schwelle.
func aufstiegLohnt(von, bis Messung, mindestProzent float64) bool {
	stufen := bis.CRF - von.CRF
	if stufen <= 0 || von.Bytes <= 0 || bis.Bytes <= 0 {
		return false
	}
	jeStufe := 1 - math.Pow(float64(bis.Bytes)/float64(von.Bytes), 1/float64(stufen))
	const rundungsSpiel = 1e-9 // "genau 5 %" soll nicht an der letzten Kommastelle scheitern
	return jeStufe*100+rundungsSpiel >= mindestProzent
}

func abweichung(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}
