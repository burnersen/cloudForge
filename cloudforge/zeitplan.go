package main

// Der Zeitplan: CloudForge arbeitet alle 30 Minuten von selbst die
// Automatik-Ordner ab — ohne Fenster, als systemd-Benutzerdienst.
//
// Warum ein Benutzerdienst (und nicht cron oder ein Systemdienst): Er läuft
// in der Sitzung des Nutzers, ohne root. Genau dort läuft auch die pCloud-App,
// ohne die es in pCloudDrive ohnehin nichts zu tun gibt. Endet die Sitzung
// (Abmelden, Neustart), ruht der Zeitplan, bis sich der Nutzer wieder anmeldet.
//
// Zusammenspiel mit dem Draufziehen: Der Hintergrundlauf wartet nie auf die
// Sperre (SperreVersuchen) und macht nach jeder fertigen Datei Platz, sobald
// ein Fenster wartet (Vorfahrt, sperre_linux.go).

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	zeitplanName = "cloudforge-zeitplan"
	// Alle 30 Minuten zur vollen und halben Stunde (Nutzer-Entscheidung 25.09.2026).
	zeitplanTakt = "*:0/30"
)

// zeitplanDienst ist die Dienst-Datei. Pfade stehen in Anführungszeichen,
// damit auch Ordner mit Leerzeichen funktionieren.
func zeitplanDienst(programm, iniPfad string) string {
	return fmt.Sprintf(`# Von CloudForge angelegt (cloudforge -zeitplan an|aus).
[Unit]
Description=CloudForge: Automatik-Ordner abarbeiten

[Service]
Type=oneshot
ExecStart="%s" -automatik -hintergrund -ini "%s"
# Ein Lauf durch den ganzen Bestand dauert Wochen. Ohne diese Zeile bricht
# systemd einen oneshot-Dienst nach 90 Sekunden ab.
TimeoutStartSec=infinity
# Was CloudForge tut, steht im Protokoll (~/cloudforge/protokoll/),
# nur Fehler gehen zusaetzlich ins Journal.
StandardOutput=null
StandardError=journal
`, programm, iniPfad)
}

// zeitplanTimer löst den Dienst aus. Läuft er noch (tagelang, durch den
// Bestand), startet systemd keinen zweiten — der Takt verfällt einfach.
func zeitplanTimer() string {
	return `# Von CloudForge angelegt (cloudforge -zeitplan an|aus).
[Unit]
Description=CloudForge alle 30 Minuten

[Timer]
OnCalendar=` + zeitplanTakt + `
Persistent=false

[Install]
WantedBy=timers.target
`
}

// ZeitplanSchalten richtet den Zeitplan ein oder entfernt ihn.
func ZeitplanSchalten(an bool, iniPfad string, e Einstellungen) error {
	konfig, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("Konfigurationsordner nicht ermittelbar: %w", err)
	}
	ordner := filepath.Join(konfig, "systemd", "user")
	dienst := filepath.Join(ordner, zeitplanName+".service")
	timer := filepath.Join(ordner, zeitplanName+".timer")

	if !an {
		return zeitplanAus(dienst, timer)
	}

	if len(e.QuellOrdner) == 0 {
		return Hinweis("fuer den Zeitplan fehlt der Ordner.\n\n" +
			"  Trag ihn in der Einstellungsdatei bei quellOrdner= ein, oder starte\n" +
			"  einmal \"CloudForge: Automatik\" - dann wird danach gefragt.")
	}
	programm, err := os.Executable()
	if err != nil {
		return fmt.Errorf("eigener Programmpfad nicht ermittelbar: %w", err)
	}
	if programm, err = filepath.Abs(programm); err != nil {
		return err
	}
	iniAbsolut, err := filepath.Abs(iniPfad)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(ordner, 0o755); err != nil {
		return fmt.Errorf("%s nicht anlegbar: %w", ordner, err)
	}
	if err := os.WriteFile(dienst, []byte(zeitplanDienst(programm, iniAbsolut)), 0o644); err != nil {
		return fmt.Errorf("Dienst-Datei nicht schreibbar: %w", err)
	}
	if err := os.WriteFile(timer, []byte(zeitplanTimer()), 0o644); err != nil {
		return fmt.Errorf("Timer-Datei nicht schreibbar: %w", err)
	}
	if err := benutzerSystemctl("daemon-reload"); err != nil {
		return err
	}
	if err := benutzerSystemctl("enable", "--now", zeitplanName+".timer"); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Zeitplan ist AN: alle 30 Minuten wird abgearbeitet, was neu ist in")
	for _, q := range e.QuellOrdner {
		fmt.Printf("  %s\n", q)
	}
	fmt.Println()
	fmt.Println("Das laeuft ohne Fenster, solange du am Server angemeldet bist")
	fmt.Println("(Remote-Desktop schliessen ist in Ordnung, Abmelden nicht) und die")
	fmt.Println("pCloud-App laeuft. Zusehen: Symbol \"CloudForge: Protokoll ansehen\".")
	fmt.Println("Ausschalten:  cloudforge -zeitplan aus")
	return nil
}

// zeitplanAus hält einen laufenden Hintergrundlauf an (geordnet: die
// angefangene Datei wird verworfen, das Original bleibt) und entfernt alles.
func zeitplanAus(dienst, timer string) error {
	// Fehler hier sind unkritisch: war der Zeitplan nie an, gibt es nichts
	// abzuschalten.
	benutzerSystemctl("disable", "--now", zeitplanName+".timer")
	benutzerSystemctl("stop", zeitplanName+".service")
	os.Remove(dienst)
	os.Remove(timer)
	if err := benutzerSystemctl("daemon-reload"); err != nil {
		return err
	}
	fmt.Println()
	fmt.Println("Zeitplan ist AUS. Draufziehen und \"CloudForge: Automatik\" gehen weiter wie gewohnt.")
	return nil
}

// benutzerSystemctl ruft systemctl für den Benutzerdienst auf. Über SSH fehlen
// die Adressen der Sitzung — dann werden die üblichen Orte eingesetzt.
func benutzerSystemctl(argumente ...string) error {
	befehl := exec.Command("systemctl", append([]string{"--user"}, argumente...)...)
	umgebung := sitzungsUmgebung()
	if os.Getenv("XDG_RUNTIME_DIR") == "" {
		umgebung = append(umgebung, fmt.Sprintf("XDG_RUNTIME_DIR=/run/user/%d", os.Getuid()))
	}
	befehl.Env = umgebung
	if ausgabe, err := befehl.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl --user %s: %v (%s)", strings.Join(argumente, " "), err, strings.TrimSpace(string(ausgabe)))
	}
	return nil
}
