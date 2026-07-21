// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// TestColocatedResolvedPluginLoadsThroughGate is the offline dev-loop
// end-to-end check: a real .wasm laid down beside its Plugin entity
// in a catalog directory resolves through the co-located tier (with its digest
// pin verified), the resolved bytes feed PluginSpec.Bytes, and the plugin loads
// + describes through the gate. It proves the co-located resolution → verified
// bytes → load path a no-embed (aggrega-light) binary relies on.
func TestColocatedResolvedPluginLoadsThroughGate(t *testing.T) {
	src := ensureFixtureBuilt(t, "frp-step-patch-rust.wasm")
	content, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	// A non-OCI artifact reference so a miss falls through to the exhaustion
	// halt rather than the OCI-not-wired hint.
	const ref = "frp-step-patch:1.0.0"
	// The co-located dir mirrors the unpacked-artifact layout: the entity and its
	// module sit together. The resolver reads only the module; the entity beside
	// it documents the layout the catalog loader indexes.
	dir := filepath.Join(t.TempDir(), "plugin", "frp-step-patch", "1.0.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	leaf := pluginpath.PluginLeaf(ref)
	if err := os.WriteFile(filepath.Join(dir, leaf), content, 0o644); err != nil {
		t.Fatalf("write colocated module: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte("kind: Plugin\n"), 0o644); err != nil {
		t.Fatalf("write colocated entity: %v", err)
	}

	resolver := pluginpath.NewResolver(
		pluginpath.ResolutionContext{
			ColocatedDirs: map[string]string{ref: dir},
		},
		pluginpath.PinPostureLockfile,
		pluginpath.PinMaps{
			// The pin is computed from the fixture bytes at test time — never a
			// hardcoded, version-stamped golden.
			Pins: map[string]string{ref: digest.ContentDigest(content)},
		},
	)
	resolved, err := resolver.Resolve(ref)
	if err != nil {
		t.Fatalf("cascade resolve: %v", err)
	}
	if resolved.Tier != pluginpath.TierColocated {
		t.Fatalf("resolved from %s; want colocated", resolved.Tier)
	}

	loader := &wasm.Loader{}
	plugin, desc, err := loader.LoadAndDescribe(context.Background(), wasm.PluginSpec{
		Name:  "frp-step-patch",
		Bytes: resolved.Bytes,
	})
	if err != nil {
		t.Fatalf("LoadAndDescribe from colocated-resolved bytes: %v", err)
	}
	defer plugin.Close(context.Background())
	if desc.AbiVersion == "" {
		t.Errorf("loaded plugin reported empty ABI version through describe()")
	}
}

// TestColocatedMissFallsThroughToExhaustionHalt: a reference mapped to a
// co-located directory that holds no matching .wasm is a clean miss — the
// cascade falls through every tier to the loud exhaustion halt, and the halt
// message names the co-located tier.
func TestColocatedMissFallsThroughToExhaustionHalt(t *testing.T) {
	const ref = "frp-step-patch:1.0.0"
	emptyDir := filepath.Join(t.TempDir(), "plugin", "frp-step-patch", "1.0.0")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, err := pluginpath.NewResolver(pluginpath.ResolutionContext{ColocatedDirs: map[string]string{ref: emptyDir}}, pluginpath.PinPostureNone, pluginpath.PinMaps{}).Resolve(ref)
	if err == nil {
		t.Fatal("expected an exhaustion halt on a co-located miss; got nil")
	}
	if !errors.Is(err, oci.ErrFetchNotFound) {
		t.Fatalf("want ErrFetchNotFound exhaustion halt; got %v", err)
	}
	if !strings.Contains(err.Error(), "colocated") {
		t.Fatalf("halt message should name the colocated tier; got %q", err.Error())
	}
}

// TestColocatedPinMismatchHaltsBeforeLoad: a co-located artifact whose bytes drift
// from the lockfile pin halts at resolution — the drifted bytes never reach the
// loader.
func TestColocatedPinMismatchHaltsBeforeLoad(t *testing.T) {
	const ref = "frp-step-patch:1.0.0"
	dir := filepath.Join(t.TempDir(), "plugin", "frp-step-patch", "1.0.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, pluginpath.PluginLeaf(ref)), []byte("drifted"), 0o644); err != nil {
		t.Fatalf("write colocated module: %v", err)
	}

	resolver := pluginpath.NewResolver(
		pluginpath.ResolutionContext{ColocatedDirs: map[string]string{ref: dir}},
		pluginpath.PinPostureLockfile,
		pluginpath.PinMaps{Pins: map[string]string{ref: digest.ContentDigest([]byte("the-real-bytes"))}},
	)
	if _, err := resolver.Resolve(ref); !errors.Is(err, oci.ErrFetchIntegrity) {
		t.Fatalf("want integrity halt at resolution; got %v", err)
	}
}

// TestResolvedGarbageBytesHaltAtGate: bytes that resolve cleanly (no pin) but are
// not a loadable plugin halt at the loader/describe gate — proving the ABI gate
// runs on cascade-resolved bytes exactly as it does on file-read bytes.
func TestResolvedGarbageBytesHaltAtGate(t *testing.T) {
	const ref = "not-a-plugin:1.0.0"
	dir := filepath.Join(t.TempDir(), "plugin", "not-a-plugin", "1.0.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, pluginpath.PluginLeaf(ref)), []byte("not wasm at all"), 0o644); err != nil {
		t.Fatalf("write colocated module: %v", err)
	}

	resolved, err := pluginpath.NewResolver(pluginpath.ResolutionContext{ColocatedDirs: map[string]string{ref: dir}}, pluginpath.PinPostureNone, pluginpath.PinMaps{}).Resolve(ref)
	if err != nil {
		t.Fatalf("resolve (no pin) should succeed; got %v", err)
	}
	loader := &wasm.Loader{}
	if _, _, err := loader.LoadAndDescribe(context.Background(), wasm.PluginSpec{
		Name:  "not-a-plugin",
		Bytes: resolved.Bytes,
	}); err == nil {
		t.Fatal("expected the gate to reject non-plugin bytes; got nil")
	}
}
