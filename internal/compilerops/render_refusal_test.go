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
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// place is where one finding of a refusal must point: a document and a key there.
type place struct {
	doc, key string
}

// TestRenderRefusal_PointsWhereTheRefusalsOwnChainPoints is the one route from a
// refusal the pipeline run raised to the findings it states and the document and
// key each one points at: one shape of chain per row, the control first.
func TestRenderRefusal_PointsWhereTheRefusalsOwnChainPoints(t *testing.T) {
	ws := t.TempDir()
	aggregate := filepath.Join(ws, "aggregate.yaml")
	release := filepath.Join(ws, "release.yaml")
	recipe := filepath.Join(ws, "catalog", "recipe.yaml")
	directory := filepath.Join(ws, "catalog")
	for path, body := range map[string]string{
		aggregate: "kind: Aggregate\n",
		release:   "kind: Release\nspec:\n  components:\n    app:\n      uses:\n        models:\n          local:\n            target: nosuch\n",
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
	// A finding the graph raises: an envelope naming the document that wrote the key
	// relative to the workspace, with the key beside it — the shape the composition
	// of a graph joins one of per violation.
	written := func(document, key, text string) error {
		return cerrors.Wrap(stderrors.New(text), cerrors.OpsError, cerrors.SourceLocation{File: document, Locator: key}, "edge")
	}
	dangling := written("release.yaml", ".spec.components.app.uses.models.local", "uses edge target does not exist (\"nosuch\")")
	other := written("release.yaml", ".spec.components.app.uses.models.other", "uses edge target does not exist (\"nowhere\")")
	inAggregate := written("aggregate.yaml", ".spec.components.app", "connection alias is already used")
	appKey := ".spec.components.app.uses.models.local"
	// A finding a resolver rule raises over what one document wrote: its locator
	// is a path inside the file its subject recorded, and the envelope it converts
	// to carries that locator where a pointer names a logical identifier — so the
	// recorded file is the only thing naming the document.
	resolverFinding := func(file string) *resolver.ResolverError {
		return &resolver.ResolverError{Entity: "Release r", Field: appKey, File: file,
			Message: "values pillar does not conform", Err: resolver.ErrSchemaConformance()}
	}

	for _, tc := range []struct {
		name     string
		gate     PreviewGate
		err      error
		want     []place
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
			want:     []place{{recipe, ".spec.workload"}},
			reported: true,
		},
		{
			name: "a frame's pointer is an absolute path to a document",
			gate: GateRender,
			err: node(cerrors.Wrap(cause, cerrors.CatalogError, cerrors.SourceLocation{File: recipe},
				"recipe \"r\" workload template")),
			want:     []place{{recipe, ""}},
			reported: true,
		},
		{
			name:     "every pointer names a node, not a document",
			gate:     GateRender,
			err:      node(cause),
			want:     []place{{aggregate, ""}},
			reported: true,
		},
		{
			name: "a pointer with a key beside it names a document relative to the workspace",
			gate: GateRender,
			err: node(cerrors.Wrap(cause, cerrors.CatalogError,
				cerrors.SourceLocation{File: filepath.Join("catalog", "recipe.yaml"), Locator: ".spec.workload"}, "frame")),
			want:     []place{{recipe, ".spec.workload"}},
			reported: true,
		},
		{
			name: "a stated document nothing is at gives way to the frame beneath it",
			gate: GateRender,
			err: node(cerrors.Locate(cerrors.Wrap(cause, cerrors.CatalogError, cerrors.SourceLocation{File: recipe}, "frame"),
				cerrors.SourceLocation{File: filepath.Join(ws, "gone.yaml"), Locator: ".spec.workload"})),
			want:     []place{{recipe, ""}},
			reported: true,
		},
		{
			name: "an absolute pointer to a directory is not a document",
			gate: GateRender,
			err: node(cerrors.Wrap(cause, cerrors.CatalogError, cerrors.SourceLocation{File: directory},
				"catalog directory")),
			want:     []place{{aggregate, ""}},
			reported: true,
		},
		{
			name: "a relative pointer climbing out of the workspace is not followed",
			gate: GateRender,
			err: node(cerrors.Wrap(cause, cerrors.CatalogError,
				cerrors.SourceLocation{File: filepath.Join("..", "elsewhere.yaml"), Locator: ".spec"}, "frame")),
			want:     []place{{aggregate, ""}},
			reported: true,
		},
		{
			name:     "a refusal joining one finding the graph raised points where that finding points",
			gate:     GateRender,
			err:      stderrors.Join(dangling),
			want:     []place{{release, appKey}},
			reported: true,
		},
		{
			name:     "a refusal joining two findings states each where it points",
			gate:     GateRender,
			err:      stderrors.Join(dangling, other),
			want:     []place{{release, appKey}, {release, ".spec.components.app.uses.models.other"}},
			reported: true,
		},
		{
			name:     "a join reached through a wrap that adds context is read through it",
			gate:     GateRender,
			err:      fmt.Errorf("graph: %w", stderrors.Join(dangling, inAggregate)),
			want:     []place{{release, appKey}, {aggregate, ".spec.components.app"}},
			reported: true,
		},
		{
			name:     "a join inside a join is read to its findings",
			gate:     GateRender,
			err:      stderrors.Join(stderrors.Join(dangling, other), inAggregate),
			want:     []place{{release, appKey}, {release, ".spec.components.app.uses.models.other"}, {aggregate, ".spec.components.app"}},
			reported: true,
		},
		{
			name:     "a joined finding naming no document is placed on the Aggregate",
			gate:     GateRender,
			err:      stderrors.Join(dangling, cause),
			want:     []place{{release, appKey}, {aggregate, ""}},
			reported: true,
		},
		{
			name:     "a resolver finding joined beneath a wrap points at the document its subject recorded",
			gate:     GateRender,
			err:      fmt.Errorf("graph construction: %w", stderrors.Join(resolverFinding("release.yaml"))),
			want:     []place{{release, appKey}},
			reported: true,
		},
		{
			name:     "a resolver finding converted to its envelope still points at the document its subject recorded",
			gate:     GateRender,
			err:      resolverFinding("release.yaml").ToCompilerError(),
			want:     []place{{release, appKey}},
			reported: true,
		},
		{
			name:     "a resolver finding whose subject recorded no document is placed on the Aggregate",
			gate:     GateRender,
			err:      stderrors.Join(resolverFinding("")),
			want:     []place{{aggregate, ""}},
			reported: true,
		},
		{
			// The terminal prints the envelope as the refusal's head and the join as its
			// causes, so the join is that envelope's detail and the refusal states one
			// finding, placed by the envelope's own route.
			name:     "a join beneath an envelope stays that envelope's detail",
			gate:     GateRender,
			err:      cerrors.Wrap(stderrors.Join(dangling, other), cerrors.OpsError, cerrors.SourceLocation{}, "recipe resolution"),
			want:     []place{{aggregate, ""}},
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
			if got.Message != cerrors.CauseChain(tc.err) {
				t.Errorf("the message is not the refusal with its causes:\ngot:  %q\nwant: %q",
					got.Message, cerrors.CauseChain(tc.err))
			}
			if got.Err != tc.err {
				t.Error("the refusal handed back is not the one the run raised")
			}
			if len(got.Findings) != len(tc.want) {
				t.Fatalf("the refusal states %d finding(s); want %d: %+v", len(got.Findings), len(tc.want), got.Findings)
			}
			for i, want := range tc.want {
				finding := got.Findings[i]
				if finding.Document != want.doc || finding.KeyPath != want.key {
					t.Errorf("finding %d points at %s %q; want %s %q", i, finding.Document, finding.KeyPath, want.doc, want.key)
				}
				if finding.Message != cerrors.CauseChain(finding.Err) {
					t.Errorf("finding %d's message is not its own chain: %q", i, finding.Message)
				}
				if _, kind := LeadingFrame(tc.err); kind != FrameMulti && (finding.Err != tc.err || finding.Message != got.Message) {
					t.Errorf("a refusal that joins nothing is its own one finding; got %q", finding.Message)
				}
				content, err := os.ReadFile(want.doc)
				if err != nil {
					t.Fatal(err)
				}
				if string(finding.Content) != string(content) {
					t.Errorf("finding %d's content is not the bytes of %s: %q", i, want.doc, finding.Content)
				}
			}
		})
	}
}
