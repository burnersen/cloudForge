package main

// Das Protokoll: was ein Lauf getan hat, als Textdatei je Tag.
//
// Warum: Seit 0.9.0 arbeitet CloudForge auch ohne Fenster (Zeitplan). Ohne
// Protokoll wäre nie zu sehen, was nachts passiert ist. Auch ein Lauf mit
// Fenster schreibt mit — ist das Fenster zu, bleibt so trotzdem alles lesbar.
//
//   ~/cloudforge/protokoll/2026-09-25.txt   eine Datei je Tag, Zeilen mit Uhrzeit
//   ~/cloudforge/protokoll/aktuell.txt      Verweis auf die Datei von heute

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	protokollAufbewahren = 30 * 24 * time.Hour // was älter ist, wird gelöscht
	protokollDatumFormat = "2006-01-02"
	protokollAktuell     = "aktuell.txt"
	protokollNachsehen   = time.Second // so oft sieht "Protokoll ansehen" nach
	protokollLetzte      = 40          // so viele Zeilen zeigt es beim Öffnen
)

// Nur Dateien mit genau diesem Namen werden je gelöscht — nie etwas anderes,
// das zufällig im Ordner liegt.
var protokollName = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}\.txt$`)

// Protokoll schreibt Zeilen in die Tagesdatei. Ein nil-Protokoll schluckt
// alles still — so müssen die Aufrufer nicht jedes Mal prüfen.
type Protokoll struct {
	ordner string
	datum  string
	datei  *os.File
}

// ProtokollOrdner liegt neben der INI.
func ProtokollOrdner(e Einstellungen) string {
	return filepath.Join(filepath.Dir(e.ArbeitsOrdner), "protokoll")
}

// ProtokollOeffnen legt den Ordner an, räumt alte Tage weg und öffnet die
// Datei von heute zum Anhängen.
func ProtokollOeffnen(ordner string) (*Protokoll, error) {
	if err := os.MkdirAll(ordner, 0o755); err != nil {
		return nil, fmt.Errorf("Protokollordner nicht anlegbar: %w", err)
	}
	alteProtokolleLoeschen(ordner, time.Now())

	p := &Protokoll{ordner: ordner}
	if err := p.tagOeffnen(time.Now()); err != nil {
		return nil, err
	}
	return p, nil
}

// Zeile schreibt eine Zeile mit Uhrzeit. Fehler beim Schreiben werden nicht
// gemeldet: ein volles Laufwerk darf die Umwandlung nicht anhalten.
func (p *Protokoll) Zeile(text string) {
	p.zeileZu(time.Now(), text)
}

func (p *Protokoll) zeileZu(jetzt time.Time, text string) {
	if p == nil || p.datei == nil {
		return
	}
	// Ein Lauf durch den ganzen Bestand dauert Wochen: um Mitternacht geht es
	// in der Datei des neuen Tages weiter.
	if jetzt.Format(protokollDatumFormat) != p.datum {
		if err := p.tagOeffnen(jetzt); err != nil {
			return
		}
	}
	for _, zeile := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		fmt.Fprintf(p.datei, "%s %s\n", jetzt.Format("15:04:05"), zeile)
	}
}

// Schliessen beendet das Protokoll. Mehrfacher Aufruf schadet nicht.
func (p *Protokoll) Schliessen() {
	if p != nil && p.datei != nil {
		p.datei.Close()
		p.datei = nil
	}
}

func (p *Protokoll) tagOeffnen(jetzt time.Time) error {
	datum := jetzt.Format(protokollDatumFormat)
	datei, err := os.OpenFile(filepath.Join(p.ordner, datum+".txt"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("Protokolldatei nicht oeffenbar: %w", err)
	}
	if p.datei != nil {
		p.datei.Close()
	}
	p.datei, p.datum = datei, datum

	// Verweis neu setzen: erst daneben anlegen, dann umbenennen — so zeigt
	// "aktuell.txt" nie ins Leere. Klappt das nicht (Windows), fehlt eben nur
	// der Verweis.
	neu := filepath.Join(p.ordner, protokollAktuell+".neu")
	os.Remove(neu)
	if os.Symlink(datum+".txt", neu) == nil {
		os.Rename(neu, filepath.Join(p.ordner, protokollAktuell))
	}
	return nil
}

// alteProtokolleLoeschen entfernt Tagesdateien, die älter als die
// Aufbewahrungszeit sind — gemessen am Datum im Namen, nicht an der Uhrzeit
// der letzten Änderung (die verschiebt jede angehängte Zeile).
func alteProtokolleLoeschen(ordner string, jetzt time.Time) {
	eintraege, err := os.ReadDir(ordner)
	if err != nil {
		return
	}
	grenze := jetzt.Add(-protokollAufbewahren)
	for _, eintrag := range eintraege {
		if !protokollName.MatchString(eintrag.Name()) {
			continue
		}
		tag, err := time.ParseInLocation(protokollDatumFormat, strings.TrimSuffix(eintrag.Name(), ".txt"), jetzt.Location())
		if err == nil && tag.Before(grenze) {
			os.Remove(filepath.Join(ordner, eintrag.Name()))
		}
	}
}

// ProtokollVerfolgen zeigt die letzten Zeilen von heute und dann laufend
// alles Neue — für das Symbol "CloudForge: Protokoll ansehen". Folgt dabei
// dem Verweis: um Mitternacht geht es in der neuen Tagesdatei weiter.
func ProtokollVerfolgen(ctx context.Context, ordner string, ausgabe io.Writer) error {
	verweis := filepath.Join(ordner, protokollAktuell)
	fmt.Fprintf(ausgabe, "CloudForge-Protokoll (%s)\n", ordner)
	fmt.Fprintln(ausgabe, "Zeigt laufend, was CloudForge tut - auch im Hintergrund. Schliessen: Strg+C.")
	fmt.Fprintln(ausgabe, strings.Repeat("-", 78))

	var ziel string
	var datei *os.File
	defer func() {
		if datei != nil {
			datei.Close()
		}
	}()
	hinweisGezeigt := false

	for {
		if neuesZiel, err := os.Readlink(verweis); err == nil && neuesZiel != ziel {
			if datei != nil {
				datei.Close()
			}
			var oeffnen error
			datei, oeffnen = os.Open(filepath.Join(ordner, neuesZiel))
			if oeffnen == nil {
				// Beim ersten Öffnen nur das Ende zeigen, bei einem neuen Tag alles.
				if ziel == "" {
					letzteZeilenZeigen(datei, ausgabe, protokollLetzte)
				}
				ziel = neuesZiel
			} else {
				datei = nil
			}
		} else if err != nil && !hinweisGezeigt {
			fmt.Fprintln(ausgabe, "Noch kein Protokoll vorhanden - es entsteht beim ersten Lauf. Ich warte ...")
			hinweisGezeigt = true
		}

		if datei != nil {
			io.Copy(ausgabe, datei) // alles Neue seit dem letzten Mal
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(protokollNachsehen):
		}
	}
}

// letzteZeilenZeigen gibt die letzten Zeilen aus und lässt die Datei am Ende
// stehen, damit danach nur noch Neues kommt.
func letzteZeilenZeigen(datei *os.File, ausgabe io.Writer, anzahl int) {
	var zeilen []string
	leser := bufio.NewScanner(datei)
	for leser.Scan() {
		zeilen = append(zeilen, leser.Text())
		if len(zeilen) > anzahl {
			zeilen = zeilen[1:]
		}
	}
	for _, zeile := range zeilen {
		fmt.Fprintln(ausgabe, zeile)
	}
	datei.Seek(0, io.SeekEnd)
}
