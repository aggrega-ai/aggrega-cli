// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package metadata_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		Digest:    "sha256:abc123def456",
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
		Digest:    "sha256:deadbeef",
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
