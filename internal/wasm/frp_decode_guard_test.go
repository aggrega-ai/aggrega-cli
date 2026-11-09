// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// deploymentKey and serviceKey are the unit tokens the guard hands the
// guest. The guest never parses a token; it content-selects by tree and
// echoes the selected key back as a patch root, so the keys here are
// opaque addresses the guard checks the returned patch against.
const (
	deploymentKey = "api-deployment.yaml"
	serviceKey    = "api-svc-service.yaml"
)

// frpStepInputPayload builds an FRPStepInput payload faithful to the
// host's walk-time encoding: it constructs the real
// [frp.FRPStepInput] value — own set keyed by unit token, each unit's
// Tree carrying the document's RFC-6902-addressable view — and encodes
// it through the same generated binary encoder the production walk uses. The own
// set carries a Deployment and a Service so the guest's content-selection
// is exercised: the Deployment must be patched and the Service must be
// left untouched. A host unit is included so the host-scope EMIT path
// runs too.
func frpStepInputPayload(t *testing.T) []byte {
	t.Helper()

	deploymentTree := json.RawMessage(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"api","annotations":{"aggrega.ai/recipe-uri":"server-recipe:1.0.0"}}}`)
	serviceTree := json.RawMessage(`{"apiVersion":"v1","kind":"Service","metadata":{"name":"api-svc"}}`)
	hostTree := json.RawMessage(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"host-workload"}}`)

	var own types.OrderedMap[frp.Unit]
	own.Set(deploymentKey, frp.Unit{Token: deploymentKey, MediaType: "application/yaml", Tree: deploymentTree})
	own.Set(serviceKey, frp.Unit{Token: serviceKey, MediaType: "application/yaml", Tree: serviceTree})

	var host types.OrderedMap[frp.Unit]
	host.Set("host-deployment.yaml", frp.Unit{Token: "host-deployment.yaml", MediaType: "application/yaml", Tree: hostTree})

	in := frp.FRPStepInput{
		Documents: frp.DocumentSet{Own: own, Host: host},
		StepIndex: 0,
	}
	payload, err := in.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode FRPStepInput payload: %v", err)
	}
	return payload
}

// TestFRPDecodeGuard is the build-and-run regression guard for the
// Go-PDK FeatureRecipe-pipeline decode capability. It builds the Go
// reference plugin (standard Go, GOOS=wasip1), loads it through the live
// host gate, dispatches the step export over a realistic FRPStepInput,
// and asserts the guest decoded the reshaped wire and produced the
// reference behaviour: one own-scope PATCH on the content-selected
// Deployment (the Service untouched), one own-scope ConfigMap EMIT, and
// one host-scope marker EMIT. A future regression that breaks the decode
// fails this test loudly rather than silently degrading the Go PDK.
//
// The test also asserts byte-identical output across two invocations of
// the same input — the determinism contract the pipeline upholds.
//
// Skips cleanly when the PDK source or the Go wasip1 toolchain is absent.
func TestFRPDecodeGuard(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")

	loader := &wasm.Loader{}
	ctx := context.Background()

	plugin, desc, err := loader.LoadAndDescribe(ctx, wasm.PluginSpec{
		Name: "frp-step-go",
		Path: pluginPath,
	})
	if err != nil {
		t.Fatalf("LoadAndDescribe: %v", err)
	}
	defer plugin.Close(ctx)

	// Descriptor parity with the Rust reference plugin: a pipeline-step plugin
	// whose step export operates=patch / scope=own and declares the helm
	// and helm-chart compatible providers.
	if desc.PluginType != string(wasm.PluginTypePipelineStep) {
		t.Fatalf("plugin type: got %q want %q", desc.PluginType, wasm.PluginTypePipelineStep)
	}
	if desc.AbiVersion != wasm.ABIVersion {
		t.Fatalf("abi version: got %q want %q", desc.AbiVersion, wasm.ABIVersion)
	}
	if want := []string{"helm", "helm-chart"}; !equalStrings(desc.Capabilities, want) {
		t.Fatalf("compatible providers: got %v want %v", desc.Capabilities, want)
	}
	stepExport, ok := exportByName(desc, "step")
	if !ok {
		t.Fatal("descriptor does not declare the step export")
	}
	if stepExport.Operates != "patch" {
		t.Errorf("step export operates: got %q want %q", stepExport.Operates, "patch")
	}
	if stepExport.Scope != wasm.ScopeOwn {
		t.Errorf("step export scope: got %q want %q", stepExport.Scope, wasm.ScopeOwn)
	}

	payload := frpStepInputPayload(t)

	rawFirst, err := wasm.InvokeExport(ctx, plugin, desc, wasm.StepExport, payload)
	if err != nil {
		t.Fatalf("InvokeExport(step): %v", err)
	}

	// Decode into the real host output type through the decode the walk runs,
	// proving wire parity on the return leg too: the guest's bytes decode
	// strictly into frp.FRPStepOutput.
	var out frp.FRPStepOutput
	if err := wasm.DecodeGuestOutputCBOR(wasm.StepExport.Name(), rawFirst, &out); err != nil {
		t.Fatalf("decode FRPStepOutput from guest output %x: %v", rawFirst, err)
	}

	// Exactly one own-scope PATCH, rooted at the Deployment key the guest
	// content-selected; the Service was not a Deployment, so it carries no
	// patch.
	if len(out.Patches) != 1 {
		t.Fatalf("expected exactly one patch (the Deployment), got %d: %+v", len(out.Patches), out.Patches)
	}
	patch := out.Patches[0]
	if patch.Scope != frp.DocumentScopeOwn {
		t.Errorf("patch scope: got %q want %q", patch.Scope, frp.DocumentScopeOwn)
	}
	if patch.Key != deploymentKey {
		t.Errorf("patch key: got %q want the Deployment key %q (content-selection picked the wrong unit)", patch.Key, deploymentKey)
	}
	if patch.Path != "/metadata/annotations/aggrega.ai~1pipeline-patched" {
		t.Errorf("patch path: got %q", patch.Path)
	}
	if patch.Op != "add" {
		t.Errorf("patch op: got %q want %q", patch.Op, "add")
	}

	// Both EMITs landed: an own-scope ConfigMap and a host-scope marker.
	var ownEmit, hostEmit bool
	for _, se := range out.SideEffects {
		if se.Kind != "emit_file" || se.File == nil {
			continue
		}
		switch se.Scope {
		case frp.DocumentScopeOwn:
			ownEmit = true
			if se.File.Path != "pipeline-configmap.yaml" {
				t.Errorf("own EMIT path: got %q want %q", se.File.Path, "pipeline-configmap.yaml")
			}
		case frp.DocumentScopeHost:
			hostEmit = true
			if se.File.Path != "pipeline-host-marker.yaml" {
				t.Errorf("host EMIT path: got %q want %q", se.File.Path, "pipeline-host-marker.yaml")
			}
		}
	}
	if !ownEmit {
		t.Errorf("expected an own-scope ConfigMap EMIT; side effects: %+v", out.SideEffects)
	}
	if !hostEmit {
		t.Errorf("expected a host-scope marker EMIT; side effects: %+v", out.SideEffects)
	}

	// Determinism: a second invocation of the same input returns
	// byte-identical output.
	rawSecond, err := wasm.InvokeExport(ctx, plugin, desc, wasm.StepExport, payload)
	if err != nil {
		t.Fatalf("InvokeExport(step) second run: %v", err)
	}
	if !bytes.Equal(rawFirst, rawSecond) {
		t.Errorf("step output not byte-identical across two runs:\nfirst:  %x\nsecond: %x", rawFirst, rawSecond)
	}
}

// exportByName returns the named export from a descriptor.
func exportByName(desc wasm.PluginDescriptor, name string) (wasm.PluginExport, bool) {
	for _, e := range desc.Exports {
		if e.Name == name {
			return e, true
		}
	}
	return wasm.PluginExport{}, false
}

// equalStrings reports whether two string slices are element-wise equal.
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
