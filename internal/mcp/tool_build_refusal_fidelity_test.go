// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// The build tool's failure path, against the fidelity rule in this package's
// documentation: a refusal carries whatever findings the run had in hand, in the
// same shape a success reports them.
//
// The assertions here read the RESULT — the failure flag, the structured payload
// and the text — because that is the whole of what a caller receives. A payload
// assembled and not delivered satisfies no caller, and a test that reached into
// the tool's own intermediate state could not tell the two apart.

import (
	"strings"
	"testing"
)

// TestBuildToolRefusalCarriesItsFindingsStructurally is the proof for the
// surface's failure path.
//
// The fixture is a Release whose lockfile records a withdrawal and whose
// workspace configures a halt at that severity, so the build refuses on a finding
// the run had already made. What the refusal must carry is that finding, as an
// entry a caller can read field by field, rather than only as a sentence.
//
// The structured payload is the assertion that matters. A refusal reporting its
// findings as prose while a success reports them as fields is one capability
// answering at two fidelities depending on its verdict, and the failing case is
// the one a caller has to act on programmatically — it is the run whose input it
// has to change.
func TestBuildToolRefusalCarriesItsFindingsStructurally(t *testing.T) {
	s := newTestServer(t)
	root, dirs, releasePath, deprecatedURI := deprecatedPinWorkspace(t, "retracted")

	res := callTool(t, s, "build", buildArgs{
		Release:       releasePath,
		WorkspaceRoot: root,
		CatalogDirs:   dirs,
	})
	if !res.IsError {
		t.Fatalf("a configured halt must refuse the build, or this is not the refusal case the assertions "+
			"below are about: %s", res.Content[0].Text)
	}
	if len(res.StructuredContent) == 0 {
		t.Fatalf("the refusal carries no structured payload at all, so a caller reading fields receives "+
			"nothing and has only the sentence to parse.\ntext:\n%s", res.Content[0].Text)
	}

	payload := unmarshalBuildPayload(t, res)
	if told(payload.Release) != releasePath {
		t.Errorf("the refusal names release %q; want %q — a caller holding several calls in flight "+
			"identifies the answer by what it was about", told(payload.Release), releasePath)
	}
	if len(payload.Deprecations) == 0 {
		t.Fatalf("the refusal carries no recorded marker, and the marker is the finding the run made "+
			"before it stopped; payload: %+v", payload)
	}
	joined := strings.Join(payload.Deprecations, "\n")
	for _, want := range []string{deprecatedURI, "withdrawn: unsound renderer"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the refusal's recorded markers drop %q:\n%s", want, joined)
		}
	}

	// No rendered files, and the absence is a statement rather than an omission:
	// the tool emitted nothing, so an empty list asserting a build that produced
	// no files would be a different fact from the one that happened.
	if len(payload.Files) != 0 {
		t.Errorf("the refusal reports %d rendered file(s); a build that refused emitted none", len(payload.Files))
	}
}

// TestBuildToolReportsTheSameFindingsWhicheverWayItEnds is the matched control,
// and it is what makes the case above an assertion about fidelity rather than
// about the presence of a field.
//
// The two runs differ in the halt dial and in nothing else: the same workspace,
// the same lockfile, the same recorded withdrawal. One refuses and one builds. A
// caller must be told the same thing about the marker either way, or choosing a
// stricter threshold costs it a finding — which inverts what configuring a
// threshold is for.
//
// Without this pairing the case above would also pass on a surface that reported
// markers only when it refused, which is the same defect facing the other way.
func TestBuildToolReportsTheSameFindingsWhicheverWayItEnds(t *testing.T) {
	refusedRoot, refusedDirs, refusedRelease, _ := deprecatedPinWorkspace(t, "retracted")
	builtRoot, builtDirs, builtRelease, _ := deprecatedPinWorkspace(t, "")

	refused := callTool(t, newTestServer(t), "build", buildArgs{
		Release:       refusedRelease,
		WorkspaceRoot: refusedRoot,
		CatalogDirs:   refusedDirs,
	})
	built := callTool(t, newTestServer(t), "build", buildArgs{
		Release:       builtRelease,
		WorkspaceRoot: builtRoot,
		CatalogDirs:   builtDirs,
	})
	if !refused.IsError {
		t.Fatalf("the halting configuration must refuse: %s", refused.Content[0].Text)
	}
	if built.IsError {
		t.Fatalf("the unconfigured workspace must build: %s", built.Content[0].Text)
	}

	refusedPayload := unmarshalBuildPayload(t, refused)
	builtPayload := unmarshalBuildPayload(t, built)
	if len(refusedPayload.Deprecations) != len(builtPayload.Deprecations) {
		t.Fatalf("the refusal reports %d recorded marker(s) and the build reports %d; the two runs found "+
			"the same thing and must say so at the same fidelity.\nrefused: %v\nbuilt:   %v",
			len(refusedPayload.Deprecations), len(builtPayload.Deprecations),
			refusedPayload.Deprecations, builtPayload.Deprecations)
	}
	for i := range refusedPayload.Deprecations {
		if refusedPayload.Deprecations[i] != builtPayload.Deprecations[i] {
			t.Errorf("recorded marker %d differs between the two verdicts:\nrefused: %q\nbuilt:   %q",
				i, refusedPayload.Deprecations[i], builtPayload.Deprecations[i])
		}
	}
}

// TestBuildToolPreFlightRefusalCarriesNoFindings is the third direction, and it
// is why the rule is about fidelity rather than about always attaching a payload.
//
// A call naming no Release never reached the compiler, so there is nothing for it
// to have found. Its answer is the correction, and a structured payload beside
// that correction would report an empty result for a run that never happened —
// which a caller cannot distinguish from a run that found nothing.
func TestBuildToolPreFlightRefusalCarriesNoFindings(t *testing.T) {
	res := callTool(t, newTestServer(t), "build", buildArgs{})
	if !res.IsError {
		t.Fatal("a call naming no Release must be refused")
	}
	if len(res.StructuredContent) != 0 {
		t.Errorf("a refusal that ran nothing carries a structured payload: %s", res.StructuredContent)
	}
	if !strings.Contains(res.Content[0].Text, "release") {
		t.Errorf("the correction does not name the missing argument: %s", res.Content[0].Text)
	}
}
