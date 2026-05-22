// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

func TestCachePathDeterministic(t *testing.T) {
	t.Parallel()
	a := oci.CachePath("/ws", ".aggrega", "hub.aggrega.ai/component/core/api-server", "1.0.0")
	b := oci.CachePath("/ws", ".aggrega", "hub.aggrega.ai/component/core/api-server", "1.0.0")
	if a != b {
		t.Errorf("two CachePath invocations returned different paths:\n  a: %s\n  b: %s", a, b)
	}
	if !strings.Contains(a, "oci-cache") {
		t.Errorf("path should contain oci-cache; got %s", a)
	}
	if !strings.Contains(a, "1.0.0") {
		t.Errorf("path should contain the version; got %s", a)
	}
	if strings.Contains(a, "/component/core/") {
		t.Errorf("URI separators should be percent-encoded inside the path segment; got %s", a)
	}
}

func TestReadCachedMissReturnsErrFetchNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := oci.ReadCached(dir, ".aggrega", "hub.aggrega.ai/component/foo", "1.0.0")
	if err == nil {
		t.Fatal("expected cache miss error; got nil")
	}
	if !errors.Is(err, oci.ErrFetchNotFound) {
		t.Errorf("error should wrap ErrFetchNotFound; got %v", err)
	}
}

func TestWriteReadCachedRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	want := []byte("bundled-bytes-payload")
	if err := oci.WriteCached(dir, ".aggrega", "hub.aggrega.ai/recipe/core/helm/mariadb", "1.0.0", want); err != nil {
		t.Fatalf("WriteCached: %v", err)
	}
	got, err := oci.ReadCached(dir, ".aggrega", "hub.aggrega.ai/recipe/core/helm/mariadb", "1.0.0")
	if err != nil {
		t.Fatalf("ReadCached: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("round-trip bytes mismatch; got %q want %q", got, want)
	}
}
