// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// The editor-facing daemon's side of the deprecation report. Two things are
// asserted together and both matter: the withdrawal is published, and the daemon
// keeps its resilient no-halt posture — every other diagnostic for the buffer
// survives. A daemon that halted on this class would replace the buffer's whole
// diagnostic set with one line, at every keystroke.
//
// Reuses writeFixtureFile / framedRequest / runOneRequest / newTestServer from the
// harness files in this package.

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// deprecatedPinWorkspace writes a Release whose Aggregate carries a component the
// catalog cannot resolve — a real, positioned diagnostic — beside a lockfile
// recording a withdrawal. The two are independent faults on purpose: the
// withdrawal must be published without displacing the diagnostic the author is
// actually editing against.
//
// withMarker=false is the matched control. Without it, a change that published
// nothing would leave the "other diagnostics survive" assertion green and the
// withdrawal assertion would be the only thing carrying the test.
func deprecatedPinWorkspace(t *testing.T, withMarker bool) (srv *Server, aggPath, aggBody string) {
	t.Helper()
	tmp := t.TempDir()

	writeFixtureFile(t, filepath.Join(tmp, "release.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: demo-release
  environment: production
  version: "1.0.0"
spec:
  aggregate: app-agg
  manifest: app-manifest
  boundaries:
    app:
      directory: ./dist/app
      components:
        app: {}
`)
	aggBody = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: app-agg
  version: "1.0.0"
spec:
  components:
    app:
      type: "demo/absent-server:1.0.0"
`
	writeFixtureFile(t, filepath.Join(tmp, "app-agg.yaml"), aggBody)
	writeFixtureFile(t, filepath.Join(tmp, "app-manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: app-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type: {}
`)

	releasePath := filepath.Join(tmp, "release.yaml")
	aggPath = filepath.Join(tmp, "app-agg.yaml")

	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     "./release.yaml",
		GeneratedAt: deprecationStamp,
	}
	if withMarker {
		// A plugin row records its content digest as both its resolved version and
		// its digest, because a plugin artifact is pinned by its bytes; the lockfile
		// refuses a row carrying anything else in either.
		pin := contentDigestHex([]byte(deprecatedPinURI))
		lf.Plugins = []lockfile.Plugin{{
			URI:             deprecatedPinURI,
			ResolvedVersion: pin,
			Digest:          pin,
			Deprecation: &lockfile.DeprecationRecord{
				Severity: "retracted",
				Message:  "withdrawn: unsound renderer",
			},
		}}
	}
	if err := lockfile.WriteLockfile(lockfile.PathFor(releasePath), lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}

	srv = newTestServer(t, tmp, nil, nil)
	return srv, aggPath, aggBody
}

const (
	deprecationStamp = "2026-08-01T09:30:00Z"
	deprecatedPinURI = "oci://reg.example/plugins/helm-renderer:1.4.0"
)

// validateDiagnosticsFor drives the real validate request end to end and returns
// the published diagnostics.
func validateDiagnosticsFor(t *testing.T, srv *Server, file, content string) []Diagnostic {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(11, "aggrega/validate", validateParams{File: file, Content: content, Release: "release.yaml"}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("validate returned an error envelope: %+v", resp.Error)
	}
	var result validateResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal validate result: %v", err)
	}
	return result.Diagnostics
}

// TestServeValidatePublishesADeprecatedPinAndKeepsItsOtherDiagnostics is the
// posture proof clause the daemon's design rests on: the withdrawal is published
// AND the buffer keeps everything else it had to say.
func TestServeValidatePublishesADeprecatedPinAndKeepsItsOtherDiagnostics(t *testing.T) {
	srv, aggPath, aggBody := deprecatedPinWorkspace(t, true)
	diags := validateDiagnosticsFor(t, srv, aggPath, aggBody)

	var sawDeprecation, sawOther bool
	for _, d := range diags {
		switch {
		case strings.Contains(d.Message, deprecatedPinURI):
			sawDeprecation = true
			if d.Severity != editorSeverityWarning {
				t.Errorf("a recorded marker publishes as an advisory, not an assertion that the buffer is broken; "+
					"severity was %q", d.Severity)
			}
			for _, want := range []string{"is retracted", "withdrawn: unsound renderer",
				"as of " + deprecationStamp, "aggrega pull"} {
				if !strings.Contains(d.Message, want) {
					t.Errorf("the published notice drops %q:\n%s", want, d.Message)
				}
			}
		case strings.Contains(d.Message, "absent-server"):
			sawOther = true
		}
	}
	if !sawDeprecation {
		t.Errorf("the daemon said nothing about a withdrawn pin; published set:\n%+v", diags)
	}
	if !sawOther {
		t.Errorf("publishing the withdrawal cost the buffer its other diagnostics — the no-halt posture is gone; "+
			"published set:\n%+v", diags)
	}
}

// TestServeValidateWithoutAMarkerPublishesNoDeprecation is the matched control. It
// keeps the assertion above from passing on a daemon that published a deprecation
// line unconditionally, and it holds the other direction of the posture: the
// unmarked workspace still gets its real diagnostic.
func TestServeValidateWithoutAMarkerPublishesNoDeprecation(t *testing.T) {
	srv, aggPath, aggBody := deprecatedPinWorkspace(t, false)
	diags := validateDiagnosticsFor(t, srv, aggPath, aggBody)

	var sawOther bool
	for _, d := range diags {
		if strings.Contains(d.Message, "is deprecated") {
			t.Errorf("no recorded marker must publish nothing; got: %s", d.Message)
		}
		if strings.Contains(d.Message, "absent-server") {
			sawOther = true
		}
	}
	if !sawOther {
		t.Fatalf("the control fixture produced no real diagnostic, so the comparison above proves nothing; "+
			"published set:\n%+v", diags)
	}
}
