// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// manifestRequestLog serves the distribution protocol from an in-memory registry
// and, once armed, records the method and path of every manifest request in
// arrival order. With tamper set, a manifest GET is answered with the registry's
// own status and headers — its digest and length included — and a body of the
// same length whose last byte is changed, so the body no longer matches the
// digest the response states for it.
type manifestRequestLog struct {
	inner http.Handler

	mu       sync.Mutex
	armed    bool
	tamper   bool
	requests []string
}

func (r *manifestRequestLog) arm(tamper bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.armed = true
	r.tamper = tamper
}

func (r *manifestRequestLog) recorded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.requests...)
}

func (r *manifestRequestLog) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if !strings.Contains(req.URL.Path, "/manifests/") {
		r.inner.ServeHTTP(w, req)
		return
	}
	r.mu.Lock()
	tamper := r.armed && r.tamper && req.Method == http.MethodGet
	if r.armed {
		r.requests = append(r.requests, req.Method+" "+req.URL.Path)
	}
	r.mu.Unlock()
	if !tamper {
		r.inner.ServeHTTP(w, req)
		return
	}
	rec := httptest.NewRecorder()
	r.inner.ServeHTTP(rec, req)
	body := append([]byte(nil), rec.Body.Bytes()...)
	if len(body) > 0 {
		body[len(body)-1] ^= 0x01
	}
	for key, values := range rec.Header() {
		w.Header()[key] = values
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(body)
}

// TestAPullAsksForTheManifestOnceByTheReferenceItWasGiven proves on the wire how
// a pull reads a manifest from a registry: one GET of the manifest by the
// reference exactly as given — a tag stays a tag and a digest stays a digest —
// and no HEAD to resolve the reference first. A body that does not match the
// digest the registry states for it is refused with ErrFetchIntegrity, whichever
// kind of reference named it.
//
// It pulls through the production repository constructor, so the transport under
// test is the one every pull uses.
func TestAPullAsksForTheManifestOnceByTheReferenceItWasGiven(t *testing.T) {
	t.Parallel()
	const repository = "component/core/api-server"
	source := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/api-server\n")

	cases := []struct {
		name string
		// reference picks what the pull names: the tag, or the digest the push landed.
		reference func(digest string) string
		tamper    bool
		wantErr   error
	}{
		{name: "a pull by tag", reference: func(string) string { return "1.0.0" }},
		{name: "a pull by digest", reference: func(digest string) string { return digest }},
		{name: "a tag whose manifest body does not match its digest", reference: func(string) string { return "1.0.0" },
			tamper: true, wantErr: oci.ErrFetchIntegrity()},
		{name: "a digest whose manifest body does not match it", reference: func(digest string) string { return digest },
			tamper: true, wantErr: oci.ErrFetchIntegrity()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reg := &manifestRequestLog{inner: registry.New()}
			srv := httptest.NewServer(reg)
			t.Cleanup(srv.Close)
			host := strings.TrimPrefix(srv.URL, "http://")

			ctx := context.Background()
			repo, err := oci.NewRemoteRepository(host+"/"+repository, &http.Client{})
			if err != nil {
				t.Fatalf("NewRemoteRepository: %v", err)
			}
			digest, err := oci.Push(ctx, repo, "1.0.0", oci.MediaTypeComponent, source, nil)
			if err != nil {
				t.Fatalf("Push: %v", err)
			}

			reference := tc.reference(digest)
			reg.arm(tc.tamper)
			got, err := oci.Pull(ctx, repo, reference)

			want := []string{http.MethodGet + " /v2/" + repository + "/manifests/" + reference}
			if requests := reg.recorded(); !slices.Equal(requests, want) {
				t.Errorf("manifest requests = %q, want exactly %q", requests, want)
			}
			if tc.wantErr != nil {
				if err == nil {
					t.Fatalf("Pull(%q) succeeded over a manifest body that does not match its digest", reference)
				}
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Pull(%q) error = %v, want one wrapping %v", reference, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Pull(%q): %v", reference, err)
			}
			if string(got.Bytes) != string(source) {
				t.Errorf("Pull(%q) bytes = %q, want %q", reference, got.Bytes, source)
			}
			if got.Digest != digest {
				t.Errorf("Pull(%q) digest = %q, want the pushed %q", reference, got.Digest, digest)
			}
		})
	}
}
