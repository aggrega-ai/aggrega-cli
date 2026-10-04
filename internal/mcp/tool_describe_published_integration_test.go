// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

// The describe tool reading an entity that IS published, over both transports,
// against a REAL HTTP OCI registry.
//
// Build-tagged `integration` so the fast unit run excludes it and the test-only
// go-containerregistry dependency stays out of the production binary's import
// graph. What it adds over the untagged proofs beside it is the registry: those
// are hermetic and see only refusals, so nothing there shows a coordinate that
// exists being answered with what was published at it. Here the entities are
// published through the product's own bundling and publication, and read back
// through the same egress gate and repository construction a user's call takes.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// publishedRegistry is an in-process registry on loopback and the number of
// requests it has answered, so a proof can show which registry a call reached.
type publishedRegistry struct {
	host     string
	requests atomic.Int64
}

func startCountingRegistry(t *testing.T) *publishedRegistry {
	t.Helper()
	r := &publishedRegistry{}
	inner := registry.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.requests.Add(1)
		inner.ServeHTTP(w, req)
	}))
	t.Cleanup(srv.Close)
	r.host = strings.TrimPrefix(srv.URL, "http://")
	return r
}

// publishedFixture is what was put at each coordinate: the reference, and the
// entity document a reader must hand back for it.
type publishedFixture struct {
	reference string
	document  []byte
	archive   bool
}

// publishDescribeFixtures publishes a Component, which carries nothing beside it
// and ships as its document, and a Recipe, which carries a file beside the entity
// and ships as an archive, to the registry at host. Both go through oci.Bundle,
// the packing every publication uses, so the archive is the shape a real
// publication makes.
func publishDescribeFixtures(t *testing.T, host string) (component, recipe publishedFixture) {
	t.Helper()
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
	componentDoc := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: test/svc\n  version: \"1.0.0\"\n" +
		"  description: A service published for a reader.\nspec:\n  schema:\n    infra:\n      port: { type: integer, default: 8080 }\n"
	recipeDoc := "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: svc\n  version: \"1.0.0\"\n" +
		"spec:\n  capability: helm\n  implements: test/svc\n"
	componentPath := write("component/test/svc/1.0.0/component.yaml", componentDoc)
	recipePath := write("recipe/helm/svc/1.0.0/recipe.yaml", recipeDoc)
	write("recipe/helm/svc/1.0.0/notes.yaml", "notes: co-located beside the entity, and not an entity\n")

	publish := func(kind, path, repository string, carried ...string) string {
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
		return "oci://" + host + "/" + repository + ":1.0.0"
	}
	component = publishedFixture{publish("Component", componentPath, "component/test/svc"), []byte(componentDoc), false}
	recipe = publishedFixture{publish("Recipe", recipePath, "recipe/helm/svc", "notes.yaml"), []byte(recipeDoc), true}
	return component, recipe
}

// assertDescribedAsPublished decodes a describe answer and holds it to what was
// published: the reference read, the kind, whether it was unpacked, and the
// document byte for byte.
func assertDescribedAsPublished(t *testing.T, what string, raw json.RawMessage, want publishedFixture, kind string) {
	t.Helper()
	var call callToolResult
	if err := json.Unmarshal(raw, &call); err != nil {
		t.Fatalf("%s: the result is not a tool result: %v", what, err)
	}
	if call.IsError {
		t.Fatalf("%s: describe failed rather than reading the coordinate: %+v", what, call.Content)
	}
	var got describePublishedPayload
	if err := json.Unmarshal(call.StructuredContent, &got); err != nil {
		t.Fatalf("%s: the structured answer does not decode: %v (%s)", what, err, call.StructuredContent)
	}
	if got.Reference != want.reference || got.Kind != kind || got.Archive != want.archive {
		t.Errorf("%s: answered reference %q kind %q archive %v; want %q %q %v",
			what, got.Reference, got.Kind, got.Archive, want.reference, kind, want.archive)
	}
	if got.Document != string(want.document) {
		t.Errorf("%s: the document is not the published one byte for byte:\n got %q\nwant %q", what, got.Document, want.document)
	}
	if !strings.HasPrefix(got.ManifestDigest, "sha256:") {
		t.Errorf("%s: manifest digest %q names no digest", what, got.ManifestDigest)
	}
}

// TestIntegration_DescribeReadsAPublishedEntityOverStandardInputAndOutput drives
// the tool through the standard-input transport's own message loop — the framing
// a launching client speaks — for a document-shaped coordinate and an
// archive-shaped one, and holds each answer to what was published.
//
// The workspace's default registry is the in-process one, so the documentation
// acquisition the run makes at start reaches loopback and nothing else.
func TestIntegration_DescribeReadsAPublishedEntityOverStandardInputAndOutput(t *testing.T) {
	pinDescribeEnvironment(t)
	reg := startCountingRegistry(t)
	component, recipe := publishDescribeFixtures(t, reg.host)

	ws := writeDescribeWorkspace(t, "workspace: {}\noffline: false\ndefault_registry: \""+reg.host+"\"\n")
	s := &server{out: io.Discard, logw: io.Discard, startDir: ws, transport: transportStdio, docsBundleVersion: "0.0.0-fixture"}
	line := func(id int, reference string) string {
		return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"describe","arguments":{"name":%q}}}`,
			id, reference)
	}
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18",` +
		`"capabilities":{},"clientInfo":{"name":"integration","version":"0"}}}`
	initialized := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	resps := runServer(t, s, initialize, initialized, line(2, component.reference), line(3, recipe.reference))

	assertDescribedAsPublished(t, "a document-shaped coordinate", responseFor(t, resps, 2).Result, component, "Component")
	assertDescribedAsPublished(t, "an archive-shaped coordinate", responseFor(t, resps, 3).Result, recipe, "Recipe")
}

// TestIntegration_DescribeOverAListenerReadsAConfiguredRegistryOnly drives the
// tool through a real listener, over a real connection, as a caller holding the
// read scope. The served workspace's default registry is one in-process
// registry; a second one, carrying the same entity, is configured nowhere.
//
// The configured one is read and answered with what was published at it, and
// the request count shows it was reached. The unconfigured one is refused,
// naming its host, and its request count is unchanged across the call — so the
// refusal came before the read rather than after it.
func TestIntegration_DescribeOverAListenerReadsAConfiguredRegistryOnly(t *testing.T) {
	pinDescribeEnvironment(t)
	configured, unlisted := startCountingRegistry(t), startCountingRegistry(t)
	component, recipe := publishDescribeFixtures(t, configured.host)
	unlistedComponent, _ := publishDescribeFixtures(t, unlisted.host)

	ws := writeDescribeWorkspace(t, "workspace: {}\noffline: false\ndefault_registry: \""+configured.host+"\"\n")
	s := &server{out: io.Discard, logw: io.Discard, startDir: ws}
	h, err := newHTTPHandler(context.Background(), s, defaultTransportConfig())
	if err != nil {
		t.Fatalf("newHTTPHandler: %v", err)
	}
	h.auth = acceptAllAuthenticator{}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	f := &httpFixture{ts: ts, h: h}

	describe := func(id int, reference string) answer {
		got := f.post(t, modernHeaders("tools/call", "describe"), modernRequest(t, id, "tools/call", map[string]any{
			"name": "describe", "arguments": map[string]any{"name": reference},
		}))
		if got.status != http.StatusOK || got.frame.Error != nil {
			t.Fatalf("describe %s over the listener: status %d, body %s", reference, got.status, got.raw)
		}
		return got
	}

	for i, row := range []struct {
		what string
		want publishedFixture
		kind string
	}{
		{"a document-shaped coordinate on the configured registry", component, "Component"},
		{"an archive-shaped coordinate on the configured registry", recipe, "Recipe"},
	} {
		before := configured.requests.Load()
		got := describe(10+i, row.want.reference)
		assertDescribedAsPublished(t, row.what, got.frame.Result, row.want, row.kind)
		if configured.requests.Load() == before {
			t.Errorf("%s: the configured registry answered no request, so the answer did not come from it", row.what)
		}
	}

	before := unlisted.requests.Load()
	got := describe(20, unlistedComponent.reference)
	var call callToolResult
	if err := json.Unmarshal(got.frame.Result, &call); err != nil {
		t.Fatalf("the result is not a tool result: %v", err)
	}
	if !call.IsError || len(call.Content) == 0 ||
		!strings.Contains(call.Content[0].Text, unlisted.host+" "+admissionRefusalText) {
		t.Errorf("a registry the workspace configures nowhere was not refused by name: %s", got.raw)
	}
	if after := unlisted.requests.Load(); after != before {
		t.Errorf("the refused registry answered %d request(s) during the call; a refusal contacts nothing", after-before)
	}
}
