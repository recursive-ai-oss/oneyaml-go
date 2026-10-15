package oneyaml

import "oneyaml.dev/go/internal/encoding"

// UTF-16 and UTF-32 documents. The scanner reads UTF-8 only, so a Document built
// from UTF-16 or UTF-32 text keeps the UTF-8 form for parsing and editing and
// converts back when it is written: an unedited document returns the original
// bytes, an edited one is written again in the original encoding, with a byte
// order mark if the input had one.
var (
	// decodeDocInput converts data to UTF-8. When data is already UTF-8 it returns
	// a nil enc. Otherwise enc describes the original encoding (including whether it
	// had a byte order mark) and is passed back to encodeDocOutput.
	decodeDocInput = func(data []byte) (utf8 []byte, enc any, err error) {
		e, bom := encoding.Detect(data)
		if e == encoding.UTF8 {
			return nil, nil, nil
		}
		out, err := encoding.ToUTF8(data[bom:], e)
		if err != nil {
			return nil, nil, convertError(err, out, &config{}, 0, 0)
		}
		return out, docEncoding{e, bom > 0}, nil
	}
	// encodeDocOutput converts edited UTF-8 text back to the original encoding.
	encodeDocOutput = func(utf8 []byte, enc any) []byte {
		de := enc.(docEncoding)
		out := encoding.FromUTF8(nil, utf8, de.enc, de.bom)
		if e, _ := encoding.Detect(out); !de.bom && e != de.enc {
			// Without a byte order mark the encoding is recognised from a first
			// character in ASCII. An edit can put something else first (a root
			// replaced by Japanese text); write the mark then, or no reader could
			// tell what the text is.
			out = encoding.FromUTF8(nil, utf8, de.enc, true)
		}
		return out
	}
)

type docEncoding struct {
	enc encoding.Encoding
	bom bool
}
