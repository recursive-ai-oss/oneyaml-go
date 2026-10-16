# Performance

This page compares the time and memory of oneyaml-go with those of the other Go YAML libraries. Every library runs the same tasks on the same inputs, through the benchmark of [oneyaml-kit](https://github.com/recursive-ai-oss/oneyaml-kit), the kit shared by the oneyaml libraries. The tables under [Results](#results) are generated from the kit's result file, and none of their numbers is typed by hand. The summary at the top of the [README](../README.md) is generated from the same file.

The figures are measured, not bounds. They describe these inputs on this machine, and your files and hardware will give other numbers.

## About this run

- **Library:** oneyaml-go v0.3.0 (commit `30e7fc4`). The library has changed since that release, and these figures do not cover the changes. This page is generated again from a new run at each release.
- **Competitors:** `go.yaml.in/yaml/v3`, the maintained fork of yaml.v3 (`yaml-v3` in the tables), `go.yaml.in/yaml/v4` (`yaml-v4`), `gopkg.in/yaml.v2` (`yaml-v2`), `sigs.k8s.io/yaml` (`sigs-yaml`) and `github.com/goccy/go-yaml` (`goccy-go-yaml`). For patches, they are `github.com/palantir/pkg/yamlpatch` with its yaml.v3 back end (`palantir-yamlpatch`) and with its goccy/go-yaml back end (`palantir-yamlpatch-goccy`), and `github.com/evanphx/json-patch/v5` on top of sigs.k8s.io/yaml (`json-patch`). The method line under each table gives the exact versions.
- **Machine and date:** a full run on 2026-10-16, on an AWS `c7i.4xlarge` instance started for this run alone. The method line gives the CPU, the operating system, the Go version and the host load before and after.
- **Kit:** revision [`38acec3`](https://github.com/recursive-ai-oss/oneyaml-kit/tree/38acec388e68e7b7e46035c6278ce4cba519d482). The kit's [benchmark notes](https://github.com/recursive-ai-oss/oneyaml-kit/blob/38acec388e68e7b7e46035c6278ce4cba519d482/docs/benchmarks.md) at that revision describe the method in full. This page summarises them.

## What is measured

### Tasks

Each task starts from a state that is built outside the clock, fresh for every iteration. The timed work is what a caller of the library does.

| Task | Timed work, as oneyaml-go does it | The answer is accepted when |
|---|---|---|
| typed load | `Unmarshal` into Go structs | the structs, written as JSON, equal the expected value |
| generic load | `Unmarshal` into `any`, or a `Decoder` for every document of a stream | the value, written as JSON, equals the expected value |
| dump | `Marshal` of the library's own value tree, which was read from the input | two independent reference parsers read the text back to the expected value |
| document parse | `Parse` into a `Document` that keeps comments, spacing and styles | the document holds the expected value and writes the input back byte for byte |
| unchanged emit | `Document.Bytes` on a document that was not edited | the bytes equal the input |
| edit script | a fixed list of replacements and removals with `Document.Set` and `Document.Remove`, then `Document.Bytes` | the bytes equal the expected bytes exactly: only the edited values change, each in its own style |
| patch (RFC 6902) | `Parse`, `Document.Apply` with a JSON Patch, then `Document.Bytes` | the bytes equal the expected bytes, and a second patch with a failing step leaves the text unchanged and returns an error |

The competitors do the same work through their own APIs. For the document tasks that means yaml.v3's and yaml/v4's `Node` and goccy/go-yaml's AST. In the Go harness, every library's patch is timed from text to text: read the file, apply the patch, and write the text back. A library that cannot do a task at all, such as a library with no lossless document, gets an "unsupported" cell, which is not a loss.

### Workloads

The inputs are real files, vendored byte for byte with their source and licence: a GitHub Actions workflow, a Compose file, Helm chart values, two OpenAPI descriptions, a multi-document Kubernetes manifest and a Prometheus configuration. Generated inputs add typed records, Compose files with anchors and merge keys, Kubernetes streams, OpenAPI and Helm-style documents at several sizes, and stress shapes: deep nesting, long scalars of every style, flow-heavy data, anchors and aliases, Unicode, and every scalar style and chomping mode. Two small reference cases edit a block scalar. The kit's notes list them under [Workloads](https://github.com/recursive-ai-oss/oneyaml-kit/blob/38acec388e68e7b7e46035c6278ce4cba519d482/docs/benchmarks.md#workloads).

The expected values do not come from any of the libraries being measured. A generated input and its value come from the same generator. A real file's value is accepted only when two independent YAML 1.2 parsers agree on it. The expected bytes after an edit are exact text substitutions on the input.

### The correctness check before timing

Each library's answer is checked before it is timed. A library gets a number only if its answer was right. A wrong answer is marked "differs" and a refused input is marked "rejects", and neither carries a time or a memory figure. The report of the run lists every such cell with the first difference the check found. See the [correctness guard](https://github.com/recursive-ai-oss/oneyaml-kit/blob/38acec388e68e7b7e46035c6278ce4cba519d482/docs/benchmarks.md#correctness-guard) in the kit's notes.

### Time and memory

The time is the ns/op that Go's `testing.Benchmark` reports, so it is a mean over the iterations of the benchmark. In a full run each cell runs for Go's standard benchtime of one second. The memory figures are the bytes and allocations per operation that `testing.B` reports with `ReportAllocs`. They count what was allocated during the timed work, not the peak size of the heap.

### Reading the tables

Each row compares oneyaml-go with the strongest competitor in that row: the fastest for time, and the one that allocated least for memory. The ratio is our figure divided by theirs, so below 1.0× is better for oneyaml-go. A small gap counts as a tie (the kit's [verdicts](https://github.com/recursive-ai-oss/oneyaml-kit/blob/38acec388e68e7b7e46035c6278ce4cba519d482/docs/benchmarks.md#verdicts) give the bands), and a loss is marked ▼. A † marks a competitor that did less than the full task, for example json-patch, which works on JSON and so cannot keep the text of the file. It is still compared with.

The README summary takes, for each task, the geometric mean over the real-world files that task has. The tables below show every workload.

## Unchanged emit

The unchanged-emit ratio is much further below 1.0× than that of any other row, and it does not measure how fast oneyaml-go renders YAML. A `Document` keeps the text it parsed. `Bytes` on a document that was not edited returns that text without copying it, so its cost does not grow with the size of the file. The competitors build their text again from a `Node` tree or an AST. The task measures what a caller pays to write back a file they did not change, which is a real use, but the gap comes from not rendering, not from rendering faster. For the time it takes to write YAML from values, see the dump row. For the time an edit takes, including writing the result, see the edit-script row.

## Rows with no rival

A row says "no rival" when oneyaml-go gave a correct answer and no competitor did. There is no comparison in such a row, so it is not a win and it is not counted as one. In this run those rows are:

- **document parse, unchanged emit and edit script** on several real files and generated inputs: the competitors' documents did not write the input back byte for byte. They changed blank lines, indentation, quoting or escapes, or the layout of a flow collection.
- **dump** on the typed-records inputs: the text the competitors wrote did not read back to the expected value. For example, a float with a whole-number value came back as an integer, or a string of digits lost its quotes and came back as a number.
- **patch** on the GitHub Actions workflow: the competitors' output differed from the expected bytes, or the patch was refused.

The report of the run gives the first difference for each of these cells.

## Reproducing the run

From a checkout of [oneyaml-kit](https://github.com/recursive-ai-oss/oneyaml-kit) at the revision above, with `KIT_ONEYAML_GO_SRC` set to a checkout of this repository at `v0.3.0`:

```sh
bin/bench run --mode full --lang go
bin/bench readme --results out --library oneyaml-go --readme <oneyaml-go>/README.md --docs <oneyaml-go>/docs --charts docs/img
```

The first command writes the result file, a report with every cell and charts under `out/bench`. The second writes the generated blocks of this page and of the README. A run on any machine other than the kit's rented cloud instance (`bin/bench-cloud full --lang go`), or a quick run, is labelled a smoke result. It shows the layout and the rough picture but it is not a claim, and `bin/bench readme` refuses it unless you pass `--allow-smoke`, which labels every block.

## Results

<!-- generated:bench-full -->
**Time per operation**

| Task | Workload | Input | `oneyaml-go` | `yaml-v3` | `yaml-v4` | `yaml-v2` | `sigs-yaml` | `goccy-go-yaml` | `palantir-yamlpatch` | `palantir-yamlpatch-goccy` | `json-patch` | Ours vs strongest competitor |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| typed load | `gha-workflow` | 9.43 KiB | 161 µs | 438 µs | 434 µs | 326 µs | differs [13] | 1.1 ms | unsupported | unsupported | unsupported | 0.49× of `yaml-v2` ▲ win |
| typed load | `records-4k` | 4.14 KiB | 112 µs | 360 µs | 347 µs | 261 µs | 470 µs | 761 µs | unsupported | unsupported | unsupported | 0.43× of `yaml-v2` ▲ win |
| typed load | `records-64k` | 64.2 KiB | 1.78 ms | 5.74 ms | 5.55 ms | 4.44 ms | 7.8 ms | 12.3 ms | unsupported | unsupported | unsupported | 0.40× of `yaml-v2` ▲ win |
| typed load | `records-1m` | 1 MiB | 26 ms | 84.5 ms | 81.1 ms | 61.5 ms | 110 ms | 181 ms | unsupported | unsupported | unsupported | 0.42× of `yaml-v2` ▲ win |
| typed load | `records-8m` | 8 MiB | 212 ms | 695 ms | 689 ms | 520 ms | 923 ms | 1.76 s | unsupported | unsupported | unsupported | 0.41× of `yaml-v2` ▲ win |
| generic load | `compose-wordpress` | 815 B | 9.04 µs | 37 µs | 36.7 µs | 28.9 µs | differs [16] | 56.5 µs | unsupported | unsupported | unsupported | 0.31× of `yaml-v2` ▲ win |
| generic load | `gha-workflow` | 9.43 KiB | 124 µs | 456 µs | 454 µs | differs [18] | differs [13] | 831 µs | unsupported | unsupported | unsupported | 0.27× of `yaml-v4` ▲ win |
| generic load | `helm-ingress-nginx` | 52.4 KiB | 271 µs | 1.24 ms | 1.24 ms | 760 µs | differs [16] | 2.22 ms | unsupported | unsupported | unsupported | 0.36× of `yaml-v2` ▲ win |
| generic load | `k8s-ingress-nginx-multi` | 16 KiB | 349 µs | 976 µs | 971 µs | 849 µs | differs [15] | 1.81 ms | unsupported | unsupported | unsupported | 0.41× of `yaml-v2` ▲ win |
| generic load | `openapi-api-with-examples` | 6.75 KiB | 59.6 µs | 200 µs | 202 µs | 163 µs | 239 µs | 349 µs | unsupported | unsupported | unsupported | 0.36× of `yaml-v2` ▲ win |
| generic load | `openapi-petstore` | 5.34 KiB | 67.7 µs | 238 µs | 238 µs | 192 µs | 302 µs | 496 µs | unsupported | unsupported | unsupported | 0.35× of `yaml-v2` ▲ win |
| generic load | `prometheus-config` | 1.1 KiB | 8.39 µs | 39.4 µs | 39.2 µs | 27.1 µs | 37.6 µs | 55.9 µs | unsupported | unsupported | unsupported | 0.31× of `yaml-v2` ▲ win |
| generic load | `reference-a` | 51 B | 1.51 µs | 7.71 µs | 7.75 µs | 5.38 µs | differs [16] | 10.3 µs | unsupported | unsupported | unsupported | 0.28× of `yaml-v2` ▲ win |
| generic load | `reference-b` | 51 B | 1.51 µs | 7.6 µs | 7.74 µs | 5.47 µs | differs [16] | 10.4 µs | unsupported | unsupported | unsupported | 0.28× of `yaml-v2` ▲ win |
| generic load | `records-4k` | 4.14 KiB | 92.4 µs | 389 µs | 377 µs | 302 µs | differs [16] | 623 µs | unsupported | unsupported | unsupported | 0.31× of `yaml-v2` ▲ win |
| generic load | `records-64k` | 64.2 KiB | 1.47 ms | 6.37 ms | 6.12 ms | 4.97 ms | differs [16] | 10.1 ms | unsupported | unsupported | unsupported | 0.30× of `yaml-v2` ▲ win |
| generic load | `records-1m` | 1 MiB | 22.4 ms | 93.6 ms | 89.2 ms | 71.4 ms | differs [16] | 151 ms | unsupported | unsupported | unsupported | 0.31× of `yaml-v2` ▲ win |
| generic load | `records-8m` | 8 MiB | 183 ms | 759 ms | 733 ms | 618 ms | differs [16] | 1.48 s | unsupported | unsupported | unsupported | 0.30× of `yaml-v2` ▲ win |
| generic load | `compose-merge-2k` | 2.14 KiB | 63.4 µs | 210 µs (merged) | 215 µs (merged) | 158 µs (merged) | differs [16] | 333 µs (merged) | unsupported | unsupported | unsupported | 0.40× of `yaml-v2` ▲ win |
| generic load | `compose-merge-64k` | 64.1 KiB | 1.94 ms | 6.52 ms (merged) | 6.66 ms (merged) | 4.73 ms (merged) | differs [16] | 10.2 ms (merged) | unsupported | unsupported | unsupported | 0.41× of `yaml-v2` ▲ win |
| generic load | `compose-merge-1m` | 1 MiB | 27.9 ms | 150 ms (merged) | 151 ms (merged) | 68.7 ms (merged) | differs [16] | 191 ms (merged) | unsupported | unsupported | unsupported | 0.41× of `yaml-v2` ▲ win |
| generic load | `k8s-multi-16k` | 16.4 KiB | 550 µs | 1.55 ms | 1.53 ms | 1.21 ms | differs [15] | 3.21 ms | unsupported | unsupported | unsupported | 0.46× of `yaml-v2` ▲ win |
| generic load | `k8s-multi-1m` | 1 MiB | 29.9 ms | 93 ms | 92.6 ms | 73.8 ms | differs [15] | 187 ms | unsupported | unsupported | unsupported | 0.40× of `yaml-v2` ▲ win |
| generic load | `openapi-100k` | 101 KiB | 2.07 ms | 6.92 ms | 6.9 ms | 5.65 ms | 8.86 ms | 12.8 ms | unsupported | unsupported | unsupported | 0.37× of `yaml-v2` ▲ win |
| generic load | `openapi-8m` | 8 MiB | 147 ms | 1.11 s | 1.13 s | 391 ms | 650 ms | 1.4 s | unsupported | unsupported | unsupported | 0.38× of `yaml-v2` ▲ win |
| generic load | `helm-values-64k` | 64 KiB | 850 µs | 4.07 ms | 3.96 ms | 2.6 ms | differs [16] | 6.88 ms | unsupported | unsupported | unsupported | 0.33× of `yaml-v2` ▲ win |
| generic load | `helm-values-1m` | 1 MiB | 13.3 ms | 71.6 ms | 71.1 ms | 39.1 ms | differs [16] | 107 ms | unsupported | unsupported | unsupported | 0.34× of `yaml-v2` ▲ win |
| generic load | `deep-block` | 5.23 KiB | 55.6 µs | 170 µs | 173 µs | 137 µs | differs [16] | 298 µs | unsupported | unsupported | unsupported | 0.41× of `yaml-v2` ▲ win |
| generic load | `deep-flow` | 202 B | 17.9 µs | 108 µs | 105 µs | 97.2 µs | differs [16] | 107 µs | unsupported | unsupported | unsupported | 0.18× of `yaml-v2` ▲ win |
| generic load | `wide-map-64k` | 64 KiB | 1.54 ms | 22.3 ms | 22.7 ms | 4.52 ms | differs [16] | 34.4 ms | unsupported | unsupported | unsupported | 0.34× of `yaml-v2` ▲ win |
| generic load | `wide-map-1m` | 1 MiB | 20.4 ms | 9.63 s | 8.55 s | 65 ms | differs [16] | 2.9 s | unsupported | unsupported | unsupported | 0.31× of `yaml-v2` ▲ win |
| generic load | `long-scalars-256k` | 261 KiB | 930 µs | 4.29 ms | 4.36 ms | 3.97 ms | 6.82 ms | 11.9 ms | unsupported | unsupported | unsupported | 0.23× of `yaml-v2` ▲ win |
| generic load | `flow-heavy-64k` | 64 KiB | 2.8 ms | 10.2 ms | 9.94 ms | 7.33 ms | differs [16] | 17.7 ms | unsupported | unsupported | unsupported | 0.38× of `yaml-v2` ▲ win |
| generic load | `anchors-32k` | 32 KiB | 3.03 ms | 7.62 ms | 8.19 ms | differs [19] | differs [16] | 8.44 ms | unsupported | unsupported | unsupported | 0.40× of `yaml-v3` ▲ win |
| generic load | `unicode-32k` | 32 KiB | 398 µs | 1.32 ms | 1.29 ms | 1.15 ms | 1.53 ms | 2.23 ms | unsupported | unsupported | unsupported | 0.35× of `yaml-v2` ▲ win |
| generic load | `scalar-styles-32k` | 32 KiB | 511 µs | 1.78 ms | 1.76 ms | 1.41 ms | 2.06 ms | 5.24 ms | unsupported | unsupported | unsupported | 0.36× of `yaml-v2` ▲ win |
| dump | `compose-wordpress` | 815 B | 2.97 µs | 37.9 µs | 42.6 µs | 31.3 µs | 84.9 µs | 61.1 µs | unsupported | unsupported | unsupported | 0.09× of `yaml-v2` ▲ win |
| dump | `gha-workflow` | 9.43 KiB | 42.5 µs | 424 µs | 432 µs | differs [18] | differs [13] | 753 µs | unsupported | unsupported | unsupported | 0.10× of `yaml-v3` ▲ win |
| dump | `helm-ingress-nginx` | 52.4 KiB | 70.4 µs | 735 µs | 783 µs | 654 µs | 1.24 ms | 1.12 ms | unsupported | unsupported | unsupported | 0.11× of `yaml-v2` ▲ win |
| dump | `openapi-api-with-examples` | 6.75 KiB | 17.3 µs | 221 µs | 225 µs | 177 µs | 361 µs | 332 µs | unsupported | unsupported | unsupported | 0.10× of `yaml-v2` ▲ win |
| dump | `openapi-petstore` | 5.34 KiB | 27.3 µs | 258 µs | 272 µs | 219 µs | 464 µs | 414 µs | unsupported | unsupported | unsupported | 0.12× of `yaml-v2` ▲ win |
| dump | `prometheus-config` | 1.1 KiB | 1.79 µs | 34.1 µs | 39.4 µs | 27.8 µs | 80.3 µs | 48.3 µs | unsupported | unsupported | unsupported | 0.06× of `yaml-v2` ▲ win |
| dump | `reference-a` | 51 B | 331 ns | 4.43 µs | 5.31 µs | 3.73 µs | 11.9 µs | 8.12 µs | unsupported | unsupported | unsupported | 0.09× of `yaml-v2` ▲ win |
| dump | `reference-b` | 51 B | 333 ns | 4.42 µs | 5.33 µs | 3.72 µs | 11.8 µs | 8.13 µs | unsupported | unsupported | unsupported | 0.09× of `yaml-v2` ▲ win |
| dump | `records-4k` | 4.14 KiB | 28.4 µs | differs [22] | differs [24] | differs [20] | differs [17] | differs [3] | unsupported | unsupported | unsupported | no rival |
| dump | `records-64k` | 64.2 KiB | 443 µs | differs [22] | differs [24] | differs [20] | differs [17] | differs [3] | unsupported | unsupported | unsupported | no rival |
| dump | `records-1m` | 1 MiB | 8.58 ms | differs [22] | differs [24] | differs [20] | differs [17] | differs [3] | unsupported | unsupported | unsupported | no rival |
| dump | `records-8m` | 8 MiB | 74.6 ms | differs [22] | differs [24] | differs [20] | differs [17] | differs [3] | unsupported | unsupported | unsupported | no rival |
| dump | `compose-merge-2k` | 2.14 KiB | 24.8 µs | 275 µs (merged) | 291 µs (merged) | 231 µs (merged) | 482 µs (merged) | 497 µs (merged) | unsupported | unsupported | unsupported | 0.11× of `yaml-v2` ▲ win |
| dump | `compose-merge-64k` | 64.1 KiB | 816 µs | 10.3 ms (merged) | 10.8 ms (merged) | 8.64 ms (merged) | 14.1 ms (merged) | 14.3 ms (merged) | unsupported | unsupported | unsupported | 0.09× of `yaml-v2` ▲ win |
| dump | `compose-merge-1m` | 1 MiB | 17.3 ms | 191 ms (merged) | 200 ms (merged) | 161 ms (merged) | 261 ms (merged) | 239 ms (merged) | unsupported | unsupported | unsupported | 0.11× of `yaml-v2` ▲ win |
| dump | `openapi-100k` | 101 KiB | 698 µs | 7.97 ms | 8.33 ms | 6.56 ms | 11.7 ms | 12.3 ms | unsupported | unsupported | unsupported | 0.11× of `yaml-v2` ▲ win |
| dump | `openapi-8m` | 8 MiB | 85.9 ms | 1.19 s | 1.19 s | 722 ms | 1.29 s | 1.18 s | unsupported | unsupported | unsupported | 0.12× of `yaml-v2` ▲ win |
| dump | `helm-values-64k` | 64 KiB | 261 µs | 3.13 ms | 3.35 ms | 2.68 ms | 4.97 ms | 5.21 ms | unsupported | unsupported | unsupported | 0.10× of `yaml-v2` ▲ win |
| dump | `helm-values-1m` | 1 MiB | 4.2 ms | 72.2 ms | 75.2 ms | 57.2 ms | 99.7 ms | 90 ms | unsupported | unsupported | unsupported | 0.07× of `yaml-v2` ▲ win |
| dump | `deep-block` | 5.23 KiB | 19.8 µs | 194 µs | 179 µs | 146 µs | 294 µs | 391 µs | unsupported | unsupported | unsupported | 0.14× of `yaml-v2` ▲ win |
| dump | `deep-flow` | 202 B | 1.48 µs | 189 µs | 160 µs | 74.4 µs | 228 µs | 86 µs | unsupported | unsupported | unsupported | 0.02× of `yaml-v2` ▲ win |
| dump | `wide-map-64k` | 64 KiB | 812 µs | 7.67 ms | 7.77 ms | 7.38 ms | 11.9 ms | 12.2 ms | unsupported | unsupported | unsupported | 0.11× of `yaml-v2` ▲ win |
| dump | `wide-map-1m` | 1 MiB | 19.7 ms | 165 ms | 170 ms | 161 ms | 256 ms | 236 ms | unsupported | unsupported | unsupported | 0.12× of `yaml-v2` ▲ win |
| dump | `long-scalars-256k` | 261 KiB | 1.24 ms | 4.25 ms | 4.33 ms | 4.18 ms | 8.98 ms | 1.57 ms | unsupported | unsupported | unsupported | 0.79× of `goccy-go-yaml` ▲ win |
| dump | `flow-heavy-64k` | 64 KiB | 566 µs | 10.8 ms | 12.1 ms | 8.58 ms | 16.2 ms | 14.8 ms | unsupported | unsupported | unsupported | 0.07× of `yaml-v2` ▲ win |
| dump | `anchors-32k` | 32 KiB | 1.05 ms | 21.2 ms | 24 ms | differs [19] | differs [14] | 27.8 ms | unsupported | unsupported | unsupported | 0.05× of `yaml-v3` ▲ win |
| dump | `unicode-32k` | 32 KiB | 96.9 µs | 1.13 ms | 1.16 ms | 928 µs | 1.87 ms | 1.85 ms | unsupported | unsupported | unsupported | 0.10× of `yaml-v2` ▲ win |
| dump | `scalar-styles-32k` | 32 KiB | 175 µs | 1.97 ms | 2.04 ms | 1.7 ms | 3.23 ms | 4.27 ms | unsupported | unsupported | unsupported | 0.10× of `yaml-v2` ▲ win |
| document parse | `compose-wordpress` | 815 B | 7.96 µs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| document parse | `gha-workflow` | 9.43 KiB | 78.8 µs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| document parse | `helm-ingress-nginx` | 52.4 KiB | 237 µs | 1.12 ms | differs [23] | unsupported | unsupported | 2.31 ms | unsupported | unsupported | unsupported | 0.21× of `yaml-v3` ▲ win |
| document parse | `k8s-ingress-nginx-multi` | 16 KiB | 166 µs | differs [21] | 720 µs | unsupported | unsupported | 1.5 ms | unsupported | unsupported | unsupported | 0.23× of `yaml-v4` ▲ win |
| document parse | `openapi-api-with-examples` | 6.75 KiB | 43.7 µs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| document parse | `openapi-petstore` | 5.34 KiB | 42.8 µs | 190 µs | differs [23] | unsupported | unsupported | 417 µs | unsupported | unsupported | unsupported | 0.23× of `yaml-v3` ▲ win |
| document parse | `prometheus-config` | 1.1 KiB | 8.1 µs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| document parse | `reference-a` | 51 B | 1.64 µs | differs [21] | differs [23] | unsupported | unsupported | 8.56 µs | unsupported | unsupported | unsupported | 0.19× of `goccy-go-yaml` ▲ win |
| document parse | `reference-b` | 51 B | 1.64 µs | differs [21] | differs [23] | unsupported | unsupported | 8.68 µs | unsupported | unsupported | unsupported | 0.19× of `goccy-go-yaml` ▲ win |
| document parse | `records-4k` | 4.14 KiB | 60.2 µs | 286 µs | differs [23] | unsupported | unsupported | differs [5] | unsupported | unsupported | unsupported | 0.21× of `yaml-v3` ▲ win |
| document parse | `records-64k` | 64.2 KiB | 930 µs | 4.72 ms | differs [23] | unsupported | unsupported | differs [5] | unsupported | unsupported | unsupported | 0.20× of `yaml-v3` ▲ win |
| document parse | `records-1m` | 1 MiB | 14 ms | 65.7 ms | differs [23] | unsupported | unsupported | differs [5] | unsupported | unsupported | unsupported | 0.21× of `yaml-v3` ▲ win |
| document parse | `records-8m` | 8 MiB | 120 ms | 560 ms | differs [23] | unsupported | unsupported | differs [5] | unsupported | unsupported | unsupported | 0.21× of `yaml-v3` ▲ win |
| document parse | `compose-merge-2k` | 2.14 KiB | 28.9 µs | differs [21] | 119 µs (merged) | unsupported | unsupported | 240 µs (merged) | unsupported | unsupported | unsupported | 0.24× of `yaml-v4` ▲ win |
| document parse | `compose-merge-64k` | 64.1 KiB | 784 µs | differs [21] | 3.51 ms (merged) | unsupported | unsupported | 7.64 ms (merged) | unsupported | unsupported | unsupported | 0.22× of `yaml-v4` ▲ win |
| document parse | `compose-merge-1m` | 1 MiB | 11.8 ms | differs [21] | 49.6 ms (merged) | unsupported | unsupported | 147 ms (merged) | unsupported | unsupported | unsupported | 0.24× of `yaml-v4` ▲ win |
| document parse | `k8s-multi-16k` | 16.4 KiB | 281 µs | differs [21] | differs [23] | unsupported | unsupported | 2.53 ms | unsupported | unsupported | unsupported | 0.11× of `goccy-go-yaml` ▲ win |
| document parse | `k8s-multi-1m` | 1 MiB | 16.1 ms | differs [21] | differs [23] | unsupported | unsupported | 149 ms | unsupported | unsupported | unsupported | 0.11× of `goccy-go-yaml` ▲ win |
| document parse | `openapi-100k` | 101 KiB | 1.21 ms | 5.34 ms | differs [23] | unsupported | unsupported | 9.93 ms | unsupported | unsupported | unsupported | 0.23× of `yaml-v3` ▲ win |
| document parse | `openapi-8m` | 8 MiB | 100 ms | 379 ms | differs [23] | unsupported | unsupported | 1.13 s | unsupported | unsupported | unsupported | 0.26× of `yaml-v3` ▲ win |
| document parse | `helm-values-64k` | 64 KiB | 592 µs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| document parse | `helm-values-1m` | 1 MiB | 9.07 ms | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| document parse | `deep-block` | 5.23 KiB | 39.3 µs | differs [21] | differs [23] | unsupported | unsupported | 217 µs | unsupported | unsupported | unsupported | 0.18× of `goccy-go-yaml` ▲ win |
| document parse | `deep-flow` | 202 B | 24.7 µs | 84.3 µs | differs [23] | unsupported | unsupported | 85.5 µs | unsupported | unsupported | unsupported | 0.29× of `yaml-v3` ▲ win |
| document parse | `wide-map-64k` | 64 KiB | 894 µs | 4.56 ms | 4.21 ms | unsupported | unsupported | 32.9 ms | unsupported | unsupported | unsupported | 0.21× of `yaml-v4` ▲ win |
| document parse | `wide-map-1m` | 1 MiB | 13.1 ms | 64.6 ms | 58.9 ms | unsupported | unsupported | 2.99 s | unsupported | unsupported | unsupported | 0.22× of `yaml-v4` ▲ win |
| document parse | `long-scalars-256k` | 261 KiB | 560 µs | differs [21] | differs [23] | unsupported | unsupported | 11.8 ms | unsupported | unsupported | unsupported | 0.05× of `goccy-go-yaml` ▲ win |
| document parse | `flow-heavy-64k` | 64 KiB | 1.9 ms | 7.1 ms | 6.48 ms | unsupported | unsupported | 14.8 ms | unsupported | unsupported | unsupported | 0.29× of `yaml-v4` ▲ win |
| document parse | `anchors-32k` | 32 KiB | 603 µs | 2.41 ms | differs [23] | unsupported | unsupported | 6.26 ms | unsupported | unsupported | unsupported | 0.25× of `yaml-v3` ▲ win |
| document parse | `unicode-32k` | 32 KiB | 257 µs | differs [21] | differs [23] | unsupported | unsupported | differs [4] | unsupported | unsupported | unsupported | no rival |
| document parse | `scalar-styles-32k` | 32 KiB | 308 µs | differs [21] | differs [23] | unsupported | unsupported | 4.48 ms | unsupported | unsupported | unsupported | 0.07× of `goccy-go-yaml` ▲ win |
| unchanged emit | `compose-wordpress` | 815 B | 6.97 ns | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| unchanged emit | `gha-workflow` | 9.43 KiB | 6.96 ns | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| unchanged emit | `helm-ingress-nginx` | 52.4 KiB | 7.4 ns | 918 µs | differs [23] | unsupported | unsupported | 659 µs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `k8s-ingress-nginx-multi` | 16 KiB | 7.16 ns | differs [21] | 835 µs | unsupported | unsupported | 367 µs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `openapi-api-with-examples` | 6.75 KiB | 6.65 ns | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| unchanged emit | `openapi-petstore` | 5.34 KiB | 6.94 ns | 228 µs | differs [23] | unsupported | unsupported | 69.4 µs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `prometheus-config` | 1.1 KiB | 6.99 ns | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| unchanged emit | `reference-a` | 51 B | 6.97 ns | differs [21] | differs [23] | unsupported | unsupported | 1.36 µs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `reference-b` | 51 B | 6.96 ns | differs [21] | differs [23] | unsupported | unsupported | 1.85 µs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `records-4k` | 4.14 KiB | 6.97 ns | 361 µs | differs [23] | unsupported | unsupported | differs [5] | unsupported | unsupported | unsupported | <0.01× of `yaml-v3` ▲ win |
| unchanged emit | `records-64k` | 64.2 KiB | 7 ns | 6.68 ms | differs [23] | unsupported | unsupported | differs [5] | unsupported | unsupported | unsupported | <0.01× of `yaml-v3` ▲ win |
| unchanged emit | `records-1m` | 1 MiB | 7.14 ns | 98.2 ms | differs [23] | unsupported | unsupported | differs [5] | unsupported | unsupported | unsupported | <0.01× of `yaml-v3` ▲ win |
| unchanged emit | `records-8m` | 8 MiB | 7.24 ns | 877 ms | differs [23] | unsupported | unsupported | differs [5] | unsupported | unsupported | unsupported | <0.01× of `yaml-v3` ▲ win |
| unchanged emit | `compose-merge-2k` | 2.14 KiB | 7.19 ns | differs [21] | 115 µs | unsupported | unsupported | 38.5 µs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `compose-merge-64k` | 64.1 KiB | 7.21 ns | differs [21] | 4.76 ms | unsupported | unsupported | 1.14 ms | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `compose-merge-1m` | 1 MiB | 7.21 ns | differs [21] | 92.4 ms | unsupported | unsupported | 17.3 ms | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `k8s-multi-16k` | 16.4 KiB | 7.71 ns | differs [21] | differs [23] | unsupported | unsupported | 451 µs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `k8s-multi-1m` | 1 MiB | 7.28 ns | differs [21] | differs [23] | unsupported | unsupported | 25.6 ms | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `openapi-100k` | 101 KiB | 7.23 ns | 8.06 ms | differs [23] | unsupported | unsupported | 1.92 ms | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `openapi-8m` | 8 MiB | 7.18 ns | 826 ms | differs [23] | unsupported | unsupported | 170 ms | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `helm-values-64k` | 64 KiB | 7.2 ns | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| unchanged emit | `helm-values-1m` | 1 MiB | 7.17 ns | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| unchanged emit | `deep-block` | 5.23 KiB | 7.15 ns | differs [21] | differs [23] | unsupported | unsupported | 181 µs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `deep-flow` | 202 B | 7.17 ns | 74.6 µs | differs [23] | unsupported | unsupported | 12.4 µs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `wide-map-64k` | 64 KiB | 7.18 ns | 5.67 ms | 4.39 ms | unsupported | unsupported | 982 µs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `wide-map-1m` | 1 MiB | 7.58 ns | 87.2 ms | 87.1 ms | unsupported | unsupported | 13.9 ms | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `long-scalars-256k` | 261 KiB | 7.65 ns | differs [21] | differs [23] | unsupported | unsupported | 616 µs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `flow-heavy-64k` | 64 KiB | 7.68 ns | 12 ms | 9.74 ms | unsupported | unsupported | 2.06 ms | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `anchors-32k` | 32 KiB | 7.67 ns | 2.64 ms | differs [23] | unsupported | unsupported | 1.21 ms | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `unicode-32k` | 32 KiB | 7.66 ns | differs [21] | differs [23] | unsupported | unsupported | differs [4] | unsupported | unsupported | unsupported | no rival |
| unchanged emit | `scalar-styles-32k` | 32 KiB | 7.67 ns | differs [21] | differs [23] | unsupported | unsupported | 997 µs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| edit script | `compose-wordpress` | 815 B | 11.8 µs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| edit script | `gha-workflow` | 9.43 KiB | 13.5 µs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| edit script | `helm-ingress-nginx` | 52.4 KiB | 19.1 µs | 826 µs | differs [23] | unsupported | unsupported | 679 µs | unsupported | unsupported | unsupported | 0.03× of `goccy-go-yaml` ▲ win |
| edit script | `openapi-api-with-examples` | 6.75 KiB | 2.11 µs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| edit script | `openapi-petstore` | 5.34 KiB | 18.4 µs | 188 µs | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | 0.10× of `yaml-v3` ▲ win |
| edit script | `prometheus-config` | 1.1 KiB | 1.98 µs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| edit script | `reference-a` | 51 B | 2.61 µs | differs [21] | differs [23] | unsupported | unsupported | 7.11 µs | unsupported | unsupported | unsupported | 0.37× of `goccy-go-yaml` ▲ win |
| edit script | `reference-b` | 51 B | 2.64 µs | differs [21] | differs [23] | unsupported | unsupported | differs [1] | unsupported | unsupported | unsupported | no rival |
| edit script | `records-4k` | 4.14 KiB | 8.48 µs | 305 µs | differs [23] | unsupported | unsupported | differs [5] | unsupported | unsupported | unsupported | 0.03× of `yaml-v3` ▲ win |
| edit script | `records-64k` | 64.2 KiB | 67.1 µs | 5.65 ms | differs [23] | unsupported | unsupported | differs [5] | unsupported | unsupported | unsupported | 0.01× of `yaml-v3` ▲ win |
| edit script | `records-1m` | 1 MiB | 2.01 ms | 107 ms | differs [23] | unsupported | unsupported | differs [5] | unsupported | unsupported | unsupported | 0.02× of `yaml-v3` ▲ win |
| edit script | `records-8m` | 8 MiB | 21.5 ms | 1.09 s | differs [23] | unsupported | unsupported | differs [5] | unsupported | unsupported | unsupported | 0.02× of `yaml-v3` ▲ win |
| edit script | `compose-merge-2k` | 2.14 KiB | 7.7 µs | differs [21] | 122 µs | unsupported | unsupported | 53.2 µs | unsupported | unsupported | unsupported | 0.14× of `goccy-go-yaml` ▲ win |
| edit script | `compose-merge-64k` | 64.1 KiB | 70.1 µs | differs [21] | 4.89 ms | unsupported | unsupported | 1.55 ms | unsupported | unsupported | unsupported | 0.05× of `goccy-go-yaml` ▲ win |
| edit script | `compose-merge-1m` | 1 MiB | 1.68 ms | differs [21] | 90.1 ms | unsupported | unsupported | 25.2 ms | unsupported | unsupported | unsupported | 0.07× of `goccy-go-yaml` ▲ win |
| edit script | `openapi-100k` | 101 KiB | 92.6 µs | 6.83 ms | differs [23] | unsupported | unsupported | 2.37 ms | unsupported | unsupported | unsupported | 0.04× of `goccy-go-yaml` ▲ win |
| edit script | `openapi-8m` | 8 MiB | 15.8 ms | 1.03 s | differs [23] | unsupported | unsupported | 189 ms | unsupported | unsupported | unsupported | 0.08× of `goccy-go-yaml` ▲ win |
| edit script | `helm-values-64k` | 64 KiB | 36.9 µs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| edit script | `helm-values-1m` | 1 MiB | 733 µs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| edit script | `wide-map-64k` | 64 KiB | 92.1 µs | 4.8 ms | 4.44 ms | unsupported | unsupported | 1.15 ms | unsupported | unsupported | unsupported | 0.08× of `goccy-go-yaml` ▲ win |
| edit script | `wide-map-1m` | 1 MiB | 2.27 ms | 92.9 ms | 84.6 ms | unsupported | unsupported | 17.4 ms | unsupported | unsupported | unsupported | 0.13× of `goccy-go-yaml` ▲ win |
| patch (RFC 6902) | `compose-wordpress` | 815 B | 20.9 µs | unsupported | unsupported | unsupported | unsupported | unsupported | differs [9] | differs [11] | 142 µs (value-only) † | 0.15× of `json-patch` † (text API, value-only) ▲ win |
| patch (RFC 6902) | `gha-workflow` | 9.43 KiB | 99.6 µs | unsupported | unsupported | unsupported | unsupported | unsupported | differs [9] | differs [11] | rejects [6] | no rival |
| patch (RFC 6902) | `reference-a` | 51 B | 4.42 µs | unsupported | unsupported | unsupported | unsupported | unsupported | differs [9] | 12.8 µs † | 21 µs (value-only) † | 0.34× of `palantir-yamlpatch-goccy` † (text API) ▲ win |
| patch (RFC 6902) | `reference-b` | 51 B | 4.43 µs | unsupported | unsupported | unsupported | unsupported | unsupported | differs [8] | differs [10] | 21 µs (value-only) † | 0.21× of `json-patch` † (text API, value-only) ▲ win |
| patch (RFC 6902) | `records-4k` | 4.14 KiB | 72.5 µs | unsupported | unsupported | unsupported | unsupported | unsupported | 686 µs † | differs [12] | differs [7] | 0.11× of `palantir-yamlpatch` † (text API) ▲ win |
| patch (RFC 6902) | `records-64k` | 64.2 KiB | 1.04 ms | unsupported | unsupported | unsupported | unsupported | unsupported | 11.8 ms † | differs [12] | differs [7] | 0.09× of `palantir-yamlpatch` † (text API) ▲ win |
| patch (RFC 6902) | `records-1m` | 1 MiB | 16.7 ms | unsupported | unsupported | unsupported | unsupported | unsupported | 190 ms † | differs [12] | differs [7] | 0.09× of `palantir-yamlpatch` † (text API) ▲ win |
| patch (RFC 6902) | `records-8m` | 8 MiB | 149 ms | unsupported | unsupported | unsupported | unsupported | unsupported | 1.4 s † | differs [12] | differs [7] | 0.11× of `palantir-yamlpatch` † (text API) ▲ win |

**Peak / allocated heap per operation**

| Task | Workload | Input | `oneyaml-go` | `yaml-v3` | `yaml-v4` | `yaml-v2` | `sigs-yaml` | `goccy-go-yaml` | `palantir-yamlpatch` | `palantir-yamlpatch-goccy` | `json-patch` | Ours vs strongest competitor |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| typed load | `gha-workflow` | 9.43 KiB | 81.3 KiB in 1,238 allocs | 194 KiB in 3,908 allocs | 196 KiB in 4,027 allocs | 144 KiB in 3,242 allocs | differs [13] | 797 KiB in 14,525 allocs | unsupported | unsupported | unsupported | 0.57× of `yaml-v2` ▲ win |
| typed load | `records-4k` | 4.14 KiB | 58.8 KiB in 788 allocs | 149 KiB in 3,360 allocs | 151 KiB in 3,436 allocs | 113 KiB in 2,715 allocs | 208 KiB in 4,615 allocs | 583 KiB in 11,571 allocs | unsupported | unsupported | unsupported | 0.52× of `yaml-v2` ▲ win |
| typed load | `records-64k` | 64.2 KiB | 841 KiB in 11,916 allocs | 2.15 MiB in 51,237 allocs | 2.17 MiB in 52,357 allocs | 1.64 MiB in 41,182 allocs | 3.02 MiB in 70,076 allocs | 9.37 MiB in 177,037 allocs | unsupported | unsupported | unsupported | 0.50× of `yaml-v2` ▲ win |
| typed load | `records-1m` | 1 MiB | 13.8 MiB in 187,204 allocs | 33.7 MiB in 812,225 allocs | 34 MiB in 829,756 allocs | 25.6 MiB in 650,360 allocs | 48.5 MiB in 1,103,395 allocs | 147 MiB in 2,790,099 allocs | unsupported | unsupported | unsupported | 0.54× of `yaml-v2` ▲ win |
| typed load | `records-8m` | 8 MiB | 116 MiB in 1,479,203 allocs | 267 MiB in 6,422,918 allocs | 269 MiB in 6,561,502 allocs | 203 MiB in 5,141,571 allocs | 391 MiB in 8,722,302 allocs | 1.17 GiB in 22,057,716 allocs | unsupported | unsupported | unsupported | 0.57× of `yaml-v2` ▲ win |
| generic load | `compose-wordpress` | 815 B | 2.69 KiB in 55 allocs | 20.9 KiB in 341 allocs | 21.2 KiB in 354 allocs | 15.8 KiB in 295 allocs | differs [16] | 50.7 KiB in 931 allocs | unsupported | unsupported | unsupported | 0.17× of `yaml-v2` ▲ win |
| generic load | `gha-workflow` | 9.43 KiB | 45 KiB in 645 allocs | 216 KiB in 4,249 allocs | 218 KiB in 4,368 allocs | differs [18] | differs [13] | 746 KiB in 12,689 allocs | unsupported | unsupported | unsupported | 0.21× of `yaml-v3` ▲ win |
| generic load | `helm-ingress-nginx` | 52.4 KiB | 41.4 KiB in 419 allocs | 558 KiB in 6,234 allocs | 561 KiB in 6,422 allocs | 218 KiB in 4,234 allocs | differs [16] | 1.67 MiB in 24,141 allocs | unsupported | unsupported | unsupported | 0.19× of `yaml-v2` ▲ win |
| generic load | `k8s-ingress-nginx-multi` | 16 KiB | 202 KiB in 1,380 allocs | 403 KiB in 8,670 allocs | 402 KiB in 8,791 allocs | 335 KiB in 7,603 allocs | differs [15] | 1.36 MiB in 25,149 allocs | unsupported | unsupported | unsupported | 0.60× of `yaml-v2` ▲ win |
| generic load | `openapi-api-with-examples` | 6.75 KiB | 20.8 KiB in 259 allocs | 87.9 KiB in 1,475 allocs | 125 KiB in 1,540 allocs | 75.4 KiB in 1,371 allocs | 133 KiB in 2,158 allocs | 348 KiB in 5,219 allocs | unsupported | unsupported | unsupported | 0.28× of `yaml-v2` ▲ win |
| generic load | `openapi-petstore` | 5.34 KiB | 28.7 KiB in 313 allocs | 120 KiB in 2,123 allocs | 122 KiB in 2,208 allocs | 102 KiB in 1,842 allocs | 189 KiB in 2,858 allocs | 451 KiB in 7,440 allocs | unsupported | unsupported | unsupported | 0.28× of `yaml-v2` ▲ win |
| generic load | `prometheus-config` | 1.1 KiB | 3.06 KiB in 39 allocs | 27.5 KiB in 319 allocs | 27.7 KiB in 334 allocs | 16 KiB in 240 allocs | 24.6 KiB in 369 allocs | 51.2 KiB in 825 allocs | unsupported | unsupported | unsupported | 0.19× of `yaml-v2` ▲ win |
| generic load | `reference-a` | 51 B | 481 B in 7 allocs | 8.75 KiB in 74 allocs | 8.85 KiB in 78 allocs | 6.2 KiB in 61 allocs | differs [16] | 9.42 KiB in 213 allocs | unsupported | unsupported | unsupported | 0.08× of `yaml-v2` ▲ win |
| generic load | `reference-b` | 51 B | 481 B in 7 allocs | 8.75 KiB in 74 allocs | 8.85 KiB in 78 allocs | 6.2 KiB in 61 allocs | differs [16] | 9.41 KiB in 213 allocs | unsupported | unsupported | unsupported | 0.08× of `yaml-v2` ▲ win |
| generic load | `records-4k` | 4.14 KiB | 22.8 KiB in 439 allocs | 170 KiB in 3,896 allocs | 171 KiB in 3,972 allocs | 134 KiB in 3,251 allocs | differs [16] | 556 KiB in 10,786 allocs | unsupported | unsupported | unsupported | 0.17× of `yaml-v2` ▲ win |
| generic load | `records-64k` | 64.2 KiB | 351 KiB in 6,629 allocs | 2.46 MiB in 59,521 allocs | 2.48 MiB in 60,640 allocs | 1.94 MiB in 49,465 allocs | differs [16] | 8.95 MiB in 164,851 allocs | unsupported | unsupported | unsupported | 0.18× of `yaml-v2` ▲ win |
| generic load | `records-1m` | 1 MiB | 5.42 MiB in 107,985 allocs | 38.6 MiB in 942,219 allocs | 38.9 MiB in 959,749 allocs | 30.6 MiB in 780,356 allocs | differs [16] | 141 MiB in 2,598,693 allocs | unsupported | unsupported | unsupported | 0.18× of `yaml-v2` ▲ win |
| generic load | `records-8m` | 8 MiB | 42.8 MiB in 854,968 allocs | 306 MiB in 7,450,690 allocs | 308 MiB in 7,589,270 allocs | 242 MiB in 6,169,342 allocs | differs [16] | 1.12 GiB in 20,544,499 allocs | unsupported | unsupported | unsupported | 0.18× of `yaml-v2` ▲ win |
| generic load | `compose-merge-2k` | 2.14 KiB | 28 KiB in 426 allocs | 99.5 KiB in 2,154 allocs | 100 KiB in 2,206 allocs | 76.7 KiB in 1,768 allocs | differs [16] | 302 KiB in 5,763 allocs | unsupported | unsupported | unsupported | 0.37× of `yaml-v2` ▲ win |
| generic load | `compose-merge-64k` | 64.1 KiB | 826 KiB in 12,559 allocs | 2.67 MiB in 62,157 allocs | 2.69 MiB in 63,557 allocs | 2.04 MiB in 50,650 allocs | differs [16] | 9.3 MiB in 164,898 allocs | unsupported | unsupported | unsupported | 0.40× of `yaml-v2` ▲ win |
| generic load | `compose-merge-1m` | 1 MiB | 12.7 MiB in 200,790 allocs | 41.5 MiB in 965,904 allocs | 41.8 MiB in 987,612 allocs | 31.7 MiB in 786,855 allocs | differs [16] | 266 MiB in 2,561,568 allocs | unsupported | unsupported | unsupported | 0.40× of `yaml-v2` ▲ win |
| generic load | `k8s-multi-16k` | 16.4 KiB | 305 KiB in 2,281 allocs | 703 KiB in 14,216 allocs | 702 KiB in 14,634 allocs | 575 KiB in 12,387 allocs | differs [15] | 2.34 MiB in 41,907 allocs | unsupported | unsupported | unsupported | 0.53× of `yaml-v2` ▲ win |
| generic load | `k8s-multi-1m` | 1 MiB | 10.8 MiB in 139,020 allocs | 41.6 MiB in 869,070 allocs | 41.6 MiB in 894,808 allocs | 34.1 MiB in 757,096 allocs | differs [15] | 210 MiB in 2,566,824 allocs | unsupported | unsupported | unsupported | 0.32× of `yaml-v2` ▲ win |
| generic load | `openapi-100k` | 101 KiB | 801 KiB in 9,676 allocs | 3.03 MiB in 61,777 allocs | 3.07 MiB in 64,197 allocs | 2.47 MiB in 52,998 allocs | 4.94 MiB in 83,622 allocs | 11.5 MiB in 183,058 allocs | unsupported | unsupported | unsupported | 0.32× of `yaml-v2` ▲ win |
| generic load | `openapi-8m` | 8 MiB | 63.5 MiB in 828,264 allocs | 243 MiB in 4,958,983 allocs | 246 MiB in 5,153,246 allocs | 198 MiB in 4,253,511 allocs | 388 MiB in 6,707,707 allocs | 1.74 GiB in 14,714,036 allocs | unsupported | unsupported | unsupported | 0.32× of `yaml-v2` ▲ win |
| generic load | `helm-values-64k` | 64 KiB | 235 KiB in 2,658 allocs | 1.94 MiB in 29,222 allocs | 1.95 MiB in 30,172 allocs | 1.05 MiB in 22,957 allocs | differs [16] | 5.86 MiB in 96,855 allocs | unsupported | unsupported | unsupported | 0.22× of `yaml-v2` ▲ win |
| generic load | `helm-values-1m` | 1 MiB | 3.65 MiB in 44,988 allocs | 32.6 MiB in 461,687 allocs | 32.9 MiB in 476,677 allocs | 16.6 MiB in 362,749 allocs | differs [16] | 131 MiB in 1,532,043 allocs | unsupported | unsupported | unsupported | 0.22× of `yaml-v2` ▲ win |
| generic load | `deep-block` | 5.23 KiB | 33.3 KiB in 205 allocs | 133 KiB in 1,165 allocs | 134 KiB in 1,269 allocs | 120 KiB in 963 allocs | differs [16] | 285 KiB in 3,384 allocs | unsupported | unsupported | unsupported | 0.28× of `yaml-v2` ▲ win |
| generic load | `deep-flow` | 202 B | 4.03 KiB in 203 allocs | 146 KiB in 862 allocs | 145 KiB in 957 allocs | 141 KiB in 862 allocs | differs [16] | 126 KiB in 2,364 allocs | unsupported | unsupported | unsupported | 0.03× of `goccy-go-yaml` ▲ win |
| generic load | `wide-map-64k` | 64 KiB | 402 KiB in 7,429 allocs | 2.73 MiB in 51,539 allocs | 2.73 MiB in 51,543 allocs | 2.25 MiB in 43,507 allocs | differs [16] | 64.4 MiB in 156,571 allocs | unsupported | unsupported | unsupported | 0.17× of `yaml-v2` ▲ win |
| generic load | `wide-map-1m` | 1 MiB | 4.07 MiB in 108,125 allocs | 37.4 MiB in 747,438 allocs | 37.3 MiB in 747,428 allocs | 30.3 MiB in 630,437 allocs | differs [16] | 11.2 GiB in 2,269,248 allocs | unsupported | unsupported | unsupported | 0.13× of `yaml-v2` ▲ win |
| generic load | `long-scalars-256k` | 261 KiB | 266 KiB in 15 allocs | 2.19 MiB in 26,140 allocs | 2.19 MiB in 26,144 allocs | 2.19 MiB in 26,132 allocs | 4.76 MiB in 26,212 allocs | 10.2 MiB in 127,457 allocs | unsupported | unsupported | unsupported | 0.12× of `yaml-v2` ▲ win |
| generic load | `flow-heavy-64k` | 64 KiB | 561 KiB in 13,496 allocs | 3.84 MiB in 90,055 allocs | 3.88 MiB in 93,319 allocs | 3.06 MiB in 79,825 allocs | differs [16] | 17 MiB in 294,330 allocs | unsupported | unsupported | unsupported | 0.18× of `yaml-v2` ▲ win |
| generic load | `anchors-32k` | 32 KiB | 1.87 MiB in 21,130 allocs | 3.54 MiB in 79,510 allocs | 3.55 MiB in 80,153 allocs | differs [19] | differs [16] | 8.38 MiB in 135,859 allocs | unsupported | unsupported | unsupported | 0.53× of `yaml-v3` ▲ win |
| generic load | `unicode-32k` | 32 KiB | 71.8 KiB in 2,905 allocs | 491 KiB in 11,644 allocs | 491 KiB in 11,648 allocs | 450 KiB in 11,079 allocs | 684 KiB in 14,011 allocs | 1.88 MiB in 35,508 allocs | unsupported | unsupported | unsupported | 0.16× of `yaml-v2` ▲ win |
| generic load | `scalar-styles-32k` | 32 KiB | 92.2 KiB in 2,661 allocs | 715 KiB in 15,981 allocs | 718 KiB in 16,151 allocs | 575 KiB in 14,486 allocs | 951 KiB in 21,822 allocs | 3.7 MiB in 71,476 allocs | unsupported | unsupported | unsupported | 0.16× of `yaml-v2` ▲ win |
| dump | `compose-wordpress` | 815 B | 709 B in 1 allocs | 39.4 KiB in 135 allocs | 46.6 KiB in 197 allocs | 27.2 KiB in 135 allocs | 86.9 KiB in 449 allocs | 44.3 KiB in 1,123 allocs | unsupported | unsupported | unsupported | 0.03× of `yaml-v2` ▲ win |
| dump | `gha-workflow` | 9.43 KiB | 9.38 KiB in 1 allocs | 636 KiB in 1,732 allocs | 701 KiB in 2,431 allocs | differs [18] | differs [13] | 648 KiB in 16,318 allocs | unsupported | unsupported | unsupported | 0.01× of `yaml-v3` ▲ win |
| dump | `helm-ingress-nginx` | 52.4 KiB | 9.41 KiB in 1 allocs | 1.01e+03 KiB in 2,189 allocs | 1.11 MiB in 3,122 allocs | 667 KiB in 2,188 allocs | 1.01e+03 KiB in 7,136 allocs | 778 KiB in 20,820 allocs | unsupported | unsupported | unsupported | 0.01× of `yaml-v2` ▲ win |
| dump | `openapi-api-with-examples` | 6.75 KiB | 4.83 KiB in 1 allocs | 344 KiB in 720 allocs | 378 KiB in 1,024 allocs | 221 KiB in 719 allocs | 392 KiB in 2,321 allocs | 293 KiB in 6,123 allocs | unsupported | unsupported | unsupported | 0.02× of `yaml-v2` ▲ win |
| dump | `openapi-petstore` | 5.34 KiB | 6.11 KiB in 1 allocs | 349 KiB in 926 allocs | 391 KiB in 1,320 allocs | 233 KiB in 926 allocs | 443 KiB in 3,289 allocs | 355 KiB in 8,092 allocs | unsupported | unsupported | unsupported | 0.03× of `yaml-v2` ▲ win |
| dump | `prometheus-config` | 1.1 KiB | 357 B in 1 allocs | 37.2 KiB in 117 allocs | 42.9 KiB in 173 allocs | 25.2 KiB in 117 allocs | 82.7 KiB in 390 allocs | 30.6 KiB in 854 allocs | unsupported | unsupported | unsupported | 0.01× of `yaml-v2` ▲ win |
| dump | `reference-a` | 51 B | 48 B in 1 allocs | 6.84 KiB in 32 allocs | 7.82 KiB in 47 allocs | 4.77 KiB in 32 allocs | 15.2 KiB in 101 allocs | 5.72 KiB in 175 allocs | unsupported | unsupported | unsupported | <0.01× of `yaml-v2` ▲ win |
| dump | `reference-b` | 51 B | 48 B in 1 allocs | 6.84 KiB in 32 allocs | 7.82 KiB in 47 allocs | 4.77 KiB in 32 allocs | 15.2 KiB in 101 allocs | 5.72 KiB in 175 allocs | unsupported | unsupported | unsupported | <0.01× of `yaml-v2` ▲ win |
| dump | `records-4k` | 4.14 KiB | 4.83 KiB in 1 allocs | differs [22] | differs [24] | differs [20] | differs [17] | differs [3] | unsupported | unsupported | unsupported | no rival |
| dump | `records-64k` | 64.2 KiB | 72.9 KiB in 1 allocs | differs [22] | differs [24] | differs [20] | differs [17] | differs [3] | unsupported | unsupported | unsupported | no rival |
| dump | `records-1m` | 1 MiB | 6.01 MiB in 40 allocs | differs [22] | differs [24] | differs [20] | differs [17] | differs [3] | unsupported | unsupported | unsupported | no rival |
| dump | `records-8m` | 8 MiB | 47.8 MiB in 49 allocs | differs [22] | differs [24] | differs [20] | differs [17] | differs [3] | unsupported | unsupported | unsupported | no rival |
| dump | `compose-merge-2k` | 2.14 KiB | 4.83 KiB in 1 allocs | 349 KiB in 1,167 allocs | 401 KiB in 1,629 allocs | 226 KiB in 1,166 allocs | 428 KiB in 3,638 allocs | 355 KiB in 10,183 allocs | unsupported | unsupported | unsupported | 0.02× of `yaml-v2` ▲ win |
| dump | `compose-merge-64k` | 64.1 KiB | 130 KiB in 1 allocs | 18.8 MiB in 33,881 allocs | 20.2 MiB in 47,479 allocs | 12.6 MiB in 33,879 allocs | 15.8 MiB in 106,131 allocs | 10.6 MiB in 307,547 allocs | unsupported | unsupported | unsupported | 0.01× of `goccy-go-yaml` ▲ win |
| dump | `compose-merge-1m` | 1 MiB | 12.8 MiB in 55 allocs | 328 MiB in 526,394 allocs | 347 MiB in 737,975 allocs | 221 MiB in 526,384 allocs | 271 MiB in 1,649,646 allocs | 165 MiB in 4,829,495 allocs | unsupported | unsupported | unsupported | 0.08× of `goccy-go-yaml` ▲ win |
| dump | `openapi-100k` | 101 KiB | 106 KiB in 1 allocs | 14.5 MiB in 27,536 allocs | 15.7 MiB in 39,491 allocs | 9.79 MiB in 27,535 allocs | 12.8 MiB in 89,944 allocs | 9.23 MiB in 253,891 allocs | unsupported | unsupported | unsupported | 0.01× of `goccy-go-yaml` ▲ win |
| dump | `openapi-8m` | 8 MiB | 59.5 MiB in 63 allocs | 1.54 GiB in 2,208,530 allocs | 1.63 GiB in 3,169,591 allocs | 1.04 GiB in 2,208,529 allocs | 1.26 GiB in 7,218,627 allocs | 749 MiB in 20,692,552 allocs | unsupported | unsupported | unsupported | 0.08× of `goccy-go-yaml` ▲ win |
| dump | `helm-values-64k` | 64 KiB | 40.7 KiB in 1 allocs | 6.14 MiB in 12,346 allocs | 6.79 MiB in 17,458 allocs | 4.12 MiB in 12,345 allocs | 5.45 MiB in 39,284 allocs | 3.87 MiB in 113,022 allocs | unsupported | unsupported | unsupported | 0.01× of `goccy-go-yaml` ▲ win |
| dump | `helm-values-1m` | 1 MiB | 644 KiB in 1 allocs | 130 MiB in 194,889 allocs | 139 MiB in 275,810 allocs | 87 MiB in 194,888 allocs | 108 MiB in 620,609 allocs | 62.1 MiB in 1,815,256 allocs | unsupported | unsupported | unsupported | 0.01× of `goccy-go-yaml` ▲ win |
| dump | `deep-block` | 5.23 KiB | 10.8 KiB in 1 allocs | 403 KiB in 648 allocs | 385 KiB in 953 allocs | 250 KiB in 646 allocs | 523 KiB in 1,912 allocs | 890 KiB in 4,293 allocs | unsupported | unsupported | unsupported | 0.04× of `yaml-v2` ▲ win |
| dump | `deep-flow` | 202 B | 213 B in 1 allocs | 158 KiB in 29 allocs | 175 KiB in 237 allocs | 98.5 KiB in 29 allocs | 239 KiB in 891 allocs | 51.2 KiB in 1,115 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| dump | `wide-map-64k` | 64 KiB | 74.9 KiB in 1 allocs | 8.31 MiB in 20,517 allocs | 9.57 MiB in 32,273 allocs | 5.66 MiB in 20,517 allocs | 8.34 MiB in 68,380 allocs | 6.96 MiB in 261,777 allocs | unsupported | unsupported | unsupported | 0.01× of `yaml-v2` ▲ win |
| dump | `wide-map-1m` | 1 MiB | 14.4 MiB in 61 allocs | 164 MiB in 297,118 allocs | 181 MiB in 468,110 allocs | 111 MiB in 297,118 allocs | 149 MiB in 990,608 allocs | 106 MiB in 4,253,732 allocs | unsupported | unsupported | unsupported | 0.14× of `goccy-go-yaml` ▲ win |
| dump | `long-scalars-256k` | 261 KiB | 332 KiB in 2 allocs | 1.4 MiB in 68 allocs | 1.27 MiB in 77 allocs | 1.4 MiB in 68 allocs | 4.67 MiB in 50,288 allocs | 5.49 MiB in 3,546 allocs | unsupported | unsupported | unsupported | 0.26× of `yaml-v4` ▲ win |
| dump | `flow-heavy-64k` | 64 KiB | 89.5 KiB in 1 allocs | 23.5 MiB in 31,323 allocs | 25.6 MiB in 51,337 allocs | 15.7 MiB in 31,323 allocs | 19.4 MiB in 120,943 allocs | 9.92 MiB in 314,835 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| dump | `anchors-32k` | 32 KiB | 154 KiB in 1 allocs | 49.4 MiB in 66,674 allocs | 53 MiB in 100,522 allocs | differs [19] | differs [14] | 19.1 MiB in 600,171 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| dump | `unicode-32k` | 32 KiB | 27 KiB in 1 allocs | 1.59 MiB in 2,937 allocs | 1.74 MiB in 4,389 allocs | 1.1 MiB in 2,937 allocs | 1.62 MiB in 12,849 allocs | 1.57 MiB in 39,190 allocs | unsupported | unsupported | unsupported | 0.02× of `yaml-v2` ▲ win |
| dump | `scalar-styles-32k` | 32 KiB | 32.6 KiB in 1 allocs | 3.26 MiB in 7,344 allocs | 3.58 MiB in 10,002 allocs | 2.22 MiB in 7,344 allocs | 3.01 MiB in 24,494 allocs | 2.89 MiB in 89,387 allocs | unsupported | unsupported | unsupported | 0.01× of `yaml-v2` ▲ win |
| document parse | `compose-wordpress` | 815 B | 7.52 KiB in 21 allocs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| document parse | `gha-workflow` | 9.43 KiB | 32.5 KiB in 26 allocs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| document parse | `helm-ingress-nginx` | 52.4 KiB | 87.6 KiB in 26 allocs | 484 KiB in 4,453 allocs | differs [23] | unsupported | unsupported | 1.62 MiB in 25,613 allocs | unsupported | unsupported | unsupported | 0.18× of `yaml-v3` ▲ win |
| document parse | `k8s-ingress-nginx-multi` | 16 KiB | 62.6 KiB in 30 allocs | differs [21] | 306 KiB in 5,819 allocs | unsupported | unsupported | 1.08 MiB in 20,708 allocs | unsupported | unsupported | unsupported | 0.20× of `yaml-v4` ▲ win |
| document parse | `openapi-api-with-examples` | 6.75 KiB | 22.3 KiB in 34 allocs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| document parse | `openapi-petstore` | 5.34 KiB | 21 KiB in 26 allocs | 86.9 KiB in 1,408 allocs | differs [23] | unsupported | unsupported | 349 KiB in 6,262 allocs | unsupported | unsupported | unsupported | 0.24× of `yaml-v3` ▲ win |
| document parse | `prometheus-config` | 1.1 KiB | 7.09 KiB in 26 allocs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| document parse | `reference-a` | 51 B | 1.93 KiB in 15 allocs | differs [21] | differs [23] | unsupported | unsupported | 7.76 KiB in 192 allocs | unsupported | unsupported | unsupported | 0.25× of `goccy-go-yaml` ▲ win |
| document parse | `reference-b` | 51 B | 1.93 KiB in 15 allocs | differs [21] | differs [23] | unsupported | unsupported | 7.76 KiB in 192 allocs | unsupported | unsupported | unsupported | 0.25× of `goccy-go-yaml` ▲ win |
| document parse | `records-4k` | 4.14 KiB | 27.4 KiB in 21 allocs | 129 KiB in 2,547 allocs | differs [23] | unsupported | unsupported | differs [5] | unsupported | unsupported | unsupported | 0.21× of `yaml-v3` ▲ win |
| document parse | `records-64k` | 64.2 KiB | 325 KiB in 21 allocs | 1.85 MiB in 38,753 allocs | differs [23] | unsupported | unsupported | differs [5] | unsupported | unsupported | unsupported | 0.17× of `yaml-v3` ▲ win |
| document parse | `records-1m` | 1 MiB | 4.77 MiB in 21 allocs | 29.1 MiB in 612,222 allocs | differs [23] | unsupported | unsupported | differs [5] | unsupported | unsupported | unsupported | 0.16× of `yaml-v3` ▲ win |
| document parse | `records-8m` | 8 MiB | 62.6 MiB in 24 allocs | 230 MiB in 4,840,411 allocs | differs [23] | unsupported | unsupported | differs [5] | unsupported | unsupported | unsupported | 0.27× of `yaml-v3` ▲ win |
| document parse | `compose-merge-2k` | 2.14 KiB | 16.2 KiB in 24 allocs | differs [21] | 61.5 KiB in 1,052 allocs | unsupported | unsupported | 218 KiB in 4,339 allocs | unsupported | unsupported | unsupported | 0.26× of `yaml-v4` ▲ win |
| document parse | `compose-merge-64k` | 64.1 KiB | 333 KiB in 24 allocs | differs [21] | 1.55 MiB in 28,692 allocs | unsupported | unsupported | 6.97 MiB in 124,036 allocs | unsupported | unsupported | unsupported | 0.21× of `yaml-v4` ▲ win |
| document parse | `compose-merge-1m` | 1 MiB | 4.87 MiB in 24 allocs | differs [21] | 24 MiB in 445,014 allocs | unsupported | unsupported | 227 MiB in 1,926,645 allocs | unsupported | unsupported | unsupported | 0.20× of `yaml-v4` ▲ win |
| document parse | `k8s-multi-16k` | 16.4 KiB | 90.3 KiB in 33 allocs | differs [21] | differs [23] | unsupported | unsupported | 1.73 MiB in 33,930 allocs | unsupported | unsupported | unsupported | 0.05× of `goccy-go-yaml` ▲ win |
| document parse | `k8s-multi-1m` | 1 MiB | 5.11 MiB in 41 allocs | differs [21] | differs [23] | unsupported | unsupported | 169 MiB in 2,080,259 allocs | unsupported | unsupported | unsupported | 0.03× of `goccy-go-yaml` ▲ win |
| document parse | `openapi-100k` | 101 KiB | 397 KiB in 29 allocs | 2.02 MiB in 39,624 allocs | differs [23] | unsupported | unsupported | 8.48 MiB in 147,520 allocs | unsupported | unsupported | unsupported | 0.19× of `yaml-v3` ▲ win |
| document parse | `openapi-8m` | 8 MiB | 70.2 MiB in 33 allocs | 162 MiB in 3,179,816 allocs | differs [23] | unsupported | unsupported | 1.5 GiB in 11,861,232 allocs | unsupported | unsupported | unsupported | 0.43× of `yaml-v3` ▲ win |
| document parse | `helm-values-64k` | 64 KiB | 237 KiB in 24 allocs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| document parse | `helm-values-1m` | 1 MiB | 3.4 MiB in 24 allocs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| document parse | `deep-block` | 5.23 KiB | 20.6 KiB in 35 allocs | differs [21] | differs [23] | unsupported | unsupported | 170 KiB in 2,453 allocs | unsupported | unsupported | unsupported | 0.12× of `goccy-go-yaml` ▲ win |
| document parse | `deep-flow` | 202 B | 37.2 KiB in 51 allocs | 133 KiB in 263 allocs | differs [23] | unsupported | unsupported | 109 KiB in 1,743 allocs | unsupported | unsupported | unsupported | 0.34× of `goccy-go-yaml` ▲ win |
| document parse | `wide-map-64k` | 64 KiB | 341 KiB in 17 allocs | 1.87 MiB in 35,392 allocs | 1.87 MiB in 35,396 allocs | unsupported | unsupported | 62.9 MiB in 139,723 allocs | unsupported | unsupported | unsupported | 0.18× of `yaml-v3` ▲ win |
| document parse | `wide-map-1m` | 1 MiB | 4.85 MiB in 17 allocs | 28.3 MiB in 513,131 allocs | 28.3 MiB in 513,133 allocs | unsupported | unsupported | 11.2 GiB in 2,025,738 allocs | unsupported | unsupported | unsupported | 0.17× of `yaml-v4` ▲ win |
| document parse | `long-scalars-256k` | 261 KiB | 267 KiB in 16 allocs | differs [21] | differs [23] | unsupported | unsupported | 9.74 MiB in 127,398 allocs | unsupported | unsupported | unsupported | 0.03× of `goccy-go-yaml` ▲ win |
| document parse | `flow-heavy-64k` | 64 KiB | 477 KiB in 24 allocs | 2.87 MiB in 53,733 allocs | 2.92 MiB in 56,997 allocs | unsupported | unsupported | 14.2 MiB in 255,155 allocs | unsupported | unsupported | unsupported | 0.16× of `yaml-v3` ▲ win |
| document parse | `anchors-32k` | 32 KiB | 215 KiB in 25 allocs | 1.02e+03 KiB in 17,019 allocs | differs [23] | unsupported | unsupported | 6.28 MiB in 122,352 allocs | unsupported | unsupported | unsupported | 0.21× of `yaml-v3` ▲ win |
| document parse | `unicode-32k` | 32 KiB | 92.5 KiB in 17 allocs | differs [21] | differs [23] | unsupported | unsupported | differs [4] | unsupported | unsupported | unsupported | no rival |
| document parse | `scalar-styles-32k` | 32 KiB | 125 KiB in 19 allocs | differs [21] | differs [23] | unsupported | unsupported | 3.22 MiB in 63,471 allocs | unsupported | unsupported | unsupported | 0.04× of `goccy-go-yaml` ▲ win |
| unchanged emit | `compose-wordpress` | 815 B | 0 B in 0 allocs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| unchanged emit | `gha-workflow` | 9.43 KiB | 0 B in 0 allocs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| unchanged emit | `helm-ingress-nginx` | 52.4 KiB | 0 B in 0 allocs | 1.09 MiB in 1,455 allocs | differs [23] | unsupported | unsupported | 739 KiB in 5,483 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `k8s-ingress-nginx-multi` | 16 KiB | 0 B in 0 allocs | differs [21] | 1.47 MiB in 2,174 allocs | unsupported | unsupported | 291 KiB in 4,198 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `openapi-api-with-examples` | 6.75 KiB | 0 B in 0 allocs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| unchanged emit | `openapi-petstore` | 5.34 KiB | 0 B in 0 allocs | 331 KiB in 470 allocs | differs [23] | unsupported | unsupported | 84.2 KiB in 843 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `prometheus-config` | 1.1 KiB | 0 B in 0 allocs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| unchanged emit | `reference-a` | 51 B | 0 B in 0 allocs | differs [21] | differs [23] | unsupported | unsupported | 640 B in 28 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `reference-b` | 51 B | 0 B in 0 allocs | differs [21] | differs [23] | unsupported | unsupported | 649 B in 28 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `records-4k` | 4.14 KiB | 0 B in 0 allocs | 586 KiB in 1,079 allocs | differs [23] | unsupported | unsupported | differs [5] | unsupported | unsupported | unsupported | <0.01× of `yaml-v3` ▲ win |
| unchanged emit | `records-64k` | 64.2 KiB | 0 B in 0 allocs | 10.7 MiB in 16,254 allocs | differs [23] | unsupported | unsupported | differs [5] | unsupported | unsupported | unsupported | <0.01× of `yaml-v3` ▲ win |
| unchanged emit | `records-1m` | 1 MiB | 0 B in 0 allocs | 201 MiB in 258,474 allocs | differs [23] | unsupported | unsupported | differs [5] | unsupported | unsupported | unsupported | <0.01× of `yaml-v3` ▲ win |
| unchanged emit | `records-8m` | 8 MiB | 0 B in 0 allocs | 1.5 GiB in 2,044,450 allocs | differs [23] | unsupported | unsupported | differs [5] | unsupported | unsupported | unsupported | <0.01× of `yaml-v3` ▲ win |
| unchanged emit | `compose-merge-2k` | 2.14 KiB | 0 B in 0 allocs | differs [21] | 168 KiB in 421 allocs | unsupported | unsupported | 28.4 KiB in 615 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `compose-merge-64k` | 64.1 KiB | 0 B in 0 allocs | differs [21] | 10.6 MiB in 11,220 allocs | unsupported | unsupported | 879 KiB in 17,471 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `compose-merge-1m` | 1 MiB | 0 B in 0 allocs | differs [21] | 200 MiB in 173,701 allocs | unsupported | unsupported | 14.3 MiB in 271,334 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `k8s-multi-16k` | 16.4 KiB | 0 B in 0 allocs | differs [21] | differs [23] | unsupported | unsupported | 263 KiB in 5,217 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `k8s-multi-1m` | 1 MiB | 0 B in 0 allocs | differs [21] | differs [23] | unsupported | unsupported | 15.8 MiB in 319,196 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `openapi-100k` | 101 KiB | 0 B in 0 allocs | 13.8 MiB in 13,790 allocs | differs [23] | unsupported | unsupported | 1.78 MiB in 24,313 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `openapi-8m` | 8 MiB | 0 B in 0 allocs | 1.49 GiB in 1,104,293 allocs | differs [23] | unsupported | unsupported | 143 MiB in 1,952,871 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `helm-values-64k` | 64 KiB | 0 B in 0 allocs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| unchanged emit | `helm-values-1m` | 1 MiB | 0 B in 0 allocs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| unchanged emit | `deep-block` | 5.23 KiB | 0 B in 0 allocs | differs [21] | differs [23] | unsupported | unsupported | 386 KiB in 406 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `deep-flow` | 202 B | 0 B in 0 allocs | 159 KiB in 30 allocs | differs [23] | unsupported | unsupported | 12.7 KiB in 203 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `wide-map-64k` | 64 KiB | 0 B in 0 allocs | 8.15 MiB in 15,521 allocs | 8.16 MiB in 16,142 allocs | unsupported | unsupported | 645 KiB in 11,813 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `wide-map-1m` | 1 MiB | 0 B in 0 allocs | 161 MiB in 225,066 allocs | 161 MiB in 234,068 allocs | unsupported | unsupported | 10.1 MiB in 171,066 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `long-scalars-256k` | 261 KiB | 0 B in 0 allocs | differs [21] | differs [23] | unsupported | unsupported | 1.7 MiB in 42 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `flow-heavy-64k` | 64 KiB | 0 B in 0 allocs | 23.1 MiB in 20,056 allocs | 23.1 MiB in 20,060 allocs | unsupported | unsupported | 1.04 MiB in 31,539 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `anchors-32k` | 32 KiB | 0 B in 0 allocs | 5.89 MiB in 6,161 allocs | differs [23] | unsupported | unsupported | 627 KiB in 21,041 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| unchanged emit | `unicode-32k` | 32 KiB | 0 B in 0 allocs | differs [21] | differs [23] | unsupported | unsupported | differs [4] | unsupported | unsupported | unsupported | no rival |
| unchanged emit | `scalar-styles-32k` | 32 KiB | 0 B in 0 allocs | differs [21] | differs [23] | unsupported | unsupported | 583 KiB in 13,294 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| edit script | `compose-wordpress` | 815 B | 9.26 KiB in 92 allocs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| edit script | `gha-workflow` | 9.43 KiB | 8.88 KiB in 89 allocs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| edit script | `helm-ingress-nginx` | 52.4 KiB | 1.94 KiB in 40 allocs | 1.12 MiB in 1,564 allocs | differs [23] | unsupported | unsupported | 729 KiB in 5,698 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| edit script | `openapi-api-with-examples` | 6.75 KiB | 1.2 KiB in 22 allocs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| edit script | `openapi-petstore` | 5.34 KiB | 11.6 KiB in 104 allocs | 321 KiB in 455 allocs | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | 0.04× of `yaml-v3` ▲ win |
| edit script | `prometheus-config` | 1.1 KiB | 1.16 KiB in 20 allocs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| edit script | `reference-a` | 51 B | 2.35 KiB in 27 allocs | differs [21] | differs [23] | unsupported | unsupported | 4.61 KiB in 146 allocs | unsupported | unsupported | unsupported | 0.51× of `goccy-go-yaml` ▲ win |
| edit script | `reference-b` | 51 B | 2.36 KiB in 28 allocs | differs [21] | differs [23] | unsupported | unsupported | differs [1] | unsupported | unsupported | unsupported | no rival |
| edit script | `records-4k` | 4.14 KiB | 4.34 KiB in 69 allocs | 608 KiB in 1,140 allocs | differs [23] | unsupported | unsupported | differs [5] | unsupported | unsupported | unsupported | <0.01× of `yaml-v3` ▲ win |
| edit script | `records-64k` | 64.2 KiB | 4.34 KiB in 69 allocs | 10.7 MiB in 16,311 allocs | differs [23] | unsupported | unsupported | differs [5] | unsupported | unsupported | unsupported | <0.01× of `yaml-v3` ▲ win |
| edit script | `records-1m` | 1 MiB | 4.34 KiB in 69 allocs | 201 MiB in 258,531 allocs | differs [23] | unsupported | unsupported | differs [5] | unsupported | unsupported | unsupported | <0.01× of `yaml-v3` ▲ win |
| edit script | `records-8m` | 8 MiB | 4.37 KiB in 69 allocs | 1.5 GiB in 2,044,511 allocs | differs [23] | unsupported | unsupported | differs [5] | unsupported | unsupported | unsupported | <0.01× of `yaml-v3` ▲ win |
| edit script | `compose-merge-2k` | 2.14 KiB | 5.44 KiB in 63 allocs | differs [21] | 179 KiB in 457 allocs | unsupported | unsupported | 32.8 KiB in 729 allocs | unsupported | unsupported | unsupported | 0.17× of `goccy-go-yaml` ▲ win |
| edit script | `compose-merge-64k` | 64.1 KiB | 13.8 KiB in 65 allocs | differs [21] | 10.6 MiB in 11,256 allocs | unsupported | unsupported | 875 KiB in 17,584 allocs | unsupported | unsupported | unsupported | 0.02× of `goccy-go-yaml` ▲ win |
| edit script | `compose-merge-1m` | 1 MiB | 134 KiB in 65 allocs | differs [21] | 200 MiB in 173,737 allocs | unsupported | unsupported | 14.3 MiB in 271,445 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| edit script | `openapi-100k` | 101 KiB | 10.1 KiB in 83 allocs | 13.8 MiB in 13,781 allocs | differs [23] | unsupported | unsupported | 1.78 MiB in 24,434 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| edit script | `openapi-8m` | 8 MiB | 10.4 KiB in 84 allocs | 1.49 GiB in 1,104,287 allocs | differs [23] | unsupported | unsupported | 143 MiB in 1,952,992 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| edit script | `helm-values-64k` | 64 KiB | 1.61 KiB in 32 allocs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| edit script | `helm-values-1m` | 1 MiB | 65.9 KiB in 34 allocs | differs [21] | differs [23] | unsupported | unsupported | differs [2] | unsupported | unsupported | unsupported | no rival |
| edit script | `wide-map-64k` | 64 KiB | 4.7 KiB in 47 allocs | 8.03 MiB in 15,549 allocs | 8.04 MiB in 16,174 allocs | unsupported | unsupported | 622 KiB in 11,862 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| edit script | `wide-map-1m` | 1 MiB | 4.7 KiB in 47 allocs | 159 MiB in 225,094 allocs | 159 MiB in 234,100 allocs | unsupported | unsupported | 10.1 MiB in 171,114 allocs | unsupported | unsupported | unsupported | <0.01× of `goccy-go-yaml` ▲ win |
| patch (RFC 6902) | `compose-wordpress` | 815 B | 17.6 KiB in 121 allocs | unsupported | unsupported | unsupported | unsupported | unsupported | differs [9] | differs [11] | 115 KiB in 1,048 allocs † | 0.15× of `json-patch` † (text API, value-only) ▲ win |
| patch (RFC 6902) | `gha-workflow` | 9.43 KiB | 54.3 KiB in 130 allocs | unsupported | unsupported | unsupported | unsupported | unsupported | differs [9] | differs [11] | rejects [6] | no rival |
| patch (RFC 6902) | `reference-a` | 51 B | 4.33 KiB in 42 allocs | unsupported | unsupported | unsupported | unsupported | unsupported | differs [9] | 10.1 KiB in 276 allocs † | 22.9 KiB in 204 allocs † | 0.43× of `palantir-yamlpatch-goccy` † (text API) ▲ win |
| patch (RFC 6902) | `reference-b` | 51 B | 4.34 KiB in 43 allocs | unsupported | unsupported | unsupported | unsupported | unsupported | differs [8] | differs [10] | 22.8 KiB in 204 allocs † | 0.19× of `json-patch` † (text API, value-only) ▲ win |
| patch (RFC 6902) | `records-4k` | 4.14 KiB | 37.6 KiB in 91 allocs | unsupported | unsupported | unsupported | unsupported | unsupported | 774 KiB in 3,829 allocs † | differs [12] | differs [7] | 0.05× of `palantir-yamlpatch` † (text API) ▲ win |
| patch (RFC 6902) | `records-64k` | 64.2 KiB | 402 KiB in 91 allocs | unsupported | unsupported | unsupported | unsupported | unsupported | 12.6 MiB in 55,211 allocs † | differs [12] | differs [7] | 0.03× of `palantir-yamlpatch` † (text API) ▲ win |
| patch (RFC 6902) | `records-1m` | 1 MiB | 5.85 MiB in 91 allocs | unsupported | unsupported | unsupported | unsupported | unsupported | 230 MiB in 870,902 allocs † | differs [12] | differs [7] | 0.03× of `palantir-yamlpatch` † (text API) ▲ win |
| patch (RFC 6902) | `records-8m` | 8 MiB | 71.1 MiB in 94 allocs | unsupported | unsupported | unsupported | unsupported | unsupported | 1.73 GiB in 6,885,064 allocs † | differs [12] | differs [7] | 0.04× of `palantir-yamlpatch` † (text API) ▲ win |

Cells without a number, with what the correctness guard found:

1. `goccy-go-yaml` (differs; edit-script ×1): Does not keep the block literal style of the edited value: the new text is written as a plain scalar. What the guard found, in edit-script on `reference-b`: `byte 26: got 48 bytes "#abc 123\nb: this is a test2\nc: 13\n", expected 53 bytes "#abc 123\nb: |-\n  this is a test2\nc: "; line 5: got "b: this is a test2", expected "b: |-"`
2. `goccy-go-yaml` (differs; document-parse ×6, unchanged-emit ×6, edit-script ×7): The AST prints its own spacing for flow collections and trailing comments, and blank lines, quoting or a leading --- marker change, so the text it writes back is not the input. What the guard found, in document-parse on `compose-wordpress`: `does not write the input back: byte 807: got 807 bytes "\n  db_data:\n", expected 815 bytes "\n  db_data:\n\n  \n    "; the texts differ only in blank lines`
3. `goccy-go-yaml` (differs; dump ×4): Writes a string that looks like a number (05509, 1.5) without quotes, so it reads back as a number. What the guard found, in dump on `records-4k`: `read back: at /14/address/zip: got 5509, expected "05509"`
4. `goccy-go-yaml` (differs; document-parse ×1, unchanged-emit ×1): Writes the escapes of a double-quoted scalar (\u00e9) as the characters themselves. What the guard found, in document-parse on `unicode-32k`: `does not write the input back: byte 20: got 28160 bytes " café 0\n- \"é中文 0\"\n- \"😀 grin", expected 32800 bytes " café 0\n- \"\\u00e9\\u4e2d\\u6587 0\"\n- "; line 2: got "- \"é中文 0\"", expected "- \…`
5. `goccy-go-yaml` (differs; document-parse ×4, unchanged-emit ×4, edit-script ×4): Writes the null spelled ~ as null. What the guard found, in document-parse on `records-4k`: `does not write the input back: byte 736: got 4275 bytes ".25\n  note: null\n- id: 3\n  name: Eps", expected 4242 bytes ".25\n  note: ~\n- id: 3\n  name: Epsilo"; line 47: got "  note: null", expected…`
6. `json-patch` (rejects; patch ×1): Reads the document through sigs.k8s.io/yaml, a YAML 1.1 reader: the key on is the boolean true, so a path under /on is not found. What the guard found, in patch on `gha-workflow`: `add operation does not apply: doc is missing path: "/on/push/branches/-": missin…`
7. `json-patch` (differs; patch ×4): Writes a float with no fractional part (812.0) as 812, so a YAML 1.2 reader reads it back as an integer. What the guard found, in patch on `records-4k`: `value after the patch: at /1/score: got 812, expected 812 (float)`
8. `palantir-yamlpatch` (differs; patch ×1): Does not keep the block literal style of the edited value: the new text is written as a plain scalar. What the guard found, in patch on `reference-b`: `byte 13: got 47 bytes "hello\na: 10\n#abc 123\nb: this is a te", expected 53 bytes "hello\na: 10\n\n#abc 123\nb: |-\n  this i"; line 3: got "#abc 123", expected ""`
9. `palantir-yamlpatch` (differs; patch ×3): Patches through the yaml.v3 node tree, which keeps comments but not blank lines, spacing or a leading --- marker, so the text it writes back differs from the input. What the guard found, in patch on `compose-wordpress`: `byte 852: got 852 bytes "\n  db_data:\n", expected 860 bytes "\n  db_data:\n\n  \n    "; the texts differ only in blank lines`
10. `palantir-yamlpatch-goccy` (differs; patch ×1): Does not keep the block literal style of the edited value: the new text is written as a plain scalar. What the guard found, in patch on `reference-b`: `byte 26: got 48 bytes "#abc 123\nb: this is a test2\nc: 13\n", expected 53 bytes "#abc 123\nb: |-\n  this is a test2\nc: "; line 5: got "b: this is a test2", expected "b: |-"`
11. `palantir-yamlpatch-goccy` (differs; patch ×2): Patches through the goccy AST, which prints its own spacing and changes blank lines, so the text it writes back differs from the input. What the guard found, in patch on `compose-wordpress`: `byte 852: got 852 bytes "\n  db_data:\n", expected 860 bytes "\n  db_data:\n\n  \n    "; the texts differ only in blank lines`
12. `palantir-yamlpatch-goccy` (differs; patch ×4): Writes the null spelled ~ as null. What the guard found, in patch on `records-4k`: `byte 752: got 4244 bytes ".25\n  note: null\n- id: 3\n  name: Eps", expected 4211 bytes ".25\n  note: ~\n- id: 3\n  name: Epsilo"; line 48: got "  note: null", expected "  note: ~"`
13. `sigs-yaml` (differs; typed-load ×1, generic-load ×1, dump ×1): A YAML 1.1 reader: the key on is the boolean true, so /on is not found. What the guard found, in typed-load on `gha-workflow`: `/on/push/branches: 0 items, expected 2`
14. `sigs-yaml` (differs; dump ×1): A YAML 1.1 reader: the plain scalar y is the boolean true. What the guard found, in dump on `anchors-32k`: `read back: at /base/b/1: got true, expected "y"`
15. `sigs-yaml` (differs; generic-load ×3): Reads one document of a multi-document stream, so the stream does not come back whole. What the guard found, in generic-load on `k8s-ingress-nginx-multi`: `at /: got a mapping of 3, expected a sequence of 19`
16. `sigs-yaml` (differs; generic-load ×19): Reads through JSON, where every number is a float, so an integer comes back as a float. What the guard found, in generic-load on `compose-wordpress`: `at /services/db/expose/0: got 3306 (float), expected 3306`
17. `sigs-yaml` (differs; dump ×4): Writes a float with no fractional part (812.0) as 812, so a YAML 1.2 reader reads it back as an integer. What the guard found, in dump on `records-4k`: `read back: at /1/score: got 812, expected 812 (float)`
18. `yaml-v2` (differs; generic-load ×1, dump ×1): A YAML 1.1 reader: the key on is the boolean true, so /on is not found. What the guard found, in generic-load on `gha-workflow`: `missing /on`
19. `yaml-v2` (differs; generic-load ×1, dump ×1): A YAML 1.1 reader: the plain scalar y is the boolean true. What the guard found, in generic-load on `anchors-32k`: `at /base/b/1: got true, expected "y"`
20. `yaml-v2` (differs; dump ×4): Writes a float with no fractional part (812.0) as 812, so a YAML 1.2 reader reads it back as an integer. What the guard found, in dump on `records-4k`: `read back: at /1/score: got 812, expected 812 (float)`
21. `yaml-v3` (differs; document-parse ×18, unchanged-emit ×18, edit-script ×11): The yaml.Node tree keeps comments but not blank lines, spacing or a leading --- marker, and it lays flow collections out its own way, so the text it writes back is not the input. What the guard found, in document-parse on `compose-wordpress`: `does not write the input back: byte 807: got 807 bytes "\n  db_data:\n", expected 815 bytes "\n  db_data:\n\n  \n    "; the texts differ only in blank lines`
22. `yaml-v3` (differs; dump ×4): Writes a float with no fractional part (812.0) as 812, so a YAML 1.2 reader reads it back as an integer. What the guard found, in dump on `records-4k`: `read back: at /1/score: got 812, expected 812 (float)`
23. `yaml-v4` (differs; document-parse ×24, unchanged-emit ×24, edit-script ×16): The yaml.Node tree keeps comments but not blank lines, spacing or a leading --- marker, and it lays flow collections out its own way, so the text it writes back is not the input. What the guard found, in document-parse on `compose-wordpress`: `does not write the input back: byte 303: got 783 bytes "olumes:\n    - db_data:/var/lib/mysql", expected 815 bytes "olumes:\n      - db_data:/var/lib/mys"; the texts differ only in blank lines and in…`
24. `yaml-v4` (differs; dump ×4): Writes a float with no fractional part (812.0) as 812, so a YAML 1.2 reader reads it back as an integer. What the guard found, in dump on `records-4k`: `read back: at /1/score: got 812, expected 812 (float)`

Relative figures are ours ÷ the strongest competitor in that row, so below 1.0× is better for us and ▼ marks a loss. A † names a cell that did less than the full task; it still counts. Cells without a number say why.

Method: full run of 2026-10-16T13:51:47Z, **full run**. Kit revision 38acec388e68 (rendered with kit version 0.1.0). `oneyaml-go 30e7fc4bf7b4`; competitors `yaml-v3 v3.0.5`, `yaml-v4 v4.0.0-rc.6`, `yaml-v2 v2.4.0`, `sigs-yaml v1.6.0`, `goccy-go-yaml v1.19.2`, `palantir-yamlpatch v1.5.0`, `palantir-yamlpatch-goccy v1.5.0`, `json-patch v5.9.11`. Runtime go1.26.0 (target linux/amd64), measured with testing.B: ns/op from testing.Benchmark at its default benchtime (20 ms in quick mode). Memory: B/op and allocs/op from testing.B (ReportAllocs). Intel(R) Xeon(R) Platinum 8488C (16 logical CPUs, 30.8 GiB), Linux 7.0.0-1014-aws (x86_64), host load (1 min) 0.68 before, 0.76 after.
<!-- end generated -->
