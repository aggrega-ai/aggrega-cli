// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// Package main's end-to-end coverage for what `aggrega import` prints, on both
// streams and under both deliveries, driving the real demo importer guest.
//
// It is deliberately untagged. The helper renderings beside it in import_test.go
// pin one line each and structurally cannot see the composition — which stream
// each line goes to, in what order, and whether the drafted output a caller
// pipes stays uncorrupted. Only a run of the assembled command sees that, and a
// run nothing reaches is not a gate, so it belongs in the routine battery rather
// than behind a tag.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// demoSourceBody is the source the demo importer echoes. Its content is not the
// subject here — the guest drafts one file whatever it is given — so it names
// the format rather than modelling any real foreign document.
const demoSourceBody = "demo source"

// demoImporterResolver resolves any requested format to the demo importer
// fixture's entity, attached by no attachment, so the command can be driven
// without a catalog on disk.
type demoImporterResolver struct{}

func (demoImporterResolver) ImporterPluginForFormat(string, []string) (types.Plugin, int, error) {
	return types.Plugin{
		Metadata: types.PluginMetadata{Name: "demo-importer", Version: "1.0.0", PluginType: "importer"},
		Spec:     types.PluginSpec{Importer: &types.PluginImporter{Capabilities: []string{"import:demo"}}},
	}, -1, nil
}

// pointImportSeamAtDemoGuest points the import operation's resolver-and-loader
// seam at the built demo importer fixture for the duration of one test,
// restoring it afterwards.
//
// The seam rather than the plugin cascade is what is pointed, because real
// resolution loads the workspace's whole catalog to select an importer by the
// format it advertises, and a hermetic workspace has no catalog. The parser pair
// is left nil: the demo guest echoes its source and never draws on it.
func pointImportSeamAtDemoGuest(t *testing.T) {
	t.Helper()
	fixture := wasmtest.EnsureFixtureBuilt(t, "import-go.wasm")
	prev := importops.BuildSeam
	t.Cleanup(func() { importops.BuildSeam = prev })
	importops.BuildSeam = func(*workspaceconfig.ResolvedWorkspace, importops.PluginBytesResolver) (importops.Seam, error) {
		return importops.Seam{
			ImporterRes: demoImporterResolver{},
			ImporterLoad: func(p types.Plugin) (wasm.PluginSpec, error) {
				return wasm.PluginSpec{Name: p.Coordinate(), Path: fixture}, nil
			},
		}, nil
	}
}

// noImportPluginBytes satisfies the plugin-bytes seam for a run whose loader is
// pointed past it.
func noImportPluginBytes(*workspaceconfig.ResolvedWorkspace, string, map[string]pluginpath.ColocatedDir) (pluginpath.ResolvedPlugin, error) {
	return pluginpath.ResolvedPlugin{}, nil
}

// TestImportCommandCarriesTheSharedResultOnItsCommentaryStream drives the
// assembled command under both deliveries and pins what each stream carries, and
// where the drafts land.
//
// 🔴 Both expectations are derived from the shared operation's own result rather
// than from the command's rendering of it. An expectation computed the way the
// surface computes it would agree with a surface that had stopped reading a
// field, which is the one thing this must not do. The reported expectation is
// composed by handing the shared result's drafted files to the reported form's
// own writer — so a command that framed its answer itself, instead of routing
// through that writer, fails here.
//
// The standard output stream is pinned WHOLE rather than searched, and that is
// the load-bearing half under --dry-run: there the stream must hold one parseable
// document and nothing else, so a commentary line leaking into it would corrupt
// what a caller parses, and only an exact expectation can see that.
//
// Each run is made from a sub-folder of its workspace, so the folder the command
// runs in and the root its drafts belong beneath are two different places, and
// the filesystem claim is read over the whole workspace.
func TestImportCommandCarriesTheSharedResultOnItsCommentaryStream(t *testing.T) {
	pointImportSeamAtDemoGuest(t)

	source := filepath.Join(t.TempDir(), "demo-source.txt")
	if err := os.WriteFile(source, []byte(demoSourceBody), 0o644); err != nil {
		t.Fatalf("write the import source: %v", err)
	}

	for _, tc := range []struct {
		name string
		args []string
		// reported says the standard output stream must carry the drafted files in
		// the reported form; otherwise it carries the one wrote line below.
		reported bool
		// writes says whether the delivery is expected to put the drafted file on
		// disk. It is asserted in BOTH directions against the workspace's own
		// content digests, because the two claims are different and each is the
		// control for the other: a reporting delivery that quietly wrote would pass
		// a stream assertion, and a writing delivery that quietly stopped writing
		// would pass an assertion that only looked for an untouched tree.
		writes bool
		// wantFile is the drafted file's path relative to the workspace root.
		wantFile string
	}{
		{
			name:     "writing the drafts to disk",
			args:     []string{source, "--format", "demo"},
			writes:   true,
			wantFile: "demo.yaml",
		},
		{
			name:     "reporting the drafts on standard output",
			args:     []string{source, "--format", "demo", "--dry-run"},
			reported: true,
			wantFile: "demo.yaml",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rw := resolveTempWorkspace(t, tempWorkspaceOptions{})
			runDir := filepath.Join(rw.Root(), "sub")
			if err := os.MkdirAll(runDir, 0o755); err != nil {
				t.Fatalf("create the folder the command runs in: %v", err)
			}

			// The shared result is obtained under a reporting delivery, which writes
			// nothing, so deriving the expectation leaves no drafted file behind for
			// the run below to find already there. It is handed no destination, as
			// the command hands none, so both fields of the expectation come from the
			// result.
			shared, err := importops.Import(context.Background(), rw, importops.Options{
				SourcePath:    source,
				SourceName:    source,
				Format:        "demo",
				Delivery:      authoredcontent.DeliveryFor(true),
				ResolvePlugin: noImportPluginBytes,
			})
			if err != nil {
				t.Fatalf("run the shared import to derive the expectation: %v", err)
			}
			wantCommentary := "read as the " + shared.Format + " format, with drafted paths relative to " +
				shared.OutputDir + "\n"
			wantOut := "wrote " + tc.wantFile + "\nwrote " + importops.RecordFileName + "\n"
			if tc.reported {
				// The reported expectation is the shared result's drafted files put
				// through the reported form's own writer. Composing it here rather than
				// transcribing bytes is what keeps this an assertion about the command
				// routing through that writer, instead of a golden over the encoder's
				// formatting choices.
				reportDocs := make([]authoredcontent.Document, 0, len(shared.Files))
				for _, f := range shared.Files {
					reportDocs = append(reportDocs, authoredcontent.Document{Path: f.Path, Content: f.Content})
				}
				var wantReported bytes.Buffer
				if err := authoredcontent.Report(&wantReported, reportDocs); err != nil {
					t.Fatalf("compose the reported expectation: %v", err)
				}
				wantOut = wantReported.String()
			}

			t.Chdir(runDir)
			before := treeFingerprint(t, rw.Root())

			cmd := newImportCmd()
			var out, errOut bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			cmd.SetArgs(tc.args)
			cmd.SetContext(withWorkspaceResolver(context.Background(), rw.Root(), globalConfigLookup("", frozenEnv(nil))))
			if err := cmd.Execute(); err != nil {
				t.Fatalf("import: %v", err)
			}

			if errOut.String() != wantCommentary {
				t.Fatalf("the commentary stream carried %q; want %q", errOut.String(), wantCommentary)
			}
			if out.String() != wantOut {
				t.Fatalf("the standard output stream carried %q; want %q", out.String(), wantOut)
			}

			// The filesystem claim, read off content digests rather than off the
			// command's own account of what it did.
			diffs := fingerprintDifferences(before, treeFingerprint(t, rw.Root()))
			draft := filepath.Join(rw.Root(), tc.wantFile)
			if tc.writes {
				want := []string{importops.RecordFileName + " was created", tc.wantFile + " was created"}
				if !slices.Equal(diffs, want) {
					t.Fatalf("the writing delivery changed the workspace as %v; want exactly %v", diffs, want)
				}
				body, rerr := os.ReadFile(draft)
				if rerr != nil {
					t.Fatalf("the writing delivery did not produce %s: %v", tc.wantFile, rerr)
				}
				if string(body) != demoEchoBody {
					t.Errorf("%s holds %q; want %q", tc.wantFile, body, demoEchoBody)
				}
				return
			}
			if len(diffs) != 0 {
				t.Fatalf("the reporting delivery touched the filesystem:\n  %s", strings.Join(diffs, "\n  "))
			}
			if _, serr := os.Stat(draft); !os.IsNotExist(serr) {
				t.Errorf("the reporting delivery left %s on disk (stat: %v); it writes nothing at all", tc.wantFile, serr)
			}
		})
	}
}
