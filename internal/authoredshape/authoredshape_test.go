// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package authoredshape

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestOf_NamesEveryShapeInTheAuthorsWords covers the decoded-value reading,
// through a pointer and an interface, and a nil on the way.
func TestOf_NamesEveryShapeInTheAuthorsWords(t *testing.T) {
	t.Parallel()
	text := "x"
	var nilMap *map[string]any
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"nothing", nil, "no value"},
		{"a nil pointer", nilMap, "no value"},
		{"an integer", 3, "a number"},
		{"a decimal", 2.5, "a number"},
		{"an authored number", json.Number("7"), "a number"},
		{"text", "x", "text"},
		{"text behind a pointer", &text, "text"},
		{"true or false", true, "a true/false value"},
		{"a list", []any{1}, "a list"},
		{"a map", map[string]any{"a": 1}, "a map"},
		{"something else", struct{}{}, "a value that is not a number, text, a true/false value, a list or a map"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Of(tc.in); got != tc.want {
				t.Errorf("Of(%#v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestOfGoType_NamesTheShapeAPositionTakes covers the decoder's spelling of the type
// it was asked to fill: lists by what they hold, maps however they are stored,
// named types by their own name in words.
func TestOfGoType_NamesTheShapeAPositionTakes(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"[]types.VolumeMount", "a list of volume mounts"},
		{"*[]types.Probe", "a list of probes"},
		{"[]types.HTTPGetAction", "a list of http get actions"},
		{"[]types.Policy", "a list of policies"},
		{"[]types.Address", "a list of addresses"},
		{"[]string", "a list of text values"},
		{"[]int", "a list of numbers"},
		{"[]interface {}", "a list of values"},
		{"map[string]interface {}", "a map"},
		{"types.OrderedMap[github.com/x/y.ContainerSpec]", "a map"},
		{"string", "text"},
		{"int64", "a number"},
		{"bool", "a true/false value"},
		{"interface {}", "a value"},
		{"types.ContainerSecurityContext", "a container security context"},
		{"types.Ingress", "an ingress"},
	}
	for _, tc := range cases {
		if got := OfGoType(tc.in); got != tc.want {
			t.Errorf("OfGoType(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestDescribeYAMLDecodeError_ReportsWhatWasWrittenAndWhatIsExpected decodes real
// documents into a typed shape, so the decoder's own report is what is reworded:
// no Go type and no YAML tag survives, the line and a quoted value do, and the
// decoder's error is still what the result unwraps to.
func TestDescribeYAMLDecodeError_ReportsWhatWasWrittenAndWhatIsExpected(t *testing.T) {
	t.Parallel()
	type volumeMount struct {
		Name string `yaml:"name"`
	}
	type doc struct {
		VolumeMounts []volumeMount `yaml:"volumeMounts"`
		Port         int           `yaml:"port"`
	}
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a map where a list is expected",
			src:  "volumeMounts:\n  config: /etc/config\n",
			want: "line 2: a map is written where a list of volume mounts is expected",
		},
		{
			name: "text where a number is expected keeps the quoted value",
			src:  "port: abc\n",
			want: "line 1: text `abc` is written where a number is expected",
		},
		{
			name: "two entries are both reported",
			src:  "volumeMounts: 3\nport: [1]\n",
			want: "line 1: a number `3` is written where a list of volume mounts is expected; line 2: a list is written where a number is expected",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var d doc
			raw := yaml.Unmarshal([]byte(tc.src), &d)
			if raw == nil {
				t.Fatal("the document must fail to decode")
			}
			got := DescribeYAMLDecodeError(raw)
			if got.Error() != tc.want {
				t.Errorf("got  %q\nwant %q", got.Error(), tc.want)
			}
			for _, leak := range []string{"!!", "unmarshal", "volumeMount", "[]", "int"} {
				if strings.Contains(got.Error(), leak) {
					t.Errorf("the report still carries %q: %q", leak, got.Error())
				}
			}
			var te *yaml.TypeError
			if !errors.As(got, &te) {
				t.Errorf("the reworded error must still unwrap to the decoder's report")
			}
		})
	}
}

// TestDescribeYAMLDecodeError_LeavesEveryOtherErrorAlone pins the pass-through:
// nil, a syntax error and an error of the caller's own are returned as they are.
func TestDescribeYAMLDecodeError_LeavesEveryOtherErrorAlone(t *testing.T) {
	t.Parallel()
	if DescribeYAMLDecodeError(nil) != nil {
		t.Error("nil must stay nil")
	}
	var v map[string]any
	syntax := yaml.Unmarshal([]byte("a: [\n"), &v)
	if got := DescribeYAMLDecodeError(syntax); got != syntax {
		t.Errorf("a syntax error must pass through unchanged: %v", got)
	}
	own := errors.New("own")
	if got := DescribeYAMLDecodeError(own); got != own {
		t.Errorf("another error must pass through unchanged: %v", got)
	}
}

// TestOfYAMLNode_NamesTheShapeANodeWasWrittenIn covers the node reading.
func TestOfYAMLNode_NamesTheShapeANodeWasWrittenIn(t *testing.T) {
	t.Parallel()
	cases := []struct{ src, want string }{
		{"a: 1\n", "a map"},
		{"- 1\n", "a list"},
		{"8080\n", "a number"},
		{"hello\n", "text"},
		{"true\n", "a true/false value"},
		{"null\n", "no value"},
	}
	for _, tc := range cases {
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(tc.src), &n); err != nil {
			t.Fatalf("parse %q: %v", tc.src, err)
		}
		if got := OfYAMLNode(&n); got != tc.want {
			t.Errorf("OfYAMLNode(%q) = %q, want %q", tc.src, got, tc.want)
		}
	}
}
