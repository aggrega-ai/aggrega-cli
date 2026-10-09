// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestListTagsReportsARepositoryTheRegistryDoesNotHoldAsNotFound lists the tags of
// three repositories on one loopback registry and holds each answer to the
// condition it names.
//
// A repository the registry does not hold is answered by a real registry the way
// the distribution protocol answers it — a 404 naming the repository unknown — and
// the listing reports that as the fetch-not-found condition, the one a resolve
// reports for a tag the registry does not hold, so a reference written without a
// version meets the same condition as one naming a version nobody published. A
// registry that fails the listing for any other reason is NOT reported as
// not-found: a reader must not take a broken registry for an empty one. And a
// repository holding tags lists them, which is the positive control a listing that
// failed every repository would fail.
func TestListTagsReportsARepositoryTheRegistryDoesNotHoldAsNotFound(t *testing.T) {
	genuine := registry.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/plugin/held/thing/tags/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"name":"plugin/held/thing","tags":["1.0.0","1.1.0"]}`))
		case "/v2/plugin/failing/thing/tags/list":
			http.Error(w, "the registry failed", http.StatusInternalServerError)
		default:
			genuine.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	cases := []struct {
		name         string
		repository   string
		wantTags     []string
		wantNotFound bool
		wantErr      bool
	}{
		{name: "a repository the registry does not hold", repository: "plugin/absent/thing", wantNotFound: true, wantErr: true},
		{name: "a registry that fails the listing", repository: "plugin/failing/thing", wantErr: true},
		{name: "a repository holding tags", repository: "plugin/held/thing", wantTags: []string{"1.0.0", "1.1.0"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, err := oci.NewRemoteRepository(host+"/"+tc.repository, &http.Client{})
			if err != nil {
				t.Fatalf("NewRemoteRepository: %v", err)
			}
			tags, err := oci.ListTags(context.Background(), repo)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ListTags error = %v; want an error: %v", err, tc.wantErr)
			}
			if got := errors.Is(err, oci.ErrFetchNotFound()); got != tc.wantNotFound {
				t.Errorf("ListTags reported the fetch-not-found condition = %v; want %v (err: %v)", got, tc.wantNotFound, err)
			}
			if !tc.wantErr && !reflect.DeepEqual(tags, tc.wantTags) {
				t.Errorf("ListTags = %v; want %v", tags, tc.wantTags)
			}
		})
	}
}
