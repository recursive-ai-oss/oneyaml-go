# Performance

How oneyaml-go compares with the other Go YAML libraries on speed and memory. Every task runs on the same inputs and workloads for every library, through the oneyaml benchmark kit. The tables and charts below are generated from the kit's result file; none of the numbers is typed by hand.

A run that is marked as not publishable is a smoke result: it shows the layout and the rough picture, and it is not a claim. The numbers to quote are the ones from a full run on a quiet host.

In the unchanged-emit task, an unedited `Document` writes back the text it holds instead of serialising a tree again, so that copy is the work the task measures. The kit times it through the library's real write API; its [method notes](https://github.com/recursive-ai-oss/oneyaml-kit/blob/v0.1.0/docs/benchmarks.md#tasks) explain the task.

## Results

<!-- generated:bench-full -->
Benchmark results will come in a later update, measured with the [oneyaml-kit](https://github.com/recursive-ai-oss/oneyaml-kit) harness.
<!-- end generated -->
