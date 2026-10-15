// Package secretscan looks for secrets and unwanted blobs in the objects a
// push would send: known secret shapes, executables and core dumps, and blobs
// over 1 MB. Findings name the object, path and kind, never the matched value.
package secretscan

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// MaxBlob is the largest blob allowed in the repository.
const MaxBlob = 1_000_000

// Shape names one secret pattern.
type Shape struct {
	Kind string
	RE   *regexp.Regexp
}

func frag(parts ...string) string { return strings.Join(parts, "") }

// Shapes are the secret shapes looked for. They are shapes only: each needs a
// long run of key characters, so these definitions do not match themselves.
var Shapes = []Shape{
	{"anthropic key", regexp.MustCompile(frag("sk-", "ant-") + `[A-Za-z0-9_-]{20,}`)},
	{"openai key", regexp.MustCompile(frag("sk-", "proj-") + `[A-Za-z0-9_-]{20,}|sk-[A-Za-z0-9]{40,}`)},
	{"aws access key id", regexp.MustCompile(`(?:AKIA|ASIA)[0-9A-Z]{16}`)},
	{"google api key", regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`)},
	{"github token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,}`)},
	{"gitlab token", regexp.MustCompile(`glpat-[A-Za-z0-9_-]{20,}`)},
	{"slack token", regexp.MustCompile(`xox[abprs]-[A-Za-z0-9-]{10,}`)},
	{"crates.io token", regexp.MustCompile(`cio[A-Za-z0-9]{28,}`)},
	{"elevenlabs key", regexp.MustCompile(`sk_[a-f0-9]{40,}`)},
	{"firecrawl key", regexp.MustCompile(`fc-[a-f0-9]{30,}`)},
	{"private key", regexp.MustCompile(frag("-----BEGIN [A-Z ]*", "PRIVATE KEY-----"))},
	{"secret assignment", regexp.MustCompile(`\b[A-Z][A-Z0-9_]*(?:API_KEY|SECRET|PASSWORD|TOKEN)\s*=\s*['"]?[A-Za-z0-9/+_.-]{16,}`)},
}

// Finding is one problem blob.
type Finding struct {
	Path string
	SHA  string
	Kind string
}

func (f Finding) String() string {
	sha := f.SHA
	if len(sha) > 12 {
		sha = sha[:12]
	}
	return fmt.Sprintf("%s (%s): %s", f.Path, sha, f.Kind)
}

// binaryMagic lists the first bytes of executables and core dumps.
var binaryMagic = []struct {
	name  string
	magic []byte
}{
	{"ELF binary or core dump", []byte("\x7fELF")},
	{"Mach-O binary", []byte{0xcf, 0xfa, 0xed, 0xfe}},
	{"Mach-O binary", []byte{0xce, 0xfa, 0xed, 0xfe}},
	{"Mach-O binary", []byte{0xfe, 0xed, 0xfa, 0xcf}},
	{"Mach-O binary", []byte{0xfe, 0xed, 0xfa, 0xce}},
}

// Inspect checks one blob. size is the blob's true size; body may be nil when
// the blob is too large to have been read.
func Inspect(path string, size int, body []byte) []string {
	if size > MaxBlob {
		return []string{fmt.Sprintf("larger than 1 MB (%d bytes)", size)}
	}
	for _, m := range binaryMagic {
		if bytes.HasPrefix(body, m.magic) {
			return []string{m.name}
		}
	}
	var kinds []string
	for _, s := range Shapes {
		if s.RE.Match(body) {
			kinds = append(kinds, s.Kind)
		}
	}
	return kinds
}

func gitOut(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// ScanRange scans every blob reachable from the given revisions and not from
// the excluded ones. Pass {"HEAD"} for a whole history, {"old..new"} for a
// push or {"--all"} for every ref. It returns the findings and the number of
// blobs scanned.
func ScanRange(dir string, revs ...string) ([]Finding, int, error) {
	out, err := gitOut(dir, append([]string{"rev-list", "--objects"}, revs...)...)
	if err != nil {
		return nil, 0, err
	}
	paths := map[string]string{}
	var order []string
	for _, line := range strings.Split(string(out), "\n") {
		sha, p, _ := strings.Cut(line, " ")
		if sha == "" {
			continue
		}
		if _, ok := paths[sha]; !ok {
			paths[sha] = p
			order = append(order, sha)
		}
	}
	if len(order) == 0 {
		return nil, 0, nil
	}

	cmd := exec.Command("git", "cat-file", "--batch")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(strings.Join(order, "\n") + "\n")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, 0, err
	}
	if err := cmd.Start(); err != nil {
		return nil, 0, err
	}
	rd := bufio.NewReaderSize(pipe, 1<<16)
	var res []Finding
	blobs := 0
	for range order {
		header, err := rd.ReadString('\n')
		if err != nil {
			return nil, 0, fmt.Errorf("reading git output: %w", err)
		}
		f := strings.Fields(header)
		if len(f) != 3 {
			return nil, 0, fmt.Errorf("unexpected git header %q", strings.TrimSpace(header))
		}
		n, err := strconv.Atoi(f[2])
		if err != nil {
			return nil, 0, err
		}
		var body []byte
		if f[1] == "blob" && n <= MaxBlob {
			body = make([]byte, n)
			if _, err := io.ReadFull(rd, body); err != nil {
				return nil, 0, err
			}
		} else if _, err := io.CopyN(io.Discard, rd, int64(n)); err != nil {
			return nil, 0, err
		}
		if _, err := rd.ReadByte(); err != nil { // trailing newline
			return nil, 0, err
		}
		if f[1] != "blob" {
			continue
		}
		blobs++
		for _, kind := range Inspect(paths[f[0]], n, body) {
			res = append(res, Finding{paths[f[0]], f[0], kind})
		}
	}
	if err := cmd.Wait(); err != nil {
		return nil, 0, err
	}
	return res, blobs, nil
}
