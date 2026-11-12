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

func signedLockfile(stamp string) *lockfile.Lockfile {
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
				Signature: &lockfile.VerifiedSignature{
					Mode:         "keyless",
					Identity:     "m.ferrero@cognitio.it",
					Issuer:       "https://accounts.google.com",
					BundleDigest: fixtureDigest("bbbb"),
				},
			},
			{
				Kind:            "component",
				URI:             "a-db",
				ResolvedVersion: "2.0.0",
				Digest:          fixtureDigest("aaaa"),
				ManifestDigest:  fixtureDigest("aaaamanifest"),
				Signature: &lockfile.VerifiedSignature{
					Mode:         "keyed",
					BundleDigest: fixtureDigest("cccc"),
				},
			},
			// An unsigned entity carries no signature record.
			{Kind: "component", URI: "m-cache", ResolvedVersion: "3.0.0", Digest: fixtureDigest("dddd")},
		},
	}
}

// TestVerifiedSignatureByteIdentity proves two independent WriteLockfile
// invocations over the same in-memory lockfile carrying verified-signature
// facts produce byte-identical output — the deterministic-output invariant
// extends to the signature field.
func TestVerifiedSignatureByteIdentity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const stamp = "2026-06-22T00:00:00Z"
	pathA := filepath.Join(dir, "a.lock")
	pathB := filepath.Join(dir, "b.lock")

	if err := lockfile.WriteLockfile(pathA, signedLockfile(stamp)); err != nil {
		t.Fatalf("write A: %v", err)
	}
	if err := lockfile.WriteLockfile(pathB, signedLockfile(stamp)); err != nil {
		t.Fatalf("write B: %v", err)
	}
	a, _ := os.ReadFile(pathA)
	b, _ := os.ReadFile(pathB)
	if !bytes.Equal(a, b) {
		t.Fatalf("lockfile bytes differ across independent writes:\n--- A ---\n%s\n--- B ---\n%s", a, b)
	}
}

// TestVerifiedSignatureRoundTrip proves the signature fact survives a
// write/load round-trip with all fields intact, and that an unsigned
// entity loads with a nil signature.
func TestVerifiedSignatureRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "rt.lock")
	if err := lockfile.WriteLockfile(path, signedLockfile("2026-06-22T00:00:00Z")); err != nil {
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

	if byURI["z-server"].ManifestDigest != fixtureDigest("zzzzmanifest") {
		t.Errorf("manifest_digest round-trip lost data: %q", byURI["z-server"].ManifestDigest)
	}
	if byURI["m-cache"].ManifestDigest != "" {
		t.Errorf("unsigned entity should round-trip with an empty manifest_digest, got %q", byURI["m-cache"].ManifestDigest)
	}

	keyless := byURI["z-server"].Signature
	if keyless == nil {
		t.Fatal("z-server should carry a signature")
	}
	if keyless.Mode != "keyless" || keyless.Identity != "m.ferrero@cognitio.it" ||
		keyless.Issuer != "https://accounts.google.com" || keyless.BundleDigest != fixtureDigest("bbbb") {
		t.Errorf("keyless signature round-trip lost data: %+v", keyless)
	}

	keyed := byURI["a-db"].Signature
	if keyed == nil || keyed.Mode != "keyed" || keyed.BundleDigest != fixtureDigest("cccc") {
		t.Errorf("keyed signature round-trip lost data: %+v", keyed)
	}
	if keyed.Identity != "" || keyed.Issuer != "" {
		t.Errorf("keyed signature should carry no identity/issuer: %+v", keyed)
	}

	if byURI["m-cache"].Signature != nil {
		t.Error("unsigned entity should load with a nil signature")
	}
}

// signedPluginLockfile's plugin rows record their content digest as their
// resolved version, because a plugin artifact is pinned by its bytes.
func signedPluginLockfile(stamp string) *lockfile.Lockfile {
	return &lockfile.Lockfile{
		Version:     1,
		Release:     "release.yaml",
		GeneratedAt: stamp,
		Plugins: []lockfile.Plugin{
			{
				URI:             "z-plugin",
				ResolvedVersion: fixtureDigest("zzzz"),
				Digest:          fixtureDigest("zzzz"),
				ManifestDigest:  fixtureDigest("zzzzmanifest"),
				Signature: &lockfile.VerifiedSignature{
					Mode:         "keyless",
					Identity:     "m.ferrero@cognitio.it",
					Issuer:       "https://accounts.google.com",
					BundleDigest: fixtureDigest("bbbb"),
				},
			},
			{
				URI:             "a-plugin",
				ResolvedVersion: fixtureDigest("aaaa"),
				Digest:          fixtureDigest("aaaa"),
				ManifestDigest:  fixtureDigest("aaaamanifest"),
				Signature: &lockfile.VerifiedSignature{
					Mode:         "keyed",
					BundleDigest: fixtureDigest("cccc"),
				},
			},
			// An unsigned plugin carries no signature record.
			{URI: "m-plugin", ResolvedVersion: fixtureDigest("dddd"), Digest: fixtureDigest("dddd")},
		},
	}
}

// TestPluginSignatureByteIdentity proves the deterministic-output
// invariant extends to the Plugin signature field — two independent
// writes of the same in-memory lockfile produce byte-identical output.
func TestPluginSignatureByteIdentity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const stamp = "2026-06-22T00:00:00Z"
	pathA := filepath.Join(dir, "a.lock")
	pathB := filepath.Join(dir, "b.lock")

	if err := lockfile.WriteLockfile(pathA, signedPluginLockfile(stamp)); err != nil {
		t.Fatalf("write A: %v", err)
	}
	if err := lockfile.WriteLockfile(pathB, signedPluginLockfile(stamp)); err != nil {
		t.Fatalf("write B: %v", err)
	}
	a, _ := os.ReadFile(pathA)
	b, _ := os.ReadFile(pathB)
	if !bytes.Equal(a, b) {
		t.Fatalf("lockfile bytes differ across independent writes:\n--- A ---\n%s\n--- B ---\n%s", a, b)
	}
}

// TestPluginSignatureRoundTrip proves the plugin signature fact survives
// a write/load round-trip with all fields intact, and that an unsigned plugin
// loads with a nil signature.
func TestPluginSignatureRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "rt.lock")
	if err := lockfile.WriteLockfile(path, signedPluginLockfile("2026-06-22T00:00:00Z")); err != nil {
		t.Fatalf("write: %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	byURI := map[string]lockfile.Plugin{}
	for _, p := range lf.Plugins {
		byURI[p.URI] = p
	}

	if byURI["z-plugin"].ManifestDigest != fixtureDigest("zzzzmanifest") {
		t.Errorf("manifest_digest round-trip lost data: %q", byURI["z-plugin"].ManifestDigest)
	}
	if byURI["m-plugin"].ManifestDigest != "" {
		t.Errorf("unsigned plugin should round-trip with an empty manifest_digest, got %q", byURI["m-plugin"].ManifestDigest)
	}

	keyless := byURI["z-plugin"].Signature
	if keyless == nil {
		t.Fatal("z-plugin should carry a signature")
	}
	if keyless.Mode != "keyless" || keyless.Identity != "m.ferrero@cognitio.it" ||
		keyless.Issuer != "https://accounts.google.com" || keyless.BundleDigest != fixtureDigest("bbbb") {
		t.Errorf("keyless signature round-trip lost data: %+v", keyless)
	}

	keyed := byURI["a-plugin"].Signature
	if keyed == nil || keyed.Mode != "keyed" || keyed.BundleDigest != fixtureDigest("cccc") {
		t.Errorf("keyed signature round-trip lost data: %+v", keyed)
	}

	if byURI["m-plugin"].Signature != nil {
		t.Error("unsigned plugin should load with a nil signature")
	}
}

// TestCanonicaliseDoesNotAliasPluginSignature proves WriteLockfile's
// canonicalise deep-copies the plugin signature pointer so it does not alias
// the caller's value.
func TestCanonicaliseDoesNotAliasPluginSignature(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lf := signedPluginLockfile("2026-06-22T00:00:00Z")
	orig := lf.Plugins[0].Signature
	if err := lockfile.WriteLockfile(filepath.Join(dir, "c.lock"), lf); err != nil {
		t.Fatalf("write: %v", err)
	}
	if lf.Plugins[0].Signature != orig {
		t.Error("WriteLockfile mutated the caller's plugin signature pointer")
	}
}

// TestCanonicaliseDoesNotAliasSignature proves WriteLockfile's
// canonicalise deep-copies the signature pointer so it does not alias the
// caller's value (the maintenance hazard the canonicaliser guards against).
func TestCanonicaliseDoesNotAliasSignature(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lf := signedLockfile("2026-06-22T00:00:00Z")
	orig := lf.Entities[0].Signature
	if err := lockfile.WriteLockfile(filepath.Join(dir, "c.lock"), lf); err != nil {
		t.Fatalf("write: %v", err)
	}
	// The caller's signature pointer is unchanged after the write.
	if lf.Entities[0].Signature != orig {
		t.Error("WriteLockfile mutated the caller's signature pointer")
	}
}
