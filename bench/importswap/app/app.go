// Package app is a small program written against yaml.v3. The import-swap test
// copies it with only the import path changed.
package app

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Server is one entry of the configuration.
type Server struct {
	Name    string            `yaml:"name"`
	Port    int               `yaml:"port"`
	TLS     bool              `yaml:"tls,omitempty"`
	Timeout time.Duration     `yaml:"timeout"`
	Labels  map[string]string `yaml:"labels,omitempty"`
	Aliases []string          `yaml:"aliases,flow"`
}

// Config is the whole file.
type Config struct {
	Version int      `yaml:"version"`
	Servers []Server `yaml:"servers"`
	Extra   yaml.Node
}

// Level is a custom scalar with its own decoding.
type Level int

// UnmarshalYAML reads "low", "mid" or "high".
func (l *Level) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	switch strings.ToLower(s) {
	case "low":
		*l = 1
	case "mid":
		*l = 2
	case "high":
		*l = 3
	default:
		return fmt.Errorf("line %d: unknown level %q", n.Line, s)
	}
	return nil
}

// MarshalYAML writes the level as its name.
func (l Level) MarshalYAML() (interface{}, error) {
	return [...]string{"none", "low", "mid", "high"}[l], nil
}

// Load reads a configuration.
func Load(data []byte) (*Config, error) {
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Dump writes a configuration.
func Dump(c *Config) ([]byte, error) { return yaml.Marshal(c) }

// Stream decodes every document of a stream into a generic value.
func Stream(r io.Reader) ([]interface{}, error) {
	dec := yaml.NewDecoder(r)
	var out []interface{}
	for {
		var v interface{}
		err := dec.Decode(&v)
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		out = append(out, v)
	}
}

// Rename changes the name of the first server in the text, keeping comments.
func Rename(data []byte, name string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "servers" {
			continue
		}
		first := root.Content[i+1].Content[0]
		for j := 0; j+1 < len(first.Content); j += 2 {
			if first.Content[j].Value == "name" {
				first.Content[j+1].Value = name
			}
		}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), enc.Close()
}
