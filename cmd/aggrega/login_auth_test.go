// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// basicAuthRegistry is a minimal OCI-registry stand-in that requires HTTP Basic
// auth. It answers /v2/ and a single manifest, and records whether the most
// recent request carried valid credentials — enough to prove that the stored
// login credential is actually attached to outgoing requests.
type basicAuthRegistry struct {
	user, pass string
	manifest   []byte
	digest     string

	mu           sync.Mutex
	sawValidAuth bool
}

func newBasicAuthRegistry(user, pass string) *basicAuthRegistry {
	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json",` +
		`"config":{"mediaType":"application/vnd.oci.empty.v1+json","digest":"sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a","size":2,"data":"e30="},` +
		`"layers":[]}`)
	sum := sha256.Sum256(manifest)
	return &basicAuthRegistry{user: user, pass: pass, manifest: manifest, digest: "sha256:" + hex.EncodeToString(sum[:])}
}

func (s *basicAuthRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u, p, ok := r.BasicAuth()
	authed := ok && u == s.user && p == s.pass
	s.mu.Lock()
	s.sawValidAuth = authed
	s.mu.Unlock()
	if !authed {
		w.Header().Set("WWW-Authenticate", `Basic realm="registry"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case r.URL.Path == "/v2/" || r.URL.Path == "/v2":
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	case strings.Contains(r.URL.Path, "/manifests/"):
		w.Header().Set("Docker-Content-Digest", s.digest)
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		w.Header().Set("Content-Length", strconv.Itoa(len(s.manifest)))
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(s.manifest)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *basicAuthRegistry) valid() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sawValidAuth
}

// TestLoginAuthenticatesEndToEnd is the binding proof that login actually
// authenticates — not merely that a credential round-trips through storage. It
// drives the real auth handshake against a Basic-auth server:
//
//   - login (verify on) succeeds, proving VerifyCredential reached the registry
//     with the entered credential and got a 200;
//   - a subsequent NewRemoteRepository request carries the stored Basic auth and
//     resolves (200), with the server confirming it saw valid credentials;
//   - with no login, the same request is rejected with 401 (ErrFetchUnauthorized);
//   - a wrong password fails verification AND stores nothing.
func TestLoginAuthenticatesEndToEnd(t *testing.T) {
	reg := newBasicAuthRegistry("hello", "world")
	srv := httptest.NewServer(reg)
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://") // 127.0.0.1:PORT → loopback → plain HTTP
	repoRef := host + "/conformance/repo"

	t.Run("unauthenticated request is rejected", func(t *testing.T) {
		t.Setenv("DOCKER_CONFIG", t.TempDir()) // no login
		repo, err := oci.NewRemoteRepository(repoRef, &http.Client{})
		if err != nil {
			t.Fatalf("NewRemoteRepository: %v", err)
		}
		_, _, err = oci.Resolve(context.Background(), repo, "1.0.0")
		if !errors.Is(err, oci.ErrFetchUnauthorized()) {
			t.Fatalf("expected ErrFetchUnauthorized without a stored credential; got %v", err)
		}
	})

	t.Run("login then authenticated request succeeds", func(t *testing.T) {
		t.Setenv("DOCKER_CONFIG", t.TempDir())
		// Verify is ON: this exercises VerifyCredential against the live server.
		if err := loginRun(context.Background(), loginConfig{
			ws:       resolveTempWorkspace(t, tempWorkspaceOptions{}),
			Registry: host, Username: "hello", PasswordStdin: true,
			In: strings.NewReader("world\n"), Out: io.Discard,
		}); err != nil {
			t.Fatalf("loginRun (valid credential, verify on): %v", err)
		}
		repo, err := oci.NewRemoteRepository(repoRef, &http.Client{})
		if err != nil {
			t.Fatalf("NewRemoteRepository: %v", err)
		}
		digest, exists, err := oci.Resolve(context.Background(), repo, "1.0.0")
		if err != nil {
			t.Fatalf("Resolve after login: %v", err)
		}
		if !exists || digest != reg.digest {
			t.Errorf("Resolve = (%q, %v); want (%q, true)", digest, exists, reg.digest)
		}
		if !reg.valid() {
			t.Errorf("server did not observe valid Basic auth on the resolved request")
		}
	})

	t.Run("wrong password fails verification and stores nothing", func(t *testing.T) {
		t.Setenv("DOCKER_CONFIG", t.TempDir())
		err := loginRun(context.Background(), loginConfig{
			ws:       resolveTempWorkspace(t, tempWorkspaceOptions{}),
			Registry: host, Username: "hello", PasswordStdin: true,
			In: strings.NewReader("not-the-password\n"), Out: io.Discard,
		})
		if !errors.Is(err, oci.ErrFetchUnauthorized()) {
			t.Fatalf("expected ErrFetchUnauthorized verifying a bad credential; got %v", err)
		}
		got, err := freshDockerStore(t).Get(context.Background(), host)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Username != "" || got.Password != "" {
			t.Errorf("a failed verification must not store a credential; found {user:%q}", got.Username)
		}
	})
}
