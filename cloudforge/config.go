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
	maxFilmKorn       = 50 // SVT-AV1 film-grain
	// Über die Hälfte hinaus wäre es kein "unteres" Perzentil mehr, sondern
	// läge über dem Median.
	maxVMAFPerzentil = 50
	// Jede Datei braucht ihren eigenen Platz auf der Platte und ihren Anteil
	// an den Kernen; mehr als 8 gleichzeitig teilt selbst ein grosser Rechner
	// zu fein auf.
	maxParallelDateien = 8
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
	Preset          int
	Kerne           int
	ParallelDateien int  // so viele Dateien gleichzeitig (seit 0.19.0), 1 = nacheinander
	Bittiefe        int  // 8 oder 10
	MaxAufloesung   int  // kurze Kante des Ergebnisses höchstens (bildformat.go), 0 = wie die Quelle
	VarianceBoost   bool // SVT-AV1: ruhigen, dunklen Flächen mehr Bits geben
	// Feinregler des Variance Boost (seit 0.18.0), wirken nur mit VarianceBoost
	VarianceBoostStaerke int  // SVT variance-boost-strength, 1 bis 4
	VarianceOktil        int  // SVT variance-octile, 1 bis 8
	Tune0                bool // SVT-AV1 tune 0 (Seheindruck) statt tune 1 (PSNR)
	FilmKorn             int  // SVT film-grain 1 bis 50 nur im finalen Encode, 0 = aus (seit 0.19.0)
	ZielVMAF             float64
	// Seit 0.19.0: 0 = Auto-CQ misst am Mittelwert (ZielVMAF), sonst am
	// unteren Perzentil der Bildwerte (ZielVMAFPerzentil), siehe vmafZiel.
	VMAFPerzentil     int
	ZielVMAFPerzentil float64
	AnkerNiedrig      int // CRF des besseren Ankers
	AnkerHoch         int // CRF des sparsameren Ankers
	CRFMin            int
	CRFMax            int

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
//   - 4 Messfenster zu je 9 s (seit 0.20.0, Nutzerwahl 03.10.2026 nach
//     seinen Läufen mit 0.19.x; 0.11.3 bis 0.19.1: 5 x 8 s). Weniger als 4
//     sollen es nicht werden: Mit 3 lag der hochgerechnete Anteil am
//     27.09.2026 bis 10 Prozentpunkte neben dem ganzen Film (bei einem: 42
//     statt 31 %), obwohl die Proben an den Messstellen genau stimmten — die
//     drei Stellen trafen den Schnitt des Films nicht. 4 x 9 s gegen 5 x 8 s
//     ist nicht eigens gemessen (36 statt 40 s Material); Grenzfälle an der
//     Mindestersparnis prüft ohnehin die Grössenprobe an 20 Stellen.
//   - 10 Bit gegen Streifen in Farbverläufen, kostet dort nur ~20 % Tempo.
//   - preset 9: 1080p50 mit 1,27x Echtzeit, preset 8 wäre knapp darunter.
//   - Variance Boost an (seit 0.19.0, vorher aus): Er gibt kontrastarmen,
//     glatten Flächen mehr Bits — genau dort blockt AV1 zuerst, und das Ziel
//     des Nutzers ist ein Bild ohne Klötzchen in ruhigen Flächen und Schatten.
//     Beim Qualitätsziel wurde die Datei damit kaum grösser (gemessen
//     26.09.2026), der Nutzer fährt ihn seit 26.09. selbst.
//   - tune 0 an (seit 0.20.0, vorher aus): Es kostet beim selben Ziel etwa
//     3 % Grösse (gemessen 26.09.2026). VMAF sieht den Nutzen nicht; der
//     Nutzer fährt es seit 26.09. und hat am 03.10.2026 nach seinen Läufen
//     entschieden, dass ihm das Bild so gefällt.
//   - Variance Boost Stärke 2, Oktil 5 (seit 0.18.0): die Empfehlung der
//     SVT-AV1-Doku für echte Filme und zugleich die Werkswerte von SVT-AV1 —
//     wer nur varianceBoost=ja setzt, bekommt dasselbe wie bis 0.17.0.
//   - Auto-CQ misst seit 0.19.0 am 5-%-Perzentil der Bildwerte statt am
//     Mittelwert (Nutzerwunsch 01.10.2026): Der Mittelwert kann gut aussehen,
//     während einzelne Szenen deutlich schlechter sind. Ziel 92, gemessen
//     01.10.2026 an je 5 x 8 s aus 6 Filmen (preset 9, 10 Bit, seine
//     Schalter): Mit 92 werden die Dateien im Schnitt so gross wie mit
//     Mittelwert 96 (seiner INI), -0,6 % — gleichmässige Filme bis 19 %
//     kleiner, Filme mit einzelnen schwachen Szenen bis 33 % grösser. Die
//     erste Schätzung 93 hätte im Schnitt 18 % mehr gekostet.
//   - Filmkorn aus: Es kostet bei preset 9 an einem echten 1080p50-Film das
//     8,5-fache an Zeit (gemessen 01.10.2026).
//   - Eine Datei nach der anderen: Mehrere gleichzeitig muss der Nutzer
//     bewusst einschalten.
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

		Preset:               9,
		Kerne:                6,
		ParallelDateien:      1,
		Bittiefe:             10,
		MaxAufloesung:        0, // aus: niemandem ungefragt die Auflösung nehmen, mit "loeschen" wäre sie weg
		VarianceBoost:        true,
		VarianceBoostStaerke: 2,
		VarianceOktil:        5,
		Tune0:                true,
		FilmKorn:             0,
		ZielVMAF:             95,
		VMAFPerzentil:        5,
		ZielVMAFPerzentil:    92,
		AnkerNiedrig:         22,
		AnkerHoch:            32,
		CRFMin:               14,
		CRFMax:               44,

		MessfensterAnzahl: 4,
		MessfensterSek:    9,
		PlateauToleranz:   0.5, // wie in NVENCForge: Bild vor den letzten Prozent Platz
		PlateauMindestSpa: 5,

		// Den Deckel (bis 0.11.1: 50) hat der Nutzer am 27.09.2026
		// abgeschaltet: gedeckelte Clips mit VMAF 93 sahen für ihn deutlich
		// schlechter aus als das Original — die Qualität geht vor. Begrenzt
		// wird nur noch über die Mindestersparnis: seit 0.20.0 7 % (die Wahl
		// des Nutzers in seiner INI; 0.19.x 10 %, 0.11.3 bis 0.18.0 15 %,
		// davor 30). Die Qualität sichert das VMAF-Ziel; darunter lohnt die
		// Rechenzeit kaum, und das Original bleibt verlustfrei erhalten.
		KostenDeckelProzent:     0,
		MindestErsparnisProzent: 7,

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
		{"parallelDateien", func(e Einstellungen) string { return strconv.Itoa(e.ParallelDateien) },
			"Wie viele Dateien gleichzeitig umgewandelt werden. 1 = eine nach der\n# anderen (ab Werk). Lohnt sich auf grossen Prozessoren: Ein einzelner Film\n# nutzt bei SVT-AV1 nur gut 6 Kerne, egal wie viele der Rechner hat.\n# Gemessen 01.10.2026 auf dem netcup-Server (8 Kerne), 1080p50, preset 9:\n# 2 gleichzeitig = 17 % mehr Durchsatz; jede einzelne Datei dauert dabei\n# fast doppelt so lange. Die Kerne werden nicht aufgeteilt - mit\n# aufgeteilten Threads (lp 3 je Datei) war es 6 % LANGSAMER als nacheinander.\n# Faustregel: etwa eine Datei je 4 Kerne des Rechners (8 Kerne: 2).\n# Dabei entfaellt das Vorab-Holen der naechsten Datei (die anderen rechnen\n# derweil), jede Datei braucht ihren eigenen Platz auf der Platte und\n# Arbeitsspeicher, und maxStundenProDatei gilt mal parallelDateien.\n# -analyse misst immer eine nach der anderen. Erlaubt: 1 bis 8."},
		{"bittiefe", func(e Einstellungen) string { return strconv.Itoa(e.Bittiefe) },
			"Bittiefe des Ergebnisses: 8 oder 10. 10 Bit beugt Streifen in\n# Farbverlaeufen (Banding) vor - auch bei 8-Bit-Quellen. Gemessen 25.09.2026\n# auf dem netcup-Server: 10 Bit kostet etwa 20 % Tempo, die Datei wird\n# nicht groesser. (Auf dem alten Contabo-VPS waren es noch 50 % Tempo.)"},
		{"maxAufloesung", func(e Einstellungen) string { return strconv.Itoa(e.MaxAufloesung) },
			"Hoechste Aufloesung des Ergebnisses (kurze Kante), wie maxResolution in\n# NVENCForge: Groesseres Material wird verkleinert, das Seitenverhaeltnis\n# bleibt, vergroessert wird nie. 1080 = hoechstens 1920 x 1080 (hochkant\n# 1080 x 1920). Ohne Nachschaerfen. Erlaubt: 0 (aus, Aufloesung wie die\n# Quelle), 720, 1080, 1440, 2160. Ab Werk 0.\n# Achtung: Mit originalBehandlung=loeschen ist die hoehere Aufloesung danach\n# weg. Was nur umgepackt wird, behaelt seine Aufloesung."},
		{"varianceBoost", func(e Einstellungen) string { return jaNein(e.VarianceBoost) },
			"Variance Boost (SVT-AV1): gibt ruhigen, glatten und dunklen Flaechen (Waende,\n# Haut, Schatten) mehr Bits - genau dort entstehen sonst Kloetzchen und\n# Streifen. Gemessen 26.09.2026 an einem 1080p50-Film: bei gleichem CRF\n# 26 % groesser und +0,56 VMAF; beim VMAF-Ziel waehlt Auto-CQ dafuer einen\n# hoeheren CRF, die Datei wird dann kaum groesser. ja oder nein, ab Werk ja\n# (seit 0.19.0, vorher nein). Mit welcher Einstellung eine Datei entstand,\n# steht im Protokoll."},
		{"varianceBoostStaerke", func(e Einstellungen) string { return strconv.Itoa(e.VarianceBoostStaerke) },
			"Staerke des Variance Boost (SVT-AV1: variance-boost-strength). Wirkt nur\n# mit varianceBoost=ja - ein- und ausgeschaltet wird nur dort. Laut SVT-Doku:\n#   1 = mild   - fuer Zeichentrick und sehr glatte, ruhige Bilder\n#   2 = sanft  - passt zu den meisten echten Filmen (EMPFOHLEN, ab Werk)\n#   3 = mittel - fuer Standbilder und Filme, in denen sich sehr kontrast-\n#                reiche und sehr kontrastarme Szenen abwechseln (Horror)\n#   4 = stark  - sehr aggressiv, nur fuer Sonderfaelle, in denen Details\n#                in ruhigen Flaechen ueber allem stehen\n# Hoeher = ruhige Flaechen bekommen mehr Bits. Mit Staerke 2 blieb die Datei\n# beim VMAF-Ziel etwa gleich gross (gemessen 26.09.2026), die Bits werden\n# also eher umverteilt. Ob es besser aussieht, zeigt nur das Auge.\n# Erlaubt: 1 bis 4."},
		{"varianceOktil", func(e Einstellungen) string { return strconv.Itoa(e.VarianceOktil) },
			"Wie waehlerisch der Variance Boost ist (SVT-AV1: variance-octile). Wirkt\n# nur mit varianceBoost=ja. SVT betrachtet jeden Bildblock in Achteln:\n# 1 = ein ruhiges Achtel genuegt, damit der Block mehr Bits bekommt,\n# 8 = der ganze Block muss ruhig sein. Kleiner = mehr Bloecke bekommen mehr,\n# auch unruhige - die Datei waechst. Groesser = sparsamer, aber einzelne\n# ruhige Stellen koennen schlechter aussehen als ihre Umgebung. Die SVT-Doku\n# empfiehlt 4 bis 7; 5 ist EMPFOHLEN und ab Werk (zugleich der Werkswert\n# von SVT-AV1). Erlaubt: 1 bis 8."},
		{"tune0", func(e Einstellungen) string { return jaNein(e.Tune0) },
			"tune 0 (SVT-AV1): stimmt den Encoder auf den Seheindruck ab statt auf die\n# Rechengenauigkeit PSNR (Standard von SVT-AV1). Gemessen 26.09.2026 an einem\n# 1080p50-Film: beim gleichen VMAF-Ziel etwa 3 % groesser. VMAF sieht den\n# Unterschied nicht, nur das Auge. ja oder nein, ab Werk ja (seit 0.20.0,\n# vorher nein)."},
		{"filmKorn", func(e Einstellungen) string { return strconv.Itoa(e.FilmKorn) },
			"Filmkorn (SVT-AV1 film-grain): Der Player legt beim Abspielen feines\n# kuenstliches Korn ueber das Bild. Das gibt koernigen Filmen ihren Look\n# zurueck und kann Kloetzchen und Streifen in dunklen, glatten Flaechen\n# ueberdecken. 0 = aus (ab Werk), 1 bis 50 = Staerke; fuer leicht koernige\n# Filme etwa 4 bis 8. Ob es gefaellt, zeigt nur das Auge.\n# ACHTUNG, SEHR LANGSAM: SVT-AV1 schaetzt dafuer das Rauschen jedes Bildes\n# ab. Gemessen 01.10.2026 auf dem netcup-Server an einem 1080p50-Film mit\n# preset 9: 280 statt 33 Sekunden je Minute Film - 8,5-mal so lange. SVT-AV1\n# raet selbst ab preset 7 davon ab.\n# Das Korn kommt nur in den fertigen Film, nie in die Messproben (sonst\n# wertet VMAF das Korn als Fehler und Auto-CQ waehlt viel zu teuer), und die\n# Quelle wird dafuer nicht entrauscht (film-grain-denoise=0): sonst passt\n# das Ergebnis nicht mehr zu den Messproben und wirkt wachsartig glatt. Die\n# Datei wird so nur rund 1 % groesser. Erlaubt: 0 bis 50."},
		{"vmafPerzentil", func(e Einstellungen) string { return strconv.Itoa(e.VMAFPerzentil) },
			"Woran Auto-CQ die Qualitaet misst. 5 (ab Werk) = am 5-%-Perzentil der\n# Bildwerte: 95 % der gemessenen Bilder sind mindestens so gut. Das faengt\n# einzelne schwache Szenen, die im Mittelwert untergehen wuerden - dann\n# gilt zielVMAFPerzentil UND zusaetzlich zielVMAF fuer den Mittelwert\n# (Sicherheitsnetz). 0 = am Mittelwert wie bis 0.18.0 - dann gilt\n# zielVMAF. Erlaubt: 0 bis 50.\n# Ehrlich: In sehr dunklen Szenen ist VMAF selbst wenig empfindlich. Gegen\n# Kloetzchen in Schatten hilft dort eher varianceBoost."},
		{"zielVMAFPerzentil", func(e Einstellungen) string { return zahl(e.ZielVMAFPerzentil) },
			"Qualitaetsziel fuer das Perzentil (gilt mit vmafPerzentil groesser 0) -\n# wie zielVMAF eine UNTERGRENZE. Das 5-%-Perzentil liegt immer unter dem\n# Mittelwert. Gemessen 01.10.2026 an 6 Filmen (preset 9, 10 Bit): Mit 92\n# werden die Dateien im Schnitt so gross wie mit zielVMAF=96 am Mittelwert;\n# Filme mit einzelnen schwachen Szenen bis 33 % groesser - genau dort sollen\n# die Bits hin. Gleichmaessige Filme werden kleiner, aber nie schlechter als\n# zielVMAF fuer den Mittelwert erlaubt (Sicherheitsnetz, siehe dort).\n# Faustregel: Mittelwert-Ziel minus 4 (95 -> 91, 96 -> 92, 96,5 -> 92,5\n# bis 93). Ab Werk 92."},
		{"zielVMAF", func(e Einstellungen) string { return zahl(e.ZielVMAF) },
			"Qualitaetsziel fuer den Mittelwert. Gilt IMMER: mit vmafPerzentil=0\n# allein, sonst als Sicherheitsnetz zusaetzlich zum Perzentil (seit 0.19.1).\n# Anlass: Bei einem sehr gleichmaessigen Film hielt das Perzentil sein Ziel\n# schon bei CRF 40, der Mittelwert lag dort aber bei 94,4 - schlechter, als\n# es zielVMAF=95 bis dahin erlaubt hatte. Mit dem Netz wurde es CRF 36\n# (Mittel 95,1). So wird keine Datei schlechter als nur mit zielVMAF.\n# Auto-CQ haelt es als UNTERGRENZE: das Ergebnis liegt nicht\n# darunter, solange das Material es ueberhaupt hergibt. Anhaltspunkte,\n# gemessen 25.09.2026 an einer 1080p-Szene mit preset 9 und 10 Bit\n# (Original 12,3 Mbit/s):\n#   VMAF 93   = CRF 33, 2,2 Mbit/s - sichtbar weicher, Kloetzchen, Streifen\n#   VMAF 96   = CRF 28, 3,2 Mbit/s\n#   VMAF 97,5 = CRF 24, 4,2 Mbit/s\n#   VMAF 98   = CRF 20, 5,2 Mbit/s - kaum vom Original zu unterscheiden\n# 1080p-Filme mit 50 Bildern/s erreichen 98 meist gar nicht (26.09.2026:\n# nur 3 von 23 Filmen) - dort greift plateauToleranz.\n# Wer das Ziel aendert, legt die Anker so, dass der bessere darueber landet."},
		{"ankerNiedrig", func(e Einstellungen) string { return strconv.Itoa(e.AnkerNiedrig) },
			"Die zwei CRF-Werte, die Auto-CQ zuerst misst, um die Kurve zu schaetzen.\n# Der niedrige sollte ueber dem Ziel landen, der hohe darunter: fuer\n# Ziel 94 bis 95 etwa 22 und 32 (ab Werk), fuer Ziel 96 bis 98 etwa 16\n# und 26. Am Perzentil mit zielVMAFPerzentil 92 (ab Werk) passen 22 und 32\n# ebenfalls (gemessen 01.10.2026). Der niedrige ist zugleich die beste\n# Qualitaet, die Auto-CQ je anbietet."},
		{"ankerHoch", func(e Einstellungen) string { return strconv.Itoa(e.AnkerHoch) }, ""},
		{"crfMin", func(e Einstellungen) string { return strconv.Itoa(e.CRFMin) },
			"Klemme: Auto-CQ verlaesst diesen Bereich nie."},
		{"crfMax", func(e Einstellungen) string { return strconv.Itoa(e.CRFMax) }, ""},
		{"kostenDeckelProzent", func(e Einstellungen) string { return zahl(e.KostenDeckelProzent) },
			"Kosten-Deckel wie bei NVENCForge: Das Bild darf hoechstens so viel Prozent\n# der Quelle kosten (gemessen an den Messstellen). Greift nur bei schon\n# stark komprimierten Quellen - dort sinkt die Qualitaet dann UNTER das Ziel.\n# Ab Werk 0 = kein Deckel (seit 0.11.2): Gedeckelte Vergleichsclips mit\n# VMAF 93 sahen am 27.09.2026 deutlich schlechter aus als das Original.\n# Ohne Deckel bleibt die Qualitaet beim Ziel; was dabei keine\n# mindestErsparnisProzent spart, wird nur verlustfrei umgepackt.\n# Wer ihn trotzdem will: 50 = hoechstens die Haelfte der Quelle. Ist der\n# Deckel gar nicht einzuhalten, bleibt die Qualitaetswahl stehen."},

		{"messfensterAnzahl", func(e Einstellungen) string { return strconv.Itoa(e.MessfensterAnzahl) },
			"Wie viele Stichproben Auto-CQ misst, gleichmaessig verteilt. 4 ab Werk\n# (seit 0.20.0, vorher 5). Mit 3 lag die Groessen-Vorhersage am 27.09.2026\n# bis 10 Prozentpunkte neben dem ganzen Film. Mehr Stichproben messen\n# genauer, aber laenger: 5 statt 3 kostete etwa zwei Drittel mehr Messzeit."},
		{"messfensterSek", func(e Einstellungen) string { return zahl(e.MessfensterSek) },
			"Laenge je Stichprobe in Sekunden, ab Werk 9 (seit 0.20.0, vorher 8).\n# NIEMALS unter 8 - kuerzere Fenster verschieben die CRF-Wahl um mehrere\n# Stufen (gemessen im NVENCForge-Projekt)."},
		{"plateauToleranz", func(e Einstellungen) string { return zahl(e.PlateauToleranz) },
			"Ist das Ziel nicht erreichbar, darf Auto-CQ hoechstens so viel VMAF unter\n# dem besten Wert (bei ankerNiedrig) bleiben, um Platz zu sparen. 0,5 wie in\n# NVENCForge (Entscheidung des Nutzers: das Bild ist wichtiger als die\n# letzten Prozent Platz)."},
		{"plateauMindestSparen", func(e Einstellungen) string { return zahl(e.PlateauMindestSpa) },
			"Der Aufstieg wird nur genommen, wenn er je CRF-Stufe im Mittel mindestens\n# so viel Prozent spart."},

		{"mindestErsparnisProzent", func(e Einstellungen) string { return zahl(e.MindestErsparnisProzent) },
			"Wird die Datei nicht um mindestens so viel Prozent kleiner, wird sie nicht\n# umgewandelt, sondern nur verlustfrei nach MKV umgepackt (Bild unveraendert,\n# wie NVENCForge) und als name.h264.mkv o. ae. in output gelegt; das Original\n# wandert wie sonst nach originals. Zweimal geprueft: gleich nach der\n# Qualitaetsmessung aus den Messproben hochgerechnet (spart die ganze\n# Rechenzeit) und nach dem Umwandeln an der echten Datei.\n# Ab Werk 7 (seit 0.20.0, vorher 10)."},

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
	case "parallelDateien":
		return ganzzahl(wert, 1, maxParallelDateien, &e.ParallelDateien)
	case "bittiefe":
		return bittiefeLesen(e, wert)
	case "maxAufloesung":
		return maxAufloesungLesen(e, wert)
	case "varianceBoost":
		e.VarianceBoost = istJa(wert)
	case "varianceBoostStaerke":
		return varianceReglerLesen(wert, 4, &e.VarianceBoostStaerke)
	case "varianceOktil":
		return varianceReglerLesen(wert, 8, &e.VarianceOktil)
	case "tune0":
		e.Tune0 = istJa(wert)
	case "filmKorn":
		return ganzzahl(wert, 0, maxFilmKorn, &e.FilmKorn)
	case "vmafPerzentil":
		return ganzzahl(wert, 0, maxVMAFPerzentil, &e.VMAFPerzentil)
	case "zielVMAFPerzentil":
		return kommazahl(wert, 1, 100, &e.ZielVMAFPerzentil)
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

// maxAufloesungLesen lässt nur die Stufen aus NVENCForge zu — ein Tippfehler
// wie 1800 soll auffallen, statt still eine krumme Grösse zu erzeugen.
func maxAufloesungLesen(e *Einstellungen, wert string) error {
	zahl, err := strconv.Atoi(wert)
	if err != nil || !erlaubteMaxAufloesungen[zahl] {
		return fmt.Errorf("unbekannter Wert %q (erlaubt: 0 = aus, 720, 1080, 1440, 2160)", wert)
	}
	e.MaxAufloesung = zahl
	return nil
}

// varianceReglerLesen liest Stärke und Oktil des Variance Boost (ab 1 bis
// max, wie SVT-AV1 sie annimmt). Eine 0 oder ein leerer Wert soll auffallen,
// statt still „aus" zu bedeuten: ein- und ausgeschaltet wird nur mit
// varianceBoost — der Hinweis sagt das gleich dazu.
func varianceReglerLesen(wert string, max int, ziel *int) error {
	if err := ganzzahl(wert, 1, max, ziel); err != nil {
		return fmt.Errorf("%w - ein- und ausgeschaltet wird mit varianceBoost=ja/nein", err)
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
