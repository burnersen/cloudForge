// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

// CloudForge — wandelt Videos aus einem Cloud-Ordner nach AV1 um.
//
// Aufrufe:
//   cloudforge DATEI|ORDNER ...    verarbeitet genau das Uebergebene
//   cloudforge -automatik          arbeitet die Ordner aus der INI ab
//   cloudforge -bericht [PFAD...]  zeigt nur, was zu tun waere
//   cloudforge -analyse DATEI ...  misst Auto-CQ, ohne umzuwandeln
//   cloudforge -starter            legt die Symbole auf dem Schreibtisch an
//   cloudforge -zeitplan an|aus    alle 30 Minuten die Automatik im Hintergrund
//   cloudforge -protokoll          zeigt das Protokoll laufend an
//   cloudforge -hilfe              erklaert die Aufrufe

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const appVersion = "0.17.0"

func main() {
	err := starten()

	var hinweis Hinweis
	switch {
	case err == nil:
	case errors.As(err, &hinweis):
		fmt.Printf("\nHINWEIS: %v\n", hinweis)
	default:
		fmt.Fprintf(os.Stderr, "\nFEHLER: %v\n", err)
		os.Exit(1)
	}
}

func starten() error {
	automatik := flag.Bool("automatik", false, "die Ordner aus der INI abarbeiten")
	bericht := flag.Bool("bericht", false, "nur zeigen, was zu tun waere; nichts veraendern")
	analyse := flag.Bool("analyse", false, "Auto-CQ an den Dateien messen, aber nicht umwandeln")
	starter := flag.Bool("starter", false, "Symbole auf dem Schreibtisch anlegen")
	hintergrund := flag.Bool("hintergrund", false, "mit -automatik: ohne Fenster, fuer den Zeitplan")
	zeitplan := flag.String("zeitplan", "", "an oder aus: alle 30 Minuten die Automatik-Ordner abarbeiten")
	protokollZeigen := flag.Bool("protokoll", false, "das Protokoll laufend ansehen")
	hilfe := flag.Bool("hilfe", false, "Erklaerung der Aufrufe")
	iniPfad := flag.String("ini", standardINIPfad(), "Pfad zur Einstellungsdatei")
	flag.Parse()

	if *hilfe {
		hilfeAusgeben()
		return nil
	}
	if *hintergrund && !*automatik {
		return Hinweis("-hintergrund gibt es nur zusammen mit -automatik (so startet ihn der Zeitplan)")
	}

	kopfAusgeben()

	e, iniHinweise, err := EinstellungenLaden(*iniPfad)
	if err != nil {
		return err
	}
	fmt.Printf("Einstellungen: %s\n", *iniPfad)
	for _, hinweis := range iniHinweise {
		fmt.Printf("  %s\n", hinweis)
	}

	// Starter und Protokoll brauchen kein ffmpeg — das darf auch gehen,
	// bevor alles Weitere eingerichtet ist.
	if *starter {
		return StarterAnlegen()
	}
	if *protokollZeigen {
		ctx, aufhoeren := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		defer aufhoeren()
		return ProtokollVerfolgen(ctx, ProtokollOrdner(e), os.Stdout)
	}

	if err := WerkzeugePruefen(e); err != nil {
		return err
	}

	switch *zeitplan {
	case "":
	case "an", "aus":
		return ZeitplanSchalten(*zeitplan == "an", *iniPfad, e)
	default:
		return Hinweis(fmt.Sprintf("-zeitplan kennt nur \"an\" oder \"aus\", nicht %q", *zeitplan))
	}
	if *hintergrund && len(e.QuellOrdner) == 0 {
		return Hinweis("fuer den Zeitplan ist kein Ordner eingetragen (quellOrdner= in der INI)")
	}

	pfade, err := pfadeBestimmen(flag.Args(), *automatik, e, *iniPfad)
	if err != nil {
		return err
	}

	if *bericht {
		return berichtAusgeben(pfade, e)
	}

	// Fenster schliessen, Strg+C oder Herunterfahren beenden den Lauf
	// geordnet: die angefangene Datei wird verworfen und aufgeräumt, alles
	// Fertige bleibt vermerkt.
	ctx, aufhoeren := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer aufhoeren()

	if *analyse {
		return analyseAusgeben(ctx, pfade, e)
	}
	return verarbeitenStarten(ctx, pfade, e, *hintergrund)
}

// verarbeitenStarten arbeitet die Warteschlange ab. Nach jeder Datei wird der
// Stand gesichert, damit ein Abbruch nichts kostet.
//
// hintergrund (seit 0.9.0, Zeitplan): kein Fenster, das zusieht. Deshalb wird
// nie gewartet und nie gefragt — gibt es nichts zu tun (Ordner fehlt, weil
// die pCloud-App nicht läuft; ein anderes CloudForge arbeitet; nichts Neues),
// endet der Lauf still, und der nächste Takt versucht es wieder.
func verarbeitenStarten(ctx context.Context, pfade []string, e Einstellungen, hintergrund bool) (err error) {
	if hintergrund {
		if pfade = vorhandeneOrdner(pfade); len(pfade) == 0 {
			fmt.Fprintln(os.Stderr, "Automatik-Ordner nicht da (laeuft die pCloud-App?) - nichts zu tun.")
			return nil
		}
	}
	dateien, err := VideoDateienSuchen(pfade, e)
	if err != nil {
		return err
	}
	if len(dateien) == 0 {
		if hintergrund {
			return nil
		}
		return Hinweis("unter dem Uebergebenen sind keine Videodateien")
	}
	if err := os.MkdirAll(e.ArbeitsOrdner, 0o755); err != nil {
		return fmt.Errorf("Arbeitsordner nicht nutzbar: %w", err)
	}

	anz := NeueAnzeige()
	freigeben, err := sperreFuerLauf(ctx, e, anz, hintergrund)
	if err != nil || freigeben == nil {
		return err
	}
	defer freigeben()

	// Erst jetzt, mit der Sperre in der Hand, ist sicher, dass liegengebliebene
	// Reste niemandem mehr gehören.
	reste := ArbeitsresteEntfernen(e.ArbeitsOrdner)

	// Das Gedächtnis (Gesamtbilanz, Tempo) erst nach der Sperre lesen: hat ein
	// anderes Fenster vorher gearbeitet, ist dessen Beitrag so schon mit drin.
	zustand, zustandFehler := ZustandLaden(StandardZustandPfad(e))

	// Was erledigt ist, sagen allein die Ordner (seit 0.11.0): liegt schon ein
	// Ergebnis im output-Ordner, ist die Datei fertig.
	offen, erledigt := nachOrdnernAufteilen(dateien, e)
	if len(offen) == 0 {
		if !hintergrund {
			erledigteZeigen(anz, erledigt, e)
			gesamtBilanzZeigen(anz, zustand)
		}
		return nil
	}

	// Die dicksten Dateien zuerst (Nutzer-Entscheidung 25.09.2026): sie
	// bringen den meisten Platz, und wird der Lauf unterbrochen, ist das
	// Wichtigste schon erledigt. Die Grössen braucht auch die Restzeit-Schätzung.
	offen, groessen := nachGroesseOrdnen(offen)

	// Ab hier gibt es Arbeit — ab hier wird protokolliert.
	protokoll := protokollStarten(e, anz, hintergrund, len(offen))
	defer protokoll.Schliessen()
	defer func() {
		if err != nil {
			protokoll.Zeile("FEHLER: " + err.Error())
		}
	}()

	if reste > 0 {
		anz.Zeile("%d Rest(e) eines abgebrochenen Laufs aufgeraeumt.", reste)
	}
	if zustandFehler != nil {
		anz.Zeile("Hinweis: %v", zustandFehler)
	}
	anz.Zeile("\n%d Datei(en) gefunden, davon %d noch offen - die groessten zuerst.", len(dateien), len(offen))

	ablauf := &Ablauf{Einstellungen: e, Zustand: zustand, Anzeige: anz}
	defer ablauf.VorabVerwerfen() // eine nicht mehr abgeholte Vorab-Kopie
	if !hintergrund {
		laufHinweiseZeigen(e, ablauf.erfahrung())
	}
	var lauf laufBilanz

	// Ab hier zeigt das Terminal die feste Übersicht. Jeder Weg aus dieser
	// Funktion schliesst sie wieder — auch ein Abbruch.
	anz.UebersichtOeffnen()
	defer anz.UebersichtSchliessen()

	for nummer, pfad := range offen {
		anz.Datei(nummer+1, len(offen), filepath.Base(pfad))
		anz.Warteschlange(restDauer(groessen[nummer+1:], ablauf.erfahrung()))
		ablauf.Naechste = ""
		if nummer+1 < len(offen) {
			ablauf.Naechste = offen[nummer+1]
		}
		ergebnis := ablauf.EineDatei(ctx, pfad)

		// Ein Abbruch durch den Nutzer ist kein Fehler der Datei und wird
		// deshalb nicht vermerkt — beim nächsten Mal kommt sie wieder dran.
		if ergebnis.Abgebrochen() || (ctx.Err() != nil && ergebnis.Eintrag.Status == StatusFehler) {
			anz.Zeile("")
			anz.Zeile("ABGEBROCHEN. Das Original ist unangetastet, Halbfertiges wurde entfernt.")
			anz.Zeile("Beim naechsten Mal faengt diese Datei einfach von vorn an.")
			return nil
		}

		zeigeDateiErgebnis(anz, ergebnis)
		if ergebnis.Eintrag.Meldung == string(NichtMehrDa) {
			continue // nichts zu zählen, die Datei gibt es nicht mehr
		}
		lauf.dazu(pfad, ergebnis)
		if err := zustand.DateiFertig(ergebnis.Eintrag); err != nil {
			// Nur die Gesamtbilanz leidet — was erledigt ist, zeigen die Ordner.
			anz.Zeile("  Hinweis: Gesamtbilanz nicht gespeichert: %v", err)
		}

		// Kam der Abbruch erst beim Ablegen, ist diese Datei trotzdem sauber
		// fertig geworden — aber die nächste wird nicht mehr angefangen.
		if ctx.Err() != nil {
			anz.Zeile("")
			anz.Zeile("ABGEBROCHEN nach dieser Datei. Beim naechsten Mal geht es mit der naechsten weiter.")
			return nil
		}

		// Wartet ein Fenster (jemand hat Dateien aufs Symbol gezogen), macht
		// der Hintergrundlauf jetzt Platz. Der nächste Takt macht weiter.
		if hintergrund && nummer < len(offen)-1 && VorfahrtGewuenscht(vorfahrtPfad(e)) {
			anz.Zeile("")
			anz.Zeile("VORFAHRT: Ein Fenster wartet mit eigenen Dateien - der Zeitplan macht spaeter weiter.")
			break
		}
	}

	anz.UebersichtSchliessen()
	anz.Zeile("")
	lauf.zeigen(anz)
	gesamtBilanzZeigen(anz, zustand)
	if lauf.umgewandelt+lauf.umgepackt > 0 && e.OriginalBehandlung == OriginalVerschieben {
		anz.Zeile("")
		anz.Zeile("Wichtig: In der Cloud wird der Platz erst frei, wenn du die Ordner")
		anz.Zeile("\"%s\" loeschst. Schau dir vorher ein paar Ergebnisse an.", e.OriginalOrdnerName)
	}
	fertigMelden(anz, lauf, hintergrund)
	return nil
}

// sperreFuerLauf holt die Sperre. Im Fenster wird gewartet (mit Vorfahrt vor
// einem Hintergrundlauf), im Hintergrund nie: freigeben == nil ohne Fehler
// heisst dann "belegt, nichts zu tun".
func sperreFuerLauf(ctx context.Context, e Einstellungen, anz *Anzeige, hintergrund bool) (func(), error) {
	if hintergrund {
		freigeben, frei, err := SperreVersuchen(sperrPfad(e))
		if err != nil {
			return nil, err
		}
		if !frei {
			fmt.Fprintln(os.Stderr, "Ein anderes CloudForge arbeitet gerade - der naechste Takt versucht es wieder.")
			return nil, nil
		}
		return freigeben, nil
	}

	freigeben, err := laufSperren(ctx, e, anz)
	if err != nil {
		if ctx.Err() != nil {
			return nil, Hinweis("abgebrochen, bevor es losging - es wurde nichts veraendert")
		}
		return nil, err
	}
	return freigeben, nil
}

// protokollStarten öffnet das Protokoll und hängt es an die Anzeige. Geht das
// nicht, läuft die Umwandlung trotzdem — nur ohne Protokoll.
func protokollStarten(e Einstellungen, anz *Anzeige, hintergrund bool, offen int) *Protokoll {
	protokoll, err := ProtokollOeffnen(ProtokollOrdner(e))
	if err != nil {
		anz.Zeile("Hinweis: %v - es wird ohne Protokoll gearbeitet.", err)
		return nil
	}
	art := "Fenster"
	if hintergrund {
		art = "Zeitplan im Hintergrund"
	}
	protokoll.Zeile(fmt.Sprintf("===== CloudForge %s - Lauf beginnt (%s), %d Datei(en) offen =====", appVersion, art, offen))
	protokoll.Zeile("Einstellungen: " + einstellungenText(e))
	anz.ProtokollSetzen(protokoll)
	return protokoll
}

// vorhandeneOrdner lässt weg, was es gerade nicht gibt — im Hintergrund der
// Normalfall, solange die pCloud-App nicht läuft.
func vorhandeneOrdner(pfade []string) []string {
	var da []string
	for _, pfad := range pfade {
		if _, err := os.Stat(pfad); err == nil {
			da = append(da, pfad)
		}
	}
	return da
}

// nachGroesseOrdnen sortiert absteigend nach Dateigrösse und liefert die
// Grössen passend dazu. Bei gleicher Grösse bleibt die gefundene Reihenfolge.
func nachGroesseOrdnen(pfade []string) ([]string, []int64) {
	type datei struct {
		pfad    string
		groesse int64
	}
	liste := make([]datei, len(pfade))
	for i, pfad := range pfade {
		liste[i] = datei{pfad, DateiGroesse(pfad)}
	}
	sort.SliceStable(liste, func(i, j int) bool { return liste[i].groesse > liste[j].groesse })

	sortiert := make([]string, len(liste))
	groessen := make([]int64, len(liste))
	for i, d := range liste {
		sortiert[i], groessen[i] = d.pfad, d.groesse
	}
	return sortiert, groessen
}

// sperrPfad und vorfahrtPfad liegen neben der INI.
func sperrPfad(e Einstellungen) string {
	return filepath.Join(filepath.Dir(e.ArbeitsOrdner), "cloudforge.lock")
}

func vorfahrtPfad(e Einstellungen) string {
	return filepath.Join(filepath.Dir(e.ArbeitsOrdner), "cloudforge.vorfahrt")
}

// restDauer schätzt, wie lange die noch wartenden Dateien zusammen brauchen.
func restDauer(groessen []int64, erfahrung Erfahrung) time.Duration {
	var summe time.Duration
	for _, groesse := range groessen {
		summe += erfahrung.DauerFuerBytes(groesse)
	}
	return summe
}

func zeigeDateiErgebnis(anz *Anzeige, ergebnis DateiErgebnis) {
	eintrag := ergebnis.Eintrag
	anz.DateiAbgeschlossen(eintrag.Status, kurzErgebnis(ergebnis))

	switch eintrag.Status {
	case StatusErledigt:
		anz.Zeile("  FERTIG nach %s: %s statt %s (%s %% kleiner), VMAF %s",
			uhrText(ergebnis.Dauer),
			groesseText(eintrag.ErgebnisBytes), groesseText(eintrag.QuelleBytes),
			komma(ProzentKleiner(eintrag.QuelleBytes, eintrag.ErgebnisBytes), 0),
			komma(eintrag.VMAF, 1))
		wohinZeigen(anz, ergebnis)

	case StatusUmgepackt:
		// Den Grund hat umpackenUndAblegen schon genannt, hier nur das Ergebnis.
		anz.Zeile("  UMGEPACKT nach %s: %s statt %s (Bild unveraendert)",
			uhrText(ergebnis.Dauer), groesseText(eintrag.ErgebnisBytes), groesseText(eintrag.QuelleBytes))
		wohinZeigen(anz, ergebnis)

	case StatusUebersprungen:
		anz.Zeile("  UEBERSPRUNGEN: %s", eintrag.Meldung)

	case StatusFehler:
		anz.Zeile("  FEHLER: %s", eintrag.Meldung)
		anz.Zeile("  Das Original wurde nicht angetastet.")
	}
}

// wohinZeigen sagt nach einer fertigen Datei, wo Ergebnis und Original liegen.
func wohinZeigen(anz *Anzeige, ergebnis DateiErgebnis) {
	anz.Zeile("  Ergebnis:  %s", ergebnis.ErgebnisPfad)
	switch {
	case strings.HasPrefix(ergebnis.Eintrag.Meldung, "fertig, aber"):
		anz.Zeile("  ACHTUNG:   %s", ergebnis.Eintrag.Meldung)
	case ergebnis.OriginalPfad == "":
		anz.Zeile("  Original:  geloescht")
	default:
		anz.Zeile("  Original:  %s", ergebnis.OriginalPfad)
	}
}

// kurzErgebnis fasst eine Datei in einer Zeile für den Verlauf der
// Übersicht zusammen.
func kurzErgebnis(ergebnis DateiErgebnis) string {
	eintrag := ergebnis.Eintrag
	switch eintrag.Status {
	case StatusErledigt:
		return fmt.Sprintf("%s → %s  (–%s %%)  VMAF %s  in %s",
			groesseText(eintrag.QuelleBytes), groesseText(eintrag.ErgebnisBytes),
			komma(ProzentKleiner(eintrag.QuelleBytes, eintrag.ErgebnisBytes), 0),
			komma(eintrag.VMAF, 1), uhrText(ergebnis.Dauer))
	case StatusUmgepackt:
		return fmt.Sprintf("%s → %s  umgepackt (lohnt nicht)  in %s",
			groesseText(eintrag.QuelleBytes), groesseText(eintrag.ErgebnisBytes), uhrText(ergebnis.Dauer))
	case StatusUebersprungen:
		return "uebersprungen: " + eintrag.Meldung
	default:
		return eintrag.Meldung
	}
}

// analyseAusgeben laesst Auto-CQ die uebergebenen Dateien vermessen und zeigt
// das Ergebnis, ohne etwas umzuwandeln. Nuetzlich, um vor einem langen Lauf
// zu sehen, was herauskommen wird.
func analyseAusgeben(ctx context.Context, pfade []string, e Einstellungen) error {
	dateien, err := VideoDateienSuchen(pfade, e)
	if err != nil {
		return err
	}
	if len(dateien) == 0 {
		return Hinweis("unter dem Uebergebenen sind keine Videodateien")
	}
	if err := os.MkdirAll(e.ArbeitsOrdner, 0o755); err != nil {
		return fmt.Errorf("Arbeitsordner nicht nutzbar: %w", err)
	}

	anz := NeueAnzeige()
	freigeben, err := laufSperren(ctx, e, anz)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	defer freigeben()
	ArbeitsresteEntfernen(e.ArbeitsOrdner)

	anz.Zeile("\nAuto-CQ misst %d Datei(en). Ziel-VMAF %s, Anker CRF %d und %d.",
		len(dateien), komma(e.ZielVMAF, 1), e.AnkerNiedrig, e.AnkerHoch)

	for nummer, pfad := range dateien {
		err := eineDateiAnalysieren(ctx, anz, nummer+1, len(dateien), pfad, e)
		if errors.Is(err, ErrAbgebrochen) || ctx.Err() != nil {
			anz.Zeile("\nABGEBROCHEN.")
			return nil
		}
		if err != nil {
			anz.Zeile("  FEHLER: %v", err)
		}
	}
	return nil
}

func eineDateiAnalysieren(ctx context.Context, anz *Anzeige, nummer, gesamt int, pfad string, e Einstellungen) error {
	anz.Datei(nummer, gesamt, filepath.Base(pfad))

	info, err := KopfdatenLesen(e.FFprobePfad, pfad)
	if err != nil {
		return err
	}
	anz.Zeile("  %s  |  %s mit %s Bildern/s  |  %s Film",
		groesseText(info.GroesseBytes), aufloesungText(info, e.MaxAufloesung), komma(info.FPS, 0),
		uhrText(time.Duration(info.DauerSek)*time.Second))

	// Wie im echten Lauf (KandidatPruefen): Unter 720p wird gar nicht gemessen.
	if unter720p(info.Breite, info.Hoehe) {
		anz.Zeile("  Wuerde nur verlustfrei UMGEPACKT: %s", KleineAufloesung)
		return nil
	}

	arbeitsplatz, err := os.MkdirTemp(e.ArbeitsOrdner, arbeitsVorsilbe)
	if err != nil {
		return fmt.Errorf("Arbeitsordner nicht anlegbar: %w", err)
	}
	defer os.RemoveAll(arbeitsplatz)

	anz.Schritt(1, 1, "Qualitaet messen")
	ergebnis, err := CRFFinden(ctx, pfad, info, arbeitsplatz, e, anz.Messung)
	if err != nil {
		return err
	}
	anz.SchrittFertig(autoCQText(ergebnis))
	if ergebnis.Hinweis != "" && (ergebnis.Gedeckelt || !ergebnis.ZielErreichbar || ergebnis.DeckelNichtEinhaltbar || ergebnis.AnteilQuelle <= 0) {
		anz.Zeile("  Hinweis: %s", ergebnis.Hinweis)
	}
	// Dieselbe Entscheidung, die ein echter Lauf vor dem Umwandeln trifft —
	// knapp an der Schwelle also mit Grössenprobe.
	erwartet, ok, err := ersparnisVorhersagen(ctx, anz, 1, 1, pfad, info, ergebnis, arbeitsplatz, e)
	if err != nil {
		return err
	}
	if ok {
		if erwartet < e.MindestErsparnisProzent {
			anz.Zeile("  Wuerde nur verlustfrei UMGEPACKT: %s", lohntNichtText(erwartet, e.MindestErsparnisProzent))
		} else {
			anz.Zeile("  Wuerde umgewandelt: voraussichtlich %.0f %% kleiner", erwartet)
		}
	}
	return nil
}

func kopfAusgeben() {
	fmt.Printf("\nCloudForge %s\n", appVersion)
	fmt.Println(strings.Repeat("=", 60))
}

func standardINIPfad() string {
	heim, err := os.UserHomeDir()
	if err != nil {
		return "cloudforge.ini"
	}
	return filepath.Join(heim, "cloudforge", "cloudforge.ini")
}

// pfadeBestimmen entscheidet, womit gearbeitet wird: mit dem, was uebergeben
// wurde, oder mit den Ordnern aus der INI.
//
// Bewusst getrennt: wer Dateien uebergibt, bekommt GENAU diese verarbeitet —
// die Automatik laeuft dann nicht zusaetzlich mit.
func pfadeBestimmen(argumente []string, automatik bool, e Einstellungen, iniPfad string) ([]string, error) {
	if len(argumente) > 0 {
		if automatik {
			return nil, fmt.Errorf("entweder Dateien uebergeben ODER -automatik, nicht beides")
		}
		return argumente, nil
	}
	if automatik {
		if len(e.QuellOrdner) == 0 {
			return automatikOrdnerErfragen(iniPfad)
		}
		return e.QuellOrdner, nil
	}
	return nil, keineDateienHinweis()
}

// WerkzeugePruefen stellt sicher, dass ffmpeg und ffprobe da sind und ffmpeg
// wirklich kann, was gebraucht wird. Ohne libvmaf gibt es kein Auto-CQ —
// das faellt lieber sofort auf als nach der ersten Stunde Rechenzeit.
func WerkzeugePruefen(e Einstellungen) error {
	for name, pfad := range map[string]string{"ffmpeg": e.FFmpegPfad, "ffprobe": e.FFprobePfad} {
		if _, err := os.Stat(pfad); err != nil {
			return fmt.Errorf("%s nicht gefunden: %s\n       Pfad in der INI pruefen.", name, pfad)
		}
	}

	fehlend, err := fehlendeBausteine(e.FFmpegPfad)
	if err != nil {
		return err
	}
	if len(fehlend) > 0 {
		return fmt.Errorf("diesem ffmpeg fehlt: %s\n"+
			"       Ubuntus eigenes ffmpeg bringt KEIN libvmaf mit.\n"+
			"       Gebraucht wird ein Build von github.com/BtbN/FFmpeg-Builds.",
			strings.Join(fehlend, ", "))
	}
	return nil
}

// fehlendeBausteine fragt ffmpeg, was es kann, und meldet, was fehlt.
func fehlendeBausteine(ffmpegPfad string) ([]string, error) {
	filter, err := exec.Command(ffmpegPfad, "-hide_banner", "-filters").Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg laesst sich nicht starten: %w", err)
	}
	encoder, err := exec.Command(ffmpegPfad, "-hide_banner", "-encoders").Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg laesst sich nicht starten: %w", err)
	}

	var fehlend []string
	if !strings.Contains(string(filter), "libvmaf") {
		fehlend = append(fehlend, "libvmaf (fuer die Qualitaetsmessung)")
	}
	if !strings.Contains(string(encoder), "libsvtav1") {
		fehlend = append(fehlend, "libsvtav1 (der AV1-Encoder)")
	}
	return fehlend, nil
}

// berichtAusgeben prueft alle gefundenen Dateien und zeigt das Ergebnis,
// ohne irgendetwas zu veraendern.
func berichtAusgeben(pfade []string, e Einstellungen) error {
	fmt.Println("\nSuche Videodateien ...")
	dateien, err := VideoDateienSuchen(pfade, e)
	if err != nil {
		return err
	}
	if len(dateien) == 0 {
		fmt.Println("Keine Videodateien gefunden.")
		return nil
	}
	fmt.Printf("%d Videodateien gefunden. Lese Kopfdaten ...\n\n", len(dateien))

	kandidaten := make([]Kandidat, 0, len(dateien))
	fortschritt := NeuerFortschritt(len(dateien))
	for _, pfad := range dateien {
		fortschritt.Schritt()
		kandidaten = append(kandidaten, KandidatPruefen(pfad, e))
	}
	fortschritt.Fertig()

	zeigeLohnende(kandidaten)
	zeigeUebersprungene(kandidaten)
	zeigeZusammenfassung(kandidaten, ErfahrungLesen(StandardZustandPfad(e)))
	return nil
}

func zeigeLohnende(kandidaten []Kandidat) {
	lohnende := make([]Kandidat, 0, len(kandidaten))
	for _, k := range kandidaten {
		if k.Lohnt() {
			lohnende = append(lohnende, k)
		}
	}
	if len(lohnende) == 0 {
		fmt.Println("Keine Datei, bei der sich das Umwandeln lohnt.")
		return
	}

	// Ertragreichste zuerst — in dieser Reihenfolge wuerde auch gearbeitet,
	// damit der Platzgewinn frueh anfaellt.
	sort.Slice(lohnende, func(a, b int) bool {
		return lohnende[a].SparSchaetzungMB > lohnende[b].SparSchaetzungMB
	})

	fmt.Printf("ZU VERARBEITEN (%d Dateien, ertragreichste zuerst)\n", len(lohnende))
	fmt.Println(strings.Repeat("-", 78))
	fmt.Printf("%-40s %9s %8s %7s %10s\n", "Datei", "Groesse", "Bitrate", "fps", "spart ca.")
	fmt.Println(strings.Repeat("-", 78))

	const zeigeHoechstens = 25
	for i, k := range lohnende {
		if i >= zeigeHoechstens {
			fmt.Printf("... und %d weitere\n", len(lohnende)-zeigeHoechstens)
			break
		}
		fmt.Printf("%-40s %8.0fM %6dk %7.0f %9.0fM\n",
			kuerzen(filepath.Base(k.Info.Pfad), 40),
			k.Info.GroesseMB(),
			k.Info.VideoBitrateKbps,
			k.Info.FPS,
			k.SparSchaetzungMB)
	}
	fmt.Println()
}

func zeigeUebersprungene(kandidaten []Kandidat) {
	umpacken := make(map[Uebersprungen]int)
	uebersprungen := make(map[Uebersprungen]int)
	for _, k := range kandidaten {
		switch {
		case k.Lohnt():
		case k.NurUmpacken():
			umpacken[k.Grund]++
		default:
			uebersprungen[k.Grund]++
		}
	}
	gruendeZeigen("NUR VERLUSTFREI UMPACKEN (lohnt nicht umzuwandeln)", umpacken)
	gruendeZeigen("UEBERSPRUNGEN", uebersprungen)
}

func gruendeZeigen(ueberschrift string, nachGrund map[Uebersprungen]int) {
	if len(nachGrund) == 0 {
		return
	}
	fmt.Println(ueberschrift)
	fmt.Println(strings.Repeat("-", 78))
	for grund, anzahl := range nachGrund {
		fmt.Printf("  %4d x  %s\n", anzahl, grund)
	}
	fmt.Println()
}

func zeigeZusammenfassung(kandidaten []Kandidat, erfahrung Erfahrung) {
	var gesamtMB, sparMB, spielzeitSek, bilder float64
	lohnende := 0
	for _, k := range kandidaten {
		gesamtMB += k.Info.GroesseMB()
		if k.Lohnt() {
			lohnende++
			sparMB += k.SparSchaetzungMB
			spielzeitSek += k.Info.DauerSek
			bilder += k.Info.DauerSek * k.Info.FPS
		}
	}

	fmt.Println(strings.Repeat("=", 78))
	fmt.Printf("Gefunden:       %d Dateien, %.1f GB\n", len(kandidaten), gesamtMB/1024)
	fmt.Printf("Zu verarbeiten: %d Dateien, %s Spielzeit\n", lohnende, spielzeitText(spielzeitSek))
	fmt.Printf("Ersparnis:      geschaetzt %.1f GB (%.0f%%)\n", sparMB/1024, anteil(sparMB, gesamtMB))
	fmt.Println()

	herkunft := "Startwert - noch keine Datei mit Zeitmessung fertig"
	if erfahrung.Dateien > 0 {
		herkunft = fmt.Sprintf("gemessen an den letzten %d fertigen Dateien", erfahrung.Dateien)
	}
	fmt.Printf("Rechenzeit:     geschaetzt %s durchgehend\n", dauerText(erfahrung.DauerFuerBilder(bilder)))
	fmt.Printf("                (%s Bilder je Sekunde, %s)\n", komma(erfahrung.BilderProSek, 0), herkunft)
	fmt.Println()
	fmt.Println("Beide Zahlen sind SCHAETZUNGEN, keine Zusagen. Die Ersparnis stammt aus")
	fmt.Println("der Quellbitrate - den echten Wert kennt erst Auto-CQ nach dem Messen.")
}

func anteil(teil, ganzes float64) float64 {
	if ganzes <= 0 {
		return 0
	}
	return teil / ganzes * 100
}

// spielzeitText gibt lange Spielzeiten in Tagen aus, sonst in Stunden.
func spielzeitText(sekunden float64) string {
	stunden := sekunden / 3600
	if stunden >= 48 {
		return fmt.Sprintf("%.1f Tage", stunden/24)
	}
	return fmt.Sprintf("%.1f Stunden", stunden)
}

func kuerzen(text string, laenge int) string {
	if len(text) <= laenge {
		return text
	}
	if laenge <= 3 {
		return text[:laenge]
	}
	return text[:laenge-3] + "..."
}

func hilfeAusgeben() {
	fmt.Printf(`CloudForge %s - wandelt Videos nach AV1 um und spart Cloud-Platz

AM EINFACHSTEN
  Videodateien oder ganze Ordner mit der Maus auf das Schreibtisch-Symbol
  "CloudForge: Dateien umwandeln" ziehen. Der Rest geht von allein.

AUFRUFE IM TERMINAL
  cloudforge DATEI|ORDNER ...    verarbeitet genau das Uebergebene.
                                 Ein Ordner wird mit allen Unterordnern
                                 durchsucht. Die Automatik laeuft dabei NICHT
                                 zusaetzlich mit.

  cloudforge -automatik          arbeitet den Ordner aus der INI ab
                                 (quellOrdner). Ist keiner eingetragen, wird
                                 beim ersten Mal danach gefragt.

  cloudforge -bericht [PFAD...]  zeigt nur, was zu tun waere: welche Dateien
                                 sich lohnen, welche uebersprungen werden und
                                 wie viel Platz ungefaehr zu holen ist.
                                 Veraendert nichts.

  cloudforge -analyse DATEI ...  misst mit Auto-CQ, welcher CRF gewaehlt
                                 wuerde, ohne umzuwandeln.

  cloudforge -starter            legt die Symbole auf dem Schreibtisch neu an.

  cloudforge -zeitplan an        arbeitet alle 30 Minuten von selbst die
                                 Automatik-Ordner ab - ohne Fenster, solange
                                 du angemeldet bist und die pCloud-App laeuft.
  cloudforge -zeitplan aus       schaltet das wieder ab.

  cloudforge -protokoll          zeigt laufend, was CloudForge tut - auch im
                                 Hintergrund (Symbol "Protokoll ansehen").

  cloudforge -ini PFAD           andere Einstellungsdatei benutzen.
  cloudforge -hilfe              diese Uebersicht.

SO ARBEITET ES
  Fuer jede Datei entsteht neben der Quelle ein Unterordner (Voreinstellung
  "output") mit dem Ergebnis. Das Original wandert in "originals".
  Beides laesst sich in der INI aendern.

  Die groessten Dateien kommen zuerst dran - sie bringen den meisten Platz.

  Uebersprungen wird, was schon AV1 ist, was bereits ein Ergebnis hat und
  was sich nicht lohnt: Wird eine Datei nicht mindestens um
  mindestErsparnisProzent kleiner, bleibt das Original (das steht schon
  nach der Qualitaetsmessung fest, nicht erst nach dem Umwandeln).

  Es rechnet immer nur ein CloudForge gleichzeitig. Ziehst du waehrenddessen
  weitere Dateien auf das Symbol, warten sie und kommen danach dran. Ein
  Hintergrundlauf des Zeitplans macht nach seiner aktuellen Datei Platz.

  Jeder Lauf schreibt ein Protokoll nach ~/cloudforge/protokoll/ (30 Tage).

  Abbrechen: Fenster schliessen oder Strg+C. Das Original bleibt unberuehrt,
  Fertiges bleibt fertig, die angefangene Datei beginnt beim naechsten Mal neu.

EINSTELLUNGEN
  %s
  Fehlt die Datei, wird sie beim ersten Start angelegt.
`, appVersion, standardINIPfad())
}
