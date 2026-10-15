//go:build race

package oneyaml_test

// raceEnabled is true under the race detector, which makes sync.Pool drop items
// at random and so changes allocation counts.
const raceEnabled = true
