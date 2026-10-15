package parse_test

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/parse"
	"oneyaml.dev/go/internal/scan"
)

// syntheticK8s builds a deterministic Kubernetes-style multi-document stream.
func syntheticK8s(n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, `---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: service-%d
  namespace: production
  labels:
    app: service-%d
    tier: backend
  annotations:
    description: "A longer description with \"escapes\" and unicode é"
spec:
  replicas: 3
  selector:
    matchLabels:
      app: service-%d
  template:
    spec:
      containers:
        - name: main
          image: registry.example.com/team/service:1.%d.0
          args: ["--port=8080", "--verbose", "--config=/etc/service/config.yaml"]
          env:
            - {name: LOG_LEVEL, value: debug}
            - name: SCRIPT
              value: |
                #!/bin/sh
                echo starting
                exec /bin/service
          resources:
            limits: {cpu: 500m, memory: 256Mi}
# trailing comment
`, i, i, i, i)
	}
	return sb.String()
}

type benchInput struct {
	name string
	data []byte
}

func benchInputs(b *testing.B) []benchInput {
	in := []benchInput{{"synthetic-k8s", []byte(syntheticK8s(40))}}
	if dir := os.Getenv("ONEYAML_BENCH_DIR"); dir != "" {
		names, _ := filepath.Glob(filepath.Join(dir, "*", "input.yaml"))
		for _, n := range names {
			if data, err := os.ReadFile(n); err == nil {
				in = append(in, benchInput{filepath.Base(filepath.Dir(n)), data})
			}
		}
	}
	return in
}

// BenchmarkScan measures the scanner alone.
func BenchmarkScan(b *testing.B) {
	for _, in := range benchInputs(b) {
		b.Run(in.name, func(b *testing.B) {
			s := scan.New(in.data, 0)
			b.SetBytes(int64(len(in.data)))
			b.ReportAllocs()
			for b.Loop() {
				s.Reset(in.data, 0)
				for {
					t, err := s.Peek()
					if err != nil {
						b.Fatal(err)
					}
					k := t.Kind
					s.Skip()
					if k == scan.StreamEnd {
						break
					}
				}
			}
		})
	}
}

// BenchmarkParse measures scanner plus event parser, without decoding values.
func BenchmarkParse(b *testing.B) {
	for _, in := range benchInputs(b) {
		b.Run(in.name, func(b *testing.B) {
			p := parse.New(in.data, 0)
			b.SetBytes(int64(len(in.data)))
			b.ReportAllocs()
			for b.Loop() {
				p.Reset(in.data, 0)
				for {
					_, err := p.Next()
					if err != nil {
						if !errors.Is(err, io.EOF) {
							b.Fatal(err)
						}
						break
					}
				}
			}
		})
	}
}

// BenchmarkParseDecode also decodes every scalar into a reused buffer, the
// work a loader does for each value.
func BenchmarkParseDecode(b *testing.B) {
	for _, in := range benchInputs(b) {
		b.Run(in.name, func(b *testing.B) {
			p := parse.New(in.data, 0)
			var buf []byte
			b.SetBytes(int64(len(in.data)))
			b.ReportAllocs()
			for b.Loop() {
				p.Reset(in.data, 0)
				for {
					ev, err := p.Next()
					if err != nil {
						if !errors.Is(err, io.EOF) {
							b.Fatal(err)
						}
						break
					}
					if ev.Kind == parse.Scalar {
						buf, _ = p.AppendScalar(buf[:0], &ev)
					}
				}
			}
		})
	}
}

// BenchmarkParseNew includes creating the parser each time, the cost of a
// one-shot Unmarshal.
func BenchmarkParseNew(b *testing.B) {
	for _, in := range benchInputs(b) {
		b.Run(in.name, func(b *testing.B) {
			b.SetBytes(int64(len(in.data)))
			b.ReportAllocs()
			for b.Loop() {
				p := parse.New(in.data, 0)
				for {
					_, err := p.Next()
					if err != nil {
						if !errors.Is(err, io.EOF) {
							b.Fatal(err)
						}
						break
					}
				}
			}
		})
	}
}
