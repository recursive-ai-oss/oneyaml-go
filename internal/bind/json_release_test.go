package bind

import "testing"

func TestReleaseDropsJSONBuffer(t *testing.T) {
	b := New(Config{})
	b.jsonBuf = make([]byte, 0, 1<<20)
	b.Release()
	if b.jsonBuf != nil {
		t.Fatalf("a %d byte buffer was kept", cap(b.jsonBuf))
	}
	b.jsonBuf = append(make([]byte, 0, 1024), "secret"...)
	b.Release()
	if len(b.jsonBuf) != 0 || string(b.jsonBuf[:6]) != "\x00\x00\x00\x00\x00\x00" {
		t.Fatalf("old bytes kept: %q", b.jsonBuf[:6])
	}
}
