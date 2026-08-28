// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// The AI-native surfaces' side of the deprecation report. The deciding principle
// is what the tool PRODUCES, not how the finding was reached: build produces an
// artifact and therefore refuses where the workspace configured a halt; validate
// and describe report on a Release and therefore name the withdrawal and carry on.
// All three directions are asserted, because a surface that silently adopted
// another posture is exactly the drift these tools were silent under before.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// unmarshalBuildPayload decodes the build tool's structured payload, failing the
// test on a decode error.
func unmarshalBuildPayload(t *testing.T, res *callToolResult) buildResultPayload {
	t.Helper()
	var payload buildResultPayload
	if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
		t.Fatalf("unmarshal build payload: %v", err)
	}
	return payload
}

const (
	mcpDeprecationStamp = "2026-08-01T09:30:00Z"
	mcpDeprecatedURI    = "oci://reg.example/plugins/helm-renderer:1.4.0"
)

// deprecatedPinWorkspace materialises the clean release-parity fixture — so the
// only thing wrong with it is the recorded marker — plus a lockfile recording a
// withdrawal and, optionally, the build halt dial.
//
// The report reads the lockfile's marker-bearing rows, which in a workspace pulled
// by the product are exactly the references the Release consumes: a pull composes
// the row set from a freshly computed closure and carries no prior row forward.
// The fixture writes the row directly because the test is about what each surface
// does with a recorded marker, not about how one comes to be recorded.
func deprecatedPinWorkspace(t *testing.T, haltSeverity string) (root string, dirs []string, releasePath string) {
	t.Helper()
	root, dirs = writeReleaseParityWorkspace(t, releaseParityCase{
		aggregate: releaseParityBackendAggregate,
		manifest:  releaseParityCleanManifest,
	})
	releasePath = filepath.Join(root, "release.yaml")

	// An empty build block is not a legal config, so the unconfigured case writes a
	// config carrying no build key at all — which is the shipped default the control
	// case is asserting against.
	config := "offline: false\n"
	if haltSeverity != "" {
		config += "build:\n  halt_on_deprecated_severity: \"" + haltSeverity + "\"\n"
	}
	if err := os.WriteFile(filepath.Join(root, ".aggrega-config.yaml"), []byte(config), 0o644); err != nil {
		t.Fatalf("write workspace config: %v", err)
	}

	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     "./release.yaml",
		GeneratedAt: mcpDeprecationStamp,
		Plugins: []lockfile.Plugin{{
			URI:             mcpDeprecatedURI,
			ResolvedVersion: "1.4.0",
			Deprecation: &lockfile.DeprecationRecord{
				Severity: "retracted",
				Message:  "withdrawn: unsound renderer",
			},
		}},
	}
	if err := lockfile.WriteLockfile(lockfile.PathFor(releasePath), lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}
	return root, dirs, releasePath
}

// TestBuildTool_DeprecatedPinRefusesUnderAConfiguredHalt pins the artifact verb's
// side. Rendering to memory rather than to disk does not soften the verdict: what
// the tool returns is still an artifact the caller may write, so it reaches the
// same verdict the command line's build reaches under the same configuration. A
// tool exempted from a policy the workspace explicitly configured is the weakest
// possible outcome — a configured policy that does not apply.
func TestBuildTool_DeprecatedPinRefusesUnderAConfiguredHalt(t *testing.T) {
	s := newTestServer(t)
	root, dirs, releasePath := deprecatedPinWorkspace(t, "retracted")

	res := callTool(t, s, "build", buildArgs{
		Release:       releasePath,
		WorkspaceRoot: root,
		CatalogDirs:   dirs,
	})
	if !res.IsError {
		t.Fatalf("a configured halt must refuse the build; got a success payload: %s", res.Content[0].Text)
	}
	text := res.Content[0].Text
	// The refusal carries the whole report, not only the halting sentence: choosing
	// a stricter threshold must never cost the caller a finding.
	for _, want := range []string{
		mcpDeprecatedURI, "is deprecated (retracted)", "withdrawn: unsound renderer",
		"as of " + mcpDeprecationStamp, "aggrega pull", `halt_on_deprecated_severity is "retracted"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal drops %q:\n%s", want, text)
		}
	}
}

// TestBuildTool_DeprecatedPinReportsAndBuildsWithNoConfiguredHalt is the matched
// control on the dial, and it is the shipped default. A recorded marker is registry
// state applied after the pin was taken, so an unconfigured workspace builds — and
// is told. Without this case the assertion above would also pass on a tool that
// refused every recorded marker regardless of configuration.
func TestBuildTool_DeprecatedPinReportsAndBuildsWithNoConfiguredHalt(t *testing.T) {
	s := newTestServer(t)
	root, dirs, releasePath := deprecatedPinWorkspace(t, "")

	res := callTool(t, s, "build", buildArgs{
		Release:       releasePath,
		WorkspaceRoot: root,
		CatalogDirs:   dirs,
	})
	if res.IsError {
		t.Fatalf("no configured threshold must halt on nothing; got: %s", res.Content[0].Text)
	}
	payload := unmarshalBuildPayload(t, res)
	if len(payload.Deprecations) == 0 {
		t.Fatalf("reporting is unconditional: a successful build must still carry the report; payload: %+v", payload)
	}
	joined := strings.Join(payload.Deprecations, "\n")
	for _, want := range []string{mcpDeprecatedURI, "is deprecated (retracted)", "as of " + mcpDeprecationStamp} {
		if !strings.Contains(joined, want) {
			t.Errorf("the report drops %q:\n%s", want, joined)
		}
	}
}

// TestDescribeTool_DeprecatedPinIsDescribedNotRefused pins a report verb's side:
// the description the caller asked for still arrives, and the withdrawal is named.
// A tool that refused would satisfy neither; one that stayed silent would satisfy
// only the first, which is the state this closes.
func TestDescribeTool_DeprecatedPinIsDescribedNotRefused(t *testing.T) {
	s := newTestServer(t)
	root, dirs, releasePath := deprecatedPinWorkspace(t, "retracted")

	res := callTool(t, s, "describe", describeArgs{
		Release:       releasePath,
		WorkspaceRoot: root,
		CatalogDirs:   dirs,
	})
	if res.IsError {
		t.Fatalf("describe must not refuse: a report verb describes the Release and names the fault; got: %s",
			res.Content[0].Text)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "closure entities:") {
		t.Errorf("describe must still deliver the Release description; got:\n%s", text)
	}
	if !strings.Contains(text, mcpDeprecatedURI) {
		t.Errorf("describe must name the withdrawn pin; got:\n%s", text)
	}
	// The halt dial is configured in this fixture and must not reach a report verb:
	// the dial governs the build, and a report that refused on it would be a second
	// stance on one key.
	if strings.Contains(text, "halt_on_deprecated_severity") {
		t.Errorf("a report verb must not act on the build's halt dial; got:\n%s", text)
	}
}

// TestValidateTool_DeprecatedPinTravelsAsAnAdvisoryNotice pins the other report
// verb. The notice travels in the payload's advisory channel and the verdict is
// untouched, which is what "advise" means here: the enumeration is the deliverable
// and a recorded marker neither creates nor suppresses a verdict.
func TestValidateTool_DeprecatedPinTravelsAsAnAdvisoryNotice(t *testing.T) {
	s := newTestServer(t)
	root, dirs, releasePath := deprecatedPinWorkspace(t, "retracted")

	res := callTool(t, s, "validate", validateArgs{
		Release:       releasePath,
		WorkspaceRoot: root,
		CatalogDirs:   dirs,
	})
	if res.IsError {
		t.Fatalf("validate reported a protocol error: %s", res.Content[0].Text)
	}
	payload := unmarshalValidatePayload(t, res)
	joined := strings.Join(payload.Notices, "\n")
	if !strings.Contains(joined, mcpDeprecatedURI) {
		t.Errorf("validate must name the withdrawn pin among its notices; notices: %#v", payload.Notices)
	}
	for _, d := range payload.Diagnostics {
		if strings.Contains(d, "is deprecated") {
			t.Errorf("a recorded marker is advisory here, not a blocking diagnostic: %s", d)
		}
	}
	if !payload.Valid {
		t.Errorf("a clean Release with a recorded marker stays valid; diagnostics: %#v", payload.Diagnostics)
	}
}

// TestReleaseAdvisoriesLeadWithTheDeprecationReport pins the ordering the advisory
// channel is assembled in: the deprecation report precedes the gate advisories,
// matching the order the facts were decided in — the markers were recorded at the
// pull that wrote the lockfile, before any gate read anything.
func TestReleaseAdvisoriesLeadWithTheDeprecationReport(t *testing.T) {
	lf := &lockfile.Lockfile{
		GeneratedAt: mcpDeprecationStamp,
		Entities: []lockfile.Entity{{
			URI:         mcpDeprecatedURI,
			Deprecation: &lockfile.DeprecationRecord{Severity: "warning", Message: "superseded"},
		}},
	}
	vr := &compilerops.ValidateResult{LintWarnings: []string{"pillar overlap on api"}}

	got := releaseAdvisories(vr, lf)
	if len(got) != 2 {
		t.Fatalf("want the deprecation notice and the overlap advisory; got %#v", got)
	}
	if !strings.Contains(got[0], mcpDeprecatedURI) {
		t.Errorf("the deprecation report must lead the advisory channel; got %#v", got)
	}
	if got[1] != "pillar overlap on api" {
		t.Errorf("the gate advisories must follow unchanged; got %#v", got)
	}

	// An absent lockfile contributes nothing and needs no guard at the call site:
	// where there is no lockfile there are no recorded markers either.
	if got := releaseAdvisories(vr, nil); len(got) != 1 || got[0] != "pillar overlap on api" {
		t.Errorf("an absent lockfile must contribute nothing; got %#v", got)
	}
}
