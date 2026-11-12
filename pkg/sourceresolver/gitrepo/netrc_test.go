// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package gitrepo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/netrc"
)

const (
	gitLogin    = "gituser"
	gitPassword = "gittoken-not-for-any-file"
)

// recordingGitServer answers every request: 401 with a basic challenge to one
// not carrying login and password, and 404 to one carrying them — the remote has
// no repository, which is enough to show the credential arrived. It records the
// credential every request carried.
type recordingGitServer struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string
}

func newRecordingGitServer(t *testing.T) *recordingGitServer {
	t.Helper()
	s := &recordingGitServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		s.mu.Lock()
		if ok {
			s.seen = append(s.seen, user+":"+pass)
		} else {
			s.seen = append(s.seen, "")
		}
		s.mu.Unlock()
		if !ok || user != gitLogin || pass != gitPassword {
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *recordingGitServer) credentialsSeen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

func writeNetrc(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "netrc")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestAGitCloneOverHTTPSendsTheNetrcCredentialForItsHost drives the production
// adapter — the production clone, through go-git's HTTP transport on the client
// the spec carries — at a repository URL carrying no credential, with a netrc
// entry for the server's host. The server sees the entry's credential on every
// request, and the clone fails as a repository not found rather than as a
// refused credential.
//
// The production clone installs its client in go-git's process-wide transport
// registry, so this does not run in parallel.
//
// A planted defect makes this fail: hand the clone no credential.
func TestAGitCloneOverHTTPSendsTheNetrcCredentialForItsHost(t *testing.T) {
	srv := newRecordingGitServer(t)
	netrcPath := writeNetrc(t, "machine 127.0.0.1 login "+gitLogin+" password "+gitPassword+"\n")

	_, err := NewAdapter().Resolve(context.Background(), sourceresolver.SourceSpec{
		Kind:          Kind,
		URL:           srv.URL + "/org/module.git",
		Ref:           "v1.0.0",
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
		Client:        srv.Client(),
		Netrc:         netrc.FromFile(netrcPath),
	})
	seen := srv.credentialsSeen()
	if len(seen) == 0 {
		t.Fatalf("the clone reached no request (%v), so this asserts nothing about its credential", err)
	}
	for i, s := range seen {
		if s != gitLogin+":"+gitPassword {
			t.Errorf("request %d carried credential %q; want the netrc entry's", i, s)
		}
	}
	if errors.Is(err, sourceresolver.ErrFetchUnauthorized()) {
		t.Errorf("the clone was refused as unauthorized with the netrc credential sent: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), gitPassword) {
		t.Errorf("the clone's error repeats the netrc file's password: %v", err)
	}
}

// TestAGitCloneWithNoNetrcEntryIsRefusedAsUnauthorized drives the same server
// with a netrc file holding no entry for its host: no credential is sent, and the
// server's 401 is the unauthorized-fetch refusal, whose advice names the netrc
// entry to write and the file the clone read.
//
// A planted defect makes this fail: give the refusal the registry fetches'
// advice instead of the netrc one.
func TestAGitCloneWithNoNetrcEntryIsRefusedAsUnauthorized(t *testing.T) {
	srv := newRecordingGitServer(t)
	netrcPath := writeNetrc(t, "machine elsewhere.example.com login "+gitLogin+" password "+gitPassword+"\n")

	_, err := NewAdapter().Resolve(context.Background(), sourceresolver.SourceSpec{
		Kind:          Kind,
		URL:           srv.URL + "/org/module.git",
		Ref:           "v1.0.0",
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
		Client:        srv.Client(),
		Netrc:         netrc.FromFile(netrcPath),
	})
	if !errors.Is(err, sourceresolver.ErrFetchUnauthorized()) {
		t.Fatalf("a 401 gave %v; want the unauthorized-fetch refusal", err)
	}
	for i, s := range srv.credentialsSeen() {
		if s != "" {
			t.Errorf("request %d carried credential %q; want none sent", i, s)
		}
	}
	msg := err.Error()
	for _, want := range []string{"machine 127.0.0.1", "netrc file " + netrcPath} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not say %q: %q", want, msg)
		}
	}
}

// TestAGitCloneOverAnotherTransportReadsNoNetrcFile holds that a clone not made
// over http or https never reads the netrc file: with a netrc file that does not
// parse, a clone of a repository on this host still resolves.
//
// A planted defect makes this fail: read the netrc file for every clone.
func TestAGitCloneOverAnotherTransportReadsNoNetrcFile(t *testing.T) {
	t.Parallel()

	fixtureDir, _ := buildFixtureRepo(t, map[string]string{"main.tf": "# module\n"})
	malformed := writeNetrc(t, "machine example.com login user pasword secret\n")

	a := &adapter{clone: cloneFromFixture(fixtureDir)}
	if _, err := a.Resolve(context.Background(), sourceresolver.SourceSpec{
		Kind:          Kind,
		URL:           "file://" + fixtureDir,
		Ref:           "HEAD",
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
		Netrc:         netrc.FromFile(malformed),
	}); err != nil {
		t.Fatalf("a clone of a local repository was halted by the netrc file: %v", err)
	}
}
