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

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The two tools whose command-line twin loads the whole catalog answer a catalog
// directory as that twin answers it: `build` as `aggrega build` does, and
// `describe` looking a name up as `aggrega inspect` does.
//
// A directory the workspace configuration lists where nothing exists is not in
// the stack the call reads, so it is read as holding nothing and the call answers
// exactly what it answers over the fixture's own list. A directory the call
// itself names, as a command line names one with --catalog, and where nothing
// exists, is refused with the load's own refusal before anything is resolved or
// written — the lockfile a filling build writes included.
//
// The fixture is the hermetic end-to-end workspace, copied and configured offline,
// carrying no lockfile. A row writes one configuration and passes the call one
// list of directories, and changes nothing else, so a refusing row has one fault —
// a call refused for anything else fails its row rather than passing it. A row
// comparing against the fixture's own list makes that call first, in the same
// workspace, so the two answers name the same paths.

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
// invocation proof for both tools: nine rows answer and three refuse.
//
// An answering row is held to more than "it answered": its answer must be the
// answer the same call gives over the fixture's own list, so a reading of the
// listing that changed what a call reads fails here even where it refuses
// nothing. A row listing a directory where nothing exists also holds the stack
// the call reads to leaving it out.
//
// A building row is held to naming such a directory exactly once, in the line the
// resolver composes for the call's stack — the line `aggrega build` prints for it:
// as the first of the build's advisories, at warning severity, where no fill runs,
// and among the resolution's own diagnostics where the fill runs and writes that
// line itself. A building row with the directory there names it nowhere. A lookup
// by name answers in a shape that carries no advisory, so its whole answer is held
// to the one given over the fixture's own list, which names no directory.
//
// A refusing row is held to the whole of what the twin does with the same stack:
// the refusal is a failure naming the directory, the load and the filesystem's
// reason, and carrying the whole-catalog load's own refusal of that stack —
// identified through the accessors it carries, since it names no registered
// condition: the catalog category, the directory the load reports, and the
// filesystem's not-exist condition beneath them. It leaves every file in the
// workspace as it was — no lockfile from a fill. A refused build answers in its
// refusal shape: the Release named, no file, and no finding, since nothing had run.
func TestBuildAndLookupByNameAnswerAListedCatalogDirectoryAsTheirTwinsDo(t *testing.T) {
	const listed = "no-such-catalog"
	const releaseFile = "release.yaml"
	const name = "core/compute/api-server"

	for _, tc := range []struct {
		name string
		// forRelease lists the directory under that Release's own entry rather than
		// for every Release.
		forRelease string
		// byCall passes the directory as the call's own catalog_dirs rather than
		// listing it in the configuration.
		byCall bool
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
		{name: "the build reads a listed directory where nothing exists as holding nothing"},
		{name: "the build asked to fill reads it as holding nothing", autoPull: true},
		{name: "a lookup by name reads it as holding nothing", describe: true, entity: name},
		{name: "a lookup by name in a Release's terms reads the directory that Release lists as holding nothing",
			forRelease: releaseFile, describe: true, entity: name, inRelease: true},
		{name: "a lookup by name in the workspace's terms answers past a directory only a Release lists",
			forRelease: releaseFile, describe: true, entity: name},
		{name: "a Release's dependency closure is described past it, as inspect describes it", describe: true},
		{name: "the build renders what it renders without the listed directory, once it is there", present: true},
		{name: "the build asked to fill renders what it renders without the listed directory, once it is there",
			autoPull: true, present: true},
		{name: "a lookup by name answers what it answers without the listed directory, once it is there",
			describe: true, entity: name, present: true},
		{name: "the build refuses a directory the call names that does not exist and writes nothing", byCall: true, refuses: true},
		{name: "the build asked to fill refuses it before the fill writes the lockfile", byCall: true, autoPull: true, refuses: true},
		{name: "a lookup by name refuses it", byCall: true, describe: true, entity: name, refuses: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, root, _, releasePath := pullWorkspace(t)
			listedDir := filepath.Join(root, listed)
			var callDirs []string
			if tc.byCall {
				callDirs = []string{listedDir}
			}

			tool := "build"
			var args any = buildArgs{Release: releasePath, WorkspaceRoot: root, AutoPull: tc.autoPull, CatalogDirs: callDirs}
			// stackRelease is the Release whose stack the call reads, empty for the
			// workspace's own.
			stackRelease := releasePath
			if tc.describe {
				tool = "describe"
				a := describeArgs{Name: tc.entity, WorkspaceRoot: root, CatalogDirs: callDirs}
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
			if tc.byCall {
				writeCatalogDirsConfig(t, root, catalogDirsConfig("", ""))
			} else {
				writeCatalogDirsConfig(t, root, catalogDirsConfig(listed, tc.forRelease))
			}

			tw, err := s.resolveToolWorkspace(root, callDirs)
			if err != nil {
				t.Fatalf("resolve the fixture workspace: %v", err)
			}
			// The stack holds the directory exactly where it is there and listed for
			// this stack, or where the call names it.
			listedHere := tc.forRelease == "" || tc.inRelease || tc.entity == ""
			wantReads := tc.byCall || (tc.present && listedHere)
			if readsIt := slices.Contains(tw.catalogDirsFor(stackRelease), listedDir); readsIt != wantReads {
				t.Fatalf("precondition: the stack this call reads is %v, and whether it lists %s is not what the "+
					"row is about", tw.catalogDirsFor(stackRelease), listedDir)
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
				payload := unmarshalBuildPayload(t, got)
				if gotFiles, wantFiles := payload.Files, unmarshalBuildPayload(t, want).Files; !reflect.DeepEqual(gotFiles, wantFiles) || len(wantFiles) == 0 {
					t.Errorf("the listing changed what the build renders\n with it:    %v\n without it: %v", gotFiles, wantFiles)
				}

				// The line the resolver composes for the stack this call reads, with
				// the indentation a terminal nests it under dropped.
				var composed bytes.Buffer
				tw.rw.NoteAbsentCatalogDirs(&composed, stackRelease, callDirs)
				line := strings.TrimSpace(composed.String())
				absentHere := !tc.present && !tc.byCall && listedHere
				if (line != "") != absentHere {
					t.Fatalf("precondition: the resolver composes %q for this stack, and whether it names %s is not what the row is about",
						line, listedDir)
				}
				names := string(filepath.Separator) + listed + " is listed under "
				var advised []buildAdvisory
				for _, a := range payload.Advisories {
					if strings.Contains(a.Message, names) {
						advised = append(advised, a)
					}
				}
				var resolved []string
				if payload.DependencyResolution != nil {
					for _, d := range payload.DependencyResolution.Diagnostics {
						if strings.Contains(d, names) {
							resolved = append(resolved, d)
						}
					}
				}
				wantAdvised, wantResolved := 0, 0
				if absentHere {
					if tc.autoPull {
						wantResolved = 1
					} else {
						wantAdvised = 1
					}
				}
				if len(advised) != wantAdvised || len(resolved) != wantResolved {
					t.Fatalf("the build names %s in %d advisory(ies) and %d resolution line(s); want %d and %d\nadvisories: %+v\nresolution: %+v",
						listedDir, len(advised), len(resolved), wantAdvised, wantResolved, payload.Advisories, payload.DependencyResolution)
				}
				for _, a := range advised {
					if a.Severity != abi.SeverityWarn || a.Message != line || a.Code != "" || a.KeyPath != "" {
						t.Errorf("the advisory naming %s is %+v; want severity %q, no code and no key, reading the resolver's own line %q",
							listedDir, a, abi.SeverityWarn, line)
					}
					if payload.Advisories[0] != a {
						t.Errorf("the advisory naming %s does not lead the build's advisories: %+v", listedDir, payload.Advisories)
					}
				}
				for _, d := range resolved {
					if d != line {
						t.Errorf("the resolution's line naming %s reads %q; want the resolver's own line %q", listedDir, d, line)
					}
				}
				return
			}

			if !got.IsError {
				t.Fatalf("%s answered over a stack naming %s, which does not exist: %s", tool, listedDir, got.Content[0].Text)
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
				t.Errorf("the refusal names %q; want the directory the call names %q", load.Source().File, listedDir)
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
