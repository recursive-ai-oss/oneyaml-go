// Package compatopt carries the hidden settings between the root package and
// the compat packages.
//
// The compat packages (yamlv3, k8syaml) need a few switches that are not part of
// the primary API. The root package cannot be imported from here, so it fills in
// the constructors from its init function and the compat packages call them.
package compatopt

import "fmt"

// V3 returns the root package's Option that selects the yaml.v3 dialect for
// reading and writing. The root package sets it in init; the result is an
// oneyaml.Option held as an any because this package cannot name that type.
var V3 func() any

// V3Compact returns the root package's Option for yaml.v3's compact sequence
// indent. The root package sets it in init.
var V3Compact func() any

// Item is one type error collected while a document was read.
type Item struct {
	Line, Column int
	Msg          string
	// Verbatim says that Msg already carries its own position (an error a user's
	// hook returned), so a caller must not prefix a line number.
	Verbatim bool
}

// Collected is the error a read returns when the yaml.v3 dialect finished the
// document but some values did not fit their targets.
type Collected struct {
	Items []Item
}

func (c *Collected) Error() string {
	if len(c.Items) == 0 {
		return "oneyaml: type errors"
	}
	return fmt.Sprintf("oneyaml: %d type errors, first: %s", len(c.Items), c.Items[0].Msg)
}
