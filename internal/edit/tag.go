package edit

import (
	"bytes"
	"sort"

	"oneyaml.dev/go/internal/cst"
)

const coreTagPrefix = "tag:yaml.org,2002:"

// CanonTag returns the tag of node n in the form the rest of the planner
// compares: a tag in the core namespace, however it is written ("!!str",
// "!<tag:yaml.org,2002:str>", or through a %TAG handle), is "!!name"; any other tag
// is its full name, which for a local tag such as "!foo" is the text as written.
// n must carry a tag.
func CanonTag(t *cst.Tree, src []byte, n int32) string {
	p := t.PropOf(n)
	tag := src[p.TagStart:p.TagEnd]
	dirs := directives(t, src, n)
	if len(dirs) == 0 && len(tag) >= 2 && tag[1] == '!' {
		// The common case: the default handles, no directives. Constants keep it
		// from allocating.
		switch string(tag) {
		case "!!str":
			return "!!str"
		case "!!int":
			return "!!int"
		case "!!float":
			return "!!float"
		case "!!bool":
			return "!!bool"
		case "!!null":
			return "!!null"
		case "!!map":
			return "!!map"
		case "!!seq":
			return "!!seq"
		case "!!binary":
			return "!!binary"
		case "!!timestamp":
			return "!!timestamp"
		}
		return string(tag)
	}
	if len(dirs) == 0 && len(tag) == 1 {
		return "!"
	}
	var full string
	switch {
	case len(tag) >= 3 && tag[0] == '!' && tag[1] == '<' && tag[len(tag)-1] == '>':
		full = string(tag[2 : len(tag)-1])
	case len(tag) >= 2 && tag[1] == '!':
		full = handlePrefix(dirs, "!!", coreTagPrefix) + string(tag[2:])
	default:
		if i := bytes.IndexByte(tag[1:], '!'); i >= 0 {
			p := handlePrefix(dirs, string(tag[:i+2]), "")
			if p == "" {
				return string(tag) // undeclared handle: keep the text
			}
			full = p + string(tag[i+2:])
		} else if len(tag) > 1 {
			full = handlePrefix(dirs, "!", "!") + string(tag[1:])
		} else {
			return "!"
		}
	}
	if len(full) > len(coreTagPrefix) && full[:len(coreTagPrefix)] == coreTagPrefix {
		return "!!" + full[len(coreTagPrefix):]
	}
	return full
}

// directives returns the text where node n's document can have %TAG directives:
// between the end of the previous document and the start marker. A document with
// no start marker has none, so the common case costs a binary search.
func directives(t *cst.Tree, src []byte, n int32) []byte {
	d := sort.Search(len(t.Docs), func(i int) bool { return t.Docs[i].Root > n }) - 1
	if d < 0 || !t.Docs[d].ExplicitStart {
		return nil
	}
	lo := 0
	if d > 0 {
		prev := &t.Docs[d-1]
		if prev.ExplicitEnd {
			lo = int(prev.EndMarker[1])
		} else if prev.Root > 0 {
			lo = int(t.Nodes[prev.Root].End)
		}
	}
	hi := int(t.Docs[d].StartMarker[0])
	if lo < 0 || hi > len(src) || lo >= hi {
		return nil
	}
	return src[lo:hi]
}

// handlePrefix returns the prefix the tag handle stands for: the one the last
// %TAG directive in dirs gives, or def.
func handlePrefix(dirs []byte, handle, def string) string {
	for end := len(dirs); ; {
		i := bytes.LastIndex(dirs[:end], []byte("%TAG"))
		if i < 0 {
			return def
		}
		if i == 0 || dirs[i-1] == '\n' || dirs[i-1] == '\r' {
			line := dirs[i:]
			if j := bytes.IndexAny(line, "\r\n"); j >= 0 {
				line = line[:j]
			}
			// "%TAG", the handle, the prefix: split by hand so the text does not
			// escape into a slice of fields.
			line = bytes.TrimLeft(line[4:], " \t")
			if k := bytes.IndexAny(line, " \t"); k > 0 && string(line[:k]) == handle {
				rest := bytes.TrimLeft(line[k:], " \t")
				if e := bytes.IndexAny(rest, " \t"); e >= 0 {
					rest = rest[:e]
				}
				if len(rest) > 0 {
					return string(rest)
				}
			}
		}
		end = i
	}
}
