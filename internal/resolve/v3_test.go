package resolve

import (
	"math"
	"testing"
)

func TestV3(t *testing.T) {
	type want struct {
		kind Kind
		i    int64
		u    uint64
		f    float64
		b    bool
	}
	cases := map[string]want{
		"":                     {kind: Null},
		"~":                    {kind: Null},
		"null":                 {kind: Null},
		"true":                 {kind: Bool, b: true},
		"False":                {kind: Bool},
		"yes":                  {kind: String},
		"on":                   {kind: String},
		"y":                    {kind: String},
		"0":                    {kind: Int},
		"0777":                 {kind: Int, i: 0o777},
		"0o17":                 {kind: Int, i: 15},
		"0b101":                {kind: Int, i: 5},
		"0x1F":                 {kind: Int, i: 31},
		"-0x10":                {kind: Int, i: -16},
		"+5":                   {kind: Int, i: 5},
		"1_000":                {kind: Int, i: 1000},
		"089":                  {kind: Float, f: 89},
		"9223372036854775807":  {kind: Int, i: math.MaxInt64},
		"9223372036854775808":  {kind: Int, u: 1 << 63},
		"+9223372036854775808": {kind: Float, f: 9223372036854775808},
		"-9223372036854775808": {kind: Int, i: math.MinInt64},
		"1e3":                  {kind: Float, f: 1000},
		".5":                   {kind: Float, f: 0.5},
		// A text that starts with a dot is read as written: an underscore needs a
		// digit on each side. Any other number loses its underscores first.
		"._5":             {kind: String},
		".5_":             {kind: String},
		".5__0":           {kind: String},
		"._":              {kind: String},
		".5e_3":           {kind: String},
		".5_0":            {kind: Float, f: 0.5},
		".1_0e1_0":        {kind: Float, f: 0.1e10},
		"+._5":            {kind: Float, f: 0.5},
		"1_0.5":           {kind: Float, f: 10.5},
		"1.":              {kind: Float, f: 1},
		"-.5":             {kind: Float, f: -0.5},
		".inf":            {kind: Float, f: math.Inf(1)},
		"-.Inf":           {kind: Float, f: math.Inf(-1)},
		"1e999":           {kind: String},
		"1.2.3":           {kind: String},
		"0x":              {kind: String},
		"-":               {kind: String},
		".":               {kind: String},
		"1:30":            {kind: String},
		"12abc":           {kind: String},
		"2001-01-02":      {kind: Timestamp},
		"2001-1-2T3:4:5Z": {kind: Timestamp},
		"2001-13-45":      {kind: String},
		"<<":              {kind: String},
	}
	for in, w := range cases {
		r := V3([]byte(in))
		if r.Kind != w.kind {
			t.Errorf("%q: kind %v, want %v", in, r.Kind, w.kind)
			continue
		}
		switch w.kind {
		case Int:
			if w.u != 0 {
				if r.W != FitsU64 || r.U != w.u {
					t.Errorf("%q: got %+v", in, r)
				}
			} else if r.W != Fits64 || r.I != w.i {
				t.Errorf("%q: got %+v", in, r)
			}
		case Float:
			if r.F != w.f {
				t.Errorf("%q: float %v, want %v", in, r.F, w.f)
			}
		case Bool:
			if r.B != w.b {
				t.Errorf("%q: bool %v", in, r.B)
			}
		}
	}
	if v, ok := V3Bool11([]byte("Yes")); !ok || !v {
		t.Error("Yes")
	}
	if v, ok := V3Bool11([]byte("OFF")); !ok || v {
		t.Error("OFF")
	}
	if _, ok := V3Bool11([]byte("maybe")); ok {
		t.Error("maybe")
	}
}
