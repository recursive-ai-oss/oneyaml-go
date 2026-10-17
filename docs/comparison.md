# Comparison

How oneyaml-go compares with the Go YAML libraries it is most often weighed against: `gopkg.in/yaml.v3` (and its maintained fork `go.yaml.in/yaml/v3`), its prerelease successor `go.yaml.in/yaml/v4`, `sigs.k8s.io/yaml` and `github.com/goccy/go-yaml`. We wrote oneyaml-go, so we chose the criteria; corrections are welcome as issues. Time and memory are on [Performance](performance.md).

## What each library is

| | oneyaml-go | yaml.v3 | yaml/v4 | sigs.k8s.io/yaml | goccy/go-yaml |
|---|---|---|---|---|---|
| Approach | one parser feeding a decoder and a lossless document | port of libyaml, `Node` tree | the yaml.v3 code base, `Node` tree, `Load`/`Dump` with options | converts YAML to JSON, `encoding/json` binds | own parser with an AST |
| Default YAML version | 1.2, Core schema | a mix of 1.1 and 1.2 | a mix of 1.1 and 1.2 | 1.1 | a mix |
| Editing that keeps the file | yes: untouched bytes are kept | comments kept, layout re-encoded | comments kept, layout re-encoded | no | comments kept through the AST |
| JSON Patch and merge patch | yes | no | no | no | no |
| Status (surveyed 2026-09-30; yaml/v4 2026-10-15) | new | original repo archived in 2025; the fork gets security fixes | prerelease (`v4.0.0-rc.6`); where new go-yaml work happens | maintained by the Kubernetes project | maintained |
| Dependencies | none outside the standard library | none | none | a yaml.v2 fork | none |

The yaml/v4 documentation says of its `Node` that "the content when re-encoded will not have its original textual representation preserved" ([`Node`](https://pkg.go.dev/go.yaml.in/yaml/v4@v4.0.0-rc.6#Node)). yaml/v4 has a limit plugin (`WithPlugin` with `plugin/limit`) for nesting depth and alias expansion. Third-party packages add patching on top of yaml.v3, goccy/go-yaml and sigs.k8s.io/yaml; [Performance](performance.md) compares against them.

## Where oneyaml-go is the better fit

- You change YAML files that people also edit by hand, and the diff must hold only your change ([Editing](editing.md)).
- You want YAML 1.2 with the Core schema, with the known deviations listed ([Conformance](conformance.md)).
- You read input from outside and want bounds on by default and a tight preset in one option ([Limits](limits.md)).
- You want RFC 6902 or RFC 7396 patches applied to a YAML file ([Patches](patches.md)).
- You want typed loads, dumps and edits that take less time and memory ([Performance](performance.md)).

## Where the others are the better fit

**yaml.v3**

- It is the incumbent: your dependencies and tools probably already import it, and staying costs nothing.
- Its `Node` API is mature, and code and examples for it are everywhere.
- It is the reference for data it already wrote. `compat/yamlv3` reproduces its documented behaviour, not its bugs.

**yaml/v4**

- It continues the yaml.v3 code base, so code and knowledge carry over, and `WithV3Defaults` selects yaml.v3's defaults.
- New go-yaml work happens there. It is a prerelease today.

**sigs.k8s.io/yaml**

- It is the Kubernetes reference: a manifest means what it says. `oneyaml` ranks YAML and text methods above `UnmarshalJSON` and reads YAML 1.2 scalars, so a type with several hooks, or a manifest that relies on YAML 1.1 forms, can read differently.

**goccy/go-yaml**

- It has an AST and YAML Path queries for filtering and replacing. `oneyaml` pointers address one value; there are no queries.
- It passes more of the yaml-test-suite than yaml.v3.

**All four**

- They have years of use in the field. oneyaml-go is new ([changelog](../CHANGELOG.md)).

## Choosing

| If you | Use |
|---|---|
| Edit YAML files that humans maintain | oneyaml-go |
| Apply JSON Patch or merge patch to YAML | oneyaml-go |
| Read untrusted input | oneyaml-go with `Untrusted()` |
| Have a working yaml.v3 or sigs.k8s.io/yaml program and no reason to change | keep it, or swap in a compat package for its parser and limits |
| Need AST queries and YAML Path | goccy/go-yaml |
| Need to match Kubernetes behaviour exactly | sigs.k8s.io/yaml |
