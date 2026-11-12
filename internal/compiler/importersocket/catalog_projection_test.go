// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importersocket_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/importersocket"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// The fixture catalog the projection tests load. It holds three versions of one
// model Component (the projection must describe the highest non-prerelease one),
// an agent Component declaring two sockets out of name order, and a Component
// extending the agent (it must carry the agent's pillars, labels and sockets as
// well as its own). Every values pillar carries a default and one model version
// an infra pillar, so the payload can be shown to carry neither.
var projectionFixture = map[string]string{
	"internal-llm-1.0.0.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: acme/ai-model/internal-llm
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: resource
    component.aggrega.ai/category: ai-model
    model.aggrega.ai/release: one-zero
spec:
  schema:
    values:
      model_name: { type: string, default: "zero-model-default" }
`,
	"internal-llm-1.2.0.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: acme/ai-model/internal-llm
  version: "1.2.0"
  labels:
    component.aggrega.ai/type: resource
    component.aggrega.ai/category: ai-model
    model.aggrega.ai/release: one-two
spec:
  schema:
    values:
      temperature: { type: number, default: 0.25 }
      model_name: { type: string, default: "projection-must-not-carry-this-default" }
      max_tokens: { type: integer }
    infra:
      replicas: { type: integer, default: 2 }
    provides:
      model_id: { type: string }
      api_base: { type: string }
`,
	"internal-llm-2.0.0-rc.1.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: acme/ai-model/internal-llm
  version: "2.0.0-rc.1"
  labels:
    component.aggrega.ai/type: resource
    component.aggrega.ai/category: ai-model
    model.aggrega.ai/release: two-rc
spec:
  schema:
    values:
      prerelease_only: { type: string }
`,
	"support-agent.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: acme/ai-agent/support-agent
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
    component.aggrega.ai/category: ai-agent
spec:
  usesSchema:
    tools:
      selector: "component.aggrega.ai/category=ai-tool"
    model:
      selector: "component.aggrega.ai/category=ai-model"
      required: true
      inject: [env]
  schema:
    values:
      system_prompt: { type: string, default: "agent-prompt-default-not-projected" }
      model: { type: string }
    provides:
      endpoint: { type: string }
`,
	"support-agent-plus.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: acme/ai-agent/support-agent-plus
  version: "0.4.0"
  labels:
    component.aggrega.ai/type: server
    tier: premium
spec:
  extends: acme/ai-agent/support-agent
  usesSchema:
    memory:
      selector: "component.aggrega.ai/category=datastore"
  schema: inherited
`,
}

// wantProjection is the projection of projectionFixture, field by field.
var wantProjection = []abi.CatalogComponent{
	{
		Name:    "acme/ai-agent/support-agent",
		Version: "1.0.0",
		Labels: map[string]string{
			"component.aggrega.ai/type":     "server",
			"component.aggrega.ai/category": "ai-agent",
		},
		Values:   []string{"model", "system_prompt"},
		Provides: []string{"endpoint"},
		Sockets: []abi.CatalogSocket{
			{Name: "tools", Selector: "component.aggrega.ai/category=ai-tool"},
			{Name: "model", Selector: "component.aggrega.ai/category=ai-model"},
		},
	},
	{
		Name:    "acme/ai-agent/support-agent-plus",
		Version: "0.4.0",
		Labels: map[string]string{
			"component.aggrega.ai/type":     "server",
			"component.aggrega.ai/category": "ai-agent",
			"tier":                          "premium",
		},
		Values:   []string{"model", "system_prompt"},
		Provides: []string{"endpoint"},
		Sockets: []abi.CatalogSocket{
			{Name: "tools", Selector: "component.aggrega.ai/category=ai-tool"},
			{Name: "model", Selector: "component.aggrega.ai/category=ai-model"},
			{Name: "memory", Selector: "component.aggrega.ai/category=datastore"},
		},
	},
	{
		Name:    "acme/ai-model/internal-llm",
		Version: "1.2.0",
		Labels: map[string]string{
			"component.aggrega.ai/type":     "resource",
			"component.aggrega.ai/category": "ai-model",
			"model.aggrega.ai/release":      "one-two",
		},
		Values:   []string{"max_tokens", "model_name", "temperature"},
		Provides: []string{"api_base", "model_id"},
	},
}

// loadProjectionFixture writes files into a fresh directory and loads it the way
// the import loads the workspace's catalog.
func loadProjectionFixture(t *testing.T, files map[string]string) *catalog.Catalog {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
	cat, err := catalog.LoadAllWithNamedDirs("", "", nil, dir)
	if err != nil {
		t.Fatalf("load fixture catalog: %v", err)
	}
	return cat
}

// TestProjectCatalog_FieldByField asserts the projection of a fixture catalog
// entry by entry and field by field: entries sorted by name, one per name at the
// version an unversioned reference resolves to, key names sorted, sockets in
// declaration order with an extending Component's inherited sockets first, and
// an extending Component carrying its base's labels and pillars.
func TestProjectCatalog_FieldByField(t *testing.T) {
	t.Parallel()

	got := importersocket.ProjectCatalog(loadProjectionFixture(t, projectionFixture))
	if got == nil {
		t.Fatal("ProjectCatalog returned nil for a catalog holding Components")
	}
	if len(got.Components) != len(wantProjection) {
		names := make([]string, 0, len(got.Components))
		for _, c := range got.Components {
			names = append(names, c.Name+":"+c.Version)
		}
		t.Fatalf("got %d entries %v; want %d, one per Component name", len(got.Components), names, len(wantProjection))
	}
	for i, want := range wantProjection {
		g := got.Components[i]
		t.Run(want.Name, func(t *testing.T) {
			if g.Name != want.Name {
				t.Errorf("entry %d name = %q; want %q (entries are sorted by name)", i, g.Name, want.Name)
			}
			if g.Version != want.Version {
				t.Errorf("version = %q; want %q", g.Version, want.Version)
			}
			if !reflect.DeepEqual(g.Labels, want.Labels) {
				t.Errorf("labels = %v; want %v", g.Labels, want.Labels)
			}
			if !reflect.DeepEqual(g.Values, want.Values) {
				t.Errorf("values = %v; want %v", g.Values, want.Values)
			}
			if !reflect.DeepEqual(g.Provides, want.Provides) {
				t.Errorf("provides = %v; want %v", g.Provides, want.Provides)
			}
			if !reflect.DeepEqual(g.Sockets, want.Sockets) {
				t.Errorf("sockets = %v; want %v", g.Sockets, want.Sockets)
			}
		})
	}
}

// TestProjectCatalog_CarriesNoValueDefaultOrInfra asserts the encoded payload —
// the canonical CBOR bytes the importer is handed — carries none of the defaults
// the fixture's pillars declare and no infra key: the projection is names and
// labels only. Every projected field is a string, and a CBOR text string carries
// its UTF-8 bytes verbatim, so a leaked name or default would appear in the
// payload as written.
func TestProjectCatalog_CarriesNoValueDefaultOrInfra(t *testing.T) {
	t.Parallel()

	payload, err := abi.ImportInput{
		Format:  "langflow",
		Bytes:   []byte("{}"),
		Catalog: importersocket.ProjectCatalog(loadProjectionFixture(t, projectionFixture)),
	}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode import input: %v", err)
	}
	// Positive control: the payload does carry the catalog, so an absence below
	// is not an empty payload passing for a clean one.
	if !bytes.Contains(payload, []byte("acme/ai-model/internal-llm")) {
		t.Fatalf("payload carries no catalog entry, so the absence checks below prove nothing: %x", payload)
	}
	for _, leak := range []string{
		"projection-must-not-carry-this-default",
		"agent-prompt-default-not-projected",
		"zero-model-default",
		"prerelease_only",
		"replicas",
		"0.25",
	} {
		if bytes.Contains(payload, []byte(leak)) {
			t.Errorf("payload carries %q, which the projection must not: %x", leak, payload)
		}
	}
}

// TestProjectCatalog_ByteIdenticalAcrossLoads asserts one catalog yields one
// byte sequence: the fixture loaded twice, into two separate catalog views,
// projects to byte-identical canonical CBOR payloads.
func TestProjectCatalog_ByteIdenticalAcrossLoads(t *testing.T) {
	t.Parallel()

	encode := func() []byte {
		t.Helper()
		b, err := abi.ImportInput{
			Format:  "langflow",
			Bytes:   []byte("{}"),
			Catalog: importersocket.ProjectCatalog(loadProjectionFixture(t, projectionFixture)),
		}.EncodeCBOR()
		if err != nil {
			t.Fatalf("encode import input: %v", err)
		}
		return b
	}
	first, second := encode(), encode()
	if !bytes.Equal(first, second) {
		t.Fatalf("two loads of one catalog projected different bytes:\n first: %x\nsecond: %x", first, second)
	}
}

// TestProjectCatalog_EmptyCatalogIsAbsent asserts a catalog holding no Component
// projects to nil, and that the payload built from it is byte-identical to one
// built with no catalog view at all.
func TestProjectCatalog_EmptyCatalogIsAbsent(t *testing.T) {
	t.Parallel()

	if got := importersocket.ProjectCatalog(nil); got != nil {
		t.Errorf("ProjectCatalog(nil) = %+v; want nil", got)
	}
	view := importersocket.ProjectCatalog(loadProjectionFixture(t, map[string]string{}))
	if view != nil {
		t.Fatalf("ProjectCatalog over a catalog holding no Component = %+v; want nil", view)
	}
	withView, err := abi.ImportInput{Format: "langflow", Bytes: []byte("{}"), Catalog: view}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode with view: %v", err)
	}
	without, err := abi.ImportInput{Format: "langflow", Bytes: []byte("{}")}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode without view: %v", err)
	}
	if !bytes.Equal(withView, without) {
		t.Errorf("an empty catalog's payload differs from one with no view:\n with: %x\n without: %x", withView, without)
	}
}
