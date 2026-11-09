// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/cacheops"
	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// heldConfigurationLayout is one workspace whose configuration file — its own, or
// the CI-owned overlay beside it — is, row by row, a file inside it or a link to a
// file outside it. The configuration lists a catalog directory outside the
// workspace holding one Feature, so a listing that read the configuration lists
// that Feature and one that read none does not.
type heldConfigurationLayout struct {
	root, outside, held string
}

func layHeldConfiguration(t *testing.T, linkRoot, linkCI, ignored bool) heldConfigurationLayout {
	t.Helper()
	l := heldConfigurationLayout{root: t.TempDir(), outside: t.TempDir()}
	if ignored {
		// The walk reading the workspace then never meets the held file, so a
		// listing names it only because the held view names every held file itself.
		writeLinkedDirsFile(t, filepath.Join(l.root, ".gitignore"), workspaceconfig.ConfigFilename+"\n"+
			workspaceconfig.AppendCIConfigFilename+"\n")
	}
	writeLinkedDirsFile(t, filepath.Join(l.root, "release.yaml"),
		"apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: only\n  version: \"1.0.0\"\n")
	writeLinkedDirsFile(t, filepath.Join(l.root, "aggregate.yaml"),
		"apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\nmetadata:\n  name: only\nspec:\n  components: {}\n")
	writeLinkedDirsFile(t, filepath.Join(l.outside, "cat", "feature.yaml"),
		"apiVersion: aggrega.ai/v1beta1\nkind: Feature\nmetadata:\n  name: local/listed-outside\n  version: 1.0.0\n"+
			"spec:\n  appliesTo: \"component.aggrega.ai/category=datastore\"\n")
	config := "workspace:\n  catalog_dirs:\n    - " + filepath.Join(l.outside, "cat") + "\noffline: true\n"
	configPath := filepath.Join(l.root, workspaceconfig.ConfigFilename)
	switch {
	case linkRoot:
		writeLinkedDirsFile(t, filepath.Join(l.outside, "config.yaml"), config)
		linkLinkedDirs(t, filepath.Join(l.outside, "config.yaml"), configPath)
		l.held = workspaceconfig.ConfigFilename
	case linkCI:
		writeLinkedDirsFile(t, configPath, config)
		writeLinkedDirsFile(t, filepath.Join(l.outside, "ci.yaml"), "catalog_dirs_append: []\n")
		linkLinkedDirs(t, filepath.Join(l.outside, "ci.yaml"), filepath.Join(l.root, workspaceconfig.AppendCIConfigFilename))
		l.held = workspaceconfig.AppendCIConfigFilename
	default:
		writeLinkedDirsFile(t, configPath, config)
	}
	return l
}

// heldConfigurationServer is a server over the process's own streams started over
// root, whose global configuration layer is an empty file of the test's own and
// whose cache inventory finds no machine-wide cache, so nothing on the machine
// running the test is read.
func heldConfigurationServer(t *testing.T, root string) *server {
	t.Helper()
	global := filepath.Join(t.TempDir(), "machine-wide.yaml")
	writeLinkedDirsFile(t, global, "registries: []\n")
	return &server{out: io.Discard, logw: io.Discard, launchRoot: root, transport: transportStdio,
		flags: workspaceconfig.FlagOverlay{ConfigPath: global},
		cacheLocations: func(workspaceRoot, cacheDir string) cacheops.Locations {
			return cacheops.Locations{WorkspaceRoot: workspaceRoot, CacheDir: cacheDir}
		}}
}

// TestOnlyTheListingToolsResolveAHeldConfiguration holds the assistant server's
// one resolution for a listing to the language server's rule: over a workspace
// whose configuration is held, the held view is taken for exactly the operations
// the operation set declares as listings, and every other operation the tool
// protocol carries — read from its own table, so a tool added tomorrow is held to
// the rule with no edit here — is refused with the hold.
func TestOnlyTheListingToolsResolveAHeldConfiguration(t *testing.T) {
	t.Parallel()
	l := layHeldConfiguration(t, true, false, false)
	s := heldConfigurationServer(t, l.root)

	operations := make([]string, 0, len(curatedToolNames))
	for op := range curatedToolNames {
		operations = append(operations, op)
	}
	sort.Strings(operations)
	held := 0
	for _, op := range operations {
		tw, err := s.resolveListingWorkspace(op, "", nil)
		if opsurface.AnswersOverHeldConfiguration(op) {
			held++
			if err != nil || tw == nil || tw.held == nil {
				t.Errorf("%s lists what a workspace holds and was not given the held view: %v", op, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s is decided by the configuration and was given a workspace over the held one", op)
			continue
		}
		if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
			t.Errorf("%s was refused, but not with the hold: %v", op, err)
		}
	}
	if held != 2 {
		t.Errorf("%d operation(s) the tool protocol carries took the held view; want the two listings", held)
	}
}

// TestTheListingToolsNameAHeldConfigurationAndEveryOtherToolRefusesIt drives the
// real tools/call dispatch over the process's own streams: list-entities and
// list-releases answer, read with nothing configured — the configured catalog
// directory's Feature absent — and name the held file in unread_linked_files and
// in their text; a listing in a Release's terms, or with catalog directories laid
// on, and every other tool driven here, are refused with the hold naming the link,
// each under the label every tool gives a workspace it could not resolve — never
// under one saying the operation ran. The cache inventory is among them: where a
// workspace's cache sits is what its configuration decides, so an inventory of the
// machine-wide caches alone is a configuration-decided answer on nothing configured.
// The control row is the same workspace with its configuration inside: the
// listings name nothing and carry the Feature, and nothing is refused for a link.
//
// A call whose arguments are malformed is answered about its arguments, held
// workspace or not: every tool checks the shape of its arguments before it
// resolves anything, so that answer runs nothing and is the same in every
// workspace. The argument-less lint row pins that order.
func TestTheListingToolsNameAHeldConfigurationAndEveryOtherToolRefusesIt(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name                      string
		linkRoot, linkCI, ignored bool
	}{
		{name: "control: the configuration inside the workspace"},
		{name: "the workspace's own configuration file a link leading out", linkRoot: true},
		{name: "the CI-owned overlay a link leading out", linkCI: true},
		{name: "a held file the .gitignore leaves out is still named", linkRoot: true, ignored: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			l := layHeldConfiguration(t, row.linkRoot, row.linkCI, row.ignored)
			s := heldConfigurationServer(t, l.root)

			entities := callToolOn(t, s, listenerRequest(), "list-entities", map[string]any{"response_format": "detailed"})
			if entities.IsError {
				t.Fatalf("list-entities refused: %s", entities.Content[0].Text)
			}
			var listed listEntitiesResult
			decodeStructured(t, entities, &listed)
			releases := callToolOn(t, s, listenerRequest(), "list-releases", map[string]any{})
			if releases.IsError {
				t.Fatalf("list-releases refused: %s", releases.Content[0].Text)
			}
			var releaseList listReleasesResult
			decodeStructured(t, releases, &releaseList)

			outsideFeature := false
			for _, e := range listed.Entities {
				if e.Name == "local/listed-outside" {
					outsideFeature = true
				}
			}
			if outsideFeature != (l.held == "") {
				t.Errorf("list-entities lists the configured catalog directory's Feature = %v; want %v — a held "+
					"listing reads no configuration, the control reads all of it", outsideFeature, l.held == "")
			}
			for tool, named := range map[string][]string{
				"list-entities": listed.UnreadLinkedFiles,
				"list-releases": releaseList.UnreadLinkedFiles,
			} {
				if l.held == "" && len(named) != 0 {
					t.Errorf("control, %s names %q as not read; want nothing", tool, named)
				}
				if l.held != "" && !slices.Contains(named, l.held) {
					t.Errorf("%s names %q as not read; want the held file %q among them", tool, named, l.held)
				}
			}
			if l.held != "" && !strings.Contains(entities.Content[0].Text, l.held) {
				t.Errorf("list-entities' text does not name the held file: %s", entities.Content[0].Text)
			}

			link := "the link " + filepath.Join(l.root, l.held) + " leads to"
			for _, c := range []struct {
				tool string
				args map[string]any
			}{
				{"list-entities", map[string]any{"release": "release.yaml"}},
				{"list-entities", map[string]any{"catalog_dirs": []string{filepath.Join(l.outside, "cat")}}},
				{"validate", map[string]any{"release": "release.yaml"}},
				{"build", map[string]any{"release": "release.yaml"}},
				{"describe", map[string]any{"release": "release.yaml"}},
				{"config-show", map[string]any{}},
				{"lint", map[string]any{"release": "release.yaml"}},
				{"cache-status", map[string]any{}},
			} {
				res := callToolOn(t, s, listenerRequest(), c.tool, c.args)
				refusedForTheHold := res.IsError && strings.Contains(res.Content[0].Text, link)
				if l.held == "" {
					if strings.Contains(res.Content[0].Text, "leads through a link out of the directory") {
						t.Errorf("control, %s %v was refused for a link: %s", c.tool, c.args, res.Content[0].Text)
					}
					continue
				}
				if !refusedForTheHold {
					t.Errorf("%s %v was not refused with the hold naming the link (isError %v): %s", c.tool, c.args,
						res.IsError, res.Content[0].Text)
					continue
				}
				if !strings.HasPrefix(res.Content[0].Text, "Could not resolve the workspace") {
					t.Errorf("%s %v refused the held workspace under a label of its own rather than as a workspace "+
						"that could not be resolved: %s", c.tool, c.args, res.Content[0].Text)
				}
			}
			if malformed := callToolOn(t, s, listenerRequest(), "lint", map[string]any{}); !malformed.IsError ||
				strings.Contains(malformed.Content[0].Text, "leads through a link out of the directory") {
				t.Errorf("an argument-less lint was not answered about its arguments (isError %v): %s",
					malformed.IsError, malformed.Content[0].Text)
			}
			if l.held != "" {
				if _, err := os.Lstat(filepath.Join(l.root, ".aggrega")); !os.IsNotExist(err) {
					t.Errorf("a held workspace had its state directory created (lstat: %v)", err)
				}
			}
		})
	}
}
