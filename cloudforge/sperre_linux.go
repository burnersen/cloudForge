//go:build linux

package main

// Sorgt dafür, dass immer nur ein CloudForge gleichzeitig rechnet.
//
// Wer zweimal Dateien auf das Symbol zieht, bekommt zwei Fenster. Ohne Sperre
// würden beide gleichzeitig mit je sechs Kernen rechnen, sich gegenseitig
// ausbremsen und dieselbe Zustandsdatei überschreiben. Mit Sperre wartet das
// zweite Fenster, bis das erste fertig ist, und macht dann weiter.
//
// flock wird vom Betriebssystem freigegeben, sobald das Programm endet — auch
// bei einem Absturz. Eine hängengebliebene Sperre kann es deshalb nicht geben.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// SperreVersuchen holt die Sperre, ohne zu warten — für den Hintergrundlauf
// des Zeitplans: Arbeitet schon ein CloudForge, gibt es für ihn nichts zu tun,
// der nächste Takt versucht es wieder. frei = false heisst: belegt.
func SperreVersuchen(pfad string) (freigeben func(), frei bool, err error) {
	datei, err := os.OpenFile(pfad, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, fmt.Errorf("Sperrdatei %s nicht nutzbar: %w", pfad, err)
	}
	griff := int(datei.Fd())

	err = syscall.Flock(griff, syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return func() {
			syscall.Flock(griff, syscall.LOCK_UN)
			datei.Close()
		}, true, nil
	}
	datei.Close()
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return nil, false, nil
	}
	return nil, false, fmt.Errorf("Sperre nicht setzbar: %w", err)
}

// Vorfahrt: Ein Fenster, das auf die Sperre wartet, hinterlegt seine
// Prozessnummer in einer kleinen Datei. Ein Hintergrundlauf schaut nach jeder
// fertigen Datei nach und macht Platz — sonst wartete ein hineingezogener Film,
// bis der Zeitplan den ganzen Bestand durch hat, also Wochen.

// VorfahrtAnmelden meldet dieses Fenster als wartend an. abmelden entfernt
// die Anmeldung wieder — aber nur, solange es noch die eigene ist: ein
// zweites wartendes Fenster kann sie inzwischen übernommen haben.
func VorfahrtAnmelden(pfad string) (abmelden func()) {
	eigene := strconv.Itoa(os.Getpid())
	if err := os.WriteFile(pfad, []byte(eigene), 0o644); err != nil {
		return func() {}
	}
	return func() {
		if inhalt, err := os.ReadFile(pfad); err == nil && strings.TrimSpace(string(inhalt)) == eigene {
			os.Remove(pfad)
		}
	}
}

// VorfahrtGewuenscht sagt, ob ein Fenster wartet. Eine Anmeldung, deren
// Prozess nicht mehr lebt (Fenster hart beendet), wird entfernt — sonst würde
// jeder künftige Hintergrundlauf nach einer einzigen Datei aufhören.
func VorfahrtGewuenscht(pfad string) bool {
	inhalt, err := os.ReadFile(pfad)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(inhalt)))
	if err != nil || pid <= 0 {
		os.Remove(pfad)
		return false
	}
	// Signal 0 prüft nur, ob es den Prozess gibt. EPERM heisst: es gibt ihn,
	// er gehört nur einem anderen Benutzer.
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		os.Remove(pfad)
		return false
	}
	return true
}

// Wie oft ein wartendes Fenster nachfragt. Absichtlich kein blockierendes
// Warten: das liesse sich nicht abbrechen, wenn jemand das Fenster schliesst,
// und es bliebe ein unsichtbarer Prozess zurück.
const sperreNachfragenAlle = 2 * time.Second

// SperreHolen wartet, bis kein anderes CloudForge mehr rechnet. beimWarten
// wird einmal aufgerufen, sobald klar ist, dass gewartet werden muss. Die
// zurückgegebene Funktion gibt die Sperre wieder frei.
func SperreHolen(ctx context.Context, pfad string, beimWarten func()) (func(), error) {
	datei, err := os.OpenFile(pfad, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("Sperrdatei %s nicht nutzbar: %w", pfad, err)
	}
	griff := int(datei.Fd())

	gewartet := false
	for {
		err := syscall.Flock(griff, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				syscall.Flock(griff, syscall.LOCK_UN)
				datei.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			datei.Close()
			return nil, fmt.Errorf("Sperre nicht setzbar: %w", err)
		}

		if !gewartet {
			gewartet = true
			beimWarten()
		}
		select {
		case <-ctx.Done():
			datei.Close()
			return nil, ctx.Err()
		case <-time.After(sperreNachfragenAlle):
		}
	}
}
