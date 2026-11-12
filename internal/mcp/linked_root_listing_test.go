// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestTheListingsOfAWorkspaceReachedThroughALinkListItsContent drives the real
// tools/call dispatch over the process's own streams, through list-releases and
// list-entities, for a workspace reached three ways: named as itself (the
// control), named in workspace_root through a link to it, and as the workspace of
// a server started over that link. Each is answered as the workspace it reaches,
// in the spellings the two tools publish: workspace_root the root as it was
// named, its two Releases and its entities' files relative to that root, its
// Aggregate and Releases among the entities, and the link inside it leading out
// named as not read and its Release left unlisted — the boundary judged through
// the root as it is judged directly.
//
// A Release path is relative to the root only where the walk handed it on spelled
// beneath the root as named: one spelled beneath the place a link leads lies
// outside that root as written and is not listed, so the relative paths assert
// the spelling as well as the content.
func TestTheListingsOfAWorkspaceReachedThroughALinkListItsContent(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	via := filepath.Join(base, "via")
	release := func(name string) string {
		return "apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: " + name + "\n  version: \"1.0.0\"\n"
	}
	writeLinkedDirsFile(t, filepath.Join(realDir, workspaceconfig.ConfigFilename), "workspace: {}\noffline: true\n")
	writeLinkedDirsFile(t, filepath.Join(realDir, "aggregate.yaml"),
		"apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\nmetadata:\n  name: app\nspec:\n  components: {}\n")
	writeLinkedDirsFile(t, filepath.Join(realDir, "release-a.yaml"), release("a"))
	writeLinkedDirsFile(t, filepath.Join(realDir, "release-b.yaml"), release("b"))
	writeLinkedDirsFile(t, filepath.Join(base, "outside", "release-out.yaml"), release("out"))
	linkLinkedDirs(t, filepath.Join(base, "outside", "release-out.yaml"), filepath.Join(realDir, "leads-out.yaml"))
	linkLinkedDirs(t, realDir, via)

	for _, row := range []struct {
		name, launchRoot, rootArg, spelled string
	}{
		{"control: the workspace named as itself", realDir, realDir, realDir},
		{"workspace_root a link to the workspace", realDir, via, via},
		{"a server started over a link to the workspace", via, "", via},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			s := heldConfigurationServer(t, row.launchRoot)
			args := map[string]any{"response_format": "detailed"}
			if row.rootArg != "" {
				args["workspace_root"] = row.rootArg
			}

			var releases listReleasesResult
			decodeStructured(t, callToolOn(t, s, listenerRequest(), "list-releases", args), &releases)
			if got := answeredRoot(releases.WorkspaceRoot); got != row.spelled {
				t.Errorf("list-releases answers workspace_root %s; want %q — the root as it was named", got, row.spelled)
			}
			want := []string{"release-a.yaml", "release-b.yaml"}
			if !slices.Equal(releases.Releases, want) {
				t.Errorf("list-releases lists %v; want %v — the workspace's Releases, relative to the root as named",
					releases.Releases, want)
			}
			if !slices.Equal(releases.UnreadLinkedFiles, []string{"leads-out.yaml"}) {
				t.Errorf("list-releases names %v as not read; want [leads-out.yaml]", releases.UnreadLinkedFiles)
			}

			var entities listEntitiesResult
			decodeStructured(t, callToolOn(t, s, listenerRequest(), "list-entities", args), &entities)
			if got := answeredRoot(entities.WorkspaceRoot); got != row.spelled {
				t.Errorf("list-entities answers workspace_root %s; want %q — the root as it was named", got, row.spelled)
			}
			var files []string
			for _, e := range entities.Entities {
				files = append(files, e.Kind+" "+e.File)
			}
			wantFiles := []string{"Aggregate aggregate.yaml", "Release release-a.yaml", "Release release-b.yaml"}
			if !slices.Equal(files, wantFiles) {
				t.Errorf("list-entities lists %v; want %v", files, wantFiles)
			}
			if !slices.Equal(entities.UnreadLinkedFiles, []string{"leads-out.yaml"}) {
				t.Errorf("list-entities names %v as not read; want [leads-out.yaml]", entities.UnreadLinkedFiles)
			}
		})
	}
}

// answeredRoot renders the workspace_root a listing answered, marking one it kept
// back so a failure says which of the two it saw.
func answeredRoot(root *string) string {
	if root == nil {
		return "(kept back)"
	}
	return *root
}
