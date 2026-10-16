// Package yaml is a compatibility adapter for gopkg.in/yaml.v3
// (go.yaml.in/yaml/v3) built on the oneyaml parser and binder. It has the same
// names and signatures, and the differences and gaps are listed below.
//
// It is opt-in compatibility, not the primary API. New code should use the
// oneyaml package, which reads YAML 1.2 by default, reports every problem at its
// position, and edits documents without losing comments. This package exists for
// code that already uses yaml.v3 and wants the oneyaml parser and limits without
// rewriting its call sites.
//
// # Migrating
//
// Change one import path; your call sites stay as they are. Then check the
// Differences and Gaps below against what your program relies on.
//
//	import yaml "gopkg.in/yaml.v3"
//	// becomes
//	import yaml "oneyaml.dev/go/compat/yamlv3"
//
// The package has the same functions and types as yaml.v3: Unmarshal, Marshal,
// NewDecoder and NewEncoder with their methods, Node with Kind and Style,
// Marshaler, Unmarshaler (and the older function-style unmarshaler), IsZeroer
// and TypeError, and the struct tag options omitempty, flow and inline.
//
// # Behaviour that is kept
//
// What yaml.v3 documents, and what real programs rely on, is reproduced:
//   - plain scalars resolve as yaml.v3 resolves them, not as YAML 1.2 Core does:
//     0777 is octal, 0o17 and 0b101 are integers, 1_000 is an integer, a date is a
//     time.Time (also inside interface{}), and yes, no, on and off are strings
//     that a bool target accepts;
//   - merge keys ("<<") are on;
//   - a mapping decodes to map[string]interface{}, or map[interface{}]interface{}
//     when a key is not a string;
//   - a nil slice or map is written as [] or {}, the default indent is four
//     spaces, sequences are indented under their key, floats are written in Go's
//     shortest form, and map keys come out in yaml.v3's natural order;
//   - a type error does not stop decoding: the rest of the document is read and
//     a *TypeError lists every value that did not fit;
//   - a Node keeps its position, style, tag, anchor and comments, and can be
//     decoded, changed and written again.
//
// # Differences
//
// Each of these is a case where yaml.v3 is wrong, or where the shim keeps a
// limit that yaml.v3 lacks:
//   - YAML that yaml.v3 refuses although it is valid (libyaml scanner limits) is
//     read, and YAML that is invalid but that yaml.v3 accepts is refused;
//   - a plain scalar such as 3.5 is not truncated into an integer target; it is
//     a type error. A float with no fraction (3.0, 1e3) is accepted;
//   - true and "true" are different keys, as the specification says; yaml.v3
//     reports them as one key;
//   - a null in a sequence of numbers leaves a zero in its place instead of being
//     dropped from the result;
//   - alias expansion and nesting depth are limited as in the oneyaml package;
//   - yaml.v3 panics on a type it cannot write, on conflicting inline fields and
//     on several other inputs, among them a Node that holds a nil entry or that
//     contains itself; here those are errors;
//   - a comment on a node never turns into content: every line of a multi-line
//     comment is written as a comment, and the lines after the first of a comment
//     on a block scalar follow the scalar, where yaml.v3 puts them inside it and
//     makes them part of its text;
//   - a type whose UnmarshalYAML method this package cannot call, for example one
//     that takes the Node of gopkg.in/yaml.v3 because it comes from a module that
//     still imports it, makes the read return an error naming the type and the
//     method; yaml.v3 skips such a method silently and fills the value by its
//     fields, which drops whatever the method validated;
//   - alias expansion is counted across the whole call: a type that decodes its
//     own Node again counts against the same budget, which yaml.v3 starts afresh;
//   - what yaml.v3 writes but cannot read back is written correctly: a block
//     scalar that starts with a space or a line break gets the right indentation
//     indicator, the key << is quoted, U+2028 or U+2029 inside a quoted scalar
//     is not followed by indentation, and after a comment inside a flow
//     collection that sits in a block collection, a closing bracket or comma
//     that yaml.v3 would start a line with, outside the collection, is indented;
//   - a top-level block scalar's indentation indicator counts from column -1, as
//     the specification says, where yaml.v3 counts from 0;
//   - Encoder.SetIndent takes effect before the first Encode, which is when
//     yaml.v3 fixes it in practice;
//
// # Limits
//
// Reading runs under the default limits of the oneyaml package (see
// oneyaml.DefaultLimits) and there is no option to change them, as yaml.v3 has
// none. For input from a source you do not control, read it with the oneyaml
// package and its Untrusted option instead.
//
// # Gaps
//
// These are places where the shim does less than yaml.v3, not places where
// yaml.v3 is wrong:
//   - a repeated key is sometimes reported without the line of its first
//     occurrence, as when the repeated key is written as an alias;
//   - comments are recovered from the text and placed on the nodes where
//     yaml.v3 places them in the common layouts: line comments, comments
//     directly above a key or item, foot comments, the comments of a document,
//     and comments after a "---" marker, a lone dash, an anchor or tag on a
//     collection, or an explicit key. Comments inside a flow collection are
//     read and written as yaml.v3 does, except a comment line just before the
//     closing bracket, which is not read.
package yaml
