// Package oneyaml reads, writes and edits YAML. It follows YAML 1.2 with the
// Core schema by default, is written in pure Go and has no dependencies outside
// the standard library.
//
// The package has two halves, built on one parser.
//
// [Unmarshal], [Marshal], [NewDecoder] and [NewEncoder] load and dump typed
// values as their counterparts in [encoding/json] do, with yaml struct tags, or
// json tags where a field has no yaml tag. The Unmarshal example shows a typed
// read.
//
// [Parse] returns a [Document] that edits a file in place: [Document.Set],
// [Document.Remove], [Document.Rename] and the comment setters change only what
// they address, and [Document.ApplyPatch] and [Document.ApplyMergePatch] apply
// RFC 6902 and RFC 7396 patches to it. Comments and every byte you do not touch
// are kept. The package example reads a file into a struct and then edits it.
//
// # Limits
//
// Nesting depth, alias expansion, number size and patch work are bounded by
// default ([DefaultLimits]); [NoLimits] lifts every bound. For input you do not control,
// pass [Untrusted], which also bounds input size; [Limits] changes single
// bounds. A bound that is hit is a [*LimitError]. Code in your own hooks runs
// outside these bounds.
//
// # Errors
//
// Match errors with [errors.Is] and [errors.As], not by their text, which may
// change. Reads return a [*SyntaxError] or a [*TypeError], edits and patches an
// [*EditError], and a bound a [*LimitError]. Each holds the details of the
// failure and matches a sentinel such as [ErrNotFound] or [ErrMaxDepth].
//
// # Compatibility
//
// The adapters oneyaml.dev/go/compat/yamlv3 and oneyaml.dev/go/compat/k8syaml
// have the API of gopkg.in/yaml.v3 and sigs.k8s.io/yaml and read YAML 1.1 forms
// as their targets do, so existing code moves over with an import change. Each
// lists where it differs from its target.
//
// The [README] links to guides on editing, patches, limits and migration;
// [Stability] says what a release promises.
//
// [README]: https://github.com/recursive-ai-oss/oneyaml-go#readme
// [Stability]: https://github.com/recursive-ai-oss/oneyaml-go/blob/main/docs/stability.md
package oneyaml
