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

	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// goldenCheckRelease compares actual against testdata/release/<name>.golden.yaml.
// Set UPDATE_GOLDEN=1 to overwrite.
func goldenCheckRelease(t *testing.T, name string, actual []byte) {
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

// makeFragment builds a one-node ReleaseNodeInfo by running SynthesizeWorkload.
// Used by Release-level tests to compose a realistic ReleaseInfo without
// hand-rolling fragment YAML.
func makeFragment(t *testing.T, nodeID string, w *types.Workload, annotations map[string]string) renderer.ReleaseNodeInfo {
	t.Helper()
	nodeCtx := nodeCtxFor(nodeID)
	nodeCtx.Annotations = annotations
	files, err := (&ComposeEngine{}).SynthesizeWorkload(context.Background(), nil, w, nodeCtx)
	if err != nil {
		t.Fatalf("SynthesizeWorkload(%s): %v", nodeID, err)
	}
	return renderer.ReleaseNodeInfo{
		NodeID:            nodeID,
		ResolvedComponent: "test/component",
		IntermediateFiles: files,
	}
}

func TestComposeRelease_SingleBoundarySingleNode(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("web", 8080)
	w.Containers.Set("main", c)
	pub := types.ServiceSpec{Type: "NodePort"}
	pub.Ports.Set("web", 8080)
	w.Services.Set("api", pub)

	node := makeFragment(t, "api", w, map[string]string{AnnotationNamespace: "production"})

	info := renderer.ReleaseInfo{
		AggregateName: "my-app",
		Boundaries: []renderer.ReleaseBoundaryInfo{
			{
				BoundaryPath: "default",
				Annotations:  map[string]map[string]string{"api": {AnnotationNamespace: "production"}},
				Nodes:        []renderer.ReleaseNodeInfo{node},
			},
		},
	}

	files, err := (&ComposeEngine{}).ComposeRelease(context.Background(), info)
	if err != nil {
		t.Fatalf("ComposeRelease: %v", err)
	}
	body, ok := files[ReleaseOutputFile]
	if !ok {
		t.Fatalf("expected %s key, got %v", ReleaseOutputFile, mapKeys(files))
	}
	goldenCheckRelease(t, "single-boundary-single-node", body)
}

func TestComposeRelease_MultiBoundaryServicesMerged(t *testing.T) {
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

	info := renderer.ReleaseInfo{
		AggregateName: "two-boundary-app",
		Boundaries: []renderer.ReleaseBoundaryInfo{
			{
				BoundaryPath: "edge",
				Annotations:  map[string]map[string]string{"frontend": {AnnotationNamespace: "edge-net"}},
				Nodes:        []renderer.ReleaseNodeInfo{makeFragment(t, "frontend", frontW, nil)},
			},
			{
				BoundaryPath: "core",
				Annotations:  map[string]map[string]string{"backend": {AnnotationNamespace: "core-net"}},
				Nodes:        []renderer.ReleaseNodeInfo{makeFragment(t, "backend", backW, nil)},
			},
		},
	}

	files, err := (&ComposeEngine{}).ComposeRelease(context.Background(), info)
	if err != nil {
		t.Fatalf("ComposeRelease: %v", err)
	}
	goldenCheckRelease(t, "multi-boundary-services-merged", files[ReleaseOutputFile])
}

func TestComposeRelease_DependsOnFromUsesEdges(t *testing.T) {
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

	info := renderer.ReleaseInfo{
		AggregateName: "depends-app",
		Boundaries: []renderer.ReleaseBoundaryInfo{
			{
				BoundaryPath: "default",
				Annotations:  map[string]map[string]string{},
				Nodes: []renderer.ReleaseNodeInfo{
					makeFragment(t, "api", apiW, nil),
					makeFragment(t, "db", dbW, nil),
				},
			},
		},
		Edges: []types.IREdge{
			{Source: "api", Target: "db"},
		},
	}

	files, err := (&ComposeEngine{}).ComposeRelease(context.Background(), info)
	if err != nil {
		t.Fatalf("ComposeRelease: %v", err)
	}
	goldenCheckRelease(t, "depends-on-from-uses-edges", files[ReleaseOutputFile])
}

func TestComposeRelease_ServiceCollisionAcrossBoundaries_Errors(t *testing.T) {
	t.Parallel()

	// Two boundaries each contributing a node whose container synthesises
	// a service named "auth" — collision via shared sidecar name.
	mkConflictWorkload := func(image string) *types.Workload {
		w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
		c := types.ContainerSpec{Image: image}
		c.Ports.Set("p", 8080)
		w.Containers.Set("main", c)
		s := types.ServiceSpec{Type: "ClusterIP"}
		s.Ports.Set("p", 8080)
		w.Services.Set("svc", s)
		return w
	}

	info := renderer.ReleaseInfo{
		AggregateName: "collision-app",
		Boundaries: []renderer.ReleaseBoundaryInfo{
			{
				BoundaryPath: "a",
				Annotations:  map[string]map[string]string{},
				Nodes:        []renderer.ReleaseNodeInfo{makeFragment(t, "auth", mkConflictWorkload("auth-a:1.0"), nil)},
			},
			{
				BoundaryPath: "b",
				Annotations:  map[string]map[string]string{},
				Nodes:        []renderer.ReleaseNodeInfo{makeFragment(t, "auth", mkConflictWorkload("auth-b:1.0"), nil)},
			},
		},
	}

	_, err := (&ComposeEngine{}).ComposeRelease(context.Background(), info)
	if err == nil {
		t.Fatal("expected TRANSLATION_ERROR for cross-boundary service collision")
	}
	if !strings.Contains(err.Error(), "TRANSLATION_ERROR") {
		t.Errorf("expected TRANSLATION_ERROR prefix, got: %v", err)
	}
	if !strings.Contains(err.Error(), `service "auth"`) {
		t.Errorf("error must name colliding service, got: %v", err)
	}
	if !strings.Contains(err.Error(), `boundary="a"`) || !strings.Contains(err.Error(), `boundary="b"`) {
		t.Errorf("error must name every contributing boundary, got: %v", err)
	}
}

func TestComposeRelease_NoNamespaceAnnotation_FallsBackToBoundaryPath(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "x:1.0"}
	c.Ports.Set("p", 80)
	w.Containers.Set("main", c)
	s := types.ServiceSpec{Type: "ClusterIP"}
	s.Ports.Set("p", 80)
	w.Services.Set("api", s)

	info := renderer.ReleaseInfo{
		AggregateName: "fallback-app",
		Boundaries: []renderer.ReleaseBoundaryInfo{
			{
				BoundaryPath: "STAGE-01",
				Annotations:  map[string]map[string]string{"x": {}}, // no namespace annotation
				Nodes:        []renderer.ReleaseNodeInfo{makeFragment(t, "x", w, nil)},
			},
		},
	}

	files, err := (&ComposeEngine{}).ComposeRelease(context.Background(), info)
	if err != nil {
		t.Fatalf("ComposeRelease: %v", err)
	}
	body := string(files[ReleaseOutputFile])
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

	overlay := map[string]interface{}{
		"services": map[string]interface{}{
			"app": map[string]interface{}{
				"image": "overlay:2.0",
				"environment": map[string]interface{}{
					"OVERWRITTEN_KEY": "win",
					"NEW_KEY":         "added",
				},
				"ports": []interface{}{"2222:2222", "3333:3333"},
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

func TestComposeRelease_Engine_RegistersAsReleaseComposer(t *testing.T) {
	t.Parallel()
	var e renderer.Engine = &ComposeEngine{}
	if _, ok := e.(renderer.ReleaseComposer); !ok {
		t.Fatal("ComposeEngine must implement renderer.ReleaseComposer")
	}
	if _, ok := e.(renderer.WorkloadSynthesizer); !ok {
		t.Fatal("ComposeEngine must implement renderer.WorkloadSynthesizer")
	}
}

func TestComposeRelease_BareValuesMode_RejectsAtRender(t *testing.T) {
	t.Parallel()
	// nil source + no Workload (Render reaches this path only when no
	// Workload is present, since orchestrator Phase B.4 dispatches Workload
	// to SynthesizeWorkload directly).
	_, err := (&ComposeEngine{}).Render(context.Background(), nil, renderer.NodeRenderContext{NodeID: "x"})
	if err == nil {
		t.Fatal("expected error for bare-values compose Recipe")
	}
	if !strings.Contains(err.Error(), "must declare workload: or source:") {
		t.Errorf("error should explain unsupported shape, got: %v", err)
	}
}

func TestComposeRelease_EmptyStringSource_ErrEmptySource(t *testing.T) {
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
