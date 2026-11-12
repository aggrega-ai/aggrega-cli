// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package lockfile_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

func deprecatedLockfile(stamp string) *lockfile.Lockfile {
	return &lockfile.Lockfile{
		Version:     1,
		Release:     "release.yaml",
		GeneratedAt: stamp,
		Entities: []lockfile.Entity{
			{
				Kind: "component", URI: "z-server",
				ResolvedVersion: "1.0.0",
				Digest:          fixtureDigest("zzzz"),
				ManifestDigest:  fixtureDigest("zzzzmanifest"),
				Deprecation: &lockfile.DeprecationRecord{
					Severity: "retracted",
					Message:  "withdrawn; use a-db instead",
					Signer: &lockfile.VerifiedSignature{
						Mode:         "keyless",
						Identity:     "security@cognitio.it",
						Issuer:       "https://accounts.google.com",
						BundleDigest: fixtureDigest("markerbundle"),
					},
				},
			},
			// A non-deprecated entity carries no deprecation record.
			{Kind: "component", URI: "a-db", ResolvedVersion: "2.0.0", Digest: fixtureDigest("aaaa")},
		},
		Plugins: []lockfile.Plugin{
			{
				// A plugin row records its content digest as its resolved
				// version, because a plugin artifact is pinned by its bytes.
				URI:             "z-plugin",
				ResolvedVersion: fixtureDigest("pppp"),
				Digest:          fixtureDigest("pppp"),
				ManifestDigest:  fixtureDigest("ppppmanifest"),
				Deprecation: &lockfile.DeprecationRecord{
					Severity: "error",
					Message:  "plugin no longer maintained",
					Signer: &lockfile.VerifiedSignature{
						Mode:         "keyed",
						BundleDigest: fixtureDigest("keyedmarker"),
					},
				},
			},
		},
	}
}

// TestDeprecationRecordByteIdentity proves two independent WriteLockfile
// invocations over the same in-memory lockfile carrying deprecation records
// produce byte-identical output — the deterministic-output invariant extends to
// the deprecation field and its nested signer.
func TestDeprecationRecordByteIdentity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const stamp = "2026-06-30T00:00:00Z"
	pathA := filepath.Join(dir, "a.lock")
	pathB := filepath.Join(dir, "b.lock")

	if err := lockfile.WriteLockfile(pathA, deprecatedLockfile(stamp)); err != nil {
		t.Fatalf("write A: %v", err)
	}
	if err := lockfile.WriteLockfile(pathB, deprecatedLockfile(stamp)); err != nil {
		t.Fatalf("write B: %v", err)
	}
	a, _ := os.ReadFile(pathA)
	b, _ := os.ReadFile(pathB)
	if !bytes.Equal(a, b) {
		t.Fatalf("lockfile bytes differ across independent writes:\n--- A ---\n%s\n--- B ---\n%s", a, b)
	}
}

// TestDeprecationRecordRoundTrip proves the deprecation record (severity,
// message, and the nested verified signer) survives a write/load round-trip on
// both entity and plugin rows, and that a non-deprecated entity loads with a nil
// deprecation record.
func TestDeprecationRecordRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "rt.lock")
	if err := lockfile.WriteLockfile(path, deprecatedLockfile("2026-06-30T00:00:00Z")); err != nil {
		t.Fatalf("write: %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	byURI := map[string]lockfile.Entity{}
	for _, e := range lf.Entities {
		byURI[e.URI] = e
	}
	rec := byURI["z-server"].Deprecation
	if rec == nil {
		t.Fatal("z-server should carry a deprecation record")
	}
	if rec.Severity != "retracted" || rec.Message != "withdrawn; use a-db instead" {
		t.Errorf("deprecation record round-trip lost data: %+v", rec)
	}
	if rec.Signer == nil || rec.Signer.Mode != "keyless" ||
		rec.Signer.Identity != "security@cognitio.it" ||
		rec.Signer.Issuer != "https://accounts.google.com" ||
		rec.Signer.BundleDigest != fixtureDigest("markerbundle") {
		t.Errorf("marker signer round-trip lost data: %+v", rec.Signer)
	}
	if byURI["a-db"].Deprecation != nil {
		t.Error("a non-deprecated entity should load with a nil deprecation record")
	}

	if len(lf.Plugins) != 1 {
		t.Fatalf("want 1 plugin, got %d", len(lf.Plugins))
	}
	prec := lf.Plugins[0].Deprecation
	if prec == nil || prec.Severity != "error" || prec.Message != "plugin no longer maintained" {
		t.Errorf("plugin deprecation record round-trip lost data: %+v", prec)
	}
	if prec.Signer == nil || prec.Signer.Mode != "keyed" || prec.Signer.BundleDigest != fixtureDigest("keyedmarker") {
		t.Errorf("plugin marker signer round-trip lost data: %+v", prec.Signer)
	}
}
