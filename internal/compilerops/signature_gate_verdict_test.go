// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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

// heldAlike asks both readers of the build's rule about the case under cfg, with the
// plugin attached at the linter socket attachments times and no record beside the
// Release: how many times VerdictPluginSignatureRefusals lists the plugin as refused
// for recording no signature, over the record as a reader loads it, and what the
// build's own check returns.
func (c verdictGateCase) heldAlike(t *testing.T, cfg workspaceconfig.Config, attachments int) (listed int, checked error) {
	t.Helper()
	for range attachments {
		cfg.Plugins.Linter = append(cfg.Plugins.Linter, workspaceconfig.PluginRef{URI: c.seeded.URI})
	}
	record, _, err := pluginstrust.LoadOrStart(pluginstrust.PathFor(c.releasePath))
	if err != nil {
		t.Fatalf("load the record: %v", err)
	}
	for _, refused := range VerdictPluginSignatureRefusals(c.view, cfg, c.root, c.releasePath, record) {
		if refused.Plugin == c.seeded.Coordinate && errors.Is(refused.Refusal, lockfile.ErrSignatureNotRecorded()) {
			listed++
		}
	}
	in := NewPreviewInputs(NoExternalInputs(), c.view, c.releasePath, c.root, cfg, "0.0.0-signature-gate-probe")
	return listed, enforceRecordedSignatures(in, closure.Closure{})
}

// TestTheExportedQuestionIsTheBuildsAnswerForAPluginRecordingNoSignature holds the
// one entry other packages ask — which plugins a build refuses over the record, and
// for what — to the build's own check, over the record's first-sighting state (no
// record at all): the plugin is listed as refused for recording no signature exactly
// where the check refuses it so, and admitted cleanly where it is not. The catalog
// row is the one a reader of the walk's cache slot would get wrong: the plugin's
// bytes are in the cache, and the view serves it from a catalog directory.
func TestTheExportedQuestionIsTheBuildsAnswerForAPluginRecordingNoSignature(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name        string
		cfg         workspaceconfig.Config
		catalog     bool
		attachments int
		want        bool
	}{
		{name: "served from the cache under a requirement: listed and refused", cfg: requiring(), attachments: 1, want: true},
		{name: "attached at two roles: listed once and refused once", cfg: requiring(), attachments: 2, want: true},
		{name: "no requirement: neither", cfg: workspaceconfig.Config{DefaultRegistry: gateRegistry}, attachments: 1},
		{name: "an advisory registry: neither", cfg: workspaceconfig.Config{DefaultRegistry: gateRegistry, Registries: []workspaceconfig.RegistryConfig{{
			Host: gateRegistry, Signing: workspaceconfig.SigningConfig{Required: true}, Advisory: true,
		}}}, attachments: 1},
		{name: "the registry's own skip: neither", cfg: workspaceconfig.Config{DefaultRegistry: gateRegistry, Registries: []workspaceconfig.RegistryConfig{{
			Host: gateRegistry, Signing: workspaceconfig.SigningConfig{Required: true}, SkipSignatureVerification: true,
		}}}, attachments: 1},
		{name: "the global skip: neither", cfg: func() workspaceconfig.Config {
			cfg := requiring()
			cfg.SkipSignatureVerification = true
			return cfg
		}(), attachments: 1},
		{name: "served from a catalog directory with its bytes in the cache too: neither", cfg: requiring(), catalog: true, attachments: 1},
	}
	for i, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			c := newVerdictGateCase(t, "exported-probe-"+string(rune('a'+i)), row.catalog)
			listed, err := c.heldAlike(t, row.cfg, row.attachments)
			if listed > 1 {
				t.Errorf("the plugin is listed %d times; one plugin is one answer", listed)
			}
			if got := listed == 1; got != row.want {
				t.Errorf("listed=%v, want %v", got, row.want)
			}
			if row.want {
				if !errors.Is(err, lockfile.ErrSignatureNotRecorded()) {
					t.Fatalf("the build's check must refuse the plugin the entry lists; got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("the build's check must admit the plugin the entry leaves out; got %v", err)
			}
		})
	}
}

// TestTheExportedQuestionLeavesOutAPluginTheBinaryCarries is the binary-carried row
// of the table above, run alone because it registers a coordinate in the binary's own
// set for the duration of the test; its control is the same case refused once the
// coordinate is no longer carried.
func TestTheExportedQuestionLeavesOutAPluginTheBinaryCarries(t *testing.T) {
	c := newVerdictGateCase(t, "exported-carried-probe", false)
	restore := embedded.SeedForTest(c.seeded.Coordinate, verdicttest.HeaderOnlyWasm)
	t.Cleanup(restore)

	if listed, err := c.heldAlike(t, requiring(), 1); listed != 0 || err != nil {
		t.Fatalf("a plugin the binary carries must be neither listed nor refused; listed %d times, check %v", listed, err)
	}
	restore()
	if listed, err := c.heldAlike(t, requiring(), 1); listed != 1 || !errors.Is(err, lockfile.ErrSignatureNotRecorded()) {
		t.Fatalf("with the coordinate not carried the plugin must be listed once and refused; listed %d times, check %v", listed, err)
	}
}

// TestTheExportedRefusalsNameEachPluginByItsOwnRefusal holds the entry other
// packages read to the plugin each refusal belongs to and to the condition it
// carries, over a record holding two plugins served by the content cache: one whose
// entry carries the other's signature, which the build refuses as made for another
// artifact whatever the requirement, and one whose entry carries none, which the
// build refuses under a requirement only. The plugin whose own signature was copied
// is admitted, so it is listed under neither, and each refusal names its plugin.
func TestTheExportedRefusalsNameEachPluginByItsOwnRefusal(t *testing.T) {
	t.Parallel()
	for _, required := range []bool{true, false} {
		t.Run(map[bool]string{true: "under a requirement", false: "under none"}[required], func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			catalogDir := filepath.Join(root, "catalog")
			suffix := map[bool]string{true: "r", false: "n"}[required]
			a := verdicttest.SeedCached(t, root, catalogDir, "linter", "refusal-a-"+suffix, "1.0.0")
			b := verdicttest.SeedCached(t, root, catalogDir, "linter", "refusal-b-"+suffix, "1.0.0")
			unsigned := verdicttest.SeedCached(t, root, catalogDir, "linter", "refusal-c-"+suffix, "1.0.0")
			cacheDir := workspaceconfig.Config{}.ResolvedArtifactCacheDir()
			releasePath := filepath.Join(root, "release.yaml")
			factA := signedFactFor(t, root, cacheDir, copiedFactSigner(t), a.ManifestDigest)
			copied := *factA
			record := &pluginstrust.Trust{Version: pluginstrust.CurrentVersion}
			record.Upsert(pluginstrust.TrustedPlugin{Plugin: a.Coordinate, ResolvedVersion: "1.0.0", Digest: a.WasmDigest, Signature: factA})
			record.Upsert(pluginstrust.TrustedPlugin{Plugin: b.Coordinate, ResolvedVersion: "1.0.0", Digest: b.WasmDigest, Signature: &copied})
			record.Upsert(pluginstrust.TrustedPlugin{Plugin: unsigned.Coordinate, ResolvedVersion: "1.0.0", Digest: unsigned.WasmDigest})
			cfg := workspaceconfig.Config{DefaultRegistry: gateRegistry}
			if required {
				cfg = requiring()
			}
			cfg.Plugins.Linter = []workspaceconfig.PluginRef{{URI: a.URI}, {URI: b.URI}, {URI: unsigned.URI}}
			view := catalog.NewLazyLookup(root, cacheDir, nil, nil)

			got := map[string]error{}
			for _, refused := range VerdictPluginSignatureRefusals(view, cfg, root, releasePath, record) {
				if _, twice := got[refused.Plugin]; twice {
					t.Errorf("%q is refused twice; one plugin is one refusal", refused.Plugin)
				}
				got[refused.Plugin] = refused.Refusal
				if !strings.Contains(refused.Refusal.Error(), `"`+refused.Plugin+`"`) {
					t.Errorf("the refusal listed under %q does not name it:\n%v", refused.Plugin, refused.Refusal)
				}
			}
			if err, listed := got[a.Coordinate]; listed {
				t.Errorf("the plugin whose own signature its entry carries is refused: %v", err)
			}
			if !errors.Is(got[b.Coordinate], oci.ErrSignatureForAnotherArtifact()) {
				t.Errorf("the plugin carrying another plugin's signature: want the signature-for-another-artifact refusal, got %v",
					got[b.Coordinate])
			}
			err, listed := got[unsigned.Coordinate]
			switch {
			case required && !errors.Is(err, lockfile.ErrSignatureNotRecorded()):
				t.Errorf("the plugin recording no signature under a requirement: want the not-recorded refusal, got %v", err)
			case !required && listed:
				t.Errorf("the plugin recording no signature under no requirement is refused: %v", err)
			}
		})
	}
}
