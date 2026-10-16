package oneyaml_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	oneyaml "oneyaml.dev/go"
)

// A limit that is hit matches ErrLimit and the sentinel of that one limit, and
// Limit names it for a switch.
func ExampleLimitError() {
	deep := strings.Repeat("[", 100) + strings.Repeat("]", 100)
	err := oneyaml.Unmarshal([]byte(deep), new(any), oneyaml.Limits(oneyaml.LimitSet{MaxDepth: 50}))

	fmt.Println(errors.Is(err, oneyaml.ErrLimit), errors.Is(err, oneyaml.ErrMaxDepth), errors.Is(err, oneyaml.ErrMaxInputBytes))
	var le *oneyaml.LimitError
	if errors.As(err, &le) {
		switch le.Limit {
		case oneyaml.LimitMaxDepth:
			fmt.Println("too deep:", le.Max, le.Option)
		}
	}
	// Output:
	// true true false
	// too deep: 50 Limits (MaxDepth)
}

// A syntax error matches ErrSyntax, and the sentinel of its category when it has
// a particular one.
func ExampleErrDuplicateKey() {
	err := oneyaml.Unmarshal([]byte("a: 1\na: 2\n"), new(any))
	fmt.Println(errors.Is(err, oneyaml.ErrSyntax), errors.Is(err, oneyaml.ErrDuplicateKey), errors.Is(err, oneyaml.ErrEncoding))
	// Output: true true false
}

// A TypeError carries the Go type involved as a reflect.Type.
func ExampleTypeError_goType() {
	var v struct{ Wait time.Duration }
	err := oneyaml.Unmarshal([]byte("wait: [1]\n"), &v)
	var te *oneyaml.TypeError
	if errors.As(err, &te) {
		fmt.Println(te.GoType == reflect.TypeFor[time.Duration](), te.GoType)
	}
	// Output: true time.Duration
}
