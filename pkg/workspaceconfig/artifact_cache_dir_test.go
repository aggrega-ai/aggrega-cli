// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// TestLoadRefusesAbsoluteArtifactCacheDir pins the contract that makes the
// setting usable at all: it is workspace-relative, and every consumer joins it
// to the workspace root. An absolute value therefore names a directory that is
// not where the consumers look, and the failure it produces is silent — the
// cache the build writes stops being the cache the catalog walks step over.
// Refusing it at load turns that into one loud diagnostic naming the field.
func TestLoadRefusesAbsoluteArtifactCacheDir(t *testing.T) {
	path := writeFixture(t, "artifact_cache_dir: /var/tmp/aggrega-cache\n")

	_, err := load(path)
	if err == nil {
		t.Fatal("an absolute artifact_cache_dir loaded without complaint; the value would silently name a directory no consumer resolves to")
	}
	if !errors.Is(err, ErrConfigInvalid()) {
		t.Errorf("refusal is not category-consistent with other config-load rejections: %v", err)
	}
	for _, must := range []string{"artifact_cache_dir", "/var/tmp/aggrega-cache", "workspace-relative"} {
		if !strings.Contains(err.Error(), must) {
			t.Errorf("the diagnostic does not mention %q, so it does not tell the author what to change:\n%s", must, err.Error())
		}
	}
}

// TestLoadRefusesWorkspaceRootArtifactCacheDir pins the second half of the same
// contract. A value naming the workspace root itself makes the cache and the
// workspace one directory, so a catalog directory sitting at the workspace root
// — the mainstream layout — is the cache, and the catalog walk steps over the
// whole tree and yields nothing with no diagnostic to explain it. The value is
// also incoherent on its own terms, scattering fetched artifacts across the
// workspace root. The predicate is what the value resolves to rather than how it
// is spelled, so every spelling of the workspace root is caught.
func TestLoadRefusesWorkspaceRootArtifactCacheDir(t *testing.T) {
	for _, v := range []string{".", "./", "sub/..", "./."} {
		path := writeFixture(t, "artifact_cache_dir: "+strconv.Quote(v)+"\n")

		_, err := load(path)
		if err == nil {
			t.Errorf("artifact_cache_dir %q loaded without complaint; it names the workspace root, so a catalog directory there is stepped over entirely and the author is told nothing", v)
			continue
		}
		if !errors.Is(err, ErrConfigInvalid()) {
			t.Errorf("refusal of %q is not category-consistent with other config-load rejections: %v", v, err)
		}
		for _, must := range []string{"artifact_cache_dir", v, "workspace root"} {
			if !strings.Contains(err.Error(), must) {
				t.Errorf("the diagnostic for %q does not mention %q, so it does not tell the author what to change:\n%s", v, must, err.Error())
			}
		}
	}
}

// TestEnvRefusesWorkspaceRootArtifactCacheDir pins the workspace-root refusal on
// the other layer that can carry the setting, for the same reason the absolute
// refusal is pinned on both: a guard on one layer leaves the other as an
// unchecked way in, and the two would then disagree about what the setting may
// hold.
func TestEnvRefusesWorkspaceRootArtifactCacheDir(t *testing.T) {
	for _, v := range []string{".", "./", "sub/.."} {
		cfg := Default()
		env := map[string]string{"AGGREGA_ARTIFACT_CACHE_DIR": v}

		_, err := applyEnvOverlay(&cfg, func(k string) string { return env[k] })
		if err == nil {
			t.Errorf("AGGREGA_ARTIFACT_CACHE_DIR=%q was accepted; the config file and the environment disagree on what the setting may hold", v)
			continue
		}
		if !errors.Is(err, ErrConfigInvalid()) {
			t.Errorf("refusal of %q is not category-consistent with other config rejections: %v", v, err)
		}
		if !strings.Contains(err.Error(), "AGGREGA_ARTIFACT_CACHE_DIR") {
			t.Errorf("the diagnostic for %q does not name the variable that carried the value:\n%s", v, err.Error())
		}
	}
}

// TestLoadAcceptsRelativeArtifactCacheDir is the non-vacuity companion: the
// refusal must reject the absolute spelling and the workspace root itself only,
// leaving every legitimate value — including the default, a nested one, and a
// near-miss that merely starts with a dot segment — loading unchanged.
func TestLoadAcceptsRelativeArtifactCacheDir(t *testing.T) {
	for _, v := range []string{".aggrega/cache", "sub/.cache", "./.cache", "../shared-cache"} {
		path := writeFixture(t, "artifact_cache_dir: "+v+"\n")
		got, err := load(path)
		if err != nil {
			t.Errorf("a relative artifact_cache_dir %q was refused: %v", v, err)
			continue
		}
		if got.ArtifactCacheDir != v {
			t.Errorf("artifact_cache_dir %q was rewritten to %q; the value must reach consumers as authored", v, got.ArtifactCacheDir)
		}
	}
}

// TestEnvRefusesAbsoluteArtifactCacheDir pins the same refusal on the other
// layer that can carry the setting. A guard on the config file alone would
// leave the environment variable as an unchecked way in, so the two would
// disagree about what the setting may hold.
func TestEnvRefusesAbsoluteArtifactCacheDir(t *testing.T) {
	cfg := Default()
	env := map[string]string{"AGGREGA_ARTIFACT_CACHE_DIR": "/var/tmp/aggrega-cache"}

	_, err := applyEnvOverlay(&cfg, func(k string) string { return env[k] })
	if err == nil {
		t.Fatal("an absolute AGGREGA_ARTIFACT_CACHE_DIR was accepted; the config file and the environment disagree on what the setting may hold")
	}
	if !errors.Is(err, ErrConfigInvalid()) {
		t.Errorf("refusal is not category-consistent with other config rejections: %v", err)
	}
	if !strings.Contains(err.Error(), "AGGREGA_ARTIFACT_CACHE_DIR") {
		t.Errorf("the diagnostic does not name the variable that carried the value:\n%s", err.Error())
	}

	// Non-vacuity: the same overlay accepts a relative value.
	relCfg := Default()
	relEnv := map[string]string{"AGGREGA_ARTIFACT_CACHE_DIR": "sub/.cache"}
	if _, relErr := applyEnvOverlay(&relCfg, func(k string) string { return relEnv[k] }); relErr != nil {
		t.Fatalf("a relative AGGREGA_ARTIFACT_CACHE_DIR was refused, so the assertion above is vacuous: %v", relErr)
	}
	if relCfg.ArtifactCacheDir != "sub/.cache" {
		t.Errorf("the relative environment value did not reach the config: %q", relCfg.ArtifactCacheDir)
	}
}
