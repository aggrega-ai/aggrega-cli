// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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
