// Package oneyaml reads, writes and edits YAML. Spec-correct YAML 1.2 by
// default, in pure Go with no dependencies outside the standard library.
//
// The package has two halves. The first is a typed load and dump in the style of
// [encoding/json]: [Unmarshal], [Marshal], [NewDecoder] and [NewEncoder]. The
// second is [Document], which holds a whole YAML file and edits it in place.
// Edits change only the value you set, in its own style; comments and formatting
// elsewhere are kept byte for byte.
//
// Nesting, alias expansion, number size and patch work are bounded by default;
// input from outside needs the [Untrusted] option as well. See "Limits" below.
//
// # Install
//
//	go get oneyaml.dev/go@latest
//
// The import path is oneyaml.dev/go and the package name is oneyaml. This is a
// v0 release: the API may change before 1.0. The supported Go versions and what
// each release promises are in the [Stability] section of the README.
//
// # Load
//
// [Unmarshal] reads the first document of its input into a Go value. Struct
// fields are matched through the yaml struct tag; with no yaml tag a json tag is
// used. A document read into
// any gives map[string]any, []any, string, bool, int, int64, uint64,
// [math/big.Int] and float64. Integers are never rounded. Plain scalars follow
// the YAML 1.2 Core schema: plain yes, no, on and off are strings, plain 0755 is
// the integer 755, and a quoted scalar such as "0755" is always a string.
// [NewDecoder] reads a stream of documents and returns [io.EOF] at
// the end.
//
// A type can read its own YAML. The hooks are tried in this order:
// [Unmarshaler], which receives a [Node]; UnmarshalYAML(func(any) error) error,
// the older form that yaml.v2, yaml.v3 and goccy/go-yaml accept;
// [encoding.TextUnmarshaler], for a
// scalar; and [encoding/json.Unmarshaler], which receives the value as JSON,
// a plain number with its digits as written. So
// types written for encoding/json, such as Kubernetes API types with json tags
// and UnmarshalJSON methods, load as they are. An UnmarshalYAML method of any
// other form, such as yaml.v3's UnmarshalYAML(*yaml.Node) error, is reported as
// a [*TypeError] rather than skipped. A [Node] is not a decode target; read one
// with [Document.Get].
//
// # Edit
//
// [Parse] returns a [Document] that keeps every byte of its input.
// [Document.Set] assigns a value at an RFC 6901 pointer: it replaces an existing
// value, keeping its style when the style can hold the new value and moving to
// the nearest style that can when it cannot, and it adds a missing key at the
// end of its mapping in the style of its siblings. Reading the value back always
// gives exactly the value set. [Document.Remove] deletes an entry with the
// comments that belong to it, [Document.Rename] changes a key in place, and
// [Document.SetHeadComment] and [Document.SetLineComment] write or remove a
// comment. [Document.Bytes] and [Document.WriteTo] write the result, and a
// Document prints as its text. An edit that cannot be made, such as one that would write through an
// alias, returns an [*EditError] and leaves the document unchanged.
//
// An edit makes every [Node] read before it stale ([Node.Stale]). A range over
// [Node.Items] or [Node.Entries] may edit the document, as a loop over a map may
// change the map: it finds its collection again and goes on with fresh nodes.
// Edits that add or remove entries may make such a loop skip or repeat some; for
// those, collect the places with [Node.Pointer] first and edit afterwards.
//
// [Document.Get] and [Document.Decode] read values back through the same binder
// that [Unmarshal] uses, and [Document.Root] gives a read-only [Node] view with
// kinds, styles, tags, anchors, comments and positions.
//
// # Patch
//
// [Document.ApplyPatch] applies JSON Patch operations (RFC 6902) with YAML
// equality for test, written as JSON or as a YAML block list: test compares
// values by the YAML data model, so 1 and 1.0 differ (see the
// Document.ApplyPatch documentation). [Document.Apply] takes the same
// operations as Go values ([PatchAdd], [PatchReplace] and the rest), and
// [Document.ApplyMergePatch] applies an RFC 7396 merge patch. A patch is
// atomic: if any operation fails, the document is unchanged. The edits it makes
// follow the same rules as [Document.Set].
//
// # Marshal
//
// [Marshal] and [Encoder] write Go values as block-style YAML. A string is
// written plain only when every common reader, under the Core schema and under
// YAML 1.1, reads it back as the same string, so "yes", "0755" and "1:30" come
// out quoted. Output is deterministic: map keys are sorted and struct fields keep
// their declaration order. A type can choose its own form by implementing
// [Marshaler], [encoding.TextMarshaler] or [encoding/json.Marshaler], tried in
// that order. A [Node] is written as the value it holds. The omitempty tag
// option leaves out zero values, empty collections and structs whose fields are
// all zero; omitzero leaves out only zero values. [Indent] sets the indentation.
//
// # Limits
//
// Every call that reads input runs under limits on nesting depth, alias
// expansion, the digits of a number and the work of a patch; see
// [DefaultLimits]. They are on unless you pass [NoLimits]. Input size, scalar
// size and documents per stream are bounded only with [Untrusted], which you
// should select, and size with [Limits], for input from a source you do not
// control. Options apply in order: [Limits] changes only the fields it sets,
// on top of the options before it, and [NoBound] lifts one bound. So
// Untrusted(), Limits(LimitSet{MaxDepth: 50}) keeps every untrusted bound and
// tightens depth. A limit that is hit is a [*LimitError]. The Document and
// decoder paths follow aliases under the same budget. Code in your own hooks
// runs outside these limits, and the compat packages always use the defaults.
//
// # Errors
//
// Errors are values to test with [errors.As] and [errors.Is]: [*SyntaxError]
// carries the line, column, offset and a category ([CategorySyntax],
// [CategoryEncoding] or [CategoryDuplicateKey], which also names the key and
// where it first appeared); [*TypeError] the path and position of a value that
// does not fit its target, and of every other one when a read finds several,
// with [ErrUnknownField] to match a key the target lacks; [*LimitError] the
// limit and the option that raises it; and [*EditError] the operation, the
// pointer and a kind to match against [ErrNotFound], [ErrAliasWrite],
// [ErrAmbiguousKey], [ErrTestFailed], [ErrAnchorInUse], [ErrInvalidPatch],
// [ErrInvalidPointer], [ErrUndefinedAlias], [ErrUnsupportedEdit],
// [ErrKeyExists], [ErrNotAnEntry], [ErrInvalidComment], [ErrStaleNode],
// [ErrNoPointer] and [ErrNilDocument]. Match errors by type and kind: the text of a message is
// written for people and may change in any release.
//
// The library contains no unsafe code. A panic caused by input is treated as a
// security bug; report it as described in the security policy in the
// repository.
//
// # Options
//
// One [Option] type serves every entry point, and an option that does not apply
// to a call is ignored: [KnownFields] makes an unknown struct key an error,
// [MergeKeys] reads the "<<" merge key, [Indent] sets the output indentation,
// and [Limits], [Untrusted] and [NoLimits] set the limits, in the order given.
// Duplicate keys are always an error.
//
// # Compatibility
//
// The compatibility adapters oneyaml.dev/go/compat/yamlv3 and
// oneyaml.dev/go/compat/k8syaml have the names and signatures of
// gopkg.in/yaml.v3 and sigs.k8s.io/yaml, so code that uses them keeps its call
// sites after one import change. They read YAML 1.1 forms the way their targets
// do, which this package never does, and each lists where it differs from its
// target.
//
// # More
//
// The repository README has a quick start, and its docs directory has guides on
// editing, patches, limits, conformance, migration and a comparison with other
// Go YAML libraries.
//
// [Stability]: https://github.com/recursive-ai-oss/oneyaml-go#stability
package oneyaml
