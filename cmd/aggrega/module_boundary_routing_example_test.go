// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// The module-boundary-routing worked example renders through the full build path
// against the sibling catalog. It is the end-to-end proof of what a boundary's
// aggregates: entry does: routing a module places every component that module
// contains into that boundary — including the components the routing entry never
// names — and a routed component is a component of that boundary, inheriting its
// annotation cascade and carrying whatever the routing entry configures on it.
//
// The example resolves against the sibling aggrega-hub catalog and the sibling
// aggrega-docs checkout, so the test skips when either is absent (a standalone
// aggrega-cli clone). It needs no OCI registry or other integration-only
// dependency, so it runs in the default suite rather than under
// -tags=integration.
//
// The assertions are structural rather than a stored copy of the rendered tree.
// Every emitted resource carries the compiler and provider versions as labels, so
// a byte-for-byte baseline would have to be regenerated on every release and
// would fail on changes that have nothing to do with placement. Determinism is
// proven instead by comparing two independent builds of the same inputs, which
// needs no baseline at all, and placement is proven by reading each resource's
// own component identity and boundary against the directory it landed in.

// moduleRoutingPlacement is the component set the example's two modules
// contribute, mapped to the boundary each module is routed into. Both boundaries
// name their directory after themselves, so the value is also the dist-relative
// directory every one of that component's artifacts must land under.
//
// The Release's routing entries name exactly one of these components (query, by
// its local name inside the metrics module). The other four are placed purely by
// routing, which is what makes the full set the property worth pinning.
var moduleRoutingPlacement = map[string]string{
	"metrics.objstore":  "observability",
	"metrics.store":     "observability",
	"metrics.query":     "observability",
	"documents.backend": "data",
	"documents.proxy":   "data",
}

// The well-known labels and annotations the assertions read. Each is emitted on
// every rendered resource: the component identity the resource belongs to, and
// the boundary that placed it.
const (
	componentIDLabel     = "aggrega.ai/component-id"
	boundaryPathAnnotate = "aggrega.ai/boundary-path"
)

// renderedResource is the slice of an emitted manifest the placement assertions
// read. Everything else in the manifest is the Recipe's business.
type renderedResource struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name        string            `yaml:"name"`
		Namespace   string            `yaml:"namespace"`
		Labels      map[string]string `yaml:"labels"`
		Annotations map[string]string `yaml:"annotations"`
	} `yaml:"metadata"`
}

// parseRenderedResource decodes one emitted manifest, failing the test when it
// does not parse or carries no component identity — either would make every
// placement assertion below vacuous.
func parseRenderedResource(t *testing.T, path string, raw []byte) renderedResource {
	t.Helper()
	var r renderedResource
	if err := yaml.Unmarshal(raw, &r); err != nil {
		t.Fatalf("parse emitted manifest %s: %v", path, err)
	}
	if r.Metadata.Labels[componentIDLabel] == "" {
		t.Fatalf("emitted manifest %s carries no %s label, so its placement cannot be read:\n%s", path, componentIDLabel, raw)
	}
	return r
}

// TestModuleBoundaryRoutingExampleRoutesModulesIntoBoundaries drives the
// module-boundary-routing example through two independent builds and asserts the
// rendered tree is byte-identical across them, that every component both modules
// contribute is placed under the boundary its module was routed into, that each
// placed component inherits that boundary's namespace annotation, that the
// per-component infra and Feature attachment the routing entry declares reach the
// one component it names, and that the resource the attached Feature emits
// carries that same namespace — a resource emitted inside a scope belongs to that
// scope whoever emitted it, so a Feature's artifact must not be left to land
// wherever the applying tool defaults to.
func TestModuleBoundaryRoutingExampleRoutesModulesIntoBoundaries(t *testing.T) {
	catalogRoot := siblingCatalogRootForExamples(t)
	src := workedExampleDir(t, "module-boundary-routing")

	// One overlay for both runs, so the two builds see byte-identical inputs.
	stack := exampleCatalogStack(t, catalogRoot, "provider/helm")

	run1 := buildExampleToDist(t, src, stack)
	run2 := buildExampleToDist(t, src, stack)
	assertByteIdenticalTrees(t, run1, run2)

	// Placement, read from each resource rather than from the file set: the
	// directory a resource landed in, the boundary it records, and the boundary
	// its component's module was routed into must all agree.
	placedNamespaces := map[string]string{}
	seen := map[string]bool{}
	for _, path := range sortedKeys(run1) {
		res := parseRenderedResource(t, path, run1[path])
		id := res.Metadata.Labels[componentIDLabel]
		want, ok := moduleRoutingPlacement[id]
		if !ok {
			t.Errorf("%s belongs to component %q, which neither module in this example contributes", path, id)
			continue
		}
		seen[id] = true

		if dir := strings.SplitN(path, "/", 2)[0]; dir != want {
			t.Errorf("%s: component %q emitted under directory %q; its module is routed into boundary %q", path, id, dir, want)
		}
		if got := res.Metadata.Annotations[boundaryPathAnnotate]; got != want {
			t.Errorf("%s: component %q records boundary %q; its module is routed into %q", path, id, got, want)
		}
		// The boundary's namespace annotation reaches a routed component the same
		// way it reaches a directly named one. Not every Recipe emits a namespaced
		// resource, so the assertion is that whatever namespace is emitted is the
		// routing boundary's — with the per-component check below requiring that
		// each component emits at least one.
		if ns := res.Metadata.Namespace; ns != "" {
			if ns != want {
				t.Errorf("%s: component %q emitted namespace %q; its routing boundary declares %q", path, id, ns, want)
			}
			placedNamespaces[id] = ns
		}
	}

	// Every component the two modules contribute is placed — named against the
	// expected set rather than counted, so a missing one is reported by name. The
	// expected set is walked in sorted order so a failure reports the same way
	// every run.
	expected := make([]string, 0, len(moduleRoutingPlacement))
	for id := range moduleRoutingPlacement {
		expected = append(expected, id)
	}
	sort.Strings(expected)
	for _, id := range expected {
		boundary := moduleRoutingPlacement[id]
		if !seen[id] {
			t.Errorf("component %q of the module routed into boundary %q emitted nothing; emitted paths: %v", id, boundary, sortedKeys(run1))
		}
		if placedNamespaces[id] != boundary {
			t.Errorf("component %q emitted no resource carrying its routing boundary's namespace %q (got %q); the boundary annotation cascade did not reach it", id, boundary, placedNamespaces[id])
		}
	}

	// The routing entry configures one component of the module it routes, by the
	// local name that component carries inside the module. Its sizing must reach
	// that component's workload and no other.
	query, _ := resourceOfKind(t, run1, "Deployment", "metrics.query")
	for _, want := range []string{"cpu: 500m", "memory: 1Gi"} {
		if !bytes.Contains(query, []byte(want)) {
			t.Errorf("the routed component's workload is missing the sizing the routing entry declares (%q):\n%s", want, query)
		}
	}

	// The Feature the routing entry attaches to that same component renders its
	// own artifact, under the routing boundary, carrying the operator's settings.
	monitor, monitorPath := resourceOfKind(t, run1, "ServiceMonitor", "metrics.query")
	for _, want := range []string{"port: http", "interval: 30s", "path: /metrics"} {
		if !bytes.Contains(monitor, []byte(want)) {
			t.Errorf("the attached Feature's artifact is missing the setting the routing entry declares (%q):\n%s", want, monitor)
		}
	}

	// A resource the Feature emits carries the boundary's namespace, exactly as
	// the component's own resources do. This is asserted positively, and about
	// this example's own ServiceMonitor rather than about kinds in general: a
	// resource that carries no namespace lands in whatever namespace the applying
	// tool selects, which need not be the boundary's, so a Feature's resource
	// would be applied outside the boundary whose component it describes while
	// that component's workload lands inside it. The per-resource check earlier in
	// this test only constrains a namespace that is present, and the per-component
	// check is satisfied by this component's Deployment, so without this assertion
	// the Feature's artifact could silently stop carrying one.
	monitorRes := parseRenderedResource(t, monitorPath, monitor)
	if want, got := moduleRoutingPlacement["metrics.query"], monitorRes.Metadata.Namespace; got != want {
		t.Errorf("%s: the attached Feature's artifact carries namespace %q; the component it describes is routed into boundary %q, whose namespace it must share:\n%s", monitorPath, got, want, monitor)
	}
}

// resourceOfKind returns the single emitted resource of the given kind belonging
// to the given component, with the path it was emitted at, failing when there is
// none or more than one — either makes an assertion over "the" resource
// meaningless. The path is returned so an assertion over the resource can name
// the file it read, and so a caller decoding it routes through the same
// parseRenderedResource every other assertion here uses.
func resourceOfKind(t *testing.T, tree map[string][]byte, kind, componentID string) ([]byte, string) {
	t.Helper()
	var hits []string
	for _, path := range sortedKeys(tree) {
		var r renderedResource
		if err := yaml.Unmarshal(tree[path], &r); err != nil {
			continue
		}
		if r.Kind == kind && r.Metadata.Labels[componentIDLabel] == componentID {
			hits = append(hits, path)
		}
	}
	switch len(hits) {
	case 1:
		return tree[hits[0]], hits[0]
	case 0:
		t.Fatalf("no emitted %s belongs to component %q; emitted paths: %v", kind, componentID, sortedKeys(tree))
	default:
		t.Fatalf("%d emitted %s resources belong to component %q, want exactly one: %v", len(hits), kind, componentID, hits)
	}
	return nil, ""
}
