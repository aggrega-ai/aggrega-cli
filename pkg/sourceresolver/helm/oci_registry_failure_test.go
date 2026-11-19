// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package helm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ocierr "github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
	"oras.land/oras-go/v2/content/memory"
)

// resolveAgainst pulls a chart from the registry the handler serves, through
// the adapter's production target factory, and returns what Resolve reports.
func resolveAgainst(t *testing.T, handler http.Handler) error {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	a := &ociAdapter{newTarget: defaultOCITargetFactory}
	_, err := a.Resolve(context.Background(), sourceresolver.SourceSpec{
		Kind:          KindOCI,
		URL:           "oci://" + strings.TrimPrefix(srv.URL, "http://") + "/charts/mariadb",
		Version:       "1.0.0",
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
		Client:        srv.Client(),
	})
	return err
}

// TestOCIResolveNamesARegistryServerError holds a registry answering 500 to the
// condition that names a server error, and to no other.
func TestOCIResolveNamesARegistryServerError(t *testing.T) {
	t.Parallel()
	err := resolveAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal failure", http.StatusInternalServerError)
	}))
	if !errors.Is(err, ocierr.ErrFetchServerError()) {
		t.Fatalf("a 500 from the registry did not report the server-error condition: %v", err)
	}
	if errors.Is(err, ocierr.ErrFetchNetworkUnreachable()) || errors.Is(err, ocierr.ErrFetchInterrupted()) {
		t.Errorf("a 500 from the registry also reported another condition: %v", err)
	}
}

// TestOCIResolveNamesATransferThatBrokeOff holds a registry that closes the
// connection part-way through the chart blob to the condition that names a
// broken transfer, and to no other.
func TestOCIResolveNamesATransferThatBrokeOff(t *testing.T) {
	t.Parallel()
	body := []byte(strings.Repeat("chart-bytes-", 512))
	store := memory.New()
	if _, err := ocierr.Push(context.Background(), store, "1.0.0", MediatypeHelmChart, body, nil); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	manifestDesc, err := store.Resolve(context.Background(), "1.0.0")
	if err != nil {
		t.Fatalf("resolve manifest: %v", err)
	}
	rc, err := store.Fetch(context.Background(), manifestDesc)
	if err != nil {
		t.Fatalf("fetch manifest: %v", err)
	}
	manifest, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	err = resolveAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/manifests/") {
			w.Header().Set("Content-Type", manifestDesc.MediaType)
			w.Header().Set("Docker-Content-Digest", manifestDesc.Digest.String())
			_, _ = w.Write(manifest)
			return
		}
		// The blob: promise all of it, send part, then drop the connection.
		w.Header().Set("Content-Length", "6144")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body[:100])
		w.(http.Flusher).Flush()
		conn, _, hijackErr := w.(http.Hijacker).Hijack()
		if hijackErr == nil {
			_ = conn.Close()
		}
	}))
	if !errors.Is(err, ocierr.ErrFetchInterrupted()) {
		t.Fatalf("a connection closed mid-blob did not report the interrupted-transfer condition: %v", err)
	}
	if errors.Is(err, ocierr.ErrFetchNetworkUnreachable()) || errors.Is(err, ocierr.ErrFetchServerError()) {
		t.Errorf("a connection closed mid-blob also reported another condition: %v", err)
	}
}
