// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types_test

import (
	"errors"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

const validWasmPlugin = `apiVersion: aggrega.ai/v1alpha1
kind: WasmPlugin
metadata:
  name: publishing-linter
  version: 1.0.0
spec:
  plugin: oci://hub.aggrega.ai/wasm/linter/publishing-linter@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
  pluginTypes: [linter, frp_step]
  abiVersion: v1alpha1
  signature:
    type: sigstore-cosign
    publicKeyRef: cosign.pub
  runtime:
    maxMemoryPages: 16
    timeoutMs: 5000
    declaredHostFunctions: [log]
    configKV:
      ZEBRA: last
      ALPHA: first
      MIKE: middle
`

// TestWasmPlugin_EnvelopeAcceptsValidInstance is the positive control: a
// fully-populated WasmPlugin validates through the registered kind with zero
// findings, proving the kind is wired into the envelope validator.
func TestWasmPlugin_EnvelopeAcceptsValidInstance(t *testing.T) {
	t.Parallel()
	if _, err := envelope.ValidateBytes(envelope.KindWasmPlugin, []byte(validWasmPlugin), "plugin.yaml"); err != nil {
		t.Fatalf("valid WasmPlugin rejected: %v", err)
	}
}

// TestWasmPlugin_RoundTripPreservesConfigKVOrder proves the entity unmarshals
// into the typed struct and re-marshals with configKV in author order — the
// determinism contract for the ordered string→string runtime map.
func TestWasmPlugin_RoundTripPreservesConfigKVOrder(t *testing.T) {
	t.Parallel()
	var wp types.WasmPlugin
	if err := yaml.Unmarshal([]byte(validWasmPlugin), &wp); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if wp.Spec.Runtime == nil {
		t.Fatal("runtime block lost on unmarshal")
	}
	gotKeys := wp.Spec.Runtime.ConfigKV.Keys()
	wantKeys := []string{"ZEBRA", "ALPHA", "MIKE"}
	if len(gotKeys) != len(wantKeys) {
		t.Fatalf("configKV keys = %v, want %v", gotKeys, wantKeys)
	}
	for i := range wantKeys {
		if gotKeys[i] != wantKeys[i] {
			t.Fatalf("configKV author order not preserved: got %v, want %v", gotKeys, wantKeys)
		}
	}

	// Re-marshal and re-parse: the key order survives the round-trip.
	out, err := yaml.Marshal(&wp)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	var again types.WasmPlugin
	if err := yaml.Unmarshal(out, &again); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if got := again.Spec.Runtime.ConfigKV.Keys(); len(got) != 3 || got[0] != "ZEBRA" || got[2] != "MIKE" {
		t.Fatalf("configKV order drifted across round-trip: %v", got)
	}
}

// TestWasmPlugin_OmittedOptionalBlocksRoundTripClean proves a minimal entity
// (no signature, no runtime) round-trips without re-emitting an empty block —
// the reason the nested blocks are pointer-typed.
func TestWasmPlugin_OmittedOptionalBlocksRoundTripClean(t *testing.T) {
	t.Parallel()
	const minimal = `apiVersion: aggrega.ai/v1alpha1
kind: WasmPlugin
metadata:
  name: p
  version: 1.0.0
spec:
  plugin: oci://r@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
  pluginTypes: [provider]
  abiVersion: v1alpha1
`
	var wp types.WasmPlugin
	if err := yaml.Unmarshal([]byte(minimal), &wp); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if wp.Spec.Signature != nil || wp.Spec.Runtime != nil {
		t.Fatalf("optional blocks should be nil when omitted; got signature=%v runtime=%v", wp.Spec.Signature, wp.Spec.Runtime)
	}
	out, err := yaml.Marshal(&wp)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	// The re-marshalled document must validate (no `signature: {}` /
	// `runtime: {}` blocks that would violate required-field constraints).
	if _, err := envelope.ValidateBytes(envelope.KindWasmPlugin, out, "min.yaml"); err != nil {
		t.Fatalf("re-marshalled minimal plugin failed envelope: %v\n%s", err, out)
	}
}

// TestWasmPlugin_EnvelopeRejectsMalformed proves the envelope schema halts the
// authoring mistakes the kind must catch: a missing plugin reference, a bad
// content-address form, and an unknown plugin type.
func TestWasmPlugin_EnvelopeRejectsMalformed(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"missing plugin ref": `apiVersion: aggrega.ai/v1alpha1
kind: WasmPlugin
metadata: {name: p, version: 1.0.0}
spec:
  pluginTypes: [linter]
  abiVersion: v1alpha1
`,
		"bad oci@sha256 form": `apiVersion: aggrega.ai/v1alpha1
kind: WasmPlugin
metadata: {name: p, version: 1.0.0}
spec:
  plugin: oci://hub.aggrega.ai/wasm/linter/p:1.0.0
  pluginTypes: [linter]
  abiVersion: v1alpha1
`,
		"unknown pluginType": `apiVersion: aggrega.ai/v1alpha1
kind: WasmPlugin
metadata: {name: p, version: 1.0.0}
spec:
  plugin: oci://r@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
  pluginTypes: [importer]
  abiVersion: v1alpha1
`,
		"unknown abiVersion": `apiVersion: aggrega.ai/v1alpha1
kind: WasmPlugin
metadata: {name: p, version: 1.0.0}
spec:
  plugin: oci://r@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
  pluginTypes: [linter]
  abiVersion: v2
`,
		"unknown spec sibling": `apiVersion: aggrega.ai/v1alpha1
kind: WasmPlugin
metadata: {name: p, version: 1.0.0}
spec:
  plugin: oci://r@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
  pluginTypes: [linter]
  abiVersion: v1alpha1
  allowedHosts: [example.com]
`,
	}
	for name, src := range cases {
		src := src
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := envelope.ValidateBytes(envelope.KindWasmPlugin, []byte(src), "bad.yaml")
			if err == nil {
				t.Fatalf("expected envelope rejection for %q; got nil", name)
			}
			if !errors.Is(err, envelope.ErrEntityEnvelope) {
				t.Fatalf("expected ErrEntityEnvelope for %q; got %v", name, err)
			}
		})
	}
}

const wasmPluginWithExports = `apiVersion: aggrega.ai/v1alpha1
kind: WasmPlugin
metadata:
  name: publishing-linter
  version: 1.0.0
spec:
  plugin: oci://hub.aggrega.ai/wasm/linter/publishing-linter@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
  pluginTypes: [linter, frp_step]
  abiVersion: v1alpha1
  exports:
    - name: lint_naming
      operates: patch
      scope: own
      selector: workloads carrying a pod template
    - name: step
      operates: emit
      scope: host
`

// TestWasmPlugin_EnvelopeAcceptsExports is the positive control for the
// per-export capability block: a plugin declaring an exports array with the
// operates/scope enums and a free-form selector validates through the kind.
func TestWasmPlugin_EnvelopeAcceptsExports(t *testing.T) {
	t.Parallel()
	if _, err := envelope.ValidateBytes(envelope.KindWasmPlugin, []byte(wasmPluginWithExports), "plugin.yaml"); err != nil {
		t.Fatalf("WasmPlugin with exports rejected: %v", err)
	}
}

// TestWasmPlugin_ExportsAreAdditive proves the exports block is additive: a
// document that omits exports validates exactly as one that declares them — so
// existing WasmPlugin documents authored before the block stay valid.
func TestWasmPlugin_ExportsAreAdditive(t *testing.T) {
	t.Parallel()
	const withoutExports = `apiVersion: aggrega.ai/v1alpha1
kind: WasmPlugin
metadata:
  name: publishing-linter
  version: 1.0.0
spec:
  plugin: oci://hub.aggrega.ai/wasm/linter/publishing-linter@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
  pluginTypes: [linter, frp_step]
  abiVersion: v1alpha1
`
	if _, err := envelope.ValidateBytes(envelope.KindWasmPlugin, []byte(withoutExports), "no-exports.yaml"); err != nil {
		t.Fatalf("WasmPlugin omitting exports rejected (the block must be additive): %v", err)
	}
}

// TestWasmPlugin_ExportsRoundTripPreservesOrder proves the exports array
// unmarshals into the typed slice, preserves author order and each capability
// field, and survives a marshal→unmarshal round-trip.
func TestWasmPlugin_ExportsRoundTripPreservesOrder(t *testing.T) {
	t.Parallel()
	var wp types.WasmPlugin
	if err := yaml.Unmarshal([]byte(wasmPluginWithExports), &wp); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if got := len(wp.Spec.Exports); got != 2 {
		t.Fatalf("exports length = %d, want 2", got)
	}
	first := wp.Spec.Exports[0]
	if first.Name != "lint_naming" || first.Operates != "patch" || first.Scope != "own" || first.Selector != "workloads carrying a pod template" {
		t.Fatalf("first export not parsed field-for-field: %+v", first)
	}
	if second := wp.Spec.Exports[1]; second.Name != "step" || second.Operates != "emit" || second.Scope != "host" {
		t.Fatalf("second export not parsed field-for-field: %+v", second)
	}

	out, err := yaml.Marshal(&wp)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	var again types.WasmPlugin
	if err := yaml.Unmarshal(out, &again); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if len(again.Spec.Exports) != 2 || again.Spec.Exports[0].Name != "lint_naming" || again.Spec.Exports[1].Name != "step" {
		t.Fatalf("exports order drifted across round-trip: %+v", again.Spec.Exports)
	}
}

// TestWasmPlugin_EnvelopeRejectsBadExportEnum proves the operates/scope enums
// are live at the envelope: an out-of-vocabulary value, or an unknown sibling
// inside an export entry, is rejected.
func TestWasmPlugin_EnvelopeRejectsBadExportEnum(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"bad operates": `apiVersion: aggrega.ai/v1alpha1
kind: WasmPlugin
metadata: {name: p, version: 1.0.0}
spec:
  plugin: oci://r@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
  pluginTypes: [linter]
  abiVersion: v1alpha1
  exports:
    - name: lint_naming
      operates: rewrite
`,
		"bad scope": `apiVersion: aggrega.ai/v1alpha1
kind: WasmPlugin
metadata: {name: p, version: 1.0.0}
spec:
  plugin: oci://r@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
  pluginTypes: [linter]
  abiVersion: v1alpha1
  exports:
    - name: lint_naming
      scope: cluster
`,
		"export missing name": `apiVersion: aggrega.ai/v1alpha1
kind: WasmPlugin
metadata: {name: p, version: 1.0.0}
spec:
  plugin: oci://r@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
  pluginTypes: [linter]
  abiVersion: v1alpha1
  exports:
    - operates: patch
`,
		"unknown export sibling": `apiVersion: aggrega.ai/v1alpha1
kind: WasmPlugin
metadata: {name: p, version: 1.0.0}
spec:
  plugin: oci://r@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
  pluginTypes: [linter]
  abiVersion: v1alpha1
  exports:
    - name: lint_naming
      stability: stable
`,
	}
	for name, src := range cases {
		src := src
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := envelope.ValidateBytes(envelope.KindWasmPlugin, []byte(src), "bad.yaml")
			if err == nil {
				t.Fatalf("expected envelope rejection for %q; got nil", name)
			}
			if !errors.Is(err, envelope.ErrEntityEnvelope) {
				t.Fatalf("expected ErrEntityEnvelope for %q; got %v", name, err)
			}
		})
	}
}
