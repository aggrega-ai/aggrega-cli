// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
)

// An Aggregate's text that prints a parameter and also iterates it halts when the
// Release left the parameter unfilled, under the condition that names that fault
// rather than the interpolation's own, naming the reference. A filled parameter read
// both ways, and a guarded print of an unfilled one, interpolate as they always did.
func TestInterpolateParameters_AbsentParameterPrintedAndIteratedHalts(t *testing.T) {
	t.Parallel()
	raw := []byte("hosts: \"{{ .parameters.hosts }}\"\nlist: \"{{ range .parameters.hosts }}-{{ . }}{{ end }}\"\n")

	_, err := InterpolateParameters(raw, map[string]any{})
	if !errors.Is(err, gotmpl.ErrAbsentPathReadAndIterated()) {
		t.Fatalf("got %v, want the absent-path-read-and-iterated halt", err)
	}
	if errors.Is(err, ErrInvalidParameterRef()) {
		t.Errorf("the halt must carry its own condition, not the interpolation's: %v", err)
	}
	var re *ResolverError
	if !errors.As(err, &re) || re.Entity != "Aggregate" {
		t.Errorf("the halt must be a finding on the Aggregate, got %#v", err)
	}
	if !strings.Contains(err.Error(), "(path .parameters.hosts)") {
		t.Errorf("the report does not name the reference: %v", err)
	}

	cases := []struct {
		name   string
		raw    string
		params map[string]any
		want   string
	}{
		{"filled, read both ways", string(raw), map[string]any{"hosts": []any{"a", "b"}},
			"hosts: \"[a b]\"\nlist: \"-a-b\"\n"},
		{"unfilled, the print guarded",
			"hosts: \"{{ if .parameters.hosts }}{{ .parameters.hosts }}{{ end }}\"\nlist: \"{{ range .parameters.hosts }}-{{ . }}{{ end }}\"\n",
			map[string]any{}, "hosts: \"\"\nlist: \"\"\n"},
	}
	for _, tc := range cases {
		out, err := InterpolateParameters([]byte(tc.raw), tc.params)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}
		if string(out) != tc.want {
			t.Errorf("%s: interpolated %q, want %q", tc.name, out, tc.want)
		}
	}
}
