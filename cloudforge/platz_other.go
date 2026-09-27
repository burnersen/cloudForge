//go:build !linux

package main

// Das Programm läuft im Betrieb ausschliesslich unter Linux. Diese Fassung
// gibt es nur, damit sich der Quelltext auf dem Entwicklungsrechner
// übersetzen und prüfen lässt.
//
// Sie meldet bewusst einen sehr grossen Wert statt eines Fehlers: der
// Platzwächter soll beim Prüfen nicht im Weg stehen. Auf dem Zielsystem
// greift immer die Linux-Fassung.
func FreierPlatzBytes(pfad string) (int64, error) {
	const reichlich = 1 << 50 // 1 PB
	return reichlich, nil
}
