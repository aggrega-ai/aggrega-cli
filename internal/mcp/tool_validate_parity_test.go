// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// One core, two front doors. The validate tool's entity, plugin, and catalog
// scopes each dispatch to a shared, importable core in internal/validateops —
// the SAME core the CLI validate verb calls. The CLI-side test proves the CLI
// adapter surfaces that core faithfully (byte-identical CLI output); these tests
// prove the other half: the MCP adapter surfaces the SAME core verbatim.
//
// Each parity test invokes the MCP tool over a fixture, then calls the shared
// core DIRECTLY over the same fixture with the same resolved inputs the tool
// passes (workspace root, cache dir, catalog dirs — obtained through the tool's
// own resolveToolWorkspace), and asserts the payload's verdict and diagnostic
// texts equal the core's, in the same order. Each scope covers a passing and a
// failing case; the failing case — where the exact diagnostic text must match —
// is the strongest single-source evidence.
//
// # What these tests assert, and what they do not
//
// ASSERTED: the verdict, and the ordered diagnostic texts character for
// character. The payload carries the core's own error strings rather than a
// re-wording of them, so a change to a diagnostic moves both front doors at
// once or fails here.
//
// NOT ASSERTED, and legitimately so: the FORM each surface gives one fact. The
// CLI prefixes a catalog diagnostic with two leading spaces and writes it to
// stderr, while the MCP carries it as a bare payload entry. That is one
// diagnostic in two renderings for two readers, and neither rendering is the
// fact.
//
// 🔴 NOT ASSERTED, and NOT thereby excused: whether each surface carries every
// structured fact the core computed. Every assertion above reads the verdict and
// the diagnostic list and nothing else, so a field the core fills and one front
// door never looks at leaves all of them green. Sharing the core is what makes
// the two agree on the verdict and the diagnostic texts — it is not what makes
// them agree on the whole result, and no test in this file asks whether they do.
//
// The line between the two is what a fact IS against how it is spelled. A count,
// a verdict, a classification, an advisory or a hint is owed on every surface
// that can carry structure; an indentation, an output stream and a field
// spelling are not. That second axis has its own instrument — the projection
// read driven from internal/opsurface, which compares the field set each surface
// reads from a shared result — so the "decoration differs by design" note above
// is bounded to rendering and is never a licence for a per-surface difference in
// what the result says.

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// resolvedToolWorkspace resolves the workspace a validate tool call operates in,
// through the tool's own resolveToolWorkspace, so a direct core call in a test
// receives byte-for-byte the inputs the tool would pass.
func resolvedToolWorkspace(t *testing.T, s *server, root string, dirs []string) *toolWorkspace {
	t.Helper()
	tw, err := s.resolveToolWorkspace(root, dirs)
	if err != nil {
		t.Fatalf("resolveToolWorkspace(%q, %v): %v", root, dirs, err)
	}
	return tw
}

// assertHaltParity asserts the halt-at-first parity shared by the entity and
// plugin scopes: when the core returns a blocking error, the payload is invalid
// with exactly that one diagnostic text; when the core succeeds, the payload is
// valid with no diagnostics.
func assertHaltParity(t *testing.T, payload validateResultPayload, coreErr error) {
	t.Helper()
	if coreErr != nil {
		if payload.Valid {
			t.Fatalf("core rejected but payload reports valid: %+v", payload)
		}
		want := []string{coreErr.Error()}
		if !reflect.DeepEqual(payload.Diagnostics, want) {
			t.Fatalf("diagnostics diverge from core:\n payload: %#v\n core:    %#v", payload.Diagnostics, want)
		}
		return
	}
	if !payload.Valid {
		t.Fatalf("core accepted but payload reports invalid: %+v", payload)
	}
	if len(payload.Diagnostics) != 0 {
		t.Fatalf("core accepted but payload carries diagnostics: %#v", payload.Diagnostics)
	}
}

// TestValidateToolEntityScopeCoreParity proves the single-entity scope's MCP
// payload carries the validateops.ValidateEntity core's verdict and blocking
// diagnostic verbatim, for both a clean entity and one the core rejects.
func TestValidateToolEntityScopeCoreParity(t *testing.T) {
	s := newTestServer(t)
	root, catalogDir, releasePath := fullstackWorkspace(t)
	dirs := []string{catalogDir}
	tw := resolvedToolWorkspace(t, s, root, dirs)

	t.Run("pass", func(t *testing.T) {
		entityPath := filepath.Join(catalogDir, "base", "components", "api-server.yaml")

		res := callTool(t, s, "validate", validateArgs{
			Entity:        entityPath,
			WorkspaceRoot: root,
			CatalogDirs:   dirs,
		})
		if res.IsError {
			t.Fatalf("validate entity scope reported error: %s", res.Content[0].Text)
		}
		payload := unmarshalValidatePayload(t, res)

		_, coreErr := validateops.ValidateEntity(context.Background(), tw.root, tw.cacheDir, tw.dirs,
			resolvedEntityPath(t, tw, entityPath), false)
		if coreErr != nil {
			t.Fatalf("core rejected the clean fixture: %v", coreErr)
		}
		assertHaltParity(t, payload, coreErr)
	})

	t.Run("fail", func(t *testing.T) {
		// A Release is not a single-entity validation target; the core halts at the
		// first check with a blocking error the payload must carry verbatim.
		entityPath := releasePath

		res := callTool(t, s, "validate", validateArgs{
			Entity:        entityPath,
			WorkspaceRoot: root,
			CatalogDirs:   dirs,
		})
		payload := unmarshalValidatePayload(t, res)

		_, coreErr := validateops.ValidateEntity(context.Background(), tw.root, tw.cacheDir, tw.dirs,
			resolvedEntityPath(t, tw, entityPath), false)
		if coreErr == nil {
			t.Fatalf("expected the core to reject a Release as a single-entity target")
		}
		assertHaltParity(t, payload, coreErr)
	})
}

// resolvedEntityPath resolves an entity path the way the validate tool resolves
// it, so a parity comparison drives the core over the same file the tool drove it
// over. A refusal is fatal rather than ignored: these fixtures are inside the
// workspace, so one would mean the comparison is no longer measuring parity.
func resolvedEntityPath(t *testing.T, tw *toolWorkspace, entity string) string {
	t.Helper()
	resolved, refusal := tw.workspacePath("validate", "entity", entity)
	if refusal != "" {
		t.Fatalf("the fixture entity path was refused: %s", refusal)
	}
	return resolved
}

// TestValidateToolPluginScopeCoreParity proves the plugin-binary scope's MCP
// payload carries the validateops.ValidatePluginBinary core's verdict and
// blocking diagnostic verbatim, for both a matching declared type and a
// mismatched one. The plugin scope feeds the core only the .wasm path and the
// tool's fixed memory-page cap, so the direct core call uses the identical
// inputs. The wasm toolchain is present in this environment, so the fixture
// builds rather than skipping.
func TestValidateToolPluginScopeCoreParity(t *testing.T) {
	s := newTestServer(t)
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "parse-go.wasm")

	t.Run("pass", func(t *testing.T) {
		res := callTool(t, s, "validate", validateArgs{
			Plugin: pluginPath,
			Type:   "parser",
		})
		if res.IsError {
			t.Fatalf("validate plugin scope reported error: %s", res.Content[0].Text)
		}
		payload := unmarshalValidatePayload(t, res)

		_, coreErr := validateops.ValidatePluginBinary(context.Background(), pluginPath, "parser", "", pluginValidateMaxMemoryPages)
		if coreErr != nil {
			t.Fatalf("core rejected a matching declared type: %v", coreErr)
		}
		assertHaltParity(t, payload, coreErr)
	})

	t.Run("fail", func(t *testing.T) {
		res := callTool(t, s, "validate", validateArgs{
			Plugin: pluginPath,
			Type:   "provider",
		})
		payload := unmarshalValidatePayload(t, res)

		_, coreErr := validateops.ValidatePluginBinary(context.Background(), pluginPath, "provider", "", pluginValidateMaxMemoryPages)
		if coreErr == nil {
			t.Fatalf("expected the core to reject a type disagreeing with the binary's self-report")
		}
		assertHaltParity(t, payload, coreErr)
	})
}

// expectedCatalogDiags builds, from a CatalogResult, the exact ordered diagnostic
// list the catalog scope must produce: each category error prefixed in the
// halt-at-first order (extends, then oci-location, then input-kind). It is the
// test's independent oracle for the tool's prefix mapping — the prefix strings
// are duplicated here deliberately so a drift in the tool's mapping is caught,
// not masked by a shared constant.
func expectedCatalogDiags(res validateops.CatalogResult) []string {
	var diags []string
	for _, e := range res.ExtendsErrs {
		diags = append(diags, "extends: "+e.Error())
	}
	if res.OCICollision != nil {
		diags = append(diags, "oci-location: "+res.OCICollision.Error())
	}
	for _, e := range res.InputKindErrs {
		diags = append(diags, "input-kind: "+e.Error())
	}
	return diags
}

// TestValidateToolCatalogScopeCoreParity proves the whole-catalog scope's MCP
// payload carries the validateops.ValidateCatalog core's verdict and the prefixed
// mapping of its category errors, in the halt-at-first order, verbatim. The
// passing case is the clean end-to-end fixture; the failing case is a minimal
// catalog whose workload:-declaring Recipe binds a provider that cannot
// synthesize — a guaranteed input-kind capability error whose exact text the
// payload must carry.
func TestValidateToolCatalogScopeCoreParity(t *testing.T) {
	t.Run("pass", func(t *testing.T) {
		s := newTestServer(t)
		root, catalogDir, _ := fullstackWorkspace(t)
		dirs := []string{catalogDir}
		tw := resolvedToolWorkspace(t, s, root, dirs)

		res := callTool(t, s, "validate", validateArgs{
			Catalog:       true,
			WorkspaceRoot: root,
			CatalogDirs:   dirs,
		})
		if res.IsError {
			t.Fatalf("validate catalog scope reported protocol error: %s", res.Content[0].Text)
		}
		payload := unmarshalValidatePayload(t, res)

		coreRes, coreErr := validateops.ValidateCatalog(tw.root, tw.cacheDir, tw.dirs)
		if coreErr != nil {
			t.Fatalf("core could not load the clean catalog: %v", coreErr)
		}
		want := expectedCatalogDiags(coreRes)
		if payload.Valid != (len(want) == 0) {
			t.Fatalf("valid verdict diverges from core: payload.Valid=%v, core diagnostics=%#v", payload.Valid, want)
		}
		if !reflect.DeepEqual(payload.Diagnostics, want) {
			t.Fatalf("diagnostics diverge from core:\n payload: %#v\n core:    %#v", payload.Diagnostics, want)
		}
	})

	t.Run("fail", func(t *testing.T) {
		s := newTestServer(t)
		dir := t.TempDir()
		writeCatalogFile(t, filepath.Join(dir, "recipe.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: api-helmchart
  version: "1.0.0"
spec:
  capability: helm-chart
  implements: core/server
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
`)
		writeCatalogFile(t, filepath.Join(dir, "plugin.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: helm-chart-renderer
  pluginType: provider
  version: "1.0.0"
spec:
  abiVersion: v1alpha1
  exports:
    - name: render_node
    - name: assemble_boundary
  provider:
    capabilities:
      - helm-chart
    contract:
      inputKinds:
        sourced: true
      boundaryAssembly: nest
`)
		dirs := []string{dir}
		tw := resolvedToolWorkspace(t, s, dir, dirs)

		res := callTool(t, s, "validate", validateArgs{
			Catalog:       true,
			WorkspaceRoot: dir,
			CatalogDirs:   dirs,
		})
		if res.IsError {
			t.Fatalf("validate catalog scope reported protocol error: %s", res.Content[0].Text)
		}
		payload := unmarshalValidatePayload(t, res)

		coreRes, coreErr := validateops.ValidateCatalog(tw.root, tw.cacheDir, tw.dirs)
		if coreErr != nil {
			t.Fatalf("core could not load the fixture catalog: %v", coreErr)
		}
		want := expectedCatalogDiags(coreRes)
		if len(want) == 0 {
			t.Fatalf("expected the core to report a failing category for the workload:+non-synthesizing-provider fixture")
		}
		if payload.Valid {
			t.Fatalf("core rejected the catalog but payload reports valid: %+v", payload)
		}
		if !reflect.DeepEqual(payload.Diagnostics, want) {
			t.Fatalf("diagnostics diverge from core:\n payload: %#v\n core:    %#v", payload.Diagnostics, want)
		}
	})
}

// writeCatalogFile writes an inline catalog entity fixture, creating parent
// directories as needed and failing the test on any I/O error.
func writeCatalogFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
