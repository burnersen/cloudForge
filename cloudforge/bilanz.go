package main

// Was ist erledigt, und was hat es gebracht? Seit 0.11.0 so, wie der Nutzer es
// am 26.09.2026 wollte:
//   - Erledigt ist, wofür ein Ergebnis im output-Ordner liegt — keine Liste,
//     kein "Logbuch" (wie NVENCForge). Originale in "originals" und die
//     Ergebnisse selbst findet die Dateisuche gar nicht erst (scan.go).
//   - Am Ende steht, was DIESER Lauf geschafft hat, und darunter die
//     Gesamtbilanz über alle Läufe ("gesamt gesparte GB und benötigte Zeit
//     dafür"). Fehler früherer Läufe erscheinen nicht mehr: deren Originale
//     liegen noch da und kommen beim nächsten Lauf von selbst wieder dran.

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// nachOrdnernAufteilen trennt die gefundenen Dateien in offene und schon
// erledigte.
func nachOrdnernAufteilen(dateien []string, e Einstellungen) (offen, erledigt []string) {
	for _, pfad := range dateien {
		if VorhandenesErgebnis(pfad, e) != "" {
			erledigt = append(erledigt, pfad)
		} else {
			offen = append(offen, pfad)
		}
	}
	return offen, erledigt
}

// erledigteZeigen sagt, warum es nichts zu tun gibt — mit Namen und Ergebnis,
// damit sich niemand wundert, was aus seiner hineingezogenen Datei wurde.
func erledigteZeigen(anz *Anzeige, erledigt []string, e Einstellungen) {
	anz.Zeile("\n%d Datei(en) gefunden - alle schon umgewandelt, das Ergebnis liegt im Ordner %s:",
		len(erledigt), e.AusgabeOrdnerName)
	const hoechstens = 5
	for i, pfad := range erledigt {
		if i == hoechstens {
			anz.Zeile("  ... und %d weitere", len(erledigt)-hoechstens)
			break
		}
		anz.Zeile("  %s  ->  %s", filepath.Base(pfad), filepath.Base(VorhandenesErgebnis(pfad, e)))
	}
	anz.Zeile("Nochmal umwandeln: das Ergebnis im Ordner %s umbenennen oder loeschen.", e.AusgabeOrdnerName)
}

// laufBilanz zählt mit, was dieser Lauf geschafft hat.
type laufBilanz struct {
	umgewandelt, umgepackt, uebersprungen, fehler int
	gespartBytes                                  int64
	rechenzeit                                    time.Duration
	fehlerListe                                   []string
}

// dazu nimmt eine abgeschlossene Datei in die Bilanz des Laufs auf.
func (l *laufBilanz) dazu(pfad string, ergebnis DateiErgebnis) {
	eintrag := ergebnis.Eintrag
	l.rechenzeit += ergebnis.Dauer
	switch eintrag.Status {
	case StatusErledigt:
		l.umgewandelt++
		l.gespartBytes += eintrag.GespartBytes()
	case StatusUmgepackt:
		l.umgepackt++
		l.gespartBytes += eintrag.GespartBytes()
	case StatusUebersprungen:
		l.uebersprungen++
	case StatusFehler:
		l.fehler++
		l.fehlerListe = append(l.fehlerListe, fmt.Sprintf("%s — %s", filepath.Base(pfad), eintrag.Meldung))
	}
}

// zeigen gibt die Bilanz des Laufs aus.
func (l laufBilanz) zeigen(anz *Anzeige) {
	anz.Zeile(strings.Repeat("=", 78))
	anz.Zeile("Dieser Lauf:   %d umgewandelt, %d umgepackt, %d uebersprungen, %d mit Fehler",
		l.umgewandelt, l.umgepackt, l.uebersprungen, l.fehler)
	anz.Zeile("               %s gespart in %s", groesseText(l.gespartBytes), uhrText(l.rechenzeit))
	if len(l.fehlerListe) > 0 {
		anz.Zeile("")
		anz.Zeile("Nicht geschafft (Original unangetastet, beim naechsten Lauf wieder dran):")
		for _, zeile := range l.fehlerListe {
			anz.Zeile("  %s", zeile)
		}
	}
}

// gesamtBilanzZeigen gibt die Summe über alle Läufe aus. Die Rechenzeit wird
// erst seit 0.7.0 gemessen — ältere Dateien zählen zur Ersparnis, nicht zur Zeit.
func gesamtBilanzZeigen(anz *Anzeige, zustand *Zustand) {
	if zustand == nil || zustand.GesamtDateien == 0 {
		return
	}
	anz.Zeile("Gesamtbilanz:  %s gespart in %s (%d Dateien)",
		groesseText(zustand.GesamtGespartBytes),
		uhrText(time.Duration(zustand.GesamtRechenSek*float64(time.Second))),
		zustand.GesamtDateien)
}
