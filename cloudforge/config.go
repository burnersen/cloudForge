// CloudForge (https://github.com/burnersen/cloudForge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

// Einstellungen aus der INI-Datei. Aufbau bewusst wie beim NVENCForge:
// schluessel=wert, Zeilen mit # sind Kommentare. Beim Start bringt das
// Programm die Datei in Form (seit 0.14.0, wie NVENCForge 2.0): Reihenfolge
// und Erklärungen wie ab Werk, die Werte des Nutzers bleiben. So muss eine
// alte INI nach einem Update nie von Hand nachgepflegt werden.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Behandlung des Originals nach erfolgreicher Umwandlung.
const (
	OriginalVerschieben = "verschieben" // in den originals-Ordner
	OriginalLoeschen    = "loeschen"    // in den pCloud-Papierkorb
	OriginalBehalten    = "behalten"    // nichts tun
)

// Grenzen, die nicht aus der INI kommen dürfen, weil sie Unsinn erlauben würden.
const (
	maxKerneHart      = 7  // SVT-AV1 4.2 nutzt ohnehin höchstens 6; 7 bleibt erlaubt, damit alte INIs laden
	minMessfensterSec = 8  // Projekt-Lektion: kürzere Fenster verschieben die CRF-Wahl
	maxCRFHart        = 63 // SVT-AV1-Skala
)

// Einstellungen hält alle Werte, die das Programm zur Laufzeit braucht.
type Einstellungen struct {
	// Werkzeuge
	FFmpegPfad  string
	FFprobePfad string

	// Ordner
	QuellOrdner        []string // von der Automatik abgearbeitet
	ArbeitsOrdner      string   // lokale Zwischenablage auf dem VPS
	AusgabeOrdnerName  string   // Unterordner neben der Quelle
	OriginalOrdnerName string   // Unterordner neben der Quelle

	// Was mit dem Original geschieht
	OriginalBehandlung string

	// Encoder
	Preset        int
	Kerne         int
	Bittiefe      int  // 8 oder 10
	VarianceBoost bool // SVT-AV1: ruhigen, dunklen Flächen mehr Bits geben
	Tune0         bool // SVT-AV1 tune 0 (Seheindruck) statt tune 1 (PSNR)
	ZielVMAF      float64
	AnkerNiedrig  int // CRF des besseren Ankers
	AnkerHoch     int // CRF des sparsameren Ankers
	CRFMin        int
	CRFMax        int

	// Auto-CQ-Verhalten
	MessfensterAnzahl int
	MessfensterSek    float64
	PlateauToleranz   float64 // wie viel VMAF eine Sprosse kosten darf
	PlateauMindestSpa float64 // wie viel Prozent sie mindestens sparen muss

	// Kosten-Deckel (deckel.go): höchstens so viel Prozent der Quelle, 0 = aus
	KostenDeckelProzent float64

	// Was übersprungen wird
	MindestErsparnisProzent float64

	// Betrieb
	PlatzReserveGB     int
	Vollpruefung       bool // Ergebnis vor dem Ablegen komplett dekodieren
	MaxStundenProDatei int
}

// standardWerte liefert die Voreinstellung — seit 0.7.1 genau die Werte, die
// der Nutzer auf dem netcup-Server gewählt hat, damit eine Neuinstallation
// gleich so arbeitet wie sein eingerichteter Server:
//   - Ziel 95 als Untergrenze (seit 0.15.0, Nutzerwahl 28.09.2026 nach einem
//     Dauerlauf mit 95, der ihn überzeugt hat). Der Weg dahin: 98 am 25.09. an
//     Vergleichsclips gewählt, 97 ab 0.10.0 als Mittelweg, 96 ab 0.11.3;
//     gedeckelte Clips mit 93 waren ihm zu schlecht.
//   - Anker 22/32 (seit 0.15.0, vorher 16/26 für Ziel 96 bis 98): Einer soll
//     über, einer unter dem Ziel landen. Mit 16/26 lagen bei Ziel 95 meist
//     beide darüber — der hohe lag bei vielen Filmen um 96. Der niedrige ist
//     zugleich die beste Qualität, die Auto-CQ je anbietet.
//   - 5 Messfenster (seit 0.11.3): Mit 3 lag der hochgerechnete Anteil am
//     27.09.2026 bis 10 Prozentpunkte neben dem ganzen Film (bei einem: 42
//     statt 31 %), obwohl die Proben an den Messstellen genau stimmten — die
//     drei Stellen trafen den Schnitt des Films nicht. Messen: 2,5 statt 2,2 Min.
//   - 10 Bit gegen Streifen in Farbverläufen, kostet dort nur ~20 % Tempo.
//   - preset 9: 1080p50 mit 1,27x Echtzeit, preset 8 wäre knapp darunter.
//   - Variance Boost und tune 0 aus: der Nutzer testet sie selbst.
//
// Die frühere Aussage "VMAF 96 ist nicht erreichbar" (21.09., Contabo) war
// vermutlich ein Messfehler (Bildpaarung nach Zeit, siehe VMAFMessen).
func standardWerte() Einstellungen {
	heim, _ := os.UserHomeDir()
	basis := filepath.Join(heim, "cloudforge")

	return Einstellungen{
		FFmpegPfad:  filepath.Join(basis, "tools", "ffmpeg"),
		FFprobePfad: filepath.Join(basis, "tools", "ffprobe"),

		QuellOrdner:        nil,
		ArbeitsOrdner:      filepath.Join(basis, "arbeit"),
		AusgabeOrdnerName:  "output",
		OriginalOrdnerName: "originals",

		OriginalBehandlung: OriginalVerschieben,

		Preset:        9,
		Kerne:         6,
		Bittiefe:      10,
		VarianceBoost: false,
		Tune0:         false,
		ZielVMAF:      95,
		AnkerNiedrig:  22,
		AnkerHoch:     32,
		CRFMin:        14,
		CRFMax:        44,

		MessfensterAnzahl: 5,
		MessfensterSek:    8,
		PlateauToleranz:   0.5, // wie in NVENCForge: Bild vor den letzten Prozent Platz
		PlateauMindestSpa: 5,

		// Den Deckel (bis 0.11.1: 50) hat der Nutzer am 27.09.2026
		// abgeschaltet: gedeckelte Clips mit VMAF 93 sahen für ihn deutlich
		// schlechter aus als das Original — die Qualität geht vor. Begrenzt
		// wird nur noch über die Mindestersparnis: seit 0.11.3 15 % (vorher 30,
		// Nutzerwahl 27.09.2026) — darunter lohnt die
		// Rechenzeit kaum, und das Original bleibt verlustfrei erhalten.
		KostenDeckelProzent:     0,
		MindestErsparnisProzent: 15,

		PlatzReserveGB:     20,
		Vollpruefung:       false,
		MaxStundenProDatei: 8,
	}
}

// iniZeile beschreibt einen Schlüssel für das Schreiben und Nachrüsten der INI.
type iniZeile struct {
	schluessel string
	wert       func(Einstellungen) string
	erklaerung string
}

// iniAufbau bestimmt Reihenfolge und Kommentare der INI-Datei. Eine neue
// Einstellung wird hier eingetragen und ist damit automatisch les-, schreib-
// und nachrüstbar.
func iniAufbau() []iniZeile {
	return []iniZeile{
		{"ffmpegPfad", func(e Einstellungen) string { return e.FFmpegPfad },
			"Pfad zu ffmpeg. Muss libvmaf und libsvtav1 koennen -\n# Ubuntus eigenes ffmpeg hat KEIN libvmaf."},
		{"ffprobePfad", func(e Einstellungen) string { return e.FFprobePfad }, ""},

		{"quellOrdner", func(e Einstellungen) string { return strings.Join(e.QuellOrdner, "|") },
			"Ordner, die der Automatik-Modus abarbeitet. Mehrere mit | trennen.\n# Leer lassen, wenn nur per Drag and Drop gearbeitet wird."},
		{"arbeitsOrdner", func(e Einstellungen) string { return e.ArbeitsOrdner },
			"Lokale Zwischenablage. Niemals direkt in der Cloud rechnen."},
		{"ausgabeOrdnerName", func(e Einstellungen) string { return e.AusgabeOrdnerName },
			"Unterordner neben der Quelldatei fuer das Ergebnis."},
		{"originalOrdnerName", func(e Einstellungen) string { return e.OriginalOrdnerName },
			"Unterordner neben der Quelldatei fuer das Original."},

		{"originalBehandlung", func(e Einstellungen) string { return e.OriginalBehandlung },
			"verschieben = in den originals-Ordner (sicher, spart aber keinen Cloud-Platz)\n# loeschen    = in den Papierkorb der Cloud (spart sofort Platz)\n# behalten    = Original bleibt unberuehrt liegen"},

		{"preset", func(e Einstellungen) string { return strconv.Itoa(e.Preset) },
			"SVT-AV1-Preset: hoeher = schneller, aber groesser bei gleicher Qualitaet.\n# 11 ist das schnellste - 12 und 13 rechnet SVT-AV1 4.2 intern als 11.\n# Gemessen 25.09.2026 auf dem netcup-Server (8 Kerne), 1080p mit 50\n# Bildern/s, 10 Bit: preset 8 = 0,91x, preset 9 = 1,27x Echtzeit.\n# Filme mit 30 Bildern/s laufen entsprechend schneller (preset 9: 2,2x)."},
		{"kerne", func(e Einstellungen) string { return strconv.Itoa(e.Kerne) },
			"Parallelitaet fuer SVT-AV1 (dessen Wert lp), keine feste Kernzahl.\n# 6 ist das Maximum - hoehere Werte kappt SVT-AV1 4.2 auf 6. Ein Film\n# belegt damit gut 6 der 8 Kerne (netcup, gemessen 25.09.2026)."},
		{"bittiefe", func(e Einstellungen) string { return strconv.Itoa(e.Bittiefe) },
			"Bittiefe des Ergebnisses: 8 oder 10. 10 Bit beugt Streifen in\n# Farbverlaeufen (Banding) vor - auch bei 8-Bit-Quellen. Gemessen 25.09.2026\n# auf dem netcup-Server: 10 Bit kostet etwa 20 % Tempo, die Datei wird\n# nicht groesser. (Auf dem alten Contabo-VPS waren es noch 50 % Tempo.)"},
		{"varianceBoost", func(e Einstellungen) string { return jaNein(e.VarianceBoost) },
			"Variance Boost (SVT-AV1): gibt ruhigen, glatten und dunklen Flaechen (Waende,\n# Haut, Schatten) mehr Bits - genau dort entstehen sonst Kloetzchen und\n# Streifen. Gemessen 26.09.2026 an einem 1080p50-Film: bei gleichem CRF\n# 26 % groesser und +0,56 VMAF; beim VMAF-Ziel waehlt Auto-CQ dafuer einen\n# hoeheren CRF, die Datei wird dann kaum groesser. Ob es besser aussieht,\n# zeigt nur das Auge. ja oder nein. Mit welcher Einstellung eine Datei\n# entstand, steht im Protokoll."},
		{"tune0", func(e Einstellungen) string { return jaNein(e.Tune0) },
			"tune 0 (SVT-AV1): stimmt den Encoder auf den Seheindruck ab statt auf die\n# Rechengenauigkeit PSNR (Werk). Gemessen 26.09.2026 an einem 1080p50-Film:\n# beim gleichen VMAF-Ziel etwa 3 % groesser. Ob es schaerfer aussieht, zeigt\n# nur das Auge. ja oder nein."},
		{"zielVMAF", func(e Einstellungen) string { return zahl(e.ZielVMAF) },
			"Qualitaetsziel. Auto-CQ haelt es als UNTERGRENZE: das Ergebnis liegt nicht\n# darunter, solange das Material es ueberhaupt hergibt. Anhaltspunkte,\n# gemessen 25.09.2026 an einer 1080p-Szene mit preset 9 und 10 Bit\n# (Original 12,3 Mbit/s):\n#   VMAF 93   = CRF 33, 2,2 Mbit/s - sichtbar weicher, Kloetzchen, Streifen\n#   VMAF 96   = CRF 28, 3,2 Mbit/s\n#   VMAF 97,5 = CRF 24, 4,2 Mbit/s\n#   VMAF 98   = CRF 20, 5,2 Mbit/s - kaum vom Original zu unterscheiden\n# 1080p-Filme mit 50 Bildern/s erreichen 98 meist gar nicht (26.09.2026:\n# nur 3 von 23 Filmen) - dort greift plateauToleranz.\n# Wer das Ziel aendert, legt die Anker so, dass der bessere darueber landet."},
		{"ankerNiedrig", func(e Einstellungen) string { return strconv.Itoa(e.AnkerNiedrig) },
			"Die zwei CRF-Werte, die Auto-CQ zuerst misst, um die Kurve zu schaetzen.\n# Der niedrige sollte ueber dem Ziel landen, der hohe darunter: fuer\n# Ziel 94 bis 95 etwa 22 und 32 (ab Werk), fuer Ziel 96 bis 98 etwa 16\n# und 26. Der niedrige ist zugleich die beste Qualitaet, die Auto-CQ je\n# anbietet."},
		{"ankerHoch", func(e Einstellungen) string { return strconv.Itoa(e.AnkerHoch) }, ""},
		{"crfMin", func(e Einstellungen) string { return strconv.Itoa(e.CRFMin) },
			"Klemme: Auto-CQ verlaesst diesen Bereich nie."},
		{"crfMax", func(e Einstellungen) string { return strconv.Itoa(e.CRFMax) }, ""},
		{"kostenDeckelProzent", func(e Einstellungen) string { return zahl(e.KostenDeckelProzent) },
			"Kosten-Deckel wie bei NVENCForge: Das Bild darf hoechstens so viel Prozent\n# der Quelle kosten (gemessen an den Messstellen). Greift nur bei schon\n# stark komprimierten Quellen - dort sinkt die Qualitaet dann UNTER das Ziel.\n# Ab Werk 0 = kein Deckel (seit 0.11.2): Gedeckelte Vergleichsclips mit\n# VMAF 93 sahen am 27.09.2026 deutlich schlechter aus als das Original.\n# Ohne Deckel bleibt die Qualitaet beim Ziel; was dabei keine\n# mindestErsparnisProzent spart, wird nur verlustfrei umgepackt.\n# Wer ihn trotzdem will: 50 = hoechstens die Haelfte der Quelle. Ist der\n# Deckel gar nicht einzuhalten, bleibt die Qualitaetswahl stehen."},

		{"messfensterAnzahl", func(e Einstellungen) string { return strconv.Itoa(e.MessfensterAnzahl) },
			"Wie viele Stichproben Auto-CQ misst, gleichmaessig verteilt. 5 ab Werk:\n# Mit 3 lag die Groessen-Vorhersage am 27.09.2026 bis 10 Prozentpunkte\n# neben dem ganzen Film. Mehr Stichproben messen genauer, aber laenger:\n# 5 statt 3 kostet etwa zwei Drittel mehr Messzeit."},
		{"messfensterSek", func(e Einstellungen) string { return zahl(e.MessfensterSek) },
			"Laenge je Stichprobe. NIEMALS unter 8 - kuerzere Fenster verschieben\n# die CRF-Wahl um mehrere Stufen (gemessen im NVENCForge-Projekt)."},
		{"plateauToleranz", func(e Einstellungen) string { return zahl(e.PlateauToleranz) },
			"Ist das Ziel nicht erreichbar, darf Auto-CQ hoechstens so viel VMAF unter\n# dem besten Wert (bei ankerNiedrig) bleiben, um Platz zu sparen. 0,5 wie in\n# NVENCForge (Entscheidung des Nutzers: das Bild ist wichtiger als die\n# letzten Prozent Platz)."},
		{"plateauMindestSparen", func(e Einstellungen) string { return zahl(e.PlateauMindestSpa) },
			"Der Aufstieg wird nur genommen, wenn er je CRF-Stufe im Mittel mindestens\n# so viel Prozent spart."},

		{"mindestErsparnisProzent", func(e Einstellungen) string { return zahl(e.MindestErsparnisProzent) },
			"Wird die Datei nicht um mindestens so viel Prozent kleiner, wird sie nicht\n# umgewandelt, sondern nur verlustfrei nach MKV umgepackt (Bild unveraendert,\n# wie NVENCForge) und als name.h264.mkv o. ae. in output gelegt; das Original\n# wandert wie sonst nach originals. Zweimal geprueft: gleich nach der\n# Qualitaetsmessung aus den Messproben hochgerechnet (spart die ganze\n# Rechenzeit) und nach dem Umwandeln an der echten Datei."},

		{"platzReserveGB", func(e Einstellungen) string { return strconv.Itoa(e.PlatzReserveGB) },
			"So viel Platz bleibt immer frei. Sonst wartet das Programm."},
		{"vollpruefung", func(e Einstellungen) string { return jaNein(e.Vollpruefung) },
			"Ergebnis vor dem Ablegen einmal komplett dekodieren. Gemessen 23.09.2026:\n# kostet 23-27 Min je 55-Min-Film. Die schnellen Pruefungen (Groesse,\n# Spieldauer, Ton- und Untertitelspuren) laufen immer. Einschalten, wenn die\n# Originale sofort geloescht werden (originalBehandlung=loeschen)."},
		{"maxStundenProDatei", func(e Einstellungen) string { return strconv.Itoa(e.MaxStundenProDatei) },
			"Notbremse: dauert eine Datei laenger, wird sie uebersprungen."},
	}
}

func zahl(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
func jaNein(b bool) string {
	if b {
		return "ja"
	}
	return "nein"
}

// ausgemusterteSchluessel braucht das Programm nicht mehr. Beim Aufräumen
// verschwinden sie still aus der INI. Jede andere unbekannte Zeile — etwa ein
// vertippter Schlüssel — wird beim Start genannt: ihr Wert hat nie gewirkt.
var ausgemusterteSchluessel = map[string]bool{
	"autoCrop":         true, // seit 0.9.0: war nie eingebaut, täuschte eine Wirkung nur vor
	"parallelerUpload": true, // seit 0.9.0: ebenso
	"tonSchwelleKbps":  true, // seit 0.14.0: Ton wird immer 1:1 kopiert
	"tonZielKbps":      true, // seit 0.14.0: ebenso
}

// iniSicherungEndung: So heisst die bisherige Fassung, bevor das Aufräumen
// die INI neu schreibt. Es gibt immer nur diese eine Sicherung.
const iniSicherungEndung = ".bak"

// utf8BOM ist die unsichtbare Markierung, die Windows-Editoren gern an den
// Anfang einer Textdatei setzen.
const utf8BOM = string(rune(0xFEFF))

// EinstellungenLaden liest die INI. Fehlt sie, wird sie mit den Standardwerten
// angelegt. Danach bringt iniAufraeumen sie in Form; was es dabei getan hat,
// steht in hinweise (für den Start-Bildschirm).
func EinstellungenLaden(pfad string) (e Einstellungen, hinweise []string, err error) {
	e = standardWerte()

	alt, err := os.ReadFile(pfad)
	if errors.Is(err, fs.ErrNotExist) {
		if schreibErr := iniAnlegen(pfad, e); schreibErr != nil {
			return e, nil, fmt.Errorf("INI konnte nicht angelegt werden: %w", schreibErr)
		}
		return e, nil, nil
	}
	if err != nil {
		return e, nil, fmt.Errorf("INI nicht lesbar: %w", err)
	}

	unbekannt, err := iniWerteLesen(string(alt), &e)
	if err != nil {
		return e, nil, err
	}
	// Aufgeräumt wird mit den Werten, wie der Nutzer sie eingetragen hat.
	// pruefeGrenzen ändert danach nur, womit das Programm rechnet.
	hinweise = iniAufraeumen(pfad, string(alt), e, unbekannt)
	pruefeGrenzen(&e)
	return e, hinweise, nil
}

// iniWerteLesen übernimmt jede Zeile schluessel=wert in e. Zurück kommen die
// Zeilen, die das Programm nicht kennt — ausgemusterte Schlüssel ausgenommen.
// Ein ungültiger Wert ist ein Fehler: dann wird auch nichts aufgeräumt.
func iniWerteLesen(inhalt string, e *Einstellungen) (unbekannt []string, err error) {
	// Windows-Editoren setzen gern eine unsichtbare Markierung (BOM) an den
	// Anfang — sie gehört nicht zur ersten Zeile.
	inhalt = strings.TrimPrefix(inhalt, utf8BOM)
	bekannt := bekannteSchluessel()
	for nummer, zeile := range strings.Split(inhalt, "\n") {
		zeile = strings.TrimSpace(zeile)
		if zeile == "" || strings.HasPrefix(zeile, "#") {
			continue
		}
		schluessel, wert, ok := strings.Cut(zeile, "=")
		if !ok {
			unbekannt = append(unbekannt, zeile)
			continue
		}
		schluessel, wert = strings.TrimSpace(schluessel), strings.TrimSpace(wert)
		if !bekannt[schluessel] {
			if !ausgemusterteSchluessel[schluessel] {
				unbekannt = append(unbekannt, schluessel)
			}
			continue
		}
		if err := wertUebernehmen(e, schluessel, wert); err != nil {
			return nil, fmt.Errorf("INI Zeile %d (%s): %w", nummer+1, schluessel, err)
		}
	}
	return unbekannt, nil
}

func bekannteSchluessel() map[string]bool {
	bekannt := make(map[string]bool)
	for _, z := range iniAufbau() {
		bekannt[z.schluessel] = true
	}
	return bekannt
}

// iniAufraeumen schreibt die INI neu, wenn sie von der Werksform abweicht:
// Reihenfolge und Erklärungen aus iniAufbau, die Werte aus e. Vorher kommt
// die bisherige Fassung in die Sicherung — mit allem, was danach nicht mehr
// drinsteht (eigene Kommentare, unbekannte Zeilen).
//
// Scheitert etwas, bleibt die INI, wie sie ist. Die Werte gelten trotzdem,
// nur die Form ist nicht aufgeräumt — deshalb ein Hinweis statt eines
// Fehlers, der den Lauf verhindern würde.
func iniAufraeumen(pfad, alt string, e Einstellungen, unbekannt []string) []string {
	neu := iniText(e)
	if neu == alt {
		return nil
	}
	sicherung := pfad + iniSicherungEndung
	if err := dateiErsetzen(sicherung, alt); err != nil {
		return []string{fmt.Sprintf("INI nicht aufgeraeumt, Sicherung nicht schreibbar: %v", err)}
	}
	if err := dateiErsetzen(pfad, neu); err != nil {
		return []string{fmt.Sprintf("INI nicht aufgeraeumt: %v", err)}
	}
	hinweise := []string{"INI aufgeraeumt (Reihenfolge und Erklaerungen wie ab Werk, deine Werte bleiben)," +
		" vorherige Fassung: " + sicherung}
	if len(unbekannt) > 0 {
		hinweise = append(hinweise, "Unbekannt und deshalb entfernt (steht noch in der Sicherung): "+
			strings.Join(unbekannt, ", "))
	}
	return hinweise
}

// wertUebernehmen setzt genau einen Schlüssel. Ein unbekannter Schlüssel ist
// kein Fehler — so überlebt eine INI auch das Zurückrüsten auf eine ältere
// Programmfassung.
func wertUebernehmen(e *Einstellungen, schluessel, wert string) error {
	switch schluessel {
	case "ffmpegPfad":
		e.FFmpegPfad = wert
	case "ffprobePfad":
		e.FFprobePfad = wert
	case "quellOrdner":
		e.QuellOrdner = ordnerlisteLesen(wert)
	case "arbeitsOrdner":
		e.ArbeitsOrdner = wert
	case "ausgabeOrdnerName":
		e.AusgabeOrdnerName = wert
	case "originalOrdnerName":
		e.OriginalOrdnerName = wert
	case "originalBehandlung":
		return behandlungLesen(e, wert)
	case "preset":
		return ganzzahl(wert, 0, 13, &e.Preset)
	case "kerne":
		return ganzzahl(wert, 1, maxKerneHart, &e.Kerne)
	case "bittiefe":
		return bittiefeLesen(e, wert)
	case "varianceBoost":
		e.VarianceBoost = istJa(wert)
	case "tune0":
		e.Tune0 = istJa(wert)
	case "zielVMAF":
		return kommazahl(wert, 1, 100, &e.ZielVMAF)
	case "ankerNiedrig":
		return ganzzahl(wert, 0, maxCRFHart, &e.AnkerNiedrig)
	case "ankerHoch":
		return ganzzahl(wert, 0, maxCRFHart, &e.AnkerHoch)
	case "crfMin":
		return ganzzahl(wert, 0, maxCRFHart, &e.CRFMin)
	case "crfMax":
		return ganzzahl(wert, 0, maxCRFHart, &e.CRFMax)
	case "messfensterAnzahl":
		return ganzzahl(wert, 1, 10, &e.MessfensterAnzahl)
	case "messfensterSek":
		return kommazahl(wert, minMessfensterSec, 60, &e.MessfensterSek)
	case "plateauToleranz":
		return kommazahl(wert, 0, 20, &e.PlateauToleranz)
	case "plateauMindestSparen":
		return kommazahl(wert, 0, 100, &e.PlateauMindestSpa)
	case "kostenDeckelProzent":
		return kommazahl(wert, 0, 100, &e.KostenDeckelProzent)
	case "mindestErsparnisProzent":
		return kommazahl(wert, 0, 99, &e.MindestErsparnisProzent)
	case "platzReserveGB":
		return ganzzahl(wert, 0, 10000, &e.PlatzReserveGB)
	case "vollpruefung":
		e.Vollpruefung = istJa(wert)
	case "maxStundenProDatei":
		return ganzzahl(wert, 1, 240, &e.MaxStundenProDatei)
	}
	// Unbekannte Schlüssel werden übergangen, alte INIs laden damit weiter
	// fehlerfrei. Beim Aufräumen verschwinden sie aus der Datei (siehe
	// ausgemusterteSchluessel).
	return nil
}

func behandlungLesen(e *Einstellungen, wert string) error {
	switch strings.ToLower(wert) {
	case OriginalVerschieben, OriginalLoeschen, OriginalBehalten:
		e.OriginalBehandlung = strings.ToLower(wert)
		return nil
	}
	return fmt.Errorf("unbekannter Wert %q (erlaubt: %s, %s, %s)",
		wert, OriginalVerschieben, OriginalLoeschen, OriginalBehalten)
}

// bittiefeLesen lässt nur 8 und 10 zu — alles andere kann SVT-AV1 hier nicht.
func bittiefeLesen(e *Einstellungen, wert string) error {
	switch wert {
	case "8":
		e.Bittiefe = 8
	case "10":
		e.Bittiefe = 10
	default:
		return fmt.Errorf("unbekannter Wert %q (erlaubt: 8 oder 10)", wert)
	}
	return nil
}

func ordnerlisteLesen(wert string) []string {
	var ordner []string
	for _, teil := range strings.Split(wert, "|") {
		if teil = strings.TrimSpace(teil); teil != "" {
			ordner = append(ordner, teil)
		}
	}
	return ordner
}

func ganzzahl(wert string, min, max int, ziel *int) error {
	n, err := strconv.Atoi(wert)
	if err != nil {
		return fmt.Errorf("%q ist keine ganze Zahl", wert)
	}
	if n < min || n > max {
		return fmt.Errorf("%d liegt ausserhalb von %d bis %d", n, min, max)
	}
	*ziel = n
	return nil
}

func kommazahl(wert string, min, max float64, ziel *float64) error {
	// Immer mit Punkt als Trennzeichen lesen, damit eine deutsche
	// Spracheinstellung nichts verfälscht.
	f, err := strconv.ParseFloat(strings.Replace(wert, ",", ".", 1), 64)
	if err != nil {
		return fmt.Errorf("%q ist keine Zahl", wert)
	}
	if f < min || f > max {
		return fmt.Errorf("%v liegt ausserhalb von %v bis %v", f, min, max)
	}
	*ziel = f
	return nil
}

func istJa(wert string) bool {
	switch strings.ToLower(strings.TrimSpace(wert)) {
	case "ja", "true", "1", "an", "yes":
		return true
	}
	return false
}

// pruefeGrenzen fängt Wertekombinationen ab, die einzeln gültig sind, aber
// zusammen keinen Sinn ergeben.
func pruefeGrenzen(e *Einstellungen) {
	if e.AnkerNiedrig >= e.AnkerHoch {
		e.AnkerNiedrig, e.AnkerHoch = standardWerte().AnkerNiedrig, standardWerte().AnkerHoch
	}
	if e.CRFMin >= e.CRFMax {
		e.CRFMin, e.CRFMax = standardWerte().CRFMin, standardWerte().CRFMax
	}
	if e.Kerne > maxKerneHart {
		e.Kerne = maxKerneHart
	}
}

// iniText ist die INI in Werksform mit den Werten aus e.
func iniText(e Einstellungen) string {
	var inhalt strings.Builder
	inhalt.WriteString("# CloudForge — Einstellungen\n")
	inhalt.WriteString("# Zeilen mit # sind Kommentare. Beim Start bringt CloudForge diese Datei in\n")
	inhalt.WriteString("# Form: Reihenfolge und Erklaerungen wie ab Werk, deine Werte bleiben.\n")
	inhalt.WriteString("# Eigene Kommentare und unbekannte Zeilen stehen danach nur noch in der\n")
	inhalt.WriteString("# Sicherung daneben (gleicher Name mit " + iniSicherungEndung + ").\n")

	for _, z := range iniAufbau() {
		inhalt.WriteString("\n")
		if z.erklaerung != "" {
			inhalt.WriteString("# " + z.erklaerung + "\n")
		}
		inhalt.WriteString(z.schluessel + "=" + z.wert(e) + "\n")
	}
	return inhalt.String()
}

func iniAnlegen(pfad string, e Einstellungen) error {
	if err := os.MkdirAll(filepath.Dir(pfad), 0o755); err != nil {
		return err
	}
	return dateiErsetzen(pfad, iniText(e))
}

// dateiErsetzen schreibt über eine Nebendatei und Umbenennen, damit ein
// Absturz mitten im Schreiben nie eine halbe Datei hinterlässt.
func dateiErsetzen(pfad, inhalt string) error {
	tarnPfad := pfad + ".neu"
	if err := os.WriteFile(tarnPfad, []byte(inhalt), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tarnPfad, pfad); err != nil {
		os.Remove(tarnPfad)
		return err
	}
	return nil
}

// IniWertSetzen ändert einen einzelnen Schlüssel in der INI und lässt alles
// andere — Kommentare, Reihenfolge, eigene Werte — unberührt. Fehlt der
// Schlüssel, wird er angehängt.
func IniWertSetzen(pfad, schluessel, wert string) error {
	inhalt, err := os.ReadFile(pfad)
	if err != nil {
		return fmt.Errorf("INI nicht lesbar: %w", err)
	}

	neueZeile := schluessel + "=" + wert
	zeilen := strings.Split(string(inhalt), "\n")
	gesetzt := false
	for i, zeile := range zeilen {
		sauber := strings.TrimSpace(zeile)
		if strings.HasPrefix(sauber, "#") {
			continue
		}
		links, _, ok := strings.Cut(sauber, "=")
		if ok && strings.TrimSpace(links) == schluessel {
			zeilen[i] = neueZeile
			gesetzt = true
			break
		}
	}

	if !gesetzt {
		// Vor der abschliessenden Leerzeile einfügen, damit die Datei sauber
		// mit einem Zeilenumbruch endet.
		if letzte := len(zeilen) - 1; letzte >= 0 && zeilen[letzte] == "" {
			zeilen = append(zeilen[:letzte], neueZeile, "")
		} else {
			zeilen = append(zeilen, neueZeile)
		}
	}

	if err := dateiErsetzen(pfad, strings.Join(zeilen, "\n")); err != nil {
		return fmt.Errorf("INI nicht schreibbar: %w", err)
	}
	return nil
}
