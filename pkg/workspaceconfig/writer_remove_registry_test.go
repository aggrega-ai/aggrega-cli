// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoveRegistry_ByHostAndAlias proves the registries remover drops the
// entry addressed by its host or its friendly alias, preserves sibling entries
// and unrelated keys, and reports removed==true. It also proves the default
// pointer is left untouched.
func TestRemoveRegistry_ByHostAndAlias(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFilename)
	seed := "workspace: {}\noffline: true\ndefault_registry: hub.aggrega.ai\n" +
		"registries:\n" +
		"  - host: hub.aggrega.ai\n" +
		"  - host: dev.local:5000\n    name: dev\n" +
		"  - host: other.example.com\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Remove by host.
	removed, err := RemoveRegistry(path, "other.example.com")
	if err != nil || !removed {
		t.Fatalf("RemoveRegistry(host) = %v, %v; want true, nil", removed, err)
	}
	rw, err := Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve after host-remove: %v", err)
	}
	cfg := rw.Config()
	if cfg.DefaultRegistry != "hub.aggrega.ai" || !cfg.Offline {
		t.Errorf("unrelated keys not preserved: default_registry=%q offline=%v", cfg.DefaultRegistry, cfg.Offline)
	}
	if _, found := cfg.RegistryTrustPolicy("other.example.com"); found {
		t.Errorf("other.example.com should be gone")
	}
	if _, found := cfg.RegistryTrustPolicy("hub.aggrega.ai"); !found {
		t.Errorf("hub entry should survive")
	}

	// Remove by alias — resolves dev -> dev.local:5000 and drops that entry.
	removed, err = RemoveRegistry(path, "dev")
	if err != nil || !removed {
		t.Fatalf("RemoveRegistry(alias) = %v, %v; want true, nil", removed, err)
	}
	rw, err = Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve after alias-remove: %v", err)
	}
	if _, found := rw.Config().RegistryTrustPolicy("dev.local:5000"); found {
		t.Errorf("dev.local:5000 should be gone after alias-remove")
	}
	// The default pointer survives even after removals.
	if rw.Config().DefaultRegistry != "hub.aggrega.ai" {
		t.Errorf("default_registry cleared by remove; want hub.aggrega.ai, got %q", rw.Config().DefaultRegistry)
	}
}

// TestRemoveRegistry_NoMatchLeavesFileUntouched proves a token that matches
// nothing reports removed==false and rewrites nothing — the file stays
// byte-for-byte as it was, so the calling command can fail loudly.
func TestRemoveRegistry_NoMatchLeavesFileUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFilename)
	seed := "workspace: {}\nregistries:\n  - host: hub.aggrega.ai\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before, _ := os.ReadFile(path)

	removed, err := RemoveRegistry(path, "absent.example.com")
	if err != nil {
		t.Fatalf("RemoveRegistry(absent): %v", err)
	}
	if removed {
		t.Fatalf("no entry matches; want removed==false")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatalf("no-match remove must not rewrite the file;\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestRemoveRegistry_DrainDropsKey proves removing the last registry drops the
// `registries` key rather than leaving an empty list.
func TestRemoveRegistry_DrainDropsKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFilename)
	seed := "workspace: {}\ndefault_registry: hub.aggrega.ai\nregistries:\n  - host: hub.aggrega.ai\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	removed, err := RemoveRegistry(path, "hub.aggrega.ai")
	if err != nil || !removed {
		t.Fatalf("RemoveRegistry = %v, %v; want true, nil", removed, err)
	}
	body, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatalf("read after drain: %v", rerr)
	}
	if strings.Contains(string(body), "registries:") {
		t.Fatalf("drained registries list should drop its key; file:\n%s", body)
	}
	// The default pointer is a separate setting and must survive the drain.
	if !strings.Contains(string(body), "default_registry: hub.aggrega.ai") {
		t.Fatalf("default_registry must survive draining registries; file:\n%s", body)
	}
	rw, err := Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve after drain: %v", err)
	}
	if got := rw.Config().Registries; len(got) != 0 {
		t.Fatalf("registries should be empty after draining; got %+v", got)
	}
	if rw.Config().DefaultRegistry != "hub.aggrega.ai" {
		t.Fatalf("default_registry cleared; want hub.aggrega.ai, got %q", rw.Config().DefaultRegistry)
	}
}

// TestRemoveRegistry_MissingFile proves removing from a config file that does not
// exist reports removed==false without creating the file.
func TestRemoveRegistry_MissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFilename)
	removed, err := RemoveRegistry(path, "hub.aggrega.ai")
	if err != nil {
		t.Fatalf("RemoveRegistry(missing file): %v", err)
	}
	if removed {
		t.Fatalf("missing file; want removed==false")
	}
	if _, serr := os.Stat(path); !os.IsNotExist(serr) {
		t.Fatalf("no-match remove created the config file; want it absent")
	}
}
