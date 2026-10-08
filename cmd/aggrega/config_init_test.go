// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/registrytrust"
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
	if err := configInitRun(t.Context(), configInitConfig{
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

// TestConfigInitRefusesARegistryItCannotPinOffline drives `config init` on an
// offline workspace the way the command runs it — no transport supplied — for a
// registry other than the hub and for the hub.
//
// Seeding takes no identities or key, so offline a registry other than the hub
// has nothing to pin and is refused with the offline-registration condition,
// leaving no file behind and printing nothing; the hub is seeded on its built-in
// baseline, and the one line printed says the baseline was pinned and why no
// advertised policy was taken. Planted defects: removing the offline refusal fails
// the first row, which sees a configuration written that trusts nothing; refusing
// the hub fails the second, and so does printing the not-advertised sentence for
// it, whose advice to pin identities by hand is wrong for an entry that carries a
// policy.
func TestConfigInitRefusesARegistryItCannotPinOffline(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		registry    string
		wantRefused bool
	}{
		{name: "a registry other than the hub is refused", registry: "private.example.test", wantRefused: true},
		{name: "the hub is seeded on its built-in baseline", registry: workspaceconfig.HubBaselineHost},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename),
				[]byte("offline: true\nworkspace: {}\n"), 0o644); err != nil {
				t.Fatalf("write the workspace configuration: %v", err)
			}
			global := filepath.Join(t.TempDir(), "aggrega", "config.yaml")
			lookup := frozenEnv(map[string]string{workspaceconfig.EnvConfigOverride: global})
			rw := resolveTempWorkspaceAgainst(t, tempWorkspaceOptions{Root: root}, lookup)

			var out, errOut bytes.Buffer
			err := configInitRun(t.Context(), configInitConfig{
				Registry: tc.registry,
				ws:       rw,
				Out:      &out,
				ErrOut:   &errOut,
			})
			_, statErr := os.Stat(global)

			if tc.wantRefused {
				if !errors.Is(err, registrytrust.ErrOfflineRegistrationWithoutTrustPolicy()) {
					t.Fatalf("configInitRun = %v; want the offline seeding refused", err)
				}
				if !os.IsNotExist(statErr) {
					t.Errorf("a refused seeding left a configuration at %s (stat err = %v)", global, statErr)
				}
				if out.Len() != 0 || errOut.Len() != 0 {
					t.Errorf("a refused seeding printed %q / %q; the refusal is the whole answer", out.String(), errOut.String())
				}
				return
			}
			if err != nil {
				t.Fatalf("configInitRun: %v", err)
			}
			if statErr != nil {
				t.Fatalf("the hub seeding wrote no configuration at %s: %v", global, statErr)
			}
			reloaded, err := workspaceconfig.Resolve(rw.Root(), "", lookup)
			if err != nil {
				t.Fatalf("re-resolve: %v", err)
			}
			pol, _ := reloaded.Config().RegistryTrustPolicy(workspaceconfig.HubBaselineHost)
			if want := workspaceconfig.DefaultBaseline(); len(pol.TrustedIdentities) != len(want.TrustedIdentities) || !pol.Signing.Required {
				t.Errorf("the hub was not seeded on its built-in baseline: %+v", pol)
			}
			host := workspaceconfig.HubBaselineHost
			want := fmt.Sprintf("%s was not asked for its trust policy, because the workspace is offline; using the built-in trust baseline for %s instead\n",
				host, host)
			if got := strings.SplitAfter(afterSeedingLocations(t, out.String()), "\n")[0]; got != want {
				t.Errorf("the seeding's first line after its locations = %q; want the one baseline notice %q", got, want)
			}
			if errOut.Len() != 0 {
				t.Errorf("a writing seeding put %q on the error stream; its answer stream is free for the notice", errOut.String())
			}
		})
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

	if err := configInitRun(t.Context(), base()); err != nil {
		t.Fatalf("first configInitRun: %v", err)
	}

	err := configInitRun(t.Context(), base())
	if err == nil {
		t.Fatal("second init without --force should refuse an existing file")
	}
	if !strings.Contains(err.Error(), global) || !strings.Contains(err.Error(), "--force") {
		t.Errorf("refusal error = %q; want it to name the path and --force", err.Error())
	}

	forced := base()
	forced.Force = true
	if err := configInitRun(t.Context(), forced); err != nil {
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

	if err := configInitRun(t.Context(), configInitConfig{
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

	if err := configInitRun(t.Context(), configInitConfig{
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

	if err := configInitRun(t.Context(), configInitConfig{
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
