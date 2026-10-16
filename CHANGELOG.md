# Changelog

All notable changes are listed here, newest first. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/). A breaking change is marked **Breaking** and links its migration notes. A new major version has a new module path (`oneyaml.dev/go/v2`), and the entry says so. What a v0 release promises, and the supported Go versions, are under [Stability](README.md#stability).

## [Unreleased]

## [0.1.1] - 2026-10-15

### Fixed

- A comment on a `---` marker that also carries a tag or anchor, as in `--- !!null # note`, now stays on the marker line when `Set` or `ApplyMergePatch` gives the empty document content. It used to move below the new content.
- Replacing an empty sequence item that has a comment after its dash (`- # note`) with a mapping or sequence now keeps the comment on the dash line and writes the new collection on the lines below, lined up with the text of the sibling items. The comment used to move to the end of the collection's last line.
- Removing an entry that follows a block scalar no longer returns an `*EditError` when lines of only spaces, indented deeper than the scalar's text, sit below the removed entry. Those lines are removed with the entry, because once it is gone they would become part of the scalar's text.
- `compat/yamlv3`: the comments of a document that has a `---` marker and no content, such as `--- # note`, become the document node's `FootComment`, as yaml.v3 reads them. They used to be lost.
- `compat/yamlv3`: a comment after a dash with no value (`- # note`) becomes the `HeadComment` of the next item, as yaml.v3 reads it, so writing the `Node` back keeps it, on its own line above that item. It used to be lost.

## [0.1.0] - 2026-10-15

The first release. It has no benchmark results; those come in a later update.

### Added

- YAML 1.2 reading with the Core schema by default: `Unmarshal`, `Marshal`, `NewDecoder`, `NewEncoder`, with `encoding/json` vocabulary and `yaml` struct tags (falling back to `json` tags). Reading UTF-8, UTF-16 and UTF-32.
- Hooks for types that read and write themselves, tried in this order when reading: `Unmarshaler` (`UnmarshalYAML(oneyaml.Node) error`), the older `UnmarshalYAML(func(any) error) error` form, `encoding.TextUnmarshaler` for scalars, then `json.Unmarshaler`. When writing: `Marshaler`, `encoding.TextMarshaler`, then `json.Marshaler`; a number that `MarshalJSON` returns is written with exactly its digits, bounded by `MaxNumberDigits`. Types written for `encoding/json` need no YAML code. An `UnmarshalYAML` or `MarshalYAML` method with any other signature, such as yaml.v3's `UnmarshalYAML(*yaml.Node) error`, is a `*TypeError` instead of being skipped. See [Migrating from yaml.v3](docs/migrate-yaml-v3.md#hooks).
- Struct tag options `omitempty`, `omitzero`, `inline` and `flow`. `omitempty` leaves out a struct whose fields are all zero, or whose `IsZero` method returns true, as yaml.v3 does; `omitzero` leaves out zero values as `encoding/json` does.
- `Parse` and `Document`: a lossless document with `Get`, `Set`, `Remove`, `Decode`, `Bytes`, `WriteTo`, stream facts and a read-only `Node` view. Edits change only the value you set, in its own style; comments and formatting elsewhere are kept byte for byte.
- `Node.Pointer`, which gives the RFC 6901 pointer of a node so that you can collect places while reading and edit afterwards, and `Node.Stale`, which reports a node read before an edit. A stale node's `Decode` and `Pointer` return an error matching `ErrStaleNode`, `Marshal` of a stale node is an error instead of `null`. A `range` over `Node.Items` or `Node.Entries` may edit the document, as a loop over a map may change the map: after an edit it finds its collection again by pointer and goes on with fresh nodes, and it ends if the collection is gone, changed kind or has no pointer. Edits that add or remove entries may make the loop skip or repeat some; collect pointers first for those. An edit, patch or merge patch that fails is rolled back completely and leaves the nodes read before it valid. `ErrNoPointer` reports a node that no pointer addresses, such as a mapping key. See [Editing](docs/editing.md#reading-while-you-edit).
- A `Node` is not a decode target: decoding into one is a `*TypeError` that points to `Document.Get(pointer, &node)`. `Marshal` writes a `Node` as the value it holds.
- RFC 6902 JSON Patch (`ApplyPatch`, `Apply`), with `test` comparing values by the YAML data model, so `1` and `1.0` differ ([details](docs/patches.md#the-test-operation)); RFC 6901 pointers; and RFC 7396 merge patch (`ApplyMergePatch`). All are atomic. See [Patches](docs/patches.md).
- Limits: depth, alias expansion, number digits and patch work are bounded by default; the `Untrusted` preset adds bounds on input size, scalar size and documents per stream; `NoLimits` lifts every bound. Limit options apply in order: `Limits` changes only the `LimitSet` fields you set and keeps the rest from the options before it, and `NoBound` lifts one bound, so `Untrusted(), Limits(LimitSet{MaxDepth: 50})` keeps every untrusted bound and tightens depth. See [Limits](docs/limits.md).
- Typed errors: `SyntaxError`, `TypeError`, `LimitError`, `EditError` and the sentinel kinds. Methods on a nil `*Document` return `ErrNilDocument` or zero values instead of panicking. Messages carry the `oneyaml:` prefix once; match errors with `errors.Is` and `errors.As`, not by their text.
- `compat/yamlv3`, a compatibility adapter for `gopkg.in/yaml.v3` with the same names and signatures and listed differences. See [Migrating from yaml.v3](docs/migrate-yaml-v3.md).
- `compat/k8syaml`, a compatibility adapter for `sigs.k8s.io/yaml` with the same names and signatures and listed differences. See [Migrating from sigs.k8s.io/yaml](docs/migrate-sigs-k8s-yaml.md).
- Conformance: the tests that run the [oneyaml-kit](https://github.com/recursive-ai-oss/oneyaml-kit) cases run in CI against a pinned kit revision. See [Conformance](docs/conformance.md).
- Documentation: [Editing](docs/editing.md), [Patches](docs/patches.md), [Limits](docs/limits.md), [Conformance](docs/conformance.md), [Comparison](docs/comparison.md) and the two migration guides.

[Unreleased]: https://github.com/recursive-ai-oss/oneyaml-go/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/recursive-ai-oss/oneyaml-go/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/recursive-ai-oss/oneyaml-go/releases/tag/v0.1.0
