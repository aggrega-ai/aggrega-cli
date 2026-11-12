// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// The build's check of the signature facts a lockfile records, driven through the
// command line over a cache-served row: the remote-aggregate fixture's
// sub-aggregate, pulled from a catalog directory and then moved into the content
// cache, which is the state a registry fetch leaves — present in the cache, pinned
// in the lockfile, absent from every catalog directory. The fixture records no
// signature for it, and the workspace's default registry is the official hub,
// whose built-in policy requires one, so the row is exactly what the check
// refuses. Every case differs from the refusing one in one setting or one fact.
//
// No registry is involved, so the facts are fabricated where a case needs one: a
// recorded signature is a bundle digest on the row and a blob at the cache slot
// that digest names, which is all a build reads of it.

const recordedSignatureSub = "mysub"

// cacheServedSubAggregate lays the fixture down under the base configuration,
// pulls it, and moves the sub-aggregate into the content cache. It returns the
// root, the Release path and the catalog directory.
func cacheServedSubAggregate(t *testing.T) (root, releasePath, catalogDir string) {
	t.Helper()
	root = t.TempDir()
	releasePath = remoteAggregateFixture(t, root)
	writeWorkspaceConfig(t, root, "auto_pull: false\n")
	catalogDir = filepath.Join(root, "catalog")
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("pull the fixture from its catalog directory: %v", err)
	}
	relocateAggregateToCache(t, root, ".aggrega/cache", recordedSignatureSub)
	return root, releasePath, catalogDir
}

// recordSubAggregateFact writes a verified signature fact onto the sub-aggregate's
// row and caches the bundle it names, returning the bundle's cache path.
func recordSubAggregateFact(t *testing.T, root, releasePath string) string {
	t.Helper()
	bundle := []byte(`{"probe":"a cached signature bundle"}`)
	bundleDigest := digest.ContentDigest(bundle)
	path := oci.SignatureBundlePath(root, ".aggrega/cache", bundleDigest)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir the bundle cache: %v", err)
	}
	if err := os.WriteFile(path, bundle, 0o644); err != nil {
		t.Fatalf("cache the bundle: %v", err)
	}
	lockPath := lockfile.PathFor(releasePath)
	lf, _, err := lockfile.LoadLockfile(lockPath)
	if err != nil {
		t.Fatalf("load the lockfile: %v", err)
	}
	recorded := false
	for i := range lf.Entities {
		if lf.Entities[i].URI == recordedSignatureSub {
			lf.Entities[i].Signature = &lockfile.VerifiedSignature{Mode: "keyed", Identity: "keyed:sha256:probe", BundleDigest: bundleDigest}
			recorded = true
		}
	}
	if !recorded {
		t.Fatalf("the lockfile holds no row for %q to record a fact on: %+v", recordedSignatureSub, lf.Entities)
	}
	if err := lockfile.WriteLockfile(lockPath, lf); err != nil {
		t.Fatalf("write the lockfile: %v", err)
	}
	return path
}

// buildRecordedSignature runs the build the cases assert on, with the fixture's
// configuration replaced by cfg, and returns its error and what it wrote to its
// diagnostic stream.
func buildRecordedSignature(t *testing.T, root, releasePath, catalogDir, cfg string) error {
	t.Helper()
	writeWorkspaceConfig(t, root, cfg)
	var out, diag bytes.Buffer
	return dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath,
		[]string{catalogDir}, false, &diag, &out)
}

// TestTheBuildRefusesACacheServedRowRecordingNoSignatureTheHubRequires is the
// refusal, named as a reader meets it: the condition, the row, the lockfile that
// lacks its record, the setting that requires the signature and the command that
// records it there, under the trust head.
func TestTheBuildRefusesACacheServedRowRecordingNoSignatureTheHubRequires(t *testing.T) {
	t.Parallel()
	root, releasePath, catalogDir := cacheServedSubAggregate(t)

	err := buildRecordedSignature(t, root, releasePath, catalogDir, "auto_pull: false\n")
	if !errors.Is(err, lockfile.ErrSignatureNotRecorded()) {
		t.Fatalf("a cache-served row recording no signature under the hub's built-in requirement must be refused; got %v", err)
	}
	text := err.Error()
	for _, want := range []string{
		lockfile.ErrSignatureNotRecorded().Error(),
		`"` + recordedSignatureSub + `"`,
		lockfile.PathFor(releasePath) + " records no signature for it",
		`the official hub's built-in signature requirement for "hub.aggrega.ai"`,
		"aggrega pull --release " + releasePath,
		"record it in " + lockfile.PathFor(releasePath),
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal does not carry %q:\n%s", want, text)
		}
	}
	if head := formatCompilerError(err); !strings.Contains(head, "TRUST_ERROR E0331") {
		t.Errorf("the printed diagnostic does not open with the trust category and the condition's short code:\n%s", head)
	}
}

// TestTheBuildRefusesTheSameRowOffline holds the refusal to the offline build: the
// check reads only what is on the machine, so being offline changes nothing about
// it.
func TestTheBuildRefusesTheSameRowOffline(t *testing.T) {
	t.Parallel()
	root, releasePath, catalogDir := cacheServedSubAggregate(t)
	err := buildRecordedSignature(t, root, releasePath, catalogDir, "auto_pull: false\noffline: true\n")
	if !errors.Is(err, lockfile.ErrSignatureNotRecorded()) {
		t.Fatalf("the offline build must refuse the row as the online one does; got %v", err)
	}
}

// TestTheSettingsThatAdmitTheRowAtAPullAdmitItAtTheBuild is the other half: every
// setting that lets a pull admit an artifact with no signature lets the build
// admit the row, and the one that does not — a registry's own skip under the
// top-level policy — still refuses.
func TestTheSettingsThatAdmitTheRowAtAPullAdmitItAtTheBuild(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name    string
		cfg     string
		refuses bool
	}{
		{name: "the hub's own entry marked advisory",
			cfg: "auto_pull: false\nregistries:\n  - host: hub.aggrega.ai\n    advisory: true\n    signing:\n      required: true\n"},
		{name: "the hub's own skip", cfg: "auto_pull: false\n" + fakeRegistryTrust},
		{name: "the global skip", cfg: "auto_pull: false\nskip_signature_verification: true\n"},
		{name: "a default registry no policy covers", cfg: "auto_pull: false\ndefault_registry: registry.example.com\n"},
		{name: "the top-level policy defeats the hub's own skip", refuses: true,
			cfg: "auto_pull: false\nsigning:\n  required: true\n" + fakeRegistryTrust},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root, releasePath, catalogDir := cacheServedSubAggregate(t)
			err := buildRecordedSignature(t, root, releasePath, catalogDir, row.cfg)
			if !row.refuses {
				if err != nil {
					t.Fatalf("the build must admit what the pull admits under this setting; got %v", err)
				}
				return
			}
			if !errors.Is(err, lockfile.ErrSignatureNotRecorded()) {
				t.Fatalf("want the not-recorded refusal, got %v", err)
			}
			if !strings.Contains(err.Error(), "`signing.required` at the top level of the workspace configuration") {
				t.Errorf("the refusal does not name the top-level policy that requires the signature: %v", err)
			}
		})
	}
}

// TestACatalogDirectoryRowBuildsUnsignedUnderARequirement is the origin half of
// the rule: a row the build read from a catalog directory is never held to a
// signing requirement, because no registry signature exists for bytes that did not
// come from one — and the top-level policy here requires one for every kind.
func TestACatalogDirectoryRowBuildsUnsignedUnderARequirement(t *testing.T) {
	t.Parallel()
	f := writeRefusalFixture(t)
	writeWorkspaceConfig(t, f.root, "offline: true\nsigning:\n  required: true\n")
	var out, diag bytes.Buffer
	if err := dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: f.root}), f.releasePath,
		[]string{f.catalogDir}, false, &diag, &out); err != nil {
		t.Fatalf("rows read from a catalog directory must build unsigned under a signing requirement: %v", err)
	}
}

// TestARecordedFactAdmitsTheRowAndATamperedBundleRefusesIt holds a row that records
// a fact to its cached bundle: intact, the build goes ahead; rewritten after the
// pull, the build refuses it as a bundle that no longer matches its digest.
func TestARecordedFactAdmitsTheRowAndATamperedBundleRefusesIt(t *testing.T) {
	t.Parallel()
	root, releasePath, catalogDir := cacheServedSubAggregate(t)
	bundlePath := recordSubAggregateFact(t, root, releasePath)

	if err := buildRecordedSignature(t, root, releasePath, catalogDir, "auto_pull: false\n"); err != nil {
		t.Fatalf("a row recording a fact whose bundle is intact must build: %v", err)
	}

	if err := os.WriteFile(bundlePath, []byte("rewritten after the pull"), 0o644); err != nil {
		t.Fatalf("tamper the cached bundle: %v", err)
	}
	err := buildRecordedSignature(t, root, releasePath, catalogDir, "auto_pull: false\n")
	if !errors.Is(err, oci.ErrSignatureBundleDigestMismatch()) {
		t.Fatalf("a tampered bundle must refuse the row as a mismatch; got %v", err)
	}
	if !strings.Contains(err.Error(), `"`+recordedSignatureSub+`"`) {
		t.Errorf("the mismatch does not name the row: %v", err)
	}
}
