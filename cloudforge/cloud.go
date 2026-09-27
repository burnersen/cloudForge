package main

// Alles, was Dateien zwischen dem Cloud-Ordner und der lokalen Platte bewegt.
//
// Zwei Grundregeln stecken hier drin:
//  1. Auf dem Cloud-Ordner wird nie gerechnet. Erst herunterkopieren, dann
//     arbeiten, dann hochkopieren.
//  2. Eine Datei erscheint im Ziel erst dann unter ihrem richtigen Namen,
//     wenn sie vollständig da ist. Ein abgebrochener Upload darf niemals
//     wie ein fertiges Ergebnis aussehen.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	unfertigEndung  = ".unfertig"            // eine noch nicht fertige Datei
	arbeitsVorsilbe = "datei-"               // Arbeitsordner je Datei
	kopierPuffer    = 4 << 20                // 4 MB je Lesevorgang
	kopierMeldeTakt = 500 * time.Millisecond // so oft wird der Stand gemeldet
)

// Kopieren überträgt eine Datei und prüft danach die Größe. Ein Abbruch
// (Fenster zu) wird zwischen zwei Blöcken bemerkt und hinterlässt nichts.
//
// Bewusst kein os.Rename: zwischen dem Cloud-Ordner und der lokalen Platte
// liegen verschiedene Dateisysteme, ein Umbenennen scheitert dort.
func Kopieren(ctx context.Context, quellPfad, zielPfad string, melde Rueckmeldung) error {
	quelle, err := os.Open(quellPfad)
	if err != nil {
		return fmt.Errorf("Quelle nicht lesbar: %w", err)
	}
	defer quelle.Close()

	quellZustand, err := quelle.Stat()
	if err != nil {
		return fmt.Errorf("Quelle nicht pruefbar: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(zielPfad), 0o755); err != nil {
		return fmt.Errorf("Zielordner nicht anlegbar: %w", err)
	}

	// Unter Tarnnamen schreiben und erst am Ende umbenennen.
	tarnPfad := zielPfad + unfertigEndung
	ziel, err := os.Create(tarnPfad)
	if err != nil {
		return fmt.Errorf("Ziel nicht schreibbar: %w", err)
	}

	kopierFehler := kopierenMitMeldung(ctx, ziel, quelle, quellZustand.Size(), melde)
	schliessFehler := ziel.Close()

	if kopierFehler != nil {
		os.Remove(tarnPfad)
		if kopierFehler == ErrAbgebrochen {
			return ErrAbgebrochen
		}
		return fmt.Errorf("Kopieren abgebrochen: %w", kopierFehler)
	}
	if schliessFehler != nil {
		os.Remove(tarnPfad)
		return fmt.Errorf("Datei liess sich nicht sauber schliessen: %w", schliessFehler)
	}

	// Gegenprobe: ist wirklich alles angekommen?
	if angekommen := DateiGroesse(tarnPfad); angekommen != quellZustand.Size() {
		os.Remove(tarnPfad)
		return fmt.Errorf("unvollstaendig uebertragen: %d von %d Bytes",
			angekommen, quellZustand.Size())
	}

	if err := os.Rename(tarnPfad, zielPfad); err != nil {
		os.Remove(tarnPfad)
		return fmt.Errorf("Umbenennen fehlgeschlagen: %w", err)
	}
	return nil
}

// kopierenMitMeldung kopiert blockweise, meldet den Stand und schaut vor
// jedem Block nach, ob abgebrochen wurde.
func kopierenMitMeldung(ctx context.Context, ziel io.Writer, quelle io.Reader, gesamt int64, melde Rueckmeldung) error {
	puffer := make([]byte, kopierPuffer)
	beginn := time.Now()
	var kopiert int64
	var zuletztGemeldet time.Time

	for {
		if ctx.Err() != nil {
			return ErrAbgebrochen
		}

		gelesen, leseFehler := quelle.Read(puffer)
		if gelesen > 0 {
			if _, err := ziel.Write(puffer[:gelesen]); err != nil {
				return err
			}
			kopiert += int64(gelesen)

			if melde != nil && gesamt > 0 && time.Since(zuletztGemeldet) >= kopierMeldeTakt {
				zuletztGemeldet = time.Now()
				anteil := float64(kopiert) / float64(gesamt)
				melde(Stand{
					Anteil: anteil,
					Rest:   restzeitSchaetzen(time.Since(beginn), anteil),
					Text:   fmt.Sprintf("%s von %s", groesseText(kopiert), groesseText(gesamt)),
				})
			}
		}

		if leseFehler == io.EOF {
			if melde != nil {
				melde(Stand{Anteil: 1})
			}
			return nil
		}
		if leseFehler != nil {
			return leseFehler
		}
	}
}

// Verschieben bringt eine Datei an einen anderen Ort. Innerhalb desselben
// Dateisystems geht das sofort, sonst wird kopiert und die Quelle danach
// entfernt — aber erst, wenn die Kopie nachweislich vollständig ist.
//
// Absichtlich ohne Abbruchmöglichkeit: das Original soll entweder ganz an
// seinem alten Platz liegen oder ganz am neuen, nie dazwischen.
func Verschieben(quellPfad, zielPfad string) error {
	if err := os.MkdirAll(filepath.Dir(zielPfad), 0o755); err != nil {
		return fmt.Errorf("Zielordner nicht anlegbar: %w", err)
	}

	if err := os.Rename(quellPfad, zielPfad); err == nil {
		return nil
	}

	// Rename scheitert über Dateisystemgrenzen hinweg — dann der lange Weg.
	if err := Kopieren(context.Background(), quellPfad, zielPfad, nil); err != nil {
		return err
	}
	if err := os.Remove(quellPfad); err != nil {
		return fmt.Errorf("Kopie liegt in %s, aber das Original liess sich nicht entfernen: %w",
			zielPfad, err)
	}
	return nil
}

// ArbeitsresteEntfernen räumt auf, was ein abgebrochener Lauf liegen lassen
// hat: Arbeitsordner einzelner Dateien und halbfertige Kopien.
//
// Darf NUR aufgerufen werden, während die Sperre gehalten wird — sonst
// könnte es einem gerade laufenden CloudForge die Dateien wegnehmen.
// Es entfernt ausschliesslich, was nach eigenem Namensmuster aussieht, und
// nie den Ordner als Ganzes: steht in der INI ein falscher Arbeitsordner,
// bleibt fremder Inhalt unberührt.
func ArbeitsresteEntfernen(ordner string) int {
	eintraege, err := os.ReadDir(ordner)
	if err != nil {
		return 0
	}

	entfernt := 0
	for _, eintrag := range eintraege {
		name := eintrag.Name()
		eigenerOrdner := eintrag.IsDir() && strings.HasPrefix(name, arbeitsVorsilbe)
		eigeneDatei := !eintrag.IsDir() && strings.HasSuffix(name, unfertigEndung)
		if !eigenerOrdner && !eigeneDatei {
			continue
		}
		if err := os.RemoveAll(filepath.Join(ordner, name)); err == nil {
			entfernt++
		}
	}
	return entfernt
}

// PlatzPruefen sagt, ob gebrauchtBytes lokal frei sind — zusätzlich zur
// Reserve aus der INI, damit die Platte nie ganz vollläuft (auch der
// Cloud-Client legt unbemerkt Daten ab).
//
// Für eine Datei braucht es ungefähr das Doppelte der Quellgröße: die
// heruntergeladene Datei, das entstehende Ergebnis und die Zwischendateien
// von Auto-CQ. Liegt die Datei schon vorab geholt da, nur noch die Hälfte.
func PlatzPruefen(arbeitsOrdner string, gebrauchtBytes int64, e Einstellungen) error {
	frei, err := FreierPlatzBytes(arbeitsOrdner)
	if err != nil {
		return err
	}

	reserve := int64(e.PlatzReserveGB) * 1024 * 1024 * 1024
	gebraucht := gebrauchtBytes + reserve

	if frei < gebraucht {
		return fmt.Errorf("zu wenig Platz: %.1f GB frei, gebraucht werden %.1f GB (inklusive %d GB Reserve)",
			gigabyte(frei), gigabyte(gebraucht), e.PlatzReserveGB)
	}
	return nil
}

func gigabyte(bytes int64) float64 {
	return float64(bytes) / 1024 / 1024 / 1024
}

// OriginalWegraeumen führt aus, was in der INI steht: verschieben, löschen
// oder liegen lassen. Liefert, was getan wurde, und wo das Original jetzt
// liegt (leer, wenn es gelöscht wurde).
func OriginalWegraeumen(quellPfad string, e Einstellungen) (was, neuerOrt string, err error) {
	switch e.OriginalBehandlung {
	case OriginalBehalten:
		return "Original bleibt liegen", quellPfad, nil

	case OriginalVerschieben:
		ziel := OriginalPfadFuer(quellPfad, e)
		if err := Verschieben(quellPfad, ziel); err != nil {
			return "", quellPfad, err
		}
		return "Original nach " + e.OriginalOrdnerName + " verschoben", ziel, nil

	case OriginalLoeschen:
		if err := os.Remove(quellPfad); err != nil {
			return "", quellPfad, fmt.Errorf("Original liess sich nicht entfernen: %w", err)
		}
		return "Original geloescht", "", nil

	default:
		// Kann nur auftreten, wenn jemand die Prüfung in config.go umgeht.
		return "", quellPfad, fmt.Errorf("unbekannte Behandlung %q - Original bleibt unangetastet",
			e.OriginalBehandlung)
	}
}
