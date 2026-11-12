// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package gotmpl

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"text/template"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// A reference is marked printed only where an action prints it as it stands at a
// point the template reaches every time it runs. Every spelling of such a print is
// marked; a print inside a body, a function's argument, an assignment, a guard's
// test and an iteration are not.
func TestCollectRootRefs_PrintedMarksOnlyAPrintTheTemplateAlwaysReaches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		tmpl    string
		segs    string
		printed bool
	}{
		{"a dot print", `{{ .values.user }}`, "values.user", true},
		{"a print through $", `{{ $.values.user }}`, "values.user", true},
		{"a parenthesised print", `{{ (.values.user) }}`, "values.user", true},
		{"a field off a parenthesised root", `{{ (.values).user }}`, "values.user", true},
		{"a field off a parenthesised root through $", `{{ ($.values).user }}`, "values.user", true},
		{"a print through a bound variable", `{{ $u := .values.user }}{{ $u }}`, "values.user", true},
		{"a print after a guard has closed", `{{ if .values.user }}y{{ end }}{{ .values.user }}`, "values.user", true},
		{"the assignment alone", `{{ $u := .values.user }}`, "values.user", false},
		{"a piped function", `{{ .values.user | quote }}`, "values.user", false},
		{"a function argument", `{{ quote .values.user }}`, "values.user", false},
		{"a guard's test", `{{ if .values.user }}y{{ end }}`, "values.user", false},
		{"a print inside an if body", `{{ if .values.user }}{{ .values.user }}{{ end }}`, "values.user", false},
		{"a print inside an else branch", `{{ if .values.flag }}{{ else }}{{ .values.user }}{{ end }}`, "values.user", false},
		{"a print inside a with body", `{{ with .metadata }}{{ $.values.user }}{{ end }}`, "values.user", false},
		{"a print inside a range body", `{{ range .values.list }}{{ $.values.user }}{{ end }}`, "values.user", false},
		{"a print inside a range's else branch", `{{ range .values.list }}{{ else }}{{ $.values.user }}{{ end }}`, "values.user", false},
		{"an iteration alone", `{{ range .values.user }}x{{ end }}`, "values.user", false},
		{"the root itself", `{{ . }}{{ .values.user }}`, "values.user", true},
	}
	for _, tc := range cases {
		var found *RootRef
		refs := CollectRootRefs(mustParseForRoots(t, tc.tmpl))
		for i := range refs {
			if strings.Join(refs[i].Segs, ".") == tc.segs {
				found = &refs[i]
			}
		}
		if found == nil {
			t.Errorf("%s: %q recorded no entry for %s: %v", tc.name, tc.tmpl, tc.segs, refPaths(refs))
			continue
		}
		if found.Printed != tc.printed {
			t.Errorf("%s: %q marked %s Printed = %v, want %v", tc.name, tc.tmpl, tc.segs, found.Printed, tc.printed)
		}
	}
}

// A path the template prints where it always runs and also iterates is refused when
// the data holds nothing there, naming the path as the template wrote it first and
// leaving the data untouched. A path holding a value, a guarded or otherwise
// unprinted read, a path whose parent does not resolve and a root the surface does
// not define are not refused.
func TestSeedAbsentLeafKeys_AbsentPathPrintedAndIteratedHalts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		tmpl     string
		data     map[string]any
		defined  []string
		wantPath string // empty: no refusal
	}{
		{"printed then ranged, absent", `{{ .values.user }}{{ range .values.user }}x{{ end }}`,
			map[string]any{"values": map[string]any{}}, nil, ".values.user"},
		{"printed, then ranged through $", `{{ .values.user }}{{ range $.values.user }}x{{ end }}`,
			map[string]any{"values": map[string]any{}}, nil, ".values.user"},
		{"ranged through $, then printed", `{{ range $.values.user }}x{{ end }}{{ .values.user }}`,
			map[string]any{"values": map[string]any{}}, nil, "$.values.user"},
		{"printed and ranged, the leaf nil", `{{ .values.user }}{{ range .values.user }}x{{ end }}`,
			map[string]any{"values": map[string]any{"user": nil}}, nil, ".values.user"},
		{"printed through a variable and ranged through it", `{{ $u := .values.user }}{{ $u }}{{ range $u }}x{{ end }}`,
			map[string]any{"values": map[string]any{}}, nil, ".values.user"},
		{"a defined root holding nothing", `{{ .workload }}{{ range .workload }}x{{ end }}`,
			map[string]any{}, []string{"workload"}, ".workload"},
		{"present, read both ways", `{{ .values.user }}{{ range .values.user }}x{{ end }}`,
			map[string]any{"values": map[string]any{"user": []any{"a"}}}, nil, ""},
		{"a guarded print", `{{ if .values.user }}{{ .values.user }}{{ end }}{{ range .values.user }}x{{ end }}`,
			map[string]any{"values": map[string]any{}}, nil, ""},
		{"a guard over the range", `{{ if .values.user }}{{ range .values.user }}x{{ end }}{{ end }}`,
			map[string]any{"values": map[string]any{}}, nil, ""},
		{"an assignment, then the range", `{{ $u := .values.user }}{{ range $u }}x{{ end }}`,
			map[string]any{"values": map[string]any{}}, nil, ""},
		{"a function argument", `{{ .values.user | quote }}{{ range .values.user }}x{{ end }}`,
			map[string]any{"values": map[string]any{}}, nil, ""},
		{"the parent absent", `{{ .values.a.b }}{{ range .values.a.b }}x{{ end }}`,
			map[string]any{"values": map[string]any{}}, nil, ""},
		{"a root the surface does not define", `{{ .undeclared }}{{ range .undeclared }}x{{ end }}`,
			map[string]any{}, nil, ""},
	}
	for _, tc := range cases {
		err := SeedAbsentLeafKeys(tc.data, CollectRootRefs(mustParseForRoots(t, tc.tmpl)), tc.defined)
		if tc.wantPath == "" {
			if err != nil {
				t.Errorf("%s: refused, want no refusal: %v", tc.name, err)
			}
			continue
		}
		if !errors.Is(err, ErrAbsentPathReadAndIterated()) {
			t.Errorf("%s: got %v, want ErrAbsentPathReadAndIterated", tc.name, err)
			continue
		}
		if code, ok := cerrors.CodeOf(err); !ok || code != "absent-path-read-and-iterated" {
			t.Errorf("%s: code %q (%v), want absent-path-read-and-iterated", tc.name, code, ok)
		}
		if !strings.Contains(err.Error(), "(path "+tc.wantPath+")") {
			t.Errorf("%s: %q does not name the path %s", tc.name, err.Error(), tc.wantPath)
		}
	}
	// A refusal writes nothing: an absent leaf read elsewhere in the same template
	// is not supplied either.
	data := map[string]any{"values": map[string]any{}}
	refs := CollectRootRefs(mustParseForRoots(t, `{{ .values.other }}{{ .values.user }}{{ range .values.user }}x{{ end }}`))
	if err := SeedAbsentLeafKeys(data, refs, nil); err == nil {
		t.Fatal("the template must be refused")
	}
	if got := len(data["values"].(map[string]any)); got != 0 {
		t.Errorf("a refused template's data holds %d supplied key(s), want none", got)
	}
}

// What the engine renders for the paths the refusal leaves alone, read off the
// engine itself: a present path read both ways prints and iterates as it always
// did, and a guarded print over an absent path renders nothing.
func TestSeedAbsentLeafKeys_PathsReadBothWaysThatAreNotRefusedRenderAsBefore(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		tmpl string
		data map[string]any
		want string
	}{
		{"present, read both ways", `{{ .values.user }}|{{ range .values.user }}-{{ . }}{{ end }}`,
			map[string]any{"values": map[string]any{"user": []any{"a", "b"}}}, "[a b]|-a-b"},
		{"absent, the print guarded", `{{ if .values.user }}{{ .values.user }}{{ end }}|{{ range .values.user }}x{{ end }}`,
			map[string]any{"values": map[string]any{}}, "|"},
	}
	for _, tc := range cases {
		tpl, err := template.New(tc.name).Option("missingkey=zero").Parse(tc.tmpl)
		if err != nil {
			t.Fatalf("%s: parse: %v", tc.name, err)
		}
		if err := SeedAbsentLeafKeys(tc.data, CollectRootRefs(tpl.Tree.Root), nil); err != nil {
			t.Errorf("%s: refused: %v", tc.name, err)
			continue
		}
		var out bytes.Buffer
		if err := tpl.Execute(&out, tc.data); err != nil {
			t.Errorf("%s: execution failed: %v", tc.name, err)
			continue
		}
		if out.String() != tc.want {
			t.Errorf("%s: rendered %q, want %q", tc.name, out.String(), tc.want)
		}
	}
}
