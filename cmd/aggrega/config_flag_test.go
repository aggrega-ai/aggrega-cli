// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// seedTreeRoot writes a workspace-tree config file carrying a `workspace: {}`
// marker (so it anchors a root) and the given default_registry scalar, and
// returns the directory. The scalar is the distinguishing field the layering
// assertions read back.
func seedTreeRoot(t *testing.T, defaultRegistry string) string {
	t.Helper()
	dir := t.TempDir()
	body := "workspace: {}\ndefault_registry: " + defaultRegistry + "\n"
	if err := os.WriteFile(filepath.Join(dir, workspaceconfig.ConfigFilename), []byte(body), 0o644); err != nil {
		t.Fatalf("seed tree config: %v", err)
	}
	return dir
}

// seedGlobalConfig writes a standalone global config file carrying the given
// default_registry scalar and returns its path. It sits in its own temp
// directory so it is never mistaken for a tree file.
func seedGlobalConfig(t *testing.T, defaultRegistry string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "aggrega", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir global dir: %v", err)
	}
	body := "default_registry: " + defaultRegistry + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("seed global config: %v", err)
	}
	return path
}

// TestGlobalConfigLookupFlagWinsOverEnv proves the flag-aware lookup constructor
// returns the --config value for the config-override variable when the flag is
// set, overriding whatever the environment holds, while every other variable
// still falls through to the base reader.
func TestGlobalConfigLookupFlagWinsOverEnv(t *testing.T) {
	t.Parallel()
	base := frozenEnv(map[string]string{
		workspaceconfig.EnvConfigOverride: "/env/path.yaml",
		"OTHER_VAR":                       "other-value",
	})
	look := globalConfigLookup("/flag/path.yaml", base)

	if got := look(workspaceconfig.EnvConfigOverride); got != "/flag/path.yaml" {
		t.Errorf("config override = %q; want the flag value /flag/path.yaml (flag must win over env)", got)
	}
	if got := look("OTHER_VAR"); got != "other-value" {
		t.Errorf("OTHER_VAR = %q; want the base value other-value (non-config vars fall through)", got)
	}
}

// TestGlobalConfigLookupEmptyFlagIsBase proves that with no --config flag the
// returned lookup is exactly the base reader, so $AGGREGA_CONFIG (else the XDG
// default) governs — the flag overrides the environment only when set.
func TestGlobalConfigLookupEmptyFlagIsBase(t *testing.T) {
	t.Parallel()
	base := frozenEnv(map[string]string{workspaceconfig.EnvConfigOverride: "/env/path.yaml"})
	look := globalConfigLookup("", base)

	if got := look(workspaceconfig.EnvConfigOverride); got != "/env/path.yaml" {
		t.Errorf("config override = %q; want the env value /env/path.yaml (an unset flag defers to the environment)", got)
	}
}

// TestConfigFlagLoadsGlobalLayerAndTreeOverrides drives the production wiring
// (globalConfigLookup → withWorkspaceResolver → workspaceFromContext →
// Resolve). It proves the --config file is loaded as the GLOBAL layer, and that
// a workspace-tree config file still layers on top and overrides it — the flag
// is the flag form of the global-path override, not a "only this file, no
// cascade" switch.
func TestConfigFlagLoadsGlobalLayerAndTreeOverrides(t *testing.T) {
	t.Parallel()
	treeRoot := seedTreeRoot(t, "from-tree.io")
	globalPath := seedGlobalConfig(t, "from-global.io")

	// base has no config-override set, so only the --config flag names the global
	// layer; the frozen base keeps resolution hermetic against the real env.
	lookup := globalConfigLookup(globalPath, frozenEnv(nil))
	ctx := withWorkspaceResolver(context.Background(), treeRoot, lookup)

	rw, err := workspaceFromContext(ctx)
	if err != nil {
		t.Fatalf("workspaceFromContext: %v", err)
	}

	if got := rw.Config().DefaultRegistry; got != "from-tree.io" {
		t.Errorf("default_registry = %q; want from-tree.io (the workspace tree must override the --config global layer)", got)
	}
	files := rw.ContributingFiles()
	if !containsString(files, globalPath) {
		t.Errorf("contributing files %v do not include the --config global file %q; it was not loaded as the global layer", files, globalPath)
	}
	if !containsString(files, filepath.Join(treeRoot, workspaceconfig.ConfigFilename)) {
		t.Errorf("contributing files %v do not include the tree config; the cascade must still layer on top", files)
	}
}

// TestConfigFlagBeatsEnv proves the precedence ladder --config > $AGGREGA_CONFIG:
// when both name distinct global files, the resolved global layer is the flag's
// file. The negative control resolves with no flag and the same env, and asserts
// the env file IS adopted — so the flag genuinely overrides a live env path
// rather than merely coinciding with one nothing consulted.
func TestConfigFlagBeatsEnv(t *testing.T) {
	t.Parallel()
	treeRoot := seedTreeRoot(t, "from-tree.io")
	flagGlobal := seedGlobalConfig(t, "from-flag-global.io")
	envGlobal := seedGlobalConfig(t, "from-env-global.io")
	envBase := frozenEnv(map[string]string{workspaceconfig.EnvConfigOverride: envGlobal})

	// Flag set: the flag's global file wins; the env path is never consulted.
	flagCtx := withWorkspaceResolver(context.Background(), treeRoot, globalConfigLookup(flagGlobal, envBase))
	flagRW, err := workspaceFromContext(flagCtx)
	if err != nil {
		t.Fatalf("flag-path workspaceFromContext: %v", err)
	}
	if !containsString(flagRW.ContributingFiles(), flagGlobal) {
		t.Errorf("contributing files %v do not include the --config file %q; the flag must win over $%s",
			flagRW.ContributingFiles(), flagGlobal, workspaceconfig.EnvConfigOverride)
	}
	if containsString(flagRW.ContributingFiles(), envGlobal) {
		t.Errorf("contributing files %v include the env file %q; the flag path must shadow the env path entirely",
			flagRW.ContributingFiles(), envGlobal)
	}

	// Negative control: no flag, same env — the env path anchors the global layer,
	// proving the env path is a real alternative the flag genuinely overrode.
	envCtx := withWorkspaceResolver(context.Background(), treeRoot, globalConfigLookup("", envBase))
	envRW, err := workspaceFromContext(envCtx)
	if err != nil {
		t.Fatalf("env-path workspaceFromContext: %v", err)
	}
	if !containsString(envRW.ContributingFiles(), envGlobal) {
		t.Errorf("contributing files %v do not include the env file %q; the negative control does not exercise the env path it guards",
			envRW.ContributingFiles(), envGlobal)
	}
}

// TestConfigLookupFromContext proves the seam a config-mutating command uses:
// the flag-aware lookup stashed on the resolver is retrievable from the context,
// so a command resolves the global config path through the same ladder without
// re-reading the flag. Absent a resolver it falls back to the process
// environment.
func TestConfigLookupFromContext(t *testing.T) {
	t.Parallel()
	lookup := globalConfigLookup("/flag/path.yaml", frozenEnv(nil))
	ctx := withWorkspaceResolver(context.Background(), "", lookup)

	got := configLookupFromContext(ctx)
	if got(workspaceconfig.EnvConfigOverride) != "/flag/path.yaml" {
		t.Errorf("configLookupFromContext returned a lookup that does not carry the --config value; the write-path seam is not wired to the flag")
	}

	// No resolver attached: the fallback is the live process environment. Assert
	// it is non-nil and callable rather than asserting a specific value, since the
	// real environment is not frozen here.
	fallback := configLookupFromContext(context.Background())
	if fallback == nil {
		t.Fatal("configLookupFromContext returned nil with no resolver; it must fall back to a usable environment reader")
	}
}
