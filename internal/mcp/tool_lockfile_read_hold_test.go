// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// TestEveryReleaseToolRefusesALockfileThatLeadsOut holds the tools that read a
// Release's lockfile to the read every other surface makes of it: a lockfile
// that is a link leading out of the workspace is refused, naming the link, before
// anything is resolved through the pins it records, and the file it leads to is
// left as it was.
//
// Each tool is called twice through the real dispatch, by name: once with the
// lockfile a link leading out, and once with it a link to a lockfile elsewhere
// inside the workspace, which is read. The second call is what makes the first one
// evidence — a tool refusing every linked lockfile, or failing on this fixture for
// a reason of its own, would answer both alike.
//
// The build's refusal also carries the answer's structured payload, as its other
// refusal ahead of the render does, so a caller reads the Release it was about and
// that no file was written field by field rather than out of prose.
func TestEveryReleaseToolRefusesALockfileThatLeadsOut(t *testing.T) {
	const refusal = "Could not read the Release's lockfile."
	for _, row := range []struct {
		tool string
		args map[string]any
	}{
		{tool: "build", args: map[string]any{"release": theLaunchedWorkspacesRelease}},
		{tool: "validate", args: map[string]any{"release": theLaunchedWorkspacesRelease}},
		{tool: "describe", args: map[string]any{"release": theLaunchedWorkspacesRelease}},
		{tool: "attach-candidates", args: map[string]any{"graph_node": "web", "release": theLaunchedWorkspacesRelease}},
		{tool: "attachment-container", args: map[string]any{"graph_node": "web", "release": theLaunchedWorkspacesRelease}},
		{tool: connectionAtPositionToolName, args: map[string]any{"file": theLaunchedWorkspacesRelease,
			"position": map[string]any{"line": 0, "character": 0}, "release": theLaunchedWorkspacesRelease}},
	} {
		t.Run(row.tool, func(t *testing.T) {
			root := rootedWorkspace(t)
			lock := lockfile.PathFor(filepath.Join(root, filepath.FromSlash(theLaunchedWorkspacesRelease)))
			outside := filepath.Join(t.TempDir(), "green.lock")
			inside := filepath.Join(root, "kept", "green.lock")
			for _, at := range []string{outside, inside} {
				if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := lockfile.WriteLockfile(at, &lockfile.Lockfile{Version: 1, Release: "green.yaml",
					GeneratedAt: "2026-08-20T09:00:00Z"}); err != nil {
					t.Fatalf("write the lockfile fixture: %v", err)
				}
			}
			before, err := os.ReadFile(outside)
			if err != nil {
				t.Fatal(err)
			}
			s := &server{out: io.Discard, logw: io.Discard, serverVersion: "test", launchRoot: root,
				transport: transportStdio}

			if err := os.Symlink(inside, lock); err != nil {
				t.Skipf("symbolic links are not available here: %v", err)
			}
			control := callDiscoveryTool(t, s, row.tool, row.args)
			if strings.Contains(control.Content[0].Text, refusal) {
				t.Fatalf("the control failed: %s refused a lockfile linked to a place inside the workspace, so the "+
					"refusal below would not be about where the link leads: %q", row.tool, control.Content[0].Text)
			}

			if err := os.Remove(lock); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, lock); err != nil {
				t.Fatal(err)
			}
			res := callDiscoveryTool(t, s, row.tool, row.args)
			text := res.Content[0].Text
			if !res.IsError {
				t.Fatalf("%s answered over a lockfile that is a link leading out of the workspace: %q", row.tool, text)
			}
			named := filepath.Join("parts", "green.lock") + " leads to"
			if !strings.Contains(text, refusal) || !strings.Contains(text, named) {
				t.Fatalf("%s failed, but not on the lockfile's link, so this row proves nothing about the read: %q",
					row.tool, text)
			}
			if after, err := os.ReadFile(outside); err != nil || string(after) != string(before) {
				t.Errorf("%s changed the lockfile outside the workspace (read error %v)", row.tool, err)
			}

			if row.tool != "build" {
				return
			}
			if len(res.StructuredContent) == 0 {
				t.Fatal("the build's refusal carries no structured payload")
			}
			var got struct {
				Release              *string          `json:"release"`
				Files                *[]buildFile     `json:"files"`
				DependencyResolution *json.RawMessage `json:"dependency_resolution"`
			}
			if err := json.Unmarshal(res.StructuredContent, &got); err != nil {
				t.Fatalf("decode the build's refusal: %v", err)
			}
			if got.Release == nil || !strings.HasSuffix(*got.Release, filepath.FromSlash(theLaunchedWorkspacesRelease)) {
				t.Errorf("the build's refusal does not name the Release it was about: %+v", got.Release)
			}
			if got.Files == nil || len(*got.Files) != 0 {
				t.Errorf("the build's refusal does not report an empty file list: %+v", got.Files)
			}
			if got.DependencyResolution != nil {
				t.Errorf("a build that filled nothing reported a dependency resolution: %s", *got.DependencyResolution)
			}
		})
	}
}
