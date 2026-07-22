// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// keyedTestSigner builds an offline keyed signer for the unit deprecate flows so
// the marker's own signature is produced without a network or identity provider.
func keyedTestSigner(t *testing.T) signing.Signer {
	t.Helper()
	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	return signer
}

// fetchLoneMarker returns the single decoded deprecation marker attached to tag
// on store, failing the test if the count is not exactly one.
func fetchLoneMarker(t *testing.T, store *memory.Store, tag string) deprecation.Marker {
	t.Helper()
	ctx := context.Background()
	markers, err := oci.ListReferrers(ctx, store, tag, deprecation.ArtifactType)
	if err != nil {
		t.Fatalf("ListReferrers(%s): %v", tag, err)
	}
	if len(markers) != 1 {
		t.Fatalf("got %d markers on %s, want 1", len(markers), tag)
	}
	payload, err := oci.FetchReferrerPayload(ctx, store, markers[0])
	if err != nil {
		t.Fatalf("FetchReferrerPayload: %v", err)
	}
	m, err := deprecation.Unmarshal(payload)
	if err != nil {
		t.Fatalf("Unmarshal marker: %v", err)
	}
	return m
}

// TestDeprecateSingle_FlagsEnrichAndStamp proves a flag-driven single-shot
// deprecate attaches an enriched marker: reason and replacement survive, the
// message back-fills from reason when unset, and issuedOn is stamped from the
// injected clock.
func TestDeprecateSingle_FlagsEnrichAndStamp(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	if _, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, []byte("subject"), nil); err != nil {
		t.Fatalf("Push subject: %v", err)
	}
	frozen := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)

	var out bytes.Buffer
	err := deprecateRun(deprecateConfig{
		Reference:   "reg.test/component/core/legacy:1.0.0",
		Severity:    "warning",
		Reason:      "superseded by v2",
		Replacement: "core/api-server:2",
		Target:      store,
		SubjectRef:  "1.0.0",
		Signer:      keyedTestSigner(t),
		Now:         func() time.Time { return frozen },
		Out:         &out,
		IsTTY:       func() bool { return false },
	})
	if err != nil {
		t.Fatalf("deprecateRun: %v", err)
	}

	m := fetchLoneMarker(t, store, "1.0.0")
	if m.Severity != "warning" || m.Reason != "superseded by v2" || m.Replacement != "core/api-server:2" {
		t.Errorf("marker = %+v, want warning/superseded/replacement", m)
	}
	if m.Message != "superseded by v2" {
		t.Errorf("message should back-fill from reason, got %q", m.Message)
	}
	if m.IssuedOn != "2026-05-06T07:08:09Z" {
		t.Errorf("issuedOn = %q, want the frozen clock value", m.IssuedOn)
	}
}

// TestDeprecateSingle_Interactive proves that with the required content flags
// absent at a terminal, the prompt fills severity, reason, and replacement from
// stdin, and a blank replacement line means none.
func TestDeprecateSingle_Interactive(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	if _, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, []byte("subject"), nil); err != nil {
		t.Fatalf("Push subject: %v", err)
	}

	var out bytes.Buffer
	err := deprecateRun(deprecateConfig{
		Reference:  "reg.test/component/core/legacy:1.0.0",
		Target:     store,
		SubjectRef: "1.0.0",
		Signer:     keyedTestSigner(t),
		Now:        func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Out:        &out,
		IsTTY:      func() bool { return true },
		In:         strings.NewReader("retracted\nwithdrawn for a defect\n\n"),
	})
	if err != nil {
		t.Fatalf("deprecateRun: %v", err)
	}

	m := fetchLoneMarker(t, store, "1.0.0")
	if m.Severity != "retracted" || m.Reason != "withdrawn for a defect" {
		t.Errorf("interactive marker = %+v, want retracted/withdrawn", m)
	}
	if m.Replacement != "" {
		t.Errorf("blank replacement line should mean none, got %q", m.Replacement)
	}
	if !strings.Contains(out.String(), "Severity") || !strings.Contains(out.String(), "Reason") {
		t.Errorf("prompts should be written to out, got:\n%s", out.String())
	}
}

// TestDeprecateSingle_NonInteractiveMissingContentFails proves the command
// refuses when the required content is absent away from a terminal, rather than
// blocking on a prompt no one can answer.
func TestDeprecateSingle_NonInteractiveMissingContentFails(t *testing.T) {
	err := deprecateRun(deprecateConfig{
		Reference: "reg.test/component/core/legacy:1.0.0",
		Target:    memory.New(),
		Signer:    keyedTestSigner(t),
		IsTTY:     func() bool { return false },
		Out:       &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("missing severity/reason on a non-interactive stdin should fail")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("error should name the missing required content, got %v", err)
	}
}

// TestDeprecateBatch_AttachesPerEntry proves a --from batch file attaches one
// enriched marker per entry against the same target, each carrying its own
// content and a stamped issuedOn.
func TestDeprecateBatch_AttachesPerEntry(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	for _, tag := range []string{"1.0.0", "2.0.0"} {
		if _, err := oci.Push(ctx, store, tag, oci.MediaTypeComponent, []byte("subject "+tag), nil); err != nil {
			t.Fatalf("Push %s: %v", tag, err)
		}
	}

	batch := `- reference: reg.test/component/core/legacy:1.0.0
  severity: retracted
  reason: withdrawn
  replacement: core/api-server:2
- reference: reg.test/component/core/old:2.0.0
  severity: warning
  reason: superseded
`
	dir := t.TempDir()
	path := filepath.Join(dir, "batch.yaml")
	if err := os.WriteFile(path, []byte(batch), 0o644); err != nil {
		t.Fatalf("write batch: %v", err)
	}

	var out bytes.Buffer
	err := deprecateRun(deprecateConfig{
		From:   path,
		Target: store,
		Signer: keyedTestSigner(t),
		Now:    func() time.Time { return time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC) },
		Out:    &out,
		IsTTY:  func() bool { return false },
	})
	if err != nil {
		t.Fatalf("deprecateRun batch: %v", err)
	}

	m1 := fetchLoneMarker(t, store, "1.0.0")
	if m1.Severity != "retracted" || m1.Reason != "withdrawn" || m1.Replacement != "core/api-server:2" {
		t.Errorf("entry 1 marker = %+v", m1)
	}
	if m1.IssuedOn != "2026-02-03T04:05:06Z" {
		t.Errorf("entry 1 issuedOn = %q, want stamped", m1.IssuedOn)
	}
	m2 := fetchLoneMarker(t, store, "2.0.0")
	if m2.Severity != "warning" || m2.Reason != "superseded" || m2.Replacement != "" {
		t.Errorf("entry 2 marker = %+v", m2)
	}
}

// tagListingStore adds tag enumeration to an in-memory store so the query-time
// probe — which lists a repository's tags before probing each — runs without a
// network. memory.Store alone does not implement the tag-listing capability.
type tagListingStore struct {
	*memory.Store
	tags []string
}

func (s *tagListingStore) Tags(_ context.Context, _ string, fn func([]string) error) error {
	return fn(s.tags)
}

// TestLatestDeprecationView_NewestWins proves the index-served advisory selects
// the recorded marker with the newest issuedOn across a repository's versions.
func TestLatestDeprecationView_NewestWins(t *testing.T) {
	deps := []VersionDeprecation{
		{Version: "1.0.0", Marker: deprecation.Marker{Severity: "warning", Reason: "older", IssuedOn: "2026-01-01T00:00:00Z"}},
		{Version: "2.0.0", Marker: deprecation.Marker{Severity: "warning", Reason: "newer", IssuedOn: "2026-06-01T00:00:00Z"}},
	}
	v, ok := latestDeprecationView(deps)
	if !ok {
		t.Fatal("view should find a marker")
	}
	if v.Reason != "newer" || v.Tag != "2.0.0" {
		t.Errorf("view = %+v, want the newer marker on 2.0.0", v)
	}
}

// TestLookupComponentDeprecation_IndexServedOffline proves the deprecation
// advisory is served from the local index and issues no network call: an offline
// workspace with a built index carrying a per-version marker surfaces the
// advisory (where the old live probe returned blank offline).
func TestLookupComponentDeprecation_IndexServedOffline(t *testing.T) {
	const host = "hub.aggrega.ai"
	const componentName = "core/compute/api-server"
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, workspaceconfig.ConfigFilename)
	if err := os.WriteFile(cfgPath, []byte("workspace: {}\ndefault_registry: "+host+"\noffline: true\n"), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	rw, err := workspaceconfig.Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}

	repoPath := catalog.OCILocation("Component", "", componentName)
	writeIndexFixture(t, rw.Root(), rw.Config().ResolvedArtifactCacheDir(), host, RegistryIndex{
		Host: host,
		Records: []IndexRecord{{
			URI:     "oci://" + host + "/" + repoPath,
			Name:    repoPath,
			Version: "1.0.0",
			Kind:    "Component",
			Deprecations: []VersionDeprecation{{
				Version: "1.0.0",
				Marker:  deprecation.Marker{Severity: "warning", Reason: "old", IssuedOn: "2026-01-01T00:00:00Z"},
			}},
		}},
	}, "2026-01-01T00:00:00Z\n")

	v, ok := lookupComponentDeprecation(rw, componentName)
	if !ok {
		t.Fatalf("offline workspace with a built index should surface the recorded advisory")
	}
	if v.Severity != "warning" || v.Tag != "1.0.0" || v.Reason != "old" {
		t.Errorf("view = %+v; want the recorded 1.0.0 warning", v)
	}
}

// TestInspect_CatalogComponent_SurfacesDeprecation drives the catalog-name
// inspect path over a temp catalog with the live deprecation lookup stubbed, and
// proves the rendered output carries both the component header and the
// deprecation block.
func TestInspect_CatalogComponent_SurfacesDeprecation(t *testing.T) {
	const componentName = "core/compute/api-server"
	prev := lookupComponentDeprecation
	lookupComponentDeprecation = func(_ *workspaceconfig.ResolvedWorkspace, name string) (deprecationView, bool) {
		if name == componentName {
			return deprecationView{Severity: "retracted", Reason: "withdrawn for a defect", Replacement: "core/compute/api-server:2", IssuedOn: "2026-01-02T03:04:05Z", Tag: "1.0.0"}, true
		}
		return deprecationView{}, false
	}
	t.Cleanup(func() { lookupComponentDeprecation = prev })

	var buf bytes.Buffer
	if err := inspectCatalogEntity(resolveTempWorkspace(t), componentName, []string{fullstackAppInspectCatalog}, testInspectOptions(&buf)); err != nil {
		t.Fatalf("inspect catalog component: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Component: "+componentName) {
		t.Errorf("output missing component header:\n%s", out)
	}
	if !strings.Contains(out, "Deprecation: retracted (version 1.0.0)") || !strings.Contains(out, "Reason: withdrawn for a defect") || !strings.Contains(out, "Replacement: core/compute/api-server:2") {
		t.Errorf("output missing deprecation block:\n%s", out)
	}
}
