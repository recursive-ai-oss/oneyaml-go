package oneyaml

import (
	"bytes"
	"io"
	"reflect"

	"oneyaml.dev/go/internal/bind"
	"oneyaml.dev/go/internal/encoding"
)

// A Decoder reads YAML documents from an input stream, one per call to Decode.
//
// It reads ahead only as far as the end of the current document: a line that
// starts with "---" or "..." at column 0 ends a document wherever it occurs,
// since YAML forbids those markers inside any node. The document is then parsed
// from the buffer, so a Decoder never holds more than one document and the
// bytes after it.
type Decoder struct {
	r   io.Reader
	cfg config

	buf     []byte
	start   int  // buf[start:] is unconsumed
	scanned int  // buf[start:scanned] has been examined, up to a line start
	probe   int  // buf[scanned:probe] is known to hold no line break, so a longer wait does not search it again
	eof     bool // the reader is exhausted
	seen    bool // the current segment has content
	total   int  // bytes read so far, for MaxInputBytes
	docs    int  // documents returned so far

	checked bool
	lineOff int // lines consumed before buf[start]
	byteOff int // bytes consumed before buf[start] (in the UTF-8 form)

	s      *session
	seg    []byte
	segOpn bool
	err    error // sticky

	hooks    int  // the binder's hook count before the current document was read
	shared   bool // a node handed out by the last document may point into buf
	examined int  // bytes searched for line breaks, so tests can check the work stays linear
}

// NewDecoder returns a Decoder that reads from r. The options are those of
// Unmarshal.
func NewDecoder(r io.Reader, opts ...Option) *Decoder {
	return &Decoder{r: r, cfg: newConfig(opts)}
}

const readChunk = 32 << 10

// Decode reads the next document into the value v points to, as Unmarshal does.
// It returns io.EOF when there are no more documents. After a syntax error or a
// read error every later call returns the same error; after a *TypeError the
// next call reads the next document. An empty document decodes as null.
func (d *Decoder) Decode(v any) error {
	if d.err != nil {
		return d.err
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return &TypeError{GoType: reflect.TypeOf(v), Msg: "Decode needs a non-nil pointer, got " + goTypeName(v)}
	}
	if d.s == nil {
		d.s = sessionPool.Get().(*session)
	}
	for {
		if d.segOpn {
			ok, err := d.s.b.NextDocument()
			if err != nil {
				return d.fail(err, true)
			}
			if ok {
				d.hooks = d.s.b.Hooks()
				d.docs++
				if max := d.cfg.limits.MaxDocuments; max > 0 && d.docs > max {
					return d.fail(limitErr(LimitMaxDocuments, "documents per stream", max, ""), false)
				}
				var err error
				if ap, isAny := v.(*any); isAny {
					var x any
					if x, err = d.s.b.DecodeAny(); err == nil {
						*ap = x
					}
				} else {
					err = d.s.b.Decode(rv.Elem())
				}
				if err == nil && d.cfg.dialect != bind.DialectCore {
					if cerr := collectedErr(d.s.b, d.seg, d.lineOff-countLines(d.seg)); cerr != nil {
						return d.fail(cerr, false)
					}
				}
				if err != nil {
					return d.fail(err, false)
				}
				d.noteHooks()
				return nil
			}
			d.segOpn = false
			continue
		}
		seg, err := d.nextSegment()
		if err != nil {
			if err == io.EOF {
				d.err = io.EOF
				d.close()
				return io.EOF
			}
			return d.fail(err, true)
		}
		d.seg = seg
		d.s.reset(seg, &d.cfg)
		d.segOpn = true
	}
}

// fail converts err and records it: errors that leave the parser mid-document
// end the segment, and sticky ones end the stream.
func (d *Decoder) fail(err error, sticky bool) error {
	d.noteHooks()
	out := convertError(err, d.seg, &d.cfg, d.lineOff-countLines(d.seg), d.byteOff-len(d.seg))
	d.segOpn = false
	if _, ok := out.(*SyntaxError); ok {
		sticky = true
	}
	if sticky {
		d.err = out
		d.close()
	}
	return out
}

// noteHooks records that the document just read gave a node to an Unmarshaler.
// The program may keep that node, and it reads the buffer, so the buffer must
// never be written again; fill moves the unread bytes to a new one instead.
func (d *Decoder) noteHooks() {
	if d.s != nil && d.s.b.Hooks() != d.hooks {
		d.shared = true
		d.hooks = d.s.b.Hooks()
	}
}

func (d *Decoder) close() {
	if d.s != nil {
		d.s.release()
		sessionPool.Put(d.s)
		d.s = nil
	}
	d.buf, d.seg = nil, nil
}

// countLines counts line breaks the way positions do: LF, CRLF and a lone CR.
func countLines(b []byte) int {
	n := 0
	for i := 0; i < len(b); i++ {
		switch b[i] {
		case '\n':
			n++
		case '\r':
			if i+1 >= len(b) || b[i+1] != '\n' {
				n++
			}
		}
	}
	return n
}

// fill reads more input into the buffer. It sets eof when the reader ends.
func (d *Decoder) fill() error {
	if d.eof {
		return nil
	}
	if d.shared {
		// A kept node may point into the old buffer: leave it as it is.
		d.shared = false
		nb := make([]byte, len(d.buf)-d.start, len(d.buf)-d.start+readChunk)
		copy(nb, d.buf[d.start:])
		d.buf, d.scanned, d.probe, d.start = nb, d.scanned-d.start, d.probe-d.start, 0
	}
	if d.start > 0 && d.start == len(d.buf) {
		d.buf, d.start, d.scanned, d.probe = d.buf[:0], 0, 0, 0
	} else if d.start > readChunk && d.start > len(d.buf)/2 {
		n := copy(d.buf, d.buf[d.start:])
		d.buf, d.scanned, d.probe, d.start = d.buf[:n], d.scanned-d.start, d.probe-d.start, 0
	}
	if cap(d.buf)-len(d.buf) < readChunk {
		nb := make([]byte, len(d.buf), 2*cap(d.buf)+readChunk)
		copy(nb, d.buf)
		d.buf = nb
	}
	n, err := d.r.Read(d.buf[len(d.buf) : len(d.buf)+readChunk])
	d.buf = d.buf[:len(d.buf)+n]
	d.total += n
	if max := d.cfg.limits.MaxInputBytes; max > 0 && d.total > max {
		return limitErr(LimitMaxInputBytes, "input size", max, "")
	}
	if err == io.EOF {
		d.eof = true
		return nil
	}
	return err
}

// convertAll reads the rest of the input and converts it to UTF-8.
func (d *Decoder) convertAll() error {
	for !d.eof {
		if err := d.fill(); err != nil {
			return err
		}
	}
	out, _, err := encoding.Convert(d.buf[d.start:])
	if err != nil {
		return convertError(err, out, &d.cfg, 0, 0)
	}
	d.buf, d.start, d.scanned, d.probe = out, 0, 0, 0
	return nil
}

// nextSegment returns the bytes of the next document of the stream, ending at
// the marker that starts the next document or after a "..." line. It returns
// io.EOF when nothing is left.
func (d *Decoder) nextSegment() ([]byte, error) {
	if !d.checked {
		for !d.eof && len(d.buf)-d.start < 4 {
			if err := d.fill(); err != nil {
				return nil, err
			}
		}
		d.checked = true
		if enc, _ := encoding.Detect(d.buf[d.start:]); enc != encoding.UTF8 {
			if err := d.convertAll(); err != nil {
				return nil, err
			}
		}
	}
	d.seen = false
	for {
		// Examine complete lines after d.scanned.
		for d.scanned < len(d.buf) {
			end, ok := d.lineLen()
			if !ok {
				break // wait for the rest of the line
			}
			line := d.buf[d.scanned : d.scanned+end]
			switch classifyLine(line) {
			case lineStart:
				if d.seen {
					return d.take(d.scanned), nil
				}
				d.seen = true
			case lineEnd:
				d.scanned += end
				return d.take(d.scanned), nil
			case lineContent:
				d.seen = true
			}
			d.scanned += end
		}
		if d.eof {
			if d.start >= len(d.buf) {
				return nil, io.EOF
			}
			return d.take(len(d.buf)), nil
		}
		if err := d.fill(); err != nil {
			return nil, err
		}
	}
}

// lineLen returns the length of the line that starts at d.scanned, its break
// included. A line ends at LF, CRLF or a lone CR, as the scanner reads them. It
// reports false when the buffer ends inside the line and more input may come.
// A search that finds nothing is remembered in d.probe, so the next one starts
// where this stopped and a long line is searched once in total.
func (d *Decoder) lineLen() (int, bool) {
	buf := d.buf
	from := max(d.scanned, d.probe)
	stop := len(buf)
	if nl := bytes.IndexByte(buf[from:], '\n'); nl >= 0 {
		stop = from + nl
	}
	d.examined += stop - from + 1
	if cr := bytes.IndexByte(buf[from:stop], '\r'); cr >= 0 {
		i := from + cr
		d.examined += cr + 1
		switch {
		case i+1 < len(buf):
			if buf[i+1] == '\n' {
				i++
			}
			return i + 1 - d.scanned, true
		case d.eof:
			return i + 1 - d.scanned, true
		}
		d.probe = i // a CR at the very end may be half of a CRLF
		return 0, false
	}
	d.examined += stop - from
	if stop < len(buf) {
		return stop + 1 - d.scanned, true
	}
	if d.eof {
		return stop - d.scanned, true
	}
	d.probe = stop
	return 0, false
}

// take consumes buf[start:end] and returns it.
func (d *Decoder) take(end int) []byte {
	seg := d.buf[d.start:end]
	d.start = end
	d.lineOff += countLines(seg)
	d.byteOff += len(seg)
	return seg
}

type lineKind uint8

const (
	lineOther   lineKind = iota // blank, comment or directive
	lineContent                 // anything else
	lineStart                   // "---"
	lineEnd                     // "..."
)

func classifyLine(l []byte) lineKind {
	if len(l) >= 3 && (l[0] == '-' || l[0] == '.') && l[1] == l[0] && l[2] == l[0] {
		if len(l) == 3 || l[3] == ' ' || l[3] == '\t' || l[3] == '\n' || l[3] == '\r' {
			if l[0] == '-' {
				return lineStart
			}
			return lineEnd
		}
	}
	for _, c := range l {
		switch c {
		case ' ', '\t', '\r', '\n':
			continue
		case '#':
			return lineOther
		}
		if c == '%' && l[0] == '%' {
			return lineOther
		}
		return lineContent
	}
	return lineOther
}
