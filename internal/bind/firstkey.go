package bind

import (
	"bytes"
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"

	"oneyaml.dev/go/internal/parse"
	"oneyaml.dev/go/internal/resolve"
	"oneyaml.dev/go/internal/scan"
)

// FirstKey explains a duplicate key. off is the offset of the repeated key in
// src; FirstKey returns the key's text and the offset of the earlier key of the
// same mapping that is written the same way. It reports false when the key is
// not a plain text key, or when the two are equal only after conversion (0x1 and
// 1, say), which leaves the caller to give the position alone.
//
// The decoder does not keep the offsets of keys it has seen, because that would
// cost every load; an error is rare, so this reads the source again.
func FirstKey(src []byte, off int) (key string, first int, ok bool) {
	text, mapStart, found := keyAt(src, off)
	if !found {
		return "", 0, false
	}
	first, ok = earlierKey(src, off, mapStart, text)
	if !ok {
		return "", 0, false
	}
	return string(text), first, true
}

// walkKeys feeds fn every scalar event with whether it is a mapping key and the
// offset of the mapping it belongs to. It stops when fn returns false, at an
// error or past offset limit.
func walkKeys(src []byte, limit int, fn func(ev *parse.Event, isKey bool, mapStart int) bool) {
	type frame struct {
		start   int
		mapping bool
		n       int
	}
	var stack []frame
	p := parse.New(src, -1)
	for {
		ev, err := p.Next()
		if err != nil || ev.Start > limit {
			return
		}
		switch ev.Kind {
		case parse.Scalar, parse.Alias, parse.MappingStart, parse.SequenceStart:
			isKey, ms := false, -1
			if n := len(stack); n > 0 {
				top := &stack[n-1]
				isKey = top.mapping && top.n%2 == 0
				ms = top.start
				top.n++
			}
			if ev.Kind == parse.Scalar && !fn(&ev, isKey, ms) {
				return
			}
			if ev.Kind == parse.MappingStart {
				stack = append(stack, frame{start: ev.Start, mapping: true})
			} else if ev.Kind == parse.SequenceStart {
				stack = append(stack, frame{start: ev.Start})
			}
		case parse.MappingEnd, parse.SequenceEnd:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
}

func keyAt(src []byte, off int) (text []byte, mapStart int, found bool) {
	walkKeys(src, off, func(ev *parse.Event, isKey bool, ms int) bool {
		if !isKey || ev.Start != off {
			return true
		}
		t, err := parse.AppendScalar(nil, src, ev)
		if err == nil {
			text, mapStart, found = t, ms, true
		}
		return false
	})
	return
}

func earlierKey(src []byte, off, mapStart int, text []byte) (first int, ok bool) {
	var buf []byte
	walkKeys(src, off, func(ev *parse.Event, isKey bool, ms int) bool {
		if !isKey || ms != mapStart || ev.Start >= off {
			return true
		}
		buf, _ = parse.AppendScalar(buf[:0], src, ev)
		if bytes.Equal(buf, text) {
			first, ok = ev.Start, true
			return false
		}
		return true
	})
	return
}

// KeyText returns the text of the mapping key that starts at offset off of src.
func KeyText(src []byte, off int) (string, bool) {
	text, _, ok := keyAt(src, off)
	return string(text), ok
}

// EarlierRounding finds, for a duplicate key at offset off, an earlier key of the
// same mapping that reads as the same float64 though it is written differently.
func EarlierRounding(src []byte, off int) (first int, ok bool) {
	text, mapStart, found := keyAt(src, off)
	if !found {
		return 0, false
	}
	later, ok := floatOf(text)
	if !ok {
		return 0, false
	}
	var buf []byte
	walkKeys(src, off, func(ev *parse.Event, isKey bool, ms int) bool {
		if !isKey || ms != mapStart || ev.Start >= off {
			return true
		}
		buf, _ = parse.AppendScalar(buf[:0], src, ev)
		if f, isF := floatOf(buf); isF && f == later {
			first, ok = ev.Start, true
			return false
		}
		return true
	})
	return first, ok
}

// floatOf reads a Core schema float, .inf included.
func floatOf(text []byte) (float64, bool) {
	t := string(text)
	switch strings.ToLower(strings.TrimPrefix(t, "+")) {
	case ".inf":
		return math.Inf(1), true
	case "-.inf":
		return math.Inf(-1), true
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return 0, false
	}
	return f, f == f
}

// EarlierSameValue finds, for a duplicate key at offset off, an earlier key of
// the same mapping that is written differently but reads as the same Core
// schema value (1 and 0x1, null and ~, true and True). It only looks at plain
// scalars without a tag, which is where such spellings come from.
func EarlierSameValue(src []byte, off int) (first int, ok bool) {
	var later any
	var mapStart int
	found := false
	walkKeys(src, off, func(ev *parse.Event, isKey bool, ms int) bool {
		if !isKey || ev.Start != off {
			return true
		}
		if v, plain := plainKeyValue(src, ev); plain {
			later, mapStart, found = v, ms, true
		}
		return false
	})
	if !found {
		return 0, false
	}
	walkKeys(src, off, func(ev *parse.Event, isKey bool, ms int) bool {
		if !isKey || ms != mapStart || ev.Start >= off {
			return true
		}
		if v, plain := plainKeyValue(src, ev); plain && sameKeyValue(v, later) {
			first, ok = ev.Start, true
			return false
		}
		return true
	})
	return first, ok
}

// plainKeyValue is the generic value of a plain, untagged scalar key. Strings
// are left out: two string keys are equal only when written alike, which the
// caller has already looked for.
func plainKeyValue(src []byte, ev *parse.Event) (any, bool) {
	if ev.Style != scan.Plain || ev.HasTag() {
		return nil, false
	}
	text, err := parse.AppendScalar(nil, src, ev)
	if err != nil {
		return nil, false
	}
	r := resolve.Core(text)
	if r.Kind == resolve.String {
		return nil, false
	}
	return anyOf(text, &r, tagNone), true
}

// sameKeyValue compares two generic key values, big integers by value.
func sameKeyValue(a, b any) bool {
	if x, ok := a.(*big.Int); ok {
		y, ok := b.(*big.Int)
		return ok && x.Cmp(y) == 0
	}
	if _, ok := b.(*big.Int); ok {
		return false
	}
	return a == b
}
