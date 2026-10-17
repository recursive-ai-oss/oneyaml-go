# Stability

What a release of oneyaml-go promises, and which Go versions it supports.

## Within version 1

From 1.0.0 the module follows Go's [rules for a stable module](https://go.dev/doc/modules/version-numbers), in the spirit of the [Go 1 compatibility promise](https://go.dev/doc/go1compat):

- **Your code keeps compiling and keeps its meaning.** This covers the exported API of `oneyaml.dev/go`, `oneyaml.dev/go/compat/yamlv3` and `oneyaml.dev/go/compat/k8syaml`, including the error types, the sentinel errors and the `Limit` and `Category` constants.
- **Written bytes stay the same.** `Marshal` and an `Encoder` with default options write the same bytes, and an edit leaves the same bytes untouched.

A minor release may still:

- **add** names, methods, struct fields and `Limit` constants. Write struct literals of these packages with field names (`oneyaml.LimitSet{MaxDepth: 50}`), and give a `switch` on `Limit` a default case;
- **fix bugs**, including refusing input that the YAML 1.2 specification calls invalid and fixing output that did not read back as the value written. Each fix is in the [changelog](../CHANGELOG.md);
- **change error messages.** Match errors by type, sentinel and constant, not by text;
- **bring the compat packages closer to their targets** in where they place comments and how they lay out output.

Patch releases only fix bugs. Security fixes go into the latest minor release ([security policy](../SECURITY.md)). Any other breaking change needs a new major version with its own module path, `oneyaml.dev/go/v2`.

Releases before 1.0 could break code in a minor release; the [changelog](../CHANGELOG.md) marks each such change, with what to write instead.

## Go versions

The minimum is Go 1.24, the `go` line in `go.mod`. CI tests Go 1.24, 1.25, 1.26 and 1.27. Raising the minimum takes a minor release and a note in the changelog. The minimum reaches back further than Go's own [release policy](https://go.dev/doc/devel/release#policy), which supports the two newest releases.
