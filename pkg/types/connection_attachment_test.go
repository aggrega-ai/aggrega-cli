// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"encoding/json"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestVerboseUsesEntry_Features_RoundTrip pins the attachments a connection on a
// component's own block carries through every decode and encode path: YAML and
// JSON, in both directions, keeping each attachment's type, description and
// values and the author order of the attachments.
//
// Both marshalers are asserted for the reason TestVerboseUsesEntry_CarriesNoInfraSurface
// gives: each path declares its own struct, so a field restored to one alone would
// leave the other reporting green.
func TestVerboseUsesEntry_Features_RoundTrip(t *testing.T) {
	t.Parallel()

	check := func(t *testing.T, label string, e VerboseUsesEntry) {
		t.Helper()
		if e.Target != "gpt" {
			t.Errorf("%s: Target = %q, want gpt", label, e.Target)
		}
		if got := e.Features.Keys(); len(got) != 2 || got[0] != "credential" || got[1] != "audit" {
			t.Fatalf("%s: attachment order = %v, want [credential audit]", label, got)
		}
		cred, _ := e.Features.Get("credential")
		if cred.Type != "core/referenced-secret" || cred.Description != "the key this connection presents" {
			t.Errorf("%s: credential = %+v", label, cred)
		}
		if v, ok := cred.Values.Get("origin"); !ok || v != "team-a" {
			t.Errorf("%s: credential.values.origin = %v (ok=%v), want team-a", label, v, ok)
		}
	}

	const yamlSrc = `{target: gpt, features: {credential: {type: core/referenced-secret, description: "the key this connection presents", values: {origin: team-a, key: api-key}}, audit: {type: core/audit-logging}}}`
	const jsonSrc = `{"target":"gpt","features":{"credential":{"type":"core/referenced-secret","description":"the key this connection presents","values":{"origin":"team-a","key":"api-key"}},"audit":{"type":"core/audit-logging"}}}`

	var y VerboseUsesEntry
	if err := yaml.Unmarshal([]byte(yamlSrc), &y); err != nil {
		t.Fatalf("YAML unmarshal: %v", err)
	}
	check(t, "YAML decode", y)
	yOut, err := yaml.Marshal(y)
	if err != nil {
		t.Fatalf("YAML marshal: %v", err)
	}
	var y2 VerboseUsesEntry
	if err := yaml.Unmarshal(yOut, &y2); err != nil {
		t.Fatalf("YAML re-unmarshal: %v", err)
	}
	check(t, "YAML round trip", y2)

	var j VerboseUsesEntry
	if err := json.Unmarshal([]byte(jsonSrc), &j); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}
	check(t, "JSON decode", j)
	jOut, err := json.Marshal(j)
	if err != nil {
		t.Fatalf("JSON marshal: %v", err)
	}
	var j2 VerboseUsesEntry
	if err := json.Unmarshal(jOut, &j2); err != nil {
		t.Fatalf("JSON re-unmarshal: %v", err)
	}
	check(t, "JSON round trip", j2)

	// An entry carrying only attachments is not empty, and one carrying none emits
	// no features key on either path.
	onlyFeatures := VerboseUsesEntry{}
	onlyFeatures.Features.Set("credential", ConnectionAttachment{Type: "core/referenced-secret"})
	if onlyFeatures.IsZero() {
		t.Error("an entry whose only content is an attachment must not be zero")
	}
	plain := VerboseUsesEntry{Target: "gpt"}
	py, _ := yaml.Marshal(plain)
	pj, _ := json.Marshal(plain)
	if strings.Contains(string(py), "features") || strings.Contains(string(pj), "features") {
		t.Errorf("an entry with no attachments must emit no features key; got YAML %q JSON %q", py, pj)
	}
}

// TestFeatureUsesEntry_CarriesNoAttachmentSurface pins the type-level half of the
// rule that a connection a Feature draws carries no attachments of its own. The
// Aggregate schema refuses the key on that surface before a document reaches this
// type; what is asserted here is that the type has nowhere to put one either: an
// attachment in the source reaches no field, no encoding emits one, and an entry
// whose only source key was an attachment block is empty.
func TestFeatureUsesEntry_CarriesNoAttachmentSurface(t *testing.T) {
	t.Parallel()

	const yamlSrc = `{target: gpt, values: {mode: ro}, features: {credential: {type: core/referenced-secret}}}`
	var y FeatureUsesEntry
	if err := yaml.Unmarshal([]byte(yamlSrc), &y); err != nil {
		t.Fatalf("YAML unmarshal: %v", err)
	}
	if y.Target != "gpt" {
		t.Errorf("Target = %q, want gpt", y.Target)
	}
	if v, ok := y.Values.Get("mode"); !ok || v != "ro" {
		t.Errorf("values.mode = %v (ok=%v), want ro", v, ok)
	}
	yOut, err := yaml.Marshal(y)
	if err != nil {
		t.Fatalf("YAML marshal: %v", err)
	}
	var j FeatureUsesEntry
	if err := json.Unmarshal([]byte(`{"target":"gpt","features":{"credential":{"type":"core/referenced-secret"}}}`), &j); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}
	jOut, err := json.Marshal(j)
	if err != nil {
		t.Fatalf("JSON marshal: %v", err)
	}
	for label, out := range map[string][]byte{"MarshalYAML": yOut, "MarshalJSON": jOut} {
		if strings.Contains(string(out), "features") {
			t.Errorf("%s emitted a features key; a connection a Feature draws carries none:\n%s", label, out)
		}
	}

	var onlyFeatures FeatureUsesEntry
	if err := yaml.Unmarshal([]byte(`{features: {credential: {type: core/referenced-secret}}}`), &onlyFeatures); err != nil {
		t.Fatalf("YAML unmarshal (features-only): %v", err)
	}
	if !onlyFeatures.IsZero() {
		t.Errorf("an entry whose only source key was features should be zero; got %+v", onlyFeatures)
	}

	var bare FeatureUsesEntry
	if err := yaml.Unmarshal([]byte(`gpt`), &bare); err == nil {
		t.Error("a bare-string alias value must be refused, as it is on a component's own block")
	}
}

// TestFeatureUsesSocketValue_RoundTrip pins both authoring forms of a socket on a
// Feature's block through YAML and JSON, and the null and empty cases, under the
// decoding rules a component's own socket follows.
func TestFeatureUsesSocketValue_RoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		yamlIn      string
		jsonIn      string
		wantCompact int
		wantVerbose int
	}{
		{name: "compact form", yamlIn: `["db1", "db2"]`, jsonIn: `["db1","db2"]`, wantCompact: 2},
		{name: "verbose form", yamlIn: `{peer: {target: corp-db, values: {pool: 5}}}`, jsonIn: `{"peer":{"target":"corp-db","values":{"pool":5}}}`, wantVerbose: 1},
		{name: "null is zero", yamlIn: `null`, jsonIn: `null`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, path := range []string{"yaml", "json"} {
				var v FeatureUsesSocketValue
				var err error
				if path == "yaml" {
					err = yaml.Unmarshal([]byte(tc.yamlIn), &v)
				} else {
					err = json.Unmarshal([]byte(tc.jsonIn), &v)
				}
				if err != nil {
					t.Fatalf("%s unmarshal: %v", path, err)
				}
				if len(v.Compact) != tc.wantCompact || v.Verbose.Len() != tc.wantVerbose {
					t.Fatalf("%s: compact=%d verbose=%d, want %d/%d", path, len(v.Compact), v.Verbose.Len(), tc.wantCompact, tc.wantVerbose)
				}
				if v.IsZero() != (tc.wantCompact == 0 && tc.wantVerbose == 0) {
					t.Errorf("%s: IsZero = %v", path, v.IsZero())
				}
				var out []byte
				if path == "yaml" {
					out, err = yaml.Marshal(v)
				} else {
					out, err = json.Marshal(v)
				}
				if err != nil {
					t.Fatalf("%s marshal: %v", path, err)
				}
				var back FeatureUsesSocketValue
				if path == "yaml" {
					err = yaml.Unmarshal(out, &back)
				} else {
					err = json.Unmarshal(out, &back)
				}
				if err != nil {
					t.Fatalf("%s re-unmarshal: %v", path, err)
				}
				if len(back.Compact) != tc.wantCompact || back.Verbose.Len() != tc.wantVerbose {
					t.Errorf("%s round trip: compact=%d verbose=%d", path, len(back.Compact), back.Verbose.Len())
				}
			}
		})
	}

	if err := yaml.Unmarshal([]byte(`just-a-string`), new(FeatureUsesSocketValue)); err == nil {
		t.Error("a non-empty scalar socket value must be refused")
	}
	if err := json.Unmarshal([]byte(`42`), new(FeatureUsesSocketValue)); err == nil {
		t.Error("a number socket value must be refused")
	}
}

// TestFeatureUsesConnections_ProjectsWithoutInventingAttachments pins the one
// projection every shared consumer reads a Feature's block through: socket and
// alias order survive, each connection keeps its target, inject and values, and
// no connection gains an attachment.
func TestFeatureUsesConnections_ProjectsWithoutInventingAttachments(t *testing.T) {
	t.Parallel()

	var uses OrderedMap[FeatureUsesSocketValue]
	if err := yaml.Unmarshal([]byte(`
zeta: [z1]
alpha:
  second: {target: s, values: {k: v}, inject: {env: [{in: A, from: host}]}}
  first: {target: f}
`), &uses); err != nil {
		t.Fatalf("decode: %v", err)
	}

	got := FeatureUsesConnections(uses)
	if keys := got.Keys(); len(keys) != 2 || keys[0] != "zeta" || keys[1] != "alpha" {
		t.Fatalf("socket order = %v, want [zeta alpha]", keys)
	}
	zeta, _ := got.Get("zeta")
	if len(zeta.Compact) != 1 || zeta.Compact[0] != "z1" {
		t.Errorf("compact form = %v, want [z1]", zeta.Compact)
	}
	alpha, _ := got.Get("alpha")
	if keys := alpha.Verbose.Keys(); len(keys) != 2 || keys[0] != "second" || keys[1] != "first" {
		t.Fatalf("alias order = %v, want [second first]", keys)
	}
	second, _ := alpha.Verbose.Get("second")
	if second.Target != "s" || second.Inject == nil {
		t.Errorf("second = %+v; want its target and inject carried", second)
	}
	if v, _ := second.Values.Get("k"); v != "v" {
		t.Errorf("second.values.k = %v, want v", v)
	}
	for alias, e := range alpha.Verbose.All() {
		if e.Features.Len() != 0 {
			t.Errorf("the projection invented an attachment on %q: %v", alias, e.Features.Keys())
		}
	}
	empty := FeatureUsesConnections(OrderedMap[FeatureUsesSocketValue]{})
	if empty.Len() != 0 {
		t.Error("an empty block must project to an empty one")
	}
}

// TestJoinConnectionAttachmentName pins the one spelling of the name an
// attachment a connection carries takes on the consuming component.
func TestJoinConnectionAttachmentName(t *testing.T) {
	t.Parallel()
	cases := []struct{ alias, attachment, want string }{
		{"gpt", "credential", "gpt.credential"},
		{"nim-b", "api_key", "nim-b.api_key"},
	}
	for _, tc := range cases {
		if got := JoinConnectionAttachmentName(tc.alias, tc.attachment); got != tc.want {
			t.Errorf("JoinConnectionAttachmentName(%q, %q) = %q, want %q", tc.alias, tc.attachment, got, tc.want)
		}
	}
}

// TestReleaseUsesEntry_Features_RoundTrip pins the attachments a Release
// connection entry carries through YAML and JSON in both directions: type,
// description and infra survive, author order is kept, and there is no values
// field for a Release to write into.
func TestReleaseUsesEntry_Features_RoundTrip(t *testing.T) {
	t.Parallel()

	check := func(t *testing.T, label string, e ReleaseUsesEntry) {
		t.Helper()
		if got := e.Features.Keys(); len(got) != 2 || got[0] != "credential" || got[1] != "extra" {
			t.Fatalf("%s: attachment order = %v, want [credential extra]", label, got)
		}
		cred, _ := e.Features.Get("credential")
		if v, ok := cred.Infra.Get("rotation"); !ok || v != "hourly" {
			t.Errorf("%s: credential.infra.rotation = %v (ok=%v), want hourly", label, v, ok)
		}
		extra, _ := e.Features.Get("extra")
		if extra.Type != "core/referenced-secret" || extra.Description != "only the Release makes it" {
			t.Errorf("%s: extra = %+v", label, extra)
		}
	}

	const yamlSrc = `{target: gpt, features: {credential: {infra: {rotation: hourly}}, extra: {type: core/referenced-secret, description: "only the Release makes it"}}}`
	var y ReleaseUsesEntry
	if err := yaml.Unmarshal([]byte(yamlSrc), &y); err != nil {
		t.Fatalf("YAML unmarshal: %v", err)
	}
	check(t, "YAML decode", y)
	yOut, err := yaml.Marshal(y)
	if err != nil {
		t.Fatalf("YAML marshal: %v", err)
	}
	var y2 ReleaseUsesEntry
	if err := yaml.Unmarshal(yOut, &y2); err != nil {
		t.Fatalf("YAML re-unmarshal: %v", err)
	}
	check(t, "YAML round trip", y2)

	jOut, err := json.Marshal(y)
	if err != nil {
		t.Fatalf("JSON marshal: %v", err)
	}
	var j ReleaseUsesEntry
	if err := json.Unmarshal(jOut, &j); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}
	check(t, "JSON round trip", j)

	if strings.Contains(string(yOut), "values") || strings.Contains(string(jOut), "values") {
		t.Errorf("a Release connection attachment has no values pillar to emit; got YAML %q JSON %q", yOut, jOut)
	}
}

// TestUsesSchemaEntry_FeaturesSchema_RoundTrip pins the slots a Component socket
// declares through YAML and JSON in both directions, in author order, each with the
// slot shape a Component's own featuresSchema takes; and that a socket declaring
// none emits no featuresSchema key.
func TestUsesSchemaEntry_FeaturesSchema_RoundTrip(t *testing.T) {
	t.Parallel()

	check := func(t *testing.T, label string, e UsesSchemaEntry) {
		t.Helper()
		if got := e.FeaturesSchema.Keys(); len(got) != 2 || got[0] != "credential" || got[1] != "trust" {
			t.Fatalf("%s: slot order = %v, want [credential trust]", label, got)
		}
		cred, _ := e.FeaturesSchema.Get("credential")
		if !cred.Sensitive || cred.Selector != "feature.aggrega.ai/kind=secret" ||
			len(cred.Inject) != 1 || cred.Inject[0] != "env" || !cred.Required || cred.Description != "the key" {
			t.Errorf("%s: credential slot = %+v", label, cred)
		}
	}

	const yamlSrc = `
selector: "component.aggrega.ai/category=ai-model"
featuresSchema:
  credential:
    required: true
    selector: "feature.aggrega.ai/kind=secret"
    sensitive: true
    inject: [env]
    description: the key
  trust: {}
`
	var y UsesSchemaEntry
	if err := yaml.Unmarshal([]byte(yamlSrc), &y); err != nil {
		t.Fatalf("YAML unmarshal: %v", err)
	}
	check(t, "YAML decode", y)
	yOut, err := yaml.Marshal(y)
	if err != nil {
		t.Fatalf("YAML marshal: %v", err)
	}
	var y2 UsesSchemaEntry
	if err := yaml.Unmarshal(yOut, &y2); err != nil {
		t.Fatalf("YAML re-unmarshal: %v", err)
	}
	check(t, "YAML round trip", y2)

	jOut, err := json.Marshal(y)
	if err != nil {
		t.Fatalf("JSON marshal: %v", err)
	}
	var j UsesSchemaEntry
	if err := json.Unmarshal(jOut, &j); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}
	check(t, "JSON round trip", j)

	none := UsesSchemaEntry{Selector: "x=y"}
	ny, _ := yaml.Marshal(none)
	nj, _ := json.Marshal(none)
	if strings.Contains(string(ny), "featuresSchema") || strings.Contains(string(nj), "featuresSchema") {
		t.Errorf("a socket declaring no slots must emit no featuresSchema key; got YAML %q JSON %q", ny, nj)
	}
}
