// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The workspace content cache is addressed as the workspace root joined with the
// configured cache directory. The cache directory carries a default and is never
// empty, so a validation handed no root does not skip the cache — it composes a
// RELATIVE address and reads whatever sits under the process's working directory.
//
// That is what these guards hold, and the working directory is why they are worth
// holding at all: run from the workspace root the two addresses coincide and every
// resolution succeeds, so the fault is invisible to anyone who tests from there.
// The guards therefore run from somewhere else, which is the only place the two
// addresses differ.

const (
	cacheAnchorCoordinate = "governance/cache-anchor-probe"
	cacheAnchorVersion    = "1.0.0"
	// A well-formed content digest the seeded bytes do not hash to. The mismatch is
	// deliberate: it makes the cache read observable without the fixture having to
	// carry a loadable guest.
	cacheAnchorUnmatchedDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
)

// cacheAnchorPluginEntity is the governance Plugin entity the recorded attachment
// names. It exists so the entity-fronted cascade gets PAST its front and reaches
// the content-cache tier, which is the tier under test.
const cacheAnchorPluginEntity = `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: cache-anchor-probe
  version: "1.0.0"
  pluginType: governance
spec:
  abiVersion: v1beta1
  exports:
    - name: mutate
`

// cacheAnchorCachedBytes are the only copy of this plugin's bytes anywhere: they
// are seeded into the workspace content cache and exist in no catalog directory,
// no local path and no embedded set, so the cache tier is the only tier that can
// produce them. They are not a Wasm module and never need to be — the record pins
// a digest they do not match, so a resolution that reaches them halts at the
// integrity check, and that halt is reportable only by a read that found this
// slot.
var cacheAnchorCachedBytes = []byte("cache-anchored-plugin-bytes")

// cacheAnchorRecord names one label-transform attachment and pins its plugin to a
// digest and a resolved version. The version is what addresses the versioned cache
// slot; the digest is what the resolved bytes are held to.
func cacheAnchorRecord() *lockfile.Lockfile {
	return &lockfile.Lockfile{
		Mutators: []lockfile.MutatorAttachment{{
			Plugin: cacheAnchorCoordinate,
			Role:   string(plugintype.RoleLabelTransform),
		}},
		Plugins: []lockfile.Plugin{{
			URI:             cacheAnchorCoordinate,
			ResolvedVersion: cacheAnchorVersion,
			Digest:          cacheAnchorUnmatchedDigest,
		}},
	}
}

// cacheAnchorFixture is a resolved workspace whose sole governance attachment
// resolves from the workspace content cache and from nowhere else.
type cacheAnchorFixture struct {
	root        string
	view        *catalog.LazyLookup
	resolved    *ResolvedClosure
	wsCfg       workspaceconfig.Config
	releasePath string
}

// inputs builds the validation inputs for one branch, varying only the workspace
// root. Everything else is identical between branches, so a difference in outcome
// is attributable to the root alone.
func (f cacheAnchorFixture) inputs(workspaceRoot string) ValidateInputs {
	return ValidateInputs{
		View:            f.view,
		Closure:         f.resolved.Closure,
		Release:         f.resolved.Release,
		ReleasePath:     f.releasePath,
		Manifest:        f.resolved.Manifest,
		AggBytes:        f.resolved.AggBytes,
		Lockfile:        cacheAnchorRecord(),
		AggregatePath:   f.resolved.AggregatePath,
		WorkspaceRoot:   workspaceRoot,
		WorkspaceConfig: f.wsCfg,
		CompilerVersion: "test",
	}
}

// resolveCacheAnchorInputs materialises the workspace, seeds the content cache
// with the attachment's only copy of its bytes, and resolves the closure.
//
// ORDERING IS LOAD-BEARING: every path is resolved HERE, before the caller changes
// the working directory. The shared catalog directory is derived by walking up
// from the working directory, so deriving it afterwards would point it at nothing
// and the test would fail on its own fixture rather than on its subject.
//
// The catalog view is given no cache directory, so the entity axis reads strictly
// from the catalog directories. Only the plugin-artifact axis is under test here,
// and letting the catalog read a cache too would add a second anchored lookup to
// attribute an outcome between.
func resolveCacheAnchorInputs(t *testing.T) cacheAnchorFixture {
	t.Helper()

	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs cwd: %v", err)
	}
	// This test runs from internal/compilerops/; the module root is two dirs up.
	moduleRoot := filepath.Dir(filepath.Dir(cwd))
	catalogDir := filepath.Join(moduleRoot, "internal", "compiler", "testdata", "fullstack-app", "catalog")
	if _, err := os.Stat(catalogDir); err != nil {
		t.Fatalf("shared catalog not found at %s: %v", catalogDir, err)
	}

	root := t.TempDir()
	releasePath := filepath.Join(root, "release.yaml")
	writeCancelFixture(t, filepath.Join(root, "aggregate.yaml"), cancelBackendAggregate)
	writeCancelFixture(t, filepath.Join(root, "manifest.yaml"), cancelInputKindManifest)
	writeCancelFixture(t, releasePath, cancelDefaultRelease)

	overlay := filepath.Join(root, "overlay")
	writeCancelFixture(t, filepath.Join(overlay, "recipes", "api-sourceonly.yaml"), cancelInputKindRecipe)
	writeCancelFixture(t, filepath.Join(overlay, "plugins", "sourced-only-renderer.yaml"), cancelInputKindPlugin)
	writeCancelFixture(t, filepath.Join(overlay, "plugins", "cache-anchor-probe.yaml"), cacheAnchorPluginEntity)

	wsCfg := workspaceconfig.Default()
	// Seeded through the same primitive the pull path writes with, so the slot this
	// reads from is the slot production writes — a hand-built path here could agree
	// with the reader while both disagreed with the writer.
	if err := oci.WriteCached(root, wsCfg.ResolvedArtifactCacheDir(),
		oci.PluginArtifactKind(), cacheAnchorCoordinate, cacheAnchorVersion, cacheAnchorCachedBytes); err != nil {
		t.Fatalf("seed the workspace content cache: %v", err)
	}

	dirs := []string{overlay, catalogDir}
	view := catalog.NewLazyLookup(root, "", dirs, nil).ForRequest(nil)

	resolved, cerr := ResolveClosure(context.Background(), ClosureInputs{
		View:            view,
		ReleasePath:     releasePath,
		WorkspaceRoot:   root,
		WorkspaceConfig: wsCfg,
	})
	if cerr != nil {
		t.Fatalf("ResolveClosure: stage=%d %v", cerr.Stage, cerr)
	}
	return cacheAnchorFixture{root: root, view: view, resolved: resolved, wsCfg: wsCfg, releasePath: releasePath}
}

// TestValidateReadsTheContentCacheAtTheWorkspaceRoot holds both halves of what the
// workspace root does on this path, from a working directory that is not the
// workspace root — the only place the two possible cache addresses differ.
//
// Supplied, the attachment's bytes are found in the workspace cache and halt on the
// digest they are pinned to. Omitted, the same resolution walks the whole cascade
// without finding them: same documents, same machine, same command, different
// answer. The two branches differ in the root and in nothing else, so the outcome
// is attributable to it.
func TestValidateReadsTheContentCacheAtTheWorkspaceRoot(t *testing.T) {
	f := resolveCacheAnchorInputs(t)

	// Everything the fixture needs from the filesystem is already resolved; from
	// here the working directory is part of the subject.
	elsewhere := t.TempDir()
	t.Chdir(elsewhere)

	// The premise the whole test rests on, asserted rather than assumed: run from
	// the workspace root the two addresses would coincide and the omitted branch
	// below would pass for the wrong reason.
	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs cwd: %v", err)
	}
	if cwd == f.root {
		t.Fatalf("the working directory is the workspace root (%s); the two cache addresses coincide there and this test would measure nothing", cwd)
	}

	t.Run("omitted root reads a cache relative to the working directory", func(t *testing.T) {
		vr := Validate(context.Background(), f.inputs(""))
		if vr.RunErr == nil {
			t.Fatalf("the attachment resolved with no workspace root supplied; its bytes live only under %s, so nothing at the working directory could have produced them", f.root)
		}
		if !errors.Is(vr.RunErr, pluginpath.ErrPluginArtifactNotFound()) {
			t.Fatalf("want the cascade to report the plugin's artifact as not found, since the cache address it composed points at the working directory; got: %v", vr.RunErr)
		}
		if !strings.Contains(vr.RunErr.Error(), cacheAnchorCoordinate) {
			t.Errorf("the failure does not name the attachment it was resolving: %v", vr.RunErr)
		}
	})

	t.Run("supplied root reads the workspace cache", func(t *testing.T) {
		vr := Validate(context.Background(), f.inputs(f.root))
		if vr.RunErr == nil {
			t.Fatal("the resolution reported no failure; the seeded bytes do not match the pinned digest, so a read that found them must halt on the pin")
		}
		if !errors.Is(vr.RunErr, oci.ErrFetchIntegrity()) {
			t.Fatalf("want the integrity halt that only a tier which PRODUCED bytes can raise; got: %v", vr.RunErr)
		}
		source := string(pluginpath.TierCache.Source())
		if !strings.Contains(vr.RunErr.Error(), source) {
			t.Errorf("the bytes were produced by some tier other than the workspace content cache (%q): %v", source, vr.RunErr)
		}
	})
}
