// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"sort"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
)

// TestMapRecipeSourceTemplatesClassifiesEveryShape covers the single home for
// "which parts of a Recipe source carry a template". Every branch is exercised,
// and each case asserts BOTH halves of the walk's contract: which parts fn was
// offered, and what the rebuilt value looks like. A test asserting only the
// callback set would pass while the rebuild dropped a field, and the evaluation
// path depends on the rebuild.
func TestMapRecipeSourceTemplatesClassifiesEveryShape(t *testing.T) {
	t.Parallel()

	type visit struct {
		field string
		text  string
	}

	for _, tc := range []struct {
		name       string
		source     any
		wantFields []string
		wantResult any
	}{
		{
			name:       "nil source offers nothing",
			source:     nil,
			wantFields: nil,
			wantResult: nil,
		},
		{
			name:       "string source is one unnamed template",
			source:     "oci://example/chart:{{ .values.tag }}",
			wantFields: []string{redact.WholeSourceKey},
			wantResult: "seen:oci://example/chart:{{ .values.tag }}",
		},
		{
			name: "structured source offers its string fields only",
			source: map[string]any{
				"url":     "oci://example/chart",
				"version": "{{ .values.tag }}",
				"depth":   3,
				"flags":   []any{"a", "b"},
			},
			wantFields: []string{"url", "version"},
			wantResult: map[string]any{
				"url":     "seen:oci://example/chart",
				"version": "seen:{{ .values.tag }}",
				"depth":   3,
				"flags":   []any{"a", "b"},
			},
		},
		{
			name:       "an already-typed shape carries no template",
			source:     struct{ URL string }{URL: "oci://example/chart"},
			wantFields: nil,
			wantResult: struct{ URL string }{URL: "oci://example/chart"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var seen []visit
			got, err := mapRecipeSourceTemplates(tc.source, func(field, text string) (string, error) {
				seen = append(seen, visit{field, text})
				return "seen:" + text, nil
			})
			if err != nil {
				t.Fatalf("walk: %v", err)
			}

			fields := make([]string, 0, len(seen))
			for _, v := range seen {
				fields = append(fields, v.field)
			}
			sort.Strings(fields)
			wantFields := append([]string(nil), tc.wantFields...)
			sort.Strings(wantFields)
			if len(fields) != len(wantFields) {
				t.Fatalf("offered fields %v, want %v", fields, wantFields)
			}
			for i := range fields {
				if fields[i] != wantFields[i] {
					t.Fatalf("offered fields %v, want %v", fields, wantFields)
				}
			}

			// The whole-source template is named by the same spelling the display
			// withholds it under; a second spelling here would let the two
			// disagree about which key a string source lands on.
			if _, isString := tc.source.(string); isString && seen[0].field != redact.WholeSourceKey {
				t.Errorf("a string source must be offered under the whole-source key %q; got %q", redact.WholeSourceKey, seen[0].field)
			}

			assertSourceEqual(t, got, tc.wantResult)
		})
	}
}

// TestMapRecipeSourceTemplatesStopsOnCallbackError pins the failure path: an
// error from one field aborts the walk rather than being folded into a partially
// rebuilt source. The evaluation path relies on it — a half-rendered source
// reaching a provider guest would be worse than a halt.
func TestMapRecipeSourceTemplatesStopsOnCallbackError(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	got, err := mapRecipeSourceTemplates(map[string]any{"url": "x"}, func(string, string) (string, error) {
		return "", boom
	})
	if !errors.Is(err, boom) {
		t.Errorf("the callback's error must reach the caller unwrapped; got %v", err)
	}
	if got != nil {
		t.Errorf("a failed walk must return no rebuilt source; got %#v", got)
	}
}

func assertSourceEqual(t *testing.T, got, want any) {
	t.Helper()
	switch w := want.(type) {
	case nil:
		if got != nil {
			t.Errorf("rebuilt source = %#v, want nil", got)
		}
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("rebuilt source = %#v, want a map", got)
		}
		if len(g) != len(w) {
			t.Fatalf("rebuilt source = %#v, want %#v", g, w)
		}
		for k, wv := range w {
			gv, present := g[k]
			if !present {
				t.Errorf("rebuilt source dropped field %q", k)
				continue
			}
			if ws, isStr := wv.(string); isStr {
				if gv != ws {
					t.Errorf("rebuilt field %q = %#v, want %q", k, gv, ws)
				}
				continue
			}
			// Non-string fields must travel verbatim, identity included: a copy
			// that rebuilt them would be a second shape of the same data.
			if !sameNonString(gv, wv) {
				t.Errorf("rebuilt field %q = %#v, want %#v carried verbatim", k, gv, wv)
			}
		}
	default:
		if got != want {
			t.Errorf("rebuilt source = %#v, want %#v", got, want)
		}
	}
}

func sameNonString(got, want any) bool {
	gl, gok := got.([]any)
	wl, wok := want.([]any)
	if gok && wok {
		if len(gl) != len(wl) {
			return false
		}
		for i := range gl {
			if gl[i] != wl[i] {
				return false
			}
		}
		return true
	}
	return got == want
}
