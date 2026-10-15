package oneyaml

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestMemoryRatio measures what a parsed Document keeps alive, and what Parse and
// a short edit script allocate, against the input size, on the kit's real-world
// workloads. A Document may keep at most eight times its input alive.
func TestMemoryRatio(t *testing.T) {
	noSpliceCheck(t)
	root := kitRoot(t)
	files, _ := filepath.Glob(filepath.Join(root, "bench", "workloads", "real", "*", "input.yaml"))
	if len(files) == 0 {
		t.Skip("no workloads")
	}
	live := func() uint64 {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		// Keep many copies alive so that the measurement is not lost in noise.
		n := max(1, (4<<20)/len(data))
		docs := make([]*Document, n)
		before := live()
		var m0, m1, m2 runtime.MemStats
		runtime.ReadMemStats(&m0)
		for i := range docs {
			if docs[i], err = Parse(data); err != nil {
				t.Fatal(err)
			}
		}
		runtime.ReadMemStats(&m1)
		retained := (live() - before) / uint64(n)
		parseAlloc := (m1.TotalAlloc - m0.TotalAlloc) / uint64(n)
		d := docs[0]
		// A short script: two replacements and a removal of the last root entry.
		var leaves []string
		if d.Len() == 1 {
			var paths [][]string
			collectLeavesForTest(d.Root(), nil, &paths)
			for _, p := range paths {
				leaves = append(leaves, "/"+joinTokens(p))
			}
		}
		if len(leaves) >= 3 {
			_ = d.Set(leaves[len(leaves)/3], "edited")
			_ = d.Set(leaves[2*len(leaves)/3], "edited")
			_ = d.Remove(leaves[len(leaves)-1])
		}
		runtime.ReadMemStats(&m2)
		afterEdit := (live()-before)/uint64(n) + 0
		name := filepath.Base(filepath.Dir(f))
		t.Logf("%-26s input %7d B: Parse allocates %.2fx, keeps %.2fx; after the edit script allocated %.2fx in total, keeps %.2fx",
			name, len(data), float64(parseAlloc)/float64(len(data)), float64(retained)/float64(len(data)),
			float64(parseAlloc+m2.TotalAlloc-m1.TotalAlloc)/float64(len(data)), float64(afterEdit)/float64(len(data)))
		if r := float64(retained) / float64(len(data)); r > 8 {
			t.Errorf("%s: a parsed Document keeps %.1fx its input alive", name, r)
		}
		runtime.KeepAlive(docs)
	}
}

func joinTokens(p []string) string {
	s := ""
	for i, t := range p {
		if i > 0 {
			s += "/"
		}
		s += t
	}
	return s
}

func collectLeavesForTest(n Node, path []string, out *[][]string) {
	if len(*out) > 200 {
		return
	}
	switch n.Kind() {
	case ScalarNode:
		if n.Style() == PlainStyle && len(path) >= 2 && n.Value() != "" {
			*out = append(*out, append([]string(nil), path...))
		}
	case MappingNode:
		for i := 0; i < n.Len(); i++ {
			k, v := n.Entry(i)
			if k.Kind() == ScalarNode && k.Value() != "" && !containsAny(k.Value(), "/~ ") {
				collectLeavesForTest(v, append(path, k.Value()), out)
			}
		}
	case SequenceNode:
		for i := 0; i < n.Len(); i++ {
			collectLeavesForTest(n.Item(i), append(path, itoa(i)), out)
		}
	}
}

func containsAny(s, chars string) bool {
	for i := 0; i < len(s); i++ {
		for j := 0; j < len(chars); j++ {
			if s[i] == chars[j] {
				return true
			}
		}
	}
	return false
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}
