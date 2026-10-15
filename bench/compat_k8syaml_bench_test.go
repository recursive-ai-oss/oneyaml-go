package bench

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sigsyaml "sigs.k8s.io/yaml"

	k8s "oneyaml.dev/go/compat/k8syaml"
)

// Benchmarks of compat/k8syaml against sigs.k8s.io/yaml on the kit's real-world
// documents and on generated Kubernetes-shaped lists.

type k8sBenchDoc struct {
	name string
	data []byte
}

func k8sBenchDocs(b *testing.B) []k8sBenchDoc {
	var docs []k8sBenchDoc
	if kit := os.Getenv("ONEYAML_KIT"); kit != "" {
		files, _ := filepath.Glob(filepath.Join(kit, "bench/workloads/real/*/input.yaml"))
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			// The multi-document workloads: both libraries read the first.
			docs = append(docs, k8sBenchDoc{filepath.Base(filepath.Dir(f)), data})
		}
	}
	docs = append(docs, k8sBenchDoc{"pods-200", genPods(200)})
	docs = append(docs, k8sBenchDoc{"pod", []byte(k8sBuiltin[0])})
	return docs
}

// genPods builds a list of n pods of the shape of the first built-in document.
func genPods(n int) []byte {
	var sb strings.Builder
	sb.WriteString("apiVersion: v1\nkind: List\nitems:\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, `- apiVersion: v1
  kind: Pod
  metadata:
    name: pod-%d
    namespace: default
    labels:
      app: demo
      tier: "%d"
      enabled: yes
    annotations:
      note: "a: b"
  spec:
    containers:
    - name: web
      image: nginx:1.25
      args: ["--port", "8080", "--debug=on"]
      ports:
      - containerPort: 80
        protocol: TCP
      resources:
        limits:
          cpu: 500m
          memory: 128Mi
        requests:
          cpu: "0.5"
          memory: 64Mi
      env:
      - name: A
        value: %d
      - name: B
        value: true
    restartPolicy: Always
`, i, i, i)
	}
	return []byte(sb.String())
}

func BenchmarkK8sYAMLToJSON(b *testing.B) {
	for _, d := range k8sBenchDocs(b) {
		b.Run(d.name+"/sigs", func(b *testing.B) {
			b.SetBytes(int64(len(d.data)))
			b.ReportAllocs()
			for b.Loop() {
				if _, err := sigsyaml.YAMLToJSON(d.data); err != nil {
					b.Skip(err)
				}
			}
		})
		b.Run(d.name+"/oneyaml", func(b *testing.B) {
			b.SetBytes(int64(len(d.data)))
			b.ReportAllocs()
			for b.Loop() {
				if _, err := k8s.YAMLToJSON(d.data); err != nil {
					b.Skip(err)
				}
			}
		})
	}
}

func BenchmarkK8sUnmarshalAny(b *testing.B) {
	for _, d := range k8sBenchDocs(b) {
		b.Run(d.name+"/sigs", func(b *testing.B) {
			b.SetBytes(int64(len(d.data)))
			b.ReportAllocs()
			for b.Loop() {
				var v any
				if err := sigsyaml.Unmarshal(d.data, &v); err != nil {
					b.Skip(err)
				}
			}
		})
		b.Run(d.name+"/oneyaml", func(b *testing.B) {
			b.SetBytes(int64(len(d.data)))
			b.ReportAllocs()
			for b.Loop() {
				var v any
				if err := k8s.Unmarshal(d.data, &v); err != nil {
					b.Skip(err)
				}
			}
		})
	}
}

type kList struct {
	kTypeMeta `json:",inline"`
	Items     []kPod `json:"items"`
}

func BenchmarkK8sUnmarshalTyped(b *testing.B) {
	docs := []k8sBenchDoc{{"pod", []byte(k8sBuiltin[0])}, {"pods-200", genPods(200)}}
	for _, d := range docs {
		typ := func() any {
			if d.name == "pod" {
				return new(kPod)
			}
			return new(kList)
		}
		b.Run(d.name+"/sigs", func(b *testing.B) {
			b.SetBytes(int64(len(d.data)))
			b.ReportAllocs()
			for b.Loop() {
				if err := sigsyaml.Unmarshal(d.data, typ()); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(d.name+"/oneyaml", func(b *testing.B) {
			b.SetBytes(int64(len(d.data)))
			b.ReportAllocs()
			for b.Loop() {
				if err := k8s.Unmarshal(d.data, typ()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkK8sMarshal(b *testing.B) {
	var pod kPod
	if err := k8s.Unmarshal([]byte(k8sBuiltin[0]), &pod); err != nil {
		b.Fatal(err)
	}
	var list kList
	if err := k8s.Unmarshal(genPods(200), &list); err != nil {
		b.Fatal(err)
	}
	for _, c := range []struct {
		name string
		v    any
	}{{"pod", pod}, {"pods-200", list}} {
		b.Run(c.name+"/sigs", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := sigsyaml.Marshal(c.v); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(c.name+"/oneyaml", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := k8s.Marshal(c.v); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkK8sJSONToYAML(b *testing.B) {
	for _, d := range k8sBenchDocs(b) {
		j, err := sigsyaml.YAMLToJSON(d.data)
		if err != nil || len(j) > 1<<22 {
			continue
		}
		b.Run(d.name+"/sigs", func(b *testing.B) {
			b.SetBytes(int64(len(j)))
			b.ReportAllocs()
			for b.Loop() {
				if _, err := sigsyaml.JSONToYAML(j); err != nil {
					b.Skip(err)
				}
			}
		})
		b.Run(d.name+"/oneyaml", func(b *testing.B) {
			b.SetBytes(int64(len(j)))
			b.ReportAllocs()
			for b.Loop() {
				if _, err := k8s.JSONToYAML(j); err != nil {
					b.Skip(err)
				}
			}
		})
	}
}
