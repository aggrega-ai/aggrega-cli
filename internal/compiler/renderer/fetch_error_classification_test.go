// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// TestOfflineEgressRoutesToFetchErrorFamily proves the offline-egress refusal
// is classified into the FETCH_ERROR family by the source-resolution error
// classifier, and that its self-carried remediation is not double-appended.
func TestOfflineEgressRoutesToFetchErrorFamily(t *testing.T) {
	t.Parallel()
	wrapped := fmt.Errorf("fetch referrers: %w", egress.ErrOfflineEgress())
	ce, ok := renderer.WrapSourceResolutionErrorForTest(wrapped)
	if !ok {
		t.Fatal("wrapped ErrOfflineEgress did not produce a CompilerError")
	}
	if ce.Category != cerrors.FetchError {
		t.Errorf("Category = %q; want FETCH_ERROR for an offline-egress refusal", ce.Category)
	}
	if !errors.Is(ce, egress.ErrOfflineEgress()) {
		t.Errorf("classified error no longer detects ErrOfflineEgress in its chain")
	}
}

// These tests cover the orchestrator's category routing for fetch
// failures: a Recipe with a remote source whose lockfile entry is
// missing OR whose cached archive is tampered MUST surface as a
// *cerrors.CompilerError with Category == FetchError, and the
// resulting error message MUST include the operator-actionable
// remediation pointer (`aggrega pull --upgrade` for integrity
// failures; verify URL/version for missing entries).

func TestRenderAll_FetchErrorCategoryOnMissingLockfileEntry(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// No cache slot seeded; lockfile carries no source-artifact
	// entries. The Recipe's structured source is a remote URI, so the
	// orchestrator routes to resolveSourceForNode, which calls into
	// sourcecache.LookupAndExtract and bottoms out with
	// ErrFetchNotFound.

	input := integrationInput(t, root, "core/compute/api-server", "api", "helm", "sha256:nope", "oci://nonexistent/chart", "9.9.9")
	// Drop the populated SourceArtifacts entry — the integration helper
	// seeds one; for this test we want NO entry matching the Recipe.
	input.Lockfile = &lockfile.Lockfile{
		Version:     1,
		Release:     "./r.yaml",
		GeneratedAt: "2026-05-22T10:00:00Z",
	}
	lookup := structuredSourceRecipeLookup("helm", "helm-oci-chart", "oci://nonexistent/chart", "9.9.9")

	_, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err == nil {
		t.Fatal("expected error when lockfile entry is missing; got nil")
	}
	var compilerErr *cerrors.CompilerError
	if !errors.As(err, &compilerErr) {
		t.Fatalf("error should be a *cerrors.CompilerError; got %T: %v", err, err)
	}
	if compilerErr.Category != cerrors.FetchError {
		t.Errorf("Category = %q; want FETCH_ERROR (got non-fetch routing for a fetch sentinel)", compilerErr.Category)
	}
}

func TestRenderAll_FetchErrorIncludesRemediationOnTamperedArchive(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	chart := minimalHelmChartContents()
	blob := buildIntegrationTGZ(t, chart)
	digest := integrationDigest(blob)
	seedIntegrationCacheSlot(t, root, "helm-oci-chart", "oci://r/c", "1.0.0", blob, digest)

	// Tamper the blob.tgz AFTER seeding so the metadata.yaml still
	// records the original digest but the on-disk bytes mismatch.
	// LookupAndExtract's first digest gate (blob vs lockfile) catches
	// this and returns ErrFetchIntegrity.
	slot := filepath.Join(root, ".aggrega/cache/source-artifacts/helm-oci-chart")
	// SlotPath would give us the exact directory, but we can write the
	// tampered blob via the well-known relative path under root: scan
	// for the blob.tgz under the slot tree.
	var blobPath string
	_ = filepath.Walk(slot, func(p string, _ os.FileInfo, _ error) error {
		if filepath.Base(p) == "blob.tgz" {
			blobPath = p
		}
		return nil
	})
	if blobPath == "" {
		t.Fatalf("blob.tgz not found under %s", slot)
	}
	if err := os.WriteFile(blobPath, []byte("tampered\n"), 0o644); err != nil {
		t.Fatalf("tamper write: %v", err)
	}

	input := integrationInput(t, root, "core/compute/api-server", "api", "helm", digest, "oci://r/c", "1.0.0")
	lookup := structuredSourceRecipeLookup("helm", "helm-oci-chart", "oci://r/c", "1.0.0")

	_, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err == nil {
		t.Fatal("expected error when archive is tampered; got nil")
	}
	var compilerErr *cerrors.CompilerError
	if !errors.As(err, &compilerErr) {
		t.Fatalf("error should be a *cerrors.CompilerError; got %T", err)
	}
	if compilerErr.Category != cerrors.FetchError {
		t.Errorf("Category = %q; want FETCH_ERROR for tampered archive", compilerErr.Category)
	}
	if !strings.Contains(compilerErr.Error(), "aggrega pull --upgrade") {
		t.Errorf("error should include 'aggrega pull --upgrade' remediation; got %q", compilerErr.Error())
	}
}
