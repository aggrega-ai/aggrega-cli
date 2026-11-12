// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestSelfInjectValueOwnKey holds the reading of one value: template to the own
// value it delivers: named by its top-level key wherever every reference the
// template makes reads that one own value, however deeply and in whichever
// spelling, and empty for everything else.
func TestSelfInjectValueOwnKey(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		text string
		want string
	}{
		{"one value, read directly", "{{ .values.role }}", "values.role"},
		{"one value, read through $", "{{ $.values.role }}", "values.role"},
		{"one value, surrounded by text and a function", "prefix-{{ .values.role | upper }}", "values.role"},
		{"one value, read through a with body", "{{ with .values.role }}{{ . }}{{ end }}", "values.role"},
		{"one value, guarded and read", "{{ if .values.role }}{{ .values.role }}{{ end }}", "values.role"},
		{"one value, a field nested inside it", "{{ .values.cfg.port }}", "values.cfg"},
		{"one value, handed to index", `{{ index .values.cfg "port" }}`, "values.cfg"},
		{"one value, a field off a function over it", "{{ (default dict .values.cfg).port }}", "values.cfg"},
		{"one value, two fields inside it", "{{ .values.cfg.host }}:{{ .values.cfg.port }}", "values.cfg"},
		{"a literal", "batch", ""},
		{"an empty value", "", ""},
		{"two values", "{{ .values.role }}-{{ .values.goal }}", ""},
		{"a value and infra", "{{ .values.role }}-{{ .infra.size }}", ""},
		{"a value and the node's identity", "{{ .values.role }}-{{ .context.instance_id }}", ""},
		{"metadata only", "{{ .metadata.release_name }}", ""},
		{"the context only", "{{ .context.instance_id }}", ""},
		{"the values handed to a function by their root", `{{ index .values "role" }}`, ""},
		{"the values read whole", "{{ toJson .values }}", ""},
		// A with block or a variable over the whole values pillar reads the pillar
		// whole in its pipeline, so no single value is attributed.
		{"the values rebound by a with block", "{{ with .values }}{{ .role }}{{ end }}", ""},
		{"the values bound to a variable", "{{ $v := .values }}{{ $v.role }}", ""},
		{"the whole context handed on", "{{ toJson . }}", ""},
		{"a template that does not parse", "{{ .values.role ", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := selfInjectValueOwnKey(tc.text); got != tc.want {
				t.Errorf("selfInjectValueOwnKey(%q) = %q; want %q", tc.text, got, tc.want)
			}
		})
	}
}

// TestSelfInjectOwnKeys_EveryMechanismInAuthorOrder holds the map to the block it
// reads: every mechanism the block names is present, the typed ones first and the
// custom ones sorted, each mechanism's names in the order the author wrote them —
// deliberately not alphabetical here, so a sorted result fails — and from: names
// the own provides key it delivers.
func TestSelfInjectOwnKeys_EveryMechanismInAuthorOrder(t *testing.T) {
	t.Parallel()
	entry := func(fields ...string) map[string]any {
		m := map[string]any{}
		for i := 0; i+1 < len(fields); i += 2 {
			m[fields[i]] = fields[i+1]
		}
		return m
	}
	block := &types.InjectBlock{
		Env: []any{
			entry("in", "ZETA_HOST", "from", "host"),
			entry("in", "ALPHA_ROLE", "value", "{{ .values.role }}"),
			entry("in", "MODE", "value", "batch"),
			entry("value", "{{ .values.goal }}"), // no in: — not a delivery, skipped
			// Both halves: refused before any render, read the way the delivery reads it.
			entry("in", "BOTH", "from", "host", "value", "{{ .values.role }}"),
		},
		File: []any{
			entry("in", "/etc/app/goal", "value", "{{ .values.goal }}"),
		},
	}
	block.Additional.Set("ssm_param", []any{entry("in", "/app/role", "value", "{{ .values.role }}")})
	block.Additional.Set("aws_secret", []any{entry("in", "token", "from", "token")})

	got := selfInjectOwnKeys(block)

	if want := []string{"env", "file", "aws_secret", "ssm_param"}; !reflect.DeepEqual(got.Keys(), want) {
		t.Errorf("mechanisms = %v; want %v", got.Keys(), want)
	}
	for _, mech := range []struct {
		name  string
		keys  []string
		owned []string
	}{
		{"env", []string{"ZETA_HOST", "ALPHA_ROLE", "MODE", "BOTH"}, []string{"provides.host", "values.role", "", "provides.host"}},
		{"file", []string{"/etc/app/goal"}, []string{"values.goal"}},
		{"aws_secret", []string{"token"}, []string{"provides.token"}},
		{"ssm_param", []string{"/app/role"}, []string{"values.role"}},
	} {
		m, ok := got.Get(mech.name)
		if !ok {
			t.Errorf("mechanism %q is missing", mech.name)
			continue
		}
		if !reflect.DeepEqual(m.Keys(), mech.keys) {
			t.Errorf("%s names = %v; want %v, in the order written", mech.name, m.Keys(), mech.keys)
		}
		for i, k := range mech.keys {
			if v, _ := m.Get(k); v != mech.owned[i] {
				t.Errorf("%s[%s] = %q; want %q", mech.name, k, v, mech.owned[i])
			}
		}
	}
}

// TestSelfInjectOwnKeys_NoBlockIsAnEmptyMap holds the absent block to an empty,
// present map, so a template reading the key never meets nothing.
func TestSelfInjectOwnKeys_NoBlockIsAnEmptyMap(t *testing.T) {
	t.Parallel()
	got := selfInjectOwnKeys(nil)
	if got.Len() != 0 {
		t.Errorf("selfInjectOwnKeys(nil) = %v; want empty", got.Keys())
	}
}
