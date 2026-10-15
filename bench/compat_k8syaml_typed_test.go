package bench

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	sigsyaml "sigs.k8s.io/yaml"

	k8s "oneyaml.dev/go/compat/k8syaml"
)

// Kubernetes-shaped types: metadata with maps, an int-or-string, resource
// quantities that decode themselves, an embedded inline type, pointers and raw
// JSON.

type kTypeMeta struct {
	Kind       string `json:"kind,omitempty"`
	APIVersion string `json:"apiVersion,omitempty"`
}

type kOwnerRef struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Controller *bool  `json:"controller,omitempty"`
}

type kObjectMeta struct {
	Name            string            `json:"name,omitempty"`
	Namespace       string            `json:"namespace,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	Annotations     map[string]string `json:"annotations,omitempty"`
	Generation      int64             `json:"generation,omitempty"`
	Finalizers      []string          `json:"finalizers,omitempty"`
	OwnerReferences []kOwnerRef       `json:"ownerReferences,omitempty"`
	Created         *time.Time        `json:"creationTimestamp,omitempty"`
}

// kIntOrString accepts a number or a string, like intstr.IntOrString.
type kIntOrString struct {
	IsInt bool
	Int   int32
	Str   string
}

func (v *kIntOrString) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		v.IsInt = false
		return json.Unmarshal(b, &v.Str)
	}
	v.IsInt = true
	return json.Unmarshal(b, &v.Int)
}

func (v kIntOrString) MarshalJSON() ([]byte, error) {
	if v.IsInt {
		return json.Marshal(v.Int)
	}
	return json.Marshal(v.Str)
}

// kQuantity keeps what was written, whether it was a number or a string, like
// resource.Quantity.
type kQuantity struct{ S string }

func (q *kQuantity) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &q.S)
	}
	q.S = string(b)
	return nil
}

func (q kQuantity) MarshalJSON() ([]byte, error) { return json.Marshal(q.S) }

type kResources struct {
	Limits   map[string]kQuantity `json:"limits,omitempty"`
	Requests map[string]kQuantity `json:"requests,omitempty"`
}

type kEnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

type kPort struct {
	ContainerPort int32        `json:"containerPort"`
	Name          string       `json:"name,omitempty"`
	Protocol      string       `json:"protocol,omitempty"`
	Target        kIntOrString `json:"target,omitempty"`
}

type kContainer struct {
	Name      string     `json:"name"`
	Image     string     `json:"image,omitempty"`
	Command   []string   `json:"command,omitempty"`
	Args      []string   `json:"args,omitempty"`
	Env       []kEnvVar  `json:"env,omitempty"`
	Ports     []kPort    `json:"ports,omitempty"`
	Resources kResources `json:"resources,omitempty"`
	TTY       bool       `json:"tty,omitempty"`
	Replicas  *int32     `json:"replicas,omitempty"`
	Note      *string    `json:"note,omitempty"`
}

type kPodSpec struct {
	Containers    []kContainer      `json:"containers"`
	RestartPolicy string            `json:"restartPolicy,omitempty"`
	NodeSelector  map[string]string `json:"nodeSelector,omitempty"`
	Extra         map[string]any    `json:"extra,omitempty"`
	Raw           json.RawMessage   `json:"raw,omitempty"`
}

type kPod struct {
	kTypeMeta  `json:",inline"`
	ObjectMeta kObjectMeta `json:"metadata,omitempty"`
	Spec       kPodSpec    `json:"spec"`
}

// kInner is embedded in kOuter, which promotes its fields.
type kInner struct {
	Name  string `json:"name"`
	Count string `json:"count"`
}

type kOuter struct {
	kInner
	Ratio   string            `json:"ratio"`
	Flag    string            `json:"flag"`
	Ptr     *string           `json:"ptr"`
	Strs    []string          `json:"strs"`
	ByName  map[string]string `json:"byName"`
	Floats  []float64         `json:"floats"`
	Ignored string            `json:"-"`
	Skipped string
}

type kScalars struct {
	S string
	I int
	U uint8
	F float64
	B bool
	P *int
	T time.Time
	D time.Duration
}

type typedCase struct {
	name string
	yaml string
	typ  reflect.Type
}

var typedCases = []typedCase{
	{"pod", k8sBuiltin[0], reflect.TypeFor[kPod]()},
	{"pod-unknown-and-case", `
APIVERSION: v1
Kind: Pod
metadata: {NAME: x, labels: {a: 1, b: true, c: 1.5, d: yes, e: null, f: "q"}}
spec: {containers: [{name: c, image: i, unknownField: 1}], restartPolicy: Never, nodeSelector: {disk: ssd, n: 3}}
`, reflect.TypeFor[kPod]()},
	{"quantities", `
spec:
  containers:
  - name: a
    resources:
      limits: {cpu: 2, memory: 1Gi, "ephemeral-storage": "10"}
      requests: {cpu: 0.5, memory: 512M}
    ports:
    - containerPort: 80
      target: http
    - containerPort: 443
      target: 8443
`, reflect.TypeFor[kPod]()},
	{"strings-from-scalars", `
env:
- {name: a, value: 1}
- {name: b, value: 1.50}
- {name: c, value: true}
- {name: d, value: yes}
- {name: e, value: 0755}
- {name: f, value: 0x1F}
- {name: g, value: 1e3}
- {name: h, value: 12:30}
- {name: i, value: 2001-12-14}
- {name: j, value: ~}
- {name: k, value: 9223372036854775807}
- {name: l, value: 18446744073709551615}
- {name: m, value: .5}
- {name: n, value: -0}
`, reflect.TypeFor[kContainer]()},
	{"embedded", `
name: nm
count: 7
ratio: 0.25
flag: on
ptr: 5
strs: [1, two, true, 3.5]
byName: {a: 1, b: x}
floats: [1, 2.5, 1e2]
Ignored: no
skipped: s
`, reflect.TypeFor[kOuter]()},
	{"scalars", "S: 5\nI: 7\nU: 200\nF: 3\nB: true\nP: 9\nD: 1000\n", reflect.TypeFor[kScalars]()},
	{"scalars-bad", "I: x\n", reflect.TypeFor[kScalars]()},
	{"scalars-overflow", "U: 300\n", reflect.TypeFor[kScalars]()},
	{"time", "T: 2020-01-02T03:04:05Z\n", reflect.TypeFor[kScalars]()},
	{"time-date", "T: 2020-01-02\n", reflect.TypeFor[kScalars]()},
	{"slice-of-strings", "[1, a, true, 2.5, ~]\n", reflect.TypeFor[[]string]()},
	{"map-of-strings", "{a: 1, b: x, c: [1]}\n", reflect.TypeFor[map[string]string]()},
	{"map-of-slices", "{a: [1, 2], b: [x]}\n", reflect.TypeFor[map[string][]string]()},
	{"merge", `
base: &b {name: base, count: 1}
a:
  <<: *b
  count: 2
list:
- <<: [*b, {count: 3, extra: z}]
`, reflect.TypeFor[map[string]any]()},
	{"merge-into-struct", `
x: &x {name: from-anchor, count: 5}
z:
  <<: *x
  name: explicit
`, reflect.TypeFor[map[string]kInner]()},
	{"anchors", "a: &x [1, 2]\nb: *x\nc: &s hello\nd: *s\n", reflect.TypeFor[map[string]any]()},
	{"empty", "", reflect.TypeFor[kPod]()},
	{"null", "null\n", reflect.TypeFor[kPod]()},
	{"comment-only", "# nothing\n", reflect.TypeFor[map[string]any]()},
	{"multi-doc", "a: 1\n---\nb: 2\n", reflect.TypeFor[map[string]any]()},
	{"any-slice", "- 1\n- [2, 3]\n- {a: 4}\n", reflect.TypeFor[[]any]()},
	{"non-string-keys", "1: a\ntrue: b\n2.5: c\n0x10: d\n", reflect.TypeFor[map[string]string]()},
	{"int-keys", "1: a\n2: b\n", reflect.TypeFor[map[int]string]()},
	{"wrong-shape", "spec: [1, 2]\n", reflect.TypeFor[kPod]()},
	{"big-ints", "a: 9007199254740993\nb: -9007199254740993\nc: 1.0\n", reflect.TypeFor[map[string]any]()},
	{"raw-and-extra", "spec:\n  containers: []\n  raw: {a: [1, 2]}\n  extra: {n: 1, s: x, l: [1, 2]}\n", reflect.TypeFor[kPod]()},
	{"tab-and-unicode", "name: \"é\\t日本\"\ncount: 'it''s'\n", reflect.TypeFor[kInner]()},
}

// typedIntended lists the typed cases on which sigs fails although the input is
// fine, with the reason.
var typedIntended = map[string]string{
	"embedded":                 "a number read into a field promoted from an embedded struct is an error in sigs, because it looks only at the embedded struct's own field",
	"merge/strict":             "sigs reports a key that a merge key overrides as a duplicate, although the mapping writes it once",
	"merge-into-struct/strict": "sigs reports a key that a merge key overrides as a duplicate, although the mapping writes it once",
}

// runSigs calls f and turns a panic into an error, so that a crash in sigs is
// reported as a result instead of ending the test.
func runSigs(f func() error) (err error, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			err, panicked = fmt.Errorf("panic: %v", r), true
		}
	}()
	return f(), false
}

func TestK8sDiffTyped(t *testing.T) {
	for _, c := range typedCases {
		for _, strict := range []bool{false, true} {
			name := c.name
			if strict {
				name += "/strict"
			}
			t.Run(name, func(t *testing.T) {
				want, got := reflect.New(c.typ), reflect.New(c.typ)
				var werr, gerr error
				if strict {
					werr, _ = runSigs(func() error { return sigsyaml.UnmarshalStrict([]byte(c.yaml), want.Interface()) })
					gerr = k8s.UnmarshalStrict([]byte(c.yaml), got.Interface())
				} else {
					werr, _ = runSigs(func() error { return sigsyaml.Unmarshal([]byte(c.yaml), want.Interface()) })
					gerr = k8s.Unmarshal([]byte(c.yaml), got.Interface())
				}
				if why, ok := typedIntended[name]; ok {
					// Sigs fails here; we must succeed, and read the same as the
					// plain Unmarshal does.
					if werr == nil || gerr != nil {
						t.Fatalf("listed as an intended difference (%s) but sigs: %v, ours: %v", why, werr, gerr)
					}
					return
				}
				if (werr == nil) != (gerr == nil) {
					t.Fatalf("errors differ\n sigs: %v\n ours: %v", werr, gerr)
				}
				if werr == nil && !reflect.DeepEqual(want.Elem().Interface(), got.Elem().Interface()) {
					t.Fatalf("values differ\n sigs: %+v\n ours: %+v", want.Elem().Interface(), got.Elem().Interface())
				}
			})
		}
	}
}

// What the typed tests rely on, stated directly so that a change shows here.
func TestK8sTypedExpectations(t *testing.T) {
	var c kContainer
	err := k8s.Unmarshal([]byte("env:\n- {name: a, value: 1}\n- {name: b, value: yes}\n- {name: c, value: 0755}\n"), &c)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{c.Env[0].Value, c.Env[1].Value, c.Env[2].Value}; !reflect.DeepEqual(got, []string{"1", "true", "493"}) {
		t.Fatalf("string fields: %q", got)
	}
	var pod kPod
	if err := k8s.Unmarshal([]byte(k8sBuiltin[0]), &pod); err != nil {
		t.Fatal(err)
	}
	if pod.Spec.Containers[0].Resources.Limits["cpu"].S != "500m" || pod.ObjectMeta.Labels["enabled"] != "true" {
		t.Fatalf("pod: %+v", pod)
	}
	if err := k8s.UnmarshalStrict([]byte("a: 1\na: 2\n"), &map[string]any{}); err == nil {
		t.Fatal("UnmarshalStrict accepted a duplicate key")
	}
	if err := k8s.UnmarshalStrict([]byte("kind: x\nbogus: 1\n"), &kTypeMeta{}); err == nil {
		t.Fatal("UnmarshalStrict accepted an unknown field")
	}
	var n struct{ Num json.Number }
	if err := k8s.Unmarshal([]byte("num: 12345678901234567890\n"), &n, func(d *json.Decoder) *json.Decoder { d.UseNumber(); return d }); err != nil || n.Num.String() != "12345678901234567890" {
		t.Fatalf("UseNumber: %v %v", n, err)
	}
	// The intended differences, from our side.
	var o kOuter
	if err := k8s.Unmarshal([]byte("name: nm\ncount: 7\n"), &o); err != nil || o.Count != "7" || o.Name != "nm" {
		t.Fatalf("promoted fields: %+v %v", o, err)
	}
	var m map[string]kInner
	if err := k8s.UnmarshalStrict([]byte("x: &x {name: a, count: 1}\nz:\n  <<: *x\n  name: b\n"), &m); err != nil || m["z"].Name != "b" || m["z"].Count != "1" {
		t.Fatalf("merge: %+v %v", m, err)
	}
}

// A map of pointers to strings gets the same conversion as a map of strings.
func TestK8sPointerMap(t *testing.T) {
	type T struct {
		M map[string]*string `json:"m"`
	}
	y := []byte("m: {a: 1, b: x}\n")
	var want, got T
	werr, panicked := runSigs(func() error { return sigsyaml.Unmarshal(y, &want) })
	if err := k8s.Unmarshal(y, &got); err != nil {
		t.Fatalf("ours: %v", err)
	}
	if got.M["a"] == nil || *got.M["a"] != "1" || *got.M["b"] != "x" {
		t.Fatalf("ours: %v", got.M)
	}
	t.Logf("sigs on map[string]*string: panicked=%v err=%v", panicked, werr)
}

// Unmarshal(y, nil) panics in sigs; ours reports an error.
func TestK8sNilTarget(t *testing.T) {
	_, panicked := runSigs(func() error { return sigsyaml.Unmarshal([]byte("a: 1"), nil) })
	if !panicked {
		t.Log("sigs no longer panics on a nil target")
	}
	if err := k8s.Unmarshal([]byte("a: 1"), nil); err == nil {
		t.Fatal("no error for a nil target")
	}
}

// Targets that are not a plain non-nil pointer: sigs does not reject them, and
// neither do we.
func TestK8sDiffOddTargets(t *testing.T) {
	y := []byte("name: a\nlabels: {x: 1}\n")
	var np *kObjectMeta
	pp := new(*kObjectMeta)
	var iface any = &kObjectMeta{}
	for name, mk := range map[string]func() (any, any){
		"value":          func() (any, any) { return kObjectMeta{}, kObjectMeta{} },
		"nil pointer":    func() (any, any) { return np, np },
		"pointer to nil": func() (any, any) { return new(*kObjectMeta), new(*kObjectMeta) },
		"pointer to pointer": func() (any, any) {
			return pp, pp
		},
		"pointer to interface holding a pointer": func() (any, any) {
			a, b := any(&kObjectMeta{}), any(&kObjectMeta{})
			_ = iface
			return &a, &b
		},
		"map": func() (any, any) { return &map[string]any{}, &map[string]any{} },
	} {
		w, g := mk()
		var werr error
		werr, _ = runSigs(func() error { return sigsyaml.Unmarshal(y, w) })
		gerr := k8s.Unmarshal(y, g)
		if (werr == nil) != (gerr == nil) {
			t.Errorf("%s: sigs: %v, ours: %v", name, werr, gerr)
			continue
		}
		if !reflect.DeepEqual(w, g) {
			t.Errorf("%s: sigs: %+v, ours: %+v", name, w, g)
		}
	}
}
