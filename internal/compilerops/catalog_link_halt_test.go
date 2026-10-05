// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
)

// TestAResolutionHaltsOnALinkLeadingOutOfACatalogDirectory drives the shared
// closure resolution every build, preview, graph, lint, validation and describe
// runs, over the self-contained fixture with one more catalog directory beside its
// own, and reads back whether the resolution completed or halted.
//
// The extra directory sits outside the workspace, a root of its own. In the control
// row it holds an unrelated entity; in the other the same entity is a link leading
// out of it. The entity is not one the fixture's Release reaches, which is the
// point: the lookup passes such a link over unread, so nothing in the resolution
// would otherwise notice it, and a reference to the entity it stands for would
// resolve another of the same name or nothing — so the resolution halts on the link
// before any name is resolved through the catalog, naming it.
func TestAResolutionHaltsOnALinkLeadingOutOfACatalogDirectory(t *testing.T) {
	const unrelated = "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: example/unrelated\n" +
		"  version: \"1.0.0\"\n  labels:\n    component.aggrega.ai/type: server\nspec: {}\n"
	for _, row := range []struct {
		name   string
		linked bool
	}{
		{name: "control: an unrelated entity in a second catalog directory"},
		{name: "the same entity a link leading out of that directory", linked: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			_, releasePath, workspaceRoot, wsCfg := fixtureWorkspace(t)
			extra := t.TempDir()
			elsewhere := t.TempDir()
			entity := filepath.Join(extra, "unrelated.yaml")
			if row.linked {
				writeHaltFile(t, filepath.Join(elsewhere, "unrelated.yaml"), unrelated)
				if err := os.Symlink(filepath.Join(elsewhere, "unrelated.yaml"), entity); err != nil {
					t.Skipf("symbolic links are not available here: %v", err)
				}
			} else {
				writeHaltFile(t, entity, unrelated)
			}
			dirs := []string{filepath.Join(workspaceRoot, "catalog"), extra}
			view := catalog.NewLazyLookup(workspaceRoot, wsCfg.ResolvedArtifactCacheDir(), dirs, nil).ForRequest(nil)

			resolved, cerr := ResolveClosure(context.Background(), ClosureInputs{
				View:            view,
				ReleasePath:     releasePath,
				WorkspaceRoot:   workspaceRoot,
				WorkspaceConfig: wsCfg,
			})
			if !row.linked {
				if cerr != nil {
					t.Fatalf("the control did not resolve: stage=%d %v", cerr.Stage, cerr)
				}
				return
			}
			if cerr == nil {
				t.Fatalf("resolved %+v past a link leading out of a catalog directory", resolved)
			}
			if cerr.Stage != ClosureStageComputeClosure {
				t.Errorf("stage = %d, want the closure-walk stage (%d)", cerr.Stage, ClosureStageComputeClosure)
			}
			if !errors.Is(cerr, readscope.ErrReadLeadsOutOfWorkspace()) {
				t.Fatalf("want the read-leads-out-of-workspace halt, got %v", cerr)
			}
			if !strings.Contains(cerr.Error(), "the link "+entity+" leads to ") {
				t.Errorf("the halt must name the link %s: %v", entity, cerr)
			}
		})
	}
}

func writeHaltFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
