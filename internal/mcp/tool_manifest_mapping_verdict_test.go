// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// The AI-native surfaces' verdict on an inconsistent Manifest mapping. The
// deciding principle is what the tool PRODUCES, not how the finding was reached:
// an artifact verb refuses, a report verb enumerates. build produces artifacts and
// therefore refuses; describe reports on a Release and therefore describes it and
// says what is wrong with it. Both directions are asserted, because a surface that
// silently adopted the other posture is exactly the drift these tools were silent
// under before.

import (
	"path/filepath"
	"strings"
	"testing"
)

// mappingInconsistentWorkspace materialises the release-parity mapping-inconsistent
// case, reusing that harness's fixture so the two test files cannot come to
// disagree about what an inconsistent mapping looks like.
func mappingInconsistentWorkspace(t *testing.T) (root string, dirs []string, releasePath string) {
	t.Helper()
	root, dirs = writeReleaseParityWorkspace(t, releaseParityCase{
		aggregate: releaseParityBackendAggregate,
		manifest:  releaseParityMappingInconsistentManifest,
	})
	return root, dirs, filepath.Join(root, "release.yaml")
}

// cleanMappingWorkspace is the matched control: the identical fixture with a
// consistent mapping. Without it, a change that made the gate refuse everything
// would leave the assertions below green.
func cleanMappingWorkspace(t *testing.T) (root string, dirs []string, releasePath string) {
	t.Helper()
	root, dirs = writeReleaseParityWorkspace(t, releaseParityCase{
		aggregate: releaseParityBackendAggregate,
		manifest:  releaseParityCleanManifest,
	})
	return root, dirs, filepath.Join(root, "release.yaml")
}

// TestBuildTool_InconsistentMappingRefuses pins the artifact verb's side of the
// principle. The tool renders to memory rather than to disk, which does not soften
// the verdict: what it returns is still an artifact the caller may write.
func TestBuildTool_InconsistentMappingRefuses(t *testing.T) {
	s := newTestServer(t)
	root, dirs, releasePath := mappingInconsistentWorkspace(t)

	res := callTool(t, s, "build", buildArgs{
		Release:       releasePath,
		WorkspaceRoot: root,
		CatalogDirs:   dirs,
	})
	if !res.IsError {
		t.Fatalf("the build tool must refuse an inconsistent mapping; got a success payload: %s",
			res.Content[0].Text)
	}
	if !strings.Contains(res.Content[0].Text, "spec.implements") {
		t.Errorf("the refusal must name the mapping fault; got: %s", res.Content[0].Text)
	}

	// Control: the same fixture with a consistent mapping is not refused for this
	// reason.
	cleanRoot, cleanDirs, cleanRelease := cleanMappingWorkspace(t)
	clean := callTool(t, s, "build", buildArgs{
		Release:       cleanRelease,
		WorkspaceRoot: cleanRoot,
		CatalogDirs:   cleanDirs,
	})
	if clean.IsError && strings.Contains(clean.Content[0].Text, "spec.implements") {
		t.Errorf("a consistent mapping must not be refused for a mapping fault; got: %s",
			clean.Content[0].Text)
	}
}

// TestDescribeTool_InconsistentMappingIsDescribedNotRefused pins the report verb's
// side. Two things are asserted and both matter: the description the caller asked
// for still arrives, and the fault is named. A tool that refused would satisfy
// neither; a tool that stayed silent would satisfy only the first, leaving the
// caller holding a description that hides a fault it should have named.
func TestDescribeTool_InconsistentMappingIsDescribedNotRefused(t *testing.T) {
	s := newTestServer(t)
	root, dirs, releasePath := mappingInconsistentWorkspace(t)

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
	// The description survived.
	if !strings.Contains(text, "closure entities:") {
		t.Errorf("describe must still deliver the Release description; got:\n%s", text)
	}
	// And the fault is named.
	if !strings.Contains(text, "spec.implements") {
		t.Errorf("describe must name the mapping fault alongside the description; got:\n%s", text)
	}

	// Control: a consistent mapping produces the description with no mapping line,
	// so the assertion above is attributable to the fault rather than to the
	// renderer always emitting it.
	cleanRoot, cleanDirs, cleanRelease := cleanMappingWorkspace(t)
	clean := callTool(t, s, "describe", describeArgs{
		Release:       cleanRelease,
		WorkspaceRoot: cleanRoot,
		CatalogDirs:   cleanDirs,
	})
	if clean.IsError {
		t.Fatalf("describe over a consistent mapping must succeed; got: %s", clean.Content[0].Text)
	}
	if strings.Contains(clean.Content[0].Text, "mapping:") {
		t.Errorf("a consistent mapping must produce no mapping line; got:\n%s", clean.Content[0].Text)
	}
}
