module oneyaml.dev/go/bench

go 1.25.0

require (
	github.com/evanphx/json-patch/v5 v5.9.11
	github.com/goccy/go-yaml v1.19.2
	github.com/palantir/pkg/yamlpatch v1.5.0
	go.yaml.in/yaml/v3 v3.0.5
	gopkg.in/yaml.v2 v2.4.0
	gopkg.in/yaml.v3 v3.0.1
	oneyaml.dev/go v0.0.0
	sigs.k8s.io/yaml v1.6.0
)

require (
	github.com/palantir/pkg v1.1.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
)

replace oneyaml.dev/go => ../
