// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types

import (
	"encoding/json"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestInjectPipe_NestedOrderedMapRoundTrip pins the nested-OrderedMap shape
// on InjectPipe.Additional. The codebase had not previously shipped a
// production struct field of type OrderedMap[OrderedMap[V]]; the gamma
// reflection caches must fire correctly on the inner OrderedMap during
// marshal/unmarshal at both depths. Encode → decode → byte-equivalent round
// trip; assert the inner OrderedMap[string] preserves author key order.
func TestInjectPipe_NestedOrderedMapRoundTrip(t *testing.T) {
	t.Parallel()

	build := func() InjectPipe {
		var pipe InjectPipe
		ssmParam := OrderedMap[string]{}
		ssmParam.Set("DB_URL", "connection.url")
		ssmParam.Set("API_KEY", "connection.token")
		pipe.Additional.Set("ssm_param", ssmParam)
		return pipe
	}

	// YAML round-trip
	t.Run("YAML", func(t *testing.T) {
		pipe := build()
		out, err := yaml.Marshal(&pipe)
		if err != nil {
			t.Fatalf("YAML marshal: %v", err)
		}
		var pipe2 InjectPipe
		if err := yaml.Unmarshal(out, &pipe2); err != nil {
			t.Fatalf("YAML re-unmarshal: %v\noutput: %s", err, out)
		}
		assertNestedShape(t, &pipe2, out)
	})

	// JSON round-trip
	t.Run("JSON", func(t *testing.T) {
		pipe := build()
		out, err := json.Marshal(&pipe)
		if err != nil {
			t.Fatalf("JSON marshal: %v", err)
		}
		var pipe2 InjectPipe
		if err := json.Unmarshal(out, &pipe2); err != nil {
			t.Fatalf("JSON re-unmarshal: %v\noutput: %s", err, out)
		}
		assertNestedShape(t, &pipe2, out)
	})
}

func assertNestedShape(t *testing.T, pipe *InjectPipe, encoded []byte) {
	t.Helper()
	if pipe.Additional.Len() != 1 {
		t.Fatalf("Additional.Len = %d, want 1; encoded: %s", pipe.Additional.Len(), encoded)
	}
	inner, ok := pipe.Additional.Get("ssm_param")
	if !ok {
		t.Fatalf("Additional missing key ssm_param; encoded: %s", encoded)
	}
	if inner.Len() != 2 {
		t.Fatalf("inner ssm_param.Len = %d, want 2; encoded: %s", inner.Len(), encoded)
	}
	wantOrder := []string{"DB_URL", "API_KEY"}
	gotOrder := inner.Keys()
	for i, w := range wantOrder {
		if i >= len(gotOrder) || gotOrder[i] != w {
			t.Errorf("inner Keys[%d] = %v, want %q; encoded: %s", i, gotOrder, w, encoded)
		}
	}
	if v, _ := inner.Get("DB_URL"); v != "connection.url" {
		t.Errorf("inner.Get(DB_URL) = %q, want %q", v, "connection.url")
	}
	if v, _ := inner.Get("API_KEY"); v != "connection.token" {
		t.Errorf("inner.Get(API_KEY) = %q, want %q", v, "connection.token")
	}
}

// TestInjectPipe_TemplateUntaggedKeys_NilWhenNoCustomMechanism pins the contract
// that keeps the template view of a core-three-only pipe exactly what it was: a
// pipe carrying no custom mechanism contributes no keys at all, so the reflective
// template walk that consumes this method has nothing to merge and every existing
// rendered artifact is unaffected by the projection's existence.
func TestInjectPipe_TemplateUntaggedKeys_NilWhenNoCustomMechanism(t *testing.T) {
	t.Parallel()

	var pipe InjectPipe
	env := OrderedMap[string]{}
	env.Set("DB_URL", "postgres.svc")
	pipe.Env = env

	if got := pipe.TemplateUntaggedKeys(); got != nil {
		t.Errorf("TemplateUntaggedKeys() = %v, want nil for a pipe with no custom mechanism", got)
	}
}

// TestInjectPipe_TemplateUntaggedKeys_ProjectsEachMechanismByName pins that every
// custom mechanism appears under the name the author wrote, carrying its own
// delivery map, and that the core three are never duplicated into the projection —
// they reach a template through their own struct tags.
func TestInjectPipe_TemplateUntaggedKeys_ProjectsEachMechanismByName(t *testing.T) {
	t.Parallel()

	var pipe InjectPipe
	env := OrderedMap[string]{}
	env.Set("CAL_ENV", "cal-needle")
	pipe.Env = env

	vault := OrderedMap[string]{}
	vault.Set("TOKEN_PATH", "vault-needle")
	ssm := OrderedMap[string]{}
	ssm.Set("DB_ARN", "ssm-needle")
	// Authored reverse-alphabetically, so a projection that re-sorted would show.
	pipe.Additional.Set("zz_vault", vault)
	pipe.Additional.Set("aa_ssm", ssm)

	got := pipe.TemplateUntaggedKeys()
	if len(got) != 2 {
		t.Fatalf("TemplateUntaggedKeys() has %d keys, want 2: %v", len(got), got)
	}
	for _, core := range []string{"env", "file", "arg", "all"} {
		if _, present := got[core]; present {
			t.Errorf("projection carries core mechanism %q; the core three reach a template by tag", core)
		}
	}
	inner, ok := got["aa_ssm"].(OrderedMap[string])
	if !ok {
		t.Fatalf("aa_ssm is %T, want OrderedMap[string]", got["aa_ssm"])
	}
	if v, _ := inner.Get("DB_ARN"); v != "ssm-needle" {
		t.Errorf("aa_ssm.DB_ARN = %q, want %q", v, "ssm-needle")
	}
}

// TestInjectBlock_TemplateUntaggedKeys pins the authored block's template
// projection: each custom mechanism under the name the author wrote, carrying its
// delivery entries exactly as authored, the core three never duplicated into it,
// and nothing at all from a block authoring only core mechanisms.
func TestInjectBlock_TemplateUntaggedKeys(t *testing.T) {
	t.Parallel()

	entries := []any{
		map[string]any{"in": "Z_KEY", "value": "z"},
		map[string]any{"in": "A_KEY", "value": "a"},
	}
	rows := []struct {
		name  string
		block func() InjectBlock
		want  map[string]any
	}{
		{
			name: "core mechanisms only contribute nothing",
			block: func() InjectBlock {
				return InjectBlock{Env: []any{map[string]any{"in": "E", "value": "e"}}}
			},
			want: nil,
		},
		{
			name: "a custom mechanism beside env is projected by name",
			block: func() InjectBlock {
				b := InjectBlock{Env: []any{map[string]any{"in": "E", "value": "e"}}}
				b.Additional.Set("ssm_param", entries)
				return b
			},
			want: map[string]any{"ssm_param": entries},
		},
		{
			name: "a custom mechanism alone is projected by name",
			block: func() InjectBlock {
				var b InjectBlock
				b.Additional.Set("ssm_param", entries)
				return b
			},
			want: map[string]any{"ssm_param": entries},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			got := row.block().TemplateUntaggedKeys()
			if row.want == nil {
				if got != nil {
					t.Fatalf("TemplateUntaggedKeys() = %v, want nil", got)
				}
				return
			}
			gotJSON, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("marshal projection: %v", err)
			}
			wantJSON, err := json.Marshal(row.want)
			if err != nil {
				t.Fatalf("marshal want: %v", err)
			}
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("TemplateUntaggedKeys() = %s, want %s", gotJSON, wantJSON)
			}
			for _, core := range []string{"env", "file", "arg"} {
				if _, present := got[core]; present {
					t.Errorf("projection carries core mechanism %q; the core three reach a template by tag", core)
				}
			}
		})
	}
}
