// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package oci_test

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/registry/remote/auth"

	"github.com/aggrega-ai/aggrega-cli/internal/hometest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// storePlaceholder is the fabricated text a malformed entry decodes to. It has
// no colon, which is the shape the store refuses while quoting what it decoded,
// and it is no credential of anyone's.
const storePlaceholder = "probe-placeholder-token-no-colon"

// challengingRegistry starts a loopback registry that answers every request
// with 401 and a basic challenge — the answer that makes the client consult the
// credential store — and returns its host:port.
func challengingRegistry(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Www-Authenticate", `Basic realm="probe"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// plantCredentialFile writes body as the credential file of this test's own
// redirected credential home and returns its path.
func plantCredentialFile(t *testing.T, body string) string {
	t.Helper()
	dir := hometest.CredentialHome()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create the credential home: %v", err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("plant the credential file: %v", err)
	}
	return path
}

// TestACredentialStoreFailureNamesTheRegistryAndFileButNotTheEntry pins what the
// registry client reports when the credential store cannot answer: a fixed
// sentence naming the registry and the credential file, and no byte of what the
// store read. Each row plants one fault in a fabricated credential file and
// drives the production constructor and a real resolve against a loopback
// registry that asks for a credential.
//
// The store's own text quotes the entry — for a malformed entry, its decoded
// contents; for a file that is not JSON, a character of the file — so a client
// relaying that text fails the absence assertions here. They are checked on the
// error's text and on its one-line cause chain, because a frame left beneath the
// sentence would print through the chain even with the sentence on top.
func TestACredentialStoreFailureNamesTheRegistryAndFileButNotTheEntry(t *testing.T) {
	cases := []struct {
		name string
		// body is the credential file, given the registry host.
		body func(host string) string
		// fixed is a phrase only the fixed sentence carries.
		fixed string
		// namesPath is whether the sentence can name the resolved file path:
		// it can once the store is open, not when opening it failed.
		namesPath bool
		// storeText is a phrase only the store's own text carries.
		storeText string
	}{
		{
			name: "an entry that decodes to text with no colon",
			body: func(host string) string {
				return `{"auths":{"` + host + `":{"auth":"` +
					base64.StdEncoding.EncodeToString([]byte(storePlaceholder)) + `"}}}`
			},
			fixed:     "cannot be read from",
			namesPath: true,
			storeText: "does not conform",
		},
		{
			name: "a file that is not valid JSON",
			body: func(host string) string {
				return `{"auths":{"` + host + `":{"auth":` + storePlaceholder + `}}}`
			},
			fixed:     "cannot be read from the credential file (config.json",
			storeText: "invalid character",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hometest.Isolate(t)
			host := challengingRegistry(t)
			path := plantCredentialFile(t, tc.body(host))

			err := resolveThroughTheStore(t, host)
			if err == nil {
				t.Fatal("the resolve succeeded; the planted fault was never read")
			}
			for _, rendered := range []struct{ how, text string }{
				{"the error's text", err.Error()},
				{"the one-line cause chain", cerrors.CauseChain(err)},
			} {
				if strings.Contains(rendered.text, storePlaceholder) {
					t.Errorf("%s carries the planted entry %q:\n%s", rendered.how, storePlaceholder, rendered.text)
				}
				if strings.Contains(rendered.text, tc.storeText) {
					t.Errorf("%s carries the store's own text (%q):\n%s", rendered.how, tc.storeText, rendered.text)
				}
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.fixed) {
				t.Errorf("the error does not carry the fixed sentence (%q):\n%s", tc.fixed, msg)
			}
			if !strings.Contains(msg, host) {
				t.Errorf("the error does not name the registry %s:\n%s", host, msg)
			}
			if tc.namesPath && !strings.Contains(msg, path) {
				t.Errorf("the error does not name the credential file %s:\n%s", path, msg)
			}
			if !tc.namesPath && !strings.Contains(msg, "config.json") {
				t.Errorf("the error does not name the credential file:\n%s", msg)
			}
			if errors.Is(err, oci.ErrFetchUnauthorized()) {
				t.Errorf("an unreadable store is reported as an authentication refusal, which it is not:\n%s", msg)
			}
		})
	}
}

// TestACredentialStoreThatAnswersBehavesAsBefore is the control for the case
// above: where the store answers — it holds no entry for the registry, or a
// well-formed one — the registry client reports exactly what it reported before
// the store's failures were given a sentence of their own. The loopback registry
// refuses every credential, so both rows end in the authentication refusal, and
// neither carries the sentence.
//
// The refusal is pinned as Resolve maps it: the raw registry failure — the
// store's empty answer, or the registry's 401 — becomes ErrFetchUnauthorized,
// with the raw failure's text kept in the message rather than on the chain. So
// the identity asserted is that condition, and the text asserted is the whole
// message, which is what tells the two answers apart.
func TestACredentialStoreThatAnswersBehavesAsBefore(t *testing.T) {
	cases := []struct {
		name string
		body func(host string) string
		// refusal is the registry failure's own text as the message carries it.
		refusal string
	}{
		{
			name:    "no entry for the registry",
			body:    func(string) string { return `{"auths":{}}` },
			refusal: auth.ErrBasicCredentialNotFound.Error(),
		},
		{
			name: "a well-formed entry",
			body: func(host string) string {
				return `{"auths":{"` + host + `":{"auth":"` +
					base64.StdEncoding.EncodeToString([]byte("probe-user:probe-placeholder-password")) + `"}}}`
			},
			refusal: "response status code 401: Unauthorized",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hometest.Isolate(t)
			host := challengingRegistry(t)
			plantCredentialFile(t, tc.body(host))

			err := resolveThroughTheStore(t, host)
			if err == nil {
				t.Fatal("the resolve succeeded against a registry that refuses every credential")
			}
			if !errors.Is(err, oci.ErrFetchUnauthorized()) {
				t.Errorf("errors.Is(ErrFetchUnauthorized) = false, want true:\n%v", err)
			}
			want := `oci: resolve "1.0.0": HEAD "http://` + host + `/v2/probe/target/manifests/1.0.0": ` +
				tc.refusal + ": " + oci.ErrFetchUnauthorized().Error()
			if got := err.Error(); got != want {
				t.Errorf("the refusal's text changed:\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

// resolveThroughTheStore builds a repository at host through the production
// constructor and resolves a tag in it, returning whichever of the two failed.
func resolveThroughTheStore(t *testing.T, host string) error {
	t.Helper()
	repo, err := oci.NewRemoteRepository(host+"/probe/target", &http.Client{})
	if err != nil {
		return err
	}
	_, _, err = oci.Resolve(context.Background(), repo, "1.0.0")
	return err
}
