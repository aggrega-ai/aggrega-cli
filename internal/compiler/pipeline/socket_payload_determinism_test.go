// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/importersocket"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog/catalogtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// The documentation examples these proofs compile, and the standard-library
// catalog the examples' own workspace configuration builds them against.
//
// Both proofs load that catalog without its Plugin entities
// (catalogtest.DirsWithoutPluginEntities says why). Neither subject reads one:
// the compilation runs with no provider binding and no governance attachment, so
// it resolves no plugin through the catalog, and the importer's input projects
// the catalog's Components alone.
const (
	payloadExamplesDir = "../../../../aggrega-docs/language-reference/examples"
	payloadHubCatalog  = "../../../../aggrega-hub/aggrega-catalog"

	// payloadImportExport is a real Langflow export, the source an importer is
	// handed beside the catalog projection.
	payloadImportExport = "../importersocket/testdata/real-exports/langflow/Research Translation Loop.json"

	// buildsPerExample is how many independent builds of one example are compared.
	// A container filled by ranging over a Go map takes a different order on a
	// different range with a probability that grows with every build compared, so
	// several builds are what let a nondeterministic fill show as a difference
	// rather than pass by agreeing with itself once.
	buildsPerExample = 5
)

// payloadExamples are the two documentation examples compiled. Between them they
// populate most of the maps the IR and socket schemas mark order-preserving: node
// labels and catalog annotations, the values and infra mappings, the per-node
// attachment mapping with its entries' labels, catalog annotations and
// provenance, nested boundaries, a sub-aggregate's components, and a node whose
// payload carries a leaf its Component declares sensitive.
var payloadExamples = []struct {
	name     string
	release  string
	manifest string
}{
	{name: "annotation-driven-monitoring", release: "release.yaml", manifest: "manifest.yaml"},
	{name: "keycloak-stack", release: "release-helm-synth.yaml", manifest: "manifest-helm-synth.yaml"},
}

// payloadAttachmentSettings stands in for an attachment's free-form settings
// object, so the payloads that carry one are compared with it present. Its keys
// are written out of key order on purpose.
func payloadAttachmentSettings() map[string]any {
	return map[string]any{
		"zeta":        "last",
		"label_value": "audited",
		"rules": []any{
			map[string]any{"set": map[string]any{"zz-last": "y", "a-first": "x"}},
		},
	}
}

// encodedPayload is one socket payload in one encoding, named for the failure
// message.
type encodedPayload struct {
	name  string
	bytes []byte
}

// encodeTwice encodes one assembled payload twice and fails the test unless the
// two encodings are byte-identical, so an encoder whose own output varies is
// caught before builds are compared at all.
func encodeTwice(t *testing.T, name string, encode func() ([]byte, error)) encodedPayload {
	t.Helper()
	first, err := encode()
	if err != nil {
		t.Fatalf("%s: encode: %v", name, err)
	}
	second, err := encode()
	if err != nil {
		t.Fatalf("%s: encode again: %v", name, err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("%s: one assembled payload encoded to two byte sequences: %s", name, describeDifference(first, second))
	}
	return encodedPayload{name: name, bytes: first}
}

// describeDifference names the first byte offset at which two encodings part
// and shows a short window of each from there, which is where a reader starts
// looking; a whole graph's encoding printed twice would bury it.
func describeDifference(want, got []byte) string {
	at := 0
	for at < len(want) && at < len(got) && want[at] == got[at] {
		at++
	}
	window := func(b []byte) []byte {
		end := min(at+48, len(b))
		return b[at:end]
	}
	return fmt.Sprintf("lengths %d and %d, first difference at byte %d:\n want: %x\n  got: %x",
		len(want), len(got), at, window(want), window(got))
}

// assembleBuildPayloads compiles one documentation example through the real
// pre-render pipeline and assembles every socket payload the run hands a guest,
// each through the function the socket itself calls, over the products this run
// produced:
//
//   - the label-transform role's input, from the flattened composition the
//     socket is handed, by topology.AssembleMutatorInput;
//   - the annotation-transform role's input, from the resolved Release and
//     Manifest, by annotationTargets and the Manifest's annotation registry;
//   - the IR graph itself;
//   - the ir-verdict role's input, from the analysis this run produced, with the
//     graph's declared-sensitive leaves withheld exactly as the socket withholds
//     them.
//
// Each payload is encoded in the encoding its socket carries and in the binary
// encoding its codec writes, so a socket that moves keeps the property.
func assembleBuildPayloads(t *testing.T, dir, release, manifest string) []encodedPayload {
	t.Helper()
	aggregatePath := filepath.Join(dir, "aggregate.yaml")
	out, catalogLookup := compileWorkspace(t, workspaceFixture{
		aggregatePath: aggregatePath,
		releasePath:   filepath.Join(dir, release),
		manifestPath:  filepath.Join(dir, manifest),
		catalogDirs:   catalogtest.DirsWithoutPluginEntities(t, payloadHubCatalog),
	}, bytesOf(t, aggregatePath))
	if len(out.Analysis.Errors) > 0 {
		t.Fatalf("the example does not compile cleanly, so its payloads are not the ones a build hands a guest: %v", out.Analysis.Errors[0])
	}
	rr := out.ResolveResult
	settings := payloadAttachmentSettings()
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}

	mi, err := topology.AssembleMutatorInput(out.Topology, catalogLookup, mutatorCompilationContext(rr.Release),
		[]topology.MutatorSource{{Path: "rules.policy", Content: "label rules\n"}}, settings)
	if err != nil {
		t.Fatalf("assemble the label-transform input: %v", err)
	}
	if mi.Graph.Nodes.Len() < 2 {
		t.Fatalf("the label-transform input carries %d node(s); the comparison needs a container with more than one entry to order", mi.Graph.Nodes.Len())
	}
	for id, node := range mi.Graph.Nodes.All() {
		if node.Labels.Len() == 0 {
			t.Fatalf("node %q carries no label, so the label order is not exercised", id)
		}
	}

	targets, _ := annotationTargets(rr.Release)
	if len(targets) < 2 {
		t.Fatalf("the annotation-transform input carries %d target(s); want at least two", len(targets))
	}
	ai := AnnotationTransformInput{
		Targets:     targets,
		BindingKeys: resolver.BuildAnnotationRegistry(rr.Manifest).Keys(),
		Sources:     []AnnotationSource{{Path: "rules.policy", Content: "annotation rules\n"}},
		Settings:    settingsJSON,
	}

	graph := out.IRGraph
	if graph.Spec.Nodes.Len() < 2 || graph.Spec.FeatureValuesMapping.Len() == 0 {
		t.Fatalf("the graph carries %d node(s) and %d attachment bucket(s); the comparison needs both populated",
			graph.Spec.Nodes.Len(), graph.Spec.FeatureValuesMapping.Len())
	}

	if out.Analysis.PolicyInput == nil {
		t.Fatal("the analysis produced no ir-verdict payload")
	}
	pi := *out.Analysis.PolicyInput
	pi.Graph = *redact.Graph(&out.Analysis.PolicyInput.Graph)
	pi.Settings = settings

	return []encodedPayload{
		encodeTwice(t, "label-transform input, binary", mi.EncodeCBOR),
		encodeTwice(t, "label-transform input, text", mi.MarshalJSON),
		encodeTwice(t, "annotation-transform input, text", func() ([]byte, error) { return json.Marshal(ai) }),
		encodeTwice(t, "annotation-transform input, binary", ai.EncodeCBOR),
		encodeTwice(t, "IR graph, text", func() ([]byte, error) { return json.Marshal(graph) }),
		encodeTwice(t, "IR graph, binary", graph.EncodeCBOR),
		encodeTwice(t, "ir-verdict input, text", func() ([]byte, error) { return json.Marshal(pi) }),
		encodeTwice(t, "ir-verdict input, binary", pi.EncodeCBOR),
	}
}

// TestSocketPayloadsAreByteIdenticalAcrossBuilds compiles each documentation
// example several times, each time from a fresh load of its documents and
// catalog, assembles the socket payloads every build hands a guest, and requires
// every build's payloads to be byte-identical to the first build's.
//
// Identical input producing identical bytes is the property every payload owes.
// A map the schema marks order-preserving travels in the order it holds, so the
// order the build FILLED it in reaches the wire: a fill that ranged over a Go map
// would make one build's bytes differ from the next. Comparing independent builds
// is what catches that, because one build encoded twice agrees with itself
// whatever order it filled in.
func TestSocketPayloadsAreByteIdenticalAcrossBuilds(t *testing.T) {
	t.Parallel()
	for _, ex := range payloadExamples {
		t.Run(ex.name, func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join(payloadExamplesDir, ex.name)
			first := assembleBuildPayloads(t, dir, ex.release, ex.manifest)
			for build := 2; build <= buildsPerExample; build++ {
				again := assembleBuildPayloads(t, dir, ex.release, ex.manifest)
				for i, p := range again {
					if !bytes.Equal(p.bytes, first[i].bytes) {
						t.Errorf("build %d: %s differs from the first build's: %s", build, p.name, describeDifference(first[i].bytes, p.bytes))
					}
				}
			}
		})
	}
}

// TestImporterInputIsByteIdenticalAcrossCatalogLoads assembles the importer's
// input the way an import run does — the projection of the catalog the import
// resolved against, beside the source bytes — from several independent loads of
// the standard-library catalog the documentation examples build against, and
// requires every load's payload to be byte-identical to the first.
func TestImporterInputIsByteIdenticalAcrossCatalogLoads(t *testing.T) {
	t.Parallel()
	source, err := os.ReadFile(payloadImportExport)
	if err != nil {
		t.Fatalf("read the export: %v", err)
	}
	catalogDirs := catalogtest.DirsWithoutPluginEntities(t, payloadHubCatalog)
	assemble := func() encodedPayload {
		t.Helper()
		cat, err := catalog.LoadAll("", catalogDirs...)
		if err != nil {
			t.Fatalf("catalog load: %v", err)
		}
		view := importersocket.ProjectCatalog(cat)
		if view == nil || len(view.Components) < 2 {
			t.Fatal("the catalog projects fewer than two Components, so the projection's order is not exercised")
		}
		in := abi.ImportInput{Format: "langflow", Bytes: source, Catalog: view}
		return encodeTwice(t, "importer input, binary", in.EncodeCBOR)
	}
	first := assemble()
	for load := 2; load <= buildsPerExample; load++ {
		if again := assemble(); !bytes.Equal(again.bytes, first.bytes) {
			t.Errorf("load %d: %s differs from the first load's: %s", load, again.name, describeDifference(first.bytes, again.bytes))
		}
	}
}
