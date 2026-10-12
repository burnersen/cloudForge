// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

// mkv2mp4 (seit 0.22.0, Nutzerwunsch 12.10.2026): packt alle MKV im aktuellen
// Ordner verlustfrei nach MP4 um, legt sie in den Unterordner output und
// löscht danach das Original — gedacht für Ergebnisse von vor 0.21.0, die auf
// dem iPhone sonst nicht spielen. Bild und Ton werden nicht neu kodiert, es
// gelten dieselben MP4-Regeln wie beim Umwandeln (container.go).
//
// Bewusst OHNE die Sperre (Nutzerwunsch): Umpacken ist im Kern Kopieren und
// kostet kaum Rechenzeit, es darf neben einem CloudForge-Lauf arbeiten. Jede
// Datei bekommt deshalb einen Arbeitsordner mit eigener Vorsilbe, den
// ArbeitsresteEntfernen eines CloudForge-Laufs nicht anfasst.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

const (
	mkv2mp4Vorsilbe = "mkv2mp4-" // Arbeitsordner je Datei, NICHT arbeitsVorsilbe
	mkv2mp4Schritte = 4

	// So viele Dateien nennt die Rückfrage einzeln, den Rest nur als Zahl.
	mkv2mp4ListeHoechstens = 20
)

// mkv2mp4Bilanz zählt, was aus den Dateien wurde.
type mkv2mp4Bilanz struct {
	umgepackt, liegenGeblieben, fehler int
}

// mkv2mp4Starten nimmt den aktuellen Ordner. Weitere Angaben lehnt es ab,
// statt sie still zu übergehen: Wer einen Ordner nennt, erwartet, dass dort
// gearbeitet wird.
func mkv2mp4Starten(argumente []string, e Einstellungen) error {
	if len(argumente) > 0 {
		return Hinweis("mkv2mp4 arbeitet immer im aktuellen Ordner - erst mit cd dorthin wechseln, " +
			"dann mkv2mp4 ohne weitere Angaben aufrufen")
	}
	ordner, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("aktueller Ordner nicht ermittelbar: %w", err)
	}
	// Strg+C, Fenster zu oder Abmelden beenden geordnet: die angefangene
	// Datei wird verworfen, ihr Original bleibt.
	ctx, aufhoeren := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer aufhoeren()
	return Mkv2mp4(ctx, ordner, e)
}

// Mkv2mp4 arbeitet alle MKV im Ordner ab — nach einer Rückfrage, weil die
// Originale danach gelöscht werden.
func Mkv2mp4(ctx context.Context, ordner string, e Einstellungen) error {
	dateien, err := mkvDateienSuchen(ordner)
	if err != nil {
		return err
	}
	if len(dateien) == 0 {
		return Hinweis(fmt.Sprintf("im Ordner %s liegen keine MKV-Dateien", ordner))
	}
	ausgabe := filepath.Join(ordner, e.AusgabeOrdnerName)

	if !eingabeVomTerminal() {
		return Hinweis("mkv2mp4 fragt vor dem Start nach - bitte in einem Terminal aufrufen")
	}
	fmt.Print(mkv2mp4Ankuendigung(dateien, ausgabe))
	if !mkv2mp4Zugestimmt(os.Stdin) {
		fmt.Println("Nichts veraendert.")
		return nil
	}

	for _, noetig := range []string{e.ArbeitsOrdner, ausgabe} {
		if err := os.MkdirAll(noetig, 0o755); err != nil {
			return fmt.Errorf("Ordner %s nicht anlegbar: %w", noetig, err)
		}
	}

	anz := NeueAnzeige()
	var bilanz mkv2mp4Bilanz
	for nummer, pfad := range dateien {
		anz.Datei(nummer+1, len(dateien), filepath.Base(pfad))
		grund, err := mkvUmpacken(ctx, anz, pfad, ausgabe, e)
		if ctx.Err() != nil {
			anz.Zeile("ABGEBROCHEN. Diese Datei ist unangetastet, Fertiges bleibt fertig.")
			break
		}
		switch {
		case err != nil:
			bilanz.fehler++
			anz.Zeile("  FEHLER: %v - das Original bleibt liegen.", err)
		case grund != "":
			bilanz.liegenGeblieben++
			anz.Zeile("  UEBERSPRUNGEN: %s - das Original bleibt liegen.", grund)
		default:
			bilanz.umgepackt++
		}
	}
	anz.Zeile("\nmkv2mp4: %d umgepackt, %d liegen geblieben, %d mit Fehler.",
		bilanz.umgepackt, bilanz.liegenGeblieben, bilanz.fehler)
	return nil
}

// mkvDateienSuchen liefert die MKV direkt im Ordner, ohne Unterordner,
// nach Namen geordnet.
func mkvDateienSuchen(ordner string) ([]string, error) {
	eintraege, err := os.ReadDir(ordner)
	if err != nil {
		return nil, fmt.Errorf("Ordner nicht lesbar: %w", err)
	}
	var dateien []string
	for _, eintrag := range eintraege {
		if eintrag.Type().IsRegular() && strings.EqualFold(filepath.Ext(eintrag.Name()), endungMKV) {
			dateien = append(dateien, filepath.Join(ordner, eintrag.Name()))
		}
	}
	sort.Strings(dateien)
	return dateien, nil
}

// mp4NameFuer: "film.av1.mkv" wird zu "film.av1.mp4" — der Name bleibt, nur
// die Endung wechselt. So erkennt CloudForge auch die MP4 als sein Ergebnis.
func mp4NameFuer(mkvPfad string) string {
	name := filepath.Base(mkvPfad)
	return strings.TrimSuffix(name, filepath.Ext(name)) + endungMP4
}

// mkv2mp4Ankuendigung sagt vor dem Start, was passieren wird.
func mkv2mp4Ankuendigung(dateien []string, ausgabe string) string {
	var text strings.Builder
	var summe int64
	for nummer, pfad := range dateien {
		groesse := DateiGroesse(pfad)
		summe += groesse
		if nummer < mkv2mp4ListeHoechstens {
			fmt.Fprintf(&text, "  %s  (%s)\n", filepath.Base(pfad), groesseText(groesse))
		}
	}
	if rest := len(dateien) - mkv2mp4ListeHoechstens; rest > 0 {
		fmt.Fprintf(&text, "  ... und %d weitere\n", rest)
	}
	return fmt.Sprintf("\nmkv2mp4: %d MKV-Datei(en), zusammen %s:\n%s\n"+
		"Sie werden verlustfrei nach MP4 umgepackt (Bild und Ton unveraendert) und\n"+
		"nach %s gelegt.\n"+
		"Jedes Original wird danach GELOESCHT - erst, wenn seine MP4 geprueft am\n"+
		"Ziel liegt. Was MP4 nicht aufnehmen kann, bleibt als MKV liegen.\n\n"+
		"Starten? j = ja, alles andere = nein: ",
		len(dateien), groesseText(summe), text.String(), ausgabe)
}

// mkv2mp4Zugestimmt liest die Antwort. Nur ein ausdrückliches Ja startet:
// Die Originale werden gelöscht, ein versehentliches Enter darf das nicht.
func mkv2mp4Zugestimmt(eingabe io.Reader) bool {
	zeile, _ := bufio.NewReader(eingabe).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(zeile)) {
	case "j", "ja", "y", "yes":
		return true
	}
	return false
}

// mkvUmpacken bringt eine Datei durch die vier Schritte. grund nennt, warum
// sie liegen bleibt; err einen Fehler. Beides leer heisst: umgepackt, die MP4
// liegt in ausgabe, das Original ist gelöscht.
//
// Gearbeitet wird wie bei CloudForge lokal im Arbeitsordner, nie direkt auf
// dem Cloud-Ordner: Holen und Ablegen haben einen Stillstand-Wächter, und
// faststart schreibt die Datei ein zweites Mal um — das soll auf der eigenen
// Platte geschehen, nicht in der pCloud.
func mkvUmpacken(ctx context.Context, anz *Anzeige, pfad, ausgabe string, e Einstellungen) (grund string, err error) {
	info, err := KopfdatenLesen(e.FFprobePfad, pfad)
	if err != nil {
		return "", err
	}
	alsMP4 := e
	alsMP4.Container = ContainerMP4
	if _, mkvGrund := ergebnisEndung(info, alsMP4, true); mkvGrund != "" {
		return "bleibt MKV - " + mkvGrund, nil
	}
	ziel := filepath.Join(ausgabe, mp4NameFuer(pfad))
	if _, err := os.Stat(ziel); err == nil {
		return "in " + filepath.Base(ausgabe) + " liegt schon " + filepath.Base(ziel), nil
	}

	// Platz für die geholte MKV und die neue MP4, beide etwa gleich gross.
	if err := PlatzPruefen(e.ArbeitsOrdner, 2*info.GroesseBytes, e); err != nil {
		return "", err
	}
	arbeitsplatz, err := os.MkdirTemp(e.ArbeitsOrdner, mkv2mp4Vorsilbe)
	if err != nil {
		return "", fmt.Errorf("Arbeitsordner nicht anlegbar: %w", err)
	}
	defer os.RemoveAll(arbeitsplatz)

	anz.Schritt(1, mkv2mp4Schritte, "Datei holen")
	lokaleQuelle := filepath.Join(arbeitsplatz, "quelle"+endungMKV)
	if err := Kopieren(ctx, pfad, lokaleQuelle, anz.Stand); err != nil {
		return "", fmt.Errorf("Datei holen: %w", err)
	}
	anz.SchrittFertig(groesseText(info.GroesseBytes))

	anz.Schritt(2, mkv2mp4Schritte, "Umpacken")
	lokalesErgebnis := filepath.Join(arbeitsplatz, "umgepackt"+endungMP4)
	auftrag := EncodeAuftrag{Quelle: lokaleQuelle, Ziel: lokalesErgebnis, Umpacken: true,
		UntertitelCodecs: info.UntertitelCodecs, QuellBildCodec: info.VideoCodec}
	if err := Kodieren(ctx, auftrag, e, info.DauerSek, anz.Stand); err != nil {
		return "", err
	}
	ergebnisBytes := DateiGroesse(lokalesErgebnis)
	anz.SchrittFertig(fmt.Sprintf("%s statt %s", groesseText(ergebnisBytes), groesseText(info.GroesseBytes)))

	anz.Schritt(3, mkv2mp4Schritte, "Pruefen")
	if pruefung := UmpackErgebnisPruefen(ctx, info, lokalesErgebnis, e, anz.Stand); !pruefung.Bestanden {
		if ctx.Err() != nil {
			return "", ErrAbgebrochen
		}
		return "", fmt.Errorf("Pruefung nicht bestanden - %s", pruefung.ErsterFehler())
	}
	anz.SchrittFertig("alles in Ordnung")

	// Erst wenn die MP4 vollständig am Ziel liegt, verschwindet das Original.
	anz.Schritt(4, mkv2mp4Schritte, "Ablegen")
	if err := Kopieren(ctx, lokalesErgebnis, ziel, anz.Stand); err != nil {
		return "", fmt.Errorf("Ablegen: %w", err)
	}
	if angekommen := DateiGroesse(ziel); angekommen != ergebnisBytes {
		os.Remove(ziel)
		return "", fmt.Errorf("am Ziel kamen %d statt %d Bytes an - MP4 verworfen", angekommen, ergebnisBytes)
	}
	if err := os.Remove(pfad); err != nil {
		return "", fmt.Errorf("die MP4 liegt fertig in %s, aber das Original liess sich nicht loeschen: %w",
			filepath.Base(ausgabe), err)
	}
	anz.SchrittFertig(filepath.Base(ziel) + " in " + filepath.Base(ausgabe) + ", Original geloescht")
	return "", nil
}
