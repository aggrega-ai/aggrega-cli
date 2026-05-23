// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package metadata_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/metadata"
)

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.yaml")

	pulled := time.Date(2026, 5, 22, 12, 30, 0, 0, time.UTC)
	want := metadata.CacheMetadata{
		Kind:      "helm-oci-chart",
		URL:       "oci://hub.aggrega.ai/charts/mariadb",
		Version:   "25.0.8",
		Digest:    "sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abcd",
		Mediatype: "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
		PulledAt:  pulled,
	}
	if err := metadata.Write(path, want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := metadata.Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != want {
		t.Errorf("round-trip mismatch:\n  got %+v\n want %+v", got, want)
	}
}

func TestWriteYAMLShape(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.yaml")

	m := metadata.CacheMetadata{
		Kind:      "compose-https-fragment",
		URL:       "https://example.com/fragment.yaml",
		Version:   "1.0.0",
		Digest:    "sha256:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		Mediatype: "application/x-yaml",
		PulledAt:  time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC),
	}
	if err := metadata.Write(path, m); err != nil {
		t.Fatalf("Write: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	text := string(body)
	for _, key := range []string{"kind:", "url:", "version:", "digest:", "mediatype:", "pulled_at:"} {
		if !strings.Contains(text, key) {
			t.Errorf("metadata.yaml missing key %q; got:\n%s", key, text)
		}
	}
}

func TestReadMissingFileWraps(t *testing.T) {
	t.Parallel()
	_, err := metadata.Read(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("expected error on missing file; got nil")
	}
	if !os.IsNotExist(unwrapToOS(err)) {
		t.Errorf("error should wrap os.ErrNotExist; got %v", err)
	}
}

func unwrapToOS(err error) error {
	type wrapper interface{ Unwrap() error }
	for err != nil {
		if w, ok := err.(wrapper); ok {
			err = w.Unwrap()
		} else {
			return err
		}
	}
	return err
}

func writeMetadataYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

const validMetadata = `kind: helm-oci-chart
url: oci://hub.aggrega.ai/charts/mariadb
version: "25.0.8"
digest: "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
mediatype: application/vnd.cncf.helm.chart.content.v1.tar+gzip
pulled_at: "2026-05-22T12:30:00Z"
`

func TestReadAcceptsValidMetadata(t *testing.T) {
	t.Parallel()
	path := writeMetadataYAML(t, validMetadata)
	if _, err := metadata.Read(path); err != nil {
		t.Errorf("valid metadata should parse cleanly; got %v", err)
	}
}

func TestReadRejectsMissingRequiredField(t *testing.T) {
	t.Parallel()
	// metadata.yaml missing the digest field; required by the schema.
	content := `kind: helm-oci-chart
url: oci://r/c
version: "1.0.0"
mediatype: application/vnd.cncf.helm.chart.content.v1.tar+gzip
pulled_at: "2026-05-22T12:30:00Z"
`
	path := writeMetadataYAML(t, content)
	_, err := metadata.Read(path)
	if err == nil {
		t.Fatal("expected error for missing required field; got nil")
	}
	if !errors.Is(err, metadata.ErrCacheMetadataInvalid) {
		t.Errorf("error should wrap ErrCacheMetadataInvalid; got %v", err)
	}
}

func TestReadRejectsUnknownProperty(t *testing.T) {
	t.Parallel()
	content := `kind: helm-oci-chart
url: oci://r/c
version: "1.0.0"
digest: "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
mediatype: application/vnd.cncf.helm.chart.content.v1.tar+gzip
pulled_at: "2026-05-22T12:30:00Z"
extra_field: "foo"
`
	path := writeMetadataYAML(t, content)
	_, err := metadata.Read(path)
	if !errors.Is(err, metadata.ErrCacheMetadataInvalid) {
		t.Errorf("unknown property should wrap ErrCacheMetadataInvalid; got %v", err)
	}
}

func TestReadRejectsInvalidDigestPattern(t *testing.T) {
	t.Parallel()
	content := `kind: helm-oci-chart
url: oci://r/c
version: "1.0.0"
digest: "not-a-sha256"
mediatype: application/vnd.cncf.helm.chart.content.v1.tar+gzip
pulled_at: "2026-05-22T12:30:00Z"
`
	path := writeMetadataYAML(t, content)
	_, err := metadata.Read(path)
	if !errors.Is(err, metadata.ErrCacheMetadataInvalid) {
		t.Errorf("invalid digest pattern should wrap ErrCacheMetadataInvalid; got %v", err)
	}
}

func TestReadRejectsInvalidPulledAtPattern(t *testing.T) {
	t.Parallel()
	// pulled_at value is a string but not RFC 3339 shape. Schema's
	// pattern enforces this strictly (format: date-time is documentation).
	content := `kind: helm-oci-chart
url: oci://r/c
version: "1.0.0"
digest: "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
mediatype: application/vnd.cncf.helm.chart.content.v1.tar+gzip
pulled_at: "not-a-timestamp"
`
	path := writeMetadataYAML(t, content)
	_, err := metadata.Read(path)
	if !errors.Is(err, metadata.ErrCacheMetadataInvalid) {
		t.Errorf("invalid pulled_at pattern should wrap ErrCacheMetadataInvalid; got %v", err)
	}
}

// TestErrCacheMetadataInvalidWrapsFetchIntegrity pins the
// fetch-class semantics: a metadata-invalid error MUST also satisfy
// errors.Is against oci.ErrFetchIntegrity so the orchestrator's
// matchFetchSentinel helper routes cache-metadata corruption through
// the FetchError category with the standard `aggrega pull
// --regenerate` remediation. Without this chain, metadata corruption
// would silently route through TranslationError despite being a
// fetch-class operational concern (the cache slot is bad,
// regenerate).
func TestErrCacheMetadataInvalidWrapsFetchIntegrity(t *testing.T) {
	t.Parallel()
	if !errors.Is(metadata.ErrCacheMetadataInvalid, oci.ErrFetchIntegrity) {
		t.Errorf("ErrCacheMetadataInvalid MUST wrap oci.ErrFetchIntegrity for fetch-class routing")
	}

	// A Read failure also wraps ErrCacheMetadataInvalid; that wrap
	// must transitively still satisfy errors.Is against
	// oci.ErrFetchIntegrity.
	content := `kind: helm-oci-chart
url: oci://r/c
version: "1.0.0"
digest: "not-a-sha256"
mediatype: application/vnd.cncf.helm.chart.content.v1.tar+gzip
pulled_at: "2026-05-22T12:30:00Z"
`
	path := writeMetadataYAML(t, content)
	_, err := metadata.Read(path)
	if !errors.Is(err, metadata.ErrCacheMetadataInvalid) {
		t.Errorf("Read error should satisfy errors.Is against ErrCacheMetadataInvalid")
	}
	if !errors.Is(err, oci.ErrFetchIntegrity) {
		t.Errorf("Read error should ALSO satisfy errors.Is against oci.ErrFetchIntegrity through the wrapped chain")
	}
}
