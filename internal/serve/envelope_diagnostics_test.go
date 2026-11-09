// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/serve/completion"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// envelopeDiagnosticHeader is what every envelope diagnostic in this file
// expects its message to open with: the condition's registered sentence, read
// off the condition, then the kind of the document it was raised on.
//
// The WORDING is derived rather than written out, so an assertion here cannot
// pass against wording that has drifted from the identity it reports — a
// literal would keep matching a message the condition no longer says. The
// SHAPE around it is spelled out here on purpose, and must not be
// "simplified" into a call to the composer the daemon itself uses: a test
// asserting an expectation built by the code under test asserts nothing.
func envelopeDiagnosticHeader(kind envelope.Kind) string {
	return envelope.ErrEntityEnvelope().Error() + " (" + string(kind) + ")"
}

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
// boundary path (line > 1) via the Tier-A position walk, and opens with the
// condition's registered sentence.
func TestEnvelopeDiagnosticsForBuffer_PositionBetterThan1To1(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	content := []byte(`apiVersion: aggrega.ai/v1beta1
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
	diags, has := srv.envelopeDiagnosticsForBuffer("release.yaml", "", content, parseDoc(t, string(content)))
	if !has || len(diags) == 0 {
		t.Fatalf("expected envelope findings; has=%v diags=%d", has, len(diags))
	}
	d := diags[0]
	if d.Line <= 1 {
		t.Errorf("diagnostic should land on the offending key (line > 1), not line 1; got line %d", d.Line)
	}
	if want := envelopeDiagnosticHeader(envelope.KindRelease); !strings.HasPrefix(d.Message, want+": ") {
		t.Errorf("diagnostic must open with %q; got %q", want, d.Message)
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
		"unknown-kind": "apiVersion: aggrega.ai/v1beta1\nkind: Nonsense\nspec: {}\n",
		"bare-yaml":    "just: data\n",
	}
	for name, content := range cases {
		name, content := name, content
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			diags, has := srv.envelopeDiagnosticsForBuffer("doc.yaml", "", []byte(content), parseDoc(t, content))
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
	content := []byte(`apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: a
  version: "1.0.0"
spec:
  components: {}
`)
	if diags, has := srv.envelopeDiagnosticsForBuffer("aggregate.yaml", "", content, parseDoc(t, string(content))); has || diags != nil {
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
	diags, has := srv.envelopeDiagnosticsForBuffer(".aggrega-config.yaml", "", content, parseDoc(t, string(content)))
	if !has || len(diags) == 0 {
		t.Fatalf("expected envelope findings for invalid config; has=%v diags=%d", has, len(diags))
	}
	d := diags[0]
	if d.Line <= 1 {
		t.Errorf("diagnostic should land on the offending key (line > 1), not line 1; got line %d", d.Line)
	}
	if want := envelopeDiagnosticHeader(envelope.KindAggregaConfig); !strings.HasPrefix(d.Message, want+": ") {
		t.Errorf("diagnostic must open with %q; got %q", want, d.Message)
	}
}

// TestEnvelopeDiagnosticsForBuffer_ConfigValueInAShapeItsReaderCannotUse pins
// that the editor reports, on the buffer, the same shape refusals a command makes
// when it loads the file: one row per setting whose value is held to the shape its
// reader uses, each drawn on the line of the offending value.
//
// The line is the assertion that matters. A diagnostic at the head of the file
// would say only that something is wrong; one on the value's own line tells the
// author which one. Every row carries one fault, and the clean-buffer test above is
// the positive control that a well-formed configuration raises nothing.
func TestEnvelopeDiagnosticsForBuffer_ConfigValueInAShapeItsReaderCannotUse(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	for _, tc := range []struct {
		name    string
		content string
		// line is the 1-based line the offending value sits on.
		line int
	}{
		{"an origin with a trailing slash",
			"workspace: {}\nmcp:\n  allowed_origins:\n    - https://ok.example.com\n    - https://app.example.com/\n", 5},
		{"a principal header with a space",
			"workspace: {}\nmcp:\n  authorization:\n    trusted_proxy:\n      principal_header: 'X Forwarded User'\n", 5},
		{"an empty principal header",
			"workspace: {}\nmcp:\n  authorization:\n    trusted_proxy:\n      principal_header: ''\n", 5},
		{"a scopes header with a space",
			"workspace: {}\nmcp:\n  authorization:\n    trusted_proxy:\n      principal_header: X-Forwarded-User\n" +
				"      scopes_header: 'X Forwarded Scopes'\n", 6},
		{"a certificate authority with no scheme",
			"workspace: {}\nsigning:\n  fulcio_url: fulcio.example.com\n", 3},
		{"a registry's transparency log with no scheme",
			"workspace: {}\nregistries:\n  - host: registry.example.com\n    signing:\n      rekor_url: rekor.example.com\n", 5},
		{"a timestamp authority with no scheme",
			"workspace: {}\nsigning:\n  timestamp_url: tsa.example.com\n", 3},
		{"a CLI plugin pin in bare hexadecimal",
			"workspace: {}\nplugins:\n  cli:\n    - uri: oci://hub.aggrega.ai/plugin/cli/moo:1.0.0\n      digest: " +
				strings.Repeat("0123456789abcdef", 4) + "\n", 5},
		{"a public key that is not a PEM block",
			"workspace: {}\nregistries:\n  - host: registry.example.com\n    public_key: not-a-pem-block\n", 4},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			content := []byte(tc.content)
			diags, has := srv.envelopeDiagnosticsForBuffer(".aggrega-config.yaml", "", content, parseDoc(t, tc.content))
			if !has || len(diags) == 0 {
				t.Fatalf("expected a diagnostic for the buffer; has=%v diags=%d", has, len(diags))
			}
			header := envelopeDiagnosticHeader(envelope.KindAggregaConfig) + ": "
			for _, d := range diags {
				if d.Line == tc.line && strings.HasPrefix(d.Message, header) && strings.Contains(d.Message, "pattern: ") {
					return
				}
			}
			t.Errorf("no diagnostic on line %d refusing the value's shape; got %+v", tc.line, diags)
		})
	}
}

// TestEnvelopeDiagnosticsForBuffer_NamedReleaseLockfileByFilename pins
// that the named Release's lockfile — also envelope-less — is classified
// by correlation to that Release's path and validated on the buffer.
func TestEnvelopeDiagnosticsForBuffer_NamedReleaseLockfileByFilename(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	content := []byte("version: 1\nrelease: 123\ngenerated_at: \"2026-01-01T00:00:00Z\"\n")
	diags, has := srv.envelopeDiagnosticsForBuffer("/ws/release.lock", "/ws/release.yaml", content, parseDoc(t, string(content)))
	if !has || len(diags) == 0 {
		t.Fatalf("expected envelope findings for invalid lockfile; has=%v diags=%d", has, len(diags))
	}
	d := diags[0]
	if d.Line <= 1 {
		t.Errorf("diagnostic should land on the offending key (line > 1), not line 1; got line %d", d.Line)
	}
	if want := envelopeDiagnosticHeader(envelope.KindLockfile); !strings.HasPrefix(d.Message, want+": ") {
		t.Errorf("diagnostic must open with %q; got %q", want, d.Message)
	}
}

// TestEnvelopeDiagnosticsForBuffer_ForeignLockfilesNoDiagnostic is the
// foreign-.lock guard: other ecosystems' lockfiles share the .lock
// extension but are not the named Release's lockfile, so they never
// classify and never get lint-bombed against the lockfile schema — even
// with a Release named (so release.lock itself WOULD classify).
func TestEnvelopeDiagnosticsForBuffer_ForeignLockfilesNoDiagnostic(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
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
			diags, has := srv.envelopeDiagnosticsForBuffer(name, "/ws/release.yaml", content, doc)
			if has || diags != nil {
				t.Errorf("%s: foreign lockfile must not engage the envelope leg; has=%v diags=%d", name, has, len(diags))
			}
		})
	}
}

// TestEnvelopeDiagnosticsForBuffer_LockfileNeedsANamedRelease pins that
// with no Release named the lockfile classification is inert: a file
// named like a lockfile is not validated, because there is no Release to
// correlate it to.
func TestEnvelopeDiagnosticsForBuffer_LockfileNeedsANamedRelease(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	content := []byte("version: 1\nrelease: 123\ngenerated_at: \"2026-01-01T00:00:00Z\"\n")
	if diags, has := srv.envelopeDiagnosticsForBuffer("release.lock", "", content, parseDoc(t, string(content))); has || diags != nil {
		t.Errorf("lockfile must not classify with no Release named; has=%v diags=%d", has, len(diags))
	}
}

// TestClassifyByFilename pins the classification rule directly: the fixed
// config name and the named Release's lockfile basename classify; foreign
// lockfiles, arbitrary files, and a lockfile-shaped name belonging to some
// other Release do not.
func TestClassifyByFilename(t *testing.T) {
	t.Parallel()

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
		{"prod.lock", want{"", false}}, // a .lock belonging to some other Release
		{"aggregate.yaml", want{"", false}},
	}
	for _, c := range cases {
		k, ok := classifyByFilename(c.file, "/ws/release.yaml")
		if string(k) != c.want.kind || ok != c.want.ok {
			t.Errorf("classifyByFilename(%q) = (%q, %v); want (%q, %v)", c.file, k, ok, c.want.kind, c.want.ok)
		}
	}
}

// TestClassifyByFilename_NoRelease pins that no .lock classifies when the
// caller named no Release, while the fixed config name still does.
func TestClassifyByFilename_NoRelease(t *testing.T) {
	t.Parallel()
	if k, ok := classifyByFilename("release.lock", ""); ok {
		t.Errorf("release.lock must not classify with no Release named; got (%q, %v)", k, ok)
	}
	if k, ok := classifyByFilename(".aggrega-config.yaml", ""); !ok || k != "AggregaConfig" {
		t.Errorf("config must still classify with no Release named; got (%q, %v)", k, ok)
	}
}

// TestEnvelopeDiagnosticsForBuffer_ClassifiedKindsProduceDiagnostics pins that a
// schema-invalid buffer produces envelope diagnostics, and that each opens with
// the condition's sentence, for the kinds enumerated below — so the editor's
// verdict on an authored document does not depend on which of them it is. A
// kind that produces none silently reports nothing while the CLI rejects the
// same bytes, which is a divergence an author experiences as the editor calling
// a file clean.
//
// 🔴 The enumerated kinds are a FLOOR, not the classification's whole set: the
// table below carries fewer rows than classifyAggregaDocument recognizes, so a
// kind absent from the table is unproven here rather than proven working. State
// the corpus rather than implying coverage of every kind — a table read as a
// total is how an untested kind passes for a tested one.
//
// The Component case is the calibration: it must produce diagnostics under the
// same harness, so a kind that produces none is a real gap rather than a broken
// instrument.
func TestEnvelopeDiagnosticsForBuffer_ClassifiedKindsProduceDiagnostics(t *testing.T) {
	t.Parallel()

	// Each fixture is YAML-valid (so it parses and classifies) and
	// schema-invalid (so the envelope has something to report).
	cases := map[string]string{
		"Component": `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: c
  version: 1.0.0
spec:
  notAComponentField: true
`,
		"Plugin": `apiVersion: aggrega.ai/v1beta1
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
			diags, has := srv.envelopeDiagnosticsForBuffer("entity.yaml", "", []byte(content), parseDoc(t, content))
			if !has || len(diags) == 0 {
				t.Fatalf("a schema-invalid %s buffer must produce envelope diagnostics; has=%v diags=%d", name, has, len(diags))
			}
			if want := envelopeDiagnosticHeader(envelope.Kind(name)); !strings.HasPrefix(diags[0].Message, want+": ") {
				t.Errorf("%s diagnostic must open with %q; got %q", name, want, diags[0].Message)
			}
		})
	}
}

// TestEntityKindByNameCoversEveryCompletedKind pins the two editor-facing
// registries against each other: every kind the daemon offers as a `kind:`
// completion is classifiable for the per-file envelope leg, and the reverse.
// Offering a kind and then declining to validate documents of it is invisible in
// either registry read alone.
//
// What this can and cannot catch is worth stating, because both sides read one
// narrowing of one vocabulary and a reader could take the pass for more than it
// is. It catches a RE-DIVERGENCE: a later edit that writes either side out by
// hand again, or that narrows one projection and not the other. It cannot catch
// a defect in what the two share — a kind wrongly declared, or one wrongly
// included in or excluded from the narrowing — because such a defect moves both
// sides together. That question is asked against the shipped schemas and the
// decided roll-call instead, where the expectation comes from somewhere neither
// side reads.
func TestEntityKindByNameCoversEveryCompletedKind(t *testing.T) {
	t.Parallel()
	if len(completion.EntityKinds) == 0 || len(entityKindByName) == 0 {
		t.Fatal("one of the two registries is empty, so the comparisons below assert nothing")
	}
	for _, kind := range completion.EntityKinds {
		if _, ok := entityKindByName[kind]; !ok {
			t.Errorf("kind %q is offered as a completion but is not envelope-classifiable: a document "+
				"of it classifies as foreign YAML, so nothing checks its shape", kind)
		}
	}
	offered := map[string]bool{}
	for _, kind := range completion.EntityKinds {
		offered[kind] = true
	}
	for kind := range entityKindByName {
		if !offered[kind] {
			t.Errorf("kind %q is envelope-classifiable and is not offered as a completion", kind)
		}
	}
}
