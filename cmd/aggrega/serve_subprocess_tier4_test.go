// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestServeAggregaTier4ValidateEndToEnd exercises the daemon's live
// aggrega/validate flow against a Tier-A fixture (parameter type
// mismatch). The subprocess goes through Run's full startup wiring:
// workspace-root discovery, merged workspace + global config load,
// and lazy catalog lookup construction. The request names the
// Release the checks run against; the handler reads the Aggregate
// via the wire, runs RunTier4Pipeline against that Release, walks
// the parsed YAML for the offending parameter key, and returns a
// Diagnostic positioned at that key.
//
// Mirrors TestServeAggregaIrGraphEndToEnd's pattern. End-to-end
// pin for the Tier-4 wiring: the in-package unit tests construct
// Server directly and bypass Run's startup wiring; this subprocess
// test verifies the round-trip — request-named Release → Tier-4
// pipeline → JSON-RPC envelope → wire-shape diagnostic — against
// a real spawned daemon.
//
// The Release travels on the request, so naming it is what selects
// the semantic checks: a request naming none is answered from the
// document alone and carries no parameter-mismatch finding at all.
// That is why the assertions below check both the diagnostic and the
// absence of the narrower answer's marker.
func TestServeAggregaTier4ValidateEndToEnd(t *testing.T) {
	// Not parallel: spawns a daemon subprocess; concurrent daemons multiply peak memory past the package budget.
	bin := buildServeBinary(t)
	ws := t.TempDir()

	// Resolve absolute paths to the Tier-4 fixture. The test runs
	// from cmd/aggrega/; the module root is two dirs up.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	moduleRoot := filepath.Dir(filepath.Dir(cwd))
	fixtureDir := filepath.Join(moduleRoot, "internal", "serve", "testdata", "tier4", "parameter-type-mismatch")

	// Copy the three fixture YAMLs (release / manifest / aggregate)
	// into the workspace at the top level so the validate request can
	// name release.yaml by a workspace-relative path and the resolver
	// loads the sibling manifest + aggregate by Release.Spec pointers.
	for _, name := range []string{"release.yaml", "manifest.yaml", "aggregate.yaml"} {
		src := filepath.Join(fixtureDir, name)
		dst := filepath.Join(ws, name)
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
	// A workspace-config file with an empty body is enough to anchor
	// the workspace; no catalog needed because the parameter
	// validation fails in the resolve phase, before topology / IR /
	// render would touch the catalog.
	if err := os.WriteFile(filepath.Join(ws, ".aggrega-config.yaml"), []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
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
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})

	// Send aggrega/initialize so the daemon completes its handshake
	// before the validate request goes out. The daemon emits an
	// aggrega/initialize response carrying configSource +
	// workspaceRoot; skip past it.
	if _, err := stdin.Write(framed(1, "aggrega/initialize", map[string]any{})); err != nil {
		t.Fatalf("write initialize: %v", err)
	}
	initResp := readOneResponse(t, stdout, 5*time.Second)
	if _, hasErr := initResp["error"]; hasErr {
		t.Fatalf("initialize returned error: %+v", initResp)
	}

	// Send aggrega/validate against the Aggregate, naming the Release
	// to validate against. The Tier-4 branch fires because the request
	// named a Release; the resolver detects the parameter type mismatch
	// and the daemon emits a Diagnostic at the offending key.
	//
	// release is workspace-relative by contract and an absolute value
	// is refused, so it is deliberately not the same shape as file
	// above, which carries an absolute path.
	aggPath := filepath.Join(ws, "aggregate.yaml")
	aggBytes, err := os.ReadFile(aggPath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	if _, err := stdin.Write(framed(2, "aggrega/validate", map[string]any{
		"file":    aggPath,
		"content": string(aggBytes),
		"release": "release.yaml",
	})); err != nil {
		t.Fatalf("write validate: %v", err)
	}

	// Read up to a few responses; the daemon may emit notifications
	// (publishDiagnostics, etc.) before the validate response. Look
	// for the message carrying our request id.
	var validateResp map[string]any
	for i := 0; i < 5; i++ {
		msg := readOneResponse(t, stdout, 10*time.Second)
		idAny, hasID := msg["id"]
		if !hasID {
			// Notification (no id); skip.
			continue
		}
		// JSON unmarshal of an int id may surface as float64.
		if idF, ok := idAny.(float64); ok && idF == 2 {
			validateResp = msg
			break
		}
	}
	if validateResp == nil {
		t.Fatalf("did not receive validate response after 5 messages")
	}

	if _, hasErr := validateResp["error"]; hasErr {
		t.Fatalf("validate returned error envelope: %+v", validateResp["error"])
	}
	result, ok := validateResp["result"].(map[string]any)
	if !ok {
		t.Fatalf("result not a map; got %T: %v", validateResp["result"], validateResp["result"])
	}
	// A request that named a Release gets the method's ordinary answer,
	// which omits checked; that marker rides only on the narrower
	// document-only answer. Asserting its absence is what stops this
	// test agreeing with a reply whose semantic checks never ran — an
	// empty diagnostic list looks the same either way.
	if checked, present := result["checked"]; present {
		t.Fatalf("named a Release but got the document-only answer (checked=%v); the semantic checks did not run. response: %+v",
			checked, validateResp)
	}
	diagsAny, _ := result["diagnostics"].([]any)
	if len(diagsAny) == 0 {
		t.Fatalf("expected at least one diagnostic; got zero. response: %+v", validateResp)
	}

	// Walk the diagnostics looking for the Tier-A port-key match.
	// The fixture's `port:` key is on line 8 of aggregate.yaml.
	const portLine = 8
	var sawPortDiag bool
	var seenLines []float64
	var seenMessages []string
	for _, raw := range diagsAny {
		d, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		line, _ := d["line"].(float64)
		msg, _ := d["message"].(string)
		seenLines = append(seenLines, line)
		seenMessages = append(seenMessages, msg)
		if int(line) == portLine && strings.Contains(msg, "port") {
			sawPortDiag = true
		}
	}
	if !sawPortDiag {
		t.Errorf("no diagnostic at port key (line %d) with message containing 'port'; lines=%v messages=%v",
			portLine, seenLines, seenMessages)
	}
}
