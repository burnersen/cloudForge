package main

// Vorab holen (seit 0.10.0): Während eine Datei umgewandelt wird, hat die
// Leitung zur Cloud nichts zu tun. Die nächste Datei wird deshalb schon in
// dieser Zeit heruntergeladen — umgewandelt wird weiterhin immer nur eine.
// Gemessen am 26.09.2026: Holen dauert rund 3 Minuten je Film, bei 45 bis 60
// Minuten für den ganzen Film also etwa 6 % der Zeit.
//
// Die Kopie liegt in einem eigenen Ordner mit demselben Namensmuster wie die
// Arbeitsplätze der Dateien. So räumt ArbeitsresteEntfernen sie nach einem
// Absturz genauso weg wie alles andere.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// errVorabUnbrauchbar heisst: die Kopie gilt nicht, die Datei wird ganz
// normal geholt. Das ist kein Fehler der Datei.
var errVorabUnbrauchbar = errors.New("Vorab-Kopie nicht verwendbar")

// VorabKopie ist eine Kopie der nächsten Datei, die im Hintergrund läuft
// oder schon fertig ist.
type VorabKopie struct {
	QuellPfad string

	ordner string
	datei  string

	// So sah die Quelle beim Start aus. Ändert sie sich bis zur Übernahme,
	// gilt die Kopie nicht — lieber frisch holen als eine alte Fassung
	// umwandeln und danach das neue Original wegräumen.
	groesse   int64
	geaendert time.Time

	aufhoeren context.CancelFunc
	fertig    chan struct{} // wird geschlossen, sobald die Kopie endet
	fehler    error         // erst nach <-fertig lesen

	mu    sync.Mutex
	stand Stand // letzter gemeldeter Stand, für die Anzeige beim Warten
}

// VorabHolenStarten beginnt, quellPfad im Hintergrund in den Arbeitsordner zu
// kopieren. laufendeBytes ist der Platz, den die gerade laufende Datei noch
// braucht (ihr Ergebnis wächst ja noch). Liefert nil und einen Grund, wenn
// es nicht geht — dann wird die Datei später ganz normal geholt.
//
// ctx muss der Lauf sein, nicht die einzelne Datei: läuft bei der aktuellen
// Datei die Notbremse ab, soll die Kopie der nächsten trotzdem weiterlaufen.
func VorabHolenStarten(ctx context.Context, quellPfad string, laufendeBytes int64, e Einstellungen) (*VorabKopie, string) {
	quelle, err := os.Stat(quellPfad)
	if err != nil {
		return nil, "Quelle nicht lesbar"
	}
	if err := PlatzPruefen(e.ArbeitsOrdner, quelle.Size()+laufendeBytes, e); err != nil {
		return nil, err.Error()
	}
	ordner, err := os.MkdirTemp(e.ArbeitsOrdner, arbeitsVorsilbe)
	if err != nil {
		return nil, "Ordner nicht anlegbar"
	}

	kopierCtx, aufhoeren := context.WithCancel(ctx)
	v := &VorabKopie{
		QuellPfad: quellPfad,
		ordner:    ordner,
		datei:     filepath.Join(ordner, filepath.Base(quellPfad)),
		groesse:   quelle.Size(),
		geaendert: quelle.ModTime(),
		aufhoeren: aufhoeren,
		fertig:    make(chan struct{}),
	}
	go func() {
		defer close(v.fertig)
		v.fehler = Kopieren(kopierCtx, quellPfad, v.datei, v.standMerken)
	}()
	return v, ""
}

func (v *VorabKopie) standMerken(s Stand) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.stand = s
}

func (v *VorabKopie) letzterStand() Stand {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.stand
}

// Uebernehmen wartet, bis die Kopie fertig ist, und legt sie an zielPfad
// (Umbenennen, beides liegt im Arbeitsordner). Solange noch kopiert wird,
// meldet es den Stand. Gilt die Kopie nicht, kommt errVorabUnbrauchbar mit
// dem Grund zurück; bei einem Abbruch ErrAbgebrochen.
func (v *VorabKopie) Uebernehmen(ctx context.Context, zielPfad string, melde Rueckmeldung) error {
	if err := v.abwarten(ctx, melde); err != nil {
		return err
	}
	if v.fehler != nil {
		return fmt.Errorf("%w: %v", errVorabUnbrauchbar, v.fehler)
	}

	quelle, err := os.Stat(v.QuellPfad)
	if err != nil {
		return fmt.Errorf("%w: Quelle nicht mehr lesbar", errVorabUnbrauchbar)
	}
	if quelle.Size() != v.groesse || !quelle.ModTime().Equal(v.geaendert) {
		return fmt.Errorf("%w: die Quelle hat sich inzwischen veraendert", errVorabUnbrauchbar)
	}

	if err := os.Rename(v.datei, zielPfad); err != nil {
		return fmt.Errorf("%w: %v", errVorabUnbrauchbar, err)
	}
	return nil
}

// abwarten wartet auf das Ende der Kopie. Ein Abbruch hat immer Vorrang:
// endete die Kopie gerade WEGEN des Abbruchs, ist das kein Grund, die Datei
// danach noch einmal frisch zu holen.
func (v *VorabKopie) abwarten(ctx context.Context, melde Rueckmeldung) error {
	takt := time.NewTicker(kopierMeldeTakt)
	defer takt.Stop()
	for {
		if ctx.Err() != nil {
			return ErrAbgebrochen
		}
		select {
		case <-v.fertig:
			if ctx.Err() != nil {
				return ErrAbgebrochen
			}
			return nil
		case <-ctx.Done():
			return ErrAbgebrochen
		case <-takt.C:
			if melde != nil {
				melde(v.letzterStand())
			}
		}
	}
}

// naechsteVorabHolen startet die Kopie der nächsten Datei, sofern es eine
// gibt. Geht das nicht, steht der Grund in der Anzeige, und die Datei wird
// später ganz normal geholt.
func (a *Ablauf) naechsteVorabHolen(ctx context.Context, laufendeBytes int64) {
	if a.Naechste == "" || a.vorab != nil {
		return
	}
	vorab, grund := VorabHolenStarten(ctx, a.Naechste, laufendeBytes, a.Einstellungen)
	if vorab == nil {
		a.Anzeige.Zeile("  Naechste Datei wird nicht vorab geholt: %s", grund)
		return
	}
	a.vorab = vorab
	a.Anzeige.Zeile("  Naechste Datei wird waehrenddessen schon geholt: %s", filepath.Base(a.Naechste))
}

// vorabFuer nimmt die Vorab-Kopie heraus, wenn sie zu quellPfad gehört. Die
// Kopie einer anderen Datei wird verworfen — sie würde nur Platz belegen.
func (a *Ablauf) vorabFuer(quellPfad string) *VorabKopie {
	vorab := a.vorab
	a.vorab = nil
	if vorab != nil && vorab.QuellPfad != quellPfad {
		vorab.Verwerfen()
		return nil
	}
	return vorab
}

// VorabVerwerfen räumt eine Kopie weg, die niemand mehr abholt — am Ende
// eines Laufs, nach einem Abbruch oder wenn der Zeitplan Vorfahrt gibt.
func (a *Ablauf) VorabVerwerfen() {
	a.vorab.Verwerfen()
	a.vorab = nil
}

// holen legt die Quelle in den Arbeitsplatz: aus der Vorab-Kopie, wenn es
// sie gibt und sie gilt, sonst frisch aus der Cloud. woher ergänzt die
// Anzeige ("vorab geholt").
func (a *Ablauf) holen(ctx context.Context, vorab *VorabKopie, quellPfad, lokaleQuelle string) (woher string, err error) {
	if vorab != nil {
		err := vorab.Uebernehmen(ctx, lokaleQuelle, a.Anzeige.Stand)
		if err == nil {
			return ", vorab geholt", nil
		}
		if !errors.Is(err, errVorabUnbrauchbar) {
			return "", err // abgebrochen
		}
		a.Anzeige.Zeile("  %v - wird neu geholt", err)
	}
	return "", Kopieren(ctx, quellPfad, lokaleQuelle, a.Anzeige.Stand)
}

// Verwerfen bricht eine laufende Kopie ab und räumt ihren Ordner weg. Auf
// nil wirkungslos und beliebig oft aufrufbar, damit es einfach per defer
// dastehen kann — auch nach einer erfolgreichen Übernahme, dann ist der
// Ordner nur noch leer.
func (v *VorabKopie) Verwerfen() {
	if v == nil {
		return
	}
	v.aufhoeren()
	<-v.fertig // erst wenn Kopieren aufgehört hat, gehört der Ordner niemandem mehr
	os.RemoveAll(v.ordner)
}
