// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// The documents the link tests lay: a Component of their own name, and a Feature
// that declares no applicability, so every label set offers it as a candidate.
func linkTestComponent(name string) string {
	return "apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  name: " + name + "\nspec: {}\n"
}

func linkTestFeature(name string) string {
	return "apiVersion: aggrega.ai/v1beta1\nkind: Feature\nmetadata:\n  name: " + name + "\nspec: {}\n"
}

// linkTestConsumer is the Component whose one socket accepts any Component, so the
// connection candidates it is offered are every Component the workspace reads.
const linkTestConsumer = "apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  name: api\n" +
	"spec:\n  usesSchema:\n    store: {}\n"

// TestTheEntityIndexLeavesOutAndNamesEachLinkLeadingOutOfTheRoot drives
// aggrega/catalogIndex, aggrega/attachCandidates and aggrega/connectionCandidates
// over real workspace roots holding symbolic links, and reads back the entities
// listed, the two link lists, and what each candidate surface offers.
//
// Each row lays one root beside one directory outside it holding a Component, a
// Feature, a Release, and a directory holding a Component of its own — each named
// so that reading it shows up by name. A row that read through a link leading out
// shows up as that entity among the entities listed, as a candidate, or as a link
// missing from the list naming it. The lists are compared in order, because the
// order is the walk's: the ordering row is laid so that the walk's order and a
// plain sort of the same names disagree.
//
// The rows whose configuration names the linked Release — by
// build.authoring_release or by a key of workspace.releases, each naming the link
// or the file it leads to — expect that Release listed at the link's place and the
// link not named, as the Release walk reads it; beside them, a file a Release
// setting names that declares something else, and a second link the configuration
// does not name, stay refused and named.
//
// Every row also pins that both lists reach the wire as arrays, never as null,
// and that the place a link leads to is named nowhere in the answer.
func TestTheEntityIndexLeavesOutAndNamesEachLinkLeadingOutOfTheRoot(t *testing.T) {
	t.Parallel()

	// The workspace's own entities, which every row lists.
	base := []string{"Component api [consumer.yaml]", "Feature inside-feature [inside-feature.yaml]"}
	outsideNames := []string{"outside-only-component", "outside-only-feature", "shared-only-component"}
	// The Release outside, as the index lists it when a link to it is read.
	const linkedRelease = "Release release/outside [linked-release.yaml]"
	linkTheRelease := func(t *testing.T, root, outside string) {
		plantLink(t, filepath.Join(outside, "outside-release.yaml"), filepath.Join(root, "linked-release.yaml"))
	}

	rows := []struct {
		name string
		// config is the workspace configuration's text, given the root and the
		// directory outside it; absent, the configuration names nothing.
		config func(root, outside string) string
		// lay builds the row's links under root; outside is the directory beside it.
		lay func(t *testing.T, root, outside string)
		// held are documents an editor holds, by path relative to root, each with
		// the content it holds.
		held       map[string]string
		wantExtra  []string
		wantDirs   []string
		wantFiles  []string
		wantDouble bool
	}{
		{
			name:      "a workspace holding no link lists its own entities and names none",
			lay:       func(t *testing.T, root, outside string) {},
			wantDirs:  []string{},
			wantFiles: []string{},
		},
		{
			name: "a link to an entity document outside the root is not read, and is named where it sits",
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, filepath.Join(outside, "outside-component.yaml"), filepath.Join(root, "linked-component.yaml"))
			},
			wantDirs:  []string{},
			wantFiles: []string{"linked-component.yaml"},
		},
		{
			name: "a link given relative to where it sits is named when it leads out, and its Feature is not offered",
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, relativeTo(t, root, filepath.Join(outside, "outside-feature.yaml")), filepath.Join(root, "linked-feature.yaml"))
			},
			wantDirs:  []string{},
			wantFiles: []string{"linked-feature.yaml"},
		},
		{
			name: "a link in a subdirectory is named by its path from the root",
			lay: func(t *testing.T, root, outside string) {
				plantDir(t, filepath.Join(root, "envs"))
				plantLink(t, filepath.Join(outside, "outside-component.yaml"), filepath.Join(root, "envs", "linked.yaml"))
			},
			wantDirs:  []string{},
			wantFiles: []string{"envs/linked.yaml"},
		},
		{
			name: "a link to a directory outside the root is named among the directories, and nothing in it is read",
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, filepath.Join(outside, "shared"), filepath.Join(root, "shared"))
			},
			wantDirs:  []string{"shared"},
			wantFiles: []string{},
		},
		{
			name: "a link named like a YAML file that leads to a directory outside is named among the directories only",
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, filepath.Join(outside, "shared"), filepath.Join(root, "shared.yaml"))
			},
			wantDirs:  []string{"shared.yaml"},
			wantFiles: []string{},
		},
		{
			name: "a link to an entity document inside the root is read at the link's place, and its file at its own",
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, filepath.Join(root, "inside-feature.yaml"), filepath.Join(root, "alias.yaml"))
			},
			wantExtra: []string{"Feature inside-feature [alias.yaml]"},
			wantDirs:  []string{},
			wantFiles: []string{},
		},
		{
			name: "a link to a directory inside the root is not named, and what it holds is read where it sits",
			lay: func(t *testing.T, root, outside string) {
				plantFile(t, filepath.Join(root, "real", "real.yaml"), linkTestComponent("real-component"))
				plantLink(t, filepath.Join(root, "real"), filepath.Join(root, "alias"))
			},
			wantExtra: []string{"Component real-component [real/real.yaml]"},
			wantDirs:  []string{},
			wantFiles: []string{},
		},
		{
			name: "a link leading nowhere is neither read nor named",
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, filepath.Join(outside, "missing.yaml"), filepath.Join(root, "gone.yaml"))
			},
			wantDirs:  []string{},
			wantFiles: []string{},
		},
		{
			name: "a link pointing at itself is neither read nor named",
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, filepath.Join(root, "loop.yaml"), filepath.Join(root, "loop.yaml"))
			},
			wantDirs:  []string{},
			wantFiles: []string{},
		},
		{
			name: "a link inside a directory the walk leaves out by its name is never met",
			lay: func(t *testing.T, root, outside string) {
				plantDir(t, filepath.Join(root, "node_modules"))
				plantLink(t, filepath.Join(outside, "outside-component.yaml"), filepath.Join(root, "node_modules", "linked.yaml"))
			},
			wantDirs:  []string{},
			wantFiles: []string{},
		},
		{
			name: "a link inside a directory a .gitignore leaves out is never met",
			lay: func(t *testing.T, root, outside string) {
				plantFile(t, filepath.Join(root, ".gitignore"), "ignored/\n")
				plantDir(t, filepath.Join(root, "ignored"))
				plantLink(t, filepath.Join(outside, "outside-component.yaml"), filepath.Join(root, "ignored", "linked.yaml"))
			},
			wantDirs:  []string{},
			wantFiles: []string{},
		},
		{
			name: "a link to a file a .gitignore leaves out is not named",
			lay: func(t *testing.T, root, outside string) {
				plantFile(t, filepath.Join(root, ".gitignore"), "linked-component.yaml\n")
				plantLink(t, filepath.Join(outside, "outside-component.yaml"), filepath.Join(root, "linked-component.yaml"))
			},
			wantDirs:  []string{},
			wantFiles: []string{},
		},
		{
			name: "a link to a directory outside bearing a name the walk leaves out is not named",
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, filepath.Join(outside, "shared"), filepath.Join(root, "vendor"))
			},
			wantDirs:  []string{},
			wantFiles: []string{},
		},
		{
			name: "build.authoring_release naming the link has the Release behind it read and listed where the link sits",
			config: func(string, string) string {
				return "workspace: {}\nbuild:\n  authoring_release: linked-release.yaml\n"
			},
			lay:       linkTheRelease,
			wantExtra: []string{linkedRelease},
			wantDirs:  []string{},
			wantFiles: []string{},
		},
		{
			name: "build.authoring_release naming the file the link leads to has the link read and listed",
			config: func(_, outside string) string {
				return fmt.Sprintf("workspace: {}\nbuild:\n  authoring_release: %q\n", filepath.Join(outside, "outside-release.yaml"))
			},
			lay:       linkTheRelease,
			wantExtra: []string{linkedRelease},
			wantDirs:  []string{},
			wantFiles: []string{},
		},
		{
			name: "a workspace.releases key naming the link has the Release behind it read and listed",
			config: func(string, string) string {
				return "workspace:\n  releases:\n    linked-release.yaml: {}\n"
			},
			lay:       linkTheRelease,
			wantExtra: []string{linkedRelease},
			wantDirs:  []string{},
			wantFiles: []string{},
		},
		{
			name: "a workspace.releases key naming the file the link leads to has the link read and listed",
			config: func(_, outside string) string {
				return fmt.Sprintf("workspace:\n  releases:\n    %q: {}\n", filepath.Join(outside, "outside-release.yaml"))
			},
			lay:       linkTheRelease,
			wantExtra: []string{linkedRelease},
			wantDirs:  []string{},
			wantFiles: []string{},
		},
		{
			name: "the configuration naming one linked Release leaves another link leading out unread",
			config: func(string, string) string {
				return "workspace: {}\nbuild:\n  authoring_release: linked-release.yaml\n"
			},
			lay: func(t *testing.T, root, outside string) {
				linkTheRelease(t, root, outside)
				plantLink(t, filepath.Join(outside, "outside-component.yaml"), filepath.Join(root, "linked-component.yaml"))
			},
			wantExtra: []string{linkedRelease},
			wantDirs:  []string{},
			wantFiles: []string{"linked-component.yaml"},
		},
		{
			name: "a file a Release setting names that declares no Release is not read, and is named",
			config: func(string, string) string {
				return "workspace: {}\nbuild:\n  authoring_release: linked-component.yaml\n"
			},
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, filepath.Join(outside, "outside-component.yaml"), filepath.Join(root, "linked-component.yaml"))
			},
			wantDirs:  []string{},
			wantFiles: []string{"linked-component.yaml"},
		},
		{
			name: "a Release the configuration names, held by the editor at its link, is read from the file it leads to",
			config: func(string, string) string {
				return "workspace:\n  releases:\n    linked-release.yaml: {}\n"
			},
			lay:       linkTheRelease,
			held:      map[string]string{"linked-release.yaml": releaseYAML("release/held-copy")},
			wantExtra: []string{linkedRelease},
			wantDirs:  []string{},
			wantFiles: []string{},
		},
		{
			name: "several links are named in the order the walk meets them",
			lay: func(t *testing.T, root, outside string) {
				plantDir(t, filepath.Join(root, "a"))
				target := filepath.Join(outside, "outside-component.yaml")
				plantLink(t, target, filepath.Join(root, "b.yaml"))
				plantLink(t, target, filepath.Join(root, "a-c.yaml"))
				plantLink(t, target, filepath.Join(root, "a", "z.yaml"))
			},
			wantDirs:  []string{},
			wantFiles: []string{"a/z.yaml", "a-c.yaml", "b.yaml"},
		},
		{
			name: "a document the editor holds at a link leading out is not offered either",
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, filepath.Join(outside, "outside-component.yaml"), filepath.Join(root, "linked-component.yaml"))
			},
			held:      map[string]string{"linked-component.yaml": linkTestComponent("outside-only-component")},
			wantDirs:  []string{},
			wantFiles: []string{"linked-component.yaml"},
		},
		{
			name: "a document the editor holds inside a linked directory outside is not offered either",
			lay: func(t *testing.T, root, outside string) {
				plantLink(t, filepath.Join(outside, "shared"), filepath.Join(root, "shared"))
			},
			held:      map[string]string{"shared/shared-component.yaml": linkTestComponent("shared-only-component")},
			wantDirs:  []string{"shared"},
			wantFiles: []string{},
		},
		{
			name:      "a document the editor holds at a place inside the root and on no link is offered as before",
			lay:       func(t *testing.T, root, outside string) {},
			held:      map[string]string{"held.yaml": linkTestComponent("held-component")},
			wantExtra: []string{"Component held-component [held.yaml]"},
			wantDirs:  []string{},
			wantFiles: []string{},
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			outside := t.TempDir()
			plantFile(t, filepath.Join(outside, "outside-component.yaml"), linkTestComponent("outside-only-component"))
			plantFile(t, filepath.Join(outside, "outside-feature.yaml"), linkTestFeature("outside-only-feature"))
			plantFile(t, filepath.Join(outside, "shared", "shared-component.yaml"), linkTestComponent("shared-only-component"))
			writeRelease(t, outside, "outside-release.yaml", "release/outside")
			plantFile(t, filepath.Join(root, "consumer.yaml"), linkTestConsumer)
			plantFile(t, filepath.Join(root, "inside-feature.yaml"), linkTestFeature("inside-feature"))
			row.lay(t, root, outside)
			config := "workspace: {}\n"
			if row.config != nil {
				config = row.config(root, outside)
			}
			srv := linkedFilesServer(t, root, config)
			for rel, content := range row.held {
				var doc yaml.Node
				if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
					t.Fatalf("parse the held document %s: %v", rel, err)
				}
				srv.parseCache.Put(filepath.Join(root, filepath.FromSlash(rel)), &doc, []byte(content))
			}

			resp := callDaemonMethod(t, srv, "aggrega/catalogIndex", map[string]any{})
			if resp.Error != nil {
				t.Fatalf("catalogIndex refused: %+v", resp.Error)
			}
			requireArrayMembers(t, resp.Result, "unreadLinkedDirs", "unreadLinkedFiles")
			if bytes.Contains(resp.Result, []byte(outside)) {
				t.Errorf("the answer names %s, the place a link leads to: %s", outside, resp.Result)
			}
			var got catalogIndexResult
			if err := json.Unmarshal(resp.Result, &got); err != nil {
				t.Fatalf("unmarshal result: %v", err)
			}
			want := append(append([]string{}, base...), row.wantExtra...)
			sort.Strings(want)
			if listed := workspaceEntityRows(got.Entities); strings.Join(listed, "\n") != strings.Join(want, "\n") {
				t.Errorf("entities listed:\n  %s\nwant:\n  %s — a link leading out was read, or one inside was not",
					strings.Join(listed, "\n  "), strings.Join(want, "\n  "))
			}
			if !sameInOrder(got.UnreadLinkedFiles, row.wantFiles) {
				t.Errorf("unreadLinkedFiles = %q; want %q", got.UnreadLinkedFiles, row.wantFiles)
			}
			if !sameInOrder(got.UnreadLinkedDirs, row.wantDirs) {
				t.Errorf("unreadLinkedDirs = %q; want %q", got.UnreadLinkedDirs, row.wantDirs)
			}

			// The two candidate surfaces read the same traversal: neither offers
			// what lies outside, and each still offers what lies inside.
			attach := callDaemonMethod(t, srv, "aggrega/attachCandidates", map[string]any{"labels": map[string]string{"tier": "edge"}})
			if attach.Error != nil {
				t.Fatalf("attachCandidates refused: %+v", attach.Error)
			}
			requireNamesOffered(t, "attachCandidates", attach.Result, []string{"inside-feature"}, outsideNames)
			connect := callDaemonMethod(t, srv, "aggrega/connectionCandidates", map[string]any{"node": "api", "socket": "store"})
			if connect.Error != nil {
				t.Fatalf("connectionCandidates refused: %+v", connect.Error)
			}
			requireNamesOffered(t, "connectionCandidates", connect.Result, []string{"api"}, outsideNames)
		})
	}
}

// TestTheEntityIndexInAReleasesTermsNamesOnlyTheLinksThatReleaseReads drives
// aggrega/catalogIndex over one root whose configuration gives each of two
// Releases a catalog directory of its own, each holding a link leading out of
// the root, and reads back unreadLinkedFiles in the workspace's terms and in each
// Release's.
//
// The first row is the control: the workspace's own answer names both links. An
// answer in one Release's terms holds no document under the other Release's
// directory, so it names only the link under its own.
func TestTheEntityIndexInAReleasesTermsNamesOnlyTheLinksThatReleaseReads(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir()
	plantFile(t, filepath.Join(outside, "outside-component.yaml"), linkTestComponent("outside-only-component"))
	writeRelease(t, root, "release-a.yaml", "release/a")
	writeRelease(t, root, "release-b.yaml", "release/b")
	plantDir(t, filepath.Join(root, "catalog-a"))
	plantDir(t, filepath.Join(root, "catalog-b"))
	plantLink(t, filepath.Join(outside, "outside-component.yaml"), filepath.Join(root, "catalog-a", "linked-a.yaml"))
	plantLink(t, filepath.Join(outside, "outside-component.yaml"), filepath.Join(root, "catalog-b", "linked-b.yaml"))
	srv := linkedFilesServer(t, root, "workspace:\n  releases:\n    release-a.yaml:\n      catalog_dirs:\n        - catalog-a\n"+
		"    release-b.yaml:\n      catalog_dirs:\n        - catalog-b\n")

	for _, row := range []struct {
		name    string
		release string
		want    []string
	}{
		{name: "the workspace's own answer names every link", want: []string{"catalog-a/linked-a.yaml", "catalog-b/linked-b.yaml"}},
		{name: "release-a.yaml's answer names the link under its own directory", release: "release-a.yaml", want: []string{"catalog-a/linked-a.yaml"}},
		{name: "release-b.yaml's answer names the link under its own directory", release: "release-b.yaml", want: []string{"catalog-b/linked-b.yaml"}},
	} {
		t.Run(row.name, func(t *testing.T) {
			params := map[string]any{}
			if row.release != "" {
				params["release"] = row.release
			}
			var got catalogIndexResult
			decodeDaemonResult(t, callDaemonMethod(t, srv, "aggrega/catalogIndex", params), &got)
			if !sameInOrder(got.UnreadLinkedFiles, row.want) {
				t.Errorf("unreadLinkedFiles = %q; want %q", got.UnreadLinkedFiles, row.want)
			}
			for _, e := range got.Entities {
				if e.Name == "outside-only-component" {
					t.Errorf("the answer lists %s from %q, a document behind a link leading out", e.Name, e.File)
				}
			}
		})
	}
}

// TestARequestNamingAPathThroughALinkLeadingOutIsRefused drives requests whose
// path parameters name a file inside the root as written that leads out of it
// through a link, over one real root, and reads back whether each was refused by
// the parameter check and why.
//
// Each row sends one request with one path at fault. A request admitted past the
// parameter check reaches its next stage, which is how admission is read: the
// graph request's lockfile gate, and the publication's refusal to publish to no
// destination (the configuration names none, so nothing is ever sent anywhere).
// The first row is the control, a Release written inside the root.
func TestARequestNamingAPathThroughALinkLeadingOutIsRefused(t *testing.T) {
	t.Parallel()

	const noDestination = "workspace: {}\ndefault_registry: \"\"\n"
	const namingTheLinkedRelease = "default_registry: \"\"\nworkspace:\n  releases:\n    linked-release.yaml: {}\n"
	const admittedToTheGraph = "admitted, and refused by the lockfile gate"
	const admittedToPublish = "no destination registry"

	rows := []struct {
		name   string
		config func(outside string) string
		method string
		params map[string]any
		// want is admittedToTheGraph, or a fragment the refusal must carry.
		want string
	}{
		{
			name:   "a Release written inside the root is admitted",
			method: "aggrega/irGraph",
			params: map[string]any{"release": "plain-release.yaml"},
			want:   admittedToTheGraph,
		},
		{
			name:   "a Release reached through a link to a file outside is refused, naming the path as sent",
			method: "aggrega/irGraph",
			params: map[string]any{"release": "linked-release.yaml"},
			want:   `release must be a path relative to the workspace root, naming a Release inside it; "linked-release.yaml" leads outside the workspace root through a link`,
		},
		{
			name:   "a Release inside a linked directory outside is refused",
			method: "aggrega/irGraph",
			params: map[string]any{"release": "shared/shared-release.yaml"},
			want:   `"shared/shared-release.yaml" leads outside the workspace root through a link`,
		},
		{
			name:   "a Release reached through a link to a file inside the root is admitted",
			method: "aggrega/irGraph",
			params: map[string]any{"release": "alias-release.yaml"},
			want:   admittedToTheGraph,
		},
		{
			name: "a Release the configuration names as a key of workspace.releases is admitted through its link",
			config: func(string) string {
				return namingTheLinkedRelease
			},
			method: "aggrega/irGraph",
			params: map[string]any{"release": "linked-release.yaml"},
			want:   admittedToTheGraph,
		},
		{
			name: "a Release build.authoring_release names, by the file the link leads to, is admitted through its link",
			config: func(outside string) string {
				return noDestination + "build:\n  authoring_release: " + filepath.Join(outside, "outside-release.yaml") + "\n"
			},
			method: "aggrega/irGraph",
			params: map[string]any{"release": "linked-release.yaml"},
			want:   admittedToTheGraph,
		},
		{
			name:   "a Release climbing out of the root is refused as before",
			method: "aggrega/irGraph",
			params: map[string]any{"release": "../elsewhere/release.yaml"},
			want:   `"../elsewhere/release.yaml" resolves outside the workspace root`,
		},
		{
			name:   "a request concerning no single Release refuses a Release reached through a link leading out",
			method: "aggrega/catalogIndex",
			params: map[string]any{"release": "linked-release.yaml"},
			want:   `"linked-release.yaml" leads outside the workspace root through a link`,
		},
		{
			name:   "the conventions check refuses an entity reached through a link leading out",
			method: "aggrega/lint",
			params: map[string]any{"entity": "linked-component.yaml"},
			want:   `entity must be a path relative to the workspace root, naming a catalog entity inside it; "linked-component.yaml" leads outside`,
		},
		{
			name:   "a publication of an entity written inside the root is admitted",
			method: "aggrega/publishEntity",
			params: map[string]any{"entity": "1.0.0/entity.yaml"},
			want:   admittedToPublish,
		},
		{
			name:   "a publication refuses an entity reached through a link leading out",
			method: "aggrega/publishEntity",
			params: map[string]any{"entity": "linked-component.yaml"},
			want:   `entity must be a path relative to the workspace root, naming an entity document inside it; "linked-component.yaml" leads outside`,
		},
		{
			name:   "a publication refuses a plugin executable reached through a link leading out",
			method: "aggrega/publishEntity",
			params: map[string]any{"entity": "1.0.0/entity.yaml", "plugin": "linked-guest.wasm"},
			want:   `plugin must be a path relative to the workspace root, naming a plugin executable inside it; "linked-guest.wasm" leads outside`,
		},
		{
			name:   "a data artifact publication refuses a file reached through a link leading out",
			method: "aggrega/publishArtifact",
			params: map[string]any{"file": "linked-bundle.txt", "name": "handbook", "tag": "2.0.0"},
			want:   `file must be a path relative to the workspace root, naming the artifact file inside it; "linked-bundle.txt" leads outside`,
		},
		{
			name:   "a withdrawal refuses a batch file reached through a link leading out",
			method: "aggrega/deprecateArtifact",
			params: map[string]any{"from": "linked-batch.yaml"},
			want:   `from must be a path relative to the workspace root, naming a batch file inside it; "linked-batch.yaml" leads outside`,
		},
		{
			name: "the configuration naming a file as a Release is no exception for a parameter naming anything else",
			config: func(string) string {
				return namingTheLinkedRelease
			},
			method: "aggrega/publishEntity",
			params: map[string]any{"entity": "linked-release.yaml"},
			want:   `"linked-release.yaml" leads outside the workspace root through a link`,
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			outside := t.TempDir()
			writeRelease(t, outside, "outside-release.yaml", "release/outside")
			writeRelease(t, outside, filepath.Join("shared", "shared-release.yaml"), "release/shared")
			plantFile(t, filepath.Join(outside, "outside-component.yaml"), linkTestComponent("outside-only-component"))
			plantFile(t, filepath.Join(outside, "guest.wasm"), "\x00asm")
			plantFile(t, filepath.Join(outside, "bundle.txt"), "bytes\n")
			plantFile(t, filepath.Join(outside, "batch.yaml"), "not: [a batch\n")
			writeRelease(t, root, "plain-release.yaml", "release/plain")
			plantFile(t, filepath.Join(root, "1.0.0", "entity.yaml"), publishComponentBody)
			plantLink(t, filepath.Join(outside, "outside-release.yaml"), filepath.Join(root, "linked-release.yaml"))
			plantLink(t, filepath.Join(outside, "shared"), filepath.Join(root, "shared"))
			plantLink(t, filepath.Join(root, "plain-release.yaml"), filepath.Join(root, "alias-release.yaml"))
			plantLink(t, filepath.Join(outside, "outside-component.yaml"), filepath.Join(root, "linked-component.yaml"))
			plantLink(t, filepath.Join(outside, "guest.wasm"), filepath.Join(root, "linked-guest.wasm"))
			plantLink(t, filepath.Join(outside, "bundle.txt"), filepath.Join(root, "linked-bundle.txt"))
			plantLink(t, filepath.Join(outside, "batch.yaml"), filepath.Join(root, "linked-batch.yaml"))
			config := noDestination
			if row.config != nil {
				config = row.config(outside)
			}
			srv := linkedFilesServer(t, root, config)

			var in, out bytes.Buffer
			in.Write(framedRequest(1, row.method, row.params))
			resp, _ := runOneRequestPastPushes(t, srv, &in, &out)
			if row.want == admittedToTheGraph {
				if resp.Error == nil || resp.Error.Code != errLockfileAbsent {
					t.Fatalf("want the request admitted and refused by the lockfile gate; got %+v (result %s)", resp.Error, resp.Result)
				}
				return
			}
			if resp.Error == nil {
				t.Fatalf("want a refusal carrying %q; got result %s", row.want, resp.Result)
			}
			if !strings.Contains(resp.Error.Message, row.want) {
				t.Errorf("the refusal reads %q; want it to carry %q", resp.Error.Message, row.want)
			}
			if row.want != admittedToPublish && resp.Error.Code != jsonRPCInvalidParams {
				t.Errorf("Code = %d; want %d (invalid params) — a path a request may not name is the request's fault", resp.Error.Code, jsonRPCInvalidParams)
			}
			if strings.Contains(resp.Error.Message, outside) {
				t.Errorf("the refusal names %s, the place the link leads to: %q", outside, resp.Error.Message)
			}
		})
	}
}

// TestTheMarkerIsNotLoadedWhereItsReleaseLiesOutsideTheRoot drives
// aggrega/activeRelease over real roots whose .aggrega/active-release marker names
// a Release in each way one can lie outside the root, and reads back the Release
// the cascade settled on and, where it settled on none, the sentence its reason
// and its trace each give the marker.
//
// The first row is the control, a marker naming a Release inside a root holding
// two, so only the marker can select it. The rows the marker may not reach lay no
// other Release, so the cascade exhausts and the trace is there to read — except
// the row pinning that the cascade then falls through to the workspace's one
// Release, as it does for a marker whose target cannot be loaded.
func TestTheMarkerIsNotLoadedWhereItsReleaseLiesOutsideTheRoot(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name   string
		config string
		// lay builds the row's Releases and links under root, and returns the
		// marker's text.
		lay        func(t *testing.T, root, outside string) string
		wantActive func(root, outside string) string
		// refused says the marker's target is reported as lying outside.
		refused bool
	}{
		{
			name: "a marker naming a Release inside the root is loaded",
			lay: func(t *testing.T, root, outside string) string {
				writeRelease(t, root, "app-release.yaml", "release/app")
				writeRelease(t, root, "other-release.yaml", "release/other")
				return "other-release.yaml"
			},
			wantActive: inRoot("other-release.yaml"),
		},
		{
			name: "a marker naming a link to a Release outside is not loaded, and the trace says why",
			lay: func(t *testing.T, root, outside string) string {
				plantLink(t, filepath.Join(outside, "outside-release.yaml"), filepath.Join(root, "linked-release.yaml"))
				return "linked-release.yaml"
			},
			refused: true,
		},
		{
			name: "a marker naming a Release outside by its absolute path is not loaded",
			lay: func(t *testing.T, root, outside string) string {
				return filepath.Join(outside, "outside-release.yaml")
			},
			refused: true,
		},
		{
			name: "a marker climbing out of the root is not loaded",
			lay: func(t *testing.T, root, outside string) string {
				return relativeTo(t, root, filepath.Join(outside, "outside-release.yaml"))
			},
			refused: true,
		},
		{
			name: "a marker naming a Release inside a linked directory outside is not loaded",
			lay: func(t *testing.T, root, outside string) string {
				plantLink(t, filepath.Join(outside, "shared"), filepath.Join(root, "shared"))
				return "shared/shared-release.yaml"
			},
			refused: true,
		},
		{
			name: "a marker not loaded falls through to the workspace's one Release",
			lay: func(t *testing.T, root, outside string) string {
				writeRelease(t, root, "app-release.yaml", "release/app")
				plantLink(t, filepath.Join(outside, "outside-release.yaml"), filepath.Join(root, "linked-release.yaml"))
				return "linked-release.yaml"
			},
			wantActive: inRoot("app-release.yaml"),
		},
		{
			name:   "a marker naming a link the configuration names as a Release is loaded",
			config: "workspace:\n  releases:\n    linked-release.yaml: {}\n",
			lay: func(t *testing.T, root, outside string) string {
				writeRelease(t, root, "app-release.yaml", "release/app")
				plantLink(t, filepath.Join(outside, "outside-release.yaml"), filepath.Join(root, "linked-release.yaml"))
				return "linked-release.yaml"
			},
			wantActive: inRoot("linked-release.yaml"),
		},
		{
			name: "a marker naming a link to a Release inside the root is loaded at the link's place",
			lay: func(t *testing.T, root, outside string) string {
				writeRelease(t, root, "app-release.yaml", "release/app")
				writeRelease(t, root, filepath.Join("real", "staging.yaml"), "release/staging")
				plantLink(t, filepath.Join(root, "real", "staging.yaml"), filepath.Join(root, "alias.yaml"))
				return "alias.yaml"
			},
			wantActive: inRoot("alias.yaml"),
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			outside := t.TempDir()
			writeRelease(t, outside, "outside-release.yaml", "release/outside")
			writeRelease(t, outside, filepath.Join("shared", "shared-release.yaml"), "release/shared")
			marker := row.lay(t, root, outside)
			plantFile(t, filepath.Join(root, ".aggrega", "active-release"), marker+"\n")
			config := "workspace: {}\n"
			if row.config != "" {
				config = row.config
			}
			srv := linkedFilesServer(t, root, config)

			got := activeReleaseAnswer(t, srv)
			want := ""
			if row.wantActive != nil {
				want = row.wantActive(root, outside)
			}
			if got.Release != want {
				t.Errorf("the cascade settled on %q; want %q", got.Release, want)
			}
			if !row.refused {
				return
			}
			trace := fmt.Sprintf("step 2 (.aggrega/active-release marker): target %q lies outside the workspace root and "+
				"the workspace configuration does not name it as a Release, so it was not loaded", marker)
			if !strings.Contains(got.Trace, trace) {
				t.Errorf("the trace does not say why the marker was not loaded; want it to carry\n  %s\nin:\n%s", trace, got.Trace)
			}
			reason := fmt.Sprintf("The .aggrega/active-release marker names %q, which lies outside the workspace and is not "+
				"a Release the workspace configuration names, so it is not read.", marker)
			if !strings.Contains(got.Reason, reason) {
				t.Errorf("the reason does not say why the marker was not read; want it to carry\n  %s\nin:\n  %s", reason, got.Reason)
			}
		})
	}
}

// relativeTo spells target relative to dir.
func relativeTo(t *testing.T, dir, target string) string {
	t.Helper()
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		t.Fatal(err)
	}
	return rel
}

// workspaceEntityRows renders the workspace documents an index lists as sorted
// "Kind name [file]" rows, so two answers compare as sets.
func workspaceEntityRows(entities []CatalogEntity) []string {
	rows := []string{}
	for _, e := range entities {
		if e.Origin != EntityOriginWorkspace {
			continue
		}
		rows = append(rows, fmt.Sprintf("%s %s [%s]", e.Kind, e.Name, e.File))
	}
	sort.Strings(rows)
	return rows
}

// requireArrayMembers fails unless each named member of the raw result is present
// and an array.
func requireArrayMembers(t *testing.T, result json.RawMessage, names ...string) {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(result, &raw); err != nil {
		t.Fatalf("unmarshal raw result: %v (raw: %s)", err, result)
	}
	for _, name := range names {
		encoded, present := raw[name]
		if !present {
			t.Fatalf("%s is absent from the answer; want it present, empty where nothing is named (raw: %s)", name, result)
		}
		if trimmed := strings.TrimSpace(string(encoded)); !strings.HasPrefix(trimmed, "[") {
			t.Fatalf("%s reached the wire as %s; want an array, empty where nothing is named", name, trimmed)
		}
	}
}

// requireNamesOffered fails unless every name in want appears in the answer and
// none in never does, reading the answer's bytes for each name as a JSON string.
func requireNamesOffered(t *testing.T, method string, result json.RawMessage, want, never []string) {
	t.Helper()
	for _, name := range want {
		if !bytes.Contains(result, []byte(`"`+name+`"`)) {
			t.Errorf("%s does not offer %s, which lies inside the root: %s", method, name, result)
		}
	}
	for _, name := range never {
		if bytes.Contains(result, []byte(`"`+name+`"`)) {
			t.Errorf("%s offers %s, which lies behind a link leading out of the root: %s", method, name, result)
		}
	}
}
