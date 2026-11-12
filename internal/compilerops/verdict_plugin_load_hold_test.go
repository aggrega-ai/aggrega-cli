// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compilerops

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/verdicttest"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// load runs the load a surface outside a build performs for the case's plugin: the
// record's contribution composed the way every Release-scoped surface composes it,
// and the plugin read from the content cache through it.
func (c verdictGateCase) load(t *testing.T, cfg workspaceconfig.Config, composed *pluginstrust.Trust) error {
	t.Helper()
	cfg.Plugins.Linter = append(cfg.Plugins.Linter, workspaceconfig.PluginRef{URI: c.seeded.URI})
	trusted, err := verdictPluginTrust(c.view, cfg, c.releasePath, composed)
	if err != nil {
		t.Fatalf("compose the record's contribution: %v", err)
	}
	sources := resolver.VerdictPluginTierSources{WorkspaceRoot: c.root, CacheDir: c.cacheDir}
	_, err = resolver.ResolveVerdictPluginFromCache(sources, trusted, c.view.PluginFromCacheByURI, c.seeded.URI)
	return err
}

// verdictClass names which of the three outcomes an error is, so the build's check
// and the plugin's load can be compared on what they decided rather than on how
// each frames it.
func verdictClass(err error) string {
	switch {
	case err == nil:
		return "admitted"
	case errors.Is(err, lockfile.ErrSignatureNotRecorded()):
		return "not-recorded"
	case errors.Is(err, oci.ErrSignatureBundleDigestMismatch()):
		return "bundle-mismatch"
	default:
		return "other: " + err.Error()
	}
}

// TestEveryLoadOfAVerdictPluginHoldsItAsTheBuildDoes is the parity between the two
// places a verdict plugin's recorded signature is held: the build's check of
// recorded signatures before anything renders, and the load every surface performs
// — a build's render, a Release-scoped validation, a Release-scoped lint. One fault
// per row, each run through both, and both must reach the same verdict; the
// positive rows are the controls that keep the refusals attributable.
//
// The plugin is served from the content cache, which is where a load outside a
// build reads a verdict plugin the catalog view does not serve. Two rows of the
// build's own table are not here because the load cannot reach them: a plugin the
// catalog view serves never reaches the cache read, and a record holding an entry
// no attachment accounts for is refused when the record is composed, before any
// load.
func TestEveryLoadOfAVerdictPluginHoldsItAsTheBuildDoes(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name  string
		build func(t *testing.T, c verdictGateCase) workspaceconfig.Config
		want  string
	}{
		{
			name: "the record carries an intact signature",
			build: func(t *testing.T, c verdictGateCase) workspaceconfig.Config {
				c.record(t, gateFact(t, c.root, c.cacheDir, []byte("verdict plugin")))
				return requiring()
			},
			want: "admitted",
		},
		{
			name: "the record's signature removed",
			build: func(t *testing.T, c verdictGateCase) workspaceconfig.Config {
				c.record(t, nil)
				return requiring()
			},
			want: "not-recorded",
		},
		{
			name: "no record at all",
			build: func(t *testing.T, c verdictGateCase) workspaceconfig.Config {
				return requiring()
			},
			want: "not-recorded",
		},
		{
			name: "no requirement",
			build: func(t *testing.T, c verdictGateCase) workspaceconfig.Config {
				c.record(t, nil)
				return workspaceconfig.Config{DefaultRegistry: gateRegistry}
			},
			want: "admitted",
		},
		{
			name: "an advisory registry",
			build: func(t *testing.T, c verdictGateCase) workspaceconfig.Config {
				c.record(t, nil)
				return workspaceconfig.Config{DefaultRegistry: gateRegistry, Registries: []workspaceconfig.RegistryConfig{{
					Host: gateRegistry, Signing: workspaceconfig.SigningConfig{Required: true}, Advisory: true,
				}}}
			},
			want: "admitted",
		},
		{
			name: "the registry's own skip",
			build: func(t *testing.T, c verdictGateCase) workspaceconfig.Config {
				c.record(t, nil)
				return workspaceconfig.Config{DefaultRegistry: gateRegistry, Registries: []workspaceconfig.RegistryConfig{{
					Host: gateRegistry, Signing: workspaceconfig.SigningConfig{Required: true}, SkipSignatureVerification: true,
				}}}
			},
			want: "admitted",
		},
		{
			name: "the registry's own skip under the top-level policy",
			build: func(t *testing.T, c verdictGateCase) workspaceconfig.Config {
				c.record(t, nil)
				cfg := requiring()
				cfg.Registries = []workspaceconfig.RegistryConfig{{Host: gateRegistry, SkipSignatureVerification: true}}
				return cfg
			},
			want: "not-recorded",
		},
		{
			name: "the global skip",
			build: func(t *testing.T, c verdictGateCase) workspaceconfig.Config {
				c.record(t, nil)
				cfg := requiring()
				cfg.SkipSignatureVerification = true
				return cfg
			},
			want: "admitted",
		},
		{
			name: "a recorded bundle tampered in the cache, no requirement",
			build: func(t *testing.T, c verdictGateCase) workspaceconfig.Config {
				fact := gateFact(t, c.root, c.cacheDir, []byte("verdict plugin"))
				if err := os.WriteFile(oci.SignatureBundlePath(c.root, c.cacheDir, fact.BundleDigest), []byte("tampered"), 0o644); err != nil {
					t.Fatalf("tamper the cached bundle: %v", err)
				}
				c.record(t, fact)
				return workspaceconfig.Config{DefaultRegistry: gateRegistry}
			},
			want: "bundle-mismatch",
		},
		{
			name: "a recorded bundle removed from the cache, under a requirement",
			build: func(t *testing.T, c verdictGateCase) workspaceconfig.Config {
				fact := gateFact(t, c.root, c.cacheDir, []byte("verdict plugin"))
				if err := os.Remove(oci.SignatureBundlePath(c.root, c.cacheDir, fact.BundleDigest)); err != nil {
					t.Fatalf("remove the cached bundle: %v", err)
				}
				c.record(t, fact)
				return requiring()
			},
			want: "bundle-mismatch",
		},
	}
	for i, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			c := newVerdictGateCase(t, "load-probe-"+string(rune('a'+i)), false)
			cfg := row.build(t, c)

			built := verdictClass(c.check(cfg, 1))
			loaded := verdictClass(c.load(t, cfg, nil))
			if built != row.want {
				t.Errorf("the build's check: %s; want %s", built, row.want)
			}
			if loaded != row.want {
				t.Errorf("the plugin's load: %s; want %s, as the build decides", loaded, row.want)
			}
		})
	}
}

// TestTheLoadsRefusalNamesThePluginTheRecordAndTheRemedy holds the occurrence a
// surface outside a build prints to the one the build prints for the same plugin.
func TestTheLoadsRefusalNamesThePluginTheRecordAndTheRemedy(t *testing.T) {
	t.Parallel()
	c := newVerdictGateCase(t, "load-text-probe", false)
	c.record(t, nil)
	err := c.load(t, requiring(), nil)
	if !errors.Is(err, lockfile.ErrSignatureNotRecorded()) {
		t.Fatalf("want the not-recorded condition, got %v", err)
	}
	for _, want := range []string{
		`"` + c.seeded.Coordinate + `"`,
		pluginstrust.PathFor(c.releasePath) + " records no signature for it",
		"`signing.required` at the top level of the workspace configuration",
		"aggrega pull --release " + c.releasePath,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not carry %q:\n%v", want, err)
		}
	}
	build := c.check(requiring(), 1)
	if build == nil || build.Error() != err.Error() {
		t.Errorf("the load and the build word the same refusal differently:\n load: %v\nbuild: %v", err, build)
	}
}

// TestTheLoadReadsTheSignatureFromTheRecordTheCallerComposed pins the render a
// resolution performs before it writes the record: the signature half of what the
// load is handed — the recorded signature and its bundle — is the composed record's,
// and the identity half — the recorded digest the bytes are compared against — the
// file's. Both directions are driven for the signature half, because either alone
// would pass a load that read the file.
func TestTheLoadReadsTheSignatureFromTheRecordTheCallerComposed(t *testing.T) {
	t.Parallel()
	composed := func(c verdictGateCase, digest string, sig *lockfile.VerifiedSignature) *pluginstrust.Trust {
		trust := &pluginstrust.Trust{Version: pluginstrust.CurrentVersion}
		trust.Upsert(pluginstrust.TrustedPlugin{Plugin: c.seeded.Coordinate, ResolvedVersion: "1.0.0", Digest: digest, Signature: sig})
		return trust
	}

	t.Run("a composed fact admits the plugin over a file that has none", func(t *testing.T) {
		t.Parallel()
		c := newVerdictGateCase(t, "load-composed-a", false)
		c.record(t, nil)
		fact := gateFact(t, c.root, c.cacheDir, []byte("verdict plugin"))
		if err := c.load(t, requiring(), composed(c, c.seeded.WasmDigest, fact)); err != nil {
			t.Fatalf("the composed record carries the fact, so the load must admit the plugin: %v", err)
		}
	})
	t.Run("a composed absence refuses the plugin over a file that has one", func(t *testing.T) {
		t.Parallel()
		c := newVerdictGateCase(t, "load-composed-b", false)
		c.record(t, gateFact(t, c.root, c.cacheDir, []byte("verdict plugin")))
		if err := c.load(t, requiring(), composed(c, c.seeded.WasmDigest, nil)); !errors.Is(err, lockfile.ErrSignatureNotRecorded()) {
			t.Fatalf("the composed record carries no fact, so the load must refuse whatever the file says; got %v", err)
		}
	})
	t.Run("the composed record's bundle is the one re-hashed", func(t *testing.T) {
		t.Parallel()
		c := newVerdictGateCase(t, "load-composed-c", false)
		onFile := gateFact(t, c.root, c.cacheDir, []byte("verdict plugin"))
		c.record(t, onFile)
		stale := *onFile
		stale.BundleDigest = "sha256:" + strings.Repeat("0", 64)
		if err := c.load(t, requiring(), composed(c, c.seeded.WasmDigest, &stale)); !errors.Is(err, oci.ErrSignatureBundleDigestMismatch()) {
			t.Fatalf("the composed record names a bundle the cache does not hold, so the load must refuse; got %v", err)
		}
	})
	t.Run("the recorded digest is the file's", func(t *testing.T) {
		t.Parallel()
		c := newVerdictGateCase(t, "load-composed-d", false)
		fact := gateFact(t, c.root, c.cacheDir, []byte("verdict plugin"))
		c.record(t, fact)
		wrong := "sha256:" + strings.Repeat("1", 64)
		if err := c.load(t, requiring(), composed(c, wrong, fact)); err != nil {
			t.Fatalf("the digest the bytes are compared against is read from the file, which agrees with the cache: %v", err)
		}
	})
}

// TestTheLoadDoesNotHoldAVerdictPluginTheBinaryCarries is the load's half of the
// binary exclusion, run alone because it registers a coordinate in the binary's own
// set for the duration of the test. The control beside it refuses the same plugin
// once it is no longer carried.
func TestTheLoadDoesNotHoldAVerdictPluginTheBinaryCarries(t *testing.T) {
	c := newVerdictGateCase(t, "load-carried-probe", false)
	restore := embedded.SeedForTest(c.seeded.Coordinate, verdicttest.HeaderOnlyWasm)
	t.Cleanup(restore)

	if err := c.load(t, requiring(), nil); err != nil {
		t.Fatalf("a plugin the binary carries must not be held to a recorded signature at its load: %v", err)
	}
	restore()
	if err := c.load(t, requiring(), nil); !errors.Is(err, lockfile.ErrSignatureNotRecorded()) {
		t.Fatalf("with the coordinate not carried the same plugin must be refused at its load; got %v", err)
	}
}
