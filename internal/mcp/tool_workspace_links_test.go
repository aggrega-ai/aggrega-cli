// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// linksTestComponent is a Component of the given name, laid where reading it
// shows up by that name.
func linksTestComponent(name string) string {
	return "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: " + name + "\nspec: {}\n"
}

// linksTestReleaseNamed is a Release of the given name, laid where reading it
// shows up by that name.
func linksTestReleaseNamed(name string) string {
	return "apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: " + name + "\n  version: \"1.0.0\"\n"
}

var linksTestRelease = linksTestReleaseNamed("probe")

// TestTheEntityListingNamesEachLinkLeadingOutOfTheWorkspace drives list-entities
// through the real tools/call dispatch over workspace roots holding symbolic
// links, on both transports, and reads unread_linked_dirs, unread_linked_files and
// the answer's text back.
//
// Each row lays its root beside one directory outside it holding a Component, a
// Release, and a directory holding a Component of its own, each named so that
// reading it shows up by name. A row that read through a link leading out shows
// up as a wrong count, that entity among the records, or a link missing from the
// list naming it. The rows whose workspace configuration names the linked Release
// — by a key of workspace.releases or by build.authoring_release, naming the link
// or the file it leads to — expect that Release listed at the link's place and the
// link not named.
//
// Every row also pins three things whatever it lays: each field reaches the
// caller as an array, never as null; the place a link leads to is named nowhere in
// the answer, structured or text, on either transport, and over a listener neither
// is the root; and the text carries the line naming each kind of link exactly
// where its field names any, and no such line where it names none.
func TestTheEntityListingNamesEachLinkLeadingOutOfTheWorkspace(t *testing.T) {
	const dirsOpening = "No document inside these links is listed as the workspace's own"
	const filesOpening = "No document behind these links is listed as the workspace's own"

	machineWide := filepath.Join(t.TempDir(), "machine-wide.yaml")
	writeLinkedDirsFile(t, machineWide, "registries: []\n")
	t.Setenv(workspaceconfig.EnvConfigOverride, machineWide)

	const noConfiguration = "workspace: {}\noffline: true\n"
	const namingTheLinkedRelease = "workspace:\n  releases:\n    linked-release.yaml: {}\noffline: true\n"
	linkTheRelease := func(t *testing.T, root, outside string) {
		linkLinkedDirs(t, filepath.Join(outside, "outside-release.yaml"), filepath.Join(root, "linked-release.yaml"))
	}

	rows := []struct {
		name      string
		transport transportMode
		// config is the workspace configuration's text, given the directory
		// outside the root; absent, the configuration names nothing.
		config func(outside string) string
		args   map[string]any
		// lay builds the row's links under root; outside is the directory beside it.
		lay       func(t *testing.T, root, outside string)
		wantDirs  []string
		wantFiles []string
		// wantRelease says the Release outside is listed, at linked-release.yaml.
		wantRelease bool
	}{
		{
			name:      "a workspace holding no link names none and says nothing about links",
			transport: transportStdio,
			args:      map[string]any{},
			lay:       func(t *testing.T, root, outside string) {},
			wantDirs:  []string{},
			wantFiles: []string{},
		},
		{
			name:      "a link to an entity document outside is not counted, and is named in the summary",
			transport: transportStdio,
			args:      map[string]any{},
			lay: func(t *testing.T, root, outside string) {
				linkLinkedDirs(t, filepath.Join(outside, "outside-component.yaml"), filepath.Join(root, "linked-component.yaml"))
			},
			wantDirs:  []string{},
			wantFiles: []string{"linked-component.yaml"},
		},
		{
			name:      "a link to a directory outside is named beside the records, and nothing in it is listed",
			transport: transportStdio,
			args:      map[string]any{"response_format": "detailed"},
			lay: func(t *testing.T, root, outside string) {
				linkLinkedDirs(t, filepath.Join(outside, "shared"), filepath.Join(root, "shared"))
			},
			wantDirs:  []string{"shared"},
			wantFiles: []string{},
		},
		{
			name:      "a page from the middle carries every link, since the links describe the workspace",
			transport: transportStdio,
			args:      map[string]any{"response_format": "detailed", "limit": 1, "offset": 1},
			lay: func(t *testing.T, root, outside string) {
				linkLinkedDirs(t, filepath.Join(outside, "shared"), filepath.Join(root, "shared"))
				linkLinkedDirs(t, filepath.Join(outside, "outside-component.yaml"), filepath.Join(root, "linked-component.yaml"))
			},
			wantDirs:  []string{"shared"},
			wantFiles: []string{"linked-component.yaml"},
		},
		{
			name:      "over a listener the same names are told, and nothing about where they lead",
			transport: transportHTTP,
			args:      map[string]any{"response_format": "detailed"},
			lay: func(t *testing.T, root, outside string) {
				linkLinkedDirs(t, filepath.Join(outside, "shared"), filepath.Join(root, "shared"))
				linkLinkedDirs(t, filepath.Join(outside, "outside-component.yaml"), filepath.Join(root, "linked-component.yaml"))
			},
			wantDirs:  []string{"shared"},
			wantFiles: []string{"linked-component.yaml"},
		},
		{
			name:      "over a listener a workspace holding no link names none",
			transport: transportHTTP,
			args:      map[string]any{},
			lay:       func(t *testing.T, root, outside string) {},
			wantDirs:  []string{},
			wantFiles: []string{},
		},
		{
			name:      "a link to a Release outside the configuration does not name is named, and its Release not counted",
			transport: transportStdio,
			args:      map[string]any{"response_format": "detailed"},
			lay:       linkTheRelease,
			wantDirs:  []string{},
			wantFiles: []string{"linked-release.yaml"},
		},
		{
			name:        "a workspace.releases key naming the link has the Release behind it listed, and the link not named",
			transport:   transportStdio,
			config:      func(string) string { return namingTheLinkedRelease },
			args:        map[string]any{"response_format": "detailed"},
			lay:         linkTheRelease,
			wantDirs:    []string{},
			wantFiles:   []string{},
			wantRelease: true,
		},
		{
			name:      "build.authoring_release naming the file the link leads to has the Release listed at the link's place",
			transport: transportStdio,
			config: func(outside string) string {
				return fmt.Sprintf("workspace: {}\nbuild:\n  authoring_release: %q\noffline: true\n",
					filepath.Join(outside, "outside-release.yaml"))
			},
			args:        map[string]any{"response_format": "detailed"},
			lay:         linkTheRelease,
			wantDirs:    []string{},
			wantFiles:   []string{},
			wantRelease: true,
		},
		{
			name:      "the configuration naming the linked Release leaves a link to another file outside named",
			transport: transportStdio,
			config:    func(string) string { return namingTheLinkedRelease },
			args:      map[string]any{"response_format": "detailed"},
			lay: func(t *testing.T, root, outside string) {
				linkTheRelease(t, root, outside)
				linkLinkedDirs(t, filepath.Join(outside, "outside-component.yaml"), filepath.Join(root, "linked-component.yaml"))
			},
			wantDirs:    []string{},
			wantFiles:   []string{"linked-component.yaml"},
			wantRelease: true,
		},
		{
			name:        "over a listener a Release the configuration names is listed where its link sits, and nothing says where it leads",
			transport:   transportHTTP,
			config:      func(string) string { return namingTheLinkedRelease },
			args:        map[string]any{"response_format": "detailed"},
			lay:         linkTheRelease,
			wantDirs:    []string{},
			wantFiles:   []string{},
			wantRelease: true,
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			config := noConfiguration
			if row.config != nil {
				config = row.config(outside)
			}
			writeLinkedDirsFile(t, filepath.Join(root, workspaceconfig.ConfigFilename), config)
			writeLinkedDirsFile(t, filepath.Join(root, "inside.yaml"), linksTestComponent("inside-component"))
			writeLinkedDirsFile(t, filepath.Join(root, "second.yaml"), linksTestComponent("second-component"))
			writeLinkedDirsFile(t, filepath.Join(outside, "outside-component.yaml"), linksTestComponent("outside-only-component"))
			writeLinkedDirsFile(t, filepath.Join(outside, "outside-release.yaml"), linksTestReleaseNamed("outside-only-release"))
			writeLinkedDirsFile(t, filepath.Join(outside, "shared", "shared-component.yaml"), linksTestComponent("shared-only-component"))
			row.lay(t, root, outside)

			s := &server{out: io.Discard, logw: io.Discard, launchRoot: root, transport: row.transport}
			res := callToolOn(t, s, listenerRequest(), "list-entities", row.args)
			if res.IsError {
				t.Fatalf("list-entities refused: %s", res.Content[0].Text)
			}

			var raw map[string]json.RawMessage
			if err := json.Unmarshal(res.StructuredContent, &raw); err != nil {
				t.Fatalf("decode the structured answer: %v (%s)", err, res.StructuredContent)
			}
			for _, member := range []string{"unread_linked_dirs", "unread_linked_files"} {
				encoded, present := raw[member]
				if !present {
					t.Fatalf("%s is absent from the answer; want it present, empty where nothing is named: %s",
						member, res.StructuredContent)
				}
				if trimmed := strings.TrimSpace(string(encoded)); !strings.HasPrefix(trimmed, "[") {
					t.Fatalf("%s reached the caller as %s; want an array, empty where nothing is named", member, trimmed)
				}
			}

			var got listEntitiesResult
			decodeStructured(t, res, &got)
			if strings.Join(got.UnreadLinkedDirs, "\n") != strings.Join(row.wantDirs, "\n") || len(got.UnreadLinkedDirs) != len(row.wantDirs) {
				t.Errorf("unread_linked_dirs = %q; want %q", got.UnreadLinkedDirs, row.wantDirs)
			}
			if strings.Join(got.UnreadLinkedFiles, "\n") != strings.Join(row.wantFiles, "\n") || len(got.UnreadLinkedFiles) != len(row.wantFiles) {
				t.Errorf("unread_linked_files = %q; want %q", got.UnreadLinkedFiles, row.wantFiles)
			}
			wantCount := 2
			never := []string{"outside-only-component", "shared-only-component"}
			if row.wantRelease {
				wantCount = 3
				listed := false
				for _, e := range got.Entities {
					if e.Kind == "Release" && e.Name == "outside-only-release" && e.File == "linked-release.yaml" {
						listed = true
					}
				}
				if !listed {
					t.Errorf("the Release the configuration names is not listed at linked-release.yaml: %s", res.StructuredContent)
				}
			} else {
				never = append(never, "outside-only-release")
			}
			if got.Count != wantCount {
				t.Errorf("count = %d; want %d — a document behind a link leading out was counted, or one the listing reads was missed",
					got.Count, wantCount)
			}
			text := res.Content[0].Text
			for _, name := range never {
				if strings.Contains(text, name) || strings.Contains(string(res.StructuredContent), name) {
					t.Errorf("the answer lists %s, which lies behind a link leading out of the workspace", name)
				}
			}
			untold := []string{outside}
			if row.transport == transportHTTP {
				untold = append(untold, root)
			}
			for _, place := range untold {
				if strings.Contains(text, place) || strings.Contains(string(res.StructuredContent), place) {
					t.Errorf("the answer names %s, a place a caller here is not told:\ntext: %s\nstructured: %s",
						place, text, res.StructuredContent)
				}
			}

			var closing string
			if len(row.wantDirs) > 0 {
				closing += "\n\n" + dirsOpening + ", because each leads to a directory outside the workspace and none is " +
					"searched: " + strings.Join(row.wantDirs, ", ")
			}
			if len(row.wantFiles) > 0 {
				closing += "\n\n" + filesOpening + ", because each leads to a file outside the workspace and none is read: " +
					strings.Join(row.wantFiles, ", ")
			}
			if closing == "" {
				if strings.Contains(text, dirsOpening) || strings.Contains(text, filesOpening) {
					t.Errorf("the text names links though the answer names none:\n%s", text)
				}
				return
			}
			if !strings.HasSuffix(text, closing) {
				t.Errorf("the text does not close with the lines naming the links; want it to end with\n  %q\nin:\n%s",
					closing, text)
			}
		})
	}
}

// TestAPathArgumentLeadingOutThroughALinkIsRefusedOverAListener drives tools whose
// path arguments name a file inside the workspace as written that leads out of it
// through a link, through the real tools/call dispatch, and reads back whether the
// call was refused and in what words.
//
// Over a listener a call names its subject inside the one workspace the process
// serves, so such a path is refused, quoting the value as it was sent and naming
// neither the root nor where the link leads; a 'release' the workspace
// configuration names as a Release is reached through the configuration and is
// admitted. On the process's own streams a path is taken as it is named, outside
// the workspace included, so the same call is admitted there: the row pinning that
// is the transport's rule, not a gap.
//
// The first row is the control, a Release written inside the workspace, over a
// listener. Each other row carries one fault, or lifts it. Every insertion-point
// row asks for the document's metadata, a container each document here writes in
// block style, so whether that call is admitted turns on where its file leads and
// on nothing else.
func TestAPathArgumentLeadingOutThroughALinkIsRefusedOverAListener(t *testing.T) {
	machineWide := filepath.Join(t.TempDir(), "machine-wide.yaml")
	writeLinkedDirsFile(t, machineWide, "registries: []\n")
	t.Setenv(workspaceconfig.EnvConfigOverride, machineWide)

	const noConfiguration = "workspace: {}\noffline: true\n"
	rows := []struct {
		name      string
		transport transportMode
		config    string
		tool      string
		args      map[string]any
		// refusal is the sentence the refusal must carry; empty where the call is
		// admitted.
		refusal string
	}{
		{
			name:      "over a listener a Release written inside the workspace is admitted",
			transport: transportHTTP,
			tool:      "list-entities",
			args:      map[string]any{"release": "app-release.yaml"},
		},
		{
			name:      "over a listener a Release reached through a link leading out is refused, quoting the value sent",
			transport: transportHTTP,
			tool:      "list-entities",
			args:      map[string]any{"release": "linked-release.yaml"},
			refusal: `list-entities's 'release' must name a path inside the workspace this server serves, written relative ` +
				`to that workspace's root; "linked-release.yaml" leads outside that root through a link.`,
		},
		{
			name:      "over a listener a Release the configuration names is admitted through its link",
			transport: transportHTTP,
			config:    "workspace:\n  releases:\n    linked-release.yaml: {}\noffline: true\n",
			tool:      "list-entities",
			args:      map[string]any{"release": "linked-release.yaml"},
		},
		{
			name:      "on the process's own streams a path is taken as it is named, a link leading out included",
			transport: transportStdio,
			tool:      "list-entities",
			args:      map[string]any{"release": "linked-release.yaml"},
		},
		{
			name:      "over a listener a document written inside the workspace is admitted",
			transport: transportHTTP,
			tool:      "insertion-point",
			args:      map[string]any{"file": "inside.yaml", "container": []string{"metadata"}},
		},
		{
			name:      "over a listener a document reached through a link leading out is refused",
			transport: transportHTTP,
			tool:      "insertion-point",
			args:      map[string]any{"file": "linked-component.yaml", "container": []string{"metadata"}},
			refusal: `insertion-point's 'file' must name a path inside the workspace this server serves, written relative ` +
				`to that workspace's root; "linked-component.yaml" leads outside that root through a link.`,
		},
		{
			name:      "the configuration naming a file as a Release is no exception for an argument naming anything else",
			transport: transportHTTP,
			config:    "workspace:\n  releases:\n    linked-release.yaml: {}\noffline: true\n",
			tool:      "insertion-point",
			args:      map[string]any{"file": "linked-release.yaml", "container": []string{"metadata"}},
			refusal:   `"linked-release.yaml" leads outside that root through a link.`,
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			config := noConfiguration
			if row.config != "" {
				config = row.config
			}
			writeLinkedDirsFile(t, filepath.Join(outside, "outside-release.yaml"), linksTestRelease)
			writeLinkedDirsFile(t, filepath.Join(outside, "outside-component.yaml"), linksTestComponent("outside-only-component"))
			writeLinkedDirsFile(t, filepath.Join(root, "app-release.yaml"), linksTestRelease)
			writeLinkedDirsFile(t, filepath.Join(root, "inside.yaml"), linksTestComponent("inside-component"))
			linkLinkedDirs(t, filepath.Join(outside, "outside-release.yaml"), filepath.Join(root, "linked-release.yaml"))
			linkLinkedDirs(t, filepath.Join(outside, "outside-component.yaml"), filepath.Join(root, "linked-component.yaml"))
			writeLinkedDirsFile(t, filepath.Join(root, workspaceconfig.ConfigFilename), config)

			s := &server{out: io.Discard, logw: io.Discard, launchRoot: root, transport: row.transport}
			res := callToolOn(t, s, listenerRequest(), row.tool, row.args)
			text := res.Content[0].Text
			if row.refusal == "" {
				if res.IsError {
					t.Fatalf("%s was refused; want it admitted: %s", row.tool, text)
				}
				return
			}
			if !res.IsError {
				t.Fatalf("%s was admitted; want it refused with %q:\n%s", row.tool, row.refusal, text)
			}
			if !strings.Contains(text, row.refusal) {
				t.Errorf("the refusal reads\n  %s\nwant it to carry\n  %s", text, row.refusal)
			}
			for _, place := range []string{outside, root} {
				if strings.Contains(text, place) {
					t.Errorf("the refusal names %s, a place a caller reaching a listener is not told: %s", place, text)
				}
			}
		})
	}
}
