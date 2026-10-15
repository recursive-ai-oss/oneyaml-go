package bench

import (
	"fmt"
	"strings"
	"testing"
)

type port struct {
	Name     string `yaml:"name" json:"name"`
	Port     int    `yaml:"port" json:"port"`
	Protocol string `yaml:"protocol,omitempty" json:"protocol,omitempty"`
}

type envVar struct {
	Name  string `yaml:"name" json:"name"`
	Value string `yaml:"value,omitempty" json:"value,omitempty"`
}

type container struct {
	Name     string            `yaml:"name" json:"name"`
	Image    string            `yaml:"image" json:"image"`
	Args     []string          `yaml:"args,omitempty" json:"args,omitempty"`
	Env      []envVar          `yaml:"env,omitempty" json:"env,omitempty"`
	Ports    []port            `yaml:"ports,omitempty" json:"ports,omitempty"`
	Labels   map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Replicas *int              `yaml:"replicas,omitempty" json:"replicas,omitempty"`
	Ratio    float64           `yaml:"ratio" json:"ratio"`
	Enabled  bool              `yaml:"enabled" json:"enabled"`
	Notes    string            `yaml:"notes,omitempty" json:"notes,omitempty"`
}

type podList struct {
	APIVersion string      `yaml:"apiVersion" json:"apiVersion"`
	Kind       string      `yaml:"kind" json:"kind"`
	Items      []container `yaml:"items" json:"items"`
}

func structWorkload() podList {
	pl := podList{APIVersion: "v1", Kind: "List"}
	for i := 0; i < 300; i++ {
		r := i%5 + 1
		c := container{
			Name:     fmt.Sprintf("service-%03d", i),
			Image:    fmt.Sprintf("registry.example.com/team/app-%d:v1.%d", i%7, i%13),
			Args:     []string{"--listen=:8080", "--log-level=info", "--config", "/etc/app/config.yaml"},
			Env:      []envVar{{"MODE", "yes"}, {"PATH", "/usr/bin:/bin"}, {"EMPTY", ""}},
			Ports:    []port{{"http", 80, "TCP"}, {"https", 443, ""}},
			Labels:   map[string]string{"app": "svc", "tier": "web", "version": fmt.Sprint(i)},
			Replicas: &r,
			Ratio:    float64(i) / 7,
			Enabled:  i%3 != 0,
		}
		if i%10 == 0 {
			c.Notes = strings.Repeat("line\n", 3)
		}
		pl.Items = append(pl.Items, c)
	}
	return pl
}

// BenchmarkDumpStruct measures Marshal of a typed value for each library.
func BenchmarkDumpStruct(b *testing.B) {
	v := structWorkload()
	for _, d := range dumpers {
		out, err := d.fn(v)
		if err != nil {
			b.Fatalf("%s: %v", d.name, err)
		}
		b.Run("structs/library="+strings.ReplaceAll(d.name, ".", "_"), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(out)))
			for b.Loop() {
				if _, err := d.fn(v); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
