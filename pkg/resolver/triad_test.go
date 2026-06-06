// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func writeTriadFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// TestResolveAggregate_ByFile_RelativePrefix — a ./ reference is an explicit file
// (no scan); the result carries the file path and its raw bytes.
func TestResolveAggregate_ByFile_RelativePrefix(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTriadFile(t, dir, "aggregate.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: by-file-app\nspec:\n  components: {}\n")
	r, err := ResolveAggregate(dir, "./aggregate.yaml", nil)
	if err != nil {
		t.Fatalf("by-file resolve: %v", err)
	}
	if r.Aggregate.Metadata.Name != "by-file-app" {
		t.Errorf("name = %q; want by-file-app", r.Aggregate.Metadata.Name)
	}
	if filepath.Base(r.Path) != "aggregate.yaml" {
		t.Errorf("Path = %q; want …/aggregate.yaml", r.Path)
	}
	if len(r.Bytes) == 0 {
		t.Errorf("Bytes empty; want the file content")
	}
}

// TestResolveAggregate_BareIdentity_SameDirScan — the primary path: a bare token
// resolves by metadata.name through the same-directory scan, regardless of the
// filename (the stem-vs-name mismatch is structurally gone). Path and Bytes come
// from the matched file.
func TestResolveAggregate_BareIdentity_SameDirScan(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// filename deliberately != metadata.name (the old stem model would fail here).
	writeTriadFile(t, dir, "app.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: scanned-app\nspec:\n  components: {}\n")
	r, err := ResolveAggregate(dir, "scanned-app", nil)
	if err != nil {
		t.Fatalf("identity resolve: %v", err)
	}
	if r.Aggregate.Metadata.Name != "scanned-app" {
		t.Errorf("name = %q; want scanned-app", r.Aggregate.Metadata.Name)
	}
	if filepath.Base(r.Path) != "app.yaml" || len(r.Bytes) == 0 {
		t.Errorf("Path/Bytes not surfaced from the matched file: path=%q bytes=%d", r.Path, len(r.Bytes))
	}
}

// TestResolveManifest_BareIdentity — a Manifest resolves by the same-directory
// scan and has no catalog tier (it is deployment-side, so the signature takes no
// lookup).
func TestResolveManifest_BareIdentity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTriadFile(t, dir, "manifest.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Manifest\nmetadata:\n  name: scanned-manifest\nspec: {}\n")
	r, err := ResolveManifest(dir, "scanned-manifest")
	if err != nil {
		t.Fatalf("manifest resolve: %v", err)
	}
	if r.Manifest.Metadata.Name != "scanned-manifest" {
		t.Errorf("name = %q; want scanned-manifest", r.Manifest.Metadata.Name)
	}
}

// TestResolveAggregate_SameDirWinsOverCatalog — a same-directory match wins; the
// catalog lookup must not even be consulted for a name the scan resolves.
func TestResolveAggregate_SameDirWinsOverCatalog(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTriadFile(t, dir, "aggregate.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: shared\nspec:\n  components: {}\n")
	catConsulted := false
	catLookup := func(name string) (ResolvedAggregate, bool) {
		catConsulted = true
		return ResolvedAggregate{Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "shared"}}}, true
	}
	r, err := ResolveAggregate(dir, "shared", catLookup)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.Aggregate == nil || r.Aggregate.Metadata.Name != "shared" {
		t.Fatalf("resolved %v; want the same-dir shared", r.Aggregate)
	}
	if catConsulted {
		t.Errorf("catalog lookup consulted despite a same-directory match — same-dir must win")
	}
}

// TestResolveAggregate_CatalogTier — a name absent from the directory falls
// through to the catalog lookup seam, and the resolved result carries the
// catalog's path and bytes (the tier-2 contract, identical to tier-1).
func TestResolveAggregate_CatalogTier(t *testing.T) {
	t.Parallel()
	dir := t.TempDir() // empty — nothing in the same dir
	catBytes := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: cat-app\n")
	catLookup := func(name string) (ResolvedAggregate, bool) {
		if name == "cat-app" {
			return ResolvedAggregate{
				Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "cat-app"}},
				Path:      "catalog/core/cat-app.yaml",
				Bytes:     catBytes,
			}, true
		}
		return ResolvedAggregate{}, false
	}
	r, err := ResolveAggregate(dir, "cat-app", catLookup)
	if err != nil {
		t.Fatalf("catalog-tier resolve: %v", err)
	}
	if r.Aggregate.Metadata.Name != "cat-app" {
		t.Errorf("name = %q; want cat-app", r.Aggregate.Metadata.Name)
	}
	if r.Path != "catalog/core/cat-app.yaml" {
		t.Errorf("catalog-tier Path = %q; want the catalog source path", r.Path)
	}
	if string(r.Bytes) != string(catBytes) {
		t.Errorf("catalog-tier Bytes not surfaced from the catalog")
	}
}

// TestResolveAggregate_TolerantScan_StrayBrokenFileSkipped — a stray broken
// `.yaml` in the directory must NOT fail the resolution of an unrelated, valid
// reference. This is the tolerant-scan risk surface: skip-with-warning, not
// build-fatal.
func TestResolveAggregate_TolerantScan_StrayBrokenFileSkipped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTriadFile(t, dir, "good.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: good-app\nspec:\n  components: {}\n")
	writeTriadFile(t, dir, "broken.yaml", "this: is: not: valid: yaml: : :\n")                                           // envelope fails
	writeTriadFile(t, dir, "unrelated.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: c\n") // valid non-triad
	r, err := ResolveAggregate(dir, "good-app", nil)
	if err != nil {
		t.Fatalf("a stray broken/unrelated file must not fail an unrelated resolve: %v", err)
	}
	if r.Aggregate.Metadata.Name != "good-app" {
		t.Errorf("name = %q; want good-app", r.Aggregate.Metadata.Name)
	}
}

// TestResolveAggregate_Replay_NeededFileBroken_RecoveredName — when the needed
// reference IS the broken file and its name was recovered from the envelope, the
// error replays the skip reason with definitive wording (the match is proven).
func TestResolveAggregate_Replay_NeededFileBroken_RecoveredName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Envelope parses (kind + metadata.name) but the full typed load fails: spec
	// is a scalar where the Aggregate type expects a structured block.
	writeTriadFile(t, dir, "broken.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: needed-app\nspec: \"not a spec block\"\n")
	_, err := ResolveAggregate(dir, "needed-app", nil)
	if !errors.Is(err, ErrTriadRefUnresolvable) {
		t.Fatalf("err = %v; want ErrTriadRefUnresolvable", err)
	}
	if !strings.Contains(err.Error(), "needed Aggregate \"needed-app\" is the unparseable file") {
		t.Errorf("definitive replay wording missing; got %v", err)
	}
}

// TestResolveAggregate_Replay_NeededAbsent_UnrecoverableName_Hedged — when the
// reference is absent and a file's name could not be recovered, the wording is
// hedged ("could not be indexed and may be it"), not a definitive misattribution.
func TestResolveAggregate_Replay_NeededAbsent_UnrecoverableName_Hedged(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTriadFile(t, dir, "broken.yaml", "}{ not yaml at all : : :\n") // envelope unrecoverable
	_, err := ResolveAggregate(dir, "absent-app", nil)
	if !errors.Is(err, ErrTriadRefUnresolvable) {
		t.Fatalf("err = %v; want ErrTriadRefUnresolvable", err)
	}
	if !strings.Contains(err.Error(), "may be it") {
		t.Errorf("hedged replay wording missing; got %v", err)
	}
	if strings.Contains(err.Error(), "is the unparseable file") {
		t.Errorf("hedged branch must not use definitive wording; got %v", err)
	}
}

// TestResolveAggregate_DuplicateIdentityHalts — two cleanly-loaded Aggregates
// sharing a metadata.name in the directory halt loudly, named as a same-directory
// duplicate (distinct from the catalog-tier ErrDuplicateRecipeIdentity).
func TestResolveAggregate_DuplicateIdentityHalts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTriadFile(t, dir, "a.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: dup\nspec:\n  components: {}\n")
	writeTriadFile(t, dir, "b.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: dup\nspec:\n  components: {}\n")
	_, err := ResolveAggregate(dir, "dup", nil)
	if !errors.Is(err, ErrDuplicateTriadIdentity) {
		t.Fatalf("err = %v; want ErrDuplicateTriadIdentity", err)
	}
	if !strings.Contains(err.Error(), "Release directory") {
		t.Errorf("duplicate error should name the Release directory (distinct from the catalog duplicate); got %v", err)
	}
}

// TestResolveAggregate_PlainNotFound — a reference naming nothing, with no
// plausibly-matching skip, gets the plain not-found (no replay).
func TestResolveAggregate_PlainNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTriadFile(t, dir, "aggregate.yaml", "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: present\nspec:\n  components: {}\n")
	_, err := ResolveAggregate(dir, "absent", nil)
	if !errors.Is(err, ErrTriadRefUnresolvable) {
		t.Fatalf("err = %v; want ErrTriadRefUnresolvable", err)
	}
	if strings.Contains(err.Error(), "unparseable") {
		t.Errorf("plain not-found must not mention an unparseable file; got %v", err)
	}
}
