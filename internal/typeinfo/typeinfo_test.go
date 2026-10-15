package typeinfo

import (
	"math/big"
	"reflect"
	"sync"
	"testing"
	"time"
)

func names(p *Plan) []string {
	var out []string
	for _, f := range p.Fields {
		out = append(out, f.Name)
	}
	return out
}

func eq(a, b []string) bool { return reflect.DeepEqual(a, b) }

type tagged struct {
	A int    `yaml:"alpha"`
	B int    `json:"beta"`
	C int    `yaml:"gamma" json:"ignored"`
	D int    `yaml:",omitempty" json:"delta"` // a yaml tag exists, so json is not consulted
	E int    `yaml:"-"`
	F int    `json:"-"`
	G int    `yaml:"-,"`
	H string `yaml:"h,omitempty,omitzero,flow"`
	I int
	j int //nolint
}

func TestTags(t *testing.T) {
	_ = tagged{}.j // the unexported field must be ignored, not used
	p := For(reflect.TypeFor[tagged]())
	want := []string{"alpha", "beta", "gamma", "d", "-", "h", "i"}
	if got := names(p); !eq(got, want) {
		t.Fatalf("names %v, want %v", got, want)
	}
	h := p.Fields[5]
	if !h.OmitEmpty || !h.OmitZero || !h.Flow || !h.Tagged {
		t.Errorf("options not read: %+v", h)
	}
	if p.Fields[3].Tagged || !p.Fields[3].OmitEmpty {
		t.Errorf("field d: %+v", p.Fields[3])
	}
}

type Base struct {
	ID   int
	Name string
}

type Mid struct {
	Base
	Name string // shadows Base.Name
}

type Left struct{ Dup int }
type Right struct{ Dup int }
type Both struct {
	Left
	Right
	Own int
}

type TaggedWins struct {
	Left
	Dup int `yaml:"dup"`
}

type withPtr struct {
	*Base
	Extra map[string]any `yaml:",inline"`
	Named Base           `yaml:"named"`
	Flat  Base           `yaml:",inline"`
}

type hidden struct{ Shown int }
type outer struct {
	hidden
	Top int
}

func TestEmbedding(t *testing.T) {
	if got, want := names(For(reflect.TypeFor[Mid]())), []string{"id", "name"}; !eq(got, want) {
		t.Errorf("Mid: %v, want %v", got, want)
	}
	if f := For(reflect.TypeFor[Mid]()).Fields[1]; len(f.Index) != 1 {
		t.Errorf("the shallower Name should win: %+v", f)
	}
	if got, want := names(For(reflect.TypeFor[Both]())), []string{"own"}; !eq(got, want) {
		t.Errorf("ambiguous names must be dropped: %v, want %v", got, want)
	}
	if got, want := names(For(reflect.TypeFor[TaggedWins]())), []string{"dup"}; !eq(got, want) {
		t.Errorf("tagged beats untagged at the same depth: %v", got)
	}
	p := For(reflect.TypeFor[withPtr]())
	// *Base and Flat both promote id and name at the same depth: ambiguous, dropped.
	if got := names(p); !eq(got, []string{"named"}) {
		t.Errorf("withPtr: %v", got)
	}
	if p.InlineMap == nil || len(p.InlineMap.Index) != 1 {
		t.Errorf("inline map not found: %+v", p.InlineMap)
	}
	if got, want := names(For(reflect.TypeFor[outer]())), []string{"shown", "top"}; !eq(got, want) {
		t.Errorf("unexported embedded struct: %v, want %v", got, want)
	}
	pe := For(reflect.TypeFor[struct{ *Base }]())
	if !pe.Fields[0].ViaPointer {
		t.Error("fields promoted through an embedded pointer must be marked")
	}
}

type recursive struct {
	*recursive
	V int
}

func TestRecursiveEmbedding(t *testing.T) {
	_ = recursive{}.recursive
	// Must terminate.
	if got := names(For(reflect.TypeFor[recursive]())); !eq(got, []string{"v"}) {
		t.Errorf("got %v", got)
	}
}

type m1 struct{}

func (m1) MarshalYAML() (any, error) { return nil, nil }

type m2 struct{}

func (*m2) MarshalYAML() (any, error) { return nil, nil }

type tm struct{}

func (tm) MarshalText() ([]byte, error) { return nil, nil }

type z struct{}

func (z) IsZero() bool { return true }

func TestHooksAndSpecials(t *testing.T) {
	cases := []struct {
		t    reflect.Type
		h    Hooks
		spec Special
	}{
		{reflect.TypeFor[m1](), HookMarshaler, SpecialNone},
		{reflect.TypeFor[*m1](), HookMarshaler, SpecialNone},
		{reflect.TypeFor[m2](), HookPtrMarshaler, SpecialNone},
		{reflect.TypeFor[*m2](), HookMarshaler, SpecialNone},
		{reflect.TypeFor[tm](), HookText, SpecialNone},
		{reflect.TypeFor[z](), HookIsZero, SpecialNone},
		{reflect.TypeFor[time.Time](), HookText | HookIsZero, SpecialTime},
		{reflect.TypeFor[time.Duration](), 0, SpecialDuration},
		{reflect.TypeFor[*big.Int](), HookText, SpecialBigInt},
		{reflect.TypeFor[big.Int](), HookPtrText, SpecialBigInt},
		{reflect.TypeFor[[]byte](), 0, SpecialBytes},
		{reflect.TypeFor[[]int](), 0, SpecialNone},
		{reflect.TypeFor[any](), 0, SpecialNone},
	}
	for _, c := range cases {
		p := For(c.t)
		// Compare only the bits under test: time.Time and big.Int have more methods.
		mask := HookMarshaler | HookPtrMarshaler | HookText | HookPtrText | HookIsZero | HookPtrIsZero
		if p.Hooks&mask != c.h || p.Special != c.spec {
			t.Errorf("%v: hooks %b special %d, want %b %d", c.t, p.Hooks, p.Special, c.h, c.spec)
		}
	}
}

func TestCacheAndData(t *testing.T) {
	tp := reflect.TypeFor[tagged]()
	first, second := For(tp), For(tp)
	if first != second {
		t.Error("plan is not cached")
	}
	var wg sync.WaitGroup
	builds := 0
	var mu sync.Mutex
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			For(tp).EmitData(func(*Plan) any { mu.Lock(); builds++; mu.Unlock(); return 1 })
			For(tp).DecodeData(func(*Plan) any { return 2 })
		}()
	}
	wg.Wait()
	if builds != 1 {
		t.Errorf("EmitData built %d times", builds)
	}
}
