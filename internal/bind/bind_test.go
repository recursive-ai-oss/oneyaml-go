package bind_test

import (
	"reflect"
	"testing"

	"oneyaml.dev/go/internal/bind"
	"oneyaml.dev/go/internal/parse"
)

// replaySource implements bind.Input without being a *parse.Parser, the way a
// document replaying a stored tree does: it hands out events from a slice.
type replaySource struct {
	src    []byte
	events []parse.Event
	p      *parse.Parser
	i      int
}

func newReplay(t *testing.T, src string) *replaySource {
	r := &replaySource{src: []byte(src), p: parse.New([]byte(src), 0)}
	for {
		ev, err := r.p.Next()
		if err != nil {
			break
		}
		r.events = append(r.events, ev)
	}
	return r
}

func (r *replaySource) Next() (parse.Event, error) {
	if r.i >= len(r.events) {
		return parse.Event{}, errEnd
	}
	ev := r.events[r.i]
	r.i++
	return ev, nil
}
func (r *replaySource) Source() []byte { return r.src }
func (r *replaySource) AppendScalar(dst []byte, ev *parse.Event) ([]byte, error) {
	return parse.AppendScalar(dst, r.src, ev)
}
func (r *replaySource) AppendTag(dst []byte, ev *parse.Event) ([]byte, error) {
	return r.p.AppendTag(dst, ev)
}

type endErr struct{}

func (endErr) Error() string { return "end" }

var errEnd = endErr{}

func TestBinderReadsAnyInput(t *testing.T) {
	src := newReplay(t, "a: &x [1, 2]\nb: *x\nc: !!str 5\n")
	b := bind.New(bind.Config{})
	b.Reset(src, bind.Config{})
	ok, err := b.NextDocument()
	if err != nil || !ok {
		t.Fatalf("NextDocument: %v %v", ok, err)
	}
	v, err := b.DecodeAny()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"a": []any{1, 2}, "b": []any{1, 2}, "c": "5"}
	if !reflect.DeepEqual(v, want) {
		t.Errorf("got %#v", v)
	}
}
