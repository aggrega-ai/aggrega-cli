// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// helmFactsNamespaceRelease is the fixture this file builds: four components in one
// boundary declaring the namespace "shop", each rendered by the helm provider in
// one of three ways, and each carrying a Feature whose template prints what the
// provider reported about its host's render and about each component calling it.
const helmFactsNamespaceRelease = "testdata/helm-render-facts-namespace/release.yaml"

// emittedDocument is one resource document a build wrote, as far as this proof
// reads it. The namespace is a pointer so a document writing none reads as nil
// rather than as an empty string.
type emittedDocument struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name      string  `yaml:"name"`
		Namespace *string `yaml:"namespace"`
	} `yaml:"metadata"`
	Data map[string]string `yaml:"data"`
}

// emittedDocuments reads every document of every file a build wrote, file by file
// in path order.
func emittedDocuments(t *testing.T, artifacts map[string][]byte) []emittedDocument {
	t.Helper()
	paths := make([]string, 0, len(artifacts))
	for p := range artifacts {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var out []emittedDocument
	for _, p := range paths {
		dec := yaml.NewDecoder(bytes.NewReader(artifacts[p]))
		for {
			var doc emittedDocument
			err := dec.Decode(&doc)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("read %s: %v", p, err)
			}
			out = append(out, doc)
		}
	}
	return out
}

// factLines is what the fixture's Feature prints for one component's render: one
// line per entry of each of the four facts, prefixed by whose render it read, for a
// component whose controller is the Deployment named controller and whose Service
// is named service, each written into namespace — "<absent>" where the entry
// carries none.
func factLines(prefix, controller, service, namespace string) []string {
	return []string{
		prefix + " pod_template_labels Deployment/" + controller + " namespace=" + namespace,
		prefix + " controller_selectors Deployment/" + controller + " namespace=" + namespace,
		prefix + " service_selectors Service/" + service + " namespace=" + namespace,
		prefix + " container_ports Deployment/" + controller + " namespace=" + namespace,
	}
}

// TestHelmRenderFacts_ReportTheNamespaceEachResourceIsEmittedInto builds the fixture
// through the real pipeline and the embedded helm provider, and reads what a
// FeatureRecipe template was handed: the host's own render facts at
// .context.render_facts, and, because the FeatureRecipe sets readsCallerRenderFacts,
// each caller's at .context.used_by.
//
// The boundary declares the namespace "shop", and its components render three
// ways. The gateway and api are synthesized Workloads, which write the boundary's
// namespace on every resource, so their entries carry "shop". web is a chart whose
// templates write no namespace: the provider places each of its namespaced
// resources in the boundary's namespace, so the documents it emits write "shop" and
// its entries carry it, read from the documents rather than supplied beside them.
// operator is a chart pinning its own namespace, which placement leaves alone, so
// its entries carry "operators-system" rather than the boundary's.
//
// Each case is read twice: as a host's own facts, by the Feature attached to that
// component, and as a caller's, by the Feature attached to the gateway, which every
// other component connects to. The emitted documents are checked first, so each
// row is known to read a document that writes what the row says.
func TestHelmRenderFacts_ReportTheNamespaceEachResourceIsEmittedInto(t *testing.T) {
	t.Parallel()

	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  helmFactsNamespaceRelease,
		CatalogPaths: []string{"testdata/helm-render-facts-namespace/catalog"},
	})
	docs := emittedDocuments(t, result.Artifacts)

	for _, want := range []struct{ kind, name, namespace string }{
		{"Deployment", "gateway", "shop"},
		{"Service", "gateway-web", "shop"},
		{"Deployment", "api", "shop"},
		{"Service", "api-web", "shop"},
		{"Deployment", "web-server", "shop"},
		{"Service", "web-server", "shop"},
		{"Deployment", "operator-manager", "operators-system"},
		{"Service", "operator-manager", "operators-system"},
	} {
		i := slices.IndexFunc(docs, func(d emittedDocument) bool {
			return d.Kind == want.kind && d.Metadata.Name == want.name
		})
		if i < 0 {
			t.Fatalf("the build emitted no %s %q, so the rows below read a render the fixture no longer produces", want.kind, want.name)
		}
		got := docs[i].Metadata.Namespace
		switch {
		case want.namespace == "" && got != nil:
			t.Fatalf("the emitted %s %q writes namespace %q; the row reading it expects a document writing none", want.kind, want.name, *got)
		case want.namespace != "" && (got == nil || *got != want.namespace):
			t.Fatalf("the emitted %s %q writes namespace %v; the row reading it expects %q", want.kind, want.name, got, want.namespace)
		}
	}

	read := map[string][]string{}
	for _, d := range docs {
		body, printed := d.Data["read"]
		if d.Kind != "ConfigMap" || !printed {
			continue
		}
		lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
		host, isHost := strings.CutPrefix(lines[0], "host ")
		if !isHost {
			t.Fatalf("a printed read does not open by naming its host:\n%s", body)
		}
		if _, twice := read[host]; twice {
			t.Fatalf("two attachments printed a read for host %q", host)
		}
		read[host] = lines[1:]
		t.Logf("the Feature on %s read:\n  %s", host, strings.Join(lines[1:], "\n  "))
	}

	shop := func(prefix, id string) []string { return factLines(prefix, id, id+"-web", "shop") }
	unpinned := func(prefix string) []string { return factLines(prefix, "web-server", "web-server", "shop") }
	pinned := func(prefix string) []string {
		return factLines(prefix, "operator-manager", "operator-manager", "operators-system")
	}
	for host, want := range map[string][]string{
		"gateway": slices.Concat(shop("own", "gateway"),
			shop("caller api", "api"), unpinned("caller web"), pinned("caller operator")),
		"api":      shop("own", "api"),
		"web":      unpinned("own"),
		"operator": pinned("own"),
	} {
		got, printed := read[host]
		if !printed {
			t.Errorf("no attachment printed a read for host %q", host)
			continue
		}
		got, want = slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))
		if !reflect.DeepEqual(got, want) {
			t.Errorf("the Feature on %s read\n  %s\nwant\n  %s", host, strings.Join(got, "\n  "), strings.Join(want, "\n  "))
		}
	}
}
