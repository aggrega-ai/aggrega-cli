// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The references every case below reads: one per section the check walks, each a
// row the content cache served unless a case says otherwise.
const (
	gateRegistry   = "registry.example.com"
	gateEntityURI  = "test/svc"
	gateLibraryURI = "DbSizing"
	gatePluginURI  = "governance/tag-injector"
	gateRelease    = "release.yaml"
)

// gateClosure is the resolution a build hands the check: a Component on the
// entity axis, a SchemaLibrary on its library axis and a plugin artifact on the
// plugin axis, each served at the resolution path a case names.
func gateClosure(entityPath, libraryPath, pluginPath string) closure.Closure {
	return closure.Closure{
		Entities: []closure.ResolvedRef{{
			URI: gateEntityURI, ResolvedVersion: "1.0.0", ResolutionPath: entityPath, KindHint: "component",
		}},
		SchemaLibraries: []closure.ResolvedLibrary{{
			URI: gateLibraryURI, ResolvedVersion: "1.0.0", ResolutionPath: libraryPath,
		}},
		Plugins: []closure.ResolvedRef{{
			URI: gatePluginURI, ResolvedVersion: "1.0.0", ResolutionPath: pluginPath, KindHint: "plugin",
		}},
	}
}

// gateFact records a verified signature whose bundle the case writes into the
// workspace's cache at the slot the pull would have written it to, and returns the
// fact.
func gateFact(t *testing.T, root, cacheDir string, bundle []byte) *lockfile.VerifiedSignature {
	t.Helper()
	d := digest.ContentDigest(bundle)
	path := oci.SignatureBundlePath(root, cacheDir, d)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir the bundle cache: %v", err)
	}
	if err := os.WriteFile(path, bundle, 0o644); err != nil {
		t.Fatalf("cache the bundle: %v", err)
	}
	return &lockfile.VerifiedSignature{Mode: "keyed", Identity: "keyed:sha256:probe", BundleDigest: d}
}

// gateLockfile records a row for each reference of gateClosure, each carrying the
// fact the case hands it.
func gateLockfile(entity, library, plugin *lockfile.VerifiedSignature) *lockfile.Lockfile {
	return &lockfile.Lockfile{
		Version:         1,
		Entities:        []lockfile.Entity{{URI: gateEntityURI, ResolvedVersion: "1.0.0", Signature: entity}},
		SchemaLibraries: []lockfile.SchemaLibrary{{URI: gateLibraryURI, ResolvedVersion: "1.0.0", Signature: library}},
		Plugins:         []lockfile.Plugin{{URI: gatePluginURI, ResolvedVersion: "1.0.0", Signature: plugin}},
	}
}

// gateInputs builds the inputs the check reads.
func gateInputs(root string, cfg workspaceconfig.Config, lf *lockfile.Lockfile) PreviewInputs {
	return NewPreviewInputs(NoExternalInputs(), nil, gateRelease, root, cfg, "0.0.0-signature-gate-probe", WithLockfile(lf, true))
}

// requiring is a configuration whose top-level policy requires a signature on
// every kind, over a default registry no entry covers.
func requiring() workspaceconfig.Config {
	return workspaceconfig.Config{DefaultRegistry: gateRegistry, Signing: workspaceconfig.SigningConfig{Required: true}}
}

// TestTheBuildRefusesARowARequirementCoversThatRecordsNoSignature is the check's
// table: one fault per row, each asserted against the condition it pins, with the
// positive controls beside the refusals so a check that refused everything — or
// nothing — fails here.
func TestTheBuildRefusesARowARequirementCoversThatRecordsNoSignature(t *testing.T) {
	t.Parallel()
	cache := closure.ResolutionPathCache
	local := closure.ResolutionPathCatalog
	rows := []struct {
		name string
		// build lays the case down under root and returns what the check reads.
		build func(t *testing.T, root string) (workspaceconfig.Config, *lockfile.Lockfile, closure.Closure)
		// refused names the references the check must refuse as recording no
		// signature; empty means it must pass.
		refused []string
		// mismatch names the reference whose cached bundle must be refused.
		mismatch string
	}{
		{
			name: "every row records an intact signature: builds",
			build: func(t *testing.T, root string) (workspaceconfig.Config, *lockfile.Lockfile, closure.Closure) {
				cfg := requiring()
				dir := cfg.ResolvedArtifactCacheDir()
				return cfg, gateLockfile(gateFact(t, root, dir, []byte("entity")), gateFact(t, root, dir, []byte("library")),
					gateFact(t, root, dir, []byte("plugin"))), gateClosure(cache, cache, cache)
			},
		},
		{
			name: "the entity row's signature removed: refused naming the row",
			build: func(t *testing.T, root string) (workspaceconfig.Config, *lockfile.Lockfile, closure.Closure) {
				cfg := requiring()
				dir := cfg.ResolvedArtifactCacheDir()
				return cfg, gateLockfile(nil, gateFact(t, root, dir, []byte("library")),
					gateFact(t, root, dir, []byte("plugin"))), gateClosure(cache, cache, cache)
			},
			refused: []string{gateEntityURI},
		},
		{
			name: "the library row's signature removed: refused naming the row",
			build: func(t *testing.T, root string) (workspaceconfig.Config, *lockfile.Lockfile, closure.Closure) {
				cfg := requiring()
				dir := cfg.ResolvedArtifactCacheDir()
				return cfg, gateLockfile(gateFact(t, root, dir, []byte("entity")), nil,
					gateFact(t, root, dir, []byte("plugin"))), gateClosure(cache, cache, cache)
			},
			refused: []string{gateLibraryURI},
		},
		{
			name: "the plugin row's signature removed: refused naming the row",
			build: func(t *testing.T, root string) (workspaceconfig.Config, *lockfile.Lockfile, closure.Closure) {
				cfg := requiring()
				dir := cfg.ResolvedArtifactCacheDir()
				return cfg, gateLockfile(gateFact(t, root, dir, []byte("entity")), gateFact(t, root, dir, []byte("library")),
					nil), gateClosure(cache, cache, cache)
			},
			refused: []string{gatePluginURI},
		},
		{
			name: "every signature removed: every row refused together",
			build: func(t *testing.T, _ string) (workspaceconfig.Config, *lockfile.Lockfile, closure.Closure) {
				return requiring(), gateLockfile(nil, nil, nil), gateClosure(cache, cache, cache)
			},
			refused: []string{gateEntityURI, gateLibraryURI, gatePluginURI},
		},
		{
			name: "no lockfile at all: a cache-served row records nothing and is refused",
			build: func(t *testing.T, _ string) (workspaceconfig.Config, *lockfile.Lockfile, closure.Closure) {
				return requiring(), nil, gateClosure(cache, local, local)
			},
			refused: []string{gateEntityURI},
		},
		{
			name: "no requirement: an unsigned row builds",
			build: func(t *testing.T, _ string) (workspaceconfig.Config, *lockfile.Lockfile, closure.Closure) {
				return workspaceconfig.Config{DefaultRegistry: gateRegistry}, gateLockfile(nil, nil, nil), gateClosure(cache, cache, cache)
			},
		},
		{
			name: "an advisory registry: an unsigned row builds, as the pull admits it",
			build: func(t *testing.T, _ string) (workspaceconfig.Config, *lockfile.Lockfile, closure.Closure) {
				cfg := workspaceconfig.Config{DefaultRegistry: gateRegistry, Registries: []workspaceconfig.RegistryConfig{{
					Host: gateRegistry, Signing: workspaceconfig.SigningConfig{Required: true}, Advisory: true,
				}}}
				return cfg, gateLockfile(nil, nil, nil), gateClosure(cache, cache, cache)
			},
		},
		{
			name: "the registry's own skip: an unsigned row builds, as the pull admits it",
			build: func(t *testing.T, _ string) (workspaceconfig.Config, *lockfile.Lockfile, closure.Closure) {
				cfg := workspaceconfig.Config{DefaultRegistry: gateRegistry, Registries: []workspaceconfig.RegistryConfig{{
					Host: gateRegistry, Signing: workspaceconfig.SigningConfig{Required: true}, SkipSignatureVerification: true,
				}}}
				return cfg, gateLockfile(nil, nil, nil), gateClosure(cache, cache, cache)
			},
		},
		{
			name: "the registry's own skip does not stand down the top-level policy",
			build: func(t *testing.T, _ string) (workspaceconfig.Config, *lockfile.Lockfile, closure.Closure) {
				cfg := requiring()
				cfg.Registries = []workspaceconfig.RegistryConfig{{Host: gateRegistry, SkipSignatureVerification: true}}
				return cfg, gateLockfile(nil, nil, nil), gateClosure(cache, local, local)
			},
			refused: []string{gateEntityURI},
		},
		{
			name: "the global skip: an unsigned row builds",
			build: func(t *testing.T, _ string) (workspaceconfig.Config, *lockfile.Lockfile, closure.Closure) {
				cfg := requiring()
				cfg.SkipSignatureVerification = true
				return cfg, gateLockfile(nil, nil, nil), gateClosure(cache, cache, cache)
			},
		},
		{
			name: "rows read from a catalog directory build unsigned under a requirement",
			build: func(t *testing.T, _ string) (workspaceconfig.Config, *lockfile.Lockfile, closure.Closure) {
				return requiring(), gateLockfile(nil, nil, nil), gateClosure(local, local, local)
			},
		},
		{
			name: "rows from the binary and beside the Release build unsigned under a requirement",
			build: func(t *testing.T, _ string) (workspaceconfig.Config, *lockfile.Lockfile, closure.Closure) {
				return requiring(), gateLockfile(nil, nil, nil),
					gateClosure(closure.ResolutionPathExternal, local, closure.ResolutionPathEmbedded)
			},
		},
		{
			name: "a recorded bundle tampered in the cache: refused as a mismatch",
			build: func(t *testing.T, root string) (workspaceconfig.Config, *lockfile.Lockfile, closure.Closure) {
				cfg := workspaceconfig.Config{DefaultRegistry: gateRegistry}
				dir := cfg.ResolvedArtifactCacheDir()
				fact := gateFact(t, root, dir, []byte("entity"))
				if err := os.WriteFile(oci.SignatureBundlePath(root, dir, fact.BundleDigest), []byte("tampered"), 0o644); err != nil {
					t.Fatalf("tamper the cached bundle: %v", err)
				}
				return cfg, gateLockfile(fact, nil, nil), gateClosure(cache, local, local)
			},
			mismatch: gateEntityURI,
		},
		{
			name: "a recorded bundle missing from the cache: refused as a mismatch",
			build: func(t *testing.T, _ string) (workspaceconfig.Config, *lockfile.Lockfile, closure.Closure) {
				fact := &lockfile.VerifiedSignature{Mode: "keyed", BundleDigest: digest.ContentDigest([]byte("never cached"))}
				return workspaceconfig.Config{DefaultRegistry: gateRegistry}, gateLockfile(nil, nil, fact), gateClosure(local, local, cache)
			},
			mismatch: gatePluginURI,
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			cfg, lf, cl := row.build(t, root)
			err := enforceRecordedSignatures(gateInputs(root, cfg, lf), cl)

			if len(row.refused) == 0 && row.mismatch == "" {
				if err != nil {
					t.Fatalf("the check refused a build it must admit: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("the check admitted a build it must refuse (want %v / mismatch %q)", row.refused, row.mismatch)
			}
			if row.mismatch != "" {
				if !errors.Is(err, oci.ErrSignatureBundleDigestMismatch()) {
					t.Fatalf("want the cached-bundle mismatch, got %v", err)
				}
				if errors.Is(err, lockfile.ErrSignatureNotRecorded()) {
					t.Errorf("a row recording a fact was refused as recording none: %v", err)
				}
				if !strings.Contains(err.Error(), row.mismatch) {
					t.Errorf("the mismatch does not name %q: %v", row.mismatch, err)
				}
				return
			}
			if !errors.Is(err, lockfile.ErrSignatureNotRecorded()) {
				t.Fatalf("want the not-recorded condition, got %v", err)
			}
			if errors.Is(err, oci.ErrSignatureBundleDigestMismatch()) {
				t.Errorf("an absent fact was reported as a mismatched bundle: %v", err)
			}
			text := err.Error()
			for _, uri := range row.refused {
				if !strings.Contains(text, `"`+uri+`"`) {
					t.Errorf("the refusal does not name %q:\n%s", uri, text)
				}
			}
			if !strings.Contains(text, "`signing.required` at the top level of the workspace configuration") {
				t.Errorf("the refusal does not name the setting that requires the signature:\n%s", text)
			}
			if !strings.Contains(text, "aggrega pull --release "+gateRelease) {
				t.Errorf("the refusal does not name the command that records the fact:\n%s", text)
			}
			// The occurrence names the file that lacks the record, as the trust record's
			// does, so a reader is never left to infer which file the pull writes.
			lockfilePath := lockfile.PathFor(gateRelease)
			for _, want := range []string{lockfilePath + " records no signature for it", "record it in " + lockfilePath} {
				if !strings.Contains(text, want) {
					t.Errorf("the refusal does not name the lockfile that lacks the record (%q):\n%s", want, text)
				}
			}
			// Exactly the rows named, and no others: a row the check should have left
			// alone and refused anyway is as much a defect as one it missed.
			for _, other := range []string{gateEntityURI, gateLibraryURI, gatePluginURI} {
				named := strings.Contains(text, `"`+other+`"`)
				want := false
				for _, uri := range row.refused {
					want = want || uri == other
				}
				if named && !want {
					t.Errorf("the refusal names %q, which the case left alone:\n%s", other, text)
				}
			}
		})
	}
}

// TestAnEntityRowAndAPluginRowRecordingOneReferenceAreReadApart holds the join
// between a reference and its row to the section both are in. A Plugin entity and
// its .wasm artifact can be recorded under the same reference, one row in each
// section; a join on the reference alone would let either row's fact stand in for
// the other's absence.
func TestAnEntityRowAndAPluginRowRecordingOneReferenceAreReadApart(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cfg := requiring()
	const shared = "governance/tag-injector"
	cl := closure.Closure{
		Entities: []closure.ResolvedRef{{URI: shared, ResolvedVersion: "1.0.0", ResolutionPath: closure.ResolutionPathCache, KindHint: "plugin"}},
		Plugins:  []closure.ResolvedRef{{URI: shared, ResolvedVersion: "1.0.0", ResolutionPath: closure.ResolutionPathCache, KindHint: "plugin"}},
	}
	lf := &lockfile.Lockfile{
		Version:  1,
		Entities: []lockfile.Entity{{URI: shared, ResolvedVersion: "1.0.0", Signature: gateFact(t, root, cfg.ResolvedArtifactCacheDir(), []byte("entity"))}},
		Plugins:  []lockfile.Plugin{{URI: shared, ResolvedVersion: "1.0.0"}},
	}
	err := enforceRecordedSignatures(gateInputs(root, cfg, lf), cl)
	if !errors.Is(err, lockfile.ErrSignatureNotRecorded()) {
		t.Fatalf("the plugin row records no signature and the entity row's fact must not stand in for it; got %v", err)
	}
}

// TestEveryVerifiableAxisHasALockfileSectionOfTheSameName is what the join above
// rests on: the closure names each axis as the Lockfile field is named. Were an
// axis renamed on one side only, every lookup on it would miss and every row on it
// would read as recording nothing — refused under a requirement, which fails
// closed, but refused for a reason that is not true.
func TestEveryVerifiableAxisHasALockfileSectionOfTheSameName(t *testing.T) {
	t.Parallel()
	sections := map[string]bool{}
	lf := &lockfile.Lockfile{
		Entities:          []lockfile.Entity{{URI: "e"}},
		SchemaLibraries:   []lockfile.SchemaLibrary{{URI: "s"}},
		MappingLibraries:  []lockfile.MappingLibrary{{URI: "m"}},
		WorkloadLibraries: []lockfile.WorkloadLibrary{{URI: "w"}},
		ArtifactLibraries: []lockfile.ArtifactLibrary{{URI: "a"}},
		Plugins:           []lockfile.Plugin{{URI: "p"}},
	}
	for _, row := range lf.VerifiedFactRows() {
		sections[row.Section] = true
	}
	axes := closure.Closure{}.VerifiableAxes()
	if len(axes) == 0 {
		t.Fatal("the closure enumerates no verifiable axes, so this asserts nothing")
	}
	for _, axis := range axes {
		if !sections[axis.Axis] {
			t.Errorf("the closure axis %q has no lockfile section of that name; sections seen: %v", axis.Axis, sections)
		}
	}
}
