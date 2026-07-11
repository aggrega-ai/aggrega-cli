// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// stampBaseEntity is a minimal Plugin entity with an explicit runtime block
// carrying no hostFunctions, the on-disk shape an author writes before publish.
const stampBaseEntity = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: frp/patch
  version: "1.0.0"
spec:
  plugin: "oci://hub.aggrega.ai/plugin/frp/patch"
  pluginType: frp_step
  abiVersion: v1alpha1
  runtime:
    maxMemoryPages: 16
`

// TestStampHostFunctions_StampsCanonicalSet proves the stamp writes the derived
// host-function set into spec.runtime.hostFunctions, and that the bytes re-parse
// into the typed field — the machine-owned record the consume side reads.
func TestStampHostFunctions_StampsCanonicalSet(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	derived := []string{"aggrega_emit_warning", "aggrega_log"} // canonical (sorted)
	stamped, err := stampHostFunctions(&out, "frp/patch:1.0.0", []byte(stampBaseEntity), derived)
	if err != nil {
		t.Fatalf("stampHostFunctions: %v", err)
	}
	var wp types.Plugin
	if err := yaml.Unmarshal(stamped, &wp); err != nil {
		t.Fatalf("re-parse stamped entity: %v", err)
	}
	if wp.Spec.Runtime == nil {
		t.Fatal("runtime block lost on stamp")
	}
	if got := wp.Spec.Runtime.HostFunctions; len(got) != 2 || got[0] != "aggrega_emit_warning" || got[1] != "aggrega_log" {
		t.Fatalf("stamped hostFunctions = %v; want the canonical [aggrega_emit_warning aggrega_log]", got)
	}
	// A clean stamp (no prior on-disk value) emits no warning.
	if out.Len() != 0 {
		t.Errorf("stamping a field with no prior value should not warn; got %q", out.String())
	}
}

// TestStampHostFunctions_CreatesRuntimeWhenAbsent proves the stamp creates the
// spec.runtime block when the entity authored none, so the machine-owned record
// always lands.
func TestStampHostFunctions_CreatesRuntimeWhenAbsent(t *testing.T) {
	t.Parallel()
	const noRuntime = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: frp/patch
  version: "1.0.0"
spec:
  plugin: "oci://hub.aggrega.ai/plugin/frp/patch"
  pluginType: frp_step
  abiVersion: v1alpha1
`
	var out bytes.Buffer
	stamped, err := stampHostFunctions(&out, "frp/patch:1.0.0", []byte(noRuntime), []string{"aggrega_log"})
	if err != nil {
		t.Fatalf("stampHostFunctions: %v", err)
	}
	var wp types.Plugin
	if err := yaml.Unmarshal(stamped, &wp); err != nil {
		t.Fatalf("re-parse stamped entity: %v", err)
	}
	if wp.Spec.Runtime == nil {
		t.Fatal("runtime block should be created when absent")
	}
	if got := wp.Spec.Runtime.HostFunctions; len(got) != 1 || got[0] != "aggrega_log" {
		t.Fatalf("stamped hostFunctions = %v; want [aggrega_log]", got)
	}
}

// TestStampHostFunctions_DivergentOnDiskWarnsAndOverwrites proves the
// machine-owned discipline: a divergent on-disk value is NOT a halt (unlike the
// content-digest pin) — it is overwritten with the derived set, and the warning
// surfaces the authored-vs-derived sets so the author sees the correction.
func TestStampHostFunctions_DivergentOnDiskWarnsAndOverwrites(t *testing.T) {
	t.Parallel()
	const authoredWrong = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: frp/patch
  version: "1.0.0"
spec:
  plugin: "oci://hub.aggrega.ai/plugin/frp/patch"
  pluginType: frp_step
  abiVersion: v1alpha1
  runtime:
    hostFunctions:
      - bogus_function
`
	var out bytes.Buffer
	derived := []string{"aggrega_log"}
	stamped, err := stampHostFunctions(&out, "frp/patch:1.0.0", []byte(authoredWrong), derived)
	if err != nil {
		t.Fatalf("stampHostFunctions should overwrite, not halt; got %v", err)
	}
	// The warning names the entity and shows both the on-disk and the derived set.
	warning := out.String()
	for _, want := range []string{"frp/patch:1.0.0", "bogus_function", "aggrega_log", "warning"} {
		if !strings.Contains(warning, want) {
			t.Errorf("warning should mention %q; got %q", want, warning)
		}
	}
	// The stamped bytes carry the DERIVED set, not the authored one.
	var wp types.Plugin
	if err := yaml.Unmarshal(stamped, &wp); err != nil {
		t.Fatalf("re-parse stamped entity: %v", err)
	}
	if got := wp.Spec.Runtime.HostFunctions; len(got) != 1 || got[0] != "aggrega_log" {
		t.Fatalf("divergent on-disk value should be overwritten with the derived set; got %v", got)
	}
}

// TestStampHostFunctions_RePublishByteIdentical proves the canonical stamp is
// stable: stamping the same derived set onto an entity already carrying it
// produces byte-identical output and emits no warning, so a re-publish of the
// same .wasm is a no-op for the host-function record (determinism + stable
// version-bump comparison).
func TestStampHostFunctions_RePublishByteIdentical(t *testing.T) {
	t.Parallel()
	derived := []string{"aggrega_emit_warning", "aggrega_log"}
	var out1 bytes.Buffer
	first, err := stampHostFunctions(&out1, "frp/patch:1.0.0", []byte(stampBaseEntity), derived)
	if err != nil {
		t.Fatalf("first stamp: %v", err)
	}
	var out2 bytes.Buffer
	second, err := stampHostFunctions(&out2, "frp/patch:1.0.0", first, derived)
	if err != nil {
		t.Fatalf("second stamp: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("re-stamping the same derived set must be byte-identical;\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if out2.Len() != 0 {
		t.Errorf("re-stamping the same canonical set should not warn; got %q", out2.String())
	}
}

// TestStampHostFunctions_EmptySet proves a plugin importing no custom host
// functions stamps an explicit empty record, so the machine-owned field is
// present rather than absent.
func TestStampHostFunctions_EmptySet(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	stamped, err := stampHostFunctions(&out, "frp/patch:1.0.0", []byte(stampBaseEntity), nil)
	if err != nil {
		t.Fatalf("stampHostFunctions: %v", err)
	}
	if !strings.Contains(string(stamped), "hostFunctions: []") {
		t.Errorf("an empty derived set should stamp an explicit empty record; got:\n%s", stamped)
	}
}
