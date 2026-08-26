// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// renderSourcedFile reads a compose source file from disk and renders it through
// the filesystem-free sourced-mode core, mirroring how the host projects source
// bytes into the shared render core the provider guest also calls.
func renderSourcedFile(path string, values map[string]any, nodeCtx renderer.NodeRenderContext) (map[string][]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return RenderSourcedCore(raw, path, values, nodeCtx)
}

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
	files, _, err := SynthesizeWorkloadCore(w, nodeCtx)
	if err != nil {
		t.Fatalf("SynthesizeWorkloadCore(%s): %v", nodeID, err)
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

	files, _, err := AssembleBoundaryCore(info.IntermediatesByNode, info.NodeAnnotations, info.NodeSyncWave, info.Edges, info.BoundaryPath, info.YAMLIndent)
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

	files, _, err := AssembleBoundaryCore(info.IntermediatesByNode, info.NodeAnnotations, info.NodeSyncWave, info.Edges, info.BoundaryPath, info.YAMLIndent)
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

	files, _, err := AssembleBoundaryCore(info.IntermediatesByNode, info.NodeAnnotations, info.NodeSyncWave, info.Edges, info.BoundaryPath, info.YAMLIndent)
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

	_, _, err := AssembleBoundaryCore(info.IntermediatesByNode, info.NodeAnnotations, info.NodeSyncWave, info.Edges, info.BoundaryPath, info.YAMLIndent)
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

	files, _, err := AssembleBoundaryCore(info.IntermediatesByNode, info.NodeAnnotations, info.NodeSyncWave, info.Edges, info.BoundaryPath, info.YAMLIndent)
	if err != nil {
		t.Fatalf("AssembleBoundary: %v", err)
	}
	body := string(files[AssembledOutputFile])
	// The boundary path is written out rather than folded, so its case survives:
	// "STAGE-01".
	if !strings.Contains(body, "STAGE-01:") {
		t.Errorf("expected the network name 'STAGE-01' in output:\n%s", body)
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

	out, err := renderSourcedFile(basePath, overlay, nodeCtxFor("app"))
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
	_, err := renderSourcedFile(srcPath, nil, nodeCtxFor("app"))
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
	out, err := renderSourcedFile(srcPath, nil, nodeCtxFor("app"))
	if err != nil {
		t.Fatalf("single-document source must render, got: %v", err)
	}
	if !strings.Contains(string(out["app.compose-fragment.yaml"]), "image: only:1.0") {
		t.Errorf("single-doc source not rendered:\n%s", out["app.compose-fragment.yaml"])
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

// waveWorkload builds a one-container Workload publishing a single port, with a
// healthcheck when withHealth is set. The healthcheck is what makes the
// difference between the two wave-derived conditions observable.
func waveWorkload(image string, port int, withHealth bool) *types.Workload {
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: image}
	c.Ports.Set("p", port)
	if withHealth {
		c.Health = &types.HealthSpec{Live: &types.ProbeSpec{TCP: port}}
	}
	w.Containers.Set("main", c)
	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("p", port)
	w.Services.Set("svc", svc)
	return w
}

// TestComposeAssemble_SyncWaveOrdersServices proves a declared deployment wave
// orders the services of the merged compose file through depends_on, with the
// ruled condition on each entry, and that the ordering fans out to the
// immediately-preceding populated wave only.
//
// Three contributors at three waves: migrate at -1 (no healthcheck), db at 0
// (healthcheck), app at 1. So db waits on migrate gated on migrate's START —
// migrate cannot report healthy — and app waits on db gated on db's HEALTH. app
// must NOT name migrate: compose chains that through db, and an all-pairs fan-out
// would grow with the size of the whole group rather than with one wave.
func TestComposeAssemble_SyncWaveOrdersServices(t *testing.T) {
	t.Parallel()

	info := renderer.BoundaryInfo{
		BoundaryPath:  "cluster",
		AggregateName: "waved-app",
		IntermediatesByNode: map[string][]renderer.EmittedFile{
			"migrate": makeFragment(t, "migrate", waveWorkload("migrate:1.0", 9000, false), nil),
			"db":      makeFragment(t, "db", waveWorkload("postgres:15", 5432, true), nil),
			"app":     makeFragment(t, "app", waveWorkload("app:1.0", 8080, false), nil),
		},
		NodeAnnotations:        map[string]map[string]string{"migrate": {}, "db": {}, "app": {}},
		NodeSyncWave:           map[string]int{"migrate": -1, "db": 0, "app": 1},
		Edges:                  []renderer.IREdgeInfo{{Source: "app", Target: "db"}},
		ContributingBoundaries: []string{"cluster"},
		YAMLIndent:             2,
	}

	files, diags, err := AssembleBoundaryCore(info.IntermediatesByNode, info.NodeAnnotations, info.NodeSyncWave, info.Edges, info.BoundaryPath, info.YAMLIndent)
	if err != nil {
		t.Fatalf("AssembleBoundary: %v", err)
	}
	body := string(files[AssembledOutputFile])

	// The edge app->db and the wave app->db name the same pair, so one entry
	// carries the wave's condition rather than the pair appearing twice.
	if strings.Count(body, "db:") < 1 || strings.Count(body, "condition: service_healthy") != 1 {
		t.Errorf("expected exactly one service_healthy entry (app waiting on db)\n--- merged ---\n%s", body)
	}
	if strings.Count(body, "condition: service_started") != 1 {
		t.Errorf("expected exactly one service_started entry (db waiting on migrate)\n--- merged ---\n%s", body)
	}
	// app must not reach past db to migrate.
	appBlock := serviceBlock(t, body, "app")
	if strings.Contains(appBlock, "migrate") {
		t.Errorf("wave ordering fanned out past the immediately-preceding wave; app names migrate:\n%s", appBlock)
	}
	// migrate is in the lowest wave, so it waits on nothing.
	if strings.Contains(serviceBlock(t, body, "migrate"), "depends_on") {
		t.Errorf("the lowest wave must wait on nothing:\n%s", serviceBlock(t, body, "migrate"))
	}

	if len(diags) != 1 {
		t.Fatalf("expected exactly one advisory naming the start-only target; got %d: %+v", len(diags), diags)
	}
	for _, want := range []string{`"compose"`, "syncWave", `service "migrate"`, "declares no healthcheck", "wait for it to start"} {
		if !strings.Contains(diags[0].Message, want) {
			t.Errorf("advisory does not mention %q; got %q", want, diags[0].Message)
		}
	}

	goldenCheckAssembled(t, "sync-wave-three-waves", files[AssembledOutputFile])
}

// TestComposeAssemble_SyncWaveSingleWaveEmitsNoOrdering proves the byte-identity
// property the wave ordering must preserve: the very same contributors with no
// wave declared anywhere produce output carrying no wave-derived dependency and
// no advisory, so the assembled bytes are what they would be without the feature.
//
// The comparison is against a committed golden, and the two goldens must differ:
// a parity of two empty sets would pass whatever the emission did.
func TestComposeAssemble_SyncWaveSingleWaveEmitsNoOrdering(t *testing.T) {
	t.Parallel()

	intermediates := map[string][]renderer.EmittedFile{
		"migrate": makeFragment(t, "migrate", waveWorkload("migrate:1.0", 9000, false), nil),
		"db":      makeFragment(t, "db", waveWorkload("postgres:15", 5432, true), nil),
		"app":     makeFragment(t, "app", waveWorkload("app:1.0", 8080, false), nil),
	}
	annotations := map[string]map[string]string{"migrate": {}, "db": {}, "app": {}}
	edges := []renderer.IREdgeInfo{{Source: "app", Target: "db"}}

	// Every form of "no wave": an absent map, an all-zero map, and a map whose
	// single non-default value is shared by every contributor. All three carry one
	// distinct wave, so all three must emit identically.
	forms := map[string]map[string]int{
		"absent":  nil,
		"zeroes":  {"migrate": 0, "db": 0, "app": 0},
		"uniform": {"migrate": 4, "db": 4, "app": 4},
	}
	var reference string
	for _, name := range []string{"absent", "uniform", "zeroes"} {
		files, diags, err := AssembleBoundaryCore(intermediates, annotations, forms[name], edges, "cluster", 2)
		if err != nil {
			t.Fatalf("AssembleBoundary(%s): %v", name, err)
		}
		if len(diags) != 0 {
			t.Errorf("form %q raised advisories with no wave ordering to report: %+v", name, diags)
		}
		body := string(files[AssembledOutputFile])
		if strings.Contains(body, "condition:") {
			t.Errorf("form %q emitted a gated depends_on with a single distinct wave\n--- merged ---\n%s", name, body)
		}
		if reference == "" {
			reference = body
			continue
		}
		if body != reference {
			t.Errorf("form %q is not byte-identical to the absent-wave form\n--- got ---\n%s\n--- want ---\n%s", name, body, reference)
		}
	}

	goldenCheckAssembled(t, "sync-wave-single-wave", []byte(reference))
}

// serviceBlock returns the lines of the assembled document belonging to one
// service: the service's own key line and every following line indented deeper,
// stopping at the next key at the same indentation. It exists so an assertion can
// say "app does not name migrate" without a whole-document substring search,
// which cannot tell which service an occurrence belongs to.
func serviceBlock(t *testing.T, body, service string) string {
	t.Helper()
	lines := strings.Split(body, "\n")
	head := "  " + service + ":"
	for i, line := range lines {
		if line != head {
			continue
		}
		block := []string{line}
		for _, next := range lines[i+1:] {
			if next != "" && !strings.HasPrefix(next, "   ") {
				break
			}
			block = append(block, next)
		}
		return strings.Join(block, "\n")
	}
	t.Fatalf("service %q not found in the assembled document:\n%s", service, body)
	return ""
}
