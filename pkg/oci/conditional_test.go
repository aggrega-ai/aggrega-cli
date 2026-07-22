// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// listingETag mirrors a registry that validates its computed tags listing with a
// strong entity-tag derived from the serialized response body: identical listings
// yield the same validator, a changed listing a different one.
func listingETag(body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf("%q", "sha256:"+hex.EncodeToString(sum[:]))
}

// tagsListServer stands in for a registry serving conditional GET on tags/list: it
// emits a body-derived ETag on 200 and answers a matching If-None-Match with 304.
func tagsListServer(t *testing.T, repo string, tags *[]string, sawIfNoneMatch *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/"+repo+"/tags/list" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		*sawIfNoneMatch = r.Header.Get("If-None-Match")
		body, err := json.Marshal(struct {
			Name string   `json:"name"`
			Tags []string `json:"tags"`
		}{Name: repo, Tags: *tags})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		etag := listingETag(body)
		w.Header().Set("ETag", etag)
		if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
}

// TestConditionalListTagsRoundTrip proves the client raw-HTTP path end to end: a first
// unconditional read returns the tags plus a validator, presenting that validator on a
// second read yields a real 304 Not Modified against the live ETag, and a changed
// listing revalidates to a fresh 200 with a different validator.
func TestConditionalListTagsRoundTrip(t *testing.T) {
	const repo = "component/core/api-server"
	tags := []string{"1.0.0", "1.1.0"}
	var sawIfNoneMatch string
	ts := tagsListServer(t, repo, &tags, &sawIfNoneMatch)
	defer ts.Close()

	host := strings.TrimPrefix(ts.URL, "http://")
	ctx := context.Background()
	client := ts.Client()

	// First read: unconditional. Returns the tags and a validator; no precondition sent.
	gotTags, etag, notModified, truncated, err := oci.ConditionalListTags(ctx, host, repo, "", client)
	if err != nil {
		t.Fatalf("first ConditionalListTags: %v", err)
	}
	if notModified {
		t.Fatal("first read reported notModified; want a full 200")
	}
	if truncated {
		t.Fatal("first read reported truncated; the listing fits one page")
	}
	if etag == "" {
		t.Fatal("first read returned no ETag validator")
	}
	if sawIfNoneMatch != "" {
		t.Fatalf("first read sent If-None-Match %q; want none", sawIfNoneMatch)
	}
	if strings.Join(gotTags, ",") != "1.0.0,1.1.0" {
		t.Fatalf("first read tags = %v; want [1.0.0 1.1.0]", gotTags)
	}

	// Second read: present the stored validator. The listing is unchanged, so the
	// registry answers 304 and the client reports notModified with the validator echoed.
	gotTags, gotEtag, notModified, truncated, err := oci.ConditionalListTags(ctx, host, repo, etag, client)
	if err != nil {
		t.Fatalf("revalidating ConditionalListTags: %v", err)
	}
	if !notModified {
		t.Fatal("revalidation did not report notModified against an unchanged listing")
	}
	if sawIfNoneMatch != etag {
		t.Fatalf("revalidation sent If-None-Match %q; want %q", sawIfNoneMatch, etag)
	}
	if gotEtag != etag {
		t.Fatalf("revalidation etag = %q; want the echoed %q", gotEtag, etag)
	}
	if gotTags != nil || truncated {
		t.Fatalf("revalidation returned tags=%v truncated=%v; want nil,false on 304", gotTags, truncated)
	}

	// A changed listing revalidates to a fresh 200 with a different validator.
	tags = append(tags, "2.0.0")
	gotTags, changedEtag, notModified, _, err := oci.ConditionalListTags(ctx, host, repo, etag, client)
	if err != nil {
		t.Fatalf("post-change ConditionalListTags: %v", err)
	}
	if notModified {
		t.Fatal("post-change read reported notModified against a changed listing")
	}
	if changedEtag == etag {
		t.Fatal("post-change read returned the stale validator; want a fresh one")
	}
	if strings.Join(gotTags, ",") != "1.0.0,1.1.0,2.0.0" {
		t.Fatalf("post-change tags = %v; want the updated listing", gotTags)
	}
}
