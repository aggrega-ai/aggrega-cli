// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// refusedRecipe is a Recipe document that fails its kind's schema at load: a
// capability and nothing else, so the tolerant load records it as refused and it
// never enters the catalog. name and version are what its header declares, which
// is the identity a failing reference is matched against.
func refusedRecipe(name, version, capability string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: ` + name + `
  version: "` + version + `"
spec:
  capability: ` + capability + `
`
}

// renderedPullFailure runs a pull over root and returns the diagnostic the
// reader would see, rendered through the same formatter the command boundary
// applies. It fails the test when the pull succeeds, because every assertion
// below is about the shape of a failure.
func renderedPullFailure(t *testing.T, root, releasePath, catalogRoot string) (error, string) {
	t.Helper()
	err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{catalogRoot},
		DiagOut:     os.Stderr,
		Out:         os.Stdout,
	})
	if err == nil {
		t.Fatal("the pull succeeded; there is no diagnostic to read")
	}
	return err, formatCompilerError(err)
}

// TestPull_SoleRefusalLeadsTheDiagnostic is acceptance clause 1 read as the
// ruling states it: cause before consequence. A refused catalog file never
// enters the catalog, so what fails is the attempt to resolve a reference to
// what it declared — and the reader has to meet the refusal, with its own
// category and its own short code, before meeting that consequence.
func TestPull_SoleRefusalLeadsTheDiagnostic(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath, catalogRoot := writeValidWorkspace(t, root)
	recipePath := filepath.Join(catalogRoot, "recipes", "helm", "server.yaml")
	writeFixture(t, recipePath, refusedRecipe("server-recipe", "1.0.0", "helm"))

	err, rendered := renderedPullFailure(t, root, releasePath, catalogRoot)

	code, ok := cerrors.ShortCodeOf(envelope.ErrEntityEnvelope())
	if !ok {
		t.Fatal("the envelope-validation condition must be registered with a short code")
	}
	head := strings.SplitN(rendered, "\n", 2)[0]
	wantHead := "[" + string(cerrors.CatalogError) + " " + code + "]"
	if !strings.Contains(head, wantHead) {
		t.Errorf("the reader must meet the refusal first, with its own head %s; the head was:\n%s\n\nwhole diagnostic:\n%s",
			wantHead, head, rendered)
	}
	if !strings.Contains(head, recipePath) {
		t.Errorf("the leading line must name the refused file; the head was:\n%s", head)
	}
	if !strings.Contains(rendered, "missing required property: implements") {
		t.Errorf("the leading diagnostic must name what the author wrote wrong; rendered:\n%s", rendered)
	}

	// The consequence is demoted, not discarded: it is the context that says why
	// the refused document was needed at all.
	idxCause := strings.Index(rendered, "missing required property: implements")
	idxConsequence := strings.Index(rendered, `resolve entity "server-recipe:1.0.0"`)
	if idxConsequence < 0 {
		t.Fatalf("the resolution failure must still be reported beneath the refusal; rendered:\n%s", rendered)
	}
	if idxConsequence < idxCause {
		t.Errorf("the consequence appears before the cause; rendered:\n%s", rendered)
	}
	if !strings.Contains(rendered, "skipped at load") {
		t.Errorf("the reader must be told the file was refused at load rather than merely invalid; rendered:\n%s", rendered)
	}

	// Promotion changes what the reader meets first; it must not change what a
	// caller matches on.
	if !errors.Is(err, catalog.ErrUnscopedRecipeReference()) {
		t.Errorf("the failure's own condition must stay matchable at the boundary; got %v", err)
	}
}

// TestPull_RefusalPromotionIsExactOrAbsent is the safety property, and it is the
// reason the feature is acceptable at all. Naming the wrong file as the cause
// would be worse than naming none, because a reader acts on it — so where the
// record cannot single out one refusal, the diagnostic is exactly what it would
// be with no record at all: the failure leads and every refusal is listed
// beneath it.
//
// Each case below is a distinct way the record fails to single one out.
func TestPull_RefusalPromotionIsExactOrAbsent(t *testing.T) {
	for _, tc := range []struct {
		name string
		// setUp rewrites the catalog and returns the reference the manifest
		// should map the in-closure component to.
		setUp func(t *testing.T, catalogRoot string) string
	}{
		{
			name: "no refusal declares the identity the failing reference names",
			setUp: func(t *testing.T, catalogRoot string) string {
				writeFixture(t, filepath.Join(catalogRoot, "recipes", "helm", "broken.yaml"),
					refusedRecipe("broken-recipe", "1.0.0", "helm"))
				return "ghost-recipe:1.0.0"
			},
		},
		{
			name: "two refusals declare it, under different capabilities",
			setUp: func(t *testing.T, catalogRoot string) string {
				writeFixture(t, filepath.Join(catalogRoot, "recipes", "helm", "server.yaml"),
					refusedRecipe("server-recipe", "1.0.0", "helm"))
				writeFixture(t, filepath.Join(catalogRoot, "recipes", "compose", "server.yaml"),
					refusedRecipe("server-recipe", "1.0.0", "compose"))
				return "server-recipe:1.0.0"
			},
		},
		{
			name: "the one refusal declares that name at another version",
			setUp: func(t *testing.T, catalogRoot string) string {
				writeFixture(t, filepath.Join(catalogRoot, "recipes", "helm", "server.yaml"),
					refusedRecipe("server-recipe", "2.0.0", "helm"))
				return "server-recipe:1.0.0"
			},
		},
		{
			name: "the one refusal declares no identity at all",
			setUp: func(t *testing.T, catalogRoot string) string {
				writeFixture(t, filepath.Join(catalogRoot, "recipes", "helm", "server.yaml"),
					"apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nspec:\n  capability: helm\n")
				return "server-recipe:1.0.0"
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			releasePath, catalogRoot := writeValidWorkspace(t, root)
			reference := tc.setUp(t, catalogRoot)
			mapManifestTo(t, root, reference)

			_, rendered := renderedPullFailure(t, root, releasePath, catalogRoot)

			// No refusal leads, so none of them heads the diagnostic. The head
			// is asserted against the refusal's condition rather than against
			// the whole line, because the line also carries the leading
			// failure's own code — every diagnostic is headed, and heading is
			// not what promotion decides. What promotion decides is WHICH
			// failure the head belongs to.
			refusalCode, ok := cerrors.ShortCodeOf(envelope.ErrEntityEnvelope())
			if !ok {
				t.Fatal("the envelope-validation condition must be registered with a short code")
			}
			if head := strings.SplitN(rendered, "\n", 2)[0]; strings.Contains(head, refusalCode) {
				t.Errorf("no single refusal explains the failure, so no refusal may head the diagnostic; the head was:\n%s\n\nwhole diagnostic:\n%s",
					head, rendered)
			}
			// And the failure itself leads the report, with the refusals listed
			// beneath it — the order is the claim, so it is asserted as order.
			idxFailure := strings.Index(rendered, "resolve entity \""+reference+"\"")
			if idxFailure < 0 {
				t.Fatalf("the failure must still name the reference it could not resolve; rendered:\n%s", rendered)
			}
			switch idxRefusals := strings.Index(rendered, "catalog files skipped at load"); {
			case idxRefusals < 0:
				t.Errorf("every refusal must still be listed for the reader to weigh; rendered:\n%s", rendered)
			case idxRefusals < idxFailure:
				t.Errorf("the refusals are listed above the failure they could not explain; rendered:\n%s", rendered)
			}
			if !strings.Contains(rendered, "explained only by a refusal naming something this Release reaches") {
				t.Errorf("the note must still state the distinction the reader has to apply themselves; rendered:\n%s", rendered)
			}
		})
	}
}

// mapManifestTo rewrites the fixture Manifest so the in-closure component maps
// to reference, which is the reference a resolution failure then names.
func mapManifestTo(t *testing.T, root, reference string) {
	t.Helper()
	path := filepath.Join(root, "manifest.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	const anchor = "core/server: server-recipe:1.0.0"
	if !strings.Contains(string(data), anchor) {
		t.Fatalf("manifest mapping anchor %q not found; the fixture changed shape", anchor)
	}
	writeFixture(t, path, strings.Replace(string(data), anchor, "core/server: "+reference, 1))
}
