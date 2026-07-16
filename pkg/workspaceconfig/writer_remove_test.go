// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoveCLIPluginRef_ByNameAndURI proves the plugins.cli remover drops the
// entry matching a short name or a full URI, preserves the sibling entry and
// unrelated keys, and reports removed==true; a fully-drained list drops its cli
// key rather than leaving an empty list.
func TestRemoveCLIPluginRef_ByNameAndURI(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFilename)
	seed := "workspace: {}\noffline: true\ndefault_registry: hub.aggrega.ai\n" +
		"plugins:\n  cli:\n" +
		"    - uri: oci://hub.aggrega.ai/plugin/cli/greet:1.0.0\n      digest: digest-a\n" +
		"    - uri: oci://hub.aggrega.ai/plugin/cli/other:2.0.0\n      digest: digest-b\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Remove by short name.
	removed, err := RemoveCLIPluginRef(path, "greet")
	if err != nil || !removed {
		t.Fatalf("RemoveCLIPluginRef(greet) = %v, %v; want true, nil", removed, err)
	}
	rw, err := Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve after name-remove: %v", err)
	}
	cfg := rw.Config()
	if cfg.DefaultRegistry != "hub.aggrega.ai" || !cfg.Offline {
		t.Errorf("unrelated keys not preserved: default_registry=%q offline=%v", cfg.DefaultRegistry, cfg.Offline)
	}
	if got := cfg.Plugins.CLI; len(got) != 1 || got[0].URI != "oci://hub.aggrega.ai/plugin/cli/other:2.0.0" {
		t.Fatalf("sibling entry not preserved after name-remove; got %+v", got)
	}

	// Remove the last entry by full URI; the cli key drops entirely.
	removed, err = RemoveCLIPluginRef(path, "oci://hub.aggrega.ai/plugin/cli/other:2.0.0")
	if err != nil || !removed {
		t.Fatalf("RemoveCLIPluginRef(full uri) = %v, %v; want true, nil", removed, err)
	}
	if body, rerr := os.ReadFile(path); rerr != nil {
		t.Fatalf("read after drain: %v", rerr)
	} else if strings.Contains(string(body), "cli:") {
		t.Fatalf("drained cli list should drop its key; file:\n%s", body)
	}
	rw, err = Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve after drain: %v", err)
	}
	if got := rw.Config().Plugins.CLI; len(got) != 0 {
		t.Fatalf("cli list should be empty after draining; got %+v", got)
	}
}

// TestRemoveCLIPluginRef_NoMatchLeavesFileUntouched proves a query that matches
// nothing reports removed==false and rewrites nothing — the file stays
// byte-for-byte as it was, so the calling command can fail loudly.
func TestRemoveCLIPluginRef_NoMatchLeavesFileUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFilename)
	seed := "workspace: {}\nplugins:\n  cli:\n    - uri: oci://hub.aggrega.ai/plugin/cli/greet:1.0.0\n      digest: digest-a\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before, _ := os.ReadFile(path)

	removed, err := RemoveCLIPluginRef(path, "absent")
	if err != nil {
		t.Fatalf("RemoveCLIPluginRef(absent): %v", err)
	}
	if removed {
		t.Fatalf("no entry matches; want removed==false")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatalf("no-match remove must not rewrite the file;\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestRemoveCLIPluginRef_MissingFile proves removing from a config file that does
// not exist reports removed==false without creating the file.
func TestRemoveCLIPluginRef_MissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFilename)
	removed, err := RemoveCLIPluginRef(path, "greet")
	if err != nil {
		t.Fatalf("RemoveCLIPluginRef(missing file): %v", err)
	}
	if removed {
		t.Fatalf("missing file; want removed==false")
	}
	if _, serr := os.Stat(path); !os.IsNotExist(serr) {
		t.Fatalf("no-match remove created the config file; want it absent")
	}
}

// TestRemovePluginSocketRef_ByNamePreservesSiblings proves a config-socket remove
// drops the matching plugins.<socket> entry by its short name, preserves other
// entries in the same list, and drains the key when it empties.
func TestRemovePluginSocketRef_ByNamePreservesSiblings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFilename)
	seed := "workspace: {}\nplugins:\n  mutator:\n" +
		"    - uri: mutator/tag-injector:1.2.0\n" +
		"    - uri: mutator/tag-rewriter:1.0.0\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	removed, err := RemovePluginSocketRef(path, "mutator", "tag-injector")
	if err != nil || !removed {
		t.Fatalf("RemovePluginSocketRef = %v, %v; want true, nil", removed, err)
	}
	rw, err := Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve after remove: %v", err)
	}
	got := rw.Config().Plugins.Mutator
	if len(got) != 1 || got[0].URI != "mutator/tag-rewriter:1.0.0" {
		t.Fatalf("sibling entry not preserved; got %+v", got)
	}

	// A socket key present but with no matching entry is a no-match.
	removed, err = RemovePluginSocketRef(path, "mutator", "does-not-exist")
	if err != nil {
		t.Fatalf("RemovePluginSocketRef(no match): %v", err)
	}
	if removed {
		t.Fatalf("want removed==false on no match")
	}
}

// TestRemoveProviderBinding_ByCapabilityAndURI proves a provider binding is
// removable by the capability it serves and by its full URI, preserving other
// bindings.
func TestRemoveProviderBinding_ByCapabilityAndURI(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFilename)
	seed := "workspace: {}\nplugins:\n  providers:\n" +
		"    - capability: helm\n      uri: oci://hub.aggrega.ai/plugin/provider/helm\n" +
		"    - capability: compose\n      uri: oci://hub.aggrega.ai/plugin/provider/compose\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Remove by capability.
	removed, err := RemoveProviderBinding(path, "helm")
	if err != nil || !removed {
		t.Fatalf("RemoveProviderBinding(helm) = %v, %v; want true, nil", removed, err)
	}
	rw, err := Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve after capability-remove: %v", err)
	}
	if got := rw.Config().Plugins.Providers; len(got) != 1 || got[0].Capability != "compose" {
		t.Fatalf("compose binding not preserved; got %+v", got)
	}

	// Remove the last binding by its full URI.
	removed, err = RemoveProviderBinding(path, "oci://hub.aggrega.ai/plugin/provider/compose")
	if err != nil || !removed {
		t.Fatalf("RemoveProviderBinding(uri) = %v, %v; want true, nil", removed, err)
	}
	rw, err = Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve after uri-remove: %v", err)
	}
	if got := rw.Config().Plugins.Providers; len(got) != 0 {
		t.Fatalf("providers should be empty; got %+v", got)
	}
}

// TestPluginNameFromURI pins the short-name derivation the removers match on.
func TestPluginNameFromURI(t *testing.T) {
	cases := map[string]string{
		"oci://hub.aggrega.ai/plugin/cli/greet:1.2.0": "greet",
		"mutator/tag-injector:1.2.0":                  "tag-injector",
		"provider/helm":                               "helm",
		"":                                            "",
	}
	for uri, want := range cases {
		if got := pluginNameFromURI(uri); got != want {
			t.Errorf("pluginNameFromURI(%q) = %q; want %q", uri, got, want)
		}
	}
}
