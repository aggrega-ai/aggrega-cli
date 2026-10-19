// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// importFixture is a Server over a scratch workspace with the machine-wide layer
// redirected inside the scratch tree.
//
// It reuses the registry fixture rather than standing up a second one, for the
// reason that fixture states: the redirect is what keeps a run from reading the
// configuration this machine happens to carry. No importer is wired, which is
// deliberate — every refusal asserted here is raised before an importer is
// resolved, so a fixture that stood one up would prove less about the ordering.
func importFixture(t *testing.T) *Server {
	t.Helper()
	srv, _, _ := registryWriteFixture(t, "{}\n")
	return srv
}

// importReadableFixture is importFixture with a plugin resolver wired and a JSON
// parser in the workspace's catalog folder, so a request reaches the path
// resolution, the read and the source's shape.
//
// The plain fixture stops at the resolver-wiring check, which sits AHEAD of both
// — deliberately, since the refusals it asserts are raised before an importer is
// resolved. That ordering makes it useless for asserting anything about a path:
// a request would be refused before the path was looked at, and a test reading
// that refusal would report a path verdict the code never reached.
//
// The format is selected from the source's own bytes before any importer is asked
// for, so a request that reaches format selection has already had its source
// opened and read. Selecting it parses the source in the syntax the importers'
// signatures read — JSON for the importers the binary carries — so the workspace
// holds a JSON parser, and the resolver wired here reads a module only from beside
// a Plugin entity in the workspace's catalog folder. That serves the parser, and
// no importer: the folder holds none, so a request that got as far as running one
// is refused rather than run.
func importReadableFixture(t *testing.T) *Server {
	t.Helper()
	srv := importFixture(t)
	root := srv.resolvedWorkspace.Root()
	importopstest.LayJSONParser(t, root, filepath.Join(root, workspaceconfig.ConventionalCatalogDir))
	srv.resolveImportPlugin = func(_ *workspaceconfig.ResolvedWorkspace, uri string,
		colocated map[string]pluginpath.ColocatedDir) (pluginpath.ResolvedPlugin, error) {
		cascade := pluginpath.NewResolver(pluginpath.ResolutionContext{ColocatedDirs: colocated},
			pluginpath.PinPostureNone, pluginpath.PinMaps{})
		resolved, err := cascade.Resolve(uri)
		if err != nil {
			return pluginpath.ResolvedPlugin{}, fmt.Errorf("this fixture resolves no module but one beside an entity in "+
				"the workspace's catalog folder: %w", err)
		}
		return resolved, nil
	}
	return srv
}

// TestImportReadsASourceTheAuthorSelectedOutsideTheWorkspace is the acceptance
// half of the source's path class, driven over the wire.
//
// The source is a file the person driving the client chose, so it sits wherever
// they saved it — a description exported by another tool lands in a downloads
// directory, not in the workspace an editor happens to have open. The file here
// is created in a sibling of the workspace root for exactly that reason: a test
// that put it inside the root would pass under the confining rule too and prove
// nothing about which rule ran.
//
// What is asserted is that the bytes were READ, not merely that the request got
// past a gate. Format selection reads the source's own content, so a
// format-detection refusal naming that file is proof the read happened — and it
// is a sharper assertion than "some other error came back", which a refusal
// raised for an unrelated reason would also satisfy.
func TestImportReadsASourceTheAuthorSelectedOutsideTheWorkspace(t *testing.T) {
	t.Parallel()
	srv := importReadableFixture(t)

	outside := filepath.Join(t.TempDir(), "exported-flow.json")
	if err := os.WriteFile(outside, []byte("{\"nodes\": []}\n"), 0o644); err != nil {
		t.Fatalf("seed the selected source: %v", err)
	}

	resp := callDaemonMethod(t, srv, "aggrega/import", map[string]any{
		"source":    outside,
		"outputDir": "drafts",
	})
	if resp.Error == nil {
		t.Fatal("this fixture resolves no importer, so the request cannot have completed")
	}
	if strings.Contains(resp.Error.Message, "inside the workspace this session opened") {
		t.Fatalf("an absolute source the author selected was refused as a workspace-scoped path: %q", resp.Error.Message)
	}
	if !strings.Contains(resp.Error.Message, "was not recognised from its shape") {
		t.Fatalf("the request did not reach format selection, so nothing here shows the source was read: %q", resp.Error.Message)
	}
	if !strings.Contains(resp.Error.Message, outside) {
		t.Errorf("the answer does not name the file that was read: %q, want it to name %q", resp.Error.Message, outside)
	}
}

// TestImportStillConfinesItsDestination is the other direction over the same
// method, and the two together are what make the split attributable.
//
// Widening what an operation reads must not widen where it writes. Read alone,
// the acceptance above is consistent with a method that stopped confining
// anything, so the destination is asserted on the same surface: an absolute
// outputDir is refused exactly as before, with the workspace bound named.
func TestImportStillConfinesItsDestination(t *testing.T) {
	t.Parallel()
	srv := importReadableFixture(t)

	elsewhere := t.TempDir()
	source := filepath.Join(elsewhere, "exported-flow.json")
	if err := os.WriteFile(source, []byte("{\"nodes\": []}\n"), 0o644); err != nil {
		t.Fatalf("seed the selected source: %v", err)
	}

	// The source is the accepted absolute path, so a refusal here can only be
	// about the destination.
	resp := callDaemonMethod(t, srv, "aggrega/import", map[string]any{
		"source":    source,
		"outputDir": elsewhere,
	})
	if resp.Error == nil {
		t.Fatal("an absolute outputDir outside the workspace was accepted")
	}
	if !strings.Contains(resp.Error.Message, "outputDir must name a path inside the workspace this session opened") {
		t.Errorf("the refusal does not name the destination bound: %q", resp.Error.Message)
	}
	if !strings.Contains(resp.Error.Message, "is absolute or climbs out of it") {
		t.Errorf("the refusal does not say what was wrong with the value: %q", resp.Error.Message)
	}

	// A destination climbing out of the root is refused on the same rule, so the
	// refusal above is not merely about the leading separator.
	resp = callDaemonMethod(t, srv, "aggrega/import", map[string]any{
		"source":    source,
		"outputDir": "../drafts",
	})
	if resp.Error == nil {
		t.Fatal("an outputDir climbing out of the workspace was accepted")
	}
	if !strings.Contains(resp.Error.Message, "inside the workspace this session opened") {
		t.Errorf("the refusal does not name the destination bound: %q", resp.Error.Message)
	}
}

// TestImportRefusesARequestNamingNoSource drives the one required-field refusal
// on aggrega/import, on THIS surface's spelling.
//
// source is the one field a request must carry. outputDir is optional, as the
// command line's --output-dir is, so a request naming no destination is not
// among these rows; the positive control below holds that it passes the field
// gate.
//
// The refusals name request fields and never a command-line flag: a caller here
// cannot act on a flag it has no way to send.
func TestImportRefusesARequestNamingNoSource(t *testing.T) {
	t.Parallel()
	srv := importFixture(t)

	for _, tc := range []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"no source", map[string]any{"outputDir": "drafts"}, "source is required"},
		{"neither", map[string]any{}, "source is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := callDaemonMethod(t, srv, "aggrega/import", tc.params)
			if resp.Error == nil {
				t.Fatal("aggrega/import accepted a request missing a required field")
			}
			if resp.Error.Code != jsonRPCInvalidParams {
				t.Errorf("refusal code = %d; want %d, the invalid-params code every "+
					"missing-field refusal here answers with", resp.Error.Code, jsonRPCInvalidParams)
			}
			if !strings.Contains(resp.Error.Message, tc.want) {
				t.Errorf("refusal = %q; want it to contain %q", resp.Error.Message, tc.want)
			}
			if strings.Contains(resp.Error.Message, "--output-dir") {
				t.Errorf("the refusal names a command-line flag a request cannot send: %q", resp.Error.Message)
			}
		})
	}
}

// TestImportPassesTheRequiredFieldGateWithOrWithoutADestination is the positive
// control for the refusals above, and it is what keeps them from passing on a
// handler that refuses everything. Its rows naming no outputDir are the session's
// half of the one destination rule: a destination is optional here, under either
// delivery, as it is on the command line.
//
// It asserts only that execution reached PAST the required-field gate, which is
// this test's subject: the fixture wires no plugin resolver, so the request
// stops at that wiring rather than at its own parameters, and it stops with a
// different code and a message naming something other than a missing field.
// What the write itself produces is proven where the write lives — against the
// real importer guest — rather than here.
//
// 🔴 PLANTED DEFECT: restore a refusal of an empty outputDir in dispatchImport
// (internal/serve/import.go) ahead of the workspace check: the three rows naming
// no destination redden as invalid params.
func TestImportPassesTheRequiredFieldGateWithOrWithoutADestination(t *testing.T) {
	t.Parallel()
	srv := importFixture(t)

	for _, tc := range []struct {
		name   string
		params map[string]any
	}{
		{"a source and a destination", map[string]any{"source": "flow.json", "outputDir": "drafts"}},
		{"a source naming no destination", map[string]any{"source": "flow.json"}},
		{"a source reported rather than written, naming no destination",
			map[string]any{"source": "flow.json", "dryRun": true}},
		{"a source with a format, reported, naming no destination",
			map[string]any{"source": "flow.json", "format": "demo", "dryRun": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := callDaemonMethod(t, srv, "aggrega/import", tc.params)
			if resp.Error == nil {
				t.Fatal("the fixture wires no importer, so this request cannot have succeeded")
			}
			if resp.Error.Code == jsonRPCInvalidParams {
				t.Fatalf("a request carrying every required field was refused as invalid params: %q",
					resp.Error.Message)
			}
			if strings.Contains(resp.Error.Message, "is required") {
				t.Fatalf("a request carrying every required field drew a missing-field refusal: %q",
					resp.Error.Message)
			}
		})
	}
}
