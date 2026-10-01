// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
)

// logoutFixture stands up a server over a scratch workspace with the credential
// store redirected into a directory of this test's own.
//
// 🔴 The redirect is a correctness requirement rather than hygiene. The store
// this method addresses is machine-wide, so a fixture that did not redirect it
// would delete credentials belonging to whoever runs the suite. None of these
// tests can be marked parallel for the same reason: the redirect is an
// environment variable.
func logoutFixture(t *testing.T) (*Server, string) {
	t.Helper()
	storeDir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", storeDir)
	return newTestServer(t, t.TempDir(), nil, nil), storeDir
}

// seedCredential puts a credential into the redirected store and returns the
// marker its password carries, so a later assertion can look for that marker in
// a response and find nothing.
func seedCredential(t *testing.T, host string) string {
	t.Helper()
	store, err := credentials.NewStoreFromDocker(credentials.StoreOptions{AllowPlaintextPut: true})
	if err != nil {
		t.Fatalf("NewStoreFromDocker: %v", err)
	}
	const marker = "s3cret-marker-do-not-echo"
	if err := store.Put(context.Background(), host, auth.Credential{Username: "alice", Password: marker}); err != nil {
		t.Fatalf("seed %s: %v", host, err)
	}
	return marker
}

// storedHosts reports which hosts the redirected store file holds entries for,
// read from the file rather than through a store handle so no in-memory view can
// mask a delete that did not reach disk.
func storedHosts(t *testing.T, storeDir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(storeDir, "config.json"))
	if err != nil {
		t.Fatalf("read the store file: %v", err)
	}
	var doc struct {
		Auths map[string]json.RawMessage `json:"auths"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode the store file: %v (raw: %s)", err, data)
	}
	out := make([]string, 0, len(doc.Auths))
	for host := range doc.Auths {
		out = append(out, host)
	}
	return out
}

// TestLogoutOnTheDaemonRemovesTheCredentialAndReportsItsReach proves the reach
// is reported: the method is invoked, the credential leaves the store, and the answer
// says the change reached past this workspace.
//
// The neighbouring host is the assertion that discriminates. A handler that
// wiped the store would satisfy "the credential is gone" while destroying every
// other credential on the machine, and a fixture holding one entry cannot see
// that.
func TestLogoutOnTheDaemonRemovesTheCredentialAndReportsItsReach(t *testing.T) {
	srv, storeDir := logoutFixture(t)
	seedCredential(t, "doomed.example")
	seedCredential(t, "kept.example")

	resp := callDaemonMethod(t, srv, "aggrega/logout", map[string]any{"registry": "doomed.example"})
	var got logoutResult
	decodeDaemonResult(t, resp, &got)

	if got.Registry != "doomed.example" {
		t.Errorf("registry = %q; want the host addressed", got.Registry)
	}
	if !got.Scope.MachineWide {
		t.Error("scope.machineWide = false; the credential store is shared by every project on the machine")
	}
	if got.Scope.WorkspaceRoot != srv.workspaceRoot {
		t.Errorf("scope.workspaceRoot = %q; want the session's own root %q", got.Scope.WorkspaceRoot, srv.workspaceRoot)
	}

	hosts := storedHosts(t, storeDir)
	if len(hosts) != 1 || hosts[0] != "kept.example" {
		t.Errorf("store holds %v; want only kept.example — the named host gone and its neighbour untouched", hosts)
	}
}

// TestLogoutOnTheDaemonCarriesNoCredentialMaterial is the absence claim with its
// instrument named: the assertion runs over the RAW response bytes, because a
// decode into the result type would drop any field the type does not declare and
// report the absence the type guarantees rather than the one the wire shows.
//
// 🔴 The workspace root is excised before the scan, and the reason is a measured
// false positive rather than convenience. That field echoes a filesystem path
// the session was opened over; under the test runner it is a temporary directory
// whose name embeds the name of the test itself, so a scan for the word
// "credential" matched THIS TEST'S NAME and reported a leak that was not there.
// Excising the field keeps the scan pointed at content the handler composed. The
// path is separately not credential material: it is a value the caller supplied.
func TestLogoutOnTheDaemonCarriesNoCredentialMaterial(t *testing.T) {
	srv, _ := logoutFixture(t)
	marker := seedCredential(t, "doomed.example")
	if marker == "" {
		t.Fatal("the fixture seeded no marker, so the absence assertion below would prove nothing")
	}

	resp := callDaemonMethod(t, srv, "aggrega/logout", map[string]any{"registry": "doomed.example"})
	if resp.Error != nil {
		t.Fatalf("expected a result; got error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	raw := string(resp.Result)

	// The shape assertion runs first and on the unmodified bytes, so a response
	// that stopped carrying these fields fails here rather than passing the scan
	// below by having nothing in it.
	for _, want := range []string{`"registry"`, `"scope"`, `"machineWide"`, `"workspaceRoot"`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("the response is missing %s, so the shape under test is not the one asserted: %s", want, raw)
		}
	}

	var shape struct {
		Scope struct {
			WorkspaceRoot string `json:"workspaceRoot"`
		} `json:"scope"`
	}
	if err := json.Unmarshal(resp.Result, &shape); err != nil {
		t.Fatalf("decode the response: %v (raw: %s)", err, raw)
	}
	if shape.Scope.WorkspaceRoot == "" {
		t.Fatal("the response reported no workspace root, so the excision below would be a no-op")
	}
	composed := strings.ReplaceAll(raw, shape.Scope.WorkspaceRoot, "<workspace-root>")

	for _, forbidden := range []string{marker, "alice", "auth", "password", "credential", "secret", "token"} {
		if strings.Contains(strings.ToLower(composed), strings.ToLower(forbidden)) {
			t.Errorf("the response carries %q: %s", forbidden, composed)
		}
	}

	// The instrument is calibrated rather than trusted: the same scan over the
	// same bytes with the marker spliced in must fire, so a green above is a
	// property of the response and not of a scan that never matches anything.
	planted := strings.ReplaceAll(composed, `"registry":"`, `"registry":"`+marker)
	if !strings.Contains(strings.ToLower(planted), strings.ToLower(marker)) {
		t.Fatal("the planted marker did not land, so this scan cannot be shown to fire")
	}
}

// TestLogoutOnTheDaemonAcceptsAHostTheStoreDoesNotHold pins the mirror: the
// command line removes an absent credential successfully, and so does this.
//
// Refusing here would make the daemon narrower than the command line on an
// operation whose whole purpose is that a host end up with no credential — and a
// caller that asked for exactly that would be told it failed.
func TestLogoutOnTheDaemonAcceptsAHostTheStoreDoesNotHold(t *testing.T) {
	srv, storeDir := logoutFixture(t)
	seedCredential(t, "kept.example")

	resp := callDaemonMethod(t, srv, "aggrega/logout", map[string]any{"registry": "never-stored.example"})
	var got logoutResult
	decodeDaemonResult(t, resp, &got)
	if got.Registry != "never-stored.example" {
		t.Errorf("registry = %q; want the host addressed", got.Registry)
	}
	if hosts := storedHosts(t, storeDir); len(hosts) != 1 || hosts[0] != "kept.example" {
		t.Errorf("store holds %v; want kept.example untouched", hosts)
	}
}

// TestLogoutOnTheDaemonRefusesARequestNamingNoRegistry covers the one refusal
// this method has, in the two shapes a caller sends it.
//
// Each row also asserts the message names the REQUEST FIELD and not a
// command-line flag: a daemon caller has no way to pass a flag, so a refusal
// naming one tells them to do something they cannot do.
func TestLogoutOnTheDaemonRefusesARequestNamingNoRegistry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params any
	}{
		{"no params at all", map[string]any{}},
		{"an empty registry", map[string]any{"registry": ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, storeDir := logoutFixture(t)
			seedCredential(t, "kept.example")

			resp := callDaemonMethod(t, srv, "aggrega/logout", tc.params)
			if resp.Error == nil {
				t.Fatalf("expected a refusal; got result %s", resp.Result)
			}
			if resp.Error.Code != jsonRPCInvalidParams {
				t.Errorf("refusal code = %d; want %d, the invalid-params code every missing-field refusal here answers with",
					resp.Error.Code, jsonRPCInvalidParams)
			}
			if !strings.Contains(resp.Error.Message, "registry is required") {
				t.Errorf("refusal = %q; want it to name the field the caller must send", resp.Error.Message)
			}
			if strings.Contains(resp.Error.Message, "--") {
				t.Errorf("refusal names a command-line flag a daemon caller cannot pass: %q", resp.Error.Message)
			}
			if strings.Contains(resp.Error.Message, "argument") {
				t.Errorf("refusal uses command-line vocabulary a request has no analogue for: %q", resp.Error.Message)
			}
			if hosts := storedHosts(t, storeDir); len(hosts) != 1 || hosts[0] != "kept.example" {
				t.Errorf("a refused removal touched the store: holds %v", hosts)
			}
		})
	}
}
