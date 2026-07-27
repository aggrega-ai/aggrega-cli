// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Negative-authoring corpus — serve surface. One permanent subprocess
// row proves the envelope gate is inherited by the serve daemon: an
// envelope-invalid Aggregate in the workspace yields a diagnostic
// carrying the envelope-violation message. Serve's contract is "carries
// the diagnostic"; the violation CLASSES are exhausted at the build/lint/
// engine layers, so one row proves the inheritance (the economy ruled at
// the plan gate).
//
// The in-process serve harness does not run the cascade (no active
// Release → no Tier-4), so the envelope diagnostic surfaces only against
// the real spawned daemon — hence the subprocess, mirroring the Tier-4
// end-to-end test.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestNegativeCorpusServe_MalformedAnchoringReleaseDiagnostic is the
// sibling row the negative-authoring corpus recorded as a finding: a
// Release the cascade FINDS but cannot load (here, the Invariant-5
// values-on-boundary-component ban) must surface as a diagnostic ON THE
// RELEASE FILE, not vanish into a generic release-not-resolved error with
// the detail only on stderr. Red against the pre-fix daemon (1002 +
// stderr, no diagnostic); green after — the daemon pushes an
// aggrega/publishDiagnostics for the Release file carrying the rendered
// envelope text.
func TestNegativeCorpusServe_MalformedAnchoringReleaseDiagnostic(t *testing.T) {
	// Not parallel: spawns a daemon subprocess; concurrent daemons multiply peak memory past the package budget.
	bin := buildServeBinary(t)
	ws := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(ws, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write(".aggrega-config.yaml", "workspace: {}\n")
	// The single fault lives on the Release: values: on a boundary
	// component (Invariant-5). The Aggregate is valid — single-fault.
	releasePath := filepath.Join(ws, "release.yaml")
	write("release.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: corpus-rel
  environment: dev
  version: "1.0.0"
spec:
  aggregate: corpus-app
  manifest: corpus-manifest
  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          values:
            port: 9999
`)
	agg := `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: corpus-app
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server
      values:
        port: 8080
`
	write("aggregate.yaml", agg)

	cmd := exec.Command(bin, "serve")
	cmd.Dir = ws
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})
	if _, err := stdin.Write(framed(1, "aggrega/initialize", map[string]any{})); err != nil {
		t.Fatalf("write initialize: %v", err)
	}
	if initResp := readOneResponse(t, stdout, 5*time.Second); initResp["error"] != nil {
		t.Fatalf("initialize returned error: %+v", initResp)
	}
	// Validating the (valid) Aggregate is enough to trigger the anchoring
	// attempt; the malformed Release surfaces via publishDiagnostics on the
	// Release file.
	aggPath := filepath.Join(ws, "aggregate.yaml")
	if _, err := stdin.Write(framed(2, "aggrega/validate", map[string]any{
		"file":    aggPath,
		"content": agg,
	})); err != nil {
		t.Fatalf("write validate: %v", err)
	}
	const wantText = "violates its envelope schema"
	found := false
	for i := 0; i < 6 && !found; i++ {
		msg := readOneResponse(t, stdout, 10*time.Second)
		// Match the publishDiagnostics NOTIFICATION specifically — not any
		// message containing the text. The 1002 error response's enriched
		// cascade trace also carries the path + envelope text, so a loose
		// substring scan would pass even if the squiggle never published
		// (proven by disabling the publish leg). Require: method ==
		// aggrega/publishDiagnostics, params.file == the Release path, and a
		// diagnostic whose message carries the envelope text.
		if diagnosticPublishedFor(msg, releasePath, wantText) {
			found = true
		}
	}
	if !found {
		t.Fatalf("daemon did not PUBLISH an envelope diagnostic on the Release file %q (%q) — a malformed anchoring Release still de-anchors silently (the 1002 trace alone does not count)", releasePath, wantText)
	}
}

// diagnosticPublishedFor reports whether msg is an aggrega/publishDiagnostics
// notification targeting file with a diagnostic message containing wantText.
func diagnosticPublishedFor(msg map[string]any, file, wantText string) bool {
	if m, _ := msg["method"].(string); m != "aggrega/publishDiagnostics" {
		return false
	}
	params, ok := msg["params"].(map[string]any)
	if !ok {
		return false
	}
	if f, _ := params["file"].(string); f != file {
		return false
	}
	diags, ok := params["diagnostics"].([]any)
	if !ok {
		return false
	}
	for _, d := range diags {
		dm, ok := d.(map[string]any)
		if !ok {
			continue
		}
		if msgText, _ := dm["message"].(string); strings.Contains(msgText, wantText) {
			return true
		}
	}
	return false
}

func TestNegativeCorpusServe_EnvelopeDiagnostic(t *testing.T) {
	// Not parallel: spawns a daemon subprocess; concurrent daemons multiply peak memory past the package budget.
	bin := buildServeBinary(t)
	ws := t.TempDir()

	// Cross-surface pair 2 (serve leg): the SAME Aggregate component-infra
	// envelope violation the build table asserts via BuildE
	// (xsurface-aggregate-component-infra). Build + serve together evidence
	// the one gate across the two surfaces that take an Aggregate subject.
	//
	// Why an Aggregate violation, not the Release values-ban: a malformed
	// Release de-anchors the serve cascade — the daemon cannot resolve a
	// Release to establish Tier-4 context, so it returns a 1002
	// "release-not-resolved" error with the envelope detail on stderr, not
	// a wire diagnostic. The Aggregate violation surfaces AS a diagnostic
	// (the cascade resolves the valid Release, then the closure load of the
	// bad Aggregate produces a Tier-D diagnostic carrying the envelope
	// message). This is the serve surface's actual diagnostic-bearing path.
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(ws, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write(".aggrega-config.yaml", "workspace: {}\n")
	write("manifest.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: corpus-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server: server-recipe:1.0.0
`)
	write("release.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: corpus-rel
  environment: dev
  version: "1.0.0"
spec:
  aggregate: corpus-app
  manifest: corpus-manifest
  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          infra:
            replicas: 1
`)
	// The single fault: component-level infra on an Aggregate (Operator
	// pillar authored on the Developer surface) — rejected by the envelope.
	agg := `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: corpus-app
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server
      infra:
        port: 8080
`
	write("aggregate.yaml", agg)

	cmd := exec.Command(bin, "serve")
	cmd.Dir = ws
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})

	if _, err := stdin.Write(framed(1, "aggrega/initialize", map[string]any{})); err != nil {
		t.Fatalf("write initialize: %v", err)
	}
	if initResp := readOneResponse(t, stdout, 5*time.Second); initResp["error"] != nil {
		t.Fatalf("initialize returned error: %+v", initResp)
	}

	aggPath := filepath.Join(ws, "aggregate.yaml")
	if _, err := stdin.Write(framed(2, "aggrega/validate", map[string]any{
		"file":    aggPath,
		"content": agg,
	})); err != nil {
		t.Fatalf("write validate: %v", err)
	}

	// The daemon may emit notifications before the id=2 response; scan a
	// few messages for one carrying a diagnostic with the envelope text.
	// The rendered diagnostic carries envelope.Error.Error() ("…document
	// violates its envelope schema"); the sentinel string "document
	// envelope validation failed" is reachable only via errors.Is at the
	// Go boundary, never in the wire diagnostic — assert the rendered text.
	const want = "violates its envelope schema"
	found := false
	for i := 0; i < 6 && !found; i++ {
		msg := readOneResponse(t, stdout, 10*time.Second)
		if strings.Contains(serializeForScan(msg), want) {
			found = true
		}
	}
	if !found {
		t.Fatalf("serve daemon did not surface an envelope diagnostic (%q) for an envelope-invalid Aggregate — the gate is not inherited at the serve surface", want)
	}
}

// serializeForScan flattens a JSON-RPC message to a string for substring
// scanning of diagnostic content.
func serializeForScan(m map[string]any) string {
	var b strings.Builder
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, vv := range t {
				b.WriteString(k)
				b.WriteByte(' ')
				walk(vv)
			}
		case []any:
			for _, vv := range t {
				walk(vv)
			}
		default:
			b.WriteString(strings.TrimSpace(stringify(t)))
			b.WriteByte(' ')
		}
	}
	walk(m)
	return b.String()
}

func stringify(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// serveValidateScan starts the daemon over the given workspace files,
// initializes, validates validateFile, and returns the flattened text of
// every response/notification it reads (up to a few). Used by the 06e3
// per-file envelope rows, whose diagnostics arrive on the validate
// RESPONSE (the envelope leg early-returns) rather than a publish
// notification.
func serveValidateScan(t *testing.T, files map[string]string, validateFile string) string {
	return serveValidateScanBuffer(t, files, validateFile, files[validateFile])
}

// serveValidateScanBuffer is serveValidateScan with an explicit editor
// buffer distinct from the on-disk content. It models live editing — the
// daemon starts against the (valid) files on disk, then validates an
// unsaved buffer. Needed where the on-disk document must be valid for the
// daemon to start (the workspace config) while the buffer under test is
// not.
func serveValidateScanBuffer(t *testing.T, files map[string]string, validateFile, buffer string) string {
	t.Helper()
	bin := buildServeBinary(t)
	ws := t.TempDir()
	if _, ok := files[".aggrega-config.yaml"]; !ok {
		files[".aggrega-config.yaml"] = "workspace: {}\n"
	}
	for name, content := range files {
		p := filepath.Join(ws, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	cmd := exec.Command(bin, "serve")
	cmd.Dir = ws
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Wait() })
	if _, err := stdin.Write(framed(1, "aggrega/initialize", map[string]any{})); err != nil {
		t.Fatalf("write initialize: %v", err)
	}
	if initResp := readOneResponse(t, stdout, 5*time.Second); initResp["error"] != nil {
		t.Fatalf("initialize returned error: %+v", initResp)
	}
	vfPath := filepath.Join(ws, validateFile)
	if _, err := stdin.Write(framed(2, "aggrega/validate", map[string]any{
		"file":    vfPath,
		"content": buffer,
	})); err != nil {
		t.Fatalf("write validate: %v", err)
	}
	var all strings.Builder
	for i := 0; i < 4; i++ {
		msg := readOneResponse(t, stdout, 10*time.Second)
		all.WriteString(serializeForScan(msg))
		all.WriteByte('\n')
		if id, ok := msg["id"].(float64); ok && id == 2 {
			break // the validate response — diagnostics (if any) are here
		}
	}
	return all.String()
}

const minimalManifest = `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: m
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server: server-recipe:1.0.0
`

// TestNegativeCorpusServe_NonAnchoringReleaseGetsDiagnostic — row (a): a
// Release the cascade does NOT anchor (ambiguous two-Release workspace)
// carrying the Invariant-5 values ban, validated directly, surfaces an
// envelope diagnostic on its own file. Red today (Tier-4 returns nil for
// a non-active file → shows clean); green with the per-file leg.
func TestNegativeCorpusServe_NonAnchoringReleaseGetsDiagnostic(t *testing.T) {
	// Not parallel: spawns a daemon subprocess; concurrent daemons multiply peak memory past the package budget.
	badRelease := `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: rel-b
  environment: dev
  version: "1.0.0"
spec:
  aggregate: a
  manifest: m
  boundaries:
    main:
      directory: ./dist
      components:
        api:
          values:
            port: 9999
`
	otherRelease := `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: rel-a
  environment: dev
  version: "1.0.0"
spec:
  aggregate: a
  manifest: m
  boundaries:
    main:
      directory: ./dist
      components:
        api:
          infra:
            replicas: 1
`
	// Two Release files → step-2 ambiguous → no anchor → validating the
	// bad one is a non-anchoring-file validate.
	scan := serveValidateScan(t, map[string]string{
		"release-a.yaml": otherRelease,
		"release-b.yaml": badRelease,
		"manifest.yaml":  minimalManifest,
	}, "release-b.yaml")
	if !strings.Contains(scan, "violates its envelope schema") {
		t.Fatalf("non-anchoring Release values-ban produced no envelope diagnostic on its own file:\n%s", scan)
	}
}

// TestNegativeCorpusServe_CatalogComponentGetsDiagnostic — row (b): a
// catalog Component with a junk spec key, validated directly, surfaces an
// envelope diagnostic on its file. Red today (catalog entities are never
// a per-file validate subject); green with the per-file leg.
func TestNegativeCorpusServe_CatalogComponentGetsDiagnostic(t *testing.T) {
	// Not parallel: spawns a daemon subprocess; concurrent daemons multiply peak memory past the package budget.
	comp := `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  bogusKey: nope
  schema:
    values:
      port: { type: integer }
`
	scan := serveValidateScan(t, map[string]string{"component.yaml": comp}, "component.yaml")
	if !strings.Contains(scan, "violates its envelope schema") {
		t.Fatalf("catalog Component junk key produced no envelope diagnostic on its file:\n%s", scan)
	}
}

// TestNegativeCorpusServe_ForeignYAMLNoEnvelopeDiagnostic — row (c): the
// non-aggrega guard. A foreign YAML document (a Helm chart template)
// produces ZERO envelope diagnostics — the daemon never lint-bombs
// foreign YAML.
func TestNegativeCorpusServe_ForeignYAMLNoEnvelopeDiagnostic(t *testing.T) {
	// Not parallel: spawns a daemon subprocess; concurrent daemons multiply peak memory past the package budget.
	chartTemplate := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .Release.Name }}-web
spec:
  replicas: {{ .Values.replicaCount }}
  template:
    spec:
      containers:
        - name: web
          image: "{{ .Values.image }}"
`
	scan := serveValidateScan(t, map[string]string{"templates/deployment.yaml": chartTemplate}, "templates/deployment.yaml")
	if strings.Contains(scan, "violates its envelope schema") {
		t.Fatalf("foreign YAML wrongly produced an envelope diagnostic (the guard failed):\n%s", scan)
	}
}

// TestNegativeCorpusServe_CleanAggregateStillTier4 — row (d), the
// regression: an envelope-CLEAN active Aggregate falls through the
// per-file envelope leg to its Tier-4 semantic validation. The fixture's
// parameter type mismatch is a Step-1 pillar (content) issue, not an
// envelope one; the leg must NOT fire (no "violates its envelope schema")
// and the Tier-4 diagnostic about the parameter must still surface —
// proving envelope-clean does not short-circuit Tier-4.
func TestNegativeCorpusServe_CleanAggregateStillTier4(t *testing.T) {
	// Not parallel: spawns a daemon subprocess; concurrent daemons multiply peak memory past the package budget.
	agg := `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: tier4-param-mismatch
  version: "1.0.0"
spec:
  parameters:
    port:
      type: integer
      required: true
  components: {}
`
	release := `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: tier4-param-mismatch-release
  environment: test
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  parameters:
    port: "not-an-integer"
  boundaries:
    main:
      directory: ./dist/main
      components:
        port: {}
`
	manifest := `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: tier4-param-mismatch-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type: {}
`
	scan := serveValidateScan(t, map[string]string{
		"aggregate.yaml": agg,
		"release.yaml":   release,
		"manifest.yaml":  manifest,
	}, "aggregate.yaml")
	if strings.Contains(scan, "violates its envelope schema") {
		t.Fatalf("envelope-clean active Aggregate wrongly produced an envelope diagnostic (the leg should not fire / not short-circuit Tier-4):\n%s", scan)
	}
	if !strings.Contains(scan, "port") {
		t.Fatalf("clean active Aggregate did not receive its Tier-4 parameter diagnostic (Tier-4 fall-through broken):\n%s", scan)
	}
}

// activeReleaseFixture is a clean Aggregate/Release/Manifest triad with a
// single Release file, so the daemon's disambiguation cascade anchors it
// and populates activeReleasePath (= <ws>/release.yaml) — the prerequisite
// for the active Release's lockfile (<ws>/release.lock) to classify.
func activeReleaseFixture() map[string]string {
	return map[string]string{
		"aggregate.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: lf-app
  version: "1.0.0"
spec:
  components: {}
`,
		"release.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: lf-rel
  environment: dev
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components: {}
`,
		"manifest.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: lf-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type: {}
`,
	}
}

// TestNegativeCorpusServe_ConfigByFilenameGetsDiagnostic — the workspace
// config carries no apiVersion+kind envelope, so the entity classifier
// cannot reach it; filename classification does. The on-disk config must
// be valid for the daemon to start, so the invalid document is supplied as
// the editor BUFFER. Red before this leg: the classifier skipped it; green
// now.
func TestNegativeCorpusServe_ConfigByFilenameGetsDiagnostic(t *testing.T) {
	// Not parallel: spawns a daemon subprocess; concurrent daemons multiply peak memory past the package budget.
	badConfigBuffer := "workspace:\noffline: \"notabool\"\n"
	scan := serveValidateScanBuffer(t, map[string]string{}, ".aggrega-config.yaml", badConfigBuffer)
	if !strings.Contains(scan, "violates its envelope schema") {
		t.Fatalf("invalid .aggrega-config.yaml buffer produced no envelope diagnostic on its file:\n%s", scan)
	}
}

// TestNegativeCorpusServe_ActiveReleaseLockfileGetsDiagnostic — the active
// Release's lockfile, also envelope-less, classifies by correlation to the
// anchored Release. A junk lockfile buffer squiggles live. Red before this
// leg; green now.
func TestNegativeCorpusServe_ActiveReleaseLockfileGetsDiagnostic(t *testing.T) {
	// Not parallel: spawns a daemon subprocess; concurrent daemons multiply peak memory past the package budget.
	badLockfileBuffer := "version: 1\nrelease: 123\ngenerated_at: \"2026-01-01T00:00:00Z\"\n"
	scan := serveValidateScanBuffer(t, activeReleaseFixture(), "release.lock", badLockfileBuffer)
	if !strings.Contains(scan, "violates its envelope schema") {
		t.Fatalf("invalid active-release lockfile buffer produced no envelope diagnostic on its file:\n%s", scan)
	}
}

// TestNegativeCorpusServe_ForeignLockNoEnvelopeDiagnostic — the
// foreign-.lock guard at the daemon level: with a Release anchored (so
// release.lock WOULD classify), a different .lock file (another
// ecosystem's lockfile) does NOT classify and is never validated against
// the lockfile schema — zero envelope diagnostics.
func TestNegativeCorpusServe_ForeignLockNoEnvelopeDiagnostic(t *testing.T) {
	// Not parallel: spawns a daemon subprocess; concurrent daemons multiply peak memory past the package budget.
	scan := serveValidateScanBuffer(t, activeReleaseFixture(), "Cargo.lock", "a: 1\n")
	if strings.Contains(scan, "violates its envelope schema") {
		t.Fatalf("foreign .lock wrongly produced an envelope diagnostic (the foreign-.lock guard failed):\n%s", scan)
	}
}
