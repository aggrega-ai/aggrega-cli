// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// flakyListingRegistry is an in-process registry over HTTP that fails the tag
// listing of one repository the first times it is read, with a 503, and serves
// everything else as the go-containerregistry in-memory registry does. failures
// of zero or less fails every read. The daemon's walk reaches it through the
// production index source, so every read goes through the walk's own retrying
// transport.
type flakyListingRegistry struct {
	host     string
	repo     string
	failures int

	mu    sync.Mutex
	fired int
}

func newFlakyListingRegistry(t *testing.T, repo string, failures int) *flakyListingRegistry {
	t.Helper()
	r := &flakyListingRegistry{repo: repo, failures: failures}
	inner := registry.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodGet && req.URL.Path == "/v2/"+r.repo+"/tags/list" {
			r.mu.Lock()
			hit := r.failures <= 0 || r.fired < r.failures
			if hit {
				r.fired++
			}
			r.mu.Unlock()
			if hit {
				http.Error(w, "the registry failed this read", http.StatusServiceUnavailable)
				return
			}
		}
		inner.ServeHTTP(w, req)
	}))
	t.Cleanup(srv.Close)
	r.host = strings.TrimPrefix(srv.URL, "http://")
	return r
}

func (r *flakyListingRegistry) firedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fired
}

// publishTo pushes one component version with searchable annotations to repo on
// the registry at host.
func publishTo(t *testing.T, host, repo string) {
	t.Helper()
	target, err := oci.NewRemoteRepository(host+"/"+repo, &http.Client{})
	if err != nil {
		t.Fatalf("open %s: %v", repo, err)
	}
	ann, err := oci.SearchableMetadata{Kind: "Component", Description: "component " + repo}.Annotations()
	if err != nil {
		t.Fatalf("annotations: %v", err)
	}
	if _, err := oci.Push(context.Background(), target, "1.0.0", oci.MediaTypeComponent, []byte("# "+repo+"\n"), ann); err != nil {
		t.Fatalf("publish %s: %v", repo, err)
	}
}

// TestTheDaemonsRefreshCarriesTheReadsItAskedAgain drives aggrega/registryUpdate
// through the daemon's own dispatch, with no transport supplied by the test,
// against an in-process registry that fails one repository's tag listing, and
// reads the answer as the wire carries it, key for key: every host carries
// readsRetried, at zero too, with the count of reads the walk asked again — each
// read once however many attempts it took.
//
// One fault per row. The control fails nothing, so the key is present and zero;
// a listing failed once is one read asked again; a listing failed on every
// attempt is still one read, and its repository is the one the answer names under
// skipped.
//
// Planted defects, each caught here: the field encoded under "-" again (no key at
// all) turns every row red; the transport counting no read turns the last two rows
// red; the transport counting every attempt rather than every read turns the last
// row red.
func TestTheDaemonsRefreshCarriesTheReadsItAskedAgain(t *testing.T) {
	t.Parallel()
	const (
		flaky = "component/core/web"
		clean = "component/core/api"
	)
	for _, row := range []struct {
		name        string
		failures    int // 0 fails nothing; -1 fails every read
		wantRetried float64
		wantSkipped []string
	}{
		{name: "control: nothing fails", failures: 0, wantRetried: 0},
		{name: "one tag listing answered 503 once", failures: 1, wantRetried: 1},
		{name: "one tag listing answered 503 on every attempt", failures: -1, wantRetried: 1, wantSkipped: []string{flaky}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			repo := flaky
			if row.failures == 0 {
				// The control's fault is aimed at a repository nothing publishes, so it
				// never meets a read.
				repo = "component/core/unpublished"
			}
			reg := newFlakyListingRegistry(t, repo, row.failures)
			publishTo(t, reg.host, clean)
			publishTo(t, reg.host, flaky)

			srv, _, _ := registryWriteFixture(t, "workspace: {}\nregistries:\n  - host: "+reg.host+"\n")
			resp := callDaemonMethod(t, srv, "aggrega/registryUpdate", map[string]any{})
			var answer struct {
				Hosts []map[string]any `json:"hosts"`
			}
			decodeDaemonResult(t, resp, &answer)
			if len(answer.Hosts) != 1 {
				t.Fatalf("one registry is configured; the answer carries %d host(s): %s", len(answer.Hosts), resp.Result)
			}
			hu := answer.Hosts[0]

			switch {
			case row.failures > 0 && reg.firedCount() != row.failures:
				t.Fatalf("the fault failed %d read(s); want %d, so the walk did not meet the failure it is meant to count", reg.firedCount(), row.failures)
			case row.failures < 0 && reg.firedCount() == 0:
				t.Fatal("the fault failed no read, so the walk did not meet the failure it is meant to count")
			}

			got, present := hu["readsRetried"]
			if !present {
				t.Fatalf("the host carries no readsRetried key, so a session client cannot tell how close the walk came to leaving a repository out: %v", hu)
			}
			if n, ok := got.(float64); !ok || n != row.wantRetried {
				t.Errorf("readsRetried = %v; want %v (%v)", got, row.wantRetried, hu)
			}

			var skipped []string
			if list, ok := hu["skipped"].([]any); ok {
				for _, s := range list {
					if m, ok := s.(map[string]any); ok {
						if name, ok := m["repo"].(string); ok {
							skipped = append(skipped, name)
						}
					}
				}
			}
			if strings.Join(skipped, ",") != strings.Join(row.wantSkipped, ",") {
				t.Errorf("skipped = %v; want %v (%v)", skipped, row.wantSkipped, hu)
			}
		})
	}
}
