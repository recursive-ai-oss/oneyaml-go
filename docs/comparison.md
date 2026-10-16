# Comparison

How oneyaml-go compares with the Go YAML libraries it is most often weighed against: `gopkg.in/yaml.v3` (and its maintained fork `go.yaml.in/yaml/v3`), its prerelease successor `go.yaml.in/yaml/v4`, `sigs.k8s.io/yaml` and `github.com/goccy/go-yaml`.

**Bias.** This page is written by the authors of oneyaml-go. We chose the criteria, and they are the ones oneyaml-go was built for: YAML 1.2 behaviour, editing a file without disturbing it, and bounded handling of untrusted input. A project with other priorities would draw a different table. We tried to state each library's strengths as its own users would. Where a statement rests on a source, the source is named; library status was surveyed on 2026-09-30 (yaml/v4 on 2026-10-15) and changes.

This page compares features and behaviour. Time and memory are measured on [Performance](performance.md), from a full benchmark run against these libraries; its method line names the library commit measured and the date, and the page says which release that commit is. That page has the evidence, and this one does not repeat it.

## What each library is

| | oneyaml-go | yaml.v3 | yaml/v4 | sigs.k8s.io/yaml | goccy/go-yaml |
|---|---|---|---|---|---|
| Approach | own scanner, one parser feeding a decoder and a lossless document | own port of libyaml, `Node` tree | the yaml.v3 code base, `Node` tree, `Load`/`Dump` with options | converts YAML to JSON, `encoding/json` binds | own parser with an AST |
| Default YAML version | 1.2, Core schema | a mix of 1.1 and 1.2 | a mix of 1.1 and 1.2 | 1.1 | a mix |
| Status | new | original repo archived in 2025; `go.yaml.in/yaml/v3` maintained, security fixes only | prerelease: `v4.0.0-rc.6` (2026-06-17) was the newest on 2026-10-15; where new work on go-yaml happens | maintained by the Kubernetes project | maintained |
| Dependencies | none outside the standard library | none | none | yaml.v2 fork | none |

## Like with like

Compare libraries on the job they share.

- **Typed load and dump** (`Unmarshal`, `Marshal`, decoders and encoders): all five. yaml/v4 adds `Load` and `Dump` with functional options, as oneyaml-go takes options on its calls. oneyaml-go reads YAML 1.2 forms where yaml.v3 and sigs.k8s.io/yaml read many 1.1 forms; the compat packages ([yaml.v3](migrate-yaml-v3.md), [sigs.k8s.io/yaml](migrate-sigs-k8s-yaml.md)) bring the 1.1 forms back.
- **Editing a file and keeping it as it was:** yaml.v3 and yaml/v4 (`Node`, which encodes the whole tree again) and goccy/go-yaml (AST and YAML Path) can keep comments; sigs.k8s.io/yaml cannot, because the JSON round trip has no comments. The yaml/v4 documentation says of its `Node` that "the content when re-encoded will not have its original textual representation preserved" ([`Node`](https://pkg.go.dev/go.yaml.in/yaml/v4@v4.0.0-rc.6#Node)). oneyaml-go's `Document` keeps every byte it does not change; it does not re-encode the tree.
- **Limits on input:** oneyaml-go bounds depth, alias expansion, number size and patch work by default and has an `Untrusted()` preset ([Limits](limits.md)). yaml/v4 has a limit plugin (`WithPlugin` with `plugin/limit`) that checks nesting depth and alias expansion, configured per call.
- **Patching:** oneyaml-go applies RFC 6902 and 7396 to YAML, with `test` comparing values by the YAML data model rather than as JSON values ([details](patches.md#the-test-operation)). None of the other four has a patch API; third-party packages build one on top of them.
- **Kubernetes manifests:** sigs.k8s.io/yaml is the reference for how a manifest binds to a type with `json` tags. `compat/k8syaml` reproduces it.
- **Benchmarks:** the benchmark times a library on a task only after it checks the library's answer. On the document tasks, a library counts only where its document wrote the input back byte for byte, so on many inputs yaml.v3, yaml/v4 and goccy/go-yaml have no number. Those rows [have no rival](performance.md#rows-with-no-rival) and are not wins.

## Where oneyaml-go is the better fit

- You change YAML files that people also edit by hand, and the diff must contain only your change. As an illustration, in the project's own checks on the two block-scalar cases of [Editing](editing.md), yaml.v3 kept the block style for both but lost the blank line before the next comment, and goccy/go-yaml kept the first case byte for byte but wrote a plain scalar for the second unless the caller supplied the style. Two cases show the approach; they do not measure it.
- You need YAML 1.2 with the Core schema, checked against the yaml-test-suite, with the known deviations listed ([Conformance](conformance.md)).
- You read input from outside, and want bounds on nesting, alias expansion and number size on by default, and a tighter preset in one option that also bounds input size. See [Limits](limits.md) and what they do not cover.
- You want RFC 6902 or 7396 patches on a YAML file.
- Time and memory matter for loading, writing or editing. [Performance](performance.md) compares each workload with the strongest competitor that gave a correct answer, shows the verdict for each, and explains why the unchanged-emit row is [not a rendering speed](performance.md#unchanged-emit).

## Where the others are the better fit

**yaml.v3**

- It is the incumbent. Tens of thousands of modules import it, and your dependencies, linters and tools probably already do. Staying on it costs nothing.
- Its `Node` API is mature and widely documented; code and examples for it are everywhere.
- Bug-for-bug compatibility with data it already wrote. The compat package reproduces its documented behaviour, but the original is the reference.

**yaml/v4**

- It continues the yaml.v3 code base under the YAML organization, so code and knowledge from yaml.v3 carry over, and an option preset (`WithV3Defaults`) selects yaml.v3-compatible defaults.
- Its maintainers say all new go-yaml work happens in v4. It is a prerelease today; check its status when you choose.

**sigs.k8s.io/yaml**

- It is the Kubernetes reference. A manifest means what sigs.k8s.io/yaml says it means. The `oneyaml` package calls `UnmarshalJSON` and `MarshalJSON` too, but ranks YAML and text methods above them and reads YAML 1.2 scalars, so a type with several hooks, or a manifest that relies on YAML 1.1 forms, can read differently. The compat package follows the original, but the original remains the authority.

**goccy/go-yaml**

- It exposes an AST and YAML Path expressions for filtering and replacing. `oneyaml` does not offer queries; its pointers address one value.
- In the project's own checks it passes more of the yaml-test-suite than yaml.v3 does.

**All four**

- They are older and have more field experience. oneyaml-go is new: its releases are listed in the [changelog](../CHANGELOG.md), and it has little use in the field so far.

## Choosing

| If you | Use |
|---|---|
| Edit YAML files that humans maintain | oneyaml-go |
| Apply JSON Patch or merge patch to YAML | oneyaml-go |
| Read untrusted input | oneyaml-go with `Untrusted()` |
| Have a working yaml.v3 or sigs.k8s.io/yaml program and no reason to change | keep it, or swap in the compat package to gain its parser and limits |
| Need AST queries and YAML Path | goccy/go-yaml |
| Need to match Kubernetes behaviour exactly | sigs.k8s.io/yaml |

Corrections are welcome; open an issue with the behaviour and a reproduction.
