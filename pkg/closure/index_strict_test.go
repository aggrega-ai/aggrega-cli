// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import (
	"errors"
	"strings"
	"testing"
)

// TestStrictIndex_SourceTemplateHaltsOnAnAbsentMapKey drives a structured
// source coordinate's template at lock time: indexing a map with a key it does
// not hold halts naming the key, reported as the lock-time condition with the
// evaluation's report — key included — in its message, and a present key, a
// list position and the presence-testing helper this surface registers render as
// before.
func TestStrictIndex_SourceTemplateHaltsOnAnAbsentMapKey(t *testing.T) {
	t.Parallel()
	ctx := templateContext{
		Values: map[string]any{"version": "1.2.3", "channels": []any{"stable", "edge"}},
		Infra:  map[string]any{},
		Locals: map[string]any{},
	}
	const field = "recipe/helm/probe:1.0.0.spec.source.version"

	for _, tmpl := range []string{
		`{{ index .values "nothing" }}`,
		`{{ index .values "nothing" | printf "%v" }}`,
		`{{ if index .values "nothing" }}y{{ end }}`,
	} {
		out, err := evalSourceTemplate(tmpl, ctx, field)
		if err == nil {
			t.Errorf("%s rendered %q; want a halt naming the key", tmpl, out)
			continue
		}
		if !errors.Is(err, ErrSourceTemplateAtLockTime()) {
			t.Errorf("%s: want the lock-time condition; got %v", tmpl, err)
		}
		if !strings.Contains(err.Error(), `key "nothing"`) {
			t.Errorf("%s: the halt does not name the key: %v", tmpl, err)
		}
	}

	for _, row := range []struct{ tmpl, want string }{
		{`{{ index .values "version" }}`, "1.2.3"},
		{`{{ index .values "channels" 1 }}`, "edge"},
		{`{{ optional .values "nothing" "0.0.0" }}`, "0.0.0"},
	} {
		out, err := evalSourceTemplate(row.tmpl, ctx, field)
		if err != nil {
			t.Errorf("%s halted: %v", row.tmpl, err)
			continue
		}
		if out != row.want {
			t.Errorf("%s rendered %q, want %q", row.tmpl, out, row.want)
		}
	}
}
