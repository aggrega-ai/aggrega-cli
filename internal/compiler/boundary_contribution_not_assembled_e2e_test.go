// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// This test reads catalog entities from the aggrega-hub repository checked out
// beside this one, as the compose-bridge end-to-end test does.

package compiler_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// buildComposeBridgeVariantE copies the compose-bridge fixture into a temporary
// tree, lets edit rewrite its Release, and builds it through the real pipeline
// with the shipped bridge entities, returning the build's result and error.
func buildComposeBridgeVariantE(t *testing.T, edit func(release string) string) (map[string][]byte, string, error) {
	t.Helper()
	root := t.TempDir()
	copyDirTree(t, "testdata/compose-bridge", root)
	releasePath := filepath.Join(root, "release.yaml")
	src, err := os.ReadFile(releasePath)
	if err != nil {
		t.Fatalf("read the fixture Release: %v", err)
	}
	if err := os.WriteFile(releasePath, []byte(edit(string(src))), 0o644); err != nil {
		t.Fatalf("write the Release: %v", err)
	}
	result, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath: releasePath,
		CatalogPaths: []string{
			filepath.Join(root, "catalog", "base"),
			"../../../aggrega-hub/aggrega-catalog/feature/core/compose-bridge/1.0.0/feature.yaml",
			"../../../aggrega-hub/aggrega-catalog/feature-recipe/compose/bridge",
			"../../../aggrega-hub/aggrega-catalog/schemalibrary/base-components/1.0.0/library.yaml",
		},
	})
	if err != nil {
		return nil, releasePath, err
	}
	return result.Artifacts, releasePath, nil
}

// childSubDirectory gives both of the fixture's child boundaries the
// sub-directory value given, so their components evaluate to a different
// dispatch path from the bridge's, which the parent boundary's own default
// places at its capability segment.
func childSubDirectory(t *testing.T, value string) func(string) string {
	return func(release string) string {
		const anchor = "          directory: \"{{ .parent_boundary.directory }}\"\n"
		if strings.Count(release, anchor) != 2 {
			t.Fatalf("the fixture Release carries %q %d time(s), want 2", anchor, strings.Count(release, anchor))
		}
		return strings.ReplaceAll(release, anchor, anchor+"          subDirectory: \""+value+"\"\n")
	}
}

// TestBoundaryContribution_FragmentsNoAssemblerFoldsHaltTheBuild builds the
// compose-bridge fixture through the real pipeline, the shipped compose-bridge
// FeatureRecipe and the embedded compose guest. The bridge is attached at the
// parent boundary and renders once for it; its gateway is a pre-assembly
// fragment, which reaches a file only through the compose assembly of the group
// it joins.
//
// As written, the two child boundaries share the parent's directory, so every
// component joins the bridge's group: the build renders and the gateway is in the
// one merged file. Moved under a sub-directory of their own — one per component,
// or one both children share — the components leave the bridge's group with no
// component in it, nothing is elected to assemble it, and the build halts with
// the boundary contribution condition, positioned at the bridge's entry in the
// Release and naming the boundary, the Feature, the dispatch path and the
// provider. Each refused row carries one fault: the layout edit and nothing else.
func TestBoundaryContribution_FragmentsNoAssemblerFoldsHaltTheBuild(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		edit    func(string) string
		refused bool
	}{
		{"the components share the bridge's file", func(r string) string { return r }, false},
		{"each component under a sub-directory of its own", childSubDirectory(t, "{{ .component_id }}"), true},
		{"both children under one sub-directory that is not the bridge's", childSubDirectory(t, "elsewhere"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			artifacts, releasePath, err := buildComposeBridgeVariantE(t, tc.edit)
			if !tc.refused {
				if err != nil {
					t.Fatalf("the build must render; got %s", cerrors.CauseChain(err))
				}
				const merged = "dist/cluster/compose/docker-compose.yaml"
				body, ok := artifacts[merged]
				if !ok {
					t.Fatalf("no merged compose file at %s; files: %v", merged, sortedKeys(artifacts))
				}
				if !strings.Contains(string(body), "aggrega-gw:") {
					t.Errorf("the merged compose file carries no gateway service:\n%s", body)
				}
				return
			}
			if err == nil {
				t.Fatalf("the build rendered %v; it must halt with the boundary contribution condition", sortedKeys(artifacts))
			}
			if !errors.Is(err, renderer.ErrBoundaryContributionNotAssembled()) {
				t.Fatalf("the halt must carry the boundary contribution condition; got %s", cerrors.CauseChain(err))
			}
			if errors.Is(err, renderer.ErrBoundaryAssemblerMissing()) {
				t.Fatalf("the halt must carry one condition, not also the missing-assembler one; got %s", cerrors.CauseChain(err))
			}
			var ce *cerrors.CompilerError
			if !errors.As(err, &ce) {
				t.Fatalf("the halt is not a compiler diagnostic: %v", err)
			}
			if ce.Category() != cerrors.OpsError {
				t.Errorf("category = %s, want %s", ce.Category(), cerrors.OpsError)
			}
			if filepath.Base(ce.Source().File) != filepath.Base(releasePath) {
				t.Errorf("source file = %q, want the Release the bridge is attached in", ce.Source().File)
			}
			if want := ".spec.boundaries.cluster.features.bridge"; ce.Source().Locator != want {
				t.Errorf("locator = %q, want the bridge's own entry %q", ce.Source().Locator, want)
			}
			chain := cerrors.CauseChain(err)
			for _, want := range []string{
				`boundary "cluster"`,
				`feature "bridge"`,
				`dispatch path "dist/cluster/compose"`,
				`provider "compose"`,
			} {
				if !strings.Contains(chain, want) {
					t.Errorf("the refusal does not carry %s:\n%s", want, chain)
				}
			}
		})
	}
}
