// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Negative-authoring corpus — serve surface. One permanent subprocess
// row proves the envelope gate is inherited by the serve daemon: an
// envelope-invalid Aggregate in the workspace yields a diagnostic
// carrying the envelope-violation message. Serve's contract is "carries
// the diagnostic"; the violation CLASSES are exhausted at the build/lint/
// engine layers, so one row proves the inheritance.
//
// Every row here drives the real spawned daemon over its real framing
// rather than an in-process harness, because what is under test is the gate
// as a client meets it: the request shape, the answer it draws, and the
// notifications pushed alongside — hence the subprocess, mirroring the
// end-to-end semantic-validation test.
//
// A row's request either names a Release or names none, and the two ask for
// different work: naming one asks additionally for the checks only a Release
// can answer, naming none asks what the document answers on its own. Each
// row below states which it does and why, because an unnamed Release
// narrows the answer without failing, and a narrowed answer is where a
// negative assertion quietly stops discriminating.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// TestNegativeCorpusServe_MalformedAnchoringReleaseDiagnostic — a Release
// the disambiguation cascade FINDS but cannot load (here, the Invariant-5
// values-on-boundary-component ban) surfaces as a diagnostic ON THE RELEASE
// FILE: the daemon pushes an aggrega/publishDiagnostics for that file
// carrying the rendered envelope text, so an operator authoring a broken
// Release reads the error on the document rather than inferring it from an
// empty answer or finding it only on stderr.
//
// The request driven here is aggrega/activeRelease, which is where the
// cascade runs: it is the one method whose question — which Release should a
// client select by default — requires walking for candidates and loading
// them, so it is the only method positioned to discover that a candidate is
// broken. Validation is not, because a validate request is told which
// Release to act on and never goes looking for one.
func TestNegativeCorpusServe_MalformedAnchoringReleaseDiagnostic(t *testing.T) {
	// Not parallel: spawns a daemon subprocess.
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
	cmd.Env = builtBinaryEnv(t)
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
	// One ceiling for the handshake read and for every read in the scan loop
	// below. Both ask whether the daemon answered at all; neither asserts how
	// long an answer took, and the loop reads messages of one exchange.
	ceiling := responseLivenessCeiling(t)

	if _, err := stdin.Write(framed(1, "aggrega/initialize", map[string]any{})); err != nil {
		t.Fatalf("write initialize: %v", err)
	}
	if initResp := readOneResponse(t, stdout, ceiling); initResp["error"] != nil {
		t.Fatalf("initialize returned error: %+v", initResp)
	}
	// Asking which Release the workspace points at is what runs the cascade,
	// and the malformed candidate it finds surfaces via publishDiagnostics on
	// the Release file before the answer is written.
	if _, err := stdin.Write(framedFor(t, cmd, 2, "aggrega/activeRelease", map[string]any{})); err != nil {
		t.Fatalf("write activeRelease: %v", err)
	}
	// This leg publishes the LOAD ERROR's own rendered text, so the expected
	// string is the condition's registered sentence read off the condition —
	// not a literal, which would let this test pass against wording that had
	// drifted from the identity. The buffer-envelope leg below reaches the same
	// condition by a different path and composes its own occurrence detail, but
	// it reads the sentence from this same place, so one expectation serves
	// both legs rather than each leg owning a wording of its own.
	wantText := envelope.ErrEntityEnvelope().Error()
	found := false
	for i := 0; i < 6 && !found; i++ {
		msg := readOneResponse(t, stdout, ceiling)
		// Match the publishDiagnostics NOTIFICATION specifically — not any
		// message containing the text. The answer to this very request carries
		// a per-step account of the exhausted cascade, and that account names
		// the same path and the same envelope text, so a loose substring scan
		// over the traffic would pass with the squiggle never published.
		// Require: method == aggrega/publishDiagnostics, params.file == the
		// Release path, and a diagnostic whose message carries the envelope
		// text.
		if diagnosticPublishedFor(msg, releasePath, wantText) {
			found = true
			break
		}
		// The answer itself. The publish is written ahead of it, so once it has
		// arrived nothing further is coming: reading on would spend the
		// liveness ceiling and report a hang in place of the absence that
		// actually occurred.
		if id, ok := msg["id"].(float64); ok && id == 2 {
			break
		}
	}
	if !found {
		t.Fatalf("daemon did not PUBLISH an envelope diagnostic on the Release file %q (%q) — a found-but-unloadable Release still fails to anchor silently (the account carried on the answer does not count)", releasePath, wantText)
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
	// Not parallel: spawns a daemon subprocess.
	bin := buildServeBinary(t)
	ws := t.TempDir()

	// Cross-surface pair 2 (serve leg): the SAME Aggregate component-infra
	// envelope violation the build table asserts via BuildE
	// (xsurface-aggregate-component-infra). Build + serve together evidence
	// the one gate across the two surfaces that take an Aggregate subject.
	//
	// Why an Aggregate violation and not the Release values-ban: the row this
	// one pairs with on the build surface takes an Aggregate subject, and a
	// cross-surface pair evidences one gate only when both halves carry the
	// same document and the same fault.
	//
	// The request names no Release. A document's envelope verdict is a fact
	// about the document — the daemon classifies the buffer by its
	// apiVersion+kind and gives it the same verdict the command line does —
	// so this row asks for exactly that and nothing a Release could add. The
	// valid Release and Manifest on disk are the surrounding workspace a real
	// author has, not inputs to the assertion.
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
	cmd.Env = builtBinaryEnv(t)
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

	// One ceiling for the handshake read and for every read in the scan loop
	// below. Both ask whether the daemon answered at all; neither asserts how
	// long an answer took, and the loop reads messages of one exchange.
	ceiling := responseLivenessCeiling(t)

	if _, err := stdin.Write(framed(1, "aggrega/initialize", map[string]any{})); err != nil {
		t.Fatalf("write initialize: %v", err)
	}
	if initResp := readOneResponse(t, stdout, ceiling); initResp["error"] != nil {
		t.Fatalf("initialize returned error: %+v", initResp)
	}

	aggPath := filepath.Join(ws, "aggregate.yaml")
	if _, err := stdin.Write(framedFor(t, cmd, 2, "aggrega/validate", map[string]any{
		"file":    aggPath,
		"content": agg,
	})); err != nil {
		t.Fatalf("write validate: %v", err)
	}

	// The daemon may emit notifications before the id=2 response; scan a
	// few messages for one carrying a diagnostic with the envelope text.
	//
	// The daemon composes its own occurrence detail here — one entry per
	// finding, positioned on the offending key — but the sentence naming the
	// condition is read off the condition, the same one the terminal opens
	// with. So the expectation is that sentence rather than a literal: a
	// literal would keep matching a message the condition no longer says.
	want := envelope.ErrEntityEnvelope().Error()
	found := false
	for i := 0; i < 6 && !found; i++ {
		msg := readOneResponse(t, stdout, ceiling)
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
// initializes, validates validateFile against the named Release, and
// returns the flattened text of every response/notification it reads (up
// to a few). Used by the per-file envelope rows, whose diagnostics arrive
// on the validate RESPONSE (the envelope leg early-returns) rather than a
// publish notification.
//
// release is the workspace-relative path of the Release the request names,
// or "" to name none. See serveValidateScanBuffer for what each choice
// asks the daemon for.
func serveValidateScan(t *testing.T, files map[string]string, validateFile, release string) string {
	return serveValidateScanBuffer(t, files, validateFile, files[validateFile], release)
}

// envelopeSentenceInScan is what an envelope rejection looks like anywhere in a
// scan of the daemon's traffic: the condition's registered sentence, read off
// the condition rather than restated as a literal.
//
// Both the positive and the negative assertions below depend on it being
// derived, and the negative ones depend on it more. A literal that the product
// has stopped emitting still makes a positive assertion fail loudly — but it
// makes a negative one pass unconditionally, so a guard that the envelope leg
// must NOT fire would go on reporting green with the leg firing on every
// document.
func envelopeSentenceInScan() string { return envelope.ErrEntityEnvelope().Error() }

// serveValidateScanBuffer is serveValidateScan with an explicit editor
// buffer distinct from the on-disk content. It models live editing — the
// daemon starts against the (valid) files on disk, then validates an
// unsaved buffer. Needed where the on-disk document must be valid for the
// daemon to start (the workspace config) while the buffer under test is
// not.
//
// release is the workspace-relative path of the Release the request names,
// and it is a required argument at every call site rather than an option
// with a default, because it decides which of two answers the daemon gives.
// Naming one asks for the checks only a Release can answer — the semantic
// pass, and the lockfile correlation that decides whether the buffer is
// that Release's lockfile. Naming none ("") asks only what the document
// answers on its own. A row whose subject needs a Release and names none
// gets a well-formed answer about the document, which is why the choice is
// spelled out per row: an omitted Release does not fail, it narrows, and a
// narrowed answer is the shape a positive assertion stops seeing and a
// negative assertion starts passing for free.
//
// A non-empty release must be a file the fixture writes. Naming one it does
// not would resolve — the daemon checks the parameter's shape, not that the
// file is there — and then report a load failure instead of the subject
// under test, so the row would go red or green for a reason nobody chose.
func serveValidateScanBuffer(t *testing.T, files map[string]string, validateFile, buffer, release string) string {
	t.Helper()
	if release != "" {
		if _, ok := files[release]; !ok {
			t.Fatalf("fixture writes no %q, so naming it as the request's Release would test a Release that is not there", release)
		}
	}
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
	cmd.Env = builtBinaryEnv(t)
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
	// One ceiling for the handshake read and for every read in the scan loop
	// below. Both ask whether the daemon answered at all; neither asserts how
	// long an answer took, and the loop reads messages of one exchange.
	ceiling := responseLivenessCeiling(t)
	if _, err := stdin.Write(framed(1, "aggrega/initialize", map[string]any{})); err != nil {
		t.Fatalf("write initialize: %v", err)
	}
	if initResp := readOneResponse(t, stdout, ceiling); initResp["error"] != nil {
		t.Fatalf("initialize returned error: %+v", initResp)
	}
	vfPath := filepath.Join(ws, validateFile)
	if _, err := stdin.Write(framedFor(t, cmd, 2, "aggrega/validate", map[string]any{
		"file":    vfPath,
		"content": buffer,
		"release": release,
	})); err != nil {
		t.Fatalf("write validate: %v", err)
	}
	var all strings.Builder
	for i := 0; i < 4; i++ {
		msg := readOneResponse(t, stdout, ceiling)
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
// Release carrying the Invariant-5 values ban, in a workspace holding a
// second Release and validated directly, surfaces an envelope diagnostic on
// its own file. The coverage is that a Release document is reported on for
// its own sake — not only when it is the one the client is working against,
// which is the case a workspace holding two Releases makes concrete.
func TestNegativeCorpusServe_NonAnchoringReleaseGetsDiagnostic(t *testing.T) {
	// Not parallel: spawns a daemon subprocess.
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
	// Two Release files, and the request names NEITHER: the subject is that a
	// Release document gets its own envelope verdict on its own file whatever
	// Release the client is working against, so naming one would narrow the
	// row to the case where the file under test IS the named Release and lose
	// exactly the coverage it exists for.
	scan := serveValidateScan(t, map[string]string{
		"release-a.yaml": otherRelease,
		"release-b.yaml": badRelease,
		"manifest.yaml":  minimalManifest,
	}, "release-b.yaml", "")
	if want := envelopeSentenceInScan(); !strings.Contains(scan, want) {
		t.Fatalf("non-anchoring Release values-ban produced no envelope diagnostic (%q) on its own file:\n%s", want, scan)
	}
}

// TestNegativeCorpusServe_CatalogComponentGetsDiagnostic — row (b): a
// catalog Component with a junk spec key, validated directly, surfaces an
// envelope diagnostic on its file. Red today (catalog entities are never
// a per-file validate subject); green with the per-file leg.
func TestNegativeCorpusServe_CatalogComponentGetsDiagnostic(t *testing.T) {
	// Not parallel: spawns a daemon subprocess.
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
	// No Release named: a catalog Component's envelope verdict is a property
	// of the document, and the workspace holds no Release to name anyway.
	scan := serveValidateScan(t, map[string]string{"component.yaml": comp}, "component.yaml", "")
	if want := envelopeSentenceInScan(); !strings.Contains(scan, want) {
		t.Fatalf("catalog Component junk key produced no envelope diagnostic (%q) on its file:\n%s", want, scan)
	}
}

// TestNegativeCorpusServe_ForeignYAMLNoEnvelopeDiagnostic — row (c): the
// non-aggrega guard. A foreign YAML document (a Helm chart template)
// produces ZERO envelope diagnostics — the daemon never lint-bombs
// foreign YAML.
func TestNegativeCorpusServe_ForeignYAMLNoEnvelopeDiagnostic(t *testing.T) {
	// Not parallel: spawns a daemon subprocess.
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
	// No Release named: whether a document is aggrega's at all is decided
	// before any Release enters, so the guard is asserted where it lives.
	scan := serveValidateScan(t, map[string]string{"templates/deployment.yaml": chartTemplate}, "templates/deployment.yaml", "")
	if want := envelopeSentenceInScan(); strings.Contains(scan, want) {
		t.Fatalf("foreign YAML wrongly produced an envelope diagnostic (%q) — the guard failed:\n%s", want, scan)
	}
}

// TestNegativeCorpusServe_CleanAggregateStillTier4 — row (d), the
// regression: an envelope-CLEAN active Aggregate falls through the
// per-file envelope leg to its Tier-4 semantic validation. The fixture's
// parameter type mismatch is a schema-validation issue over the entity's
// declared pillars, not an envelope one; the leg must NOT fire (no
// envelope diagnostic at all) and the Tier-4 diagnostic about the
// parameter must still surface — proving envelope-clean does not
// short-circuit Tier-4.
func TestNegativeCorpusServe_CleanAggregateStillTier4(t *testing.T) {
	t.Parallel()
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
	// The request NAMES the Release, because the fall-through under test ends
	// in the semantic pass and that pass is what a named Release asks for. The
	// mismatch the row asserts is between this Aggregate's declared parameter
	// and this Release's fulfilment of it, so it is a fact about the pair and
	// there is no answer to it from the Aggregate alone.
	scan := serveValidateScan(t, map[string]string{
		"aggregate.yaml": agg,
		"release.yaml":   release,
		"manifest.yaml":  manifest,
	}, "aggregate.yaml", "release.yaml")
	if want := envelopeSentenceInScan(); strings.Contains(scan, want) {
		t.Fatalf("envelope-clean active Aggregate wrongly produced an envelope diagnostic (%q) — the leg should not fire / not short-circuit Tier-4:\n%s", want, scan)
	}
	if !strings.Contains(scan, "port") {
		t.Fatalf("clean active Aggregate did not receive its Tier-4 parameter diagnostic (Tier-4 fall-through broken):\n%s", scan)
	}
}

// activeReleaseFixture is a clean Aggregate/Release/Manifest triad with a
// single Release file at <ws>/release.yaml — the one a request names in
// order for that Release's lockfile (<ws>/release.lock) to classify, since
// the lockfile carries no apiVersion+kind envelope and is reached only by
// correlation to the Release the request is about.
//
// The triad is clean on purpose: every row built on it asserts something
// about the lockfile, so a fault anywhere else would give the row a second
// way to go red.
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
	// Not parallel: spawns a daemon subprocess.
	badConfigBuffer := "workspace:\noffline: \"notabool\"\n"
	// No Release named: the config classifies by its fixed, unique filename,
	// which no Release participates in.
	scan := serveValidateScanBuffer(t, map[string]string{}, ".aggrega-config.yaml", badConfigBuffer, "")
	if want := envelopeSentenceInScan(); !strings.Contains(scan, want) {
		t.Fatalf("invalid .aggrega-config.yaml buffer produced no envelope diagnostic (%q) on its file:\n%s", want, scan)
	}
}

// TestNegativeCorpusServe_ActiveReleaseLockfileGetsDiagnostic — a Release's
// lockfile, which carries no apiVersion+kind envelope of its own, classifies
// by correlation to the Release the request named, and a junk lockfile
// buffer squiggles live.
//
// This is the POSITIVE half of the correlation pair whose negative half is
// the foreign-.lock row below. The request must name the Release: correlation
// is defined against it, so a request naming none classifies no lockfile at
// all and this row would report an absence that says nothing about the
// filename rule.
func TestNegativeCorpusServe_ActiveReleaseLockfileGetsDiagnostic(t *testing.T) {
	// Not parallel: spawns a daemon subprocess.
	badLockfileBuffer := "version: 1\nrelease: 123\ngenerated_at: \"2026-01-01T00:00:00Z\"\n"
	scan := serveValidateScanBuffer(t, activeReleaseFixture(), "release.lock", badLockfileBuffer, "release.yaml")
	if want := envelopeSentenceInScan(); !strings.Contains(scan, want) {
		t.Fatalf("invalid active-release lockfile buffer produced no envelope diagnostic (%q) on its file:\n%s", want, scan)
	}
}

// TestNegativeCorpusServe_ForeignLockNoEnvelopeDiagnostic — the
// foreign-.lock guard at the daemon level: another ecosystem's lockfile does
// NOT classify and is never validated against the lockfile schema, so it
// draws zero envelope diagnostics.
//
// 🔴 The request names the SAME Release the positive row above names, and
// that is the whole discriminator rather than fixture furniture. What is
// being asserted is that correlation to a named Release admits exactly that
// Release's lockfile basename and rejects every other .lock — so the run
// must be one in which release.lock WOULD have classified. Name no Release
// and nothing classifies for any reason at all: the assertion goes on
// passing, the guard is never consulted, and the green stops carrying a
// claim about the filename rule. That failure is silent by construction —
// no gate, no count and no diff can see a test that agrees for the wrong
// reason — so the Release named here is load-bearing and is not to be
// dropped as an unused argument.
func TestNegativeCorpusServe_ForeignLockNoEnvelopeDiagnostic(t *testing.T) {
	t.Parallel()
	scan := serveValidateScanBuffer(t, activeReleaseFixture(), "Cargo.lock", "a: 1\n", "release.yaml")
	if want := envelopeSentenceInScan(); strings.Contains(scan, want) {
		t.Fatalf("foreign .lock wrongly produced an envelope diagnostic (%q) — the foreign-.lock guard failed:\n%s", want, scan)
	}
}
