package main

// Die Prüfkette. Erst wenn hier alles besteht, darf das Original angefasst
// werden. Lieber eine Datei zu viel als fehlgeschlagen melden, als ein
// Original wegen eines übersehenen Fehlers verlieren.

import (
	"context"
	"fmt"
	"strings"
)

// Wie weit die Spieldauer abweichen darf.
//
// Die Toleranz muss mitwachsen: Container runden Zeitstempel, das letzte Bild
// hat eine eigene Dauer, und beim Umpacken zwischen MP4 und MKV verschiebt
// sich der Anfang leicht. Bei einem langen Film summiert sich das auf mehrere
// Sekunden, ohne dass etwas fehlt.
//
// Gemessen am 21.09.2026: Ein 120-Sekunden-Ausschnitt kam als 121,1 Sekunden
// heraus und fiel durch die frühere feste Grenze von einer Sekunde — ein
// Fehlalarm. Ein echter Abbruch fehlt dagegen um Minuten, nicht um Sekunden.
// Diese Prüfung ist die Hauptsicherung gegen abgebrochene Encodes; die
// Vollprüfung (vollpruefung=ja) ist nur ein zusätzliches Netz.
const (
	dauerToleranzMindestensSek = 2.0
	dauerToleranzAnteil        = 0.005 // 0,5 % der Spieldauer
)

// dauerToleranzFuer liefert die erlaubte Abweichung für eine Spieldauer.
func dauerToleranzFuer(dauerSek float64) float64 {
	anteilig := dauerSek * dauerToleranzAnteil
	if anteilig < dauerToleranzMindestensSek {
		return dauerToleranzMindestensSek
	}
	return anteilig
}

// PruefErgebnis hält fest, was geprüft wurde und was dabei herauskam.
type PruefErgebnis struct {
	Bestanden bool
	Schritte  []PruefSchritt
}

// PruefSchritt ist eine einzelne Prüfung.
type PruefSchritt struct {
	Name      string
	Bestanden bool
	Befund    string
}

// ErsterFehler liefert die Beschreibung der ersten nicht bestandenen Prüfung.
func (p PruefErgebnis) ErsterFehler() string {
	for _, s := range p.Schritte {
		if !s.Bestanden {
			return s.Name + ": " + s.Befund
		}
	}
	return ""
}

// ErgebnisPruefen geht die ganze Kette durch. quelle und ergebnis sind
// lokale Pfade; die Cloud wird hier nicht angefasst. melde bekommt den
// Fortschritt der längsten Prüfung, des vollständigen Dekodierens — die
// läuft nur mit vollpruefung=ja.
func ErgebnisPruefen(ctx context.Context, quelle VideoInfo, ergebnisPfad string, e Einstellungen, melde Rueckmeldung) PruefErgebnis {
	return kettePruefen(ctx, quelle, ergebnisPfad, e, melde, true)
}

// UmpackErgebnisPruefen prüft ein umgepacktes Ergebnis mit derselben Kette —
// nur "kleiner geworden" entfällt: Beim verlustfreien Umpacken bleibt das
// Bild gleich gross, MKV und MP4 verwalten ihre Spuren nur etwas anders. Das
// Ergebnis darf deshalb ein wenig grösser sein (NVENCForge behält es dann
// ebenfalls).
func UmpackErgebnisPruefen(ctx context.Context, quelle VideoInfo, ergebnisPfad string, e Einstellungen, melde Rueckmeldung) PruefErgebnis {
	return kettePruefen(ctx, quelle, ergebnisPfad, e, melde, false)
}

func kettePruefen(ctx context.Context, quelle VideoInfo, ergebnisPfad string, e Einstellungen, melde Rueckmeldung, mussKleinerSein bool) PruefErgebnis {
	ergebnis := PruefErgebnis{Bestanden: true}

	pruefe := func(name string, bestanden bool, befund string) {
		ergebnis.Schritte = append(ergebnis.Schritte, PruefSchritt{
			Name: name, Bestanden: bestanden, Befund: befund,
		})
		if !bestanden {
			ergebnis.Bestanden = false
		}
	}

	// 1. Überhaupt vorhanden und nicht leer?
	groesse := DateiGroesse(ergebnisPfad)
	if groesse == 0 {
		pruefe("Datei vorhanden", false, "Ergebnisdatei fehlt oder ist leer")
		return ergebnis
	}
	pruefe("Datei vorhanden", true, fmt.Sprintf("%.0f MB", float64(groesse)/1024/1024))

	// 2. Kleiner als die Quelle? Sonst war die ganze Mühe umsonst.
	if mussKleinerSein {
		kleiner := ProzentKleiner(quelle.GroesseBytes, groesse)
		if kleiner <= 0 {
			pruefe("Kleiner geworden", false,
				fmt.Sprintf("Ergebnis ist %.1f%% GROESSER als die Quelle", -kleiner))
			return ergebnis
		}
		pruefe("Kleiner geworden", true, fmt.Sprintf("%.1f%% kleiner", kleiner))
	}

	// 3. Kopfdaten lesbar, und stimmen sie mit der Quelle überein?
	neu, err := KopfdatenLesen(e.FFprobePfad, ergebnisPfad)
	if err != nil {
		pruefe("Kopfdaten lesbar", false, err.Error())
		return ergebnis
	}
	pruefe("Kopfdaten lesbar", true, neu.VideoCodec)

	dauerDiff := abweichung(neu.DauerSek, quelle.DauerSek)
	erlaubt := dauerToleranzFuer(quelle.DauerSek)
	if dauerDiff > erlaubt {
		pruefe("Spieldauer stimmt", false,
			fmt.Sprintf("%.1f s Unterschied, erlaubt sind %.1f s (Quelle %.1f s, Ergebnis %.1f s)",
				dauerDiff, erlaubt, quelle.DauerSek, neu.DauerSek))
		return ergebnis
	}
	pruefe("Spieldauer stimmt", true,
		fmt.Sprintf("%.1f s Unterschied, erlaubt %.1f s", dauerDiff, erlaubt))

	if len(neu.Tonspuren) != len(quelle.Tonspuren) {
		pruefe("Tonspuren vollstaendig", false,
			fmt.Sprintf("Quelle hat %d, Ergebnis %d", len(quelle.Tonspuren), len(neu.Tonspuren)))
		return ergebnis
	}
	pruefe("Tonspuren vollstaendig", true, fmt.Sprintf("%d Spuren", len(neu.Tonspuren)))

	if neu.UntertitelAnzahl != quelle.UntertitelAnzahl {
		pruefe("Untertitel vollstaendig", false,
			fmt.Sprintf("Quelle hat %d, Ergebnis %d", quelle.UntertitelAnzahl, neu.UntertitelAnzahl))
		return ergebnis
	}
	pruefe("Untertitel vollstaendig", true, fmt.Sprintf("%d Spuren", neu.UntertitelAnzahl))

	// 4. Die teuerste Prüfung: lässt sich die Datei von vorn bis hinten
	// fehlerfrei dekodieren? Sie findet seltene Schäden mitten im Bild, die
	// Größe, Spieldauer und Spuren nicht verraten. Weil sie gemessen 23-27
	// Minuten je 55-Minuten-Film kostet, läuft sie nur auf Wunsch. Ausgelassen
	// wird sie gar nicht erst als Schritt vermerkt — geprüft ist dann nichts.
	if !e.Vollpruefung {
		return ergebnis
	}
	if err := VollstaendigDekodierbar(ctx, ergebnisPfad, neu.DauerSek, e, melde); err != nil {
		pruefe("Vollstaendig abspielbar", false, err.Error())
		return ergebnis
	}
	pruefe("Vollstaendig abspielbar", true, "ohne Fehler durchgelaufen")

	return ergebnis
}

// VollstaendigDekodierbar liest die Datei einmal komplett durch und meldet
// jeden Fehler, den ffmpeg dabei findet.
//
// Gemessen am 23.09.2026 auf dem VPS: 2,0- bis 2,4-fache Echtzeit bei nur
// gut zwei belegten Kernen. Aufgerufen wird sie nur mit vollpruefung=ja.
func VollstaendigDekodierbar(ctx context.Context, pfad string, dauerSek float64, e Einstellungen, melde Rueckmeldung) error {
	args := []string{"-nostdin", "-xerror", "-i", pfad, "-f", "null", "-"}

	meldungen, err := ffmpegLaufen(ctx, e.FFmpegPfad, args, dauerSek, melde)
	if err != nil {
		if err == ErrAbgebrochen {
			return err
		}
		return fmt.Errorf("Dekodieren gescheitert: %w", err)
	}

	// -xerror bricht bei schweren Fehlern ab; leichtere landen nur als Text
	// in der Ausgabe. Auch die sind hier ein Grund zum Misstrauen.
	if meldung := strings.TrimSpace(meldungen); meldung != "" {
		return fmt.Errorf("Fehler beim Dekodieren: %s", letzteZeilen(meldung, 2))
	}
	return nil
}
