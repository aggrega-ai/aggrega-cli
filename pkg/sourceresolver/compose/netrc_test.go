// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compose

import (
	"context"
	"errors"
	"io/fs"
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
	fragmentLogin    = "fraguser"
	fragmentPassword = "fragtoken-not-for-any-file"
)

// credentialServer serves body to a request carrying login and password as basic
// credentials and answers 401 to any other, recording what every request carried.
type credentialServer struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string
}

func newCredentialServer(t *testing.T, login, password string, body []byte) *credentialServer {
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
		_, _ = w.Write(body)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *credentialServer) credentialsSeen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
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

// writeNetrc writes a netrc file readable by its owner alone and returns its path.
func writeNetrc(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "netrc")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestAComposeFragmentIsFetchedWithTheNetrcCredentialForItsHost drives the
// adapter's Resolve against a server that serves the fragment only to a request
// carrying the credential, with the credential in a netrc entry for the server's
// host and the URL carrying none. The fetch succeeds, the server saw the
// credential arrive, and nothing the resolution wrote holds it.
//
// A planted defect makes this fail: drop the netrc credential from the request.
func TestAComposeFragmentIsFetchedWithTheNetrcCredentialForItsHost(t *testing.T) {
	t.Parallel()

	srv := newCredentialServer(t, fragmentLogin, fragmentPassword, []byte("services:\n  web:\n    image: nginx\n"))
	netrcPath := writeNetrc(t, "machine 127.0.0.1 login "+fragmentLogin+" password "+fragmentPassword+"\n")
	root := t.TempDir()

	got, err := NewAdapter().Resolve(context.Background(), sourceresolver.SourceSpec{
		Kind:          Kind,
		URL:           srv.URL + "/fragments/web.yaml",
		Version:       "1.0.0",
		WorkspaceRoot: root,
		CacheDir:      ".aggrega/cache",
		Client:        srv.Client(),
		Netrc:         netrc.FromFile(netrcPath),
	})
	if err != nil {
		t.Fatalf("Resolve with a netrc entry for the host: %v", err)
	}
	if seen := srv.credentialsSeen(); len(seen) != 1 || seen[0] != fragmentLogin+":"+fragmentPassword {
		t.Fatalf("the server saw credentials %q; want the netrc entry's on the one request", seen)
	}
	if strings.Contains(got.URL, fragmentPassword) || strings.Contains(got.URL, fragmentLogin) {
		t.Errorf("the resolved source's URL carries the credential: %q", got.URL)
	}
	for _, needle := range []string{fragmentPassword, fragmentLogin} {
		if files := filesHolding(t, root, needle); len(files) != 0 {
			t.Errorf("the resolution wrote %q into %v", needle, files)
		}
	}
}

// TestAComposeFetchWithNoNetrcEntryIsRefusedAsUnauthorized drives the same
// server with a netrc file holding no entry for its host. No credential is sent,
// the server's 401 is the unauthorized-fetch refusal, and its advice names the
// netrc entry to write and the file the fetch read.
//
// A planted defect makes this fail: give the refusal the registry fetches'
// advice instead of the netrc one.
func TestAComposeFetchWithNoNetrcEntryIsRefusedAsUnauthorized(t *testing.T) {
	t.Parallel()

	srv := newCredentialServer(t, fragmentLogin, fragmentPassword, []byte("services: {}\n"))
	netrcPath := writeNetrc(t, "machine elsewhere.example.com login "+fragmentLogin+" password "+fragmentPassword+"\n")

	_, err := NewAdapter().Resolve(context.Background(), sourceresolver.SourceSpec{
		Kind:          Kind,
		URL:           srv.URL + "/fragments/web.yaml",
		Version:       "1.0.0",
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
		Client:        srv.Client(),
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
	if strings.Contains(msg, fragmentPassword) {
		t.Errorf("the refusal repeats the netrc file's password: %q", msg)
	}
}

// TestAComposeFetchIsHaltedByANetrcFileThatDoesNotParse holds that a malformed
// netrc file halts the resolution with its own refusal before any request.
func TestAComposeFetchIsHaltedByANetrcFileThatDoesNotParse(t *testing.T) {
	t.Parallel()

	srv := newCredentialServer(t, fragmentLogin, fragmentPassword, []byte("services: {}\n"))
	netrcPath := writeNetrc(t, "machine 127.0.0.1 login "+fragmentLogin+" pasword "+fragmentPassword+"\n")

	_, err := NewAdapter().Resolve(context.Background(), sourceresolver.SourceSpec{
		Kind:          Kind,
		URL:           srv.URL + "/fragments/web.yaml",
		Version:       "1.0.0",
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
		Client:        srv.Client(),
		Netrc:         netrc.FromFile(netrcPath),
	})
	if !errors.Is(err, netrc.ErrNetrcFileUnreadable()) {
		t.Fatalf("a malformed netrc file gave %v; want the unreadable-netrc refusal", err)
	}
	if seen := srv.credentialsSeen(); len(seen) != 0 {
		t.Errorf("the server received %d requests; want none before the netrc file is read", len(seen))
	}
}
