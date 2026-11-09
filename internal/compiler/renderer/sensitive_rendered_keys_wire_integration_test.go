// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Cross-package integration proof for the per-node sensitivity marking a Provider
// guest receives over its rendered keys, driven from a worked example's own
// authored catalog through the real render path and the real embedded guest.
//
// This test reads a worked example's catalog from the aggrega-docs repository
// and the standard catalog from the aggrega-hub repository, and compares its
// capture with the shared cross-language wire vector in the aggrega-abi
// repository, writing that vector under AGGREGA_UPDATE_GOLDEN=1. All three are
// read from checkouts beside this repository; check them out there first.

package renderer_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog/catalogtest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The worked example driven here is the multi-key credential synthesis one: its
// Component declares a single credential slot as handling secret material, and
// its Recipe's Workload references that one slot twice under two environment
// variable names while also declaring a third, ordinary variable. So one build
// produces both a rendered key whose identity is recoverable from a declaration
// and a rendered key whose identity is not, which is what makes presence and
// absence observable in the same payload rather than in two runs.
const (
	multikeyExampleCatalog = "../../../../aggrega-docs/language-reference/examples/multikey-secret-synth/catalog"
	multikeyHubCatalog     = "../../../../aggrega-hub/aggrega-catalog"

	// The shared vector corpus is one tree read by the Go, Rust and TypeScript
	// harnesses. The vector this test writes is a captured render payload rather
	// than an authored fixture, which is what makes it evidence: it is the
	// projection the host built for a real node of a real example.
	sharedVectorDir  = "../../../../aggrega-abi/testdata"
	sharedVectorName = "IRNodeProjectionSensitiveRenderedKeys"
)

// recordingSynthesizer wraps a real provider engine and records the two inputs
// the host hands the workload dispatch, then delegates so the real guest renders
// the node exactly as a build does. Recording the dispatch's own arguments — and
// building the projection from them through the host's one projection builder —
// is what keeps the observation at the boundary rather than beside it: a
// projection assembled from anything other than these two values would be a
// second construction, free to differ from the one that crossed.
//
// It embeds the concrete guest adapter rather than the Provider interface, and
// that is what keeps the render it observes a real one. The render reaches every
// capability beyond Render and Contract by asserting it on the engine it was
// handed — the name the target renders a node under, the bound on a composed
// name, artifact placement, input validation, the pipeline codec — and takes a
// quiet fallback where the assertion fails. An embedded interface promotes only
// its own two methods, so every one of those assertions would fail on the
// wrapper while the guest underneath implements them all: a Feature reading its
// host's rendered name would then be refused for a name the guest does report,
// and the checks with no refusal would simply not run. Embedding the adapter
// promotes its whole method set, including any capability added to it later;
// SynthesizeWorkload below is the one method replaced.
type recordingSynthesizer struct {
	*wasmengine.Engine
	nodeCtx  renderer.NodeRenderContext
	workload *types.Workload
	seen     bool
}

func (r *recordingSynthesizer) SynthesizeWorkload(
	ctx context.Context,
	tmplCtx *types.TemplateContext,
	w *types.Workload,
	nodeCtx renderer.NodeRenderContext,
) ([]renderer.EmittedFile, error) {
	r.nodeCtx = nodeCtx
	r.workload = w
	r.seen = true
	return r.Engine.SynthesizeWorkload(ctx, tmplCtx, w, nodeCtx)
}

// multikeyRenderInputs loads the worked example's own catalog plus the standard
// library it fills its credential slot from, and builds the render input for its
// single node. mutateComponent, when non-nil, is applied to the loaded Component
// before the lookups are taken, which is how the control below moves exactly one
// declaration.
func multikeyRenderInputs(t *testing.T, mutateComponent func(*types.Component)) (renderer.RendererInput, renderer.RecipeLookup) {
	t.Helper()

	// The standard library is loaded without its Plugin entities
	// (catalogtest.DirsWithoutPluginEntities says why). The render reads none: its
	// provider guest comes from the binary, the node and the Release carry no
	// annotation a provider's declared support would be read for, and no Recipe it
	// renders declares a pipeline step.
	dirs := append(catalogtest.DirsWithoutPluginEntities(t, multikeyHubCatalog), multikeyExampleCatalog)
	cat, err := catalog.LoadAll("", dirs...)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d error(s); first: %v", len(extErrs), extErrs[0])
	}
	lookup := cat.CatalogLookup()
	if mutateComponent != nil {
		comp, found := lookup.Component("local/compute/app:1.0.0")
		if !found {
			t.Fatal("example Component local/compute/app:1.0.0 did not resolve from the loaded catalog")
		}
		clone := comp
		mutateComponent(&clone)
		inner := lookup.Component
		lookup.Component = func(ref string) (types.Component, bool) {
			if ref == "local/compute/app:1.0.0" || ref == "local/compute/app" {
				return clone, true
			}
			return inner(ref)
		}
	}

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("app", types.IRNode{Component: "local/compute/app:1.0.0", BoundaryPath: "helm"})

	valuesMapping := types.OrderedMap[json.RawMessage]{}
	valuesMapping.Set("app", json.RawMessage(`{"greeting":"hi"}`))
	infraMapping := types.OrderedMap[json.RawMessage]{}
	infraMapping.Set("app", json.RawMessage(`{"image_tag":"1.2"}`))

	// The credential fill the example's Aggregate authors: one multi-key
	// referenceable credential carrying the two data keys the container reads.
	attachmentValues := types.OrderedMap[any]{}
	attachmentValues.Set("entries", []any{
		map[string]any{"key": "db_username", "value": "appuser"},
		map[string]any{"key": "db_password", "value": "dev-pw-change-me"},
	})
	inner := types.OrderedMap[types.IRFeatureEntry]{}
	inner.Set("app_credentials", types.IRFeatureEntry{
		Type: "core/referenced-secret-set:1.0.0",
		// The per-attachment roll-up the graph carries for this fill, set here to
		// the value the graph build derives for it. It is deliberately left set in
		// the control below, where the slot declares nothing: the roll-up then
		// still says this attachment handles secret material while no slot
		// declaration does, so a marking that consulted the roll-up would mark and
		// the control would fail. That is what makes the control prove which of the
		// two sources the marking reads, and not merely that it reads something.
		Sensitive: true,
		Values:    attachmentValues,
	})
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set("app", inner)

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "multikey-secret-synth"},
			Spec: types.IRSpec{
				Nodes:                nodes,
				ValuesMapping:        valuesMapping,
				InfraMapping:         infraMapping,
				FeatureValuesMapping: fvm,
				Provenance:           types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder:  ir.FeatureOrder{"app": {"app_credentials"}},
		CatalogLookup: lookup,
		ResolveResult: &resolver.ResolveResult{
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "multikey-secret-synth"}},
			Release:   &types.Release{},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{
				"app": {BoundaryPath: "helm"},
			},
		},
	}

	componentTier := &types.MappingTier{}
	componentTier.Type.Set("local/compute/app:1.0.0", "app")
	featureTier := &types.MappingTier{}
	featureTier.Type.Set("core/referenced-secret-set:1.0.0", "k8s-secret-set:1.0.0")
	manifest := &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{
		Components: componentTier,
		Features:   featureTier,
	}}}

	return input, cat.RecipeLookupWithManifest(manifest)
}

// renderMultikeyAndCapture runs the real render over the example and returns the
// projection the host built for its workload dispatch, together with the workload
// the same dispatch carried.
func renderMultikeyAndCapture(t *testing.T, mutateComponent func(*types.Component)) (renderer.IRNodeProjection, *types.Workload) {
	t.Helper()

	input, recipeLookup := multikeyRenderInputs(t, mutateComponent)
	base := wasmEngineFactory(t)
	var rec *recordingSynthesizer
	factory := func(providerRef string) (renderer.Provider, error) {
		engine, err := base(providerRef)
		if err != nil {
			return nil, err
		}
		if rec == nil {
			guest, ok := engine.(*wasmengine.Engine)
			if !ok {
				return nil, fmt.Errorf("provider %q resolved to %T, not the render adapter the recorder wraps", providerRef, engine)
			}
			rec = &recordingSynthesizer{Engine: guest}
		}
		return rec, nil
	}

	if _, err := renderer.RenderAll(context.Background(), input, recipeLookup, factory); err != nil {
		// The error's own text is its outermost frame alone; the condition that
		// halted and the template that raised it sit beneath it.
		code, _ := cerrors.ShortCodeOf(err)
		t.Fatalf("RenderAll over the worked example: [%s] %s", code, cerrors.CauseChain(err))
	}
	if rec == nil || !rec.seen {
		t.Fatal("the render never dispatched a workload synthesis, so nothing was observed at the boundary")
	}

	// The host's one projection builder, fed the dispatch's own two arguments —
	// the same call the provider engine makes for this shape.
	return renderer.NodeProjection(rec.nodeCtx, types.OrderedMap[any]{}, rec.workload), rec.workload
}

// TestSensitiveRenderedKeys_WorkloadPathOverWorkedExample proves the marking on a
// real render of a worked example, in one build, for both directions at once: the
// projection names the two rendered environment variables whose identity is
// recovered from the Component's own slot declaration, and does NOT name the
// ordinary variable the same container renders beside them. It also holds a
// control that moves exactly one declaration, so the marking is attributable to
// the slot declaration rather than to an attachment being present at all.
func TestSensitiveRenderedKeys_WorkloadPathOverWorkedExample(t *testing.T) {
	t.Parallel()

	proj, workload := renderMultikeyAndCapture(t, nil)

	// The rendered keys the example's container actually declares, read off the
	// evaluated workload the dispatch carried rather than restated here: the
	// contrast is only meaningful if the public key is really on the payload.
	main, ok := workload.Containers.Get("main")
	if !ok {
		t.Fatal("the evaluated workload carries no 'main' container")
	}
	if _, ok := main.Variables.Get("GREETING"); !ok {
		t.Fatalf("the evaluated workload's main container declares no GREETING variable; variables = %v", main.Variables.Keys())
	}
	for _, key := range []string{"APP_DB_USER", "APP_DB_PASSWORD"} {
		if _, ok := main.Attachments.Get(key); !ok {
			t.Fatalf("the evaluated workload's main container declares no %s attachment; attachments = %v", key, main.Attachments.Keys())
		}
	}

	// PRESENCE: each recovered key, mapped to the authored coordinate of the
	// declaration it inherited its sensitivity from. Both entries read the same
	// slot, so both carry that slot's coordinate.
	want := map[string]string{
		"APP_DB_USER":     "featuresSchema.app_credentials",
		"APP_DB_PASSWORD": "featuresSchema.app_credentials",
	}
	for key, wantDecl := range want {
		gotDecl, marked := proj.SensitiveRenderedKeys[key]
		if !marked {
			t.Errorf("rendered key %q is not marked; the projection carries %v", key, proj.SensitiveRenderedKeys)
			continue
		}
		if gotDecl != wantDecl {
			t.Errorf("rendered key %q names declaration %q, want %q", key, gotDecl, wantDecl)
		}
	}

	// ABSENCE, from the same build: the public rendered key is not named, and
	// neither is anything else. Naming the whole difference rather than comparing
	// counts, because two sets short by one still agree on size.
	if _, marked := proj.SensitiveRenderedKeys["GREETING"]; marked {
		t.Error("the public rendered variable GREETING is marked sensitive; a public value marked sensitive is hidden from an operator who needed it")
	}
	for key := range proj.SensitiveRenderedKeys {
		if _, expected := want[key]; !expected {
			t.Errorf("the projection marks %q, which no declaration names", key)
		}
	}

	// The workload shape carries no node values, so the values join contributes
	// nothing here and the keys above can only have come from the slot join.
	if proj.Values.Len() != 0 {
		t.Errorf("the workload dispatch carries %d node value(s); the marking's provenance is only unambiguous while that map is empty", proj.Values.Len())
	}

	// CONTROL: exactly one declaration moves — the slot stops declaring secret
	// material — and the marking disappears while the attachments, the workload
	// and the render are otherwise identical. It separates three readings a green
	// assertion above cannot: that the marking is not "every attachment entry",
	// that it is not the attachment's own per-attachment roll-up (which the
	// control leaves asserting secret material), and that it is the slot
	// declaration.
	t.Run("control_slot_declares_nothing", func(t *testing.T) {
		controlProj, controlWorkload := renderMultikeyAndCapture(t, func(c *types.Component) {
			slots := types.OrderedMap[types.AttachmentSlot]{}
			for name, slot := range c.Spec.FeaturesSchema.All() {
				slot.Sensitive = false
				slots.Set(name, slot)
			}
			c.Spec.FeaturesSchema = slots
		})
		controlMain, ok := controlWorkload.Containers.Get("main")
		if !ok {
			t.Fatal("the control's evaluated workload carries no 'main' container")
		}
		if got, want := strings.Join(controlMain.Attachments.Keys(), ","), strings.Join(main.Attachments.Keys(), ","); got != want {
			t.Fatalf("the control moved more than one variable: attachment keys %q, want %q", got, want)
		}
		entry, ok := controlProj.Features.Get("app_credentials")
		if !ok || !entry.Sensitive {
			t.Fatal("the control's attachment lost its per-attachment marking, so it no longer separates that source from the slot declaration")
		}
		if len(controlProj.SensitiveRenderedKeys) != 0 {
			t.Errorf("the control marks %v; with no slot declaring secret material nothing is recoverable, and the per-attachment roll-up is not a substitute for a declaration", controlProj.SensitiveRenderedKeys)
		}
	})
}

// TestSensitiveRenderedKeys_SharedWireVector pins the captured projection as the
// cross-language wire vector, which is what closes the coverage hole the field's
// elision leaves: it is omitted from the payload when empty, so a codec that
// dropped it would move no byte of any vector that does not populate it. The JSON
// document written here is the vector the Go, Rust and TypeScript harnesses each
// encode and compare, so the three codecs are held to one populated payload.
//
// Regenerate with AGGREGA_UPDATE_GOLDEN=1.
func TestSensitiveRenderedKeys_SharedWireVector(t *testing.T) {
	t.Parallel()

	proj, _ := renderMultikeyAndCapture(t, nil)

	// Marshalled through the generated host codec — the same encoder the dispatch
	// uses — so what is asserted here is the wire document rather than a Go value.
	hostEncoded, err := json.Marshal(proj)
	if err != nil {
		t.Fatalf("marshal the captured projection: %v", err)
	}

	// The marking has to be ON the marshalled document, not merely on the Go
	// value: a codec that dropped the field would leave the assertions above
	// passing and this vector silent.
	const markingKey = `"sensitive_rendered_keys":{"APP_DB_PASSWORD":"featuresSchema.app_credentials","APP_DB_USER":"featuresSchema.app_credentials"}`
	if !strings.Contains(string(hostEncoded), markingKey) {
		t.Fatalf("the host-marshalled projection does not carry the marking as %s:\n%s", markingKey, hostEncoded)
	}

	// The shared corpus is the GUEST codec's canonical form, and the two codecs
	// generated from this one schema do not agree about an empty ordered map: the
	// host emits it and all three guests elide it. So the captured payload is
	// re-encoded through the guest codec before it is pinned, which normalises
	// exactly that difference and nothing else. The marking is asserted to survive
	// the normalisation verbatim, so the step cannot quietly reshape the subject.
	var canonical abi.IRNodeProjection
	if err := json.Unmarshal(hostEncoded, &canonical); err != nil {
		t.Fatalf("decode the captured projection through the shared codec: %v", err)
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		t.Fatalf("re-marshal the captured projection through the shared codec: %v", err)
	}
	got := string(encoded)
	if !strings.Contains(got, markingKey) {
		t.Fatalf("the marking did not survive re-encoding through the shared codec:\n%s", got)
	}

	path := filepath.Join(sharedVectorDir, sharedVectorName+".golden.json")
	if os.Getenv("AGGREGA_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s (run AGGREGA_UPDATE_GOLDEN=1 to create): %v", path, err)
	}
	if string(want) != got {
		t.Errorf("the captured projection is not byte-identical to the committed vector\n golden: %s\n    got: %s", want, got)
	}
}
