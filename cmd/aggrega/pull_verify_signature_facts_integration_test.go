// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

// `aggrega pull --verify` answers whether running `aggrega pull` now would change
// the committed files, and the signature facts are part of what a pull writes: the
// `signature` on each lockfile row, and on each entry of the plugin trust record.
//
// A Component and its Recipe, and a linter plugin, are published signed to one
// in-process registry and pulled under a signing requirement, so every row and the
// plugin's entry record their verified fact and every bundle is cached. Each case
// then changes that state in one place — a fact removed, copied, edited, a cached
// bundle removed, a cache slot removed, the requirement, the posture, or what the
// registry answers — and runs a verify, and then a plain pull from the same
// starting state. The registry counts the signature lookups it is asked for, so a
// case the verify must answer without asking is held to that as well.
//
// The property every connected case is held to is the one the verify exists for,
// in both directions: the verify fails exactly where the pull changes a committed
// file, and passes exactly where the pull leaves both byte-identical. One case
// departs from it, and is named: a plugin the record holds no entry for, where
// nothing requires a signature, is a first sighting — the pull records its identity,
// and a verify does not hold the record to an identity it has never held.
//
// Under the offline posture a plain pull halts with the offline refusal wherever it
// would ask a registry about a lockfile entry, and over the attached plugin it halts
// before that, naming a .wasm it could not resolve: it resolves the plugin's .wasm
// from the plugin's registry on every run, because no lockfile pin names a cached
// copy of it. So an offline case holds the verify to what it can say without a
// registry: it reads the plugin from the content cache as a build does, reports what
// a pull rewrites whatever the registry answers, names on one line each recorded
// signature only the registry could confirm, and still halts where the plugin's
// identity or its host functions are not the recorded ones.
//
// The verify's own failure is matched by the descriptive code a script matching
// the command's output reads, not through the condition's accessor, so this file
// compiles against the tree before the condition was declared: there, every case
// whose verify must fail is the reproduction, failing because the verify passed.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/mcp"
	"github.com/aggrega-ai/aggrega-cli/internal/pullops"
	"github.com/aggrega-ai/aggrega-cli/internal/serve"
	"github.com/aggrega-ai/aggrega-cli/internal/verdicttest"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// signatureFactsCode is the descriptive code of the condition a verify reports a
// recorded signature fact a pull would rewrite under.
const signatureFactsCode = "recorded-signature-out-of-date"

// signatureFactsCacheDir is the cache directory the consumer's configuration
// leaves at its default.
const signatureFactsCacheDir = ".aggrega/cache"

// signatureFactsFixture is the published registry and the consumer that pulled
// from it, with what that pull wrote.
type signatureFactsFixture struct {
	host   string
	pubPEM []byte
	signer signing.Signer

	ws, releasePath, lockPath, recordPath string
	pluginURI, coordinate                 string

	// pristineLock and pristineRecord are the files the first pull wrote; bundles
	// holds every cached signature bundle by path, so a case that removed one starts
	// the next case from the full cache.
	pristineLock, pristineRecord []byte
	bundles                      map[string][]byte
	// componentSlot is the cache slot the first pull wrote the Component's bytes to,
	// and componentFiles every file in it by its path inside the slot, so a case
	// that removed the slot starts the next case from the full cache.
	componentSlot  string
	componentFiles map[string]string

	component, recipe lockfile.VerifiedSignature
	plugin            lockfile.VerifiedSignature

	// hide has the registry answer every signature lookup with nothing, and
	// lookups counts the signature lookups it is asked.
	hide    *atomic.Bool
	lookups *atomic.Int64
}

// signatureFactsStance is the configuration one case runs under.
type signatureFactsStance struct {
	// required sets `signing.required` at the top level.
	required bool
	// offline sets the offline posture.
	offline bool
	// linter attaches the signed linter plugin, whose identity the record holds.
	linter bool
	// linterURI, where set, is the plugin attached at the linter socket in its place.
	linterURI string
}

// config renders the consumer's configuration for the stance.
func (s signatureFactsStance) config(f *signatureFactsFixture) string {
	var b strings.Builder
	b.WriteString("workspace:\n  catalog_dirs: []\n")
	b.WriteString("default_registry: \"" + f.host + "\"\n")
	b.WriteString("auto_pull: false\n")
	fmt.Fprintf(&b, "offline: %t\n", s.offline)
	if s.required {
		b.WriteString("signing:\n  required: true\n")
	}
	switch {
	case s.linterURI != "":
		b.WriteString("plugins:\n  linter:\n    - uri: " + s.linterURI + "\n")
	case s.linter:
		b.WriteString("plugins:\n  linter:\n    - uri: " + f.pluginURI + "\n")
	}
	b.WriteString("registries:\n  - host: \"" + f.host + "\"\n")
	b.WriteString("    public_key: |\n")
	for _, line := range strings.Split(strings.TrimRight(string(f.pubPEM), "\n"), "\n") {
		b.WriteString("      " + line + "\n")
	}
	return b.String()
}

// newSignatureFactsFixture publishes the signed service and the signed linter
// plugin, and pulls them under a requirement.
func newSignatureFactsFixture(t *testing.T) *signatureFactsFixture {
	t.Helper()
	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	f := &signatureFactsFixture{pubPEM: pubPEM, signer: signer, hide: new(atomic.Bool), lookups: new(atomic.Int64)}
	f.host = startRegistry(t, func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isSignatureLookup(r) {
				f.lookups.Add(1)
				if f.hide.Load() {
					http.NotFound(w, r)
					return
				}
			}
			h.ServeHTTP(w, r)
		})
	})
	publishSignedService(t, f.host, signer)
	f.pluginURI = publishSignedVerdictPlugin(t, f.host, "linter", "signed-gate", "1.0.0", signer)
	f.coordinate = types.PluginCoordinate("linter", "signed-gate")

	f.ws, f.releasePath = writeSignedServiceConsumer(t)
	f.lockPath = lockfile.PathFor(f.releasePath)
	f.recordPath = pluginstrust.PathFor(f.releasePath)
	writeWorkspaceConfig(t, f.ws, signatureFactsStance{required: true, linter: true}.config(f))
	if err := f.pull(t); err != nil {
		t.Fatalf("pull the signed service and plugin under a requirement: %v", err)
	}
	f.pristineLock = readForTest(t, f.lockPath)
	f.pristineRecord = readForTest(t, f.recordPath)

	lf, _, err := lockfile.LoadLockfile(f.lockPath)
	if err != nil {
		t.Fatalf("load the lockfile the pull wrote: %v", err)
	}
	component, recipe := signedServiceRows(t, lf)
	f.component, f.recipe = *component.Signature, *recipe.Signature
	// A reference written without its version is cached under the reference with
	// the version it resolved to appended, and every pinned read addresses that
	// slot, so the slot is named the same way here. The snapshot doubles as the
	// check that it is the slot the pull wrote: a slot named any other way holds
	// nothing, and the fixture stops here rather than letting a case that removes
	// it leave the Component in the cache for the run it means to fetch it.
	f.componentSlot = mustCachePath(f.ws, signatureFactsCacheDir,
		recordedSignatureComponent+":"+component.ResolvedVersion, component.ResolvedVersion)
	f.componentFiles = snapshotDir(t, f.componentSlot)
	if len(f.componentFiles) == 0 {
		t.Fatalf("the pull cached nothing for the Component at %s, so no case below can remove what it fetched", f.componentSlot)
	}
	record, _, err := pluginstrust.LoadOrStart(f.recordPath)
	if err != nil {
		t.Fatalf("load the record the pull wrote: %v", err)
	}
	entry, held := record.Find(f.coordinate)
	if !held || entry.Signature == nil || entry.Signature.BundleDigest == "" {
		t.Fatalf("the pull recorded no signature for the plugin %q, so no case below starts from a recorded fact:\n%s",
			f.coordinate, f.pristineRecord)
	}
	f.plugin = *entry.Signature
	f.bundles = map[string][]byte{}
	for _, fact := range []lockfile.VerifiedSignature{f.component, f.recipe, f.plugin} {
		path := oci.SignatureBundlePath(f.ws, signatureFactsCacheDir, fact.BundleDigest)
		f.bundles[path] = readForTest(t, path)
	}
	return f
}

// pull runs a plain pull over the workspace.
func (f *signatureFactsFixture) pull(t *testing.T) error {
	t.Helper()
	var out, diag bytes.Buffer
	err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: f.ws}),
		ReleasePath: f.releasePath, Out: &out, DiagOut: &diag})
	if err != nil {
		t.Logf("pull diagnostics:\n%s", diag.String())
	}
	return err
}

// verify runs a verifying pull over the workspace and returns its diagnostics with
// its error.
func (f *signatureFactsFixture) verify(t *testing.T) (string, error) {
	t.Helper()
	var out, diag bytes.Buffer
	err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: f.ws}),
		ReleasePath: f.releasePath, Verify: true, Out: &out, DiagOut: &diag})
	return diag.String(), err
}

// restore puts the pristine files, every cached bundle and the Component's cache
// slot back, and lets the registry answer again.
func (f *signatureFactsFixture) restore(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(f.lockPath, f.pristineLock, 0o644); err != nil {
		t.Fatalf("restore the lockfile: %v", err)
	}
	if err := os.WriteFile(f.recordPath, f.pristineRecord, 0o644); err != nil {
		t.Fatalf("restore the record: %v", err)
	}
	for path, b := range f.bundles {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("restore the bundle directory: %v", err)
		}
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatalf("restore the cached bundle %s: %v", path, err)
		}
	}
	if err := os.RemoveAll(f.componentSlot); err != nil {
		t.Fatalf("clear the Component's cache slot: %v", err)
	}
	for rel, b := range f.componentFiles {
		path := filepath.Join(f.componentSlot, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("restore the Component's cache slot directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(b), 0o644); err != nil {
			t.Fatalf("restore the Component's cached file %s: %v", path, err)
		}
	}
	f.hide.Store(false)
}

// editComponent rewrites the lockfile with the Component's row changed by edit.
func (f *signatureFactsFixture) editComponent(t *testing.T, edit func(*lockfile.Entity)) {
	t.Helper()
	lf, _, err := lockfile.LoadLockfile(f.lockPath)
	if err != nil {
		t.Fatalf("load the lockfile: %v", err)
	}
	for i := range lf.Entities {
		if lf.Entities[i].URI == recordedSignatureComponent {
			edit(&lf.Entities[i])
		}
	}
	if err := lockfile.WriteLockfile(f.lockPath, lf); err != nil {
		t.Fatalf("write the edited lockfile: %v", err)
	}
}

// editPlugin rewrites the record with the plugin's entry changed by edit.
func (f *signatureFactsFixture) editPlugin(t *testing.T, edit func(*pluginstrust.TrustedPlugin)) {
	t.Helper()
	record, _, err := pluginstrust.LoadOrStart(f.recordPath)
	if err != nil {
		t.Fatalf("load the record: %v", err)
	}
	for i := range record.Plugins {
		if record.Plugins[i].Plugin == f.coordinate {
			edit(&record.Plugins[i])
		}
	}
	if err := pluginstrust.Write(f.recordPath, record); err != nil {
		t.Fatalf("write the edited record: %v", err)
	}
}

// removeRecord deletes the record.
func (f *signatureFactsFixture) removeRecord(t *testing.T) {
	t.Helper()
	if err := os.Remove(f.recordPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("delete the record: %v", err)
	}
}

// removeBundle deletes the cached signature bundle fact names. A bundle already
// gone is the state asked for: an offline verify does not cache it again.
func (f *signatureFactsFixture) removeBundle(t *testing.T, fact lockfile.VerifiedSignature) {
	t.Helper()
	if err := os.Remove(oci.SignatureBundlePath(f.ws, signatureFactsCacheDir, fact.BundleDigest)); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		t.Fatalf("delete the cached bundle: %v", err)
	}
}

// removeComponentSlot deletes the Component's cache slot, the one the first pull
// wrote, so a run fetches it.
func (f *signatureFactsFixture) removeComponentSlot(t *testing.T) {
	t.Helper()
	if err := os.RemoveAll(f.componentSlot); err != nil {
		t.Fatalf("delete the Component's cache slot: %v", err)
	}
}

// readOptional returns a file's bytes, or nil where it does not exist.
func readOptional(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// outOfDateOccurrences returns every occurrence of the condition err carries: the
// elements of the first join along its chain that name it, or err itself where its
// chain holds no join and names it.
func outOfDateOccurrences(err error) []error {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if joined, ok := e.(interface{ Unwrap() []error }); ok {
			var out []error
			for _, part := range joined.Unwrap() {
				out = append(out, outOfDateOccurrences(part)...)
			}
			return out
		}
	}
	if code, ok := cerrors.CodeOf(err); ok && code == signatureFactsCode {
		return []error{err}
	}
	return nil
}

// signatureFactsReport is one difference a case requires the verify to report:
// the reference or plugin, and the file recording the fact.
type signatureFactsReport struct {
	subject string
	file    string
}

// TestIntegration_AVerifyReportsEveryRecordedSignatureAPullWouldRewrite is the
// matrix: each store, by fact agreeing, missing or differing, by requirement on or
// off, over a cache-served and a fetched row, and under the offline posture.
func TestIntegration_AVerifyReportsEveryRecordedSignatureAPullWouldRewrite(t *testing.T) {
	f := newSignatureFactsFixture(t)
	component := signatureFactsReport{recordedSignatureComponent, f.lockPath}
	plugin := signatureFactsReport{f.coordinate, f.recordPath}

	stripComponent := func(t *testing.T) { f.editComponent(t, func(e *lockfile.Entity) { e.Signature = nil }) }
	copyRecipeOntoComponent := func(t *testing.T) {
		recipe := f.recipe
		f.editComponent(t, func(e *lockfile.Entity) { e.Signature = &recipe })
	}
	editComponentSigner := func(t *testing.T) {
		f.editComponent(t, func(e *lockfile.Entity) { e.Signature.Identity = "keyed:sha256:edited-by-hand" })
	}
	stripPlugin := func(t *testing.T) { f.editPlugin(t, func(p *pluginstrust.TrustedPlugin) { p.Signature = nil }) }
	// The fixture's record holds one plugin, so the signature copied onto its entry is
	// another artifact's from the lockfile: its bundle is in the cache and intact, and
	// it was made for the Component's manifest.
	copyComponentOntoPlugin := func(t *testing.T) {
		component := f.component
		f.editPlugin(t, func(p *pluginstrust.TrustedPlugin) { p.Signature = &component })
	}
	editPluginSigner := func(t *testing.T) {
		f.editPlugin(t, func(p *pluginstrust.TrustedPlugin) { p.Signature.Identity = "keyed:sha256:edited-by-hand" })
	}
	editPluginDigest := func(t *testing.T) {
		f.editPlugin(t, func(p *pluginstrust.TrustedPlugin) { p.Digest = "sha256:" + strings.Repeat("0", 64) })
	}

	type lookupsWant int
	const (
		anyLookups lookupsWant = iota
		noLookups
		someLookups
	)
	rows := []struct {
		name   string
		stance signatureFactsStance
		// setup changes the pristine state in one place. It runs before the verify
		// and again before the pull, so the pull starts where the verify started: a
		// verify may fill the content cache, and a pull run after it would otherwise
		// start from a different cache.
		setup func(t *testing.T)
		// hide has the registry answer every signature lookup with nothing.
		hide bool
		// reports is what the verify must report; empty, it must pass.
		reports []signatureFactsReport
		// lookups is what the verify may ask the registry.
		lookups lookupsWant
		// firstSighting marks the one connected case the verify passes while the pull
		// writes: a plugin the record holds no entry for, where nothing requires a
		// signature.
		firstSighting bool
		// notChecked is every reference or plugin the verify's lines saying a recorded
		// signature was not checked must name; empty, no such line may be printed.
		notChecked []string
		// verifyHalt and pullHalt, where set, are the conditions the verify and the
		// plain pull must halt on instead.
		verifyHalt, pullHalt error
	}{
		// The lockfile, with no plugin attached and no record, so every lookup the
		// registry counts is the lockfile's.
		{name: "lockfile: as the pull wrote it, required", stance: signatureFactsStance{required: true},
			setup: f.removeRecord, lookups: noLookups},
		{name: "lockfile: as the pull wrote it, not required", stance: signatureFactsStance{},
			setup: f.removeRecord, lookups: noLookups},
		{name: "lockfile: the Component's signature removed, required", stance: signatureFactsStance{required: true},
			setup:   func(t *testing.T) { f.removeRecord(t); stripComponent(t) },
			reports: []signatureFactsReport{component}, lookups: noLookups},
		{name: "lockfile: the Component's signature removed, not required", stance: signatureFactsStance{},
			setup: func(t *testing.T) { f.removeRecord(t); stripComponent(t) }, lookups: noLookups},
		{name: "lockfile: the Recipe's signature copied onto the Component, required", stance: signatureFactsStance{required: true},
			setup:   func(t *testing.T) { f.removeRecord(t); copyRecipeOntoComponent(t) },
			reports: []signatureFactsReport{component}, lookups: noLookups},
		{name: "lockfile: the Recipe's signature copied onto the Component, not required", stance: signatureFactsStance{},
			setup:   func(t *testing.T) { f.removeRecord(t); copyRecipeOntoComponent(t) },
			reports: []signatureFactsReport{component}, lookups: noLookups},
		{name: "lockfile: the Component's cached bundle removed, required", stance: signatureFactsStance{required: true},
			setup:   func(t *testing.T) { f.removeRecord(t); f.removeBundle(t, f.component) },
			lookups: someLookups},
		{name: "lockfile: the Component's signer edited, its bundle intact, required", stance: signatureFactsStance{required: true},
			setup: func(t *testing.T) { f.removeRecord(t); editComponentSigner(t) }, lookups: noLookups},
		{name: "lockfile: the Component fetched again, required", stance: signatureFactsStance{required: true},
			setup: func(t *testing.T) { f.removeRecord(t); f.removeComponentSlot(t) }, lookups: someLookups},
		{name: "lockfile: the Component fetched again, its signer edited, required", stance: signatureFactsStance{required: true},
			setup:   func(t *testing.T) { f.removeRecord(t); f.removeComponentSlot(t); editComponentSigner(t) },
			reports: []signatureFactsReport{component}, lookups: someLookups},
		{name: "lockfile: the Component fetched again from a registry now attaching no signature, not required",
			stance: signatureFactsStance{}, hide: true,
			setup:   func(t *testing.T) { f.removeRecord(t); f.removeComponentSlot(t) },
			reports: []signatureFactsReport{component}, lookups: someLookups},
		{name: "lockfile, offline: the Component's signature removed, required", stance: signatureFactsStance{required: true, offline: true},
			setup:   func(t *testing.T) { f.removeRecord(t); stripComponent(t) },
			reports: []signatureFactsReport{component}, lookups: noLookups, pullHalt: egress.ErrOfflineEgress()},
		{name: "lockfile, offline: the Recipe's signature copied onto the Component, required", stance: signatureFactsStance{required: true, offline: true},
			setup:   func(t *testing.T) { f.removeRecord(t); copyRecipeOntoComponent(t) },
			reports: []signatureFactsReport{component}, lookups: noLookups, pullHalt: egress.ErrOfflineEgress()},
		{name: "lockfile, offline: the Component's cached bundle removed, required", stance: signatureFactsStance{required: true, offline: true},
			setup:   func(t *testing.T) { f.removeRecord(t); f.removeBundle(t, f.component) },
			lookups: noLookups, notChecked: []string{recordedSignatureComponent}, pullHalt: egress.ErrOfflineEgress()},
		{name: "lockfile, offline: as the pull wrote it, required", stance: signatureFactsStance{required: true, offline: true},
			setup: f.removeRecord, lookups: noLookups},

		// The record, with the plugin attached; a pull verifies its signature at the
		// registry on every run, and so does a verify.
		{name: "record: as the pull wrote it, required", stance: signatureFactsStance{required: true, linter: true}},
		{name: "record: as the pull wrote it, not required", stance: signatureFactsStance{linter: true}},
		{name: "record: the plugin's signature removed, required", stance: signatureFactsStance{required: true, linter: true},
			setup: stripPlugin, reports: []signatureFactsReport{plugin}},
		{name: "record: the plugin's signature removed, not required", stance: signatureFactsStance{linter: true},
			setup: stripPlugin, reports: []signatureFactsReport{plugin}},
		{name: "record: the plugin's signer edited, required", stance: signatureFactsStance{required: true, linter: true},
			setup: editPluginSigner, reports: []signatureFactsReport{plugin}},
		{name: "record: the Component's signature copied onto the plugin's entry, required", stance: signatureFactsStance{required: true, linter: true},
			setup: copyComponentOntoPlugin, reports: []signatureFactsReport{plugin}, lookups: someLookups},
		{name: "record: deleted, required", stance: signatureFactsStance{required: true, linter: true},
			setup: f.removeRecord, reports: []signatureFactsReport{plugin}},
		{name: "record: deleted, not required, which is a first sighting", stance: signatureFactsStance{linter: true},
			setup: f.removeRecord, firstSighting: true},
		{name: "record: the plugin's cached bundle removed, required", stance: signatureFactsStance{required: true, linter: true},
			setup: func(t *testing.T) { f.removeBundle(t, f.plugin) }},
		{name: "record: a registry now attaching no signature, not required", stance: signatureFactsStance{linter: true},
			hide: true, reports: []signatureFactsReport{plugin}},
		// Offline, a pull halts at the plugin's .wasm, which it resolves from the
		// plugin's registry on every run, naming a .wasm it could not resolve; a verify
		// asks no registry about the plugin and reads it from the content cache as a
		// build does. It still holds the plugin's identity to the record, reports an
		// entry a pull rewrites whatever the registry answers — the build refuses it
		// for recording no signature, or for recording one made for another artifact —
		// and names every other entry on a line saying its signature was not checked,
		// passing over it: the signer edited by hand is one a connected verify reports.
		{name: "record, offline: as the pull wrote it, required", stance: signatureFactsStance{required: true, linter: true, offline: true},
			lookups: noLookups, notChecked: []string{f.coordinate}, pullHalt: closure.ErrUnresolvableURI()},
		{name: "record, offline: the plugin's signer edited, required", stance: signatureFactsStance{required: true, linter: true, offline: true},
			setup: editPluginSigner, lookups: noLookups, notChecked: []string{f.coordinate}, pullHalt: closure.ErrUnresolvableURI()},
		{name: "record, offline: the plugin's signature removed, required", stance: signatureFactsStance{required: true, linter: true, offline: true},
			setup: stripPlugin, reports: []signatureFactsReport{plugin}, lookups: noLookups, pullHalt: closure.ErrUnresolvableURI()},
		{name: "record, offline: the plugin's signature removed, not required", stance: signatureFactsStance{linter: true, offline: true},
			setup: stripPlugin, lookups: noLookups, notChecked: []string{f.coordinate}, pullHalt: closure.ErrUnresolvableURI()},
		{name: "record, offline: the Component's signature copied onto the plugin's entry, required", stance: signatureFactsStance{required: true, linter: true, offline: true},
			setup: copyComponentOntoPlugin, reports: []signatureFactsReport{plugin}, lookups: noLookups, pullHalt: closure.ErrUnresolvableURI()},
		{name: "record, offline: the Component's signature copied onto the plugin's entry, not required", stance: signatureFactsStance{linter: true, offline: true},
			setup: copyComponentOntoPlugin, reports: []signatureFactsReport{plugin}, lookups: noLookups, pullHalt: closure.ErrUnresolvableURI()},
		{name: "record, offline: deleted, required", stance: signatureFactsStance{required: true, linter: true, offline: true},
			setup: f.removeRecord, reports: []signatureFactsReport{plugin}, lookups: noLookups, pullHalt: closure.ErrUnresolvableURI()},
		{name: "record, offline: deleted, not required, which is a first sighting", stance: signatureFactsStance{linter: true, offline: true},
			setup: f.removeRecord, lookups: noLookups, pullHalt: closure.ErrUnresolvableURI()},
		{name: "record, offline: the plugin's recorded digest edited, required", stance: signatureFactsStance{required: true, linter: true, offline: true},
			setup: editPluginDigest, lookups: noLookups, verifyHalt: pullops.ErrVerdictPluginIdentityChanged(), pullHalt: closure.ErrUnresolvableURI()},
		{name: "both, offline: the Component's and the plugin's signatures removed, required", stance: signatureFactsStance{required: true, linter: true, offline: true},
			setup:   func(t *testing.T) { stripComponent(t); stripPlugin(t) },
			reports: []signatureFactsReport{component, plugin}, lookups: noLookups, pullHalt: closure.ErrUnresolvableURI()},

		// Both files at once: every difference is reported in one run.
		{name: "both: the Component's and the plugin's signatures removed, required", stance: signatureFactsStance{required: true, linter: true},
			setup:   func(t *testing.T) { stripComponent(t); stripPlugin(t) },
			reports: []signatureFactsReport{component, plugin}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			f.restore(t)
			t.Cleanup(func() { f.restore(t) })
			writeWorkspaceConfig(t, f.ws, row.stance.config(f))
			arrange := func(t *testing.T) {
				if row.setup != nil {
					row.setup(t)
				}
				f.hide.Store(row.hide)
				f.lookups.Store(0)
			}

			arrange(t)
			lockBefore, recordBefore := readOptional(t, f.lockPath), readOptional(t, f.recordPath)
			diag, verr := f.verify(t)
			lookups := f.lookups.Load()
			if !bytes.Equal(readOptional(t, f.lockPath), lockBefore) {
				t.Errorf("the verify wrote the lockfile")
			}
			if !bytes.Equal(readOptional(t, f.recordPath), recordBefore) {
				t.Errorf("the verify wrote the plugin trust record")
			}

			switch {
			case row.verifyHalt != nil:
				if !errors.Is(verr, row.verifyHalt) {
					t.Fatalf("the verify must halt with %v; got %v", row.verifyHalt, verr)
				}
			case len(row.reports) == 0:
				if verr != nil {
					t.Errorf("the verify must pass: %v\n%s", verr, diag)
				}
			default:
				// Reported and carried on, so the pull below still runs and a verify that
				// passed here is shown beside the pull that rewrites the file.
				if verr == nil {
					t.Errorf("the verify passed over a recorded signature a pull would rewrite; it must report %v\n%s", row.reports, diag)
					break
				}
				occurrences := outOfDateOccurrences(verr)
				if len(occurrences) != len(row.reports) {
					t.Errorf("the verify reported %d recorded signature(s) out of date; want %d:\n%v", len(occurrences), len(row.reports), verr)
				}
				for _, want := range row.reports {
					found := false
					for _, o := range occurrences {
						text := o.Error()
						if strings.Contains(text, `"`+want.subject+`"`) && strings.Contains(text, want.file) {
							found = true
							if remedy := "aggrega pull --release " + f.releasePath; !strings.Contains(text, remedy) {
								t.Errorf("the occurrence for %q does not name the remedy %q:\n%s", want.subject, remedy, text)
							}
						}
					}
					if !found {
						t.Errorf("no occurrence names %q and the file %s:\n%v", want.subject, want.file, verr)
					}
				}
			}
			switch row.lookups {
			case noLookups:
				if lookups != 0 {
					t.Errorf("the verify made %d signature lookup(s) where it must answer without asking", lookups)
				}
			case someLookups:
				if lookups == 0 {
					t.Errorf("the verify asked the registry nothing where only the registry's answer settles the row")
				}
			}
			const notChecked = "was not checked at its registry"
			var lines []string
			for _, line := range strings.Split(diag, "\n") {
				if strings.Contains(line, notChecked) {
					lines = append(lines, line)
				}
			}
			if len(row.notChecked) == 0 && len(lines) > 0 {
				t.Errorf("the verify printed a line saying a recorded signature was not checked where it must not:\n%s", diag)
			}
			for _, subject := range row.notChecked {
				named := false
				for _, line := range lines {
					named = named || strings.Contains(line, `"`+subject+`"`)
				}
				if !named {
					t.Errorf("no line saying a recorded signature was not checked names %q; diagnostics:\n%s", subject, diag)
				}
			}

			// The plain pull, from the same starting state.
			arrange(t)
			lockBefore, recordBefore = readOptional(t, f.lockPath), readOptional(t, f.recordPath)
			perr := f.pull(t)
			if row.pullHalt != nil {
				if !errors.Is(perr, row.pullHalt) {
					t.Fatalf("the pull must halt with %v; got %v", row.pullHalt, perr)
				}
				return
			}
			if perr != nil {
				t.Fatalf("the pull must complete: %v", perr)
			}
			rewrote := !bytes.Equal(readOptional(t, f.lockPath), lockBefore) || !bytes.Equal(readOptional(t, f.recordPath), recordBefore)
			if row.firstSighting {
				if verr != nil || !rewrote {
					t.Errorf("a first sighting: the verify must pass (got %v) while the pull records the plugin (rewrote=%t)", verr, rewrote)
				}
				return
			}
			if failed := verr != nil; failed != rewrote {
				t.Errorf("the verify failed=%t while the pull from the same state rewrote a committed file=%t; the two must agree", failed, rewrote)
			}
		})
	}
}

// TestIntegration_AnOfflineVerifyStillHoldsAVerdictPluginToItsHostFunctions keeps
// the host-function check where an offline verify asks no registry about a verdict
// plugin. A plugin whose entity stamps a host function its .wasm does not import is
// published signed; a connected pull caches it and halts on the stamp, writing
// nothing. Offline, the verify reads the plugin from the content cache and halts on
// the same stamp rather than passing over a plugin it asked no registry about, while
// a plain offline pull halts before it, at the plugin's .wasm, which it resolves from
// the plugin's registry.
func TestIntegration_AnOfflineVerifyStillHoldsAVerdictPluginToItsHostFunctions(t *testing.T) {
	f := newSignatureFactsFixture(t)
	entity := verdicttest.PluginEntityYAML("linter", "mis-stamped-gate", "1.0.0") +
		"  runtime:\n    hostFunctions:\n      - aggrega_log\n"
	misStamped := publishSignedVerdictPluginEntity(t, f.host, "linter", "mis-stamped-gate", "1.0.0", entity, f.signer)

	writeWorkspaceConfig(t, f.ws, signatureFactsStance{required: true, linterURI: misStamped}.config(f))
	if err := f.pull(t); !errors.Is(err, wasm.ErrWasmHostFunctionsStampMismatch()) {
		t.Fatalf("precondition: a connected pull must cache the plugin and halt on its stamp; got %v", err)
	}

	writeWorkspaceConfig(t, f.ws, signatureFactsStance{required: true, offline: true, linterURI: misStamped}.config(f))
	f.lookups.Store(0)
	lockBefore, recordBefore := readOptional(t, f.lockPath), readOptional(t, f.recordPath)
	diag, verr := f.verify(t)
	if errors.Is(verr, egress.ErrOfflineEgress()) {
		t.Fatalf("the offline verify asked the plugin's registry and was refused:\n%v\n%s", verr, diag)
	}
	if !errors.Is(verr, wasm.ErrWasmHostFunctionsStampMismatch()) {
		t.Fatalf("the offline verify must halt on the stamp the cached .wasm disagrees with; got %v\n%s", verr, diag)
	}
	if lookups := f.lookups.Load(); lookups != 0 {
		t.Errorf("the offline verify made %d signature lookup(s)", lookups)
	}
	if !bytes.Equal(readOptional(t, f.lockPath), lockBefore) || !bytes.Equal(readOptional(t, f.recordPath), recordBefore) {
		t.Errorf("the offline verify wrote a committed file")
	}

	if err := f.pull(t); !errors.Is(err, closure.ErrUnresolvableURI()) {
		t.Errorf("the plain offline pull must still halt at the plugin's .wasm, which it resolves from its registry; got %v", err)
	}
}

// TestIntegration_EverySurfaceThatVerifiesReportsARecordedSignatureAPullWouldRewrite
// runs one verify on the daemon's dependency resolution and on the assistant
// tool's pull, over the Component's and the plugin's signatures both removed under
// a requirement, and requires each to fail naming both, their files and the remedy.
func TestIntegration_EverySurfaceThatVerifiesReportsARecordedSignatureAPullWouldRewrite(t *testing.T) {
	f := newSignatureFactsFixture(t)
	writeWorkspaceConfig(t, f.ws, signatureFactsStance{required: true, linter: true}.config(f))
	setup := func(t *testing.T) {
		f.restore(t)
		f.editComponent(t, func(e *lockfile.Entity) { e.Signature = nil })
		f.editPlugin(t, func(p *pluginstrust.TrustedPlugin) { p.Signature = nil })
	}
	requireBoth := func(t *testing.T, surface, text string) {
		t.Helper()
		for _, want := range []string{`"` + recordedSignatureComponent + `"`, `"` + f.coordinate + `"`,
			filepath.Base(f.lockPath), pluginstrust.FileName, "aggrega pull --release "} {
			if !strings.Contains(text, want) {
				t.Errorf("%s's failure does not carry %q:\n%s", surface, want, text)
			}
		}
	}
	unchanged := func(t *testing.T, surface string, lockBefore, recordBefore []byte) {
		t.Helper()
		if !bytes.Equal(readOptional(t, f.lockPath), lockBefore) || !bytes.Equal(readOptional(t, f.recordPath), recordBefore) {
			t.Errorf("%s's verify wrote a committed file", surface)
		}
	}

	t.Run("the daemon's dependency resolution", func(t *testing.T) {
		setup(t)
		lockBefore, recordBefore := readOptional(t, f.lockPath), readOptional(t, f.recordPath)
		body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "aggrega/resolveDependencies",
			"params": map[string]any{"workspaceRoot": f.ws, "release": filepath.Base(f.releasePath), "verify": true}})
		if err != nil {
			t.Fatalf("marshal the request: %v", err)
		}
		var stdin, stdout, stderr bytes.Buffer
		fmt.Fprintf(&stdin, "Content-Length: %d\r\n\r\n", len(body))
		stdin.Write(body)
		if rerr := serve.Run(context.Background(), serve.RunConfig{Stdin: &stdin, Stdout: &stdout, Stderr: &stderr,
			WorkspaceRoot: f.ws, CompilerVersion: provenanceCompilerVersion(), Toolchain: toolchainStamp()}); rerr != nil {
			t.Fatalf("the daemon exited with an error: %v\nstderr: %s", rerr, stderr.String())
		}
		frame := daemonFrameAnswering(t, stdout.Bytes(), 1)
		raw, refused := frame["error"]
		if !refused {
			t.Fatalf("the daemon's verify answered with a result over two recorded signatures a pull would rewrite: %s", frame["result"])
		}
		var refusal struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(raw, &refusal); err != nil {
			t.Fatalf("decode the refusal: %v\n%s", err, raw)
		}
		requireBoth(t, "the daemon", refusal.Message)
		unchanged(t, "the daemon", lockBefore, recordBefore)
	})

	t.Run("the assistant tool's pull", func(t *testing.T) {
		setup(t)
		lockBefore, recordBefore := readOptional(t, f.lockPath), readOptional(t, f.recordPath)
		body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "pull", "arguments": map[string]any{
				"release": f.releasePath, "workspace_root": f.ws, "verify": true}}})
		if err != nil {
			t.Fatalf("marshal the call: %v", err)
		}
		var stdin, stdout, stderr bytes.Buffer
		stdin.Write(body)
		stdin.WriteByte('\n')
		if rerr := mcp.Run(context.Background(), mcp.Config{Stdin: &stdin, Stdout: &stdout, Stderr: &stderr,
			ServerVersion: provenanceCompilerVersion(), Toolchain: toolchainStamp(), WorkspaceRoot: f.ws}); rerr != nil {
			t.Fatalf("the tool session exited with an error: %v\nstderr: %s", rerr, stderr.String())
		}
		var reply *struct {
			Result *struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
				IsError bool `json:"isError"`
			} `json:"result"`
			Error json.RawMessage `json:"error"`
		}
		for _, line := range bytes.Split(stdout.Bytes(), []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			var message struct {
				ID json.RawMessage `json:"id"`
			}
			if err := json.Unmarshal(line, &message); err != nil {
				t.Fatalf("decode a message of the tool session: %v\n%s", err, line)
			}
			if string(bytes.TrimSpace(message.ID)) != "1" {
				continue
			}
			if err := json.Unmarshal(line, &reply); err != nil {
				t.Fatalf("decode the answer: %v\n%s", err, line)
			}
			break
		}
		if reply == nil || reply.Result == nil {
			t.Fatalf("the tool session wrote no result for the pull call\nstdout: %s\nstderr: %s", stdout.String(), stderr.String())
		}
		if !reply.Result.IsError {
			t.Fatalf("the tool's verify succeeded over two recorded signatures a pull would rewrite: %s", stdout.String())
		}
		var text strings.Builder
		for _, c := range reply.Result.Content {
			text.WriteString(c.Text)
		}
		requireBoth(t, "the tool", text.String())
		unchanged(t, "the tool", lockBefore, recordBefore)
	})
}
