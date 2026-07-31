// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/serve/completion"
)

func parseDoc(t *testing.T, s string) *yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(s), &n); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return &n
}

// TestEnvelopeDiagnosticsForBuffer_PositionBetterThan1To1 pins that an
// envelope finding squiggles the offending key, not line 1: a Release
// with values: on a boundary component lands the diagnostic on the
// boundary path (line > 1) via the Tier-A position walk, and carries the
// rendered envelope framing.
func TestEnvelopeDiagnosticsForBuffer_PositionBetterThan1To1(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	content := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: r
  environment: dev
  version: "1.0.0"
spec:
  aggregate: a
  manifest: m
  boundaries:
    main:
      directory: ./d
      components:
        api:
          values:
            port: 9999
`)
	diags, has := srv.envelopeDiagnosticsForBuffer("release.yaml", content, parseDoc(t, string(content)))
	if !has || len(diags) == 0 {
		t.Fatalf("expected envelope findings; has=%v diags=%d", has, len(diags))
	}
	d := diags[0]
	if d.Line <= 1 {
		t.Errorf("diagnostic should land on the offending key (line > 1), not line 1; got line %d", d.Line)
	}
	if !strings.Contains(d.Message, "violates its envelope schema") {
		t.Errorf("diagnostic must carry the envelope framing; got %q", d.Message)
	}
}

// TestEnvelopeDiagnosticsForBuffer_NonAggregaGuard pins the guard at the
// unit level: foreign YAML and unknown kinds produce no envelope leg
// activity (has=false), so the daemon never lint-bombs them.
func TestEnvelopeDiagnosticsForBuffer_NonAggregaGuard(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	cases := map[string]string{
		"k8s-manifest": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: x\n",
		"compose-file": "services:\n  web:\n    image: nginx\n",
		"unknown-kind": "apiVersion: aggrega.ai/v1alpha1\nkind: Nonsense\nspec: {}\n",
		"bare-yaml":    "just: data\n",
	}
	for name, content := range cases {
		name, content := name, content
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			diags, has := srv.envelopeDiagnosticsForBuffer("doc.yaml", []byte(content), parseDoc(t, content))
			if has || diags != nil {
				t.Errorf("%s: foreign/unknown doc must not engage the envelope leg; has=%v diags=%d", name, has, len(diags))
			}
		})
	}
}

// TestEnvelopeDiagnosticsForBuffer_CleanFallsThrough pins that a
// well-formed aggrega document produces no findings (has=false), so the
// handler falls through to its existing flow (Tier-4 / cascade) unchanged.
func TestEnvelopeDiagnosticsForBuffer_CleanFallsThrough(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	content := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: a
  version: "1.0.0"
spec:
  components: {}
`)
	if diags, has := srv.envelopeDiagnosticsForBuffer("aggregate.yaml", content, parseDoc(t, string(content))); has || diags != nil {
		t.Errorf("clean aggrega doc must fall through (has=false); has=%v diags=%d", has, len(diags))
	}
}

// TestEnvelopeDiagnosticsForBuffer_ConfigByFilename pins that the
// workspace config — a flat-top-level document with no apiVersion+kind
// envelope, which the entity classifier cannot reach — is classified by
// its filename and validated on the buffer, squiggling the offending key.
func TestEnvelopeDiagnosticsForBuffer_ConfigByFilename(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	content := []byte("workspace:\n  offline: \"notabool\"\n")
	diags, has := srv.envelopeDiagnosticsForBuffer(".aggrega-config.yaml", content, parseDoc(t, string(content)))
	if !has || len(diags) == 0 {
		t.Fatalf("expected envelope findings for invalid config; has=%v diags=%d", has, len(diags))
	}
	d := diags[0]
	if d.Line <= 1 {
		t.Errorf("diagnostic should land on the offending key (line > 1), not line 1; got line %d", d.Line)
	}
	if !strings.Contains(d.Message, "AggregaConfig document violates its envelope schema") {
		t.Errorf("diagnostic must carry the AggregaConfig envelope framing; got %q", d.Message)
	}
}

// TestEnvelopeDiagnosticsForBuffer_ActiveReleaseLockfileByFilename pins
// that the active Release's lockfile — also envelope-less — is classified
// by correlation to activeReleasePath and validated on the buffer.
func TestEnvelopeDiagnosticsForBuffer_ActiveReleaseLockfileByFilename(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	srv.activeReleasePath = "/ws/release.yaml" // lockfile.PathFor -> /ws/release.lock
	content := []byte("version: 1\nrelease: 123\ngenerated_at: \"2026-01-01T00:00:00Z\"\n")
	diags, has := srv.envelopeDiagnosticsForBuffer("/ws/release.lock", content, parseDoc(t, string(content)))
	if !has || len(diags) == 0 {
		t.Fatalf("expected envelope findings for invalid lockfile; has=%v diags=%d", has, len(diags))
	}
	d := diags[0]
	if d.Line <= 1 {
		t.Errorf("diagnostic should land on the offending key (line > 1), not line 1; got line %d", d.Line)
	}
	if !strings.Contains(d.Message, "Lockfile document violates its envelope schema") {
		t.Errorf("diagnostic must carry the Lockfile envelope framing; got %q", d.Message)
	}
}

// TestEnvelopeDiagnosticsForBuffer_ForeignLockfilesNoDiagnostic is the
// foreign-.lock guard: other ecosystems' lockfiles share the .lock
// extension but are not the active Release's lockfile, so they never
// classify and never get lint-bombed against the lockfile schema — even
// with an active Release set (so release.lock itself WOULD classify).
func TestEnvelopeDiagnosticsForBuffer_ForeignLockfilesNoDiagnostic(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	srv.activeReleasePath = "/ws/release.yaml"
	// Foreign lockfiles classify by name, not content — their real
	// (TOML/JSON) bodies fail the daemon's yaml.Unmarshal upstream and
	// never reach this leg. The guard is that the NAME does not classify,
	// so the benign buffer here is never validated against any schema.
	content := []byte("a: 1\n")
	doc := parseDoc(t, string(content))
	for _, name := range []string{"Cargo.lock", "flake.lock", "poetry.lock", "yarn.lock", "Gemfile.lock", "composer.lock"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			diags, has := srv.envelopeDiagnosticsForBuffer(name, content, doc)
			if has || diags != nil {
				t.Errorf("%s: foreign lockfile must not engage the envelope leg; has=%v diags=%d", name, has, len(diags))
			}
		})
	}
}

// TestEnvelopeDiagnosticsForBuffer_LockfileNeedsActiveRelease pins that
// with no active Release the lockfile classification is inert: a file
// named like a lockfile is not validated, because there is no release to
// correlate it to.
func TestEnvelopeDiagnosticsForBuffer_LockfileNeedsActiveRelease(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil) // activeReleasePath == ""
	content := []byte("version: 1\nrelease: 123\ngenerated_at: \"2026-01-01T00:00:00Z\"\n")
	if diags, has := srv.envelopeDiagnosticsForBuffer("release.lock", content, parseDoc(t, string(content))); has || diags != nil {
		t.Errorf("lockfile must not classify without an active Release; has=%v diags=%d", has, len(diags))
	}
}

// TestClassifyByFilename pins the classification rule directly: the fixed
// config name and the active Release's lockfile basename classify; foreign
// lockfiles, arbitrary files, and a lockfile-shaped name with no active
// Release do not.
func TestClassifyByFilename(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	srv.activeReleasePath = "/ws/release.yaml" // -> /ws/release.lock

	type want struct {
		kind string
		ok   bool
	}
	cases := []struct {
		file string
		want want
	}{
		{".aggrega-config.yaml", want{"AggregaConfig", true}},
		{"/abs/path/.aggrega-config.yaml", want{"AggregaConfig", true}},
		{"/ws/release.lock", want{"Lockfile", true}},
		{"release.lock", want{"Lockfile", true}},
		{"Cargo.lock", want{"", false}},
		{"flake.lock", want{"", false}},
		{"prod.lock", want{"", false}}, // a .lock that is not the active release's
		{"aggregate.yaml", want{"", false}},
	}
	for _, c := range cases {
		k, ok := srv.classifyByFilename(c.file)
		if string(k) != c.want.kind || ok != c.want.ok {
			t.Errorf("classifyByFilename(%q) = (%q, %v); want (%q, %v)", c.file, k, ok, c.want.kind, c.want.ok)
		}
	}
}

// TestClassifyByFilename_NoActiveRelease pins that no .lock classifies
// when there is no active Release, while the fixed config name still does.
func TestClassifyByFilename_NoActiveRelease(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil) // activeReleasePath == ""
	if k, ok := srv.classifyByFilename("release.lock"); ok {
		t.Errorf("release.lock must not classify without an active Release; got (%q, %v)", k, ok)
	}
	if k, ok := srv.classifyByFilename(".aggrega-config.yaml"); !ok || k != "AggregaConfig" {
		t.Errorf("config must still classify without an active Release; got (%q, %v)", k, ok)
	}
}

// TestEnvelopeDiagnosticsForBuffer_EveryClassifiedKindIsCovered pins that a
// schema-invalid buffer produces envelope diagnostics for EVERY kind the
// apiVersion+kind classification recognizes — so the editor's verdict on an
// authored document does not depend on which kind it is. A kind the
// classification omits silently produces no diagnostics at all while the CLI
// rejects the same bytes, which is a divergence an author experiences as the
// editor calling a file clean.
//
// The Component case is the calibration: it must produce diagnostics under the
// same harness, so a kind that produces none is a real gap rather than a broken
// instrument.
func TestEnvelopeDiagnosticsForBuffer_EveryClassifiedKindIsCovered(t *testing.T) {
	t.Parallel()

	// Each fixture is YAML-valid (so it parses and classifies) and
	// schema-invalid (so the envelope has something to report).
	cases := map[string]string{
		"Component": `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: c
  version: 1.0.0
spec:
  notAComponentField: true
`,
		"Plugin": `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: broken
  pluginType: linter
  version: 1.0.0
spec:
  abiVersion: v0
`,
	}
	for name, content := range cases {
		name, content := name, content
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newTestServer(t, t.TempDir(), nil, nil)
			diags, has := srv.envelopeDiagnosticsForBuffer("entity.yaml", []byte(content), parseDoc(t, content))
			if !has || len(diags) == 0 {
				t.Fatalf("a schema-invalid %s buffer must produce envelope diagnostics; has=%v diags=%d", name, has, len(diags))
			}
			if !strings.Contains(diags[0].Message, "violates its envelope schema") {
				t.Errorf("%s diagnostic must carry the envelope framing; got %q", name, diags[0].Message)
			}
		})
	}
}

// TestEntityKindByNameCoversEveryCompletedKind pins the two editor registries
// against each other: every entity kind the daemon offers as a `kind:`
// completion must also be classifiable for the per-file envelope leg. Offering
// a kind and then declining to validate documents of it is the divergence this
// guards — it is invisible in either registry read alone, and it is what
// dropping a kind from one side leaves behind.
func TestEntityKindByNameCoversEveryCompletedKind(t *testing.T) {
	t.Parallel()
	for _, kind := range completion.EntityKinds {
		if _, ok := entityKindByName[kind]; !ok {
			t.Errorf("kind %q is offered as a completion but is not envelope-classifiable", kind)
		}
	}
	if len(entityKindByName) != len(completion.EntityKinds) {
		t.Errorf("the two registries must enumerate the same kinds; envelope-classifiable=%d, completion-offered=%d",
			len(entityKindByName), len(completion.EntityKinds))
	}
}
