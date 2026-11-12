// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// fakeEnv builds a lookup over a fixed map, so tests inject the environment
// without os.Setenv and stay parallel-safe.
func fakeEnv(vals map[string]string) func(string) string {
	return func(k string) string { return vals[k] }
}

// writeFile writes content to path, creating parent directories.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestEnvBindingNamingConvention pins the 1:1 mapping between each
// config-backed binding's variable name and its config path: AGGREGA_ + the
// path upper-snake-cased (dots become underscores).
func TestEnvBindingNamingConvention(t *testing.T) {
	t.Parallel()
	re := regexp.MustCompile(`^AGGREGA_[A-Z0-9_]+$`)
	for _, b := range envBindings {
		if !re.MatchString(b.envVar) {
			t.Errorf("binding %q does not match AGGREGA_[A-Z0-9_]+", b.envVar)
		}
		if b.shortName {
			continue // deliberate ergonomic exception to the full-path rule
		}
		want := "AGGREGA_" + strings.ToUpper(strings.ReplaceAll(b.configPath, ".", "_"))
		if b.envVar != want {
			t.Errorf("binding for %q has env var %q; want %q", b.configPath, b.envVar, want)
		}
	}
}

// TestEnvOverlayStringWinsOverConfig: a SET string env value overlays the merged
// config value (env > config). Combined with buildSigner's flag overlay at the
// command layer, this yields flag > env > config > default.
func TestEnvOverlayStringWinsOverConfig(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\ndefault_registry: from-config.io\n")

	// Default only (no env): config value wins over the built-in default.
	rwConfig, err := Resolve(root, "", fakeEnv(nil))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := rwConfig.Config().DefaultRegistry; got != "from-config.io" {
		t.Errorf("config layer: got %q; want from-config.io", got)
	}

	// Env set: env wins over config.
	rwEnv, err := Resolve(root, "", fakeEnv(map[string]string{"AGGREGA_DEFAULT_REGISTRY": "from-env.io"}))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := rwEnv.Config().DefaultRegistry; got != "from-env.io" {
		t.Errorf("env layer: got %q; want from-env.io (env must win over config)", got)
	}

	// No config file, no env: the built-in default holds.
	bare := t.TempDir()
	rwDefault, err := Resolve(bare, "", fakeEnv(nil))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := rwDefault.Config().DefaultRegistry; got != "hub.aggrega.ai" {
		t.Errorf("default layer: got %q; want hub.aggrega.ai", got)
	}
}

// TestEnvOverlaySigningURLWinsOverConfig proves the env layer for a signing URL,
// the representative string the command's flag overlays on top of.
func TestEnvOverlaySigningURLWinsOverConfig(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\nsigning:\n  fulcio_url: https://config.fulcio\n")

	rw, err := Resolve(root, "", fakeEnv(map[string]string{"AGGREGA_SIGNING_FULCIO_URL": "https://env.fulcio"}))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := rw.Config().Signing.FulcioURL; got != "https://env.fulcio" {
		t.Errorf("signing fulcio: got %q; want https://env.fulcio", got)
	}
	// ResolveSigning reads the env-overlaid Config, so the env value flows
	// through the host-resolution the commands use.
	if got := rw.Config().ResolveSigning("hub.aggrega.ai").FulcioURL; got != "https://env.fulcio" {
		t.Errorf("ResolveSigning fulcio: got %q; want https://env.fulcio", got)
	}
}

// TestEnvOverlayBoolSemantics: unset = fall-through (config/default untouched),
// "false"/"0" = false, "true"/"1" = true, and a malformed value is a loud error.
func TestEnvOverlayBoolSemantics(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// AutoPull defaults to false; offline defaults to false.
	writeFile(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\noffline: true\n")

	// Unset: config value (offline:true) holds; AutoPull default (false) holds.
	rwUnset, err := Resolve(root, "", fakeEnv(nil))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !rwUnset.Config().Offline {
		t.Errorf("unset AGGREGA_OFFLINE must leave config offline:true untouched")
	}
	if rwUnset.Config().AutoPull {
		t.Errorf("unset AGGREGA_AUTO_PULL must leave default auto_pull:false untouched")
	}

	// "false" overrides config true.
	rwFalse, err := Resolve(root, "", fakeEnv(map[string]string{"AGGREGA_OFFLINE": "false"}))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if rwFalse.Config().Offline {
		t.Errorf("AGGREGA_OFFLINE=false must set offline false")
	}

	// "1" sets true.
	rwOne, err := Resolve(root, "", fakeEnv(map[string]string{"AGGREGA_OFFLINE": "1"}))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !rwOne.Config().Offline {
		t.Errorf("AGGREGA_OFFLINE=1 must set offline true")
	}

	// Invalid value is a loud error.
	if _, err := Resolve(root, "", fakeEnv(map[string]string{"AGGREGA_OFFLINE": "yes"})); err == nil {
		t.Errorf("AGGREGA_OFFLINE=yes must be a loud error, not a silent coercion")
	}
}

// TestEnvOverlayCatalogDirsSplit: AGGREGA_CATALOG_DIRS splits on the OS path
// list separator and replaces the catalog stack.
func TestEnvOverlayCatalogDirsSplit(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace:\n  catalog_dirs: [/from/config]\n")

	sep := string(os.PathListSeparator)
	env := fakeEnv(map[string]string{"AGGREGA_CATALOG_DIRS": "/a/cat" + sep + "/b/cat"})
	rw, err := Resolve(root, "", env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got := rw.Config().Workspace.CatalogDirs
	want := []string{"/a/cat", "/b/cat"}
	if len(got) != len(want) {
		t.Fatalf("catalog dirs: got %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("catalog dirs[%d]: got %q; want %q", i, got[i], want[i])
		}
	}
}

// TestEnvOverlayBreadcrumbDeterministic: contributing env vars are reported in
// table order, so the "loaded from environment" breadcrumb is byte-stable.
func TestEnvOverlayBreadcrumbDeterministic(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\n")

	env := fakeEnv(map[string]string{
		"AGGREGA_SIGNING_FULCIO_URL": "https://f",
		"AGGREGA_DEFAULT_REGISTRY":   "r.io",
		"AGGREGA_OFFLINE":            "true",
	})
	rw, err := Resolve(root, "", env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	files := rw.ContributingFiles()
	// The env contributions appear after the config file, in table order:
	// default_registry, offline, then signing.fulcio_url.
	var envEntries []string
	for _, f := range files {
		if strings.HasPrefix(f, "$") {
			envEntries = append(envEntries, f)
		}
	}
	want := []string{"$AGGREGA_DEFAULT_REGISTRY", "$AGGREGA_OFFLINE", "$AGGREGA_SIGNING_FULCIO_URL"}
	if len(envEntries) != len(want) {
		t.Fatalf("env breadcrumb: got %v; want %v", envEntries, want)
	}
	for i := range want {
		if envEntries[i] != want[i] {
			t.Errorf("env breadcrumb[%d]: got %q; want %q (table order)", i, envEntries[i], want[i])
		}
	}
}
