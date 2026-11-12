// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build integration

// The daemon's aggrega/entityDefinition reading an entity that IS published,
// against a REAL HTTP OCI registry.
//
// Build-tagged `integration` so the fast unit run excludes it and the test-only
// go-containerregistry dependency stays out of the production binary's import
// graph. The untagged proof beside it drives an offline workspace and sees only
// the refusal; this one publishes through the product's own bundling and reads
// the coordinate back through the production dispatch and serializer path.
package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"

	"github.com/aggrega-ai/aggrega-cli/internal/hometest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestIntegration_EntityDefinitionReadsAPublishedEntity publishes a Component,
// which carries nothing beside it and ships as its document, and a Recipe,
// which carries a file beside the entity and ships as an archive, and asks the
// daemon for each
// by its coordinate. Each answer must be the published entity document byte for
// byte — unpacked for the archive — carrying the published facts, and naming no
// file on this machine. A coordinate nothing is published at is the negative,
// refused with entity-not-resolvable naming the condition.
//
// Not parallel: it points the per-user homes at a scratch directory for its
// duration, because constructing a registry client reads the credential file.
func TestIntegration_EntityDefinitionReadsAPublishedEntity(t *testing.T) {
	hometest.Isolate(t)
	srvRegistry := httptest.NewServer(registry.New())
	t.Cleanup(srvRegistry.Close)
	host := strings.TrimPrefix(srvRegistry.URL, "http://")

	dir := t.TempDir()
	write := func(rel, body string) string {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
		return p
	}
	componentDoc := "apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  name: test/svc\n  version: \"1.0.0\"\n" +
		"spec:\n  schema:\n    infra:\n      port: { type: integer, default: 8080 }\n"
	recipeDoc := "apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: svc\n  version: \"1.0.0\"\n" +
		"spec:\n  capability: helm\n  implements: test/svc\n"
	componentPath := write("component/test/svc/1.0.0/component.yaml", componentDoc)
	recipePath := write("recipe/helm/svc/1.0.0/recipe.yaml", recipeDoc)
	write("recipe/helm/svc/1.0.0/notes.yaml", "notes: co-located beside the entity, and not an entity\n")

	publish := func(kind, path, repository string, carried ...string) (string, string) {
		content, mediaType, err := oci.Bundle(kind, path, carried)
		if err != nil {
			t.Fatalf("bundle %s: %v", kind, err)
		}
		repo, err := oci.NewRemoteRepository(host+"/"+repository, &http.Client{})
		if err != nil {
			t.Fatalf("repository for %s: %v", kind, err)
		}
		if _, err := oci.Push(context.Background(), repo, "1.0.0", mediaType, content, nil); err != nil {
			t.Fatalf("publish %s: %v", kind, err)
		}
		return "oci://" + host + "/" + repository + ":1.0.0", mediaType
	}
	componentRef, componentType := publish("Component", componentPath, "component/test/svc")
	recipeRef, recipeType := publish("Recipe", recipePath, "recipe/helm/svc", "notes.yaml")

	ws := t.TempDir()
	srv := newTestServer(t, ws, &strings.Reader{}, &strings.Builder{})
	srv.wsConfig = workspaceconfig.Default()

	for _, row := range []struct {
		what, reference, kind, document, mediaType string
		archive                                    bool
	}{
		{"a document-shaped coordinate", componentRef, "Component", componentDoc, componentType, false},
		{"an archive-shaped coordinate", recipeRef, "Recipe", recipeDoc, recipeType, true},
	} {
		got := decodeEntityDefinition(t, callEntityDefinition(t, srv, map[string]any{"name": row.reference}))
		if got.Kind != row.kind || got.Definition != row.document || got.AuthoredLength != len(row.document) {
			t.Errorf("%s: answered kind %q, a %d-byte definition (authoredLength %d); want %q and the published "+
				"document byte for byte:\n got %q\nwant %q",
				row.what, got.Kind, len(got.Definition), got.AuthoredLength, row.kind, got.Definition, row.document)
		}
		if got.File != "" {
			t.Errorf("%s: names the file %q, but an entity read from a registry has no document on this machine", row.what, got.File)
		}
		if got.Published == nil {
			t.Fatalf("%s: the answer carries no published facts, so it reads as a catalog resolution", row.what)
		}
		if got.Published.Reference != row.reference || got.Published.Archive != row.archive ||
			got.Published.MediaType != row.mediaType || !strings.HasPrefix(got.Published.ManifestDigest, "sha256:") {
			t.Errorf("%s: published facts %+v; want reference %q, archive %v, media type %q and a manifest digest",
				row.what, *got.Published, row.reference, row.archive, row.mediaType)
		}
	}

	missing := callEntityDefinition(t, srv, map[string]any{"name": "oci://" + host + "/component/test/svc:9.9.9"})
	if missing.Error == nil || missing.Error.Code != errEntityNotResolvable || !strings.Contains(missing.Error.Message, "E0137") {
		t.Errorf("a coordinate nothing is published at: %+v; want entity-not-resolvable naming E0137", missing.Error)
	}
}
