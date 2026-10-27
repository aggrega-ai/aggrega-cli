// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/netrc"
)

// credentialServer answers a request carrying login and password as basic
// credentials through serve, and 401 to any other, recording what every request
// carried.
type credentialServer struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string
}

func newCredentialServer(t *testing.T, login, password string, serve http.HandlerFunc) *credentialServer {
	t.Helper()
	s := &credentialServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		s.mu.Lock()
		if ok {
			s.seen = append(s.seen, user+":"+pass)
		} else {
			s.seen = append(s.seen, "")
		}
		s.mu.Unlock()
		if !ok || user != login || pass != password {
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		serve(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *credentialServer) credentialsSeen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

// onHost returns rawURL with its host name replaced by host, keeping the port.
func onHost(t *testing.T, rawURL, host string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	u.Host = host + ":" + u.Port()
	return u.String()
}

// filesHolding returns every file under root whose bytes contain needle.
func filesHolding(t *testing.T, root, needle string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), needle) {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return found
}

func writeNetrc(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "netrc")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const (
	indexLogin    = "indexuser"
	indexPassword = "indextoken-not-for-any-file"
	chartLogin    = "chartuser"
	chartPassword = "charttoken-not-for-any-file"
)

// TestAHelmRepositoryIsFetchedWithEachRequestsOwnHostCredential drives the
// production adapter against a repository root whose index places the chart on
// another host. The index server and the chart server each serve only to their
// own credential, and the netrc file holds one entry per host, so the fetch
// succeeds only if each of the two requests carries its own host's credential:
// the index request the index host's, the chart request the chart host's. The URL
// carries none, and nothing the resolution wrote holds either.
//
// The two servers listen on one loopback address, so the chart server is reached
// by the name localhost to make its host differ from the index server's.
//
// A planted defect makes this fail: scope both requests' credential to the
// authored repository URL rather than to the URL each request fetches.
func TestAHelmRepositoryIsFetchedWithEachRequestsOwnHostCredential(t *testing.T) {
	t.Parallel()

	chartBody := []byte("chart-archive-bytes\n")
	chartSrv := newCredentialServer(t, chartLogin, chartPassword, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(chartBody)
	})
	chartURL := onHost(t, chartSrv.URL, "localhost") + "/charts/mariadb-1.0.0.tgz"
	index := fmt.Sprintf("apiVersion: v1\nentries:\n  mariadb:\n  - name: mariadb\n    version: 1.0.0\n    urls:\n    - %s\n", chartURL)
	indexSrv := newCredentialServer(t, indexLogin, indexPassword, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.yaml" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(index))
	})

	netrcPath := writeNetrc(t,
		"machine 127.0.0.1 login "+indexLogin+" password "+indexPassword+"\n"+
			"machine localhost login "+chartLogin+" password "+chartPassword+"\n")
	root := t.TempDir()

	got, err := NewHTTPSAdapter().Resolve(context.Background(), sourceresolver.SourceSpec{
		Kind:          KindHTTPS,
		URL:           indexSrv.URL,
		Chart:         "mariadb",
		Version:       "1.0.0",
		WorkspaceRoot: root,
		CacheDir:      ".aggrega/cache",
		Client:        &http.Client{Transport: &http.Transport{}},
		Netrc:         netrc.FromFile(netrcPath),
	})
	if err != nil {
		t.Fatalf("Resolve with one netrc entry per host: %v (index server saw %q, chart server saw %q)",
			err, indexSrv.credentialsSeen(), chartSrv.credentialsSeen())
	}
	if seen := indexSrv.credentialsSeen(); len(seen) != 1 || seen[0] != indexLogin+":"+indexPassword {
		t.Errorf("the index server saw credentials %q; want its own host's on the one request", seen)
	}
	if seen := chartSrv.credentialsSeen(); len(seen) != 1 || seen[0] != chartLogin+":"+chartPassword {
		t.Errorf("the chart server saw credentials %q; want its own host's on the one request", seen)
	}
	if got.URL != indexSrv.URL || got.ResolvedURL != chartURL {
		t.Errorf("the resolution recorded URL %q and resolved URL %q; want the URLs as served, %q and %q",
			got.URL, got.ResolvedURL, indexSrv.URL, chartURL)
	}
	for _, needle := range []string{indexPassword, chartPassword, indexLogin, chartLogin} {
		if files := filesHolding(t, root, needle); len(files) != 0 {
			t.Errorf("the resolution wrote %q into %v", needle, files)
		}
	}
}

// TestAFetchStatusIsReadAfterTheURLNotFromIt holds that the status of a failed
// fetch is read from what follows the URL in the getter's report, so a URL whose
// port holds the digits of another status is not mistaken for it.
//
// A planted defect makes this fail: read the status digits anywhere in the report.
func TestAFetchStatusIsReadAfterTheURLNotFromIt(t *testing.T) {
	t.Parallel()

	spec := sourceresolver.SourceSpec{Kind: KindHTTPS, URL: "http://127.0.0.1:40412/charts", Version: "1.0.0"}
	for _, row := range []struct {
		name, report string
		want         error
	}{
		{"a 401 from a port holding 404", "failed to fetch http://127.0.0.1:40412/charts/index.yaml : 401 Unauthorized",
			sourceresolver.ErrFetchUnauthorized()},
		{"a 404 from a port holding 401", "failed to fetch http://127.0.0.1:44013/charts/index.yaml : 404 Not Found",
			sourceresolver.ErrFetchNotFound()},
	} {
		t.Run(row.name, func(t *testing.T) {
			if err := mapHTTPError(spec, errors.New(row.report)); !errors.Is(err, row.want) {
				t.Errorf("mapHTTPError(%q) = %v; want %v", row.report, err, row.want)
			}
		})
	}
}

// TestAHelmFetchWithNoNetrcEntryIsRefusedAsUnauthorized drives the production
// adapter at a direct chart URL with a netrc file holding no entry for its host:
// no credential is sent, and the server's 401 is the unauthorized-fetch refusal,
// whose advice names the netrc entry to write and the file the fetch read.
//
// A planted defect makes this fail: give the refusal the registry fetches'
// advice instead of the netrc one.
func TestAHelmFetchWithNoNetrcEntryIsRefusedAsUnauthorized(t *testing.T) {
	t.Parallel()

	srv := newCredentialServer(t, chartLogin, chartPassword, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("never served\n"))
	})
	netrcPath := writeNetrc(t, "machine elsewhere.example.com login "+chartLogin+" password "+chartPassword+"\n")

	_, err := NewHTTPSAdapter().Resolve(context.Background(), sourceresolver.SourceSpec{
		Kind:          KindHTTPS,
		URL:           srv.URL + "/charts/mariadb-1.0.0.tgz",
		Version:       "1.0.0",
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
		Client:        &http.Client{Transport: &http.Transport{}},
		Netrc:         netrc.FromFile(netrcPath),
	})
	if !errors.Is(err, sourceresolver.ErrFetchUnauthorized()) {
		t.Fatalf("a 401 gave %v; want the unauthorized-fetch refusal", err)
	}
	if seen := srv.credentialsSeen(); len(seen) != 1 || seen[0] != "" {
		t.Errorf("the server saw credentials %q; want none sent", seen)
	}
	msg := err.Error()
	for _, want := range []string{"machine 127.0.0.1", "netrc file " + netrcPath} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not say %q: %q", want, msg)
		}
	}
	if strings.Contains(msg, chartPassword) {
		t.Errorf("the refusal repeats the netrc file's password: %q", msg)
	}
}
