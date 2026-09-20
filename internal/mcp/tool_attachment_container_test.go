// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// attachmentContainerWorkspace lays out a workspace carrying the two authoring
// shapes a component can be written in, each with its own Release over a shared
// catalog.
//
// It is built here rather than taken from the committed fixtures because these
// cases WRITE: the applied-edit proof edits the Aggregate it was answered about,
// and a shared fixture edited in place would leak into every other test that
// reads it.
//
// The two shapes are mutually exclusive within one Aggregate — a document
// declares top-level components or named subsystems, never both — so covering
// one covers about half the authoring styles in use. The subsystem side nests
// two levels deep, which is where an answer that joined the enclosing scope in
// as a single segment would name a key no file contains.
func attachmentContainerWorkspace(t *testing.T) (root, catalogDir, nestedRelease, flatRelease string) {
	t.Helper()
	root = t.TempDir()
	catalogDir = filepath.Join(root, "catalog")

	write := func(rel, content string) {
		t.Helper()
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("create the fixture directory: %v", err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write the fixture: %v", err)
		}
	}

	write("catalog/components/edge.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: demo/edge
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    provides:
      host: { type: string, required: true }
`)
	write("catalog/recipes/edge.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: demo-edge-synthesized
  version: "1.0.0"
spec:
  capability: helm
  implements: demo/edge
  provides:
    host: "{{ .context.instance_id }}"
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: demo-edge:latest
`)
	write("catalog/plugins/helm-renderer.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: helm
  pluginType: provider
  version: "1.0.0"
spec:
  abiVersion: v1alpha1
  provider:
    capabilities:
      - helm
  exports:
    - name: render_node
  runtime:
    maxMemoryPages: 4096
    timeoutMs: 300000
`)
	write("app-manifest.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: app-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        demo/edge: demo-edge-synthesized
`)
	write("nested-agg.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: nested-agg
  version: "1.0.0"
spec:
  subsystems:
    billing:
      subsystems:
        payments:
          components:
            ledger:
              type: "demo/edge:1.0.0"
`)
	write("nested-release.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: nested-release
  environment: production
  version: "1.0.0"
spec:
  aggregate: nested-agg
  manifest: app-manifest
  boundaries:
    app:
      directory: ./dist/nested
      components:
        ledger: {}
`)
	write("flat-agg.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: flat-agg
  version: "1.0.0"
spec:
  components:
    checkout:
      type: "demo/edge:1.0.0"
`)
	write("flat-release.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: flat-release
  environment: production
  version: "1.0.0"
spec:
  aggregate: flat-agg
  manifest: app-manifest
  boundaries:
    app:
      directory: ./dist/flat
      components:
        checkout: {}
`)

	nestedRelease = filepath.Join(root, "nested-release.yaml")
	flatRelease = filepath.Join(root, "flat-release.yaml")
	for _, rel := range []string{nestedRelease, flatRelease} {
		lf := &lockfile.Lockfile{
			Version:     1,
			Release:     "./" + filepath.Base(rel),
			GeneratedAt: "2026-06-03T10:00:00Z",
			Entities:    []lockfile.Entity{{URI: "demo/edge:1.0.0", ResolvedVersion: "1.0.0"}},
		}
		if err := lockfile.WriteLockfile(lockfile.PathFor(rel), lf); err != nil {
			t.Fatalf("WriteLockfile: %v", err)
		}
	}
	return root, catalogDir, nestedRelease, flatRelease
}

// TestTheAttachmentContainerToolNamesTheSiteForEveryAuthoringShape drives the
// real tool over both shapes and pins the document and the container it names.
//
// The nested case is the one that does not work without this surface: the graph
// keys that component as "ledger", carrying no trace of the two scopes enclosing
// it, so a caller composing a container from what it holds would name
// .spec.components.ledger.features — a path that reads correctly and addresses
// nothing.
func TestTheAttachmentContainerToolNamesTheSiteForEveryAuthoringShape(t *testing.T) {
	root, catalogDir, nested, flat := attachmentContainerWorkspace(t)

	for _, tc := range []struct {
		name     string
		release  string
		node     string
		wantFile string
		wantPath []string
	}{
		{
			name:     "a component two subsystems deep",
			release:  nested,
			node:     "ledger",
			wantFile: "nested-agg.yaml",
			wantPath: []string{"spec", "subsystems", "billing", "subsystems", "payments", "components", "ledger", "features"},
		},
		{
			name:     "a component at the top level",
			release:  flat,
			node:     "checkout",
			wantFile: "flat-agg.yaml",
			wantPath: []string{"spec", "components", "checkout", "features"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &server{out: io.Discard, logw: io.Discard, serverVersion: "test", startDir: root}

			var got attachmentContainerPayload
			decodeStructured(t, callDiscoveryTool(t, s, "attachment-container", map[string]any{
				"workspace_root": root,
				"catalog_dirs":   []string{catalogDir},
				"graph_node":     tc.node,
				"release":        tc.release,
			}), &got)

			if got.WorkspaceRoot != root {
				t.Errorf("workspace_root = %q, want %q", got.WorkspaceRoot, root)
			}
			if got.Node != tc.node {
				t.Errorf("node = %q, want %q", got.Node, tc.node)
			}
			if got.File != tc.wantFile {
				t.Errorf("file = %q, want %q", got.File, tc.wantFile)
			}
			if strings.Join(got.Container, "\x00") != strings.Join(tc.wantPath, "\x00") {
				t.Errorf("container = %v, want %v", got.Container, tc.wantPath)
			}
		})
	}
}

// TestTheAttachmentContainerAndInsertionPointToolsCompose is the applied-edit
// proof on this surface, and it is the join the two tools exist to make: the
// first answer's file and container go into the second call verbatim, the
// composed text is spliced in at the position it reports, and the result is read
// back through the compiler's own Aggregate decode.
//
// Nothing structural is composed between the two calls. A test that assembled
// the container itself would prove the pair composes for a caller that did not
// need the first tool.
func TestTheAttachmentContainerAndInsertionPointToolsCompose(t *testing.T) {
	for _, tc := range []struct {
		name          string
		useNested     bool
		node          string
		wantScopePath string
	}{
		{name: "a component two subsystems deep", useNested: true, node: "ledger", wantScopePath: "billing.payments"},
		{name: "a component at the top level", useNested: false, node: "checkout", wantScopePath: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, catalogDir, nested, flat := attachmentContainerWorkspace(t)
			release := flat
			if tc.useNested {
				release = nested
			}
			s := &server{out: io.Discard, logw: io.Discard, serverVersion: "test", startDir: root}

			var where attachmentContainerPayload
			decodeStructured(t, callDiscoveryTool(t, s, "attachment-container", map[string]any{
				"workspace_root": root,
				"catalog_dirs":   []string{catalogDir},
				"graph_node":     tc.node,
				"release":        release,
			}), &where)

			position := insertionPayloadFrom(t, s, insertionPointArgs{
				File:          where.File,
				Container:     where.Container,
				WorkspaceRoot: root,
			})

			abs := filepath.Join(root, where.File)
			before, err := os.ReadFile(abs)
			if err != nil {
				t.Fatalf("read the document: %v", err)
			}
			edited := applyInsertionPayload(t, string(before), position,
				[]string{"audit:", "  type: demo/audit"})

			// The assertion the carrier check cannot make: a container naming a
			// scope the document does not carry is CREATED by the insertion
			// rather than refused, so the attachment then lands on an instance
			// reading correctly inside a scope nobody authored. That case parses,
			// carries exactly one attachment, and reports the expected scope
			// path. It moves the census, and nothing an authoring edit
			// legitimately does moves it.
			if was, now := instanceCensus(t, string(before)), instanceCensus(t, edited); was != now {
				t.Fatalf("the edit changed which components the document declares, so the container named a "+
					"place the document did not carry:\n  before: %s\n  after:  %s\n---\n%s---",
					was, now, edited)
			}

			carriers := attachmentCarriers(t, edited, "audit")
			if len(carriers) != 1 {
				t.Fatalf("%d component instance(s) carry the attachment after the edit, want exactly 1: %v\n---\n%s---",
					len(carriers), carriers, edited)
			}
			if want := tc.wantScopePath + "/" + tc.node; carriers[0] != want {
				t.Fatalf("the attachment landed on %q, want %q\n---\n%s---", carriers[0], want, edited)
			}
		})
	}
}

// instanceCensus renders every component instance a document declares, through
// the compiler's own walk, as one comparable string. A container naming a scope
// the document does not carry moves it; an authoring edit that only adds an
// attachment does not.
func instanceCensus(t *testing.T, content string) string {
	t.Helper()
	var agg types.Aggregate
	if err := yaml.Unmarshal([]byte(content), &agg); err != nil {
		t.Fatalf("the document does not decode as an Aggregate: %v\n---\n%s---", err, content)
	}
	var out []string
	for _, ref := range types.Normalize(&agg).ComponentInstances() {
		path := ref.SubsystemPath
		if ref.Synthesized {
			path = ""
		}
		out = append(out, path+"/"+ref.Name)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// attachmentCarriers re-reads a document through the compiler's own Aggregate
// decode and normalized instance walk, and returns every instance carrying an
// attachment under the given name, as "<scope path>/<instance name>".
//
// Reading it back through the compiler rather than through a text match is what
// makes this a proof about the document: a decode that fails and an attachment
// that landed under a sibling component are each visible here and neither is
// visible in a substring search.
func attachmentCarriers(t *testing.T, content, instance string) []string {
	t.Helper()
	var agg types.Aggregate
	if err := yaml.Unmarshal([]byte(content), &agg); err != nil {
		t.Fatalf("the edited document no longer decodes as an Aggregate: %v\n---\n%s---", err, content)
	}
	var out []string
	for _, ref := range types.Normalize(&agg).ComponentInstances() {
		if _, ok := ref.Instance.Features.Get(instance); !ok {
			continue
		}
		path := ref.SubsystemPath
		if ref.Synthesized {
			path = ""
		}
		out = append(out, path+"/"+ref.Name)
	}
	return out
}

// TestTheAttachmentContainerToolRefusesWhatItCannotName drives each refusal,
// with an answerable call first as the positive control.
func TestTheAttachmentContainerToolRefusesWhatItCannotName(t *testing.T) {
	root, catalogDir, nested, _ := attachmentContainerWorkspace(t)
	s := &server{out: io.Discard, logw: io.Discard, serverVersion: "test", startDir: root}

	for _, tc := range []struct {
		name    string
		args    map[string]any
		refuses string
	}{
		{
			name: "a node the graph carries",
			args: map[string]any{
				"workspace_root": root, "catalog_dirs": []string{catalogDir},
				"graph_node": "ledger", "release": nested,
			},
			refuses: "",
		},
		{
			name:    "no node at all",
			args:    map[string]any{"workspace_root": root, "release": nested},
			refuses: "requires a 'graph_node'",
		},
		{
			name:    "a node with no Release to look it up in",
			args:    map[string]any{"workspace_root": root, "graph_node": "ledger"},
			refuses: "requires a 'release'",
		},
		{
			name: "a node the graph does not carry",
			args: map[string]any{
				"workspace_root": root, "catalog_dirs": []string{catalogDir},
				"graph_node": "nonesuch", "release": nested,
			},
			refuses: "carries no node with that id",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := callDiscoveryTool(t, s, "attachment-container", tc.args)
			switch {
			case tc.refuses == "" && res.IsError:
				t.Fatalf("an answerable call was refused: %s", res.Content[0].Text)
			case tc.refuses == "":
				return
			case !res.IsError:
				t.Fatalf("a call that should have been refused was answered: %s", res.Content[0].Text)
			case !strings.Contains(res.Content[0].Text, tc.refuses):
				t.Fatalf("the refusal does not say what was wrong: want %q in %q",
					tc.refuses, res.Content[0].Text)
			}
		})
	}
}

// TestTheAttachmentContainerToolIsReachableFromTheCapabilityListing pins that
// the tool is discoverable and invocable under the name the operation table
// gives, rather than merely present in a registry. A tool a client cannot learn
// of is a tool nothing calls, and this one sits in the tail rather than in the
// set a caller meets first.
func TestTheAttachmentContainerToolIsReachableFromTheCapabilityListing(t *testing.T) {
	s := newTestServer(t)

	listing := indexOf(t, s)
	var found *capabilityProjection
	for i := range listing.Index {
		if listing.Index[i].Name == "attachment-container" {
			found = &listing.Index[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("the capability index does not surface attachment-container; it surfaced %v",
			capabilityNames(listing))
	}
	if strings.TrimSpace(found.Summary) == "" {
		t.Errorf("attachment-container is indexed with no purpose, so a caller reads a blank row where it "+
			"chooses from: %+v", *found)
	}

	// Discovery is only useful if the definition is one call away, so the
	// argument schema is asked for by name and has to arrive.
	defined := definitionsOf(t, s, "attachment-container")
	if len(defined.Capabilities) != 1 || len(defined.Capabilities[0].InputSchema) == 0 {
		t.Errorf("naming attachment-container returned %+v, not the argument schema a caller composes a call from",
			defined.Capabilities)
	}

	for _, kind := range []string{"aggregate", "component", "feature"} {
		scoped := callTool(t, s, describeCapabilitiesName, describeCapabilitiesArgs{Kind: kind})
		var narrowed describeCapabilitiesResult
		if err := json.Unmarshal(scoped.StructuredContent, &narrowed); err != nil {
			t.Fatalf("decode the %s listing: %v", kind, err)
		}
		var present bool
		for _, c := range narrowed.Index {
			if c.Name == "attachment-container" {
				present = true
				break
			}
		}
		if !present {
			t.Errorf("the %s-scoped capability listing does not surface attachment-container", kind)
		}
	}
}

// TestTheAttachmentContainerToolReportsSegmentsRatherThanAJoinedPath pins the
// wire shape the whole capability turns on.
//
// A joined path would be read back apart by the caller, and the two joins a
// caller would have to undo — a scope path and a qualified node identifier — use
// the same separator as a legal character in an authored name. So a joined
// answer would reintroduce, one call later, exactly the ambiguity this surface
// exists to remove. The segments are the answer; nothing here may collapse them.
func TestTheAttachmentContainerToolReportsSegmentsRatherThanAJoinedPath(t *testing.T) {
	root, catalogDir, nested, _ := attachmentContainerWorkspace(t)
	s := &server{out: io.Discard, logw: io.Discard, serverVersion: "test", startDir: root}

	res := callDiscoveryTool(t, s, "attachment-container", map[string]any{
		"workspace_root": root,
		"catalog_dirs":   []string{catalogDir},
		"graph_node":     "ledger",
		"release":        nested,
	})
	var raw struct {
		Container json.RawMessage `json:"container"`
	}
	if err := json.Unmarshal(res.StructuredContent, &raw); err != nil {
		t.Fatalf("decode the payload: %v\n%s", err, res.StructuredContent)
	}
	var segments []string
	if err := json.Unmarshal(raw.Container, &segments); err != nil {
		t.Fatalf("the container is not carried as a list of segments: %v (%s)", err, raw.Container)
	}
	if len(segments) < 2 {
		t.Fatalf("the container arrived as %d segment(s); a joined path would decode as one", len(segments))
	}
	for _, seg := range segments {
		if strings.Contains(seg, ".") {
			t.Errorf("the segment %q carries a separator, so a caller splitting the answer would "+
				"reach a key the document does not contain", seg)
		}
	}
}

// TestTheAttachmentContainerToolIsInsertionPointReady pins the one thing the two
// answers have to share to compose: the container the first reports is accepted
// by the second unchanged. Both are driven, so a change to either surface's
// vocabulary fails here rather than at whichever client hit it first.
func TestTheAttachmentContainerToolIsInsertionPointReady(t *testing.T) {
	root, catalogDir, nested, _ := attachmentContainerWorkspace(t)
	s := &server{out: io.Discard, logw: io.Discard, serverVersion: "test", startDir: root}

	var where attachmentContainerPayload
	decodeStructured(t, callDiscoveryTool(t, s, "attachment-container", map[string]any{
		"workspace_root": root,
		"catalog_dirs":   []string{catalogDir},
		"graph_node":     "ledger",
		"release":        nested,
	}), &where)

	position := insertionPayloadFrom(t, s, insertionPointArgs{
		File:          where.File,
		Container:     where.Container,
		WorkspaceRoot: root,
	})
	if strings.Join(position.Container, "\x00") != strings.Join(where.Container, "\x00") {
		t.Fatalf("the insertion point echoes a different container from the one it was handed:\n"+
			"  handed:  %v\n  echoed:  %v", where.Container, position.Container)
	}
	if position.File != where.File {
		t.Errorf("the insertion point reports file %q for the document %q was reported at",
			position.File, where.File)
	}
	// The container is not there yet, so the second answer says which keys have
	// to be written first. An empty list here would mean the fixture already
	// carried the block and the composition case went untested.
	if len(position.MissingContainerKeys) == 0 {
		t.Error("the document already carries the attachment block, so this case exercises no creation")
	}
	if position.EntryShape != yamlpos.EntryShapeUnknown {
		t.Errorf("entry shape = %q, want %q for a container the document does not carry",
			position.EntryShape, yamlpos.EntryShapeUnknown)
	}
}
