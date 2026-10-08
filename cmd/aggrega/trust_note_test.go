// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/registrytrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// statusTrustRegistry stands a loopback registry that answers the well-known trust
// request with status, and returns its host. It listens on loopback, so the fetch
// speaks plain HTTP to it and nothing leaves the machine.
func statusTrustRegistry(t *testing.T, status int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != registrytrust.WellKnownPath {
			http.NotFound(w, r)
			return
		}
		http.Error(w, http.StatusText(status), status)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// statusTrustClient returns a client whose transport answers every request with
// status and dials nothing, so a test can ask the official hub's own host for its
// trust policy without reaching it.
func statusTrustClient(status int) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
			Body:       http.NoBody,
			Header:     http.Header{},
			Request:    r,
		}, nil
	})}
}

// noPackagePrefixes are the internal package names the trust note used to carry
// in front of its reason. None of them is a word the reader typed or can act on.
var noPackagePrefixes = []string{"registrytrust:", "egress:", "oci:", "workspaceconfig:"}

// TestTheTrustNoteSaysWhatHappenedInTheReadersTerms drives the commands that
// register a registry and reads the trust note each one prints, for every outcome
// that leaves the registration without an advertised policy.
//
// The note opens with what happened to the request, naming the registry — what it
// answered, in the reader's terms where the answer has one meaning, or why it was
// not asked — and goes on to what follows for the entry: the official hub's
// built-in baseline, or the flags that pin a policy by hand. It carries no
// internal package name, and it is pinned whole, so a reason worded anywhere other
// than where the fetch composes it fails the row that reaches it.
//
// The registry rows run `aggrega registry add` through the real command tree
// against a loopback registry answering with each status. The hub cannot be
// served from loopback, so its rows run the hub's own host: offline, through the
// command tree, where it is never asked; and answering with a request for
// credentials — the answer the operator met — through `config init`'s run, which
// takes the transport the test supplies so nothing reaches the network.
func TestTheTrustNoteSaysWhatHappenedInTheReadersTerms(t *testing.T) {
	// Not parallel: the registry rows move the process working directory.
	const byHand = "; pin its trust by hand with --identity or --key\n"
	hub := workspaceconfig.HubBaselineHost
	baseline := "; using the built-in trust baseline for " + hub + " instead\n"

	for _, tc := range []struct {
		name   string
		status int
		want   func(host string) string
	}{
		{name: "a registry that asks for credentials", status: http.StatusUnauthorized,
			want: func(h string) string {
				return "Notice: " + h + " requires credentials to read its trust policy (HTTP 401)" + byHand
			}},
		{name: "a registry that refuses access", status: http.StatusForbidden,
			want: func(h string) string {
				return "Notice: " + h + " refuses access to its trust policy (HTTP 403)" + byHand
			}},
		{name: "a registry that publishes no policy", status: http.StatusNotFound,
			want: func(h string) string { return "Notice: " + h + " publishes no trust policy (HTTP 404)" + byHand }},
		{name: "a registry that answers with another status", status: http.StatusInternalServerError,
			want: func(h string) string {
				return "Notice: " + h + " answered the request for its trust policy with HTTP 500" + byHand
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := statusTrustRegistry(t, tc.status)
			root := t.TempDir()
			writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
			t.Chdir(root)

			out, err := runAggrega(t, "--workspace-root", root, "registry", "add", host, "--workspace")
			if err != nil {
				t.Fatalf("registry add: %v\n%s", err, out)
			}
			assertTrustNote(t, out, tc.want(host))
		})
	}

	t.Run("the hub in an offline workspace", func(t *testing.T) {
		root := t.TempDir()
		writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "offline: true\nworkspace: {}\n")
		t.Chdir(root)

		out, err := runAggrega(t, "--workspace-root", root, "registry", "add", hub, "--workspace")
		if err != nil {
			t.Fatalf("registry add: %v\n%s", err, out)
		}
		assertTrustNote(t, out, hub+" was not asked for its trust policy, because the workspace is offline"+baseline)
	})

	t.Run("the hub asking for credentials while a configuration is seeded", func(t *testing.T) {
		global := filepath.Join(t.TempDir(), "aggrega", "config.yaml")
		lookup := frozenEnv(map[string]string{workspaceconfig.EnvConfigOverride: global})
		rw := resolveTempWorkspaceAgainst(t, tempWorkspaceOptions{}, lookup)

		var out, errOut bytes.Buffer
		if err := configInitRun(t.Context(), configInitConfig{
			Registry:    hub,
			ws:          rw,
			Out:         &out,
			ErrOut:      &errOut,
			trustClient: statusTrustClient(http.StatusUnauthorized),
		}); err != nil {
			t.Fatalf("config init: %v", err)
		}
		if _, err := os.Stat(global); err != nil {
			t.Fatalf("the seeding wrote no configuration at %s: %v", global, err)
		}
		assertTrustNote(t, afterSeedingLocations(t, out.String()),
			hub+" requires credentials to read its trust policy (HTTP 401)"+baseline)
		if errOut.Len() != 0 {
			t.Errorf("a writing seeding put %q on the error stream", errOut.String())
		}
	})
}

// assertTrustNote holds the first line of out to want exactly, and the whole of
// out to carrying no internal package name and the note once.
func assertTrustNote(t *testing.T, out, want string) {
	t.Helper()
	if got := strings.SplitAfter(out, "\n")[0]; got != want {
		t.Errorf("the trust note reads\n%q\nwant\n%q\nfull output:\n%s", got, want, out)
	}
	for _, prefix := range noPackagePrefixes {
		if strings.Contains(out, prefix) {
			t.Errorf("the output carries the internal package name %q:\n%s", prefix, out)
		}
	}
	if n := strings.Count(out, strings.TrimSuffix(want, "\n")); n != 1 {
		t.Errorf("the trust note was printed %d time(s); want once:\n%s", n, out)
	}
}
