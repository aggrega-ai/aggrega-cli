// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compose

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/metadata"
)

func TestComposeResolveHappyPath(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-yaml")
		_, _ = w.Write([]byte("services:\n  api:\n    image: nginx\n"))
	}))
	defer server.Close()

	root := t.TempDir()
	a := NewAdapter().(*adapter)
	a.client = &http.Client{}
	got, err := a.Resolve(context.Background(), sourceresolver.SourceSpec{
		Kind:          Kind,
		URL:           server.URL + "/compose-fragment.yaml",
		Version:       "1.0.0",
		WorkspaceRoot: root,
		CacheDir:      ".aggrega/cache",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Digest == "" || got.Digest[:7] != "sha256:" {
		t.Errorf("Digest = %q; want sha256: prefix", got.Digest)
	}
	if _, err := os.Stat(got.CachePath); err != nil {
		t.Errorf("cache blob missing: %v", err)
	}
	m, err := metadata.Read(filepath.Join(filepath.Dir(got.CachePath), "metadata.yaml"))
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if m.Kind != Kind {
		t.Errorf("metadata Kind = %q; want %s", m.Kind, Kind)
	}
	if m.Mediatype != MediatypeYAML {
		t.Errorf("metadata Mediatype = %q; want %s", m.Mediatype, MediatypeYAML)
	}
}

func TestComposeResolveDeterministicArchive(t *testing.T) {
	t.Parallel()
	body := []byte("services:\n  api:\n    image: nginx:1.27\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()

	a := NewAdapter().(*adapter)
	a.client = &http.Client{}
	spec := sourceresolver.SourceSpec{
		Kind:          Kind,
		URL:           server.URL + "/x.yaml",
		Version:       "1.0.0",
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
	}
	one, err := a.Resolve(context.Background(), spec)
	if err != nil {
		t.Fatalf("Resolve 1: %v", err)
	}
	spec.WorkspaceRoot = t.TempDir()
	two, err := a.Resolve(context.Background(), spec)
	if err != nil {
		t.Fatalf("Resolve 2: %v", err)
	}
	if one.Digest != two.Digest {
		t.Errorf("digest not deterministic: %s vs %s", one.Digest, two.Digest)
	}
	blob1, err := os.ReadFile(one.CachePath)
	if err != nil {
		t.Fatal(err)
	}
	blob2, err := os.ReadFile(two.CachePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(blob1) != string(blob2) {
		t.Errorf("blob bytes not deterministic; len1=%d len2=%d", len(blob1), len(blob2))
	}
}

func TestComposeResolve404WrapsNotFound(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	a := NewAdapter().(*adapter)
	a.client = &http.Client{}
	_, err := a.Resolve(context.Background(), sourceresolver.SourceSpec{
		Kind:          Kind,
		URL:           server.URL + "/missing.yaml",
		Version:       "1.0.0",
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
	})
	if !errors.Is(err, sourceresolver.ErrFetchNotFound()) {
		t.Errorf("error should wrap ErrFetchNotFound; got %v", err)
	}
}

func TestComposeResolve401WrapsUnauthorized(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	a := NewAdapter().(*adapter)
	a.client = &http.Client{}
	_, err := a.Resolve(context.Background(), sourceresolver.SourceSpec{
		Kind:          Kind,
		URL:           server.URL + "/private.yaml",
		Version:       "1.0.0",
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
	})
	if !errors.Is(err, sourceresolver.ErrFetchUnauthorized()) {
		t.Errorf("error should wrap ErrFetchUnauthorized; got %v", err)
	}
}

func TestComposeResolveConnectionRefusedWrapsNetwork(t *testing.T) {
	t.Parallel()
	a := NewAdapter().(*adapter)
	a.client = &http.Client{}
	_, err := a.Resolve(context.Background(), sourceresolver.SourceSpec{
		Kind:          Kind,
		URL:           "http://127.0.0.1:1/never-listens.yaml",
		Version:       "1.0.0",
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
	})
	if !errors.Is(err, sourceresolver.ErrFetchNetworkUnreachable()) {
		t.Errorf("error should wrap ErrFetchNetworkUnreachable; got %v", err)
	}
}
