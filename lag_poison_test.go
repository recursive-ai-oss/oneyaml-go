package oneyaml

import (
	"os"

	"oneyaml.dev/go/internal/cst"
)

// ONEYAML_LAG_POISON makes every stale offset in the package's tests wrong by a
// large amount, so a read that skipped the settling step shows up as a failure in
// whichever test it happens in, and in the fuzz targets.
func init() {
	if os.Getenv("ONEYAML_LAG_POISON") != "" {
		cst.LagPoison = 1 << 20
	}
}
