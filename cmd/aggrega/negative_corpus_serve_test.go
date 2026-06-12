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

func TestNegativeCorpusServe_EnvelopeDiagnostic(t *testing.T) {
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
