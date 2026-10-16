package oneyaml_test

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	oneyaml "oneyaml.dev/go"
)

// leafHook reads its own node into a list, so every bad element is a mismatch
// reported through the hook.
type leafHook struct{ L []int }

func (l *leafHook) UnmarshalYAML(n oneyaml.Node) error { return n.Decode(&l.L) }

type leafMap map[string]leafEntry

type leafEntry struct {
	S map[string]leafEntry `yaml:"s"`
	H *leafHook            `yaml:"h"`
}

// nestHook reads its own node into a struct that holds more nestHooks, so a
// chain of them is a chain of hooks each inside the one before.
type nestHook struct {
	M map[string]*nestHook
	L []int
}

func (h *nestHook) UnmarshalYAML(n oneyaml.Node) error {
	var tmp struct {
		M map[string]*nestHook `yaml:"m"`
		L []int                `yaml:"l"`
	}
	err := n.Decode(&tmp)
	h.M, h.L = tmp.M, tmp.L
	return err
}

// nestPlain is nestHook without the hook, the reference for what a read reports.
type nestPlain struct {
	M map[string]*nestPlain `yaml:"m"`
	L []int                 `yaml:"l"`
}

func badInts(n int) string {
	return "[" + strings.TrimSuffix(strings.Repeat("x, ", n), ", ") + "]"
}

func typeErrorsOf(t *testing.T, err error) []*oneyaml.TypeError {
	t.Helper()
	var te *oneyaml.TypeError
	if !errors.As(err, &te) {
		t.Fatalf("got %T %v, want a TypeError", err, err)
	}
	return te.All()
}

// checkPathBytes asserts the rule for the paths of one read: the first is whole
// and the ones after it take no more than the larger of the input size and 1 MiB.
func checkPathBytes(t *testing.T, name string, src []byte, errs []*oneyaml.TypeError) {
	t.Helper()
	if len(errs) == 0 {
		t.Fatalf("%s: no errors", name)
	}
	total := 0
	for _, e := range errs[1:] {
		total += len(e.Path)
	}
	if bound := max(len(src), 1<<20); total > bound {
		t.Errorf("%s: %d errors; the paths after the first take %d bytes, more than %d", name, len(errs), total, bound)
	}
}

// A hook that returns the errors of its own decode does not let the paths of a
// read cost more than the budget: the hook's path is not copied in front of each
// of up to 100 inner errors for free, whether the hook error is the only one or
// the first.
func TestHookPathBytesAreBounded(t *testing.T) {
	t.Run("long key", func(t *testing.T) {
		src := []byte("? " + strings.Repeat("k", 20000) + "\n:\n  h: " + badInts(100) + "\n")
		var m leafMap
		errs := typeErrorsOf(t, oneyaml.Unmarshal(src, &m))
		checkPathBytes(t, "long key", src, errs)
		if want := "/" + strings.Repeat("k", 20000) + "/h/0"; errs[0].Path != want {
			t.Errorf("first path is %d bytes, want the whole %d", len(errs[0].Path), len(want))
		}
	})
	t.Run("alias key", func(t *testing.T) {
		// One 10,000-byte key reached through an alias at every level.
		const levels = 300
		key := strings.Repeat("k", 10000)
		src := []byte("{? &k " + key + " : {s: " + strings.Repeat("{*k : {s: ", levels) + "{x: {h: " + badInts(100) + "}}" + strings.Repeat("}}", levels) + "}}\n")
		var m leafMap
		errs := typeErrorsOf(t, oneyaml.Unmarshal(src, &m))
		checkPathBytes(t, "alias key", src, errs)
		if got, want := len(errs[0].Path), (levels+1)*len("/"+key+"/s")+len("/x/h/0"); got != want {
			t.Errorf("first path is %d bytes, want the whole %d", got, want)
		}
	})
	t.Run("nested hooks", func(t *testing.T) {
		// Each level adds a 200-byte key; the hooks are nested 500 deep, so the
		// path of the bad values is 100 KB and every level could copy it again.
		const levels = 500
		key := strings.Repeat("q", 200)
		src := []byte(strings.Repeat("{m: {"+key+": ", levels) + "{l: " + badInts(100) + "}" + strings.Repeat("}}", levels) + "\n")
		var h nestHook
		errs := typeErrorsOf(t, oneyaml.Unmarshal(src, &h))
		checkPathBytes(t, "nested", src, errs)
		want := strings.Repeat("/m/"+key, levels) + "/l/0"
		if errs[0].Path != want {
			t.Errorf("first path is %d bytes, want the whole %d", len(errs[0].Path), len(want))
		}
		// What is reported is what a read without the hooks reports, as far as it goes.
		var p nestPlain
		ref := typeErrorsOf(t, oneyaml.Unmarshal(src, &p))
		if len(errs) > len(ref) {
			t.Fatalf("%d errors, but a read without hooks reports %d", len(errs), len(ref))
		}
		for i, e := range errs {
			if r := ref[i]; e.Path != r.Path || e.Line != r.Line || e.Column != r.Column || e.Msg != r.Msg {
				t.Fatalf("error %d differs from the read without hooks:\n got %q %d:%d %s\nwant %q %d:%d %s", i,
					shorten(e.Path), e.Line, e.Column, e.Msg, shorten(r.Path), r.Line, r.Column, r.Msg)
			}
		}
	})
}

func shorten(s string) string {
	if len(s) > 80 {
		return s[:40] + "..." + s[len(s)-40:]
	}
	return s
}

// With paths short enough to be joined as the read goes, a chain of hooks
// reports every mismatch exactly as a read without hooks does.
func TestNestedHooksReportAsWithoutHooks(t *testing.T) {
	src := []byte("{m: {a: {m: {b: {l: [x, 1, y]}}}}}\n")
	var h nestHook
	got := typeErrorsOf(t, oneyaml.Unmarshal(src, &h))
	var p nestPlain
	want := typeErrorsOf(t, oneyaml.Unmarshal(src, &p))
	if len(got) != len(want) || len(got) != 2 {
		t.Fatalf("got %d errors, want %d (2)", len(got), len(want))
	}
	for i := range got {
		g, w := got[i], want[i]
		if g.Path != w.Path || g.Line != w.Line || g.Column != w.Column || g.Msg != w.Msg {
			t.Errorf("error %d: got %q %d:%d %s, want %q %d:%d %s", i, g.Path, g.Line, g.Column, g.Msg, w.Path, w.Line, w.Column, w.Msg)
		}
	}
}

// hookB keeps the raw error of its own Node.Decode, as a hook that logs or
// caches its failures might.
var stashed error

type hookB struct{ X int }

func (b *hookB) UnmarshalYAML(n oneyaml.Node) error {
	stashed = n.Decode(&b.X)
	return stashed
}

// hookA is read from one text and returns an error that a hook produced while
// reading another.
type hookA struct{}

func (a *hookA) UnmarshalYAML(oneyaml.Node) error {
	var b struct{ B hookB }
	_ = oneyaml.Unmarshal([]byte("\n\n\n\n\n\n\nb: [x]\n"), &b)
	return stashed
}

// An error of a decode made from some other Node is not the error of the hook's
// own decode, even if the hook returns it: it is reported at the hook's value,
// with its message, and its positions are not taken for positions in the input.
func TestHookReturningAnErrorOfAnotherReadKeepsItsOwnPosition(t *testing.T) {
	var v struct{ A hookA }
	errs := typeErrorsOf(t, oneyaml.Unmarshal([]byte("a: 1\n"), &v))
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
	}
	e := errs[0]
	if e.Path != "/a" || e.Line != 1 || e.Column != 4 {
		t.Errorf("got %q at %d:%d, want /a at 1:4", e.Path, e.Line, e.Column)
	}
	if !strings.Contains(e.Msg, "line 8, column 4") {
		t.Errorf("the message %q does not carry the other text's position", e.Msg)
	}
}

// hookChild decodes a node below the one it was given.
type hookChild struct{ Port int }

func (c *hookChild) UnmarshalYAML(n oneyaml.Node) error {
	ch, err := n.Find("inner")
	if err != nil {
		return err
	}
	var x struct {
		Port int `yaml:"port"`
	}
	err = ch.Decode(&x)
	c.Port = x.Port
	return err
}

// hookDeepChild decodes a node two levels down, under a sequence item.
type hookDeepChild struct{ Port int }

func (c *hookDeepChild) UnmarshalYAML(n oneyaml.Node) error {
	ch, err := n.Find("items")
	if err != nil {
		return err
	}
	item := ch.Item(1)
	var port int
	err = item.Decode(&port)
	c.Port = port
	return err
}

// A hook that decodes a node below its own reports the path of that node under
// the hook's value, so path and position agree.
func TestHookDecodingAChildReportsThePathOfTheChild(t *testing.T) {
	check := func(t *testing.T, name string, err error, path string, line, col int) {
		t.Helper()
		errs := typeErrorsOf(t, err)
		if len(errs) != 1 || errs[0].Path != path || errs[0].Line != line || errs[0].Column != col {
			t.Errorf("%s: got %v, want one error at %s %d:%d", name, positions(err), path, line, col)
		}
	}
	src := "svc:\n  inner:\n    port: eighty\n"
	var v struct {
		Svc hookChild `yaml:"svc"`
	}
	check(t, "Unmarshal", oneyaml.Unmarshal([]byte(src), &v), "/svc/inner/port", 3, 11)

	doc, err := oneyaml.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	check(t, "Document.Decode", doc.Decode(&v), "/svc/inner/port", 3, 11)

	deep := "top:\n  items:\n    - 1\n    - two\n"
	var w struct {
		Top hookDeepChild `yaml:"top"`
	}
	check(t, "item", oneyaml.Unmarshal([]byte(deep), &w), "/top/items/1", 4, 7)
	doc, err = oneyaml.Parse([]byte("x: 1\n" + deep))
	if err != nil {
		t.Fatal(err)
	}
	check(t, "item in a Document", doc.Decode(&w), "/top/items/1", 5, 7)
}

// hookText returns the error of a read of text of its own.
type hookText struct{}

func (h *hookText) UnmarshalYAML(oneyaml.Node) error {
	return oneyaml.Unmarshal([]byte("\n\n\n\n\n\n\nx: [1]\n"), &struct{ X int }{})
}

type hookSyntax struct{}

func (h *hookSyntax) UnmarshalYAML(oneyaml.Node) error {
	return oneyaml.Unmarshal([]byte("a: [\n"), new(any))
}

// The package's own errors from a hook are not prefixed twice.
func TestHookErrorsFromOtherTextHaveOnePrefix(t *testing.T) {
	var a struct{ A hookText }
	var b struct{ A hookSyntax }
	for name, err := range map[string]error{
		"type":   oneyaml.Unmarshal([]byte("a: 1\n"), &a),
		"syntax": oneyaml.Unmarshal([]byte("a: 1\n"), &b),
	} {
		if err == nil {
			t.Fatalf("%s: no error", name)
		}
		if n := strings.Count(err.Error(), "oneyaml: "); n != 1 {
			t.Errorf("%s: %q has the prefix %d times", name, err.Error(), n)
		}
	}
	errs := typeErrorsOf(t, oneyaml.Unmarshal([]byte("a: 1\n"), &a))
	if errs[0].Line != 1 || !strings.Contains(errs[0].Msg, "line 8, column 4: /x: cannot unmarshal") {
		t.Errorf("got %d:%d %q", errs[0].Line, errs[0].Column, errs[0].Msg)
	}
}

// A bound that no option lifts does not suggest one.
func TestFixedDepthLimitsSuggestNoOption(t *testing.T) {
	deep := strings.Repeat("[", 20000) + strings.Repeat("]", 20000)
	check := func(name string, err error, wantOption bool) {
		t.Helper()
		var le *oneyaml.LimitError
		if !errors.As(err, &le) || le.Limit != oneyaml.LimitMaxDepth {
			t.Errorf("%s: got %T %v, want a depth LimitError", name, err, err)
			return
		}
		if (le.Option != "") != wantOption {
			t.Errorf("%s: Option %q, want one: %v (%v)", name, le.Option, wantOption, err)
		}
		if strings.Contains(err.Error(), "raise it") != wantOption {
			t.Errorf("%s: message %q", name, err.Error())
		}
	}
	jsonPatch := []byte(`[{"op":"add","path":"/b","value":` + deep + `}]`)
	yamlPatch := []byte("- op: add\n  path: /b\n  value: " + deep + "\n")
	for _, c := range []struct {
		name string
		opts []oneyaml.Option
	}{
		{"default", nil},
		{"MaxDepth raised", []oneyaml.Option{oneyaml.Limits(oneyaml.LimitSet{MaxDepth: 50000})}},
		{"NoLimits", []oneyaml.Option{oneyaml.NoLimits()}},
	} {
		for pname, p := range map[string][]byte{"json": jsonPatch, "yaml": yamlPatch} {
			d, err := oneyaml.Parse([]byte("a: 1\n"), c.opts...)
			if err != nil {
				t.Fatal(err)
			}
			check(c.name+" "+pname+" patch", d.ApplyPatch(p), false)
		}
	}
	// A bound the options do set still names its option.
	d, _ := oneyaml.Parse([]byte("a: 1\n"), oneyaml.Limits(oneyaml.LimitSet{MaxDepth: 5}))
	check("MaxDepth 5 patch", d.ApplyPatch([]byte(`[{"op":"add","path":"/b","value":[[[[[[[1]]]]]]]}]`)), true)

	// The typed recursion ceiling holds under NoLimits and no option moves it.
	type rec struct {
		N *rec `yaml:"n"`
	}
	const levels = 100_001
	src := strings.Repeat("{n: ", levels) + "{}" + strings.Repeat("}", levels)
	var r rec
	check("typed ceiling", oneyaml.Unmarshal([]byte(src), &r, oneyaml.NoLimits()), false)
}

// Hooks nested inside each other do not copy a long path at every level: the
// path is joined once, when the outermost read ends. The key is 10,000 bytes,
// reached through an alias at each of 300 levels, so the path is 3 MB; copying
// it at every level would allocate more than 400 MB.
func TestNestedHookPathIsJoinedOnce(t *testing.T) {
	const levels = 300
	key := strings.Repeat("k", 10000)
	src := []byte("{m: {? &k " + key + " : " + strings.Repeat("{m: {*k : ", levels-1) + "{l: " + badInts(3) + "}" + strings.Repeat("}}", levels-1) + "}}\n")
	var h nestHook
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	err := oneyaml.Unmarshal(src, &h)
	runtime.ReadMemStats(&after)
	errs := typeErrorsOf(t, err)
	if want := strings.Repeat("/m/"+key, levels) + "/l/0"; errs[0].Path != want {
		t.Errorf("first path is %d bytes, want the whole %d", len(errs[0].Path), len(want))
	}
	if got := (after.TotalAlloc - before.TotalAlloc) >> 20; got > 100 {
		t.Errorf("the read allocated %d MB; a path of 3 MB should not be copied at every level", got)
	}
}
