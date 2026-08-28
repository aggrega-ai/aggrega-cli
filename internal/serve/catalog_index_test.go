// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"
)

// writeCatalogFixture lays out a workspace holding several entities
// across several kinds, plus the two documents the index must skip.
//
// Two properties of the layout are load-bearing rather than incidental.
//
// Label coverage is deliberate: api and api-helm carry labels, db, mid,
// tls and app carry none, so the absent-labels case is exercised rather
// than assumed. api's labels are authored in reverse-alphabetical order,
// so a test reading the marshalled bytes sees a key order the author did
// not supply.
//
// The filenames are chosen so that sorting the files by path yields a
// DIFFERENT sequence from sorting the entities by kind and name — very
// nearly the reverse of it, and with the two Components ordered opposite
// ways round. Without that, path order and entity order coincide and an
// ordering assertion passes whether the index orders anything or not.
func writeCatalogFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"00-recipe.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: api-helm
  labels:
    provider: helm
`,
		"10-feature.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: tls
`,
		"20-component-b.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: db
`,
		"30-component-a.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: api
  description: the public edge
  labels:
    tier: edge
    component.aggrega.ai/type: service
`,
		"40-aggregate.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: app
  description: the storefront
`,
		"nested/50-component-c.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: mid
`,
		// Foreign YAML: no kind:, so it is not an entity.
		"values.yaml": `replicaCount: 2
image:
  tag: latest
`,
		// An aggrega kind with no metadata.name is not addressable, so
		// the index cannot report anything a caller could refer to.
		"90-unnamed.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  description: nameless
`,
	}
	for rel, content := range files {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}

// catalogFixtureEntityFiles are the fixture's entity documents in
// path-sorted order — deliberately not the order the index reports them
// in.
var catalogFixtureEntityFiles = []string{
	"00-recipe.yaml",
	"10-feature.yaml",
	"20-component-b.yaml",
	"30-component-a.yaml",
	"40-aggregate.yaml",
	"nested/50-component-c.yaml",
}

// catalogIndexOverWire drives one real aggrega/catalogIndex request
// through the server's message loop and returns the response Result as
// raw bytes plus the generically-decoded entity list.
//
// The result is decoded into map[string]any rather than into
// catalogIndexResult on purpose: decoding into the response type reads
// every field back through the same struct tags that wrote it, so a
// mistagged field round-trips cleanly and the assertion passes while no
// client ever receives the value. Indexing by the literal wire key is
// what makes the field names themselves the thing under test.
func catalogIndexOverWire(t *testing.T, srv *Server) ([]byte, []map[string]any) {
	t.Helper()
	in := bytes.NewBuffer(framedRequest(1, "aggrega/catalogIndex", map[string]any{}))
	out := &bytes.Buffer{}
	resp := runOneRequest(t, srv, in, out)
	if resp.Error != nil {
		t.Fatalf("catalogIndex returned an error: %+v", resp.Error)
	}
	var result map[string]any
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v (body: %s)", err, resp.Result)
	}
	raw, ok := result["entities"]
	if !ok {
		t.Fatalf(`result carries no "entities" key: %s`, resp.Result)
	}
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf(`"entities" is not an array: %s`, resp.Result)
	}
	entities := make([]map[string]any, 0, len(list))
	for i, item := range list {
		entity, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("entities[%d] is not an object: %s", i, resp.Result)
		}
		entities = append(entities, entity)
	}
	return resp.Result, entities
}

// TestCatalogIndexReportsWorkspaceEntitiesThroughTheRealDispatchPath is
// the acceptance assertion: an actual aggrega/catalogIndex request,
// routed through the daemon's own method table, comes back carrying kind,
// name, apiVersion, description, labels and source file for every
// workspace entity — read off the marshalled JSON by the literal key an
// integration author types.
func TestCatalogIndexReportsWorkspaceEntitiesThroughTheRealDispatchPath(t *testing.T) {
	srv := newTestServer(t, writeCatalogFixture(t), &bytes.Buffer{}, &bytes.Buffer{})
	body, entities := catalogIndexOverWire(t, srv)

	// Ordering is by kind, then name, then file. Note that this sequence
	// is not the fixture's path order: the Recipe is the first file and
	// the last entity, and the two Components appear in the opposite
	// order from their filenames. So this table asserts the index's own
	// ordering rather than the order its inputs arrived in.
	want := []struct {
		kind        string
		name        string
		description string
		file        string
		labels      map[string]string
	}{
		{"Aggregate", "app", "the storefront", "40-aggregate.yaml", map[string]string{}},
		{"Component", "api", "the public edge", "30-component-a.yaml", map[string]string{
			"tier":                      "edge",
			"component.aggrega.ai/type": "service",
		}},
		{"Component", "db", "", "20-component-b.yaml", map[string]string{}},
		{"Component", "mid", "", "nested/50-component-c.yaml", map[string]string{}},
		{"Feature", "tls", "", "10-feature.yaml", map[string]string{}},
		{"Recipe", "api-helm", "", "00-recipe.yaml", map[string]string{"provider": "helm"}},
	}
	if len(entities) != len(want) {
		t.Fatalf("got %d entities, want %d: %s", len(entities), len(want), body)
	}
	for i, w := range want {
		got := entities[i]
		// Every field is addressed by its literal wire name.
		if got["kind"] != w.kind {
			t.Errorf("entities[%d][kind] = %v, want %q", i, got["kind"], w.kind)
		}
		if got["name"] != w.name {
			t.Errorf("entities[%d][name] = %v, want %q", i, got["name"], w.name)
		}
		if got["apiVersion"] != "aggrega.ai/v1alpha1" {
			t.Errorf("entities[%d][apiVersion] = %v, want %q", i, got["apiVersion"], "aggrega.ai/v1alpha1")
		}
		if got["description"] != w.description {
			t.Errorf("entities[%d][description] = %v, want %q", i, got["description"], w.description)
		}
		if got["file"] != w.file {
			t.Errorf("entities[%d][file] = %v, want %q", i, got["file"], w.file)
		}
		labels, ok := got["labels"].(map[string]any)
		if !ok {
			// A document declaring no labels must still carry an object,
			// so a client iterates without a null check.
			t.Fatalf("entities[%d][labels] is not an object: %v", i, got["labels"])
		}
		if len(labels) != len(w.labels) {
			t.Errorf("entities[%d][labels] = %v, want %v", i, labels, w.labels)
		}
		for k, v := range w.labels {
			if labels[k] != v {
				t.Errorf("entities[%d][labels][%q] = %v, want %q", i, k, labels[k], v)
			}
		}
	}
}

// TestCatalogIndexSkipsForeignYAMLAndUnaddressableDocuments pins what
// keeps the index an entity index rather than a file listing: YAML with
// no kind: is not an entity, and an aggrega kind with no metadata.name is
// not addressable.
func TestCatalogIndexSkipsForeignYAMLAndUnaddressableDocuments(t *testing.T) {
	srv := newTestServer(t, writeCatalogFixture(t), &bytes.Buffer{}, &bytes.Buffer{})
	_, entities := catalogIndexOverWire(t, srv)
	for _, entity := range entities {
		if entity["file"] == "values.yaml" {
			t.Errorf("foreign YAML with no kind: reached the index: %v", entity)
		}
		if entity["file"] == "90-unnamed.yaml" {
			t.Errorf("document with no metadata.name reached the index: %v", entity)
		}
	}
}

// TestCatalogIndexIsByteStableAcrossRepeatedRequests is the determinism
// assertion, and it is discriminating rather than decorative because the
// nondeterminism it guards is live: the parse cache iterates in Go map
// order, so the traversal delivers cached documents in a different
// sequence on each call. The index is byte-stable only because it
// imposes a total order of its own.
//
// The cache is loaded with several documents precisely so that map order
// has something to permute. Removing the ordering from the projection
// makes this test fail on the marshalled bytes.
func TestCatalogIndexIsByteStableAcrossRepeatedRequests(t *testing.T) {
	root := writeCatalogFixture(t)
	srv := newTestServer(t, root, &bytes.Buffer{}, &bytes.Buffer{})

	// Populate the parse cache with the same documents the walk sees, so
	// both passes are live and the cache pass has several entries to
	// permute.
	for _, rel := range catalogFixtureEntityFiles {
		abs := filepath.Join(root, rel)
		content, err := os.ReadFile(abs)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(content, &doc); err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		srv.parseCache.Put(abs, &doc, content)
	}

	first, _ := catalogIndexOverWire(t, srv)
	for i := 2; i <= 12; i++ {
		again, _ := catalogIndexOverWire(t, srv)
		if !bytes.Equal(first, again) {
			t.Fatalf("request %d returned different bytes\nfirst: %s\nagain: %s", i, first, again)
		}
	}
}

// TestCatalogIndexReportsTheEditorBufferRatherThanDisk pins the
// traversal's freshness contract at the index: when the editor has sent
// content the file on disk does not yet carry, the index reports the
// buffer — and reports the file once, not once per pass.
//
// The rename is the case that makes path identity load-bearing.
// De-duplicating on the entity's declared name instead would report both
// the buffer's api-v2 and the disk's api, and one of those two entities
// exists in no state of the workspace.
func TestCatalogIndexReportsTheEditorBufferRatherThanDisk(t *testing.T) {
	root := writeCatalogFixture(t)
	srv := newTestServer(t, root, &bytes.Buffer{}, &bytes.Buffer{})

	buffer := `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: api-v2
  labels:
    tier: edge
`
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(buffer), &doc); err != nil {
		t.Fatalf("parse buffer: %v", err)
	}
	srv.parseCache.Put(filepath.Join(root, "30-component-a.yaml"), &doc, []byte(buffer))

	_, entities := catalogIndexOverWire(t, srv)
	var forFile []map[string]any
	for _, entity := range entities {
		if entity["file"] == "30-component-a.yaml" {
			forFile = append(forFile, entity)
		}
	}
	if len(forFile) != 1 {
		t.Fatalf("components/api.yaml reported %d times, want 1: %v", len(forFile), entities)
	}
	if forFile[0]["name"] != "api-v2" {
		t.Errorf("name = %v, want the buffer's %q", forFile[0]["name"], "api-v2")
	}
	for _, entity := range entities {
		if entity["name"] == "api" {
			t.Errorf("the superseded on-disk name is still reported: %v", entity)
		}
	}
}

// TestCatalogIndexExcludesDocumentsOutsideTheWorkspaceRoot pins that a
// parse-cache entry for a file the editor sent from outside the workspace
// is not workspace-local and does not reach the index — which also keeps
// absolute host paths off the wire.
func TestCatalogIndexExcludesDocumentsOutsideTheWorkspaceRoot(t *testing.T) {
	root := writeCatalogFixture(t)
	outside := filepath.Join(t.TempDir(), "elsewhere.yaml")
	srv := newTestServer(t, root, &bytes.Buffer{}, &bytes.Buffer{})

	content := `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: elsewhere
`
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	srv.parseCache.Put(outside, &doc, []byte(content))

	body, entities := catalogIndexOverWire(t, srv)
	for _, entity := range entities {
		if entity["name"] == "elsewhere" {
			t.Errorf("a document outside the workspace root reached the index: %v", entity)
		}
	}
	if bytes.Contains(body, []byte(outside)) {
		t.Errorf("an absolute path outside the workspace root reached the wire: %s", body)
	}
}

// TestCatalogIndexIsRoutedByTheMethodTable pins that the method is
// reachable at all. A handler present in the package but absent from the
// routing table compiles, and every direct-call test of it passes, while
// no client can ever invoke it — so this asserts the negative case too:
// an unrouted sibling name is rejected as unknown.
func TestCatalogIndexIsRoutedByTheMethodTable(t *testing.T) {
	srv := newTestServer(t, writeCatalogFixture(t), &bytes.Buffer{}, &bytes.Buffer{})

	in := bytes.NewBuffer(framedRequest(1, "aggrega/catalogIndex", map[string]any{}))
	out := &bytes.Buffer{}
	if resp := runOneRequest(t, srv, in, out); resp.Error != nil {
		t.Fatalf("aggrega/catalogIndex is not routed: %+v", resp.Error)
	}

	// Positive control for the assertion above: the same harness DOES
	// report an error for a method the table does not carry, so the
	// green above reflects routing rather than an unchecked field.
	in = bytes.NewBuffer(framedRequest(2, "aggrega/catalogIndexes", map[string]any{}))
	out = &bytes.Buffer{}
	resp := runOneRequest(t, srv, in, out)
	if resp.Error == nil {
		t.Fatal("an unrouted method name was accepted, so the routing assertion above proves nothing")
	}
	if resp.Error.Code != jsonRPCMethodNotFound {
		t.Errorf("unrouted method error code = %d, want %d", resp.Error.Code, jsonRPCMethodNotFound)
	}
}

// TestCatalogIndexOnAnEmptyWorkspaceReturnsAnEmptyArray pins that the
// collection is an array on the wire even when the workspace declares no
// entities, so a client iterates it without a null check.
func TestCatalogIndexOnAnEmptyWorkspaceReturnsAnEmptyArray(t *testing.T) {
	srv := newTestServer(t, t.TempDir(), &bytes.Buffer{}, &bytes.Buffer{})
	body, entities := catalogIndexOverWire(t, srv)
	if len(entities) != 0 {
		t.Fatalf("empty workspace reported %d entities: %s", len(entities), body)
	}
	if !bytes.Contains(body, []byte(`"entities":[]`)) {
		t.Errorf("empty result is not an empty array on the wire: %s", body)
	}
}
