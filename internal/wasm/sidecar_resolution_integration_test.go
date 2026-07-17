// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// TestSidecarResolvedPluginLoadsThroughGate is the end-to-end check that a real
// .wasm placed in a sidecar directory is resolved through the plugin cascade
// (with its lockfile digest pin verified), that the resolved bytes feed
// PluginSpec.Bytes, and that the plugin loads + describes through the gate. It
// proves the resolution → verified-bytes → load path works against a real guest.
func TestSidecarResolvedPluginLoadsThroughGate(t *testing.T) {
	src := ensureFixtureBuilt(t, "frp-step-patch-rust.wasm")
	content, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	const uri = "core/frp-step-patch:1.0.0"
	sidecarDir := filepath.Join(t.TempDir(), "sidecar")
	if err := os.MkdirAll(sidecarDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Flat layout: <uri-leaf>.wasm.
	if err := os.WriteFile(filepath.Join(sidecarDir, "frp-step-patch.wasm"), content, 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}

	resolver := pluginpath.Resolver{
		SidecarDir: sidecarDir,
		Pins:       map[string]string{uri: digest.ContentDigest(content)},
	}
	resolved, err := resolver.Resolve(uri)
	if err != nil {
		t.Fatalf("cascade resolve: %v", err)
	}
	if resolved.Tier != pluginpath.TierSidecar {
		t.Fatalf("resolved from %s; want sidecar", resolved.Tier)
	}

	loader := &wasm.Loader{}
	plugin, desc, err := loader.LoadAndDescribe(context.Background(), wasm.PluginSpec{
		Name:  "frp-step-patch",
		Bytes: resolved.Bytes,
	})
	if err != nil {
		t.Fatalf("LoadAndDescribe from sidecar-resolved bytes: %v", err)
	}
	defer plugin.Close(context.Background())
	if desc.AbiVersion == "" {
		t.Errorf("loaded plugin reported empty ABI version through describe()")
	}
}

// TestSidecarPinMismatchHaltsBeforeLoad: a sidecar artifact whose bytes drift
// from the lockfile pin halts at resolution — the drifted bytes never reach the
// loader.
func TestSidecarPinMismatchHaltsBeforeLoad(t *testing.T) {
	const uri = "core/frp-step-patch:1.0.0"
	sidecarDir := filepath.Join(t.TempDir(), "sidecar")
	if err := os.MkdirAll(sidecarDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sidecarDir, "frp-step-patch.wasm"), []byte("drifted"), 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}

	resolver := pluginpath.Resolver{
		SidecarDir: sidecarDir,
		Pins:       map[string]string{uri: digest.ContentDigest([]byte("the-real-bytes"))},
	}
	if _, err := resolver.Resolve(uri); !errors.Is(err, oci.ErrFetchIntegrity) {
		t.Fatalf("want integrity halt at resolution; got %v", err)
	}
}

// TestResolvedGarbageBytesHaltAtGate: bytes that resolve cleanly (no pin) but
// are not a loadable plugin halt at the loader/describe gate — proving the ABI
// gate runs on cascade-resolved bytes exactly as it does on file-read bytes.
func TestResolvedGarbageBytesHaltAtGate(t *testing.T) {
	const uri = "core/not-a-plugin:1.0.0"
	sidecarDir := filepath.Join(t.TempDir(), "sidecar")
	if err := os.MkdirAll(sidecarDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sidecarDir, "not-a-plugin.wasm"), []byte("not wasm at all"), 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}

	resolved, err := pluginpath.Resolver{SidecarDir: sidecarDir}.Resolve(uri)
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
