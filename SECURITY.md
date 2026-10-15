# Security policy

## Supported versions

Security fixes go into the latest minor release. Older minor releases are not patched; upgrade to the latest.

## Reporting a vulnerability

Report it privately through GitHub: open the [Security tab](https://github.com/recursive-ai-oss/oneyaml-go/security) of this repository and choose **Report a vulnerability**. Please do not open a public issue or pull request for a vulnerability.

Include the input or code that shows the problem, the version, and what you expected. A reproduction that is a few lines of YAML or Go is the most useful thing you can send.

A panic, a hang, or memory use out of proportion to the input is a bug in this library and a valid report, whether or not you can show it is exploitable.

## What the library promises

- **Bounds by default.** Nesting depth, alias expansion, the digits of a number and the work of a patch are bounded unless you pass `NoLimits()`. [Limits](docs/limits.md) lists each one.
- **More bounds with `Untrusted()`.** Input size, scalar size and documents per stream are bounded only when you select `Untrusted()`, which also tightens the default bounds. Select it for any input you do not control, and size it for your data with `Limits`.
- **A panic caused by input is a security bug.** The library is written so that no input makes a public function panic: nesting is followed with explicit stacks, so deep input does not grow the Go stack, and the code is fuzzed (below). That is a design goal tested by fuzzing, not a proof. If you find an input that causes a panic, report it as described above.
- **No `unsafe`** in library code, no cgo, and no dependency outside the standard library.
- **Nothing is executed.** YAML tags never construct Go values or call code; a tag such as `!!python/object/apply:os.system` is read as plain data.
- **Fuzzed.** The parser, decoder, edit and patch paths and the compat packages have Go fuzz targets. Their seed corpora run with every `go test`, and longer fuzzing runs are made as the code changes.

## What it does not promise

These are known limits, not bugs. Read [Limits](docs/limits.md) before you handle input from outside.

- Code you write inside a hook (`UnmarshalYAML`, `MarshalYAML`, or the text and JSON methods the library calls) runs outside the library's limits.
- The compat packages (`compat/yamlv3`, `compat/k8syaml`) always run with the default limits and have no `Untrusted` mode. For outside input, read with the `oneyaml` package.
- Without `Untrusted()`, there is no cap on input size, documents per stream or scalar length, and memory grows with the input. A `Document` keeps the text and an index of it, which for dense input is an order of magnitude more than the text.
- `NoLimits()` removes every bound, including alias expansion. Never use it on input you do not trust.
- `Marshal` writes a value that is shared in several places once per place, as `encoding/json` does, so its output can be far larger than the value in memory.
- A `Document` uses 32-bit offsets. `Parse` refuses input of 2 GiB or more; keep documents far below that.
