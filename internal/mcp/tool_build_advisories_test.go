// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tool surface's half of the one advisory channel.
//
// A caller driving the compiler through this surface reads no terminal, so before
// the channel existed these findings were unreachable from here by construction —
// not missing from a response a refactor could add them to, but produced only as
// bytes on a process stream a tool client cannot see. These proofs read the
// RESPONSE, which is the only place that changes.

// advisoryBuildWorkspace copies the hermetic end-to-end fixture into a temp
// workspace and splices in one parameter declaration no reference reads, so the
// build raises an advisory without failing.
//
// It is copied rather than used in place because the splice has to reach a file on
// disk — the build resolves the Release's sibling Aggregate itself rather than
// taking a buffer — and the fixture is shared with other proofs that must keep
// producing exactly what they expect.
func advisoryBuildWorkspace(t *testing.T) (root, catalogDir, releasePath string) {
	t.Helper()
	src := filepath.Join(moduleRoot(t), "internal", "compiler", "testdata", "fullstack-app")
	root = t.TempDir()
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatalf("copy fixture workspace: %v", err)
	}

	aggPath := filepath.Join(root, "aggregate.yaml")
	aggBytes, err := os.ReadFile(aggPath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	const anchor = "spec:\n"
	const declared = "spec:\n" +
		"  parameters:\n" +
		"    unread_probe:\n" +
		"      type: string\n"
	if !bytes.Contains(aggBytes, []byte(anchor)) {
		t.Fatalf("fixture no longer carries the %q anchor; the splice would be silently inert", anchor)
	}
	spliced := bytes.Replace(aggBytes, []byte(anchor), []byte(declared), 1)
	if bytes.Equal(spliced, aggBytes) {
		t.Fatal("splice changed nothing; the probe parameter would not be declared")
	}
	if err := os.WriteFile(aggPath, spliced, 0o644); err != nil {
		t.Fatalf("write spliced aggregate: %v", err)
	}
	return root, filepath.Join(root, "catalog"), filepath.Join(root, "release.yaml")
}

// buildPayloadOf invokes the build tool over the supplied workspace and returns
// its structured payload plus the text form.
func buildPayloadOf(t *testing.T, root, catalogDir, releasePath string) (buildResultPayload, string) {
	t.Helper()
	s := newTestServer(t)
	res := callTool(t, s, "build", buildArgs{
		Release:       releasePath,
		WorkspaceRoot: root,
		CatalogDirs:   []string{catalogDir},
	})
	if res.IsError {
		t.Fatalf("build reported an error, so the successful-build channel cannot be read: %s", res.Content[0].Text)
	}
	var payload buildResultPayload
	if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
		t.Fatalf("unmarshal build payload: %v", err)
	}
	return payload, res.Content[0].Text
}

// TestBuildToolCarriesTheAdvisoriesTheBuildRaised is the proof that the surface
// which structurally could not deliver these findings now does.
//
// It asserts the STRUCTURED payload and the text form separately. A caller
// choosing the text form must not receive fewer findings for that choice, and a
// caller matching programmatically needs the entry rather than a sentence — so
// each is a distinct promise and one passing does not imply the other.
func TestBuildToolCarriesTheAdvisoriesTheBuildRaised(t *testing.T) {
	root, catalogDir, releasePath := advisoryBuildWorkspace(t)
	payload, text := buildPayloadOf(t, root, catalogDir, releasePath)

	if len(payload.Files) == 0 {
		t.Fatal("the build rendered no files, so this is not the successful-build case the assertions below assume")
	}
	if len(payload.Advisories) == 0 {
		t.Fatal("the build raised no advisory in the answer; a caller with no terminal receives nothing, " +
			"which is the defect this channel exists to close")
	}

	var found *buildAdvisory
	for i := range payload.Advisories {
		if strings.Contains(payload.Advisories[i].Message, "unread_probe") {
			found = &payload.Advisories[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no advisory names the unread parameter; got %+v", payload.Advisories)
	}
	if found.Severity != "warn" {
		t.Errorf("advisory severity = %q; want %q — an advisory must not arrive at the halting tier on a "+
			"build that succeeded", found.Severity, "warn")
	}
	if found.Code == "" {
		t.Error("advisory carries no code; a caller matching the class programmatically has nothing to match on")
	}
	// The path into the authored document travels too, which is what lets a client
	// point the author at the declaration rather than at the Release.
	if found.KeyPath == "" {
		t.Error("advisory carries no key path; the finding named one, so it was available and was lost")
	} else if !strings.Contains(found.KeyPath, "unread_probe") {
		t.Errorf("advisory key path = %q; want the path to the unread declaration", found.KeyPath)
	}

	// The text form carries it too.
	if !strings.Contains(text, "unread_probe") {
		t.Errorf("the text form does not name the advisory; a caller reading text receives fewer findings "+
			"than one reading the payload.\ntext:\n%s", text)
	}
}

// unrenderableMappedRecipe rewrites the Recipe the fixture's Manifest maps one
// component type onto, so its workload template cannot be parsed.
//
// The fault is deliberately a template the render cannot even parse, and the
// placement is what the proof depends on rather than a matter of taste. It has to
// sit INSIDE the render, because every earlier gate refuses before the phase that
// raises the advisory has run — at which point there is no finding in hand and
// carrying it would be a claim about nothing.
//
// The rewrite is anchored on the file's existing identity, so a fixture
// reorganisation fails loudly here instead of silently leaving the workspace
// renderable and the refusal half of the proof vacuous.
func unrenderableMappedRecipe(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, "catalog", "recipes", "helm-synthesized-with-artifacts", "api-server.yaml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the fixture no longer carries the mapped Recipe this fault is written into, so the refusal "+
			"half of this proof has nothing to break: %v", err)
	}
	const anchor = "image: api-server:latest"
	if !bytes.Contains(body, []byte(anchor)) {
		t.Fatalf("the mapped Recipe no longer carries the %q anchor; the rewrite would be silently inert and "+
			"the workspace would stay renderable", anchor)
	}
	broken := bytes.Replace(body, []byte(anchor), []byte(`image: "{{ this action is never closed`), 1)
	if bytes.Equal(broken, body) {
		t.Fatal("the rewrite changed nothing; the render would still complete")
	}
	if err := os.WriteFile(path, broken, 0o644); err != nil {
		t.Fatalf("write the unrenderable Recipe: %v", err)
	}
}

// TestBuildToolRefusalCarriesTheAdvisoriesTheRunRaised is the headline proof for
// this surface: a run that RAISED an advisory and then REFUSED reports it.
//
// # The fixture pair, and why it differs in exactly one thing
//
// Both halves are independent copies of ONE fixture carrying ONE splice — the
// same unread parameter declaration, in the same Aggregate, driven at the same
// Release under the same Manifest. The only difference is that one copy's mapped
// Recipe carries a workload template the render cannot parse. So the pair differs
// in whether the run reaches its end and in nothing else, and the refusal cannot
// be attributed to a second fault a separately-written workspace would have been
// free to carry.
//
// # Where the halt sits, and why that placement is the point
//
// The fault is in the render, which runs AFTER the phase that raises the
// advisory. So the run genuinely holds the finding at the moment it stops — which
// is the only situation in which carrying it is a claim about fidelity rather
// than about a coincidence of ordering.
//
// # The expectation is derived from the fixture, not from the implementation
//
// The token asserted below is the parameter name THIS TEST authors. The
// implementation reads it only as input data and never as an expectation, so the
// assertion cannot be satisfied by a defect planted on both sides of the
// comparison: for the advisory to name it, the finding has to have travelled.
func TestBuildToolRefusalCarriesTheAdvisoriesTheRunRaised(t *testing.T) {
	refusedRoot, refusedCatalog, refusedRelease := advisoryBuildWorkspace(t)
	unrenderableMappedRecipe(t, refusedRoot)

	refused := callTool(t, newTestServer(t), "build", buildArgs{
		Release:       refusedRelease,
		WorkspaceRoot: refusedRoot,
		CatalogDirs:   []string{refusedCatalog},
	})
	if !refused.IsError {
		t.Fatalf("the unresolvable mapping must refuse the build, or this is not the refusal case every "+
			"assertion below is about: %s", refused.Content[0].Text)
	}
	if len(refused.StructuredContent) == 0 {
		t.Fatalf("the refusal carries no structured payload at all, so a caller reading fields receives "+
			"nothing.\ntext:\n%s", refused.Content[0].Text)
	}

	refusedPayload := unmarshalBuildPayload(t, refused)
	if len(refusedPayload.Advisories) == 0 {
		t.Fatalf("the refusal carries no advisory. The run raised one before it stopped, so a caller with no "+
			"terminal is told nothing about a finding it has to act on.\npayload: %+v\ntext:\n%s",
			refusedPayload, refused.Content[0].Text)
	}

	var found *buildAdvisory
	for i := range refusedPayload.Advisories {
		if strings.Contains(refusedPayload.Advisories[i].Message, "unread_probe") {
			found = &refusedPayload.Advisories[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("the refusal reports %d advisory(ies) and none names the unread parameter this fixture "+
			"declares; got %+v", len(refusedPayload.Advisories), refusedPayload.Advisories)
	}
	if found.Severity != "warn" {
		t.Errorf("advisory severity = %q; want %q — a refusal must not promote an advisory to the halting "+
			"tier just because the run ended badly", found.Severity, "warn")
	}
	if found.Code == "" {
		t.Error("the refusal's advisory carries no code; a caller matching the class programmatically has " +
			"nothing to match on")
	}
	if found.KeyPath == "" {
		t.Error("the refusal's advisory carries no key path; the finding named one, so it was available and " +
			"was lost on the way out")
	} else if !strings.Contains(found.KeyPath, "unread_probe") {
		t.Errorf("advisory key path = %q; want the path to the unread declaration", found.KeyPath)
	}

	// Recorded so what a refusal actually hands a caller is readable from a verbose
	// run rather than only inferable from the assertions that passed.
	t.Logf("the refusal answered with:\n  message: %s\n  advisory: %+v", refused.Content[0].Text, *found)

	// No rendered files, which is what distinguishes this from a success carrying
	// the same advisory. Without it the assertions above would also pass on a run
	// that quietly completed.
	if len(refusedPayload.Files) != 0 {
		t.Errorf("the refusal reports %d rendered file(s); a build that refused emitted none", len(refusedPayload.Files))
	}

	// The matched control, and it is what makes the above an assertion about
	// FIDELITY rather than about a field being populated. The same fixture and the
	// same splice, with the Recipe left renderable: it succeeds, and it must report
	// the same advisory. Without this pairing the case above would also pass on a
	// surface that reported advisories only when it refused — the same defect
	// facing the other way.
	builtRoot, builtCatalog, builtRelease := advisoryBuildWorkspace(t)
	built, _ := buildPayloadOf(t, builtRoot, builtCatalog, builtRelease)
	if len(built.Advisories) != len(refusedPayload.Advisories) {
		t.Fatalf("the refusal reports %d advisory(ies) and the success reports %d; the two runs raised the "+
			"same finding and must say so at the same fidelity.\nrefused: %+v\nbuilt:   %+v",
			len(refusedPayload.Advisories), len(built.Advisories), refusedPayload.Advisories, built.Advisories)
	}
	for i := range built.Advisories {
		if built.Advisories[i] != refusedPayload.Advisories[i] {
			t.Errorf("advisory %d differs between the two verdicts:\nrefused: %+v\nbuilt:   %+v",
				i, refusedPayload.Advisories[i], built.Advisories[i])
		}
	}
}

// TestBuildToolReportsNoAdvisoriesForACleanRelease is the calibration without
// which the assertions above are unfalsifiable.
//
// A channel that reported something for every build would satisfy them and teach
// a caller to ignore it. The fixture as authored raises nothing, so the key must
// be absent from the payload entirely rather than present and empty — an empty
// list and no list are the same statement to a reader, and the omitted key is the
// one this wire makes.
func TestBuildToolReportsNoAdvisoriesForACleanRelease(t *testing.T) {
	root, catalogDir, releasePath := fullstackWorkspace(t)
	payload, text := buildPayloadOf(t, root, catalogDir, releasePath)

	if len(payload.Advisories) != 0 {
		t.Errorf("a Release raising no advisory produced %d: %+v", len(payload.Advisories), payload.Advisories)
	}
	if strings.Contains(text, "advisory finding") {
		t.Errorf("the text form reports advisories for a clean build:\n%s", text)
	}
}
