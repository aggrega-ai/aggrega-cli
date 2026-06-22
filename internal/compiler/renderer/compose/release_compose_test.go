// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compose

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// goldenCheckAssembled compares actual against
// testdata/release/<name>.golden.yaml. Set UPDATE_GOLDEN=1 to overwrite.
func goldenCheckAssembled(t *testing.T, name string, actual []byte) {
	t.Helper()
	path := filepath.Join("testdata", "release", name+".golden.yaml")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, actual, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		t.Logf("updated golden: %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with UPDATE_GOLDEN=1 to create)", path, err)
	}
	if string(actual) != string(want) {
		t.Errorf("output mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, actual, want)
	}
}

// makeFragment runs SynthesizeWorkload for a single node and returns the
// emitted Intermediate=true fragments.
func makeFragment(t *testing.T, nodeID string, w *types.Workload, annotations map[string]string) []renderer.EmittedFile {
	t.Helper()
	nodeCtx := nodeCtxFor(nodeID)
	nodeCtx.Annotations = annotations
	files, err := (&ComposeEngine{}).SynthesizeWorkload(context.Background(), nil, w, nodeCtx)
	if err != nil {
		t.Fatalf("SynthesizeWorkload(%s): %v", nodeID, err)
	}
	return files
}

func TestComposeAssemble_SingleBoundary(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("web", 8080)
	w.Containers.Set("main", c)
	pub := types.ServiceSpec{Type: "NodePort"}
	pub.Ports.Set("web", 8080)
	w.Services.Set("api", pub)

	frag := makeFragment(t, "api", w, map[string]string{renderer.AnnotationNamespace: "production"})

	info := renderer.BoundaryInfo{
		BoundaryPath:  "default",
		AggregateName: "my-app",
		IntermediatesByNode: map[string][]renderer.EmittedFile{
			"api": frag,
		},
		NodeAnnotations:        map[string]map[string]string{"api": {renderer.AnnotationNamespace: "production"}},
		NodeComponent:          map[string]string{"api": "test/component"},
		ContributingBoundaries: []string{"default"},
	}

	files, _, err := (&ComposeEngine{}).AssembleBoundary(context.Background(), info)
	if err != nil {
		t.Fatalf("AssembleBoundary: %v", err)
	}
	body, ok := files[AssembledOutputFile]
	if !ok {
		t.Fatalf("expected %s key, got %v", AssembledOutputFile, mapKeys(files))
	}
	goldenCheckAssembled(t, "single-boundary-single-node", body)
}

// TestComposeAssemble_MergedPath verifies that contributors from multiple
// boundaries that route to the SAME dispatch path produce one merged
// docker-compose.yaml — the canonical compose-bridge / shared-directory
// pattern. The assembler is invoked once per (path, provider) tuple; both
// boundaries' nodes appear in IntermediatesByNode and ContributingBoundaries
// lists every contributor.
func TestComposeAssemble_MergedPath(t *testing.T) {
	t.Parallel()

	frontW := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	fc := types.ContainerSpec{Image: "frontend:1.0"}
	fc.Ports.Set("http", 80)
	frontW.Containers.Set("main", fc)
	fSvc := types.ServiceSpec{Type: "LoadBalancer"}
	fSvc.Ports.Set("http", 80)
	frontW.Services.Set("public", fSvc)

	backW := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	bc := types.ContainerSpec{Image: "backend:1.0"}
	bc.Ports.Set("rpc", 50051)
	backW.Containers.Set("main", bc)
	bSvc := types.ServiceSpec{Type: "ClusterIP"}
	bSvc.Ports.Set("rpc", 50051)
	backW.Services.Set("internal", bSvc)

	info := renderer.BoundaryInfo{
		BoundaryPath:  "merged",
		AggregateName: "two-boundary-app",
		IntermediatesByNode: map[string][]renderer.EmittedFile{
			"frontend": makeFragment(t, "frontend", frontW, map[string]string{renderer.AnnotationNamespace: "edge-net"}),
			"backend":  makeFragment(t, "backend", backW, map[string]string{renderer.AnnotationNamespace: "core-net"}),
		},
		NodeAnnotations: map[string]map[string]string{
			"frontend": {renderer.AnnotationNamespace: "edge-net"},
			"backend":  {renderer.AnnotationNamespace: "core-net"},
		},
		NodeComponent:          map[string]string{"frontend": "test/frontend", "backend": "test/backend"},
		ContributingBoundaries: []string{"core", "edge"},
	}

	files, _, err := (&ComposeEngine{}).AssembleBoundary(context.Background(), info)
	if err != nil {
		t.Fatalf("AssembleBoundary: %v", err)
	}
	goldenCheckAssembled(t, "multi-boundary-services-merged", files[AssembledOutputFile])
}

func TestComposeAssemble_DependsOnFromUsesEdges(t *testing.T) {
	t.Parallel()

	apiW := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	ac := types.ContainerSpec{Image: "api:1.0"}
	ac.Ports.Set("http", 8080)
	apiW.Containers.Set("main", ac)
	apiSvc := types.ServiceSpec{Type: "ClusterIP"}
	apiSvc.Ports.Set("http", 8080)
	apiW.Services.Set("api", apiSvc)

	dbW := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	dc := types.ContainerSpec{Image: "postgres:15"}
	dc.Ports.Set("pg", 5432)
	dbW.Containers.Set("main", dc)
	dbSvc := types.ServiceSpec{Type: "ClusterIP"}
	dbSvc.Ports.Set("pg", 5432)
	dbW.Services.Set("db", dbSvc)

	info := renderer.BoundaryInfo{
		BoundaryPath:  "default",
		AggregateName: "depends-app",
		IntermediatesByNode: map[string][]renderer.EmittedFile{
			"api": makeFragment(t, "api", apiW, nil),
			"db":  makeFragment(t, "db", dbW, nil),
		},
		NodeAnnotations:        map[string]map[string]string{},
		NodeComponent:          map[string]string{"api": "test/api", "db": "test/db"},
		ContributingBoundaries: []string{"default"},
		Edges: []renderer.IREdgeInfo{
			{Source: "api", Target: "db"},
		},
	}

	files, _, err := (&ComposeEngine{}).AssembleBoundary(context.Background(), info)
	if err != nil {
		t.Fatalf("AssembleBoundary: %v", err)
	}
	goldenCheckAssembled(t, "depends-on-from-uses-edges", files[AssembledOutputFile])
}

func TestComposeAssemble_ServiceCollision_Errors(t *testing.T) {
	t.Parallel()

	// Two contributing nodes hand-roll fragments declaring a shared service
	// name "shared". The merger surfaces TRANSLATION_ERROR naming every
	// contributing (boundary, nodeID) tuple. (Workload-synthesised fragments
	// can't collide naturally because the synthesiser keys services by
	// nodeID; this test uses hand-authored fragment bodies to construct the
	// collision case directly.)
	fragBody := func(image string) []byte {
		return []byte("services:\n  shared:\n    image: " + image + "\n")
	}
	info := renderer.BoundaryInfo{
		BoundaryPath:  "merged",
		AggregateName: "collision-app",
		IntermediatesByNode: map[string][]renderer.EmittedFile{
			"auth-a": {{Name: "auth-a.compose-fragment.yaml", Body: fragBody("auth-a:1.0"), Origin: renderer.EmitOriginWorkload, Intermediate: true}},
			"auth-b": {{Name: "auth-b.compose-fragment.yaml", Body: fragBody("auth-b:1.0"), Origin: renderer.EmitOriginWorkload, Intermediate: true}},
		},
		NodeAnnotations: map[string]map[string]string{
			"auth-a": {renderer.AnnotationNamespace: "a"},
			"auth-b": {renderer.AnnotationNamespace: "b"},
		},
		NodeComponent:          map[string]string{},
		ContributingBoundaries: []string{"a", "b"},
	}

	_, _, err := (&ComposeEngine{}).AssembleBoundary(context.Background(), info)
	if err == nil {
		t.Fatal("expected TRANSLATION_ERROR for service collision across contributors")
	}
	if !strings.Contains(err.Error(), "TRANSLATION_ERROR") {
		t.Errorf("expected TRANSLATION_ERROR prefix, got: %v", err)
	}
	if !strings.Contains(err.Error(), `service "shared"`) {
		t.Errorf("error must name colliding service, got: %v", err)
	}
	if !strings.Contains(err.Error(), `node="auth-a"`) || !strings.Contains(err.Error(), `node="auth-b"`) {
		t.Errorf("error must name every contributing node, got: %v", err)
	}
}

func TestComposeAssemble_NoNamespaceAnnotation_FallsBackToBoundaryPath(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "x:1.0"}
	c.Ports.Set("p", 80)
	w.Containers.Set("main", c)
	s := types.ServiceSpec{Type: "ClusterIP"}
	s.Ports.Set("p", 80)
	w.Services.Set("api", s)

	info := renderer.BoundaryInfo{
		BoundaryPath:  "STAGE-01",
		AggregateName: "fallback-app",
		IntermediatesByNode: map[string][]renderer.EmittedFile{
			"x": makeFragment(t, "x", w, nil),
		},
		NodeAnnotations:        map[string]map[string]string{"x": {}},
		NodeComponent:          map[string]string{"x": "test/x"},
		ContributingBoundaries: []string{"STAGE-01"},
	}

	files, _, err := (&ComposeEngine{}).AssembleBoundary(context.Background(), info)
	if err != nil {
		t.Fatalf("AssembleBoundary: %v", err)
	}
	body := string(files[AssembledOutputFile])
	// Expect the boundary path lowercased + sanitised: "stage-01".
	if !strings.Contains(body, "stage-01:") {
		t.Errorf("expected sanitised network name 'stage-01' in output:\n%s", body)
	}
}

func TestRenderSourced_DeepMergeOverlay(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.yaml")
	baseBody := []byte(strings.TrimLeft(`
services:
  app:
    image: original:1.0
    environment:
      ORIGINAL_KEY: keep
      OVERWRITTEN_KEY: lose
    ports:
      - "1111:1111"
`, "\n"))
	if err := os.WriteFile(basePath, baseBody, 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}

	overlay := map[string]any{
		"services": map[string]any{
			"app": map[string]any{
				"image": "overlay:2.0",
				"environment": map[string]any{
					"OVERWRITTEN_KEY": "win",
					"NEW_KEY":         "added",
				},
				"ports": []any{"2222:2222", "3333:3333"},
			},
		},
	}

	out, err := renderSourced(basePath, overlay, nodeCtxFor("app"))
	if err != nil {
		t.Fatalf("renderSourced: %v", err)
	}
	body := string(out["app.compose-fragment.yaml"])

	// Image: overlay wins.
	if !strings.Contains(body, "image: overlay:2.0") {
		t.Errorf("image should be overlay:2.0:\n%s", body)
	}
	// ORIGINAL_KEY preserved (map merge, base-only key).
	if !strings.Contains(body, "ORIGINAL_KEY: keep") {
		t.Errorf("ORIGINAL_KEY should survive merge:\n%s", body)
	}
	// OVERWRITTEN_KEY: overlay wins.
	if !strings.Contains(body, "OVERWRITTEN_KEY: win") {
		t.Errorf("OVERWRITTEN_KEY should be 'win':\n%s", body)
	}
	// NEW_KEY: overlay added.
	if !strings.Contains(body, "NEW_KEY: added") {
		t.Errorf("NEW_KEY should be present:\n%s", body)
	}
	// Ports: overlay REPLACES base list (compose semantics).
	if strings.Contains(body, "1111:1111") {
		t.Errorf("base ports should be replaced by overlay ports:\n%s", body)
	}
	if !strings.Contains(body, "2222:2222") || !strings.Contains(body, "3333:3333") {
		t.Errorf("overlay ports should be present:\n%s", body)
	}
}

func TestRenderSourced_MissingFile_Errors(t *testing.T) {
	t.Parallel()
	_, err := renderSourced("/nonexistent/path/does-not-exist.yaml", nil, nodeCtxFor("x"))
	if err == nil {
		t.Fatal("expected error for missing source file")
	}
	if !strings.Contains(err.Error(), "read source") {
		t.Errorf("expected 'read source' in error: %v", err)
	}
}

// TestRenderSourced_MultiDocumentSourceHalts pins the loud guardrail: a single
// compose source file carrying `---`-separated documents passes the source
// resolver's file-count guard, but must not be silently parsed as just its
// first document. renderSourced halts with a CatalogError naming the
// multiple-documents condition rather than dropping every document after the
// first.
func TestRenderSourced_MultiDocumentSourceHalts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "multi.yaml")
	body := []byte(strings.TrimLeft(`
services:
  app:
    image: first:1.0
---
services:
  app2:
    image: second:2.0
`, "\n"))
	if err := os.WriteFile(srcPath, body, 0o644); err != nil {
		t.Fatalf("write multi-doc source: %v", err)
	}
	_, err := renderSourced(srcPath, nil, nodeCtxFor("app"))
	if err == nil {
		t.Fatal("expected a loud error for a multi-document compose source, got nil (silent first-doc drop)")
	}
	if !strings.Contains(err.Error(), "multiple YAML documents") {
		t.Errorf("expected the multiple-documents catalog error, got: %v", err)
	}
}

// TestRenderSourced_SingleDocumentSourceRenders is the non-vacuity counterpart:
// an ordinary single-document compose file still renders unaffected by the
// multi-document guard.
func TestRenderSourced_SingleDocumentSourceRenders(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "single.yaml")
	body := []byte(strings.TrimLeft(`
services:
  app:
    image: only:1.0
`, "\n"))
	if err := os.WriteFile(srcPath, body, 0o644); err != nil {
		t.Fatalf("write single-doc source: %v", err)
	}
	out, err := renderSourced(srcPath, nil, nodeCtxFor("app"))
	if err != nil {
		t.Fatalf("single-document source must render, got: %v", err)
	}
	if !strings.Contains(string(out["app.compose-fragment.yaml"]), "image: only:1.0") {
		t.Errorf("single-doc source not rendered:\n%s", out["app.compose-fragment.yaml"])
	}
}

func TestCompose_Engine_RegistersAsBoundaryAssembler(t *testing.T) {
	t.Parallel()
	var e renderer.Engine = &ComposeEngine{}
	if _, ok := e.(renderer.BoundaryAssembler); !ok {
		t.Fatal("ComposeEngine must implement renderer.BoundaryAssembler")
	}
	if _, ok := e.(renderer.WorkloadSynthesizer); !ok {
		t.Fatal("ComposeEngine must implement renderer.WorkloadSynthesizer")
	}
}

func TestCompose_NilSource_DefensiveInternalError(t *testing.T) {
	t.Parallel()
	// A nil source never reaches Render in a valid build: the central
	// input-kind gate dispatches Render only for sourced recipes and routes
	// synthesised mode to Phase B.4. There is no bare-values synthesis path,
	// so this arm is a defensive internal-error guard.
	_, err := (&ComposeEngine{}).Render(context.Background(), nil, renderer.NodeRenderContext{NodeID: "x"})
	if err == nil {
		t.Fatal("expected a defensive internal error for a nil-source compose render")
	}
	if !strings.Contains(err.Error(), "engine.Render dispatched with no source") {
		t.Errorf("error should be the defensive no-source internal error, got: %v", err)
	}
}

func TestCompose_EmptyStringSource_ErrEmptySource(t *testing.T) {
	t.Parallel()
	nodeCtx := renderer.NodeRenderContext{NodeID: "x", Source: ""}
	_, err := (&ComposeEngine{}).Render(context.Background(), nil, nodeCtx)
	if err == nil {
		t.Fatal("expected ErrEmptySource for empty string source")
	}
	if !errors.Is(err, ErrEmptySource) {
		t.Errorf("expected ErrEmptySource, got %v", err)
	}
}

// mapKeys returns the sorted keys of a string-keyed map (test helper).
func mapKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
