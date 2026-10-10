// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/verdicttest"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestOnlyAnOfflineVerifyAsksNoRegistryAboutAVerdictPlugin holds the one decision
// of whether the walk over verdict plugins asks their registries to its four cells:
// a pull asks online and offline alike — offline its walk then halts, as a plain
// pull keeps halting — and a verify asks online and asks no registry offline. The posture is read from the egress gate's own answer for the
// configuration, as the resolution reads it, and a refusal wrapped on its way is
// still that answer.
func TestOnlyAnOfflineVerifyAsksNoRegistryAboutAVerdictPlugin(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name            string
		verify, offline bool
		want            verdictPluginLookup
	}{
		{name: "a pull, online", want: askEachRegistry},
		{name: "a pull, offline", offline: true, want: askEachRegistry},
		{name: "a verify, online", verify: true, want: askEachRegistry},
		{name: "a verify, offline", verify: true, offline: true, want: askNoRegistry},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, egressErr := egress.HTTPClient(workspaceconfig.Config{Offline: tc.offline})
			if got := verdictPluginLookupFor(tc.verify, egressErr); got != tc.want {
				t.Errorf("verdictPluginLookupFor(verify=%t, offline=%t) = %d; want %d", tc.verify, tc.offline, got, tc.want)
			}
		})
	}
	t.Run("a verify, offline, the gate's refusal wrapped", func(t *testing.T) {
		t.Parallel()
		_, egressErr := egress.HTTPClient(workspaceconfig.Config{Offline: true})
		if got := verdictPluginLookupFor(true, fmt.Errorf("verdict plugins: %w", egressErr)); got != askNoRegistry {
			t.Errorf("a wrapped offline refusal must still read as offline; got %d", got)
		}
	})
}

// countingTarget is a registry target factory that counts what it is asked and
// answers every question with a refusal, so a walk that must ask nothing is held
// to having asked nothing.
func countingTarget(asked *int) pullverify.TargetFor {
	return func(pullverify.Entity) (oras.ReadOnlyTarget, string, error) {
		*asked++
		return nil, "", errors.New("the registry was asked")
	}
}

// TestAWalkAskingNoRegistryAsksNothingAndSaysSo is the walk's half of an offline
// verify over a registry-served verdict plugin: asking no registry, it still
// identifies the plugin from the cache, carries no signature and no withdrawal, and
// marks the plugin as not asked — and only where the fetch home would have asked
// about its signature, so a plugin bound to no registry is not marked. Asking every
// registry, the same walk asks.
func TestAWalkAskingNoRegistryAsksNothingAndSaysSo(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		cfg        workspaceconfig.Config
		registries verdictPluginLookup
		wantAsked  bool
		wantMarked bool
	}{
		{name: "bound to a registry, asking none", cfg: verdictSignatureConfig(nil, true), registries: askNoRegistry,
			wantMarked: true},
		{name: "bound to no registry, asking none", cfg: workspaceconfig.Config{}, registries: askNoRegistry},
		{name: "bound to a registry, asking each", cfg: verdictSignatureConfig(nil, false), registries: askEachRegistry,
			wantAsked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			seeded := verdicttest.SeedCached(t, root, filepath.Join(root, "unsearched-catalog"), "linter", "offline-probe", "1.0.0")
			cacheDir := workspaceconfig.Config{}.ResolvedArtifactCacheDir()
			lazy := catalog.NewLazyLookup(root, cacheDir, nil, nil)
			asked := 0

			var diag bytes.Buffer
			resolved, err := verifyCachedVerdictSocketPlugin(context.Background(), tc.cfg, lazy, seeded.URI,
				closure.Resolution{ManifestDigest: seeded.ManifestDigest}, root, cacheDir, countingTarget(&asked), tc.registries, &diag)
			if tc.wantAsked {
				if asked == 0 {
					t.Errorf("asking each registry, the walk asked nothing (err %v)", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("asking no registry, the walk must complete: %v", err)
			}
			if asked != 0 {
				t.Errorf("asking no registry, the walk asked %d question(s)", asked)
			}
			if resolved.Coordinate != seeded.Coordinate || resolved.Digest != seeded.WasmDigest {
				t.Errorf("the plugin must still be identified from the cache; got %q %q", resolved.Coordinate, resolved.Digest)
			}
			if resolved.Signature != nil || resolved.Deprecation != nil {
				t.Errorf("a walk that asked nothing carries no fact: signature %+v, deprecation %+v",
					resolved.Signature, resolved.Deprecation)
			}
			if resolved.SignatureNotAsked != tc.wantMarked {
				t.Errorf("SignatureNotAsked = %t; want %t", resolved.SignatureNotAsked, tc.wantMarked)
			}
			if !strings.Contains(diag.String(), "host functions match") {
				t.Errorf("the host functions must still be checked; diagnostics:\n%s", diag.String())
			}
		})
	}
}

// TestAWalkAskingNoRegistryStillHaltsOnAStampItsWasmDisagreesWith keeps the
// host-function check where the walk asks no registry: a cached plugin whose entity
// stamps a host function its .wasm does not import halts the walk as it halts a
// walk that asks.
func TestAWalkAskingNoRegistryStillHaltsOnAStampItsWasmDisagreesWith(t *testing.T) {
	t.Parallel()
	for _, registries := range []verdictPluginLookup{askNoRegistry, askEachRegistry} {
		t.Run(fmt.Sprintf("lookup %d", registries), func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			entity := verdicttest.PluginEntityYAML("linter", "stamped-probe", "1.0.0") +
				"  runtime:\n    hostFunctions:\n      - aggrega_log\n"
			seeded := verdicttest.SeedCachedArtifact(t, root, filepath.Join(root, "unsearched-catalog"), "linter",
				"stamped-probe", "1.0.0", entity, verdicttest.HeaderOnlyWasm)
			cacheDir := workspaceconfig.Config{}.ResolvedArtifactCacheDir()
			lazy := catalog.NewLazyLookup(root, cacheDir, nil, nil)
			// Bound to no registry, so a walk that asks reaches the stamp too: there is no
			// signature to ask about, which is what isolates the host-function check.
			asked := 0
			_, err := verifyCachedVerdictSocketPlugin(context.Background(), workspaceconfig.Config{}, lazy, seeded.URI,
				closure.Resolution{ManifestDigest: seeded.ManifestDigest}, root, cacheDir, countingTarget(&asked), registries, &bytes.Buffer{})
			if !errors.Is(err, wasm.ErrWasmHostFunctionsStampMismatch()) {
				t.Fatalf("a stamp the .wasm disagrees with must halt the walk with ErrWasmHostFunctionsStampMismatch; got %v", err)
			}
		})
	}
}

// entityAnswered is the lookup a run's walk resolves through, with the plugin
// entity's resolution answered as the lookup answers an entity the content cache
// serves, and every other question, the plugin's .wasm among them, left to the
// lookup it wraps.
type entityAnswered struct {
	closure.Lookup
	entity closure.Resolution
}

func (l entityAnswered) ResolveCatalogEntity(context.Context, string, string) (closure.Resolution, error) {
	return l.entity, nil
}

// TestOnlyAWalkAskingNoRegistryReadsAVerdictPluginFromTheContentCache holds the
// walk over verdict plugins where no registry is reached. The lookup it resolves a
// plugin's .wasm through has no puller, as an offline run's has none, and reads a
// cached .wasm only through a recorded lockfile pin, which a plugin attached at a
// verdict role never has, so it finds no copy of a plugin the content cache holds.
// Asking no registry — an offline verify — the walk reads that plugin from the
// cache as a build reads it: it identifies it, checks its host functions and marks
// it not asked, without asking anything; it refuses a .wasm the cache does not
// hold, and one that no longer hashes to the digest its manifest names, as the
// build refuses each; and a plugin the cache holds nothing for is skipped, as every
// walk skips it. Asking each registry — every pull, offline among them — the walk
// halts at that resolution, as it does without this read.
func TestOnlyAWalkAskingNoRegistryReadsAVerdictPluginFromTheContentCache(t *testing.T) {
	t.Parallel()
	// alteredWasm is an import-free module other than the one seeded — the header
	// and one empty custom section — so a walk reading it without holding it to its
	// digest would find its host functions in agreement and pass.
	alteredWasm := append(append([]byte(nil), verdicttest.HeaderOnlyWasm...), 0x00, 0x01, 0x00)
	removeWasm := func(t *testing.T, root, cacheDir string, seeded verdicttest.Cached) {
		slot, err := oci.CachePath(root, cacheDir, seeded.Coordinate, seeded.WasmDigest)
		if err != nil {
			t.Fatalf("the .wasm slot: %v", err)
		}
		if err := os.RemoveAll(slot); err != nil {
			t.Fatalf("remove the .wasm slot: %v", err)
		}
	}
	alterWasm := func(t *testing.T, root, cacheDir string, seeded verdicttest.Cached) {
		if err := oci.WriteCached(root, cacheDir, seeded.Coordinate, seeded.WasmDigest, alteredWasm); err != nil {
			t.Fatalf("alter the cached .wasm: %v", err)
		}
	}
	removeCache := func(t *testing.T, root, cacheDir string, _ verdicttest.Cached) {
		if err := os.RemoveAll(filepath.Join(root, cacheDir)); err != nil {
			t.Fatalf("remove the content cache: %v", err)
		}
	}
	for _, tc := range []struct {
		name       string
		registries verdictPluginLookup
		// nothingHeld removes the content cache, and the entity is then resolved by
		// the lookup itself, which finds it nowhere.
		nothingHeld bool
		damage      func(t *testing.T, root, cacheDir string, seeded verdicttest.Cached)
		// halt is the condition the walk must halt with; nil, it must complete.
		halt error
		// identified is whether the walk must return the plugin's resolution.
		identified bool
	}{
		{name: "asking none, the plugin cached", registries: askNoRegistry, identified: true},
		{name: "asking none, its .wasm not cached", registries: askNoRegistry, damage: removeWasm,
			halt: pluginpath.ErrPluginArtifactNotFound()},
		{name: "asking none, its cached .wasm altered", registries: askNoRegistry, damage: alterWasm,
			halt: oci.ErrFetchIntegrity()},
		{name: "asking none, nothing cached", registries: askNoRegistry, nothingHeld: true, damage: removeCache},
		{name: "asking each, the plugin cached", registries: askEachRegistry, halt: closure.ErrUnresolvableURI()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			seeded := verdicttest.SeedCached(t, root, filepath.Join(root, "unsearched-catalog"), "linter", "offline-probe", "1.0.0")
			cacheDir := workspaceconfig.Config{}.ResolvedArtifactCacheDir()
			if tc.damage != nil {
				tc.damage(t, root, cacheDir, seeded)
			}
			lazy := catalog.NewLazyLookup(root, cacheDir, nil, nil)
			// No puller, as an offline run's lookup has none.
			var lookup closure.Lookup = lazy.ClosureLookup(nil, root, cacheDir, nil, nil, nil)
			if !tc.nothingHeld {
				lookup = entityAnswered{Lookup: lookup, entity: closure.Resolution{ResolvedVersion: "1.0.0",
					ManifestDigest: seeded.ManifestDigest, ResolutionPath: closure.ResolutionPathCache}}
			}
			cfg := verdictSignatureConfig(nil, true)
			cfg.Plugins.Linter = []workspaceconfig.PluginRef{{URI: seeded.URI}}
			asked := 0

			var diag bytes.Buffer
			resolutions, err := cacheVerdictSocketPlugins(context.Background(), cfg, lookup,
				pipeline.CatalogPluginArtifacts(root, cfg, lazy.PluginColocatedDirs()), lazy, root, cacheDir,
				countingTarget(&asked), tc.registries, &diag)
			if asked != 0 {
				t.Errorf("the walk asked a registry %d question(s)", asked)
			}
			if tc.halt != nil {
				if !errors.Is(err, tc.halt) {
					t.Fatalf("the walk must halt with %v; got %v", tc.halt, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("the walk must complete: %v", err)
			}
			if !tc.identified {
				if len(resolutions) != 0 {
					t.Errorf("a plugin the cache holds nothing for yields no resolution; got %+v", resolutions)
				}
				return
			}
			if len(resolutions) != 1 {
				t.Fatalf("one resolution must be returned; got %+v", resolutions)
			}
			got := resolutions[0]
			if got.Coordinate != seeded.Coordinate || got.Digest != seeded.WasmDigest || got.Version != "1.0.0" {
				t.Errorf("the plugin must be identified from the cache; got %q %q %q", got.Coordinate, got.Digest, got.Version)
			}
			if !got.SignatureNotAsked || got.Signature != nil || got.Deprecation != nil {
				t.Errorf("the plugin must be marked not asked with no fact: %+v", got)
			}
			if !strings.Contains(diag.String(), "host functions match") {
				t.Errorf("the host functions must be checked against the cached .wasm; diagnostics:\n%s", diag.String())
			}
		})
	}
}

// TestAnOfflineVerifyReportsOnlyWhatARecordCertainlyMoves holds the record half of
// an offline verify, for a plugin whose registry the walk did not ask, to the cells
// of entry and requirement, each read through the build's own check of the record: a
// plugin the build refuses for recording no signature — its entry records none, or
// the record holds no entry for it — or because its entry's signature was made for
// another artifact, is reported without asking, because a pull rewrites it whatever
// the registry answers, and is not named on the line; every other entry — its own
// signature, which the build accepts, and one whose cached bundle is gone, which only
// the registry's answer settles — is named on the line saying its signature was not
// checked, and nothing is reported about it; a first sighting nothing requires is
// neither. The control is a held entry resolved by a walk that did ask and verified
// none: that is a signature a pull clears, and it is reported.
func TestAnOfflineVerifyReportsOnlyWhatARecordCertainlyMoves(t *testing.T) {
	t.Parallel()
	// Each lays into root the cached bundle an entry's signature names, if any, and
	// returns the signature.
	own := func(t *testing.T, root string, seeded verdicttest.Cached) *lockfile.VerifiedSignature {
		return &lockfile.VerifiedSignature{Mode: "keyed", Identity: "keyed:sha256:" + strings.Repeat("e", 64),
			BundleDigest: cacheBundleMadeFor(t, root, seeded.ManifestDigest)}
	}
	another := func(t *testing.T, root string, _ verdicttest.Cached) *lockfile.VerifiedSignature {
		return &lockfile.VerifiedSignature{Mode: "keyed", Identity: "keyed:sha256:" + strings.Repeat("e", 64),
			BundleDigest: cacheBundleMadeFor(t, root, digest.ContentDigest([]byte("the manifest of another artifact")))}
	}
	bundleGone := func(*testing.T, string, verdicttest.Cached) *lockfile.VerifiedSignature {
		return &lockfile.VerifiedSignature{Mode: "keyed", Identity: "keyed:sha256:" + strings.Repeat("e", 64),
			BundleDigest: "sha256:" + strings.Repeat("b", 64)}
	}
	madeForAnother := ", made for another artifact, " + askedForTheArtifactsOwnSignature
	for _, tc := range []struct {
		name     string
		required bool
		// entry lays the signature the record's entry for the plugin carries; nil, the
		// entry carries none; absent, the record holds no entry for it at all.
		entry  func(t *testing.T, root string, seeded verdicttest.Cached) *lockfile.VerifiedSignature
		absent bool
		// asked resolves the plugin as a walk that asked and verified none.
		asked bool
		// reportedAs is a phrase the one occurrence must carry; empty, none is reported.
		reportedAs string
		line       bool
	}{
		{name: "held with its own signature, required", required: true, entry: own, line: true},
		{name: "held with its own signature, not required", entry: own, line: true},
		{name: "held with a signature made for another artifact, required", required: true, entry: another,
			reportedAs: madeForAnother},
		{name: "held with a signature made for another artifact, not required", entry: another, reportedAs: madeForAnother},
		{name: "held with a signature whose cached bundle is gone, required", required: true, entry: bundleGone, line: true},
		{name: "held with no signature, required", required: true, reportedAs: "records no signature, " + askedForARequiredSignature},
		{name: "held with no signature, not required", line: true},
		{name: "no entry, required", required: true, absent: true, reportedAs: unrecordedEntry + ", " + askedForARequiredSignature},
		{name: "no entry, not required, a first sighting", absent: true},
		{name: "control: held with its own signature, asked and verified none", required: true, entry: own, asked: true,
			reportedAs: "a pull would record no signature"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			releasePath := filepath.Join(root, "release.yaml")
			seeded := verdicttest.SeedCached(t, root, filepath.Join(root, "unsearched-catalog"), "linter", "offline-probe", "1.0.0")
			cacheDir := workspaceconfig.Config{}.ResolvedArtifactCacheDir()
			view := catalog.NewLazyLookup(root, cacheDir, nil, nil)
			cfg := verdictSignatureConfig(nil, tc.required)
			cfg.Plugins.Linter = []workspaceconfig.PluginRef{{URI: seeded.URI}}
			if !tc.absent {
				var sig *lockfile.VerifiedSignature
				if tc.entry != nil {
					sig = tc.entry(t, root, seeded)
				}
				seed := &pluginstrust.Trust{Version: pluginstrust.CurrentVersion}
				seed.Upsert(pluginstrust.TrustedPlugin{Plugin: seeded.Coordinate, ResolvedVersion: "1.0.0",
					Digest: seeded.WasmDigest, Signature: sig})
				if err := pluginstrust.Write(pluginstrust.PathFor(releasePath), seed); err != nil {
					t.Fatalf("seed record: %v", err)
				}
			}
			bare, _ := splitAttachmentRef(seeded.URI)
			resolution := verdictPluginResolution{BareURI: bare, Coordinate: seeded.Coordinate, Version: "1.0.0",
				Digest: seeded.WasmDigest, SignatureNotAsked: !tc.asked}

			var diag bytes.Buffer
			differences, err := trustRecordSignatureDifferences(releasePath, root, cfg, view,
				[]verdictPluginResolution{resolution}, catalog.PinReuseMode{VerifyOnly: true}, &diag)
			if err != nil {
				t.Fatalf("compare the record: %v", err)
			}
			switch {
			case tc.reportedAs == "":
				if len(differences) != 0 {
					t.Errorf("nothing may be reported; got %v", differences)
				}
			case len(differences) != 1:
				t.Errorf("one occurrence must be reported; got %v", differences)
			default:
				if !errors.Is(differences[0], ErrRecordedSignatureOutOfDate()) {
					t.Errorf("the occurrence must carry the condition; got %v", differences[0])
				}
				if !strings.Contains(differences[0].Error(), tc.reportedAs) {
					t.Errorf("the occurrence must say %q:\n%v", tc.reportedAs, differences[0])
				}
			}
			const notChecked = "was not checked at its registry"
			if got := strings.Contains(diag.String(), notChecked); got != tc.line {
				t.Errorf("the line saying the signature was not checked: want %t, diagnostics:\n%s", tc.line, diag.String())
			}
			if tc.line && !strings.Contains(diag.String(), fmt.Sprintf("%q", seeded.Coordinate)) {
				t.Errorf("the line must name the plugin; diagnostics:\n%s", diag.String())
			}
		})
	}
}
