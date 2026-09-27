<div align="center">

# ☁️ CloudForge

### Videos aus der Cloud platzsparend nach AV1 umwandeln – auf einem Linux-Server, ganz ohne Grafikkarte.

**Videos auf ein Schreibtisch-Symbol ziehen – CloudForge misst die Qualität, wandelt um, prüft das Ergebnis und legt es ordentlich ab.**
Das Original wird erst angefasst, wenn das Ergebnis fertig, geprüft und sicher in der Cloud liegt.

*⚙️ Das Linux-Gegenstück zu [NVENCForge](https://github.com/burnersen/NVENCForge): gleiche Idee, gleiche Ordner-Logik, gleiche Art von Einstellungsdatei – nur rechnet hier der Prozessor mit SVT-AV1 statt der Grafikkarte mit NVENC.*

[![CI](https://github.com/burnersen/cloudForge/actions/workflows/ci.yml/badge.svg)](https://github.com/burnersen/cloudForge/actions/workflows/ci.yml)
[![Linux x86_64 | ARM64](https://img.shields.io/badge/Linux-x86__64%20%7C%20ARM64-FCC624?logo=linux&logoColor=black)](#-voraussetzungen)
[![AV1 mit SVT-AV1](https://img.shields.io/badge/AV1-SVT--AV1-6E4C9E)](#-so-arbeitet-cloudforge)
[![Erprobt mit pCloud Drive](https://img.shields.io/badge/Cloud-pCloud%20Drive-17BED0)](#-voraussetzungen)
[![Geschrieben in Go](https://img.shields.io/badge/Geschrieben%20in-Go-00ADD8?logo=go)](#-selbst-bauen)
[![Lizenz](https://img.shields.io/badge/Lizenz-PolyForm%20Noncommercial-blue)](#-lizenz)
[![Ko-fi](https://img.shields.io/badge/Ko--fi-Unterst%C3%BCtzen-FF5E5B?logo=kofi&logoColor=white)](https://ko-fi.com/burnersen)

**[⬇️ Neueste Fassung herunterladen](https://github.com/burnersen/cloudForge/releases/latest)** · **[📖 Ausführliche Anleitung](CloudForge-Installer/ANLEITUNG.txt)** · **[☕ Kaffee spendieren](https://ko-fi.com/burnersen)**

*Frei für private und nicht-kommerzielle Nutzung – [Quelltext offen einsehbar](#-lizenz), nie zum Weiterverkauf.*

</div>

---

**📑 Inhalt**

- [Was CloudForge macht](#-was-cloudforge-macht)
- [So arbeitet CloudForge](#-so-arbeitet-cloudforge)
- [Voraussetzungen](#-voraussetzungen)
- [Einrichten](#-einrichten)
- [Benutzen](#-benutzen)
- [Einstellungen](#-einstellungen)
- [Sicherheit für deine Originale](#-sicherheit-für-deine-originale)
- [Selbst bauen](#-selbst-bauen)
- [Aufbau des Quelltexts](#-aufbau-des-quelltexts)
- [Lizenz](#-lizenz)

---

## ✨ Was CloudForge macht

CloudForge ist für große Video-Sammlungen in einem Cloud-Speicher gedacht, der auf einem Linux-Server als Ordner eingebunden ist (gebaut und erprobt mit **pCloud Drive**). Es wandelt die Videos in das platzsparende Format **AV1** um und misst dabei für jede Datei selbst nach, wie stark es komprimieren darf, damit das Bild gut bleibt. Das Ergebnis ist meistens nur noch halb so groß oder kleiner.

Bedient wird es ohne Terminal: Dateien oder ganze Ordner mit der Maus auf ein Schreibtisch-Symbol ziehen, fertig.

- **Auto-CQ mit VMAF** – an mehreren Stellen jedes Films wird probeweise umgewandelt und mit dem Original verglichen. Gewählt wird die kleinste Datei, die das Qualitätsziel noch hält. Videos unter 1080p werden dafür auf 1080p vergrößert gemessen – so, wie sie im Vollbild aussehen.
- **Lohnt sich nicht? Dann nur umpacken** – ist eine Datei schon AV1, kleiner als 720p, schon sehr schlank oder würde sie kaum kleiner, wird sie verlustfrei nach MKV umgepackt. Das Bild bleibt bitgleich.
- **Prüfkette** – Größe, Spieldauer, Ton- und Untertitelspuren werden mit dem Original verglichen; auf Wunsch wird jedes Ergebnis komplett durchgelesen.
- **Abbrechen ist ungefährlich** – Fenster zu oder Strg+C: Halbfertiges verschwindet, das Original bleibt unberührt, Fertiges bleibt fertig.
- **Nächste Datei vorab holen** – während ein Film umgewandelt wird, kommt der nächste schon aus der Cloud (spart etwa 3 Minuten je Film).
- **Übersicht wie bei NVENCForge** – Fortschritt, Tempo, Restzeit und erwartete Größe; die Zeitschätzung lernt aus den letzten Filmen.
- **Automatik und Zeitplan** – einen festen Ordner abarbeiten, auf Wunsch alle 30 Minuten von selbst, ganz ohne Fenster.
- **Bilanz und Protokoll** – was dieser Lauf und alle Läufe zusammen gespart haben; ein Protokoll je Tag.
- **Nimmt Rücksicht** – ffmpeg rechnet mit niedrigster Priorität, der Desktop bleibt bedienbar.

## 🔄 So arbeitet CloudForge

Jede Datei durchläuft fünf Schritte:

| Schritt | Was passiert |
|---|---|
| **1. Datei holen** | Kopie aus dem Cloud-Ordner auf die lokale Platte. Auf dem Cloud-Ordner selbst wird nie gerechnet. Kommt dabei 10 Minuten lang kein einziges Byte an (Cloud hängt), gibt CloudForge diese Datei auf und macht mit der nächsten weiter, statt ewig zu warten. |
| **2. Qualität messen** | Kurze Stücke an 5 Stellen des Films werden probeweise umgewandelt und per **VMAF** (ein Maß für die Bildqualität, 100 = wie das Original) verglichen. Gewählt wird der höchste **CRF** (Kompressionsstufe), der das Ziel mindestens hält – ab Werk VMAF 96. Danach steht auch fest, wie groß das Ergebnis etwa wird; spart es zu wenig, wird die Datei nur umgepackt. Liegt die Vorhersage knapp an der Schwelle, prüft eine Größenprobe an 10 Stellen über den ganzen Film nach, bevor umgewandelt wird. |
| **3. Umwandeln** | SVT-AV1, ab Werk preset 9 und 10 Bit (gegen Streifen in Farbverläufen). Ton, Untertitel, Kapitel und Anhänge (etwa Schriften) werden 1:1 übernommen. Einzige Ausnahme: MP4-Textuntertitel kann MKV nicht aufnehmen, sie kommen als SRT an (gleicher Text, gleiche Zeiten). Ein eingebettetes Vorschaubild entfällt. |
| **4. Prüfen** | Das Ergebnis wird gegen das Original geprüft: Größe, Spieldauer, alle Spuren. |
| **5. Ergebnis ablegen** | Hochladen unter einem Zwischennamen – unter seinem richtigen Namen erscheint das Ergebnis erst, wenn es vollständig da ist. Erst dann wandert das Original nach `originals/`. |

Danach sieht der Ordner so aus:

```text
Daten/
├── output/
│   └── Film.av1.mkv      das neue, kleine Video
└── originals/
    └── Film.mp4          dein Original, unverändert
```

Nur umgepackte Dateien heißen nach ihrem alten Format, zum Beispiel `Film.h264.mkv`.

## 📋 Voraussetzungen

- **Linux** auf x86_64 (normale PCs und Server) oder aarch64 (ARM). Die ARM-Fassung wird mitgebaut, ist aber noch nicht auf echter ARM-Hardware erprobt.
- Ein Cloud-Speicher, der als Ordner eingebunden ist – erprobt mit **pCloud Drive**. Ein ganz normaler Ordner funktioniert genauso.
- Für die Schreibtisch-Symbole ein Desktop wie **XFCE** (dort erprobt). Ohne Desktop geht alles auch im Terminal.
- `curl` oder `wget`, `tar` und `xz` für die Einrichtung. **Kein sudo, keine Paketinstallation:** ffmpeg (mit libvmaf und SVT-AV1) lädt das Einrichtungs-Skript selbst von [BtbN/FFmpeg-Builds](https://github.com/BtbN/FFmpeg-Builds). Das ffmpeg aus den Ubuntu-Paketquellen taugt nicht, ihm fehlt libvmaf.
- **Geduld:** Ohne Grafikkarte ist AV1 echte Arbeit. Auf einem Server mit 8 Kernen (AMD EPYC) braucht eine Stunde Film etwa 35 Minuten (30 Bilder/s) bis knapp eine Stunde (50 Bilder/s).
- Programm, Einstellungsdatei und Anleitung sind **deutsch**.

## 📦 Einrichten

Im Terminal – als normaler Benutzer, **nicht** mit sudo:

```bash
curl -LO https://github.com/burnersen/cloudForge/releases/latest/download/CloudForge-Installer.tar.gz
tar xzf CloudForge-Installer.tar.gz
bash CloudForge-Installer/installieren.sh
```

Ohne curl geht die erste Zeile genauso mit `wget` statt `curl -LO`.

Das Paket enthält das Einrichtungs-Skript, die Anleitung und das Programm für beide Rechnertypen; das Skript nimmt das passende. Es

- kopiert CloudForge nach `~/cloudforge/` (eine vorhandene Fassung bleibt als `cloudforge.vorher` erhalten),
- lädt ffmpeg und ffprobe nach `~/cloudforge/tools/` (rund 140 MB, bevorzugt Fassung 8.1, mit der die Qualitätswerte gemessen wurden),
- legt die Einstellungsdatei `~/cloudforge/cloudforge.ini` und drei Schreibtisch-Symbole an (gleich als vertrauenswürdig markiert),
- legt die Anleitung auf den Schreibtisch und macht `cloudforge` im Terminal aufrufbar.

Mehrmals ausführen schadet nicht, die Einstellungen bleiben erhalten. **Aktualisieren** geht genauso. Was sich in welcher Fassung geändert hat, steht bei den [Releases](https://github.com/burnersen/cloudForge/releases).

## 🚀 Benutzen

| Schreibtisch-Symbol | Wozu |
|---|---|
| **CloudForge: Dateien umwandeln** | Videos oder ganze Ordner mit der Maus daraufziehen. Ein Fenster zeigt die Übersicht; am Ende steht, wo jede Datei liegt, und die Bilanz. |
| **CloudForge: Automatik** | Arbeitet immer denselben Ordner ab. Beim ersten Mal fragt es, welcher Ordner das sein soll. |
| **CloudForge: Protokoll ansehen** | Zeigt, was gerade passiert – auch wenn der Zeitplan ohne Fenster arbeitet. |

Alles geht auch im Terminal:

| Aufruf | Wirkung |
|---|---|
| `cloudforge DATEI_ODER_ORDNER` | umwandeln |
| `cloudforge -automatik` | den eingestellten Ordner abarbeiten |
| `cloudforge -bericht ORDNER` | nur anzeigen, was zu tun wäre und wie viel Platz es bringt |
| `cloudforge -analyse DATEI` | nur messen, welche Qualitätsstufe gewählt würde |
| `cloudforge -zeitplan an` | alle 30 Minuten von selbst abarbeiten (`aus` schaltet es ab) |
| `cloudforge -protokoll` | das Protokoll laufend ansehen |
| `cloudforge -starter` | die Schreibtisch-Symbole neu anlegen |
| `cloudforge -hilfe` | alle Möglichkeiten |

Immer nur ein CloudForge rechnet gleichzeitig. Zieht man während eines Laufs weitere Dateien auf das Symbol, wartet das zweite Fenster und macht danach von selbst weiter.

Die [Anleitung](CloudForge-Installer/ANLEITUNG.txt) erklärt alles Schritt für Schritt – auch, was bei Meldungen wie „übersprungen" oder „zu wenig Platz" zu tun ist.

## 🔧 Einstellungen

Die Einstellungen stehen in `~/cloudforge/cloudforge.ini`; jeder Eintrag ist dort erklärt. Die wichtigsten:

| Eintrag | Ab Werk | Bedeutung |
|---|---|---|
| `zielVMAF` | `96` | Qualitätsziel, gilt als Untergrenze. 93 = sichtbar weicher, 98 = kaum vom Original zu unterscheiden. |
| `originalBehandlung` | `verschieben` | `verschieben` (nach `originals/`), `loeschen` oder `behalten` |
| `mindestErsparnisProzent` | `15` | Wird eine Datei nicht mindestens so viel kleiner, wird sie nur verlustfrei umgepackt. |
| `kostenDeckelProzent` | `0` (aus) | Deckel wie bei NVENCForge: das neue Bild darf höchstens so viel Prozent des alten kosten – notfalls unter dem Qualitätsziel. |
| `messfensterAnzahl` | `5` | An so vielen Stellen wird die Qualität gemessen. |
| `preset` | `9` | SVT-AV1-Preset: höher = schneller, aber größer bei gleicher Qualität. |
| `bittiefe` | `10` | 10 Bit beugt Streifen in Farbverläufen vor, 8 ist etwas schneller. |
| `kerne` | `6` | Wie stark SVT-AV1 parallel rechnet; 6 ist das Maximum. |
| `varianceBoost`, `tune0` | `nein` | Zwei SVT-AV1-Schalter zum Ausprobieren – ob es besser aussieht, zeigt nur das Auge. |
| `vollpruefung` | `nein` | Jedes Ergebnis vor dem Ablegen komplett durchlesen (sicherer, aber langsam). |
| `quellOrdner` | leer | Der Ordner für die Automatik. |

Beim Start bringt CloudForge die Datei in Form: Reihenfolge und Erklärungen wie ab Werk, deine Werte bleiben. Weicht sie davon ab, liegt die vorherige Fassung danach als `cloudforge.ini.bak` daneben – mit eigenen Kommentaren und unbekannten Zeilen, die in der aufgeräumten Datei fehlen.

## 🔒 Sicherheit für deine Originale

- **Auf dem Cloud-Ordner wird nie gerechnet:** erst herunterkopieren, dann arbeiten, dann hochkopieren.
- **Ein abgebrochener Upload sieht nie wie ein fertiges Ergebnis aus:** Dateien erscheinen erst vollständig unter ihrem richtigen Namen.
- **Das Original wird erst bewegt, wenn das Ergebnis die Prüfkette bestanden hat** – und ab Werk nur nach `originals/` verschoben, nicht gelöscht.
- **Nichts wird doppelt gemacht:** Was schon ein Ergebnis in `output/` hat, wird übersprungen. Die Ordner `output/` und `originals/` sowie Dateien auf `.av1.mkv`, `.h264.mkv`, `.h265.mkv` und `.remux.mkv` fasst CloudForge nie an.
- **Platz in der Cloud wird erst frei, wenn du die `originals/`-Ordner löschst.** Schau dir vorher ein paar Ergebnisse an.

## 🔨 Selbst bauen

Gebraucht wird nur [Go](https://go.dev/dl/) in einer aktuellen Fassung (mindestens 1.22). CloudForge nutzt ausschließlich die Standardbibliothek – keine fremden Pakete.

```bash
cd cloudforge
gofmt -l .      # darf nichts ausgeben
go vet ./...
go test ./...
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o ../CloudForge-Installer/cloudforge-x86_64 .
GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o ../CloudForge-Installer/cloudforge-aarch64 .
```

Unter Windows in PowerShell sehen die beiden Bau-Befehle so aus:

```powershell
$env:GOOS = "linux"; $env:GOARCH = "amd64"; go build -trimpath -ldflags="-s -w" -o ../CloudForge-Installer/cloudforge-x86_64 .
$env:GOARCH = "arm64"; go build -trimpath -ldflags="-s -w" -o ../CloudForge-Installer/cloudforge-aarch64 .
Remove-Item Env:GOOS, Env:GOARCH   # sonst baut und testet dieses Fenster weiter für Linux
```

Danach ist der Ordner `CloudForge-Installer/` ein vollständiges Einrichtungspaket.

Die Tests mit echtem ffmpeg laufen nur, wenn `CLOUDFORGE_TEST_FFMPEG` und `CLOUDFORGE_TEST_FFPROBE` auf ein passendes ffmpeg und ffprobe zeigen (mit libvmaf und SVT-AV1); sonst werden sie übersprungen.

**Neue Fassung veröffentlichen:** `appVersion` in `cloudforge/main.go` erhöhen, committen und ein annotiertes Tag `vX.Y.Z` hochladen. Der Release-Workflow prüft, ob Tag und `appVersion` zusammenpassen, baut beide Programme, schnürt das Paket und veröffentlicht es. Die Nachricht des Tags wird zum Release-Text.

## 📁 Aufbau des Quelltexts

| Datei | Aufgabe |
|---|---|
| `main.go` | Aufrufe, Ablauf eines Laufs, Hilfe |
| `verarbeiten.go` | Der Weg einer Datei von der Cloud bis zurück in die Cloud – die fünf Schritte |
| `autocq.go`, `deckel.go` | Auto-CQ (CRF-Suche nach VMAF-Ziel) und Kosten-Deckel |
| `encoder.go` | Alle ffmpeg-Aufrufe an einer Stelle |
| `verify.go` | Die Prüfkette |
| `cloud.go`, `vorab.go` | Dateien zwischen Cloud und lokaler Platte bewegen, nächste Datei vorab holen |
| `scan.go`, `streams.go` | Dateisuche, Vorauswahl, Auswertung der Kopfdaten per ffprobe |
| `anzeige.go`, `bildschirm*.go`, `fortschritt.go` | Fortschritt und Übersicht im Terminal |
| `bilanz.go`, `queue.go`, `protokoll.go` | Bilanz, Gedächtnis zwischen den Läufen, Tagesprotokoll |
| `config.go` | Die Einstellungsdatei mit ihren Erklärungen |
| `bedienung.go`, `starter.go` | Bedienung ohne Technikkenntnisse, Schreibtisch-Symbole |
| `sperre_*.go`, `zeitplan.go`, `platz_*.go` | Nur ein Lauf gleichzeitig, Zeitplan (systemd-Benutzerdienst), freier Platz |
| `CloudForge-Installer/` | Einrichtungs-Skript und Anleitung |

Die Kommentare im Quelltext sind deutsch und erklären vor allem das *Warum* – oft mit den Messungen, auf denen eine Entscheidung beruht.

## 📜 Lizenz

CloudForge ist quelloffen einsehbar unter der [PolyForm Noncommercial License 1.0.0](LICENSE.md).
Frei zum Benutzen, Studieren, Verändern und Weitergeben für jeden **nicht-kommerziellen** Zweck: privat, Hobby, Bildung, Forschung. **Kommerzielle Nutzung, Weiterverkauf oder das Einbauen in bezahlte Produkte sind nicht erlaubt** – außer mit einer eigenen Lizenz vom Autor. Interesse an einer kommerziellen Lizenz? Einfach ein Issue eröffnen.

**Worauf CloudForge aufbaut:** Umgewandelt wird mit [FFmpeg](https://ffmpeg.org/) (GPL) samt SVT-AV1 und libvmaf. FFmpeg ist nicht Teil dieses Repos – das Einrichtungs-Skript lädt die fertigen Builds von [BtbN/FFmpeg-Builds](https://github.com/BtbN/FFmpeg-Builds). Diese stehen unter ihren eigenen Lizenzen; die Lizenz oben gilt für den Code in diesem Repository.

---

## 💬 Rückmeldung

Fehler gefunden oder eine Idee? [Issue eröffnen](https://github.com/burnersen/cloudForge/issues).
Und wenn dir CloudForge Platz und Zeit spart: [☕ Kaffee spendieren auf Ko-fi](https://ko-fi.com/burnersen).
