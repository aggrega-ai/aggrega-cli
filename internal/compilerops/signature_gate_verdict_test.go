// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/verdicttest"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// verdictGateCase is a workspace holding one verdict-role plugin the way a pull
// leaves it: its entity and .wasm in the content cache, and — where the case says
// so — a catalog directory serving the entity instead. The record beside the
// Release is the case's to write.
type verdictGateCase struct {
	root        string
	releasePath string
	cacheDir    string
	seeded      verdicttest.Cached
	view        *catalog.LazyLookup
}

// newVerdictGateCase seeds the plugin and returns the case. servedFromCatalog puts
// the entity's catalog directory on the view, so the view serves it rather than
// the content cache.
func newVerdictGateCase(t *testing.T, name string, servedFromCatalog bool) verdictGateCase {
	t.Helper()
	root := t.TempDir()
	catalogDir := filepath.Join(root, "catalog")
	seeded := verdicttest.SeedCached(t, root, catalogDir, "linter", name, "1.0.0")
	cacheDir := workspaceconfig.Config{}.ResolvedArtifactCacheDir()
	var dirs []string
	if servedFromCatalog {
		dirs = []string{catalogDir}
	}
	return verdictGateCase{
		root:        root,
		releasePath: filepath.Join(root, "release.yaml"),
		cacheDir:    cacheDir,
		seeded:      seeded,
		view:        catalog.NewLazyLookup(root, cacheDir, dirs, nil),
	}
}

// record writes the record beside the case's Release holding the plugin with the
// signature fact given.
func (c verdictGateCase) record(t *testing.T, sig *lockfile.VerifiedSignature) {
	t.Helper()
	trust := &pluginstrust.Trust{Version: pluginstrust.CurrentVersion}
	trust.Upsert(pluginstrust.TrustedPlugin{
		Plugin: c.seeded.Coordinate, ResolvedVersion: "1.0.0", Digest: c.seeded.WasmDigest, Signature: sig,
	})
	if err := pluginstrust.Write(pluginstrust.PathFor(c.releasePath), trust); err != nil {
		t.Fatalf("write the record: %v", err)
	}
}

// check runs the build's check over the case under cfg, with the plugin attached
// at the linter socket, which returns a verdict, as many times as attachments says.
func (c verdictGateCase) check(cfg workspaceconfig.Config, attachments int) error {
	for range attachments {
		cfg.Plugins.Linter = append(cfg.Plugins.Linter, workspaceconfig.PluginRef{URI: c.seeded.URI})
	}
	in := NewPreviewInputs(NoExternalInputs(), c.view, c.releasePath, c.root, cfg, "0.0.0-signature-gate-probe")
	return enforceRecordedSignatures(in, closure.Closure{})
}

// TestTheBuildHoldsAVerdictPluginToTheSignatureItsRecordCarries is the second
// store's table: one fault per row, each asserted against the condition it pins,
// with the positive controls beside the refusals.
//
// The plugin is attached at the linter socket and served from the content cache,
// which is what a pull acquiring it from a registry leaves; the lockfile holds no
// row for it at all, so every refusal below can only have come from the record
// beside the lockfile.
func TestTheBuildHoldsAVerdictPluginToTheSignatureItsRecordCarries(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name string
		// build lays the case down and returns the configuration the check runs under
		// and how many attachments name the plugin.
		build func(t *testing.T, c verdictGateCase) (workspaceconfig.Config, int)
		// catalog serves the entity from a catalog directory rather than the cache.
		catalog  bool
		refused  bool
		mismatch bool
	}{
		{
			name: "the record carries an intact signature: builds",
			build: func(t *testing.T, c verdictGateCase) (workspaceconfig.Config, int) {
				c.record(t, gateFact(t, c.root, c.cacheDir, []byte("verdict plugin")))
				return requiring(), 1
			},
		},
		{
			name: "the record's signature removed: refused naming the plugin and the record",
			build: func(t *testing.T, c verdictGateCase) (workspaceconfig.Config, int) {
				c.record(t, nil)
				return requiring(), 1
			},
			refused: true,
		},
		{
			name: "the record holds no entry for the plugin: refused the same way",
			build: func(t *testing.T, c verdictGateCase) (workspaceconfig.Config, int) {
				trust := &pluginstrust.Trust{Version: pluginstrust.CurrentVersion}
				trust.Upsert(pluginstrust.TrustedPlugin{Plugin: "linter/other", ResolvedVersion: "1.0.0", Digest: c.seeded.WasmDigest})
				if err := pluginstrust.Write(pluginstrust.PathFor(c.releasePath), trust); err != nil {
					t.Fatalf("write the record: %v", err)
				}
				return requiring(), 1
			},
			refused: true,
		},
		{
			name: "no record at all: refused the same way, since deleting it must not admit the plugin",
			build: func(t *testing.T, c verdictGateCase) (workspaceconfig.Config, int) {
				return requiring(), 1
			},
			refused: true,
		},
		{
			name: "the plugin attached at two roles is refused once",
			build: func(t *testing.T, c verdictGateCase) (workspaceconfig.Config, int) {
				c.record(t, nil)
				return requiring(), 2
			},
			refused: true,
		},
		{
			name: "no requirement: the unsigned plugin builds",
			build: func(t *testing.T, c verdictGateCase) (workspaceconfig.Config, int) {
				c.record(t, nil)
				return workspaceconfig.Config{DefaultRegistry: gateRegistry}, 1
			},
		},
		{
			name: "an advisory registry: the unsigned plugin builds, as the pull admits it",
			build: func(t *testing.T, c verdictGateCase) (workspaceconfig.Config, int) {
				c.record(t, nil)
				return workspaceconfig.Config{DefaultRegistry: gateRegistry, Registries: []workspaceconfig.RegistryConfig{{
					Host: gateRegistry, Signing: workspaceconfig.SigningConfig{Required: true}, Advisory: true,
				}}}, 1
			},
		},
		{
			name: "the registry's own skip: the unsigned plugin builds, as the pull admits it",
			build: func(t *testing.T, c verdictGateCase) (workspaceconfig.Config, int) {
				c.record(t, nil)
				return workspaceconfig.Config{DefaultRegistry: gateRegistry, Registries: []workspaceconfig.RegistryConfig{{
					Host: gateRegistry, Signing: workspaceconfig.SigningConfig{Required: true}, SkipSignatureVerification: true,
				}}}, 1
			},
		},
		{
			name: "the registry's own skip does not stand down the top-level policy",
			build: func(t *testing.T, c verdictGateCase) (workspaceconfig.Config, int) {
				c.record(t, nil)
				cfg := requiring()
				cfg.Registries = []workspaceconfig.RegistryConfig{{Host: gateRegistry, SkipSignatureVerification: true}}
				return cfg, 1
			},
			refused: true,
		},
		{
			name: "the global skip: the unsigned plugin builds",
			build: func(t *testing.T, c verdictGateCase) (workspaceconfig.Config, int) {
				c.record(t, nil)
				cfg := requiring()
				cfg.SkipSignatureVerification = true
				return cfg, 1
			},
		},
		{
			name: "a plugin the catalog view serves builds unsigned under a requirement",
			build: func(t *testing.T, c verdictGateCase) (workspaceconfig.Config, int) {
				return requiring(), 1
			},
			catalog: true,
		},
		{
			name: "a recorded bundle tampered in the cache: refused as a mismatch",
			build: func(t *testing.T, c verdictGateCase) (workspaceconfig.Config, int) {
				fact := gateFact(t, c.root, c.cacheDir, []byte("verdict plugin"))
				if err := os.WriteFile(oci.SignatureBundlePath(c.root, c.cacheDir, fact.BundleDigest), []byte("tampered"), 0o644); err != nil {
					t.Fatalf("tamper the cached bundle: %v", err)
				}
				c.record(t, fact)
				return workspaceconfig.Config{DefaultRegistry: gateRegistry}, 1
			},
			mismatch: true,
		},
	}
	for i, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			c := newVerdictGateCase(t, "gate-probe-"+string(rune('a'+i)), row.catalog)
			cfg, attachments := row.build(t, c)
			err := c.check(cfg, attachments)

			if !row.refused && !row.mismatch {
				if err != nil {
					t.Fatalf("the check refused a build it must admit: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("the check admitted a build it must refuse")
			}
			if row.mismatch {
				if !errors.Is(err, oci.ErrSignatureBundleDigestMismatch()) {
					t.Fatalf("want the cached-bundle mismatch, got %v", err)
				}
				if errors.Is(err, lockfile.ErrSignatureNotRecorded()) {
					t.Errorf("a plugin whose record carries a fact was refused as carrying none: %v", err)
				}
				if !strings.Contains(err.Error(), c.seeded.Coordinate) {
					t.Errorf("the mismatch does not name %q: %v", c.seeded.Coordinate, err)
				}
				return
			}
			if !errors.Is(err, lockfile.ErrSignatureNotRecorded()) {
				t.Fatalf("want the not-recorded condition, got %v", err)
			}
			text := err.Error()
			for _, want := range []string{
				`"` + c.seeded.Coordinate + `"`,
				pluginstrust.PathFor(c.releasePath) + " records no signature for it",
				"`signing.required` at the top level of the workspace configuration",
				"aggrega pull --release " + c.releasePath,
			} {
				if !strings.Contains(text, want) {
					t.Errorf("the refusal does not carry %q:\n%s", want, text)
				}
			}
			if n := strings.Count(text, `"`+c.seeded.Coordinate+`"`); n != 1 {
				t.Errorf("the plugin is named %d times; one plugin is one refusal:\n%s", n, text)
			}
		})
	}
}

// TestTheCheckReadsTheRecordTheCallerComposedRatherThanTheFile pins the render a
// resolution performs before it writes the record: handed the record it composed,
// the check reads that one and not the file it is about to replace. Both
// directions are driven, because either alone would pass a check that read the
// file — a composed fact over a file carrying none, and a composed absence over a
// file carrying one.
func TestTheCheckReadsTheRecordTheCallerComposedRatherThanTheFile(t *testing.T) {
	t.Parallel()
	composed := func(c verdictGateCase, sig *lockfile.VerifiedSignature) *pluginstrust.Trust {
		trust := &pluginstrust.Trust{Version: pluginstrust.CurrentVersion}
		trust.Upsert(pluginstrust.TrustedPlugin{
			Plugin: c.seeded.Coordinate, ResolvedVersion: "1.0.0", Digest: c.seeded.WasmDigest, Signature: sig,
		})
		return trust
	}
	run := func(c verdictGateCase, trust *pluginstrust.Trust) error {
		cfg := requiring()
		cfg.Plugins.Linter = []workspaceconfig.PluginRef{{URI: c.seeded.URI}}
		in := NewPreviewInputs(NoExternalInputs(), c.view, c.releasePath, c.root, cfg, "0.0.0-signature-gate-probe",
			WithComposedTrustRecord(trust))
		return enforceRecordedSignatures(in, closure.Closure{})
	}

	t.Run("a composed fact admits the plugin over a file that has none", func(t *testing.T) {
		t.Parallel()
		c := newVerdictGateCase(t, "composed-probe-a", false)
		if err := run(c, composed(c, gateFact(t, c.root, c.cacheDir, []byte("verdict plugin")))); err != nil {
			t.Fatalf("the composed record carries the fact, so the plugin must be admitted: %v", err)
		}
	})
	t.Run("a composed absence refuses the plugin over a file that has one", func(t *testing.T) {
		t.Parallel()
		c := newVerdictGateCase(t, "composed-probe-b", false)
		c.record(t, gateFact(t, c.root, c.cacheDir, []byte("verdict plugin")))
		if err := run(c, composed(c, nil)); !errors.Is(err, lockfile.ErrSignatureNotRecorded()) {
			t.Fatalf("the composed record carries no fact, so the plugin must be refused whatever the file says; got %v", err)
		}
	})
}

// TestTheBuildDoesNotHoldAVerdictPluginTheBinaryCarries is the one exclusion the
// table above cannot reach in parallel, because it registers a coordinate in the
// binary's own set for the duration of the test. A plugin whose bytes the binary
// carries runs from the binary whatever served its entity, and a pull never
// records one, so holding it would refuse a build with a remedy that records
// nothing.
func TestTheBuildDoesNotHoldAVerdictPluginTheBinaryCarries(t *testing.T) {
	c := newVerdictGateCase(t, "carried-gate-probe", false)
	restore := embedded.SeedForTest(c.seeded.Coordinate, verdicttest.HeaderOnlyWasm)
	t.Cleanup(restore)

	if err := c.check(requiring(), 1); err != nil {
		t.Fatalf("a plugin the binary carries must not be held to a recorded signature: %v", err)
	}

	// The control: the same case, the coordinate no longer carried, is refused — so
	// the pass above is the exclusion and not a check that holds nothing.
	restore()
	err := c.check(requiring(), 1)
	if !errors.Is(err, lockfile.ErrSignatureNotRecorded()) {
		t.Fatalf("with the coordinate not carried the same plugin must be refused; got %v", err)
	}
}
