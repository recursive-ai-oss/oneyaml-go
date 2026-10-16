package oneyaml

import "oneyaml.dev/go/internal/bind"

// Option configures Marshal, Unmarshal, an Encoder, a Decoder or Parse. One
// option type is shared by every entry point; an option that does not apply to
// the call it is passed to is ignored.
type Option func(*config)

// NoBound, used as the value of a LimitSet field, removes the bound for that
// field alone. Any negative value has the same effect; NoBound gives it a name.
const NoBound = -1

// LimitSet bounds the work and memory a call may spend.
//
// A field left at zero is not changed: it keeps the bound given by the options
// before Limits (the value DefaultLimits gives it if there are none, which is no
// bound for the fields that have none by default). So setting one field adds that
// bound without lifting the others, and Untrusted(), Limits(LimitSet{MaxDepth:
// 50}) tightens the depth of the untrusted preset. Set a field to NoBound, or
// any negative value, to remove its bound, or pass NoLimits to remove them all.
//
// MaxDepth and MaxNumberDigits also apply when writing (the latter to numbers a
// MarshalJSON method returns); the other limits apply to reading.
type LimitSet struct {
	// MaxDepth is the deepest nesting of collections.
	MaxDepth int
	// MaxAliasNodes is the number of nodes that alias expansion may produce.
	// Left at its default, the bound is raised to ten times the number of nodes
	// the document itself holds (two times with Untrusted), so a large document
	// that reuses its anchors is not refused. A value you set with Limits
	// applies exactly as given.
	MaxAliasNodes int
	// MaxAliasBytes is the number of scalar bytes that alias expansion may
	// produce. When left at zero it is raised with the input size in the same way
	// as MaxAliasNodes.
	MaxAliasBytes int
	// MaxInputBytes is the largest input accepted.
	MaxInputBytes int
	// MaxScalarBytes is the longest single scalar accepted.
	MaxScalarBytes int
	// MaxDocuments is the most documents accepted in one stream.
	MaxDocuments int
	// MaxNumberDigits is the most digits (counting the whole text of a float)
	// that an integer or float scalar may have when it is converted to a
	// number, whether into a big.Int, a float, or a generic value. Turning a
	// very long decimal into a big.Int takes time that grows with the square of
	// its length, so a few megabytes of digits could otherwise keep a call busy
	// for minutes. A longer number makes the call fail with a *LimitError.
	// Reading the scalar as a string, or keeping its text in a Node or Document,
	// is never limited because no conversion happens.
	MaxNumberDigits int
	// MaxPatchWorkRatio bounds the text that the edits of one patch (ApplyPatch,
	// Apply or ApplyMergePatch) may have to read again, as a multiple of the
	// document plus the patch, with 16 MiB on top. Most edits touch a small
	// region, but some re-read the whole document, so a long patch against a large
	// document could otherwise cost time that grows with the square of its size.
	// A patch that goes past the bound fails with a *LimitError and changes
	// nothing.
	MaxPatchWorkRatio int
}

// DefaultLimits returns the limits that apply when no limit option is given:
// generous bounds that real documents do not reach but that stop alias bombs and
// runaway nesting.
func DefaultLimits() LimitSet {
	return LimitSet{
		MaxDepth:      10000,
		MaxAliasNodes: 1_000_000,
		MaxAliasBytes: 64 << 20,
		// Far above any number a real document holds; a 100,000 digit integer
		// still converts in about ten milliseconds.
		MaxNumberDigits: 100_000,
		// Real patches stay far below this; it stops a patch of thousands of
		// operations from re-reading a large document for each one.
		MaxPatchWorkRatio: 256,
	}
}

// UntrustedLimits returns the tighter limits that the Untrusted option applies.
func UntrustedLimits() LimitSet {
	return LimitSet{
		MaxDepth:       1000,
		MaxAliasNodes:  100_000,
		MaxAliasBytes:  8 << 20,
		MaxInputBytes:  64 << 20,
		MaxScalarBytes: 16 << 20,
		MaxDocuments:   10_000,
		// The bound Python puts on converting a string to an int.
		MaxNumberDigits:   4300,
		MaxPatchWorkRatio: 32,
	}
}

type config struct {
	indent int
	// The small fields sit together so that they share one word: a config is
	// part of every Document that is parsed.
	knownFields bool
	mergeKeys   bool
	// compactSeq is set by the yaml.v3 compat package for CompactSeqIndent.
	compactSeq bool
	// yaml11 is set by YAML11Strings.
	yaml11 bool
	// exactNodes and exactBytes mark an alias bound the caller set: the ratio
	// does not raise it.
	exactNodes, exactBytes bool
	// dialect is set by the compat packages through an internal option.
	dialect bind.Dialect
	limits  LimitSet
	// aliasRatio raises the alias bounds to this multiple of the document's own
	// size; zero keeps them exactly as given.
	aliasRatio int
	// spend is the alias expansion already paid for by an enclosing decode, so
	// a nested Node.Decode inside an Unmarshaler hook shares its budget.
	spend *bind.AliasSpend
}

func newConfig(opts []Option) config {
	if len(opts) == 0 {
		// The common case must not allocate: applying options passes a pointer to
		// unknown functions, which moves the config to the heap.
		return config{limits: DefaultLimits(), aliasRatio: 10}
	}
	return applyOptions(opts)
}

func applyOptions(opts []Option) config {
	c := &config{limits: DefaultLimits(), aliasRatio: 10}
	for _, o := range opts {
		if o != nil {
			o(c)
		}
	}
	return *c
}

// Indent sets how many spaces each nesting level of block output is indented by.
// The default is 2. Values below 1 select the default and values above 9 are
// treated as 9.
func Indent(n int) Option { return func(c *config) { c.indent = n } }

// YAML11Strings makes a Document quote the new strings it writes that a YAML 1.1
// reader would take for another type. By default an edit judges plainness by the
// YAML 1.2 Core schema, so Set("/a", "yes") writes a: yes, a string to oneyaml but
// a boolean to a program that reads the file with YAML 1.1 rules, such as
// Kubernetes tooling built on sigs.k8s.io/yaml or yaml.v2. With YAML11Strings the
// strings written by Set, by the add, replace and copy operations of a patch, by a
// merge patch, and the new keys written by Rename and add use the rule Marshal
// uses: yes, on, y, 0755, 1_000, 12:30 and 2001-01-01 are quoted, hello is not.
//
// An existing quoted or block style is kept as it is, and values that are not
// strings are written as before. The option applies to Parse and is remembered by
// the Document and by the views Doc returns; other functions ignore it.
func YAML11Strings() Option { return func(c *config) { c.yaml11 = true } }

// KnownFields makes reading into a struct fail on a mapping key that matches no
// field. The key is reported as a *TypeError that errors.Is matches with
// ErrUnknownField, and the rest of the document is still read. It applies to
// reads and is ignored when writing.
func KnownFields() Option { return func(c *config) { c.knownFields = true } }

// MergeKeys makes reading treat the "<<" key as a YAML merge key instead of an
// ordinary key. It applies to reads and is ignored when writing.
func MergeKeys() Option { return func(c *config) { c.mergeKeys = true } }

// Limits sets the bounds that l names and leaves the others as the options
// before it set them: the defaults if there are none, the bounds of Untrusted
// after Untrusted, no bounds after NoLimits. So
// Untrusted(), Limits(LimitSet{MaxDepth: 50}) keeps every untrusted bound and
// tightens the depth, and NoLimits(), Limits(LimitSet{MaxInputBytes: 1 << 20})
// bounds the input and nothing else.
//
// A positive field sets that bound; NoBound removes it. A bound you set applies
// exactly as given, while an alias bound you leave alone keeps the behaviour it
// had: the default and Untrusted ones still grow with the size of the document.
// Limits(LimitSet{}) changes nothing. When writing, MaxDepth and MaxNumberDigits are used.
func Limits(l LimitSet) Option {
	return func(c *config) { c.setLimits(l) }
}

// setLimits applies l on top of the limits already in c: a zero field keeps the
// current value, a negative field becomes the internal "no bound" of zero, and a
// positive field replaces it. The ratio and the exact flags are left alone for
// an alias field that l does not name.
func (c *config) setLimits(l LimitSet) {
	pick := func(v, cur int) int {
		switch {
		case v == 0:
			return cur
		case v < 0:
			return 0
		}
		return v
	}
	if l.MaxAliasNodes != 0 {
		c.exactNodes = true
	}
	if l.MaxAliasBytes != 0 {
		c.exactBytes = true
	}
	cur := c.limits
	c.limits = LimitSet{
		MaxDepth:          pick(l.MaxDepth, cur.MaxDepth),
		MaxAliasNodes:     pick(l.MaxAliasNodes, cur.MaxAliasNodes),
		MaxAliasBytes:     pick(l.MaxAliasBytes, cur.MaxAliasBytes),
		MaxInputBytes:     pick(l.MaxInputBytes, cur.MaxInputBytes),
		MaxScalarBytes:    pick(l.MaxScalarBytes, cur.MaxScalarBytes),
		MaxDocuments:      pick(l.MaxDocuments, cur.MaxDocuments),
		MaxNumberDigits:   pick(l.MaxNumberDigits, cur.MaxNumberDigits),
		MaxPatchWorkRatio: pick(l.MaxPatchWorkRatio, cur.MaxPatchWorkRatio),
	}
}

// Untrusted selects UntrustedLimits, for input from a source you do not control.
// It replaces any limits set before it. Options after it can tighten or lift
// single bounds: Untrusted(), Limits(LimitSet{MaxDepth: 50}).
func Untrusted() Option {
	return func(c *config) {
		c.limits, c.aliasRatio = UntrustedLimits(), 2
		c.exactNodes, c.exactBytes = false, false
	}
}

// NoLimits removes every limit, including the depth limit when writing. Only use
// it for input you trust. A Limits option after it adds back just the bounds it
// names.
func NoLimits() Option {
	return func(c *config) { c.limits, c.aliasRatio = LimitSet{}, 0; c.exactNodes, c.exactBytes = false, false }
}
