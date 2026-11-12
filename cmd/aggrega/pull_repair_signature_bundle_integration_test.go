// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build integration

package main

// A re-pull over a signature bundle removed or altered in the content cache after
// the pull that recorded its fact. The artifacts are published signed to a real
// in-process registry and pulled under a signing requirement, so each row records
// its verified fact and the bundle that fact names is cached where a build
// re-hashes it. Each case then damages that one cached bundle — removes it,
// rewrites it, or rewrites it and clears the artifact's own cache slot so the pull
// fetches the artifact afresh — pulls again and builds. The registry counts the
// signature lookups it is asked for, so a repair is held to having asked as well as
// to what it left on disk.

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// bundleDamage is one way a cached signature bundle stops matching the fact its
// row records.
type bundleDamage struct {
	name string
	// remove deletes the bundle; alter rewrites it with other bytes.
	remove, alter bool
	// refetch also clears the cache slots holding the artifact the row pins, so the
	// pull fetches it from the registry rather than reading it from the cache.
	refetch bool
}

// bundleDamages is every damage a case is run under.
var bundleDamages = []bundleDamage{
	{name: "the cached bundle removed", remove: true},
	{name: "the cached bundle altered in place", alter: true},
	{name: "the cached bundle altered and the artifact fetched afresh", alter: true, refetch: true},
}

// apply damages the bundle at bundlePath, and clears slots where the case fetches
// the artifact afresh.
func (d bundleDamage) apply(t *testing.T, bundlePath string, slots []string) {
	t.Helper()
	if d.remove {
		if err := os.Remove(bundlePath); err != nil {
			t.Fatalf("remove the cached bundle %s: %v", bundlePath, err)
		}
	}
	if d.alter {
		if err := os.WriteFile(bundlePath, []byte("rewritten after the pull"), 0o644); err != nil {
			t.Fatalf("alter the cached bundle %s: %v", bundlePath, err)
		}
	}
	if d.refetch {
		if len(slots) == 0 {
			t.Fatal("no cache slot holds the artifact, so clearing it would fetch nothing afresh")
		}
		for _, slot := range slots {
			if err := os.RemoveAll(slot); err != nil {
				t.Fatalf("clear the cache slot %s: %v", slot, err)
			}
		}
	}
}

// countSignatureLookups wraps a registry handler so every signature lookup it is
// asked for adds one to n.
func countSignatureLookups(n *atomic.Int64) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isSignatureLookup(r) {
				n.Add(1)
			}
			h.ServeHTTP(w, r)
		})
	}
}

// cacheSlotsHolding returns every slot of the workspace's entity cache holding a
// manifest whose digest is manifestDigest or content whose digest is
// contentDigest, sorted. The slots are found by what they hold rather than by
// spelling their paths, so the case clears exactly the slots a pull reads the
// artifact from.
func cacheSlotsHolding(t *testing.T, ws, manifestDigest, contentDigest string) []string {
	t.Helper()
	root := oci.EntitiesRoot(ws, ".aggrega/cache")
	held := map[string]struct{}{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if name != "manifest" && name != "blob" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		got := digest.ContentDigest(data)
		if (name == "manifest" && manifestDigest != "" && got == manifestDigest) ||
			(name == "blob" && contentDigest != "" && got == contentDigest) {
			held[filepath.Dir(path)] = struct{}{}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the entity cache %s: %v", root, err)
	}
	slots := make([]string, 0, len(held))
	for slot := range held {
		slots = append(slots, slot)
	}
	sort.Strings(slots)
	return slots
}

// assertBundleRepaired holds a completed re-pull to the repair: it asked the
// registry, the bundle the row records passes the build's re-hash again, and the
// lockfile it wrote records what the first pull recorded.
func assertBundleRepaired(t *testing.T, ws, bundleDigest string, asked int64, lockPath string, pristine []byte, diag string) {
	t.Helper()
	if asked == 0 {
		t.Errorf("the re-pull asked the registry nothing, so it did not verify the damaged row again:\n%s", diag)
	}
	if _, err := oci.VerifyCachedSignatureBundle(ws, ".aggrega/cache", bundleDigest); err != nil {
		t.Errorf("after the re-pull the cached bundle the row records is still one the build refuses: %v", err)
	}
	after, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read the lockfile after the re-pull: %v", err)
	}
	if !bytes.Equal(unstamped(t, after), unstamped(t, pristine)) {
		t.Errorf("the re-pull did not write back the lockfile the first pull wrote.\nwant:\n%s\ngot:\n%s", pristine, after)
	}
}

// restoreFile writes body to path, creating its directory.
func restoreFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("restore %s: %v", path, err)
	}
}

// TestIntegration_ARePullRepairsAComponentsDamagedSignatureBundle is the entity
// row: a Component and its Recipe pulled under a top-level signing requirement,
// then the Component's cached bundle damaged.
func TestIntegration_ARePullRepairsAComponentsDamagedSignatureBundle(t *testing.T) {
	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	var lookups atomic.Int64
	host := startRegistry(t, countSignatureLookups(&lookups))
	publishSignedService(t, host, signer)

	ws, releasePath := writeSignedServiceConsumer(t)
	lockPath := lockfile.PathFor(releasePath)
	writeWorkspaceConfig(t, ws, recordedSignatureStance{topRequired: true}.config(host, pubPEM))
	pull := func(t *testing.T) (string, error) {
		t.Helper()
		var out, diag bytes.Buffer
		err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}),
			ReleasePath: releasePath, Out: &out, DiagOut: &diag})
		return diag.String(), err
	}
	if diag, err := pull(t); err != nil {
		t.Fatalf("pull the signed service under a requirement: %v\n%s", err, diag)
	}
	pristine, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read the lockfile the pull wrote: %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(lockPath)
	if err != nil {
		t.Fatalf("load the lockfile the pull wrote: %v", err)
	}
	var row *lockfile.Entity
	for i := range lf.Entities {
		if lf.Entities[i].URI == recordedSignatureComponent {
			row = &lf.Entities[i]
		}
	}
	if row == nil || row.Signature == nil || row.Signature.BundleDigest == "" {
		t.Fatalf("the pull recorded no signature bundle for %q, so no case below starts from a recorded fact:\n%s",
			recordedSignatureComponent, pristine)
	}
	bundlePath := oci.SignatureBundlePath(ws, ".aggrega/cache", row.Signature.BundleDigest)
	intact, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("read the cached bundle the pull wrote: %v", err)
	}

	for _, d := range bundleDamages {
		t.Run(d.name, func(t *testing.T) {
			restoreFile(t, lockPath, pristine)
			restoreFile(t, bundlePath, intact)
			d.apply(t, bundlePath, cacheSlotsHolding(t, ws, row.ManifestDigest, row.Digest))

			lookups.Store(0)
			diag, err := pull(t)
			if err != nil {
				t.Fatalf("the re-pull must complete: %v\n%s", err, diag)
			}
			assertBundleRepaired(t, ws, row.Signature.BundleDigest, lookups.Load(), lockPath, pristine, diag)

			var out, bdiag bytes.Buffer
			if err := dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), releasePath, nil, false, &bdiag, &out); err != nil {
				t.Fatalf("the build after the re-pull must go ahead (refused as a cached bundle that does not match its recorded digest: %t): %v",
					errors.Is(err, oci.ErrSignatureBundleDigestMismatch()), err)
			}
		})
	}
}

// TestIntegration_ARePullRepairsAPluginsDamagedSignatureBundle is the plugin row:
// a label-transform plugin, whose bytes shape the rendered output and are therefore
// pinned in the lockfile, pulled under a signing requirement for plugins, then its
// cached bundle damaged.
func TestIntegration_ARePullRepairsAPluginsDamagedSignatureBundle(t *testing.T) {
	// Resolve fixtures BEFORE any chdir (fixture resolution walks up from cwd).
	mutatorPath := wasmtest.EnsureFixtureBuilt(t, "mutator-proof-go.wasm")
	std := standardCatalogDir(t)

	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	const mutatorSocket = "governance/tag-injector"
	pluginsBlock := "plugins:\n  governance:\n    - uri: " + mutatorSocket + ":1.0.0\n      role: label-transform\n"

	var lookups atomic.Int64
	host := startRegistry(t, countSignatureLookups(&lookups))
	configSocketFixtureCatalog(t, host)
	publishSocketPluginEntitySigned(t, host, mutatorSocket, "1.0.0", mutatorPath, "governance", "mutate", signer)
	ws := writeVerifyIfPresentConfigSocketWorkspace(t, host, pubPEM, pluginsBlock)
	// The fixture's Component and Recipe are published unsigned, so the requirement
	// covers plugins alone; with no fill, the pulls below are the only runs that can
	// write the lockfile.
	cfgPath := filepath.Join(ws, ".aggrega-config.yaml")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read the workspace config: %v", err)
	}
	requiring := strings.Replace(string(cfg), "auto_pull: true\n", "auto_pull: false\n", 1) +
		"signing:\n  required: true\n  kinds: [Plugin]\n"
	if err := os.WriteFile(cfgPath, []byte(requiring), 0o644); err != nil {
		t.Fatalf("write the requiring config: %v", err)
	}
	t.Chdir(ws)

	pull := func(t *testing.T) (string, error) {
		t.Helper()
		var out, diag bytes.Buffer
		err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}),
			ReleasePath: "release.yaml", CatalogDirs: []string{std}, Out: &out, DiagOut: &diag})
		return diag.String(), err
	}
	if diag, err := pull(t); err != nil {
		t.Fatalf("pull the signed plugin under a requirement: %v\n%s", err, diag)
	}
	lockPath := filepath.Join(ws, lockfile.PathFor("release.yaml"))
	pristine, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read the lockfile the pull wrote: %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(lockPath)
	if err != nil {
		t.Fatalf("load the lockfile the pull wrote: %v", err)
	}
	row := findLockfilePluginRow(t, lf, mutatorSocket)
	if row.Signature == nil || row.Signature.BundleDigest == "" {
		t.Fatalf("the pull recorded no signature bundle for the plugin row, so no case below starts from a recorded fact: %+v", row)
	}
	bundleDigest, manifestDigest, contentDigest := row.Signature.BundleDigest, row.ManifestDigest, row.Digest
	bundlePath := oci.SignatureBundlePath(ws, ".aggrega/cache", bundleDigest)
	intact, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("read the cached bundle the pull wrote: %v", err)
	}

	for _, d := range bundleDamages {
		t.Run(d.name, func(t *testing.T) {
			restoreFile(t, lockPath, pristine)
			restoreFile(t, bundlePath, intact)
			d.apply(t, bundlePath, cacheSlotsHolding(t, ws, manifestDigest, contentDigest))

			lookups.Store(0)
			diag, err := pull(t)
			if err != nil {
				t.Fatalf("the re-pull must complete: %v\n%s", err, diag)
			}
			assertBundleRepaired(t, ws, bundleDigest, lookups.Load(), lockPath, pristine, diag)

			var out, bdiag bytes.Buffer
			if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", []string{std}, false, &bdiag, &out); err != nil {
				t.Fatalf("the build after the re-pull must go ahead (refused as a cached bundle that does not match its recorded digest: %t): %v",
					errors.Is(err, oci.ErrSignatureBundleDigestMismatch()), err)
			}
		})
	}
}

// recordedSignatureRecipe is the Recipe the signed-service fixture's Manifest binds,
// whose row records a fact of its own beside the Component's.
const recordedSignatureRecipe = "helm/svc:1.0.0"

// signedServiceFixture is the entity rows' fixture pulled once under a top-level
// requirement: the workspace, its Release and lockfile, the bytes that pull wrote,
// the registry and its key, the registry's signature-lookup counter, and the
// Component's and the Recipe's recorded facts with their cached bundles as that
// pull left them.
type signedServiceFixture struct {
	ws, releasePath, lockPath, host string
	pubPEM, pristine                []byte
	lookups                         *atomic.Int64
	component, recipe               lockfile.VerifiedSignature
	componentBundle, recipeBundle   string
	intactComponent, intactRecipe   []byte
}

// pullSignedService publishes the signed service, pulls it under a top-level
// requirement and returns the fixture.
func pullSignedService(t *testing.T) signedServiceFixture {
	t.Helper()
	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	f := signedServiceFixture{pubPEM: pubPEM, lookups: &atomic.Int64{}}
	f.host = startRegistry(t, countSignatureLookups(f.lookups))
	publishSignedService(t, f.host, signer)
	f.ws, f.releasePath = writeSignedServiceConsumer(t)
	f.lockPath = lockfile.PathFor(f.releasePath)
	writeWorkspaceConfig(t, f.ws, recordedSignatureStance{topRequired: true}.config(f.host, pubPEM))
	if diag, err := f.pull(t); err != nil {
		t.Fatalf("pull the signed service under a requirement: %v\n%s", err, diag)
	}
	if f.pristine, err = os.ReadFile(f.lockPath); err != nil {
		t.Fatalf("read the lockfile the pull wrote: %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(f.lockPath)
	if err != nil {
		t.Fatalf("load the lockfile the pull wrote: %v", err)
	}
	for _, e := range lf.Entities {
		if e.Signature == nil || e.Signature.BundleDigest == "" {
			continue
		}
		switch e.URI {
		case recordedSignatureComponent:
			f.component = *e.Signature
		case recordedSignatureRecipe:
			f.recipe = *e.Signature
		}
	}
	if f.component.BundleDigest == "" || f.recipe.BundleDigest == "" || f.component.BundleDigest == f.recipe.BundleDigest {
		t.Fatalf("the pull did not record a signature bundle of its own for both %q and %q, so no case below starts "+
			"from two recorded facts:\n%s", recordedSignatureComponent, recordedSignatureRecipe, f.pristine)
	}
	f.componentBundle = oci.SignatureBundlePath(f.ws, ".aggrega/cache", f.component.BundleDigest)
	f.recipeBundle = oci.SignatureBundlePath(f.ws, ".aggrega/cache", f.recipe.BundleDigest)
	if f.intactComponent, err = os.ReadFile(f.componentBundle); err != nil {
		t.Fatalf("read the Component's cached bundle: %v", err)
	}
	if f.intactRecipe, err = os.ReadFile(f.recipeBundle); err != nil {
		t.Fatalf("read the Recipe's cached bundle: %v", err)
	}
	return f
}

// pull runs a pull over the fixture's workspace and returns its diagnostics.
func (f signedServiceFixture) pull(t *testing.T) (string, error) {
	t.Helper()
	var out, diag bytes.Buffer
	err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: f.ws}),
		ReleasePath: f.releasePath, Out: &out, DiagOut: &diag})
	return diag.String(), err
}

// build runs a build over the fixture's workspace — one that fills its lockfile
// where the configuration asks for it — and returns its diagnostics.
func (f signedServiceFixture) build(t *testing.T) (string, error) {
	t.Helper()
	var out, diag bytes.Buffer
	err := dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: f.ws}), f.releasePath, nil, false, &diag, &out)
	return diag.String(), err
}

// restore puts back the lockfile and both cached bundles exactly as the fixture's
// pull left them.
func (f signedServiceFixture) restore(t *testing.T) {
	t.Helper()
	restoreFile(t, f.lockPath, f.pristine)
	restoreFile(t, f.componentBundle, f.intactComponent)
	restoreFile(t, f.recipeBundle, f.intactRecipe)
}

// readLock returns the fixture's lockfile as it is on disk.
func (f signedServiceFixture) readLock(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(f.lockPath)
	if err != nil {
		t.Fatalf("read the lockfile: %v", err)
	}
	return b
}

// assertRepaired holds a completed run to the repair: it asked the registry, the
// lockfile records what the fixture's pull recorded, and the Component's cached
// bundle is the one that fact names again.
func (f signedServiceFixture) assertRepaired(t *testing.T, diag string) {
	t.Helper()
	if f.lookups.Load() == 0 {
		t.Errorf("the run asked the registry nothing, so it did not verify the damaged row again:\n%s", diag)
	}
	if !bytes.Equal(unstamped(t, f.readLock(t)), unstamped(t, f.pristine)) {
		t.Errorf("the run did not write back the lockfile the first pull wrote.\nwant:\n%s\ngot:\n%s", f.pristine, f.readLock(t))
	}
	if _, err := oci.VerifyCachedSignatureBundle(f.ws, ".aggrega/cache", f.component.BundleDigest); err != nil {
		t.Errorf("after the run the Component's cached bundle is still one the build refuses: %v", err)
	}
}

// recordedFactDamage is one way the Component's recorded fact comes to be one the
// build refuses, with the condition it refuses it with.
type recordedFactDamage struct {
	name  string
	apply func(t *testing.T, f signedServiceFixture)
	cond  error
}

var recordedFactDamages = []recordedFactDamage{
	{name: "the Component's cached bundle removed", cond: oci.ErrSignatureBundleDigestMismatch(),
		apply: func(t *testing.T, f signedServiceFixture) {
			if err := os.Remove(f.componentBundle); err != nil {
				t.Fatalf("remove the cached bundle: %v", err)
			}
		}},
	{name: "the Component's cached bundle altered", cond: oci.ErrSignatureBundleDigestMismatch(),
		apply: func(t *testing.T, f signedServiceFixture) {
			if err := os.WriteFile(f.componentBundle, []byte("rewritten after the pull"), 0o644); err != nil {
				t.Fatalf("alter the cached bundle: %v", err)
			}
		}},
	{name: "the Recipe's signature copied onto the Component's row", cond: oci.ErrSignatureForAnotherArtifact(),
		apply: func(t *testing.T, f signedServiceFixture) {
			lf, _, err := lockfile.LoadLockfile(f.lockPath)
			if err != nil {
				t.Fatalf("load the lockfile: %v", err)
			}
			for i := range lf.Entities {
				if lf.Entities[i].URI == recordedSignatureComponent {
					copied := f.recipe
					lf.Entities[i].Signature = &copied
				}
			}
			if err := lockfile.WriteLockfile(f.lockPath, lf); err != nil {
				t.Fatalf("write the edited lockfile: %v", err)
			}
		}},
}

// TestIntegration_ARePullOverADamagedRecordedSignatureUnderEveryStance is the matrix
// over the entity row: for each damage, what the build says, what a re-pull and a
// build that fills its lockfile do online and offline, with and without a signing
// requirement; and an intact cache beside them, which costs no registry request.
func TestIntegration_ARePullOverADamagedRecordedSignatureUnderEveryStance(t *testing.T) {
	f := pullSignedService(t)
	type outcome int
	const (
		// repaired: the run completes, asked the registry, and leaves the lockfile
		// and the bundle as the fixture's pull left them.
		repaired outcome = iota
		// untouched: the run completes, asks the registry nothing and leaves the
		// lockfile as it was.
		untouched
		// refused: the run halts with the case's condition and leaves the lockfile
		// as it was.
		refused
		// buildRefused: a build with no fill halts with the case's condition, names
		// the pull that repairs it, and asks the registry nothing.
		buildRefused
	)
	type row struct {
		name   string
		stance recordedSignatureStance
		damage *recordedFactDamage
		build  bool
		want   outcome
		cond   error
	}
	top := recordedSignatureStance{topRequired: true}
	rows := []row{
		{name: "a warm re-pull over an intact cache asks nothing", stance: top, want: untouched},
		{name: "offline, a re-pull over an intact cache passes and asks nothing",
			stance: recordedSignatureStance{topRequired: true, offline: true}, want: untouched},
	}
	for i := range recordedFactDamages {
		d := &recordedFactDamages[i]
		rows = append(rows,
			row{name: d.name + ": the build refuses it, naming the pull", stance: top, damage: d, build: true,
				want: buildRefused, cond: d.cond},
			row{name: d.name + ": a re-pull under the requirement repairs it", stance: top, damage: d, want: repaired},
			row{name: d.name + ": with no requirement a re-pull repairs it", damage: d, want: repaired},
			row{name: d.name + ": offline, a re-pull refuses and writes nothing",
				stance: recordedSignatureStance{topRequired: true, offline: true}, damage: d,
				want: refused, cond: egress.ErrOfflineEgress()},
			row{name: d.name + ": a build that fills its lockfile repairs it and builds",
				stance: recordedSignatureStance{topRequired: true, autoPull: true}, damage: d, build: true, want: repaired},
			row{name: d.name + ": offline, a build that fills its lockfile refuses at the fill",
				stance: recordedSignatureStance{topRequired: true, autoPull: true, offline: true}, damage: d, build: true,
				want: refused, cond: egress.ErrOfflineEgress()},
		)
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			f.restore(t)
			writeWorkspaceConfig(t, f.ws, r.stance.config(f.host, f.pubPEM))
			if r.damage != nil {
				r.damage.apply(t, f)
			}
			before := f.readLock(t)
			f.lookups.Store(0)
			var diag string
			var err error
			if r.build {
				diag, err = f.build(t)
			} else {
				diag, err = f.pull(t)
			}
			switch r.want {
			case repaired:
				if err != nil {
					t.Fatalf("the run must complete: %v\n%s", err, diag)
				}
				f.assertRepaired(t, diag)
				if !r.build {
					if bdiag, err := f.build(t); err != nil {
						t.Fatalf("the build after the repair must go ahead: %v\n%s", err, bdiag)
					}
				}
			case untouched:
				if err != nil {
					t.Fatalf("the run must complete: %v\n%s", err, diag)
				}
				if n := f.lookups.Load(); n != 0 {
					t.Errorf("the run made %d signature lookups over a cache whose every recorded fact the build accepts", n)
				}
				if after := f.readLock(t); !bytes.Equal(after, before) {
					t.Errorf("the run changed the lockfile.\nwant:\n%s\ngot:\n%s", before, after)
				}
			case refused, buildRefused:
				if !errors.Is(err, r.cond) {
					t.Fatalf("want %v, got %v\n%s", r.cond, err, diag)
				}
				if after := f.readLock(t); !bytes.Equal(after, before) {
					t.Errorf("a refused run changed the lockfile.\nwant:\n%s\ngot:\n%s", before, after)
				}
				if r.want == buildRefused {
					remedy := "remediation: run `aggrega pull --release " + f.releasePath + "` to verify the artifact's " +
						"signature at its registry again, record it in " + f.lockPath + " and cache the bundle it names"
					if !strings.Contains(err.Error(), remedy) {
						t.Errorf("the build's refusal does not name the pull that repairs it:\nwant %s\n got %v", remedy, err)
					}
					if n := f.lookups.Load(); n != 0 {
						t.Errorf("a build with no fill made %d signature lookups", n)
					}
				}
			}
		})
	}
}

// TestIntegration_APullHaltsOverADamagedSignatureItHasNoRegistryToAskAbout is the
// row that cannot be asked about: a workspace that pulled with a default registry
// and then names none, with the Component's recorded fact damaged. The pull halts
// with the build's own refusal, saying no registry is configured to ask about the
// row again, asks nothing and leaves the lockfile as it was; with the registry
// configured again the same pull repairs the row.
func TestIntegration_APullHaltsOverADamagedSignatureItHasNoRegistryToAskAbout(t *testing.T) {
	f := pullSignedService(t)
	withRegistry := recordedSignatureStance{topRequired: true}.config(f.host, f.pubPEM)
	noRegistry := strings.Replace(withRegistry, `default_registry: "`+f.host+`"`, `default_registry: ""`, 1)
	if noRegistry == withRegistry {
		t.Fatalf("the configuration names no default registry to take away:\n%s", withRegistry)
	}
	for _, d := range recordedFactDamages {
		t.Run(d.name, func(t *testing.T) {
			f.restore(t)
			writeWorkspaceConfig(t, f.ws, noRegistry)
			d.apply(t, f)
			before := f.readLock(t)
			f.lookups.Store(0)
			diag, err := f.pull(t)
			if !errors.Is(err, d.cond) {
				t.Fatalf("want the build's own refusal (%v), got %v\n%s", d.cond, err, diag)
			}
			want := `"` + recordedSignatureComponent + `" records a signature a build does not accept, and no registry is ` +
				`configured to ask about it again`
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the halt does not say no registry is configured to ask about the row:\nwant %s\n got %v", want, err)
			}
			if n := f.lookups.Load(); n != 0 {
				t.Errorf("the halted pull made %d signature lookups", n)
			}
			if after := f.readLock(t); !bytes.Equal(after, before) {
				t.Errorf("the halted pull changed the lockfile.\nwant:\n%s\ngot:\n%s", before, after)
			}

			writeWorkspaceConfig(t, f.ws, withRegistry)
			f.lookups.Store(0)
			diag, err = f.pull(t)
			if err != nil {
				t.Fatalf("with the registry configured again the pull must complete: %v\n%s", err, diag)
			}
			f.assertRepaired(t, diag)
			if bdiag, err := f.build(t); err != nil {
				t.Fatalf("the build after the repair must go ahead: %v\n%s", err, bdiag)
			}
		})
	}
}
