package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

const sample = `# fleet
version: 2
servers:
  # the first one
  - name: alpha   # primary
    port: 8_080
    tls: yes
    timeout: 90s
    labels: {tier: web}
    aliases: [a1, a2]
  - name: beta
    port: 0x1F90
    timeout: 1m
    aliases: []
extra: {k: v}
`

func TestLoad(t *testing.T) {
	c, err := Load([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != 2 || len(c.Servers) != 2 {
		t.Fatalf("got %+v", c)
	}
	a := c.Servers[0]
	if a.Name != "alpha" || a.Port != 8080 || !a.TLS || a.Timeout != 90*time.Second || a.Labels["tier"] != "web" {
		t.Fatalf("got %+v", a)
	}
	if c.Servers[1].Port != 8080 {
		t.Fatalf("hex port: %+v", c.Servers[1])
	}
	if c.Extra.Kind != yaml.MappingNode || len(c.Extra.Content) != 2 {
		t.Fatalf("extra: %+v", c.Extra)
	}
}

func TestLoadReportsTypeErrors(t *testing.T) {
	_, err := Load([]byte("version: x\nservers: []\n"))
	var te *yaml.TypeError
	if !errors.As(err, &te) || len(te.Errors) != 1 {
		t.Fatalf("err = %v", err)
	}
}

func TestDump(t *testing.T) {
	out, err := Dump(&Config{Version: 1, Servers: []Server{{Name: "a", Port: 1, Aliases: nil}}})
	if err != nil {
		t.Fatal(err)
	}
	want := "version: 1\nservers:\n    - name: a\n      port: 1\n      timeout: 0s\n      aliases: []\nextra: null\n"
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestStream(t *testing.T) {
	docs, err := Stream(strings.NewReader("a: 1\n---\n[1, 2]\n---\nplain\n"))
	if err != nil || len(docs) != 3 {
		t.Fatalf("got %v, %v", docs, err)
	}
}

func TestRename(t *testing.T) {
	out, err := Rename([]byte(sample), "omega")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "name: omega") || !strings.Contains(string(out), "# the first one") || !strings.Contains(string(out), "# primary") {
		t.Fatalf("comments or edit lost:\n%s", out)
	}
}

func TestLevel(t *testing.T) {
	var v struct{ L Level }
	if err := yaml.Unmarshal([]byte("l: HIGH\n"), &v); err != nil || v.L != 3 {
		t.Fatalf("got %+v, %v", v, err)
	}
	if err := yaml.Unmarshal([]byte("l: nope\n"), &v); err == nil || !strings.Contains(err.Error(), "unknown level") {
		t.Fatalf("err = %v", err)
	}
	out, err := yaml.Marshal(v)
	if err != nil || string(out) != "l: high\n" {
		t.Fatalf("got %q, %v", out, err)
	}
}
