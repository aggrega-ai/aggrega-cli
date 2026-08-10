// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// unmarshalValidatePayload decodes the validate tool's structured payload,
// failing the test on a decode error.
func unmarshalValidatePayload(t *testing.T, res *callToolResult) validateResultPayload {
	t.Helper()
	var payload validateResultPayload
	if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
		t.Fatalf("unmarshal validate payload: %v", err)
	}
	return payload
}

// TestValidateToolEntityScope drives the single-entity scope over a clean
// Component: the tool accepts the entity selector, reports the entity scope, and
// returns a valid structured result with no diagnostics.
func TestValidateToolEntityScope(t *testing.T) {
	s := newTestServer(t)
	root, catalogDir, _ := fullstackWorkspace(t)
	entityPath := filepath.Join(catalogDir, "base", "components", "api-server.yaml")

	res := callTool(t, s, "validate", validateArgs{
		Entity:        entityPath,
		WorkspaceRoot: root,
		CatalogDirs:   []string{catalogDir},
	})
	if res.IsError {
		t.Fatalf("validate entity scope reported error: %s", res.Content[0].Text)
	}
	payload := unmarshalValidatePayload(t, res)
	if payload.Scope != validateScopeEntity {
		t.Fatalf("scope = %q, want %q", payload.Scope, validateScopeEntity)
	}
	if payload.Target != entityPath {
		t.Fatalf("target = %q, want %q", payload.Target, entityPath)
	}
	if !payload.Valid || len(payload.Diagnostics) != 0 {
		t.Fatalf("entity scope = %+v, want valid with no diagnostics", payload)
	}
}

// TestValidateToolEntityScopeInvalid confirms an entity the core rejects surfaces
// as an invalid structured result with the blocking diagnostic, not a protocol
// error — and that valid tracks diagnostics emptiness uniformly.
func TestValidateToolEntityScopeInvalid(t *testing.T) {
	s := newTestServer(t)
	root, catalogDir, _ := fullstackWorkspace(t)
	// A Release is not a single-entity validation target; the core halts with a
	// blocking error, which the tool surfaces as one diagnostic.
	entityPath := filepath.Join(root, "release.yaml")

	res := callTool(t, s, "validate", validateArgs{
		Entity:        entityPath,
		WorkspaceRoot: root,
		CatalogDirs:   []string{catalogDir},
	})
	payload := unmarshalValidatePayload(t, res)
	if payload.Scope != validateScopeEntity {
		t.Fatalf("scope = %q, want %q", payload.Scope, validateScopeEntity)
	}
	if payload.Valid {
		t.Fatalf("expected invalid entity result, got %+v", payload)
	}
	if len(payload.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %v, want exactly one blocking diagnostic", payload.Diagnostics)
	}
}

// TestValidateToolPluginScope drives the binary-only plugin scope: a raw .wasm
// checked against a matching declared type. The tool accepts the plugin selector,
// reports the plugin scope, and returns a valid structured result. The wasm
// toolchain is present in this environment, so the fixture builds rather than
// skipping.
func TestValidateToolPluginScope(t *testing.T) {
	s := newTestServer(t)
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "parse-go.wasm")

	res := callTool(t, s, "validate", validateArgs{
		Plugin: pluginPath,
		Type:   "parser",
	})
	if res.IsError {
		t.Fatalf("validate plugin scope reported error: %s", res.Content[0].Text)
	}
	payload := unmarshalValidatePayload(t, res)
	if payload.Scope != validateScopePlugin {
		t.Fatalf("scope = %q, want %q", payload.Scope, validateScopePlugin)
	}
	if payload.Target != pluginPath {
		t.Fatalf("target = %q, want %q", payload.Target, pluginPath)
	}
	if !payload.Valid || len(payload.Diagnostics) != 0 {
		t.Fatalf("plugin scope = %+v, want valid with no diagnostics", payload)
	}
}

// TestValidateToolPluginScopeTypeMismatch confirms a declared type that
// disagrees with the binary's self-report surfaces as an invalid structured
// result with the blocking diagnostic, not a protocol error.
func TestValidateToolPluginScopeTypeMismatch(t *testing.T) {
	s := newTestServer(t)
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "parse-go.wasm")

	res := callTool(t, s, "validate", validateArgs{
		Plugin: pluginPath,
		Type:   "provider",
	})
	payload := unmarshalValidatePayload(t, res)
	if payload.Scope != validateScopePlugin {
		t.Fatalf("scope = %q, want %q", payload.Scope, validateScopePlugin)
	}
	if payload.Valid || len(payload.Diagnostics) != 1 {
		t.Fatalf("expected invalid plugin result with one diagnostic, got %+v", payload)
	}
}

// TestValidateToolCatalogScope drives the whole-catalog scope: the tool accepts
// the catalog selector, reports the catalog scope, and returns a structured
// result whose validity tracks diagnostics emptiness.
func TestValidateToolCatalogScope(t *testing.T) {
	s := newTestServer(t)
	root, catalogDir, _ := fullstackWorkspace(t)

	res := callTool(t, s, "validate", validateArgs{
		Catalog:       true,
		WorkspaceRoot: root,
		CatalogDirs:   []string{catalogDir},
	})
	if res.IsError {
		t.Fatalf("validate catalog scope reported protocol error: %s", res.Content[0].Text)
	}
	payload := unmarshalValidatePayload(t, res)
	if payload.Scope != validateScopeCatalog {
		t.Fatalf("scope = %q, want %q", payload.Scope, validateScopeCatalog)
	}
	if payload.Target != root {
		t.Fatalf("target = %q, want %q", payload.Target, root)
	}
	if payload.Valid != (len(payload.Diagnostics) == 0) {
		t.Fatalf("valid/diagnostics inconsistent: %+v", payload)
	}
}

// TestValidateToolReleaseScopeCarriesScopeAndTarget confirms the release scope
// now reports the uniform scope/target discriminators while still populating the
// back-compat release field.
func TestValidateToolReleaseScopeCarriesScopeAndTarget(t *testing.T) {
	s := newTestServer(t)
	root, catalogDir, releasePath := fullstackWorkspace(t)

	res := callTool(t, s, "validate", validateArgs{
		Release:       releasePath,
		WorkspaceRoot: root,
		CatalogDirs:   []string{catalogDir},
	})
	if res.IsError {
		t.Fatalf("validate release scope reported error: %s", res.Content[0].Text)
	}
	payload := unmarshalValidatePayload(t, res)
	if payload.Scope != validateScopeRelease {
		t.Fatalf("scope = %q, want %q", payload.Scope, validateScopeRelease)
	}
	if payload.Target != releasePath || payload.Release != releasePath {
		t.Fatalf("release scope target/release = %q/%q, want %q for both", payload.Target, payload.Release, releasePath)
	}
	if !payload.Valid || len(payload.Diagnostics) != 0 {
		t.Fatalf("release scope = %+v, want valid with no diagnostics", payload)
	}
}

// TestValidateToolScopeGuards exercises the mutual-exclusion and scope-selection
// guards. Each is an argument-combination failure, so the tool returns a protocol
// error with codeInvalidParams and arg-name guidance — not a tool result.
func TestValidateToolScopeGuards(t *testing.T) {
	s := newTestServer(t)
	cases := []struct {
		name string
		args validateArgs
		want string
	}{
		{"type without plugin", validateArgs{Type: "parser"}, "validate 'type' applies only with 'plugin'"},
		{"plugin with release", validateArgs{Plugin: "p.wasm", Release: "r.yaml"}, "'plugin' and 'release' are mutually exclusive"},
		{"plugin with catalog", validateArgs{Plugin: "p.wasm", Catalog: true}, "'plugin' and 'catalog' are mutually exclusive"},
		{"release with entity", validateArgs{Release: "r.yaml", Entity: "e.yaml"}, "'release' and 'entity' are mutually exclusive"},
		{"release with catalog", validateArgs{Release: "r.yaml", Catalog: true}, "'release' and 'catalog' are mutually exclusive"},
		{"catalog with entity", validateArgs{Catalog: true, Entity: "e.yaml"}, "'catalog' and 'entity' are mutually exclusive"},
		{"no scope selected", validateArgs{}, "validate requires exactly one of 'release', 'entity', 'plugin', or 'catalog'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.args)
			if err != nil {
				t.Fatalf("marshal args: %v", err)
			}
			res, rpcErr := validateInvoke(s, context.Background(), raw)
			if res != nil {
				t.Fatalf("expected no tool result, got %+v", res)
			}
			if rpcErr == nil || rpcErr.Code != codeInvalidParams {
				t.Fatalf("error = %+v, want code %d", rpcErr, codeInvalidParams)
			}
			if !strings.Contains(rpcErr.Message, tc.want) {
				t.Fatalf("guidance %q does not contain %q", rpcErr.Message, tc.want)
			}
		})
	}
}
