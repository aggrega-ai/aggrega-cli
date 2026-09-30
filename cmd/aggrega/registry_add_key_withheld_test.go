// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// throwawayKeyFiles generates a fresh P-256 key pair for one test and writes its
// two halves where a person would keep them, as a private-key file and a
// public-key file. Nothing is read from or committed to a fixture.
func throwawayKeyFiles(t *testing.T) (privatePath, privatePEM, publicPath, publicPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate a throwaway key: %v", err)
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("encode the private half: %v", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		t.Fatalf("encode the public half: %v", err)
	}
	dir := t.TempDir()
	privatePEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}))
	publicPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
	privatePath = filepath.Join(dir, "signing-key.pem")
	publicPath = filepath.Join(dir, "signing-key.pub")
	if err := os.WriteFile(privatePath, []byte(privatePEM), 0o600); err != nil {
		t.Fatalf("write the private half: %v", err)
	}
	if err := os.WriteFile(publicPath, []byte(publicPEM), 0o644); err != nil {
		t.Fatalf("write the public half: %v", err)
	}
	return privatePath, privatePEM, publicPath, publicPEM
}

// TestRegistryAddRefusesAKeyFileThatIsNotAPublicKeyWithoutQuotingIt drives
// `registry add --key` the way the command runs it — no transport supplied —
// against a loopback registry that counts every request it receives.
//
// A key file that is not a public key is refused by the configuration gate
// (ErrConfigInvalid) under the same short code that condition always carries, and
// neither the refusal nor the diagnostic a terminal renders from it quotes any
// line of the file: they name the configuration file and the key, and state the
// pattern a public key must match, with the placeholder where the value would be.
// Nothing is written. The row pinning a real public key is the positive control:
// it registers, and the entry holds the file exactly as it was read.
//
// The registry is never asked, on any row: a key supplied by hand is the whole
// trust policy, so there is nothing to fetch, and a refused registration must not
// have reached the network on the way to being refused.
//
// Planted defect: build the configuration gate's validator without the option
// that withholds sensitive values, and both refusal rows see the file's lines in
// the refusal and in the rendered diagnostic.
func TestRegistryAddRefusesAKeyFileThatIsNotAPublicKeyWithoutQuotingIt(t *testing.T) {
	t.Parallel()
	privatePath, privatePEM, publicPath, publicPEM := throwawayKeyFiles(t)
	notAKey := filepath.Join(t.TempDir(), "notes.txt")
	const notAKeyText = "registry token a1b2c3d4e5f6, keep this safe\n"
	if err := os.WriteFile(notAKey, []byte(notAKeyText), 0o600); err != nil {
		t.Fatalf("write the file that is not a key: %v", err)
	}
	const seed = "workspace: {}\n"

	for _, tc := range []struct {
		name        string
		keyPath     string
		content     string
		wantRefused bool
	}{
		{name: "a private key picked by mistake", keyPath: privatePath, content: privatePEM, wantRefused: true},
		{name: "a file that holds no key at all", keyPath: notAKey, content: notAKeyText, wantRefused: true},
		{name: "a public key registers and is pinned as read", keyPath: publicPath, content: publicPEM},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			asked := &atomic.Int32{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				asked.Add(1)
				http.NotFound(w, r)
			}))
			t.Cleanup(srv.Close)
			host := strings.TrimPrefix(srv.URL, "http://")

			root := t.TempDir()
			configPath := filepath.Join(root, workspaceconfig.ConfigFilename)
			if err := os.WriteFile(configPath, []byte(seed), 0o644); err != nil {
				t.Fatalf("write the workspace configuration: %v", err)
			}
			rw := resolveTempWorkspaceAgainst(t, tempWorkspaceOptions{Root: root}, frozenEnv(nil))

			var out bytes.Buffer
			runErr := registryAddRun(t.Context(), registryAddConfig{
				Host:       host,
				KeyPath:    tc.keyPath,
				FetchTrust: true,
				Workspace:  true,
				ws:         rw,
				Out:        &out,
			})

			if got := asked.Load(); got != 0 {
				t.Errorf("the registry was asked %d time(s); a key supplied by hand fetches nothing", got)
			}
			written, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatalf("read the workspace configuration: %v", err)
			}

			if !tc.wantRefused {
				if runErr != nil {
					t.Fatalf("registryAddRun: %v", runErr)
				}
				reloaded, err := workspaceconfig.ResolveWithoutGlobal("", root, frozenEnv(nil))
				if err != nil {
					t.Fatalf("re-resolve: %v", err)
				}
				pol, found := reloaded.Config().RegistryTrustPolicy(host)
				if !found || pol.PublicKey != tc.content {
					t.Errorf("the entry does not hold the key as read: found=%t\n got: %q\nwant: %q", found, pol.PublicKey, tc.content)
				}
				return
			}

			if !errors.Is(runErr, workspaceconfig.ErrConfigInvalid()) {
				t.Fatalf("registryAddRun = %v; want the configuration gate's refusal", runErr)
			}
			got, gotOK := cerrors.ShortCodeOf(runErr)
			want, wantOK := cerrors.ShortCodeOf(workspaceconfig.ErrConfigInvalid())
			if !gotOK || !wantOK || got != want {
				t.Errorf("the refusal carries short code %q (present %t); want the condition's own %q", got, gotOK, want)
			}
			if string(written) != seed {
				t.Errorf("a refused registration changed the configuration:\n%s", written)
			}
			for _, s := range []struct{ surface, text string }{
				{"the refusal", runErr.Error()},
				{"the rendered diagnostic", formatCompilerError(runErr)},
			} {
				surface, text := s.surface, s.text
				if !strings.Contains(text, configPath) || !strings.Contains(text, ": registries[0].public_key: ") {
					t.Errorf("%s does not name the file and the key: %s", surface, text)
				}
				if want := `pattern: ` + redact.Placeholder() + ` does not satisfy pattern "-----BEGIN [A-Z ]*PUBLIC KEY-----"`; !strings.Contains(text, want) {
					t.Errorf("%s does not state the pattern a public key must match: %s", surface, text)
				}
				for i, line := range strings.Split(strings.TrimSpace(tc.content), "\n") {
					if line = strings.TrimSpace(line); line != "" && strings.Contains(text, line) {
						t.Errorf("%s quotes line %d of the key file", surface, i+1)
					}
				}
			}
		})
	}
}
