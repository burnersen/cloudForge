#!/usr/bin/env bash
#
# CloudForge einrichten
#
# Richtet CloudForge fuer den angemeldeten Benutzer ein - ohne sudo und ohne
# etwas am System zu veraendern. Alles landet in ~/cloudforge.
#
# Mehrfach ausfuehren schadet nicht: Einstellungen bleiben erhalten, ein
# schon geladenes ffmpeg wird wiederverwendet, das bisherige Programm wird
# als cloudforge.vorher gesichert.
#
# Aufruf:   bash installieren.sh

set -uo pipefail

ZIEL="$HOME/cloudforge"
WERKZEUGE="$ZIEL/tools"
QUELLE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ffmpeg kommt von BtbN - dieselbe Quelle, die NVENCForge unter Windows nutzt.
# Das ffmpeg aus den Paketquellen von Ubuntu taugt NICHT: ihm fehlt libvmaf,
# ohne das die Qualitaetsmessung nicht geht (geprueft auf Ubuntu 26.04).
BTBN_API="https://api.github.com/repos/BtbN/FFmpeg-Builds/releases/tags/latest"
BTBN_LADEN="https://github.com/BtbN/FFmpeg-Builds/releases/download/latest"
# Mit dieser Fassung wurden die Qualitaetswerte gemessen.
BEVORZUGTE_FASSUNG="8.1"

AUFRAEUMEN=""
trap '[ -n "$AUFRAEUMEN" ] && rm -rf "$AUFRAEUMEN"' EXIT

schritt() { printf '\n==> %s\n' "$1"; }
ok()      { printf '    OK: %s\n' "$1"; }
fehler() {
    printf '\nFEHLER: %s\n' "$1" >&2
    shift
    for zeile in "$@"; do printf '       %s\n' "$zeile" >&2; done
    printf '\nEs wurde nichts kaputt gemacht. Nach dem Beheben einfach noch einmal starten.\n' >&2
    exit 1
}

# ---------------------------------------------------------------------------

echo
echo "CloudForge wird eingerichtet"
echo "============================"

if [ "$(id -u)" -eq 0 ]; then
    fehler "Bitte NICHT mit sudo oder als root starten." \
           "CloudForge gehoert zum normalen Benutzer. Einfach so aufrufen:" \
           "  bash installieren.sh"
fi

[ "$(uname -s)" = "Linux" ] || fehler "CloudForge laeuft nur unter Linux."

case "$(uname -m)" in
    x86_64 | amd64)  PROGRAMM="cloudforge-x86_64";  BTBN_SYSTEM="linux64" ;;
    aarch64 | arm64) PROGRAMM="cloudforge-aarch64"; BTBN_SYSTEM="linuxarm64" ;;
    *) fehler "Dieser Rechnertyp ($(uname -m)) wird nicht unterstuetzt." \
              "Moeglich sind x86_64 (normale PCs und Server) und aarch64 (ARM)." ;;
esac

[ -f "$QUELLE/$PROGRAMM" ] || fehler "Die Datei $PROGRAMM fehlt neben diesem Skript." \
    "Bitte den GANZEN Ordner mit allen vier Dateien kopieren, nicht nur das Skript."

# ---------------------------------------------------------------------------

schritt "Pruefe die noetigen Werkzeuge"

if command -v curl >/dev/null 2>&1; then
    LADER="curl"
elif command -v wget >/dev/null 2>&1; then
    LADER="wget"
else
    fehler "Zum Herunterladen fehlt curl (und auch wget)." \
           "Einmal nachinstallieren mit:" "  sudo apt install curl"
fi
command -v tar >/dev/null 2>&1 || fehler "tar fehlt." "  sudo apt install tar"
command -v xz  >/dev/null 2>&1 || fehler "xz fehlt (zum Entpacken von ffmpeg)." "  sudo apt install xz-utils"
ok "$LADER, tar und xz sind da"

# ---------------------------------------------------------------------------

schritt "Kopiere CloudForge nach $ZIEL"

mkdir -p "$ZIEL" "$WERKZEUGE" "$ZIEL/arbeit" \
    || fehler "Der Ordner $ZIEL laesst sich nicht anlegen."

if [ -f "$ZIEL/cloudforge" ]; then
    cp -p "$ZIEL/cloudforge" "$ZIEL/cloudforge.vorher" \
        || fehler "Die bisherige Fassung liess sich nicht sichern."
    ok "bisherige Fassung gesichert als cloudforge.vorher"
fi

# Erst daneben ablegen, dann umbenennen: so klappt es auch, wenn CloudForge
# gerade laeuft (eine laufende Programmdatei laesst sich nicht ueberschreiben).
cp "$QUELLE/$PROGRAMM" "$ZIEL/cloudforge.neu" \
    && chmod 755 "$ZIEL/cloudforge.neu" \
    && mv -f "$ZIEL/cloudforge.neu" "$ZIEL/cloudforge" \
    || fehler "Das Programm liess sich nicht nach $ZIEL kopieren."

"$ZIEL/cloudforge" -hilfe >/dev/null 2>&1 \
    || fehler "Das Programm startet auf diesem Rechner nicht."
ok "$("$ZIEL/cloudforge" -hilfe | head -1)"

# ---------------------------------------------------------------------------

ffmpeg_taugt() {
    local programm="$1" filter encoder
    [ -x "$programm" ] || return 1
    filter=$("$programm" -hide_banner -filters 2>/dev/null) || return 1
    encoder=$("$programm" -hide_banner -encoders 2>/dev/null) || return 1
    grep -q libvmaf <<<"$filter" \
        && grep -q libsvtav1 <<<"$encoder"
}

text_laden() {
    if [ "$LADER" = "curl" ]; then curl -fsSL "$1"; else wget -qO- "$1"; fi
}

datei_laden() {
    if [ "$LADER" = "curl" ]; then
        curl -fL --progress-bar -o "$2" "$1"
    else
        wget -q --show-progress -O "$2" "$1"
    fi
}

# Sucht die passende ffmpeg-Fassung: bevorzugt 8.1, sonst die neueste
# stabile. Nie die "master"-Fassung - das ist ein Entwicklungsstand, der
# schon einmal eine Option entfernt hat, auf die sich das Programm verliess.
ffmpeg_adresse() {
    local bevorzugt namen neueste
    bevorzugt="ffmpeg-n${BEVORZUGTE_FASSUNG}-latest-${BTBN_SYSTEM}-gpl-${BEVORZUGTE_FASSUNG}.tar.xz"
    namen=$(text_laden "$BTBN_API" 2>/dev/null \
        | grep -o "ffmpeg-n[0-9][0-9.]*-latest-${BTBN_SYSTEM}-gpl-[0-9.]*\.tar\.xz" \
        | sort -u) || true

    if grep -qxF "$bevorzugt" <<<"$namen"; then
        echo "$BTBN_LADEN/$bevorzugt"
        return
    fi
    neueste=$(sort -V <<<"$namen" | tail -1)
    if [ -n "$neueste" ]; then
        echo "$BTBN_LADEN/$neueste"
        return
    fi
    # GitHub hat nicht geantwortet (etwa zu viele Anfragen) - dann den
    # bekannten Namen direkt versuchen.
    echo "$BTBN_LADEN/$bevorzugt"
}

schritt "Pruefe ffmpeg"

if ffmpeg_taugt "$WERKZEUGE/ffmpeg" && [ -x "$WERKZEUGE/ffprobe" ]; then
    ok "schon da und tauglich: $("$WERKZEUGE/ffmpeg" -hide_banner -version | head -1 | cut -d' ' -f1-3)"
else
    ADRESSE=$(ffmpeg_adresse)
    echo "    Lade $(basename "$ADRESSE")"
    echo "    (rund 140 MB - je nach Leitung einige Sekunden bis Minuten)"

    AUFRAEUMEN=$(mktemp -d "$ZIEL/.installation-XXXXXX") \
        || fehler "Kein Platz fuer die Zwischendateien in $ZIEL."

    datei_laden "$ADRESSE" "$AUFRAEUMEN/ffmpeg.tar.xz" \
        || fehler "Herunterladen fehlgeschlagen." \
                  "Adresse: $ADRESSE" \
                  "Bitte die Internetverbindung pruefen."
    tar -xJf "$AUFRAEUMEN/ffmpeg.tar.xz" -C "$AUFRAEUMEN" \
        || fehler "Entpacken fehlgeschlagen - die Datei ist vermutlich unvollstaendig."

    BINORDNER=$(dirname "$(find "$AUFRAEUMEN" -type f -name ffmpeg -path '*/bin/*' | head -1)")
    [ -x "$BINORDNER/ffmpeg" ] && [ -x "$BINORDNER/ffprobe" ] \
        || fehler "Im heruntergeladenen Paket fehlen ffmpeg oder ffprobe."
    ffmpeg_taugt "$BINORDNER/ffmpeg" \
        || fehler "Das geladene ffmpeg kann nicht alles, was CloudForge braucht" \
                  "(libvmaf, libsvtav1)."

    for werkzeug in ffmpeg ffprobe; do
        cp "$BINORDNER/$werkzeug" "$WERKZEUGE/$werkzeug.neu" \
            && chmod 755 "$WERKZEUGE/$werkzeug.neu" \
            && mv -f "$WERKZEUGE/$werkzeug.neu" "$WERKZEUGE/$werkzeug" \
            || fehler "$werkzeug liess sich nicht nach $WERKZEUGE kopieren."
    done
    ok "$("$WERKZEUGE/ffmpeg" -hide_banner -version | head -1 | cut -d' ' -f1-3) mit libvmaf und SVT-AV1"
fi

# ---------------------------------------------------------------------------

schritt "Lege Einstellungen und Schreibtisch-Symbole an"

"$ZIEL/cloudforge" -starter || fehler "Die Symbole liessen sich nicht anlegen."

# Sucht den Schreibtisch genauso wie das Programm selbst: erst das System
# fragen (der Ordner heisst je nach Sprache anders), sonst die ueblichen Namen.
schreibtisch_finden() {
    local pfad name
    pfad=$(xdg-user-dir DESKTOP 2>/dev/null || true)
    if [ -n "$pfad" ] && [ "$pfad" != "$HOME" ] && [ -d "$pfad" ]; then
        echo "$pfad"
        return
    fi
    for name in Desktop Schreibtisch Arbeitsfläche; do
        if [ -d "$HOME/$name" ]; then
            echo "$HOME/$name"
            return
        fi
    done
}

cp "$QUELLE/ANLEITUNG.txt" "$ZIEL/ANLEITUNG.txt" 2>/dev/null
SCHREIBTISCH=$(schreibtisch_finden)
if [ -n "$SCHREIBTISCH" ]; then
    cp "$QUELLE/ANLEITUNG.txt" "$SCHREIBTISCH/CloudForge-Anleitung.txt" 2>/dev/null \
        && ok "Anleitung liegt auf dem Schreibtisch"
fi

# Damit im Terminal einfach "cloudforge" reicht. Ubuntu nimmt ~/.local/bin
# ab dem naechsten Anmelden von selbst in den Suchpfad auf.
mkdir -p "$HOME/.local/bin" \
    && ln -sf "$ZIEL/cloudforge" "$HOME/.local/bin/cloudforge" \
    && ok "im Terminal aufrufbar als: cloudforge (ab dem naechsten Anmelden)"

# ---------------------------------------------------------------------------

schritt "Abschlusspruefung"

"$ZIEL/cloudforge" -bericht "$ZIEL/arbeit" >/dev/null 2>&1 \
    || fehler "Die Abschlusspruefung ist gescheitert." \
              "Zum Nachsehen:  $ZIEL/cloudforge -bericht $ZIEL/arbeit"
ok "CloudForge findet ffmpeg und seine Einstellungen"

echo
echo "================================================================"
echo " FERTIG - CloudForge ist eingerichtet."
echo "================================================================"
echo
if [ -n "$SCHREIBTISCH" ]; then
    echo " So geht es: Videodateien oder ganze Ordner mit der Maus auf das"
    echo " Schreibtisch-Symbol \"CloudForge: Dateien umwandeln\" ziehen."
    echo
    echo " Die Anleitung liegt auf dem Schreibtisch: CloudForge-Anleitung.txt"
else
    echo " Kein Schreibtisch gefunden - im Terminal benutzen:"
    echo "   $ZIEL/cloudforge DATEI_ODER_ORDNER"
    echo
    echo " Die Anleitung: $ZIEL/ANLEITUNG.txt"
fi
echo
echo " Alle Moeglichkeiten:  $ZIEL/cloudforge -hilfe"
echo " Einstellungen:        $ZIEL/cloudforge.ini"
echo
