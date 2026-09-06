// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// offlineTrustClient returns an http.Client whose transport fails every request,
// so config init's advertised-trust fetch takes the offline fallback path
// (writing the built-in official baseline) without contacting the network.
func offlineTrustClient() *http.Client {
	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("offline: no network in test")
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestConfigInitSeedsGlobalHubBaseline proves `config init` on a fresh machine
// (no existing global config, offline) writes a schema-valid global config
// registering the hub with signatures required and the full official identity
// set, round-tripped through the resolver.
func TestConfigInitSeedsGlobalHubBaseline(t *testing.T) {
	t.Parallel()
	global := filepath.Join(t.TempDir(), "aggrega", "config.yaml")
	lookup := frozenEnv(map[string]string{workspaceconfig.EnvConfigOverride: global})
	rw := resolveTempWorkspaceAgainst(t, tempWorkspaceOptions{}, lookup)

	var out bytes.Buffer
	if err := configInitRun(configInitConfig{
		Registry:    workspaceconfig.HubBaselineHost,
		ws:          rw,
		Out:         &out,
		trustClient: offlineTrustClient(),
	}); err != nil {
		t.Fatalf("configInitRun: %v", err)
	}

	if _, err := os.Stat(global); err != nil {
		t.Fatalf("global config not written at %q: %v", global, err)
	}

	reloaded, err := workspaceconfig.Resolve(rw.Root(), "", lookup)
	if err != nil {
		t.Fatalf("re-resolve: %v", err)
	}
	pol, found := reloaded.Config().RegistryTrustPolicy(workspaceconfig.HubBaselineHost)
	if !found {
		t.Fatal("hub registry should be found after init")
	}
	if !pol.Signing.Required {
		t.Error("signing.required should be set from the hub baseline")
	}
	want := workspaceconfig.DefaultBaseline()
	if len(pol.TrustedIdentities) != len(want.TrustedIdentities) {
		t.Errorf("trusted identities = %d; want the full baseline set of %d", len(pol.TrustedIdentities), len(want.TrustedIdentities))
	}
	if len(pol.TrustedDeprecationIdentities) != len(want.TrustedDeprecationIdentities) {
		t.Errorf("deprecation identities = %d; want the baseline set of %d", len(pol.TrustedDeprecationIdentities), len(want.TrustedDeprecationIdentities))
	}
}

// TestConfigInitRefusesExistingWithoutForce proves a second init against an
// existing config file refuses (naming the path and --force) rather than
// overwrite, and that --force lets it proceed.
func TestConfigInitRefusesExistingWithoutForce(t *testing.T) {
	t.Parallel()
	global := filepath.Join(t.TempDir(), "aggrega", "config.yaml")
	lookup := frozenEnv(map[string]string{workspaceconfig.EnvConfigOverride: global})
	rw := resolveTempWorkspaceAgainst(t, tempWorkspaceOptions{}, lookup)

	base := func() configInitConfig {
		return configInitConfig{
			Registry:    workspaceconfig.HubBaselineHost,
			ws:          rw,
			Out:         &bytes.Buffer{},
			trustClient: offlineTrustClient(),
		}
	}

	if err := configInitRun(base()); err != nil {
		t.Fatalf("first configInitRun: %v", err)
	}

	err := configInitRun(base())
	if err == nil {
		t.Fatal("second init without --force should refuse an existing file")
	}
	if !strings.Contains(err.Error(), global) || !strings.Contains(err.Error(), "--force") {
		t.Errorf("refusal error = %q; want it to name the path and --force", err.Error())
	}

	forced := base()
	forced.Force = true
	if err := configInitRun(forced); err != nil {
		t.Fatalf("init --force over an existing file should succeed: %v", err)
	}
}

// TestConfigInitConfigPathCreatesTarget proves `config init --config <newpath>`
// writes to a not-yet-existent path — init uses the write path, which computes a
// destination rather than requiring it to already exist.
func TestConfigInitConfigPathCreatesTarget(t *testing.T) {
	t.Parallel()
	// A --config target under a not-yet-created directory. The flag-aware lookup
	// is exactly what the root wiring builds from --config.
	target := filepath.Join(t.TempDir(), "custom", "config.yaml")
	lookup := globalConfigLookup(target, frozenEnv(nil))
	rw := resolveTempWorkspaceAgainst(t, tempWorkspaceOptions{}, lookup)

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("precondition: target should not exist yet; stat err = %v", err)
	}

	if err := configInitRun(configInitConfig{
		Registry:    workspaceconfig.HubBaselineHost,
		ws:          rw,
		Out:         &bytes.Buffer{},
		trustClient: offlineTrustClient(),
	}); err != nil {
		t.Fatalf("configInitRun --config into a new path: %v", err)
	}

	if _, err := os.Stat(target); err != nil {
		t.Fatalf("--config target not created at %q: %v", target, err)
	}
	reloaded, err := workspaceconfig.Resolve(rw.Root(), "", lookup)
	if err != nil {
		t.Fatalf("re-resolve: %v", err)
	}
	if _, found := reloaded.Config().RegistryTrustPolicy(workspaceconfig.HubBaselineHost); !found {
		t.Error("hub registry should be found via the --config global layer")
	}
}

// TestConfigInitConfigFlagMissingPathCreates proves the fix for the resolve-time
// rejection: when --config names a file under a not-yet-created nested directory,
// the shared full-cascade resolve rejects the missing explicit path (its read-path
// typo guard), but init resolves without the global layer and creates the file.
func TestConfigInitConfigFlagMissingPathCreates(t *testing.T) {
	t.Parallel()
	root := resolveTempWorkspace(t, tempWorkspaceOptions{}).Root()
	// --config points at a file under a directory that does not exist yet.
	target := filepath.Join(t.TempDir(), "deep", "nested", "config.yaml")
	lookup := globalConfigLookup(target, frozenEnv(nil))

	if _, err := os.Stat(filepath.Dir(target)); !os.IsNotExist(err) {
		t.Fatalf("precondition: nested dir should not exist yet; stat err = %v", err)
	}
	// The full-cascade resolve the shared workspace path runs rejects the missing
	// explicit --config path — this is exactly the rejection init must not hit.
	if _, err := workspaceconfig.Resolve("", root, lookup); err == nil {
		t.Fatal("precondition: Resolve should reject a missing explicit --config path")
	}
	// The write-oriented resolve skips the global layer and succeeds.
	rw, err := workspaceconfig.ResolveWithoutGlobal("", root, lookup)
	if err != nil {
		t.Fatalf("ResolveWithoutGlobal into a missing --config path: %v", err)
	}

	if err := configInitRun(configInitConfig{
		Registry:    workspaceconfig.HubBaselineHost,
		ws:          rw,
		Out:         &bytes.Buffer{},
		trustClient: offlineTrustClient(),
	}); err != nil {
		t.Fatalf("configInitRun into a missing --config path: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("config not created at %q: %v", target, err)
	}
	reloaded, err := workspaceconfig.Resolve(rw.Root(), "", lookup)
	if err != nil {
		t.Fatalf("re-resolve: %v", err)
	}
	if _, found := reloaded.Config().RegistryTrustPolicy(workspaceconfig.HubBaselineHost); !found {
		t.Error("hub registry should be found via the created --config global layer")
	}
}

// TestConfigInitContextLookupWritesConfigPath proves the seam the command body
// uses: the flag-aware lookup pulled from the context via configLookupFromContext
// routes init's write to the --config path, exactly as the RunE wiring does.
func TestConfigInitContextLookupWritesConfigPath(t *testing.T) {
	t.Parallel()
	root := resolveTempWorkspace(t, tempWorkspaceOptions{}).Root()
	target := filepath.Join(t.TempDir(), "wired", "config.yaml")
	ctx := withWorkspaceResolver(context.Background(), root, globalConfigLookup(target, frozenEnv(nil)))

	// The RunE resolves the workspace through the context's own lookup, and the
	// write destination then comes off that resolution — so pulling the lookup
	// from the context is what carries --config all the way to the write.
	rw, err := workspaceconfig.ResolveWithoutGlobal("", root, configLookupFromContext(ctx))
	if err != nil {
		t.Fatalf("resolve via the context lookup: %v", err)
	}

	if err := configInitRun(configInitConfig{
		Registry:    workspaceconfig.HubBaselineHost,
		ws:          rw,
		Out:         &bytes.Buffer{},
		trustClient: offlineTrustClient(),
	}); err != nil {
		t.Fatalf("configInitRun via context lookup: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("context-lookup init did not write to the --config path %q: %v", target, err)
	}
}
