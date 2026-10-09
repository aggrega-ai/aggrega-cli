// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/registry"

	"github.com/aggrega-ai/aggrega-cli/internal/retry"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// wireRequest is one request the registry received, in arrival order.
type wireRequest struct {
	method string
	path   string
	digest string
	status int
}

// uploadFaultingRegistry serves the distribution protocol from an in-memory
// registry and fails the first finalizing PUT of a blob upload with a 500,
// once armed. It records every request after arming.
//
// It also answers a second PUT on an upload location that has already received
// one with a 416, the way a registry does that rewrites an upload's state as it
// takes in the body: such a registry cannot accept a resent final request, and
// only a new upload session recovers. So a client that resent the failed PUT
// fails here, as it would against that registry, rather than passing because
// this test's registry happened to be forgiving.
type uploadFaultingRegistry struct {
	inner http.Handler

	mu       sync.Mutex
	armed    bool
	faulted  bool
	putSeen  map[string]bool
	requests []wireRequest
}

func (r *uploadFaultingRegistry) arm() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.armed = true
	r.putSeen = map[string]bool{}
}

func (r *uploadFaultingRegistry) recorded() []wireRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]wireRequest(nil), r.requests...)
}

func (r *uploadFaultingRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	if !r.armed {
		r.mu.Unlock()
		r.inner.ServeHTTP(w, req)
		return
	}
	rec := wireRequest{method: req.Method, path: req.URL.Path, digest: req.URL.Query().Get("digest")}
	finalizing := req.Method == http.MethodPut && strings.Contains(req.URL.Path, "/blobs/uploads/")
	if finalizing {
		if r.putSeen[req.URL.Path] {
			rec.status = http.StatusRequestedRangeNotSatisfiable
			r.requests = append(r.requests, rec)
			r.mu.Unlock()
			_, _ = io.Copy(io.Discard, req.Body)
			writeRegistryError(w, rec.status, "RANGE_ERROR", "the upload's state does not match this request")
			return
		}
		r.putSeen[req.URL.Path] = true
		if !r.faulted {
			r.faulted = true
			rec.status = http.StatusInternalServerError
			r.requests = append(r.requests, rec)
			r.mu.Unlock()
			_, _ = io.Copy(io.Discard, req.Body)
			writeRegistryError(w, rec.status, "INTERNAL_ERROR", "internal error")
			return
		}
	}
	r.requests = append(r.requests, rec)
	r.mu.Unlock()
	r.inner.ServeHTTP(w, req)
}

func writeRegistryError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `{"errors":[{"code":"`+code+`","message":"`+message+`"}]}`)
}

// TestARetriedBlobUploadOpensANewUploadRatherThanResendingTheFailedOne proves on
// the wire what makes a retry safe against a registry that cannot accept a
// resent final upload request: the retry of a blob whose finalizing PUT failed
// begins with a new POST, and no upload location ever receives two PUTs.
//
// It drives a published-at attach — the write that met the transient 500 this
// retry exists for — through the production repository constructor, so the
// transport under test is the one a publication uses. A transport that retried
// the request itself would resend the failed PUT to the same location, which
// the registry above answers 416, so the attach fails and the recorded sequence
// shows the repeated location.
func TestARetriedBlobUploadOpensANewUploadRatherThanResendingTheFailedOne(t *testing.T) {
	// Not parallel: mutates the process-global retry.BaseDelay seam.
	prev := retry.BaseDelay
	retry.BaseDelay = time.Millisecond
	t.Cleanup(func() { retry.BaseDelay = prev })

	reg := &uploadFaultingRegistry{inner: registry.New()}
	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	ctx := context.Background()
	repo, err := oci.NewRemoteRepository(host+"/component/core/api-server", &http.Client{})
	if err != nil {
		t.Fatalf("NewRemoteRepository: %v", err)
	}
	if _, err := oci.Push(ctx, repo, "1.0.0", oci.MediaTypeComponent, []byte("kind: Component\n"), nil); err != nil {
		t.Fatalf("publish the subject: %v", err)
	}

	reg.arm()
	at := time.Date(2026, 10, 4, 7, 29, 28, 0, time.UTC)
	if _, err := oci.AttachPublishedAt(ctx, repo, "1.0.0", at); err != nil {
		t.Fatalf("the attach failed on a 500 one retry clears: %v\nrequests:\n%s", err, describeWire(reg.recorded()))
	}
	requests := reg.recorded()

	// No upload location ever receives two finalizing PUTs.
	seen := map[string]int{}
	for i, r := range requests {
		if r.method != http.MethodPut || !strings.Contains(r.path, "/blobs/uploads/") {
			continue
		}
		if first, ok := seen[r.path]; ok {
			t.Fatalf("upload location %s received a PUT at request %d and again at %d — a resend of the failed request\n%s",
				r.path, first, i, describeWire(requests))
		}
		seen[r.path] = i
	}

	// The failed PUT is followed by a new POST, then a PUT of the same digest on
	// the location that POST opened.
	failedAt := -1
	for i, r := range requests {
		if r.method == http.MethodPut && r.status == http.StatusInternalServerError {
			failedAt = i
			break
		}
	}
	if failedAt < 0 {
		t.Fatalf("the registry never failed a finalizing PUT, so this run proves nothing about a retry\n%s", describeWire(requests))
	}
	failed := requests[failedAt]
	postBetween, retriedAt := false, -1
	for i := failedAt + 1; i < len(requests); i++ {
		r := requests[i]
		if r.method == http.MethodPost && strings.HasSuffix(strings.TrimSuffix(r.path, "/"), "/blobs/uploads") {
			postBetween = true
			continue
		}
		if r.method == http.MethodPut && r.digest == failed.digest {
			retriedAt = i
			break
		}
	}
	if retriedAt < 0 {
		t.Fatalf("the blob whose PUT failed (%s) was never uploaded again\n%s", failed.digest, describeWire(requests))
	}
	if !postBetween {
		t.Fatalf("the retry of %s did not open a new upload with a POST before its PUT\n%s", failed.digest, describeWire(requests))
	}
	if requests[retriedAt].path == failed.path {
		t.Fatalf("the retry PUT went to the failed upload's own location %s\n%s", failed.path, describeWire(requests))
	}

	// What landed is one date, the one attached.
	refs, err := oci.ListReferrers(ctx, repo, "1.0.0", oci.MediaTypePublishedAt)
	if err != nil {
		t.Fatalf("ListReferrers: %v", err)
	}
	if len(refs) != 1 {
		t.Errorf("published-at referrers = %d; want 1", len(refs))
	}
	got, found, err := oci.ReadPublishedAt(ctx, repo, "1.0.0")
	if err != nil || !found {
		t.Fatalf("ReadPublishedAt: found=%v err=%v", found, err)
	}
	if !got.Equal(at) {
		t.Errorf("stated date %v; want %v", got, at)
	}
}

func describeWire(requests []wireRequest) string {
	var b strings.Builder
	for i, r := range requests {
		fmt.Fprintf(&b, "  %d %s %s %s %d\n", i, r.method, r.path, r.digest, r.status)
	}
	return b.String()
}
