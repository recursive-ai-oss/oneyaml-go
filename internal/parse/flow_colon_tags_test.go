package parse_test

import "testing"

// After a plain key, a flow collection may not touch the colon; after a quoted
// key or a closed collection it may. Kit case
// diagnostics/plain-key-with-adjacent-flow-value-is-invalid.
func TestFlowCollectionAfterColon(t *testing.T) {
	for _, in := range []string{"{a:{}}", "{a:[]}", "[a:{}]", "{a :{}}", "{x: 1, b:[1]}"} {
		e := mustReject(t, in)
		if e.Line != 1 {
			t.Errorf("%q: line %d", in, e.Line)
		}
	}
	if e := mustReject(t, "{a:{}}"); e.Column != 4 {
		t.Errorf("{a:{}}: column %d, want 4", e.Column)
	}
	for _, in := range []string{
		`{"a":{}}`, `{'a':[1]}`, "{a: {}}", "{a: []}", "[a:b]", "{a:b}", "{a:}", "{a:, b: 1}",
		"[[1]:{}]", "{{}:{}}", "http://x", "{:{0}}", "[:[]]", "a:{b",
	} {
		mustEvents(t, in)
	}
}

// A verbatim tag is a local tag or a URI; a bare bang is neither (kit case
// diagnostics/verbatim-tag-of-a-lone-bang-is-invalid).
func TestVerbatimTagOfLoneBang(t *testing.T) {
	if e := mustReject(t, "- !<!> foo\n"); e.Line != 1 || e.Column != 6 {
		t.Errorf("position %d:%d, want 1:6", e.Line, e.Column)
	}
	for _, in := range []string{"!<!x> a", "!<tag:yaml.org,2002:str> a", "!<!!> a"} {
		mustEvents(t, in)
	}
	mustReject(t, "!<!")
}
