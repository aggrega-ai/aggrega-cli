// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"encoding/json"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestUsesSocketValue_RoundTrip_YAML(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		yamlIn         string
		wantCompactLen int
		wantVerboseLen int
		wantTarget     string // first verbose entry's target, when applicable
	}{
		{
			name:           "compact form",
			yamlIn:         `["db1", "db2"]`,
			wantCompactLen: 2,
		},
		{
			name:           "verbose form full",
			yamlIn:         `{"hr-db": {target: "corp-hr"}}`,
			wantVerboseLen: 1,
			wantTarget:     "corp-hr",
		},
		{
			name:   "null produces zero value",
			yamlIn: `null`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var v UsesSocketValue
			if err := yaml.Unmarshal([]byte(tc.yamlIn), &v); err != nil {
				t.Fatalf("YAML unmarshal: %v", err)
			}
			if len(v.Compact) != tc.wantCompactLen {
				t.Errorf("Compact len = %d, want %d", len(v.Compact), tc.wantCompactLen)
			}
			if v.Verbose.Len() != tc.wantVerboseLen {
				t.Errorf("Verbose len = %d, want %d", v.Verbose.Len(), tc.wantVerboseLen)
			}
			if tc.wantTarget != "" {
				keys := v.Verbose.Keys()
				if len(keys) == 0 {
					t.Fatalf("expected verbose entry, got none")
				}
				entry, _ := v.Verbose.Get(keys[0])
				if entry.Target != tc.wantTarget {
					t.Errorf("Target = %q, want %q", entry.Target, tc.wantTarget)
				}
			}

			// Round-trip: marshal back to YAML and unmarshal a second time;
			// the resulting value must equal the first unmarshal's result
			// in the populated-field's len.
			out, err := yaml.Marshal(&v)
			if err != nil {
				t.Fatalf("YAML marshal: %v", err)
			}
			var v2 UsesSocketValue
			if err := yaml.Unmarshal(out, &v2); err != nil {
				t.Fatalf("YAML re-unmarshal: %v\noutput: %s", err, out)
			}
			if len(v2.Compact) != len(v.Compact) || v2.Verbose.Len() != v.Verbose.Len() {
				t.Errorf("round-trip diverged: first=%+v second=%+v", v, v2)
			}
		})
	}
}

func TestUsesSocketValue_RoundTrip_JSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		jsonIn         string
		wantCompactLen int
		wantVerboseLen int
		wantTarget     string
	}{
		{
			name:           "compact form",
			jsonIn:         `["db1","db2"]`,
			wantCompactLen: 2,
		},
		{
			name:           "verbose form full",
			jsonIn:         `{"hr-db":{"target":"corp-hr"}}`,
			wantVerboseLen: 1,
			wantTarget:     "corp-hr",
		},
		{
			name:   "null produces zero value",
			jsonIn: `null`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var v UsesSocketValue
			if err := json.Unmarshal([]byte(tc.jsonIn), &v); err != nil {
				t.Fatalf("JSON unmarshal: %v", err)
			}
			if len(v.Compact) != tc.wantCompactLen {
				t.Errorf("Compact len = %d, want %d", len(v.Compact), tc.wantCompactLen)
			}
			if v.Verbose.Len() != tc.wantVerboseLen {
				t.Errorf("Verbose len = %d, want %d", v.Verbose.Len(), tc.wantVerboseLen)
			}
			if tc.wantTarget != "" {
				keys := v.Verbose.Keys()
				if len(keys) == 0 {
					t.Fatalf("expected verbose entry, got none")
				}
				entry, _ := v.Verbose.Get(keys[0])
				if entry.Target != tc.wantTarget {
					t.Errorf("Target = %q, want %q", entry.Target, tc.wantTarget)
				}
			}

			// JSON round-trip: marshal then unmarshal; populated-field's
			// len must agree.
			out, err := json.Marshal(&v)
			if err != nil {
				t.Fatalf("JSON marshal: %v", err)
			}
			var v2 UsesSocketValue
			if err := json.Unmarshal(out, &v2); err != nil {
				t.Fatalf("JSON re-unmarshal: %v\noutput: %s", err, out)
			}
			if len(v2.Compact) != len(v.Compact) || v2.Verbose.Len() != v.Verbose.Len() {
				t.Errorf("round-trip diverged: first=%+v second=%+v", v, v2)
			}
		})
	}
}

func TestVerboseUsesEntry_RoundTrip_YAML(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		yamlIn     string
		wantTarget string
		wantInject bool
	}{
		{
			name:       "mapping with target only",
			yamlIn:     `{target: corp-db}`,
			wantTarget: "corp-db",
		},
		{
			name:       "mapping with target and inject",
			yamlIn:     `{target: corp-db, inject: {env: {DB_URL: host}}}`,
			wantTarget: "corp-db",
			wantInject: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var e VerboseUsesEntry
			if err := yaml.Unmarshal([]byte(tc.yamlIn), &e); err != nil {
				t.Fatalf("YAML unmarshal: %v", err)
			}
			if e.Target != tc.wantTarget {
				t.Errorf("Target = %q, want %q", e.Target, tc.wantTarget)
			}
			if tc.wantInject {
				if e.Inject == nil {
					t.Errorf("expected Inject populated, got nil")
				}
			} else {
				if e.Inject != nil {
					t.Errorf("expected Inject nil, got %+v", e.Inject)
				}
			}

			// Round-trip — marshal back to YAML, re-unmarshal, compare
			// Target. Inject's deep equality is exercised by other
			// tests; here we only confirm round-trip preserves Target.
			out, err := yaml.Marshal(&e)
			if err != nil {
				t.Fatalf("YAML marshal: %v", err)
			}
			var e2 VerboseUsesEntry
			if err := yaml.Unmarshal(out, &e2); err != nil {
				t.Fatalf("YAML re-unmarshal: %v\noutput: %s", err, out)
			}
			if e2.Target != e.Target {
				t.Errorf("round-trip Target diverged: first=%q second=%q", e.Target, e2.Target)
			}
		})
	}
}

func TestVerboseUsesEntry_RoundTrip_JSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		jsonIn     string
		wantTarget string
		wantInject bool
	}{
		{
			name:       "object with target only",
			jsonIn:     `{"target":"corp-db"}`,
			wantTarget: "corp-db",
		},
		{
			name:       "object with target and inject",
			jsonIn:     `{"target":"corp-db","inject":{"env":{"DB_URL":"host"}}}`,
			wantTarget: "corp-db",
			wantInject: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var e VerboseUsesEntry
			if err := json.Unmarshal([]byte(tc.jsonIn), &e); err != nil {
				t.Fatalf("JSON unmarshal: %v", err)
			}
			if e.Target != tc.wantTarget {
				t.Errorf("Target = %q, want %q", e.Target, tc.wantTarget)
			}
			if tc.wantInject {
				if e.Inject == nil {
					t.Errorf("expected Inject populated, got nil")
				}
			} else {
				if e.Inject != nil {
					t.Errorf("expected Inject nil, got %+v", e.Inject)
				}
			}

			// Round-trip
			out, err := json.Marshal(&e)
			if err != nil {
				t.Fatalf("JSON marshal: %v", err)
			}
			var e2 VerboseUsesEntry
			if err := json.Unmarshal(out, &e2); err != nil {
				t.Fatalf("JSON re-unmarshal: %v\noutput: %s", err, out)
			}
			if e2.Target != e.Target {
				t.Errorf("round-trip Target diverged: first=%q second=%q", e.Target, e2.Target)
			}
		})
	}
}

// TestOrderedMapUsesSocketValue_RoundTrip_YAML pins the typed-V dispatch
// at the OrderedMap layer when V is UsesSocketValue. The OrderedMap's
// recursive decoder defers to V's UnmarshalYAML method via the V-method-
// set cache, so the typed-wrapper's polymorphic dispatch runs at every
// socket value.
func TestOrderedMapUsesSocketValue_RoundTrip_YAML(t *testing.T) {
	t.Parallel()

	yamlIn := `
databases: ["db1", "db2"]
caches:
  primary:
    target: redis-1
    inject:
      env:
        CACHE_URL: host
  fallback:
    target: redis-2
`
	var m OrderedMap[UsesSocketValue]
	if err := yaml.Unmarshal([]byte(yamlIn), &m); err != nil {
		t.Fatalf("YAML unmarshal: %v", err)
	}

	dbVal, ok := m.Get("databases")
	if !ok {
		t.Fatal("missing databases socket")
	}
	if len(dbVal.Compact) != 2 {
		t.Errorf("databases.Compact len = %d, want 2", len(dbVal.Compact))
	}
	if dbVal.Compact[0] != "db1" || dbVal.Compact[1] != "db2" {
		t.Errorf("databases.Compact = %v, want [db1 db2]", dbVal.Compact)
	}

	cVal, ok := m.Get("caches")
	if !ok {
		t.Fatal("missing caches socket")
	}
	if cVal.Verbose.Len() != 2 {
		t.Errorf("caches.Verbose len = %d, want 2", cVal.Verbose.Len())
	}
	primary, ok := cVal.Verbose.Get("primary")
	if !ok {
		t.Fatal("missing primary entry")
	}
	if primary.Target != "redis-1" {
		t.Errorf("primary.Target = %q, want redis-1", primary.Target)
	}
	if primary.Inject == nil {
		t.Errorf("primary.Inject = nil, want populated")
	}
	fallback, ok := cVal.Verbose.Get("fallback")
	if !ok {
		t.Fatal("missing fallback entry")
	}
	if fallback.Target != "redis-2" {
		t.Errorf("fallback.Target = %q, want redis-2 (bare-string shorthand)", fallback.Target)
	}

	// Verify outer-socket author order: databases, caches.
	keys := m.Keys()
	if len(keys) != 2 || keys[0] != "databases" || keys[1] != "caches" {
		t.Errorf("outer key order = %v, want [databases caches]", keys)
	}

	// Verify inner-alias author order under caches: primary, fallback.
	cKeys := cVal.Verbose.Keys()
	if len(cKeys) != 2 || cKeys[0] != "primary" || cKeys[1] != "fallback" {
		t.Errorf("verbose alias order = %v, want [primary fallback]", cKeys)
	}
}

// TestVerboseUsesEntry_ValuesInfra_RoundTrip pins connection-scoped per-edge
// values/infra through both marshalers: a verbose entry carrying values and
// infra round-trips (YAML and JSON) preserving the keys, the nested content,
// and author order, alongside target/inject.
func TestVerboseUsesEntry_ValuesInfra_RoundTrip(t *testing.T) {
	t.Parallel()
	const src = `{target: corp-db, values: {pool_size: 20, mode: ro}, infra: {storage_class: fast}}`

	checkEntry := func(t *testing.T, e VerboseUsesEntry) {
		if e.Target != "corp-db" {
			t.Errorf("Target = %q, want corp-db", e.Target)
		}
		if got := e.Values.Keys(); len(got) != 2 || got[0] != "pool_size" || got[1] != "mode" {
			t.Errorf("values key order = %v, want [pool_size mode]", got)
		}
		if v, ok := e.Values.Get("mode"); !ok || v != "ro" {
			t.Errorf("values.mode = %v (ok=%v), want ro", v, ok)
		}
		if v, ok := e.Infra.Get("storage_class"); !ok || v != "fast" {
			t.Errorf("infra.storage_class = %v (ok=%v), want fast", v, ok)
		}
	}

	// YAML: unmarshal -> marshal -> unmarshal, asserting both decoded forms.
	var y VerboseUsesEntry
	if err := yaml.Unmarshal([]byte(src), &y); err != nil {
		t.Fatalf("YAML unmarshal: %v", err)
	}
	checkEntry(t, y)
	yOut, err := yaml.Marshal(y)
	if err != nil {
		t.Fatalf("YAML marshal: %v", err)
	}
	var y2 VerboseUsesEntry
	if err := yaml.Unmarshal(yOut, &y2); err != nil {
		t.Fatalf("YAML re-unmarshal: %v", err)
	}
	checkEntry(t, y2)

	// JSON: same loop.
	var j VerboseUsesEntry
	if err := json.Unmarshal([]byte(`{"target":"corp-db","values":{"pool_size":20,"mode":"ro"},"infra":{"storage_class":"fast"}}`), &j); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}
	checkEntry(t, j)
	jOut, err := json.Marshal(j)
	if err != nil {
		t.Fatalf("JSON marshal: %v", err)
	}
	var j2 VerboseUsesEntry
	if err := json.Unmarshal(jOut, &j2); err != nil {
		t.Fatalf("JSON re-unmarshal: %v", err)
	}
	checkEntry(t, j2)

	// An entry with no values/infra stays zero (IsZero true for a bare target? no —
	// target is set; but values/infra must be empty and omitted from output).
	var empty VerboseUsesEntry
	_ = yaml.Unmarshal([]byte(`{target: x}`), &empty)
	if empty.Values.Len() != 0 || empty.Infra.Len() != 0 {
		t.Errorf("omitted values/infra should be empty; got values=%d infra=%d", empty.Values.Len(), empty.Infra.Len())
	}
}
