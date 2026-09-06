// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/gencatalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// moduleRoot returns the aggrega-cli module root; the mcp package sits two
// directories below it, so the shared compiler testdata resolves relative to it.
func moduleRoot(t *testing.T) string {
	t.Helper()
	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs cwd: %v", err)
	}
	return filepath.Dir(filepath.Dir(cwd))
}

// fullstackWorkspace returns the good end-to-end fixture — a Release with its
// sibling Manifest and Aggregate plus a local catalog — that resolves and
// renders hermetically through the embedded providers.
func fullstackWorkspace(t *testing.T) (root, catalogDir, releasePath string) {
	t.Helper()
	root = filepath.Join(moduleRoot(t), "internal", "compiler", "testdata", "fullstack-app")
	catalogDir = filepath.Join(root, "catalog")
	releasePath = filepath.Join(root, "release.yaml")
	return root, catalogDir, releasePath
}

// badWorkspace returns the known-bad fixture — a Release whose Manifest maps no
// recipe for its one component — reusing the fullstack catalog. It resolves
// clean but fails semantic validation with a resolution-completeness finding.
func badWorkspace(t *testing.T) (root, catalogDir, releasePath string) {
	t.Helper()
	root = filepath.Join(moduleRoot(t), "internal", "mcp", "testdata", "bad-workspace")
	catalogDir = filepath.Join(moduleRoot(t), "internal", "compiler", "testdata", "fullstack-app", "catalog")
	releasePath = filepath.Join(root, "release.yaml")
	return root, catalogDir, releasePath
}

// callTool invokes a tool by name through the tools/call dispatch path with the
// supplied arguments and returns the tool result, failing on a protocol-level
// error.
func callTool(t *testing.T, s *server, name string, args any) *callToolResult {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	params, err := json.Marshal(callToolParams{Name: name, Arguments: raw})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	res, rpcErr := s.handleToolsCall(params)
	if rpcErr != nil {
		t.Fatalf("tools/call %s returned protocol error: %+v", name, rpcErr)
	}
	cr, ok := res.(*callToolResult)
	if !ok {
		t.Fatalf("tools/call %s returned %T, want *callToolResult", name, res)
	}
	if len(cr.Content) == 0 || cr.Content[0].Type != "text" {
		t.Fatalf("tools/call %s returned no text content block: %+v", name, cr.Content)
	}
	return cr
}

// newTestServer builds a server with a discard log sink for direct tool
// invocation.
//
// Its start directory is an empty temporary directory, which is where a tool
// call that supplies no workspace root resolves from. Left unset, that
// resolution would begin at the process working directory and walk upward until
// it found a configured workspace — reaching the checkout the suite happens to
// run inside, and adopting its catalog stack and artifact cache. A case that
// needs a populated workspace constructs one and passes its root.
func newTestServer(t *testing.T) *server {
	t.Helper()
	return &server{out: io.Discard, logw: io.Discard, serverVersion: "test", startDir: t.TempDir()}
}

func TestGetSchemaTool(t *testing.T) {
	s := newTestServer(t)

	// A named kind returns its schema byte-for-byte in structuredContent.
	res := callTool(t, s, "get-schema", getSchemaArgs{Kind: "component"})
	if res.IsError {
		t.Fatalf("get-schema component reported error: %s", res.Content[0].Text)
	}
	var payload getSchemaResult
	if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
		t.Fatalf("unmarshal get-schema payload: %v", err)
	}
	if payload.Kind != "component" || len(payload.Schema) == 0 {
		t.Fatalf("get-schema payload = %+v, want component schema", payload)
	}

	// list enumerates kinds.
	listRes := callTool(t, s, "get-schema", getSchemaArgs{List: true})
	var listPayload getSchemaListResult
	if err := json.Unmarshal(listRes.StructuredContent, &listPayload); err != nil {
		t.Fatalf("unmarshal get-schema list payload: %v", err)
	}
	if len(listPayload.Kinds) == 0 {
		t.Fatal("get-schema list returned no kinds")
	}

	// An unknown kind is a tool-level error carrying corrective guidance.
	bad := callTool(t, s, "get-schema", getSchemaArgs{Kind: "not-a-kind"})
	if !bad.IsError {
		t.Fatal("get-schema unknown kind did not report an error")
	}
	if !strings.Contains(bad.Content[0].Text, "Valid kinds:") {
		t.Errorf("get-schema unknown-kind guidance missing valid-kinds list: %q", bad.Content[0].Text)
	}
}

func TestScaffoldTool(t *testing.T) {
	s := newTestServer(t)
	res := callTool(t, s, "scaffold", scaffoldArgs{Kind: "component", Name: "my/svc"})
	if res.IsError {
		t.Fatalf("scaffold component reported error: %s", res.Content[0].Text)
	}
	var payload scaffoldResult
	if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
		t.Fatalf("unmarshal scaffold payload: %v", err)
	}
	// The skeleton is derived from the schema: the fixed apiVersion/kind are
	// filled from their const values, and the seeded name lands in metadata.
	for _, want := range []string{
		"apiVersion: aggrega.ai/v1alpha1",
		"kind: Component",
		"name: my/svc",
	} {
		if !strings.Contains(payload.Skeleton, want) {
			t.Errorf("scaffold skeleton missing %q:\n%s", want, payload.Skeleton)
		}
	}

	// Determinism: the same request yields byte-identical output.
	res2 := callTool(t, s, "scaffold", scaffoldArgs{Kind: "component", Name: "my/svc"})
	var payload2 scaffoldResult
	if err := json.Unmarshal(res2.StructuredContent, &payload2); err != nil {
		t.Fatalf("unmarshal scaffold payload 2: %v", err)
	}
	if payload.Skeleton != payload2.Skeleton {
		t.Errorf("scaffold not deterministic:\n--- 1 ---\n%s\n--- 2 ---\n%s", payload.Skeleton, payload2.Skeleton)
	}

	bad := callTool(t, s, "scaffold", scaffoldArgs{Kind: "not-a-kind"})
	if !bad.IsError || !strings.Contains(bad.Content[0].Text, "Valid kinds:") {
		t.Errorf("scaffold unknown-kind guidance missing: %+v", bad)
	}
}

func TestValidateToolCleanFixture(t *testing.T) {
	s := newTestServer(t)
	root, catalogDir, releasePath := fullstackWorkspace(t)
	res := callTool(t, s, "validate", validateArgs{
		Release:       releasePath,
		WorkspaceRoot: root,
		CatalogDirs:   []string{catalogDir},
	})
	if res.IsError {
		t.Fatalf("validate clean fixture reported error: %s", res.Content[0].Text)
	}
	var payload validateResultPayload
	if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
		t.Fatalf("unmarshal validate payload: %v", err)
	}
	if !payload.Valid || len(payload.Diagnostics) != 0 {
		t.Fatalf("validate clean fixture = %+v, want valid with no diagnostics", payload)
	}
}

// TestValidateToolMatchesCompilerops asserts the validate tool surfaces exactly
// the diagnostic compilerops.Validate produces for a known-bad Release.
func TestValidateToolMatchesCompilerops(t *testing.T) {
	root, catalogDir, releasePath := badWorkspace(t)

	// Independent ground truth: run the shared validation core directly over the
	// same inputs the tool builds internally.
	wsCfg := workspaceconfig.Default()
	lazy := catalog.NewLazyLookup(root, wsCfg.ResolvedArtifactCacheDir(), []string{catalogDir}, nil)
	view := lazy.ForRequest(nil)
	resolved, cerr := compilerops.ResolveClosure(compilerops.ClosureInputs{
		View: view, ReleasePath: releasePath, WorkspaceRoot: root, WorkspaceConfig: wsCfg,
	})
	if cerr != nil {
		t.Fatalf("ground-truth ResolveClosure: %v", cerr)
	}
	vr := compilerops.Validate(context.Background(), compilerops.ValidateInputs{
		View: view, Closure: resolved.Closure, Release: resolved.Release, ReleasePath: releasePath,
		Manifest: resolved.Manifest,
		AggBytes: resolved.AggBytes, AggregatePath: resolved.AggregatePath, WorkspaceConfig: wsCfg, CompilerVersion: "test",
	})
	if len(vr.ResolutionFindings) == 0 {
		t.Fatalf("known-bad fixture produced no resolution finding; ValidateResult=%+v", vr)
	}
	wantDiag := vr.ResolutionFindings[0].Error()

	// The tool over the same workspace must report the same diagnostic verbatim.
	s := newTestServer(t)
	res := callTool(t, s, "validate", validateArgs{Release: releasePath, WorkspaceRoot: root, CatalogDirs: []string{catalogDir}})
	if res.IsError {
		t.Fatalf("validate known-bad reported a call error rather than diagnostics: %s", res.Content[0].Text)
	}
	var payload validateResultPayload
	if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
		t.Fatalf("unmarshal validate payload: %v", err)
	}
	if payload.Valid {
		t.Fatal("validate known-bad reported valid")
	}
	found := false
	for _, d := range payload.Diagnostics {
		if d == wantDiag {
			found = true
		}
	}
	if !found {
		t.Fatalf("validate tool diagnostics %v do not include the compilerops diagnostic %q", payload.Diagnostics, wantDiag)
	}
}

// TestBuildToolRendersAndWritesNothing asserts the build tool renders artifacts
// to a buffer and leaves the workspace byte-unchanged on disk.
func TestBuildToolRendersAndWritesNothing(t *testing.T) {
	root, catalogDir, releasePath := fullstackWorkspace(t)
	before := hashTree(t, root)

	s := newTestServer(t)
	res := callTool(t, s, "build", buildArgs{
		Release:       releasePath,
		WorkspaceRoot: root,
		CatalogDirs:   []string{catalogDir},
	})
	if res.IsError {
		t.Fatalf("build reported error: %s", res.Content[0].Text)
	}
	var payload buildResultPayload
	if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
		t.Fatalf("unmarshal build payload: %v", err)
	}
	if len(payload.Files) == 0 {
		t.Fatal("build produced no rendered files")
	}
	// Detailed is the default: rendered content is present.
	if payload.Files[0].Content == "" {
		t.Errorf("build (detailed) omitted rendered content for %s", payload.Files[0].Path)
	}

	after := hashTree(t, root)
	if before != after {
		t.Fatalf("build tool mutated the workspace on disk (tree hash changed %s -> %s)", before, after)
	}
	if _, err := os.Stat(filepath.Join(root, "dist")); err == nil {
		t.Fatal("build tool wrote a dist/ directory; it must render to a buffer only")
	}
}

func TestDescribeToolComponentAndRelease(t *testing.T) {
	s := newTestServer(t)
	root, catalogDir, releasePath := fullstackWorkspace(t)

	comp := callTool(t, s, "describe", describeArgs{
		Name: "core/compute/api-server", WorkspaceRoot: root, CatalogDirs: []string{catalogDir},
	})
	if comp.IsError {
		t.Fatalf("describe component reported error: %s", comp.Content[0].Text)
	}
	var compPayload describeEntityPayload
	if err := json.Unmarshal(comp.StructuredContent, &compPayload); err != nil {
		t.Fatalf("unmarshal describe component payload: %v", err)
	}
	if compPayload.Kind != "Component" || compPayload.Name != "core/compute/api-server" || len(compPayload.Definition) == 0 {
		t.Fatalf("describe component payload = %+v", compPayload)
	}

	rel := callTool(t, s, "describe", describeArgs{
		Release: releasePath, WorkspaceRoot: root, CatalogDirs: []string{catalogDir},
	})
	if rel.IsError {
		t.Fatalf("describe release reported error: %s", rel.Content[0].Text)
	}
	var relPayload describeReleasePayload
	if err := json.Unmarshal(rel.StructuredContent, &relPayload); err != nil {
		t.Fatalf("unmarshal describe release payload: %v", err)
	}
	if relPayload.EntityCount == 0 || len(relPayload.Entities) == 0 {
		t.Fatalf("describe release summarized no closure entities: %+v", relPayload)
	}

	// A missing catalog entity yields guidance, not a bare miss.
	miss := callTool(t, s, "describe", describeArgs{Name: "no/such/thing", WorkspaceRoot: root, CatalogDirs: []string{catalogDir}})
	if !miss.IsError || !strings.Contains(miss.Content[0].Text, "See:") {
		t.Errorf("describe missing entity guidance absent: %+v", miss)
	}
}

func TestExplainErrorTool(t *testing.T) {
	s := newTestServer(t)
	res := callTool(t, s, "explain-error", explainErrorArgs{Message: "load release foo: not found", Kind: "release"})
	if res.IsError {
		t.Fatalf("explain-error reported error: %s", res.Content[0].Text)
	}
	var payload explainErrorResult
	if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
		t.Fatalf("unmarshal explain-error payload: %v", err)
	}
	if !strings.Contains(payload.Guidance, "load release foo: not found") {
		t.Errorf("guidance dropped the original message: %q", payload.Guidance)
	}
	if payload.DocPointer != "aggrega-docs/language-reference/entities/release.md" {
		t.Errorf("doc pointer = %q, want the release language-reference page", payload.DocPointer)
	}
}

// TestGenCatalogToolInvokesCore asserts the gen-catalog tool builds the
// catalog-generation options from its arguments, drives the in-process core
// through the injected runner seam, and returns the operation's output.
func TestGenCatalogToolInvokesCore(t *testing.T) {
	s := newTestServer(t)
	var gotOpts gencatalog.Options
	s.runGenCatalog = func(_ context.Context, opts gencatalog.Options, out io.Writer) error {
		gotOpts = opts
		_, _ = io.WriteString(out, "generated out/core/helm-provider/1.0.0/plugin.yaml\n")
		return nil
	}
	res := callTool(t, s, "gen-catalog", genCatalogArgs{FromEmbedded: true, Output: "out", Overlay: "overlays"})
	if res.IsError {
		t.Fatalf("gen-catalog reported error: %s", res.Content[0].Text)
	}
	wantEmbedded := gencatalog.Options{FromEmbedded: true, OutputDir: "out", OverlayPath: "overlays"}
	if gotOpts != wantEmbedded {
		t.Fatalf("gen-catalog options = %+v, want %+v", gotOpts, wantEmbedded)
	}
	if !strings.Contains(res.Content[0].Text, "plugin.yaml") {
		t.Errorf("gen-catalog did not return the operation output: %q", res.Content[0].Text)
	}

	// The single-plugin case builds the plugin-path options.
	res = callTool(t, s, "gen-catalog", genCatalogArgs{Plugin: "p.wasm", Output: "out", Overlay: "o.yaml"})
	if res.IsError {
		t.Fatalf("gen-catalog (single) reported error: %s", res.Content[0].Text)
	}
	wantSingle := gencatalog.Options{PluginPath: "p.wasm", OutputDir: "out", OverlayPath: "o.yaml"}
	if gotOpts != wantSingle {
		t.Fatalf("gen-catalog single options = %+v, want %+v", gotOpts, wantSingle)
	}

	// A generation failure surfaces the core's error as guidance.
	s.runGenCatalog = func(_ context.Context, _ gencatalog.Options, _ io.Writer) error {
		return fmt.Errorf("overlay for guest has no metadata.name")
	}
	bad := callTool(t, s, "gen-catalog", genCatalogArgs{Plugin: "p.wasm", Output: "out", Overlay: "o.yaml"})
	if !bad.IsError || !strings.Contains(bad.Content[0].Text, "metadata.name") {
		t.Errorf("gen-catalog failure did not surface error guidance: %+v", bad)
	}
}

// TestUnknownToolReportsCuratedSet asserts an unknown tool name is a protocol
// error naming the curated set.
func TestUnknownToolReportsCuratedSet(t *testing.T) {
	s := newTestServer(t)
	params, _ := json.Marshal(callToolParams{Name: "nope", Arguments: json.RawMessage(`{}`)})
	_, rpcErr := s.handleToolsCall(params)
	if rpcErr == nil || rpcErr.Code != codeInvalidParams {
		t.Fatalf("unknown tool error = %+v, want invalid-params", rpcErr)
	}
	if !strings.Contains(rpcErr.Message, "validate") || !strings.Contains(rpcErr.Message, "gen-catalog") {
		t.Errorf("unknown-tool message does not name the curated set: %q", rpcErr.Message)
	}
}

// TestToolsCallOverWire drives tools/list and a tools/call through the real
// newline-delimited JSON-RPC message loop, proving the curated tools are
// invocable end-to-end over the transport, not only through direct dispatch.
func TestToolsCallOverWire(t *testing.T) {
	resps, _, _, err := drive(t,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get-schema","arguments":{"list":true}}}`,
	)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(resps) != 2 {
		t.Fatalf("want 2 responses, got %d", len(resps))
	}
	var tl listToolsResult
	if err := json.Unmarshal(resps[0].Result, &tl); err != nil {
		t.Fatalf("unmarshal tools/list: %v", err)
	}
	if len(tl.Tools) != len(toolRegistry) {
		t.Fatalf("tools/list over wire returned %d tools, want %d", len(tl.Tools), len(toolRegistry))
	}
	if resps[1].Error != nil {
		t.Fatalf("tools/call get-schema over wire errored: %+v", resps[1].Error)
	}
	var cr callToolResult
	if err := json.Unmarshal(resps[1].Result, &cr); err != nil {
		t.Fatalf("unmarshal tools/call result: %v", err)
	}
	if cr.IsError || len(cr.Content) == 0 {
		t.Fatalf("tools/call get-schema over wire = %+v", cr)
	}
	var listPayload getSchemaListResult
	if err := json.Unmarshal(cr.StructuredContent, &listPayload); err != nil {
		t.Fatalf("unmarshal get-schema list over wire: %v", err)
	}
	if len(listPayload.Kinds) == 0 {
		t.Fatal("get-schema list over wire returned no kinds")
	}
}

// hashTree returns a stable hash over every file's path and content under dir,
// so a byte-identical tree hashes identically and any write changes the hash.
func hashTree(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	var paths []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	sort.Strings(paths)
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
