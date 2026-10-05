// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	stderrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestRenderRefusal_PointsWhereTheRefusalsOwnChainPoints is the one route from a
// refusal raised while rendering to the document and key every positioned surface
// reports it at: one shape of chain per row, the control first.
func TestRenderRefusal_PointsWhereTheRefusalsOwnChainPoints(t *testing.T) {
	ws := t.TempDir()
	aggregate := filepath.Join(ws, "aggregate.yaml")
	recipe := filepath.Join(ws, "catalog", "recipe.yaml")
	directory := filepath.Join(ws, "catalog")
	for path, body := range map[string]string{
		aggregate: "kind: Aggregate\n",
		recipe:    "kind: Recipe\nspec:\n  workload: |\n    kind: Workload\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cause := stderrors.New("references slot \"admin_keyy\" which is not declared")
	node := func(err error) error {
		return fmt.Errorf("rendering: %w",
			cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: "router"}, "rendering node \"router\""))
	}

	for _, tc := range []struct {
		name     string
		gate     PreviewGate
		err      error
		wantDoc  string
		wantKey  string
		reported bool
	}{
		{
			name: "a refusal from a gate ahead of the render",
			gate: GateLockfileDrift,
			err:  stderrors.New("the lockfile no longer matches"),
		},
		{
			name:     "a raising site stated the document and the key",
			gate:     GateRender,
			err:      node(cerrors.Locate(cause, cerrors.SourceLocation{File: recipe, Locator: ".spec.workload"})),
			wantDoc:  recipe,
			wantKey:  ".spec.workload",
			reported: true,
		},
		{
			name: "a frame's pointer is an absolute path to a document",
			gate: GateRender,
			err: node(cerrors.Wrap(cause, cerrors.CatalogError, cerrors.SourceLocation{File: recipe},
				"recipe \"r\" workload template")),
			wantDoc:  recipe,
			reported: true,
		},
		{
			name:     "every pointer names a node, not a document",
			gate:     GateRender,
			err:      node(cause),
			wantDoc:  aggregate,
			reported: true,
		},
		{
			name: "a pointer with a key beside it names a document relative to the workspace",
			gate: GateRender,
			err: node(cerrors.Wrap(cause, cerrors.CatalogError,
				cerrors.SourceLocation{File: filepath.Join("catalog", "recipe.yaml"), Locator: ".spec.workload"}, "frame")),
			wantDoc:  recipe,
			wantKey:  ".spec.workload",
			reported: true,
		},
		{
			name: "a stated document nothing is at gives way to the frame beneath it",
			gate: GateRender,
			err: node(cerrors.Locate(cerrors.Wrap(cause, cerrors.CatalogError, cerrors.SourceLocation{File: recipe}, "frame"),
				cerrors.SourceLocation{File: filepath.Join(ws, "gone.yaml"), Locator: ".spec.workload"})),
			wantDoc:  recipe,
			reported: true,
		},
		{
			name: "an absolute pointer to a directory is not a document",
			gate: GateRender,
			err: node(cerrors.Wrap(cause, cerrors.CatalogError, cerrors.SourceLocation{File: directory},
				"catalog directory")),
			wantDoc:  aggregate,
			reported: true,
		},
		{
			name: "a relative pointer climbing out of the workspace is not followed",
			gate: GateRender,
			err: node(cerrors.Wrap(cause, cerrors.CatalogError,
				cerrors.SourceLocation{File: filepath.Join("..", "elsewhere.yaml"), Locator: ".spec"}, "frame")),
			wantDoc:  aggregate,
			reported: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			preview := &PreviewResult{
				Resolved:   &ResolvedClosure{AggregatePath: aggregate, AggBytes: []byte("kind: Aggregate\n")},
				FailedGate: tc.gate,
			}
			if tc.gate == GateRender {
				preview.renderFailure, preview.workspaceRoot = tc.err, ws
			}
			got, ok := preview.RenderRefusal()
			if ok != tc.reported {
				t.Fatalf("RenderRefusal reported %v; want %v", ok, tc.reported)
			}
			if !ok {
				return
			}
			if got.Document != tc.wantDoc || got.KeyPath != tc.wantKey {
				t.Errorf("the refusal points at %s %q; want %s %q", got.Document, got.KeyPath, tc.wantDoc, tc.wantKey)
			}
			if got.Message != cerrors.CauseChain(tc.err) {
				t.Errorf("the message is not the refusal with its causes:\ngot:  %q\nwant: %q",
					got.Message, cerrors.CauseChain(tc.err))
			}
			want, err := os.ReadFile(tc.wantDoc)
			if err != nil {
				t.Fatal(err)
			}
			if string(got.Content) != string(want) {
				t.Errorf("the content is not the bytes of %s: %q", tc.wantDoc, got.Content)
			}
			if got.Err != tc.err {
				t.Error("the refusal handed back is not the one the run raised")
			}
		})
	}
}
