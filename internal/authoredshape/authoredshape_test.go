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
			if errors.As(got, &te) || errors.Unwrap(got) != nil {
				t.Errorf("the reworded error must wrap nothing, so no surface rendering its causes reaches the decoder's report")
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

// TestOfGoType_AStructSpelledInFullIsAMap covers the decoder's spelling of an
// anonymous struct, which carries the program's field list and is a map to the
// author.
func TestOfGoType_AStructSpelledInFullIsAMap(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{`struct { Name string "yaml:\"name\""; Version string "yaml:\"version\"" }`, "a map"},
		{"*struct {}", "a map"},
		{"[]struct { A int }", "a list of maps"},
	}
	for _, tc := range cases {
		if got := OfGoType(tc.in); got != tc.want {
			t.Errorf("OfGoType(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// decoderText is what a reworded report must no longer carry: the decoders' own
// phrasing and the program's types.
var decoderText = []string{"cannot unmarshal", "!!", "Go struct field", "Go value", "json:", "struct {"}

// TestDescribeYAMLDecodeErrorIn_NamesTheFieldTheValueWasWrittenUnder holds the
// field naming to the document the decode read, one row per way a field is
// found or deliberately not named.
func TestDescribeYAMLDecodeErrorIn_NamesTheFieldTheValueWasWrittenUnder(t *testing.T) {
	t.Parallel()
	type header struct {
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
	}
	type spec struct {
		Spec struct {
			Replicas int `yaml:"replicas"`
		} `yaml:"spec"`
	}
	type items struct {
		Items []int `yaml:"items"`
	}
	type pair struct {
		A struct {
			X int `yaml:"x"`
			Y int `yaml:"y"`
		} `yaml:"a"`
	}
	cases := []struct {
		name    string
		doc     string
		into    func() any
		want    string
		notWant string
	}{
		{
			name: "a map field written as a list",
			doc:  "apiVersion: x\nkind: K\nmetadata:\n  - a\n",
			into: func() any { return &header{} },
			want: "line 4 (metadata): a list is written where a map is expected",
		},
		{
			name: "a nested number written as text",
			doc:  "spec:\n  replicas: abc\n",
			into: func() any { return &spec{} },
			want: "line 2 (spec.replicas): text `abc` is written where a number is expected",
		},
		{
			name: "a list item written as text",
			doc:  "items:\n  - 1\n  - abc\n",
			into: func() any { return &items{} },
			want: "line 3 (items[1]): text `abc` is written where a number is expected",
		},
		{
			name:    "two values of one shape on one line name the line alone",
			doc:     "a: {x: abc, y: def}\n",
			into:    func() any { return &pair{} },
			want:    "line 1: text `abc` is written where a number is expected; line 1: text `def` is written where a number is expected",
			notWant: "(a.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := yaml.Unmarshal([]byte(tc.doc), tc.into())
			if raw == nil {
				t.Fatalf("decoding %q succeeded; the row needs a mismatch", tc.doc)
			}
			got := DescribeYAMLDecodeErrorIn(raw, []byte(tc.doc))
			if got.Error() != tc.want {
				t.Errorf("got  %q\nwant %q", got.Error(), tc.want)
			}
			if tc.notWant != "" && strings.Contains(got.Error(), tc.notWant) {
				t.Errorf("%q names a field it cannot tell apart from another", got)
			}
			for _, leak := range decoderText {
				if strings.Contains(got.Error(), leak) {
					t.Errorf("%q still carries %q", got, leak)
				}
			}
			var te *yaml.TypeError
			if errors.As(got, &te) || errors.Unwrap(got) != nil {
				t.Errorf("the reworded report still wraps the decoder's error: %v", got)
			}
		})
	}

	t.Run("a document that does not parse names the line alone", func(t *testing.T) {
		t.Parallel()
		raw := yaml.Unmarshal([]byte("spec:\n  replicas: abc\n"), &spec{})
		got := DescribeYAMLDecodeErrorIn(raw, []byte("spec: : [\n"))
		if want := "line 2: text `abc` is written where a number is expected"; got.Error() != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("another error passes through unchanged", func(t *testing.T) {
		t.Parallel()
		own := errors.New("not a decode mismatch")
		if got := DescribeYAMLDecodeErrorIn(own, []byte("a: 1\n")); got != own {
			t.Errorf("another error must pass through unchanged: %v", got)
		}
	})
}

// TestDescribeYAMLDecodeErrorIn_NamesAKeyWrittenAsAListOrAMap holds the
// decoder's refusal of a non-text key — spelled as a Go value, with no line — to
// the key's line, the field holding it and its shape. One fault per row, and a
// document showing no such key leaves the refusal as it was.
func TestDescribeYAMLDecodeErrorIn_NamesAKeyWrittenAsAListOrAMap(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"a key written as a list", "services:\n  web:\n    depends_on:\n      ? [db]\n      : {condition: x}\n",
			"line 4 (services.web.depends_on): reading a key: a list is written where text is expected"},
		{"a key written as a map", "services:\n  web:\n    labels:\n      ? {a: 1}\n      : x\n",
			"line 4 (services.web.labels): reading a key: a map is written where text is expected"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var into map[string]any
			raw := yaml.Unmarshal([]byte(tc.doc), &into)
			if raw == nil || !strings.HasPrefix(raw.Error(), invalidMapKeyPrefix) {
				t.Fatalf("the row needs the decoder's non-text-key refusal; got %v", raw)
			}
			got := DescribeYAMLDecodeErrorIn(raw, []byte(tc.doc))
			if got.Error() != tc.want {
				t.Errorf("got  %q\nwant %q", got.Error(), tc.want)
			}
			for _, leak := range []string{"interface {}", "invalid map key"} {
				if strings.Contains(got.Error(), leak) {
					t.Errorf("%q still carries %q", got, leak)
				}
			}
			if errors.Is(got, raw) || errors.Unwrap(got) != nil {
				t.Errorf("the reworded report still wraps the decoder's error: %v", got)
			}
		})
	}

	t.Run("a document showing no such key leaves the refusal as it was", func(t *testing.T) {
		t.Parallel()
		var into map[string]any
		doc := "services:\n  web:\n    depends_on:\n      ? [db]\n      : x\n"
		raw := yaml.Unmarshal([]byte(doc), &into)
		if got := DescribeYAMLDecodeErrorIn(raw, []byte("a: 1\n")); got != raw {
			t.Errorf("got %v, want the decoder's own refusal unchanged", got)
		}
	})
}

// kindName is a type of its own over text, the way a request field is often
// declared.
type kindName string

// TestDescribeJSONDecodeError_NamesTheFieldAndTheShapes holds the JSON rewording
// to the field the value was sent under and the two shapes, one row per shape of
// mismatch.
func TestDescribeJSONDecodeError_NamesTheFieldAndTheShapes(t *testing.T) {
	t.Parallel()
	type kind struct {
		Kind string `json:"kind"`
	}
	type nested struct {
		A struct {
			B int `json:"b"`
		} `json:"a"`
	}
	type named struct {
		K kindName `json:"k"`
	}
	type list struct {
		Xs []string `json:"xs"`
	}
	cases := []struct {
		name string
		doc  string
		into func() any
		want string
	}{
		{"a number sent as text's field", `{"kind": 5}`, func() any { return &kind{} }, `"kind": a number is written where text is expected`},
		{"a nested field", `{"a": {"b": "x"}}`, func() any { return &nested{} }, `"a.b": text is written where a number is expected`},
		{"a field of a type of its own over text", `{"k": true}`, func() any { return &named{} }, `"k": a true/false value is written where text is expected`},
		{"a list field sent a number", `{"xs": 1}`, func() any { return &list{} }, `"xs": a number is written where a list of text values is expected`},
		{"the whole object sent as a list", `[1]`, func() any { return &kind{} }, `a list is written where a map is expected`},
		{"a map sent where text goes", `{"kind": {}}`, func() any { return &kind{} }, `"kind": a map is written where text is expected`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := json.Unmarshal([]byte(tc.doc), tc.into())
			if raw == nil {
				t.Fatalf("decoding %s succeeded; the row needs a mismatch", tc.doc)
			}
			got := DescribeJSONDecodeError(raw)
			if got.Error() != tc.want {
				t.Errorf("got  %q\nwant %q", got.Error(), tc.want)
			}
			for _, leak := range decoderText {
				if strings.Contains(got.Error(), leak) {
					t.Errorf("%q still carries %q", got, leak)
				}
			}
			var te *json.UnmarshalTypeError
			if errors.As(got, &te) || errors.Unwrap(got) != nil {
				t.Errorf("the reworded report still wraps the decoder's error: %v", got)
			}
		})
	}

	t.Run("a syntax error passes through unchanged", func(t *testing.T) {
		t.Parallel()
		raw := json.Unmarshal([]byte(`{"kind":`), &kind{})
		if got := DescribeJSONDecodeError(raw); got != raw {
			t.Errorf("a syntax error must pass through unchanged: %v", got)
		}
	})
}
