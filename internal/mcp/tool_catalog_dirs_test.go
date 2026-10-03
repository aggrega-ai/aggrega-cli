// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The two tools whose command-line twin loads the whole catalog answer a catalog
// directory the workspace configuration lists as that twin answers it: `build` as
// `aggrega build` does, and `describe` looking a name up as `aggrega inspect` does.
// A directory that does not exist is refused with the load's own refusal before
// anything is resolved or written — the lockfile a filling build writes included —
// and the same list with the directory there answers exactly what the fixture
// answers without it.
//
// The fixture is the hermetic end-to-end workspace, copied and configured offline,
// carrying no lockfile. A row writes one configuration listing the directory and
// changes nothing else, so a refusing row has one fault — a call refused for
// anything else fails its row rather than passing it. A row comparing against the
// fixture's own list makes that call first, in the same workspace, so the two
// answers name the same paths.

// catalogDirsConfig is a workspace configuration listing the fixture's own catalog
// directory and, where extra is set, extra beside it: for every Release when
// forRelease is empty, or under that Release's own entry only.
func catalogDirsConfig(extra, forRelease string) string {
	switch {
	case extra == "":
		return "workspace:\n  catalog_dirs:\n    - ./catalog\noffline: true\n"
	case forRelease == "":
		return "workspace:\n  catalog_dirs:\n    - ./catalog\n    - ./" + extra + "\noffline: true\n"
	default:
		return "workspace:\n  catalog_dirs:\n    - ./catalog\n  releases:\n    " + forRelease +
			":\n      catalog_dirs:\n        - ./catalog\n        - ./" + extra + "\noffline: true\n"
	}
}

// writeCatalogDirsConfig writes config as the workspace configuration at root.
func writeCatalogDirsConfig(t *testing.T, root, config string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(config), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
}

// TestBuildAndLookupByNameAnswerAListedCatalogDirectoryAsTheirTwinsDo is the
// invocation proof for both tools: four rows refuse, and five answer — three with
// the listed directory there and empty, two with it absent from the stack the call
// reads or on a mode whose twin tolerates it.
//
// A refusing row is held to the whole of what the twin does with the same stack:
// the refusal is a failure naming the directory, the load and the filesystem's
// reason, and carrying the whole-catalog load's own refusal of that stack —
// identified through the accessors it carries, since it names no registered
// condition: the catalog category, the directory the load reports, and the
// filesystem's not-exist condition beneath them. It leaves every file in the
// workspace as it was — no lockfile from a fill. A refused build answers in its
// refusal shape: the Release named, no file, and no finding, since nothing had run.
//
// An answering row is held to more than "it answered": its answer must be the
// answer the same call gives over the fixture's own list, so a check that changed
// what a call reads fails here even where it refuses nothing.
//
// Two answering rows are about where the check is asked. A lookup by name with no
// Release reads the workspace's own stack, so a directory listed only for a
// Release is not its business; and describing a Release's dependency closure is
// the mode whose twin, `aggrega inspect --closure`, tolerates the directory, so it
// answers past it.
func TestBuildAndLookupByNameAnswerAListedCatalogDirectoryAsTheirTwinsDo(t *testing.T) {
	const listed = "no-such-catalog"
	const releaseFile = "release.yaml"
	const name = "core/compute/api-server"

	for _, tc := range []struct {
		name string
		// forRelease lists the directory under that Release's own entry rather than
		// for every Release.
		forRelease string
		// present creates the listed directory, empty, before the call.
		present bool
		// describe sends the describe tool rather than the build tool.
		describe bool
		// entity is the name describe looks up; empty is describe's Release mode.
		entity string
		// inRelease sends the Release with describe's name.
		inRelease bool
		// autoPull asks the build to fill its lockfile before it renders.
		autoPull bool
		// refuses is the twin's answer to the stack this call reads.
		refuses bool
	}{
		{name: "the build refuses a listed directory that does not exist and writes nothing", refuses: true},
		{name: "the build asked to fill refuses it before the fill writes the lockfile", autoPull: true, refuses: true},
		{name: "a lookup by name refuses it", describe: true, entity: name, refuses: true},
		{name: "a lookup by name in a Release's terms refuses the directory that Release reads",
			forRelease: releaseFile, describe: true, entity: name, inRelease: true, refuses: true},
		{name: "a lookup by name in the workspace's terms answers past a directory only a Release reads",
			forRelease: releaseFile, describe: true, entity: name},
		{name: "a Release's dependency closure is described past it, as inspect describes it", describe: true},
		{name: "the build renders what it renders without the listed directory, once it is there", present: true},
		{name: "the build asked to fill renders what it renders without the listed directory, once it is there",
			autoPull: true, present: true},
		{name: "a lookup by name answers what it answers without the listed directory, once it is there",
			describe: true, entity: name, present: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, root, _, releasePath := pullWorkspace(t)
			listedDir := filepath.Join(root, listed)

			tool := "build"
			var args any = buildArgs{Release: releasePath, WorkspaceRoot: root, AutoPull: tc.autoPull}
			// stackRelease is the Release whose stack the call reads, empty for the
			// workspace's own.
			stackRelease := releasePath
			if tc.describe {
				tool = "describe"
				a := describeArgs{Name: tc.entity, WorkspaceRoot: root}
				if tc.entity == "" || tc.inRelease {
					a.Release = releasePath
				}
				if tc.entity != "" && !tc.inRelease {
					stackRelease = ""
				}
				args = a
			}

			var want *callToolResult
			if !tc.refuses {
				writeCatalogDirsConfig(t, root, catalogDirsConfig("", ""))
				want = callTool(t, s, tool, args)
				if want.IsError {
					t.Fatalf("precondition: %s over the fixture's own list refused, so no row has an answer to "+
						"compare against: %s", tool, want.Content[0].Text)
				}
			}
			if tc.present {
				if err := os.Mkdir(listedDir, 0o755); err != nil {
					t.Fatalf("create the listed directory: %v", err)
				}
			}
			writeCatalogDirsConfig(t, root, catalogDirsConfig(listed, tc.forRelease))

			tw, err := s.resolveToolWorkspace(root, nil)
			if err != nil {
				t.Fatalf("resolve the fixture workspace: %v", err)
			}
			readsIt := slices.Contains(tw.catalogDirsFor(stackRelease), listedDir)
			if readsIt != (tc.forRelease == "" || tc.inRelease || tc.entity == "") {
				t.Fatalf("precondition: the stack this call reads is %v, and whether it lists %s is not what the "+
					"row is about", tw.catalogDirsFor(stackRelease), listedDir)
			}
			if tc.forRelease != "" && !slices.Contains(tw.catalogDirsFor(releasePath), listedDir) {
				t.Fatalf("precondition: the Release's own stack %v does not list %s, so the row does not hold the "+
					"directory where it says it does", tw.catalogDirsFor(releasePath), listedDir)
			}

			before := hashTree(t, root)
			got := callTool(t, s, tool, args)

			if !tc.refuses {
				if got.IsError {
					t.Fatalf("%s refused a stack its twin answers: %s", tool, got.Content[0].Text)
				}
				if tc.describe {
					if !bytes.Equal(got.StructuredContent, want.StructuredContent) || got.Content[0].Text != want.Content[0].Text {
						t.Errorf("describe answered differently from the same call over the fixture's own list\n got: %s\nwant: %s",
							got.StructuredContent, want.StructuredContent)
					}
					return
				}
				if gotFiles, wantFiles := unmarshalBuildPayload(t, got).Files, unmarshalBuildPayload(t, want).Files; !reflect.DeepEqual(gotFiles, wantFiles) || len(wantFiles) == 0 {
					t.Errorf("an empty listed directory changed what the build renders\n with it:    %v\n without it: %v", gotFiles, wantFiles)
				}
				return
			}

			if !got.IsError {
				t.Fatalf("%s answered over a stack listing %s, which does not exist: %s", tool, listedDir, got.Content[0].Text)
			}
			text := got.Content[0].Text
			for _, phrase := range []string{listedDir, "catalog load", "no such file or directory"} {
				if !strings.Contains(text, phrase) {
					t.Errorf("the refusal does not say %q:\n%s", phrase, text)
				}
			}

			refusal := tw.catalogDirsRefusal(stackRelease)
			var load *cerrors.CompilerError
			if !errors.As(refusal, &load) {
				t.Fatalf("the stack's refusal carries no compiler error: %v", refusal)
			}
			if load.Category() != cerrors.CatalogError {
				t.Errorf("the refusal is filed under %q; want %q", load.Category(), cerrors.CatalogError)
			}
			if load.Source().File != listedDir {
				t.Errorf("the refusal names %q; want the listed directory %q", load.Source().File, listedDir)
			}
			if !errors.Is(refusal, fs.ErrNotExist) {
				t.Errorf("the refusal does not carry the filesystem's not-exist condition: %v", refusal)
			}
			if chain := cerrors.CauseChain(refusal); !strings.Contains(text, chain) {
				t.Errorf("the answer does not carry the load's refusal on one line, %q:\n%s", chain, text)
			}

			if after := hashTree(t, root); after != before {
				t.Errorf("the refused %s wrote into the workspace", tool)
			}
			if present, _ := lockfileBytes(t, releasePath); present {
				t.Error("the refused build left a lockfile; the fill ran ahead of the refusal")
			}

			if tc.describe {
				if len(got.StructuredContent) != 0 {
					t.Errorf("the refused lookup carries a payload, though nothing was looked up: %s", got.StructuredContent)
				}
				return
			}
			payload := unmarshalBuildPayload(t, got)
			if told(payload.Release) != releasePath || payload.ReleaseWithheld {
				t.Errorf("the refused build names the Release %q (withheld %v); want %q", told(payload.Release),
					payload.ReleaseWithheld, releasePath)
			}
			if len(payload.Files) != 0 || payload.DependencyResolution != nil || len(payload.Advisories) != 0 ||
				len(payload.Deprecations) != 0 || len(payload.DeclinedExternalInputs) != 0 {
				t.Errorf("the refused build reports what a run produced, though nothing had run: %+v", payload)
			}
		})
	}
}
