//go:build !race

package oneyaml

// underRace is true under the race detector, which slows timed tests several times over.
const underRace = false
