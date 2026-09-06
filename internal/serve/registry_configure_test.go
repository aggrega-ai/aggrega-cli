// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// registryWriteFixture resolves a real workspace over a real two-layer cascade
// and hands back the server, the global config path and the workspace config
// path.
//
// The global layer is a file of this test's own, reached through the
// environment override rather than through the machine's own configuration, so
// nothing here can read or write the developer's real registries. That is not
// tidiness: the operations under test default to the global file, so a fixture
// that did not redirect it would mutate the machine.
func registryWriteFixture(t *testing.T, workspaceYAML string) (*Server, string, string) {
	t.Helper()

	root := t.TempDir()
	globalPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(globalPath, []byte("registries: []\n"), 0o644); err != nil {
		t.Fatalf("write the global layer: %v", err)
	}
	wsPath := filepath.Join(root, workspaceconfig.ConfigFilename)
	if err := os.WriteFile(wsPath, []byte(workspaceYAML), 0o644); err != nil {
		t.Fatalf("write the workspace layer: %v", err)
	}

	lookup := func(k string) string {
		if k == workspaceconfig.EnvConfigOverride {
			return globalPath
		}
		return ""
	}
	rw, err := workspaceconfig.Resolve(root, root, lookup)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	srv := newTestServer(t, root, nil, nil)
	srv.resolvedWorkspace = rw
	return srv, globalPath, wsPath
}

// callRegistryMethod drives one request through the production dispatch and
// serializer path and returns the decoded response.
func callRegistryMethod(t *testing.T, srv *Server, method string, params any) jsonRPCResponse {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(1, method, params))
	return runOneRequest(t, srv, &in, &out)
}

// decodeRegistryResult decodes a successful response's result into v, failing
// the test when the response carried an error instead.
func decodeRegistryResult(t *testing.T, resp jsonRPCResponse, v any) {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("expected a result; got error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	if err := json.Unmarshal(resp.Result, v); err != nil {
		t.Fatalf("unmarshal result: %v (raw: %s)", err, resp.Result)
	}
}

// TestRegistryAddOnTheDaemonReportsItsMachineWideScope is the clause-3 proof for
// the registration: it drives the method with NO scope flag — the shape a caller
// that has not thought about scope sends — and asserts the answer names the
// global file and says the change is machine-wide.
//
// Asserting the entry landed would pass on a method that wrote the machine's
// configuration and described the change as this workspace's, which is the
// failure worth catching: a caller told a machine-wide removal was local would
// act on it more freely than it should.
func TestRegistryAddOnTheDaemonReportsItsMachineWideScope(t *testing.T) {
	t.Parallel()
	srv, globalPath, wsPath := registryWriteFixture(t, "workspace: {}\n")

	resp := callRegistryMethod(t, srv, "aggrega/registryAdd", map[string]any{
		"host":       "machine.example.test",
		"name":       "shared",
		"fetchTrust": false,
	})
	var res registryAddResult
	decodeRegistryResult(t, resp, &res)

	if res.Host != "machine.example.test" {
		t.Errorf("host: got %q, want machine.example.test", res.Host)
	}
	if !res.Scope.MachineWide {
		t.Errorf("a registration with no scope flag must report a machine-wide change; got scope %+v", res.Scope)
	}
	if res.Scope.ConfigFile != globalPath {
		t.Errorf("configFile: got %q, want the global layer %q", res.Scope.ConfigFile, globalPath)
	}
	if !res.Trust.Skipped {
		t.Errorf("fetchTrust:false must report a skipped fetch; got %+v", res.Trust)
	}

	// The write landed where the answer said it did, and nowhere else. Reading
	// the two files apart is what separates a true scope statement from a field
	// that merely says the right word.
	global, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read the global layer: %v", err)
	}
	if !strings.Contains(string(global), "machine.example.test") {
		t.Errorf("the global layer does not carry the entry the answer said it wrote:\n%s", global)
	}
	ws, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatalf("read the workspace layer: %v", err)
	}
	if strings.Contains(string(ws), "machine.example.test") {
		t.Errorf("the entry reached the workspace layer, which the answer did not claim:\n%s", ws)
	}
}

// TestRegistryAddOnTheDaemonHonoursTheWorkspaceOverride is the other side of the
// scope statement: a request that asks for the workspace file gets it, and the
// answer says the change is NOT machine-wide.
//
// It is the calibration for the test above. Without it, a method hard-coding
// machineWide to true would pass that one and be wrong.
func TestRegistryAddOnTheDaemonHonoursTheWorkspaceOverride(t *testing.T) {
	t.Parallel()
	srv, globalPath, wsPath := registryWriteFixture(t, "workspace: {}\n")

	resp := callRegistryMethod(t, srv, "aggrega/registryAdd", map[string]any{
		"host":       "local.example.test",
		"workspace":  true,
		"fetchTrust": false,
	})
	var res registryAddResult
	decodeRegistryResult(t, resp, &res)

	if res.Scope.MachineWide {
		t.Errorf("a workspace-scoped registration must not report a machine-wide change; got scope %+v", res.Scope)
	}
	if res.Scope.ConfigFile != wsPath {
		t.Errorf("configFile: got %q, want the workspace layer %q", res.Scope.ConfigFile, wsPath)
	}
	global, _ := os.ReadFile(globalPath)
	if strings.Contains(string(global), "local.example.test") {
		t.Errorf("a workspace-scoped registration reached the global layer:\n%s", global)
	}
}

// TestRegistryRemoveOnTheDaemonReportsScopeAndRefusesAnAbsentEntry covers both
// halves of the removal: what a successful one tells the caller, and that a
// token matching nothing is refused rather than answered as a success over an
// unchanged configuration.
//
// The refusal half is the load-bearing one. A removal that reported success
// having removed nothing would tell a caller the machine's resolution had
// changed when it had not, which is worse than either outcome on its own.
func TestRegistryRemoveOnTheDaemonReportsScopeAndRefusesAnAbsentEntry(t *testing.T) {
	t.Parallel()
	srv, globalPath, _ := registryWriteFixture(t, "workspace: {}\n")

	addResp := callRegistryMethod(t, srv, "aggrega/registryAdd", map[string]any{
		"host":       "drop.example.test",
		"name":       "doomed",
		"fetchTrust": false,
	})
	var added registryAddResult
	decodeRegistryResult(t, addResp, &added)

	// A configuration write is not visible to a session holding the resolution it
	// started with, so the session re-resolves before addressing the entry by its
	// alias — the same thing a client does by reconnecting.
	rw, err := workspaceconfig.Resolve(srv.workspaceRoot, srv.workspaceRoot, func(k string) string {
		if k == workspaceconfig.EnvConfigOverride {
			return globalPath
		}
		return ""
	})
	if err != nil {
		t.Fatalf("re-resolve: %v", err)
	}
	srv.resolvedWorkspace = rw

	removeResp := callRegistryMethod(t, srv, "aggrega/registryRemove", map[string]any{"registry": "doomed"})
	var removed registryRemoveResult
	decodeRegistryResult(t, removeResp, &removed)
	if removed.Host != "drop.example.test" {
		t.Errorf("host: got %q, want the alias resolved to drop.example.test", removed.Host)
	}
	if !removed.Scope.MachineWide {
		t.Errorf("a removal from the global layer must report a machine-wide change; got %+v", removed.Scope)
	}
	global, _ := os.ReadFile(globalPath)
	if strings.Contains(string(global), "drop.example.test") {
		t.Errorf("the entry survived a removal the answer reported as done:\n%s", global)
	}

	again := callRegistryMethod(t, srv, "aggrega/registryRemove", map[string]any{"registry": "drop.example.test"})
	if again.Error == nil {
		t.Fatalf("removing an absent entry must be refused; got result %s", again.Result)
	}
	if !strings.Contains(again.Error.Message, "is not configured in") {
		t.Errorf("the refusal must name what it looked for and where; got %q", again.Error.Message)
	}
}

// TestRegistryUpdateOnTheDaemonRefusesLoudlyWhenACredentialIsMissing is the
// clause-6 proof, driven rather than asserted.
//
// A registry answering 401 with a Basic challenge is exactly the case where a
// terminal would have prompted. The daemon has nobody to prompt, so the only two
// possible behaviours are a loud refusal and a hang — and a hang on this surface
// is the worse failure, because nothing is watching it block. The test therefore
// bounds the call with a LIVENESS CEILING rather than a performance budget: it
// separates "returned" from "did not return", which no amount of machine
// contention can blur.
//
// Nothing supplies a credential: no request field carries one, and the fixture's
// configuration names no identity. What the refusal must do is say what it
// wanted.
func TestRegistryUpdateOnTheDaemonRefusesLoudlyWhenACredentialIsMissing(t *testing.T) {
	// Not parallel: it drives the live index-target resolution, which reads the
	// process environment for the egress client.
	var reached int
	srvHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached++
		w.Header().Set("WWW-Authenticate", `Basic realm="aggrega-test"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srvHTTP.Close)

	host := strings.TrimPrefix(srvHTTP.URL, "http://")
	srv, _, _ := registryWriteFixture(t, "workspace: {}\nregistries:\n  - host: "+host+"\n")

	done := make(chan jsonRPCResponse, 1)
	go func() { done <- callRegistryMethod(t, srv, "aggrega/registryUpdate", map[string]any{"registry": host}) }()

	select {
	case resp := <-done:
		if resp.Error == nil {
			t.Fatalf("a registry that demands a credential must be refused, not answered; got result %s", resp.Result)
		}
		// The refusal names the registry and what it wanted. A bare "failed" would
		// leave the caller with nothing to act on, which on a surface with nobody
		// watching is the same as silence.
		for _, want := range []string{host, "credential"} {
			if !strings.Contains(resp.Error.Message, want) {
				t.Errorf("the refusal must name %q; got %q", want, resp.Error.Message)
			}
		}
		t.Logf("refusal: %s", resp.Error.Message)
	case <-time.After(30 * time.Second):
		t.Fatal("the request did not return: a surface with nobody watching must refuse rather than block")
	}

	// Calibration: a request that never reached the registry would have produced
	// some other failure, and the assertions above would have passed on it.
	if reached == 0 {
		t.Error("the registry was never contacted, so the refusal above is not the credential refusal this test is about")
	}
}

// TestRegistryUpdateOnTheDaemonSaysWhenNothingIsConfigured pins the answer a
// workspace with no registries gets.
//
// "Nothing is configured" and "every configured registry held nothing" are
// different answers to a caller deciding what to do next, and a response
// carrying only an empty host list collapses them.
func TestRegistryUpdateOnTheDaemonSaysWhenNothingIsConfigured(t *testing.T) {
	t.Parallel()
	srv, _, _ := registryWriteFixture(t, "workspace: {}\nregistries: []\n")

	resp := callRegistryMethod(t, srv, "aggrega/registryUpdate", map[string]any{})
	var res registryUpdateResult
	decodeRegistryResult(t, resp, &res)

	if res.RegistriesConfigured {
		t.Errorf("a workspace with no registries must report registriesConfigured false; got %+v", res)
	}
	if res.Hosts == nil || res.Failed == nil {
		t.Errorf("every collection is an array rather than a null, so a client iterates unconditionally; got %+v", res)
	}
}

// TestRegistryWriteMethodsRefuseAMissingSubject pins the two required arguments.
// A registration with no host and a removal with nothing to address are refusals
// naming what to send, not answers over a default.
func TestRegistryWriteMethodsRefuseAMissingSubject(t *testing.T) {
	t.Parallel()
	srv, _, _ := registryWriteFixture(t, "workspace: {}\n")

	for _, tc := range []struct {
		name   string
		method string
		params map[string]any
		want   string
	}{
		{"add with no host", "aggrega/registryAdd", map[string]any{}, "host is required"},
		{"remove with no registry", "aggrega/registryRemove", map[string]any{}, "registry is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := callRegistryMethod(t, srv, tc.method, tc.params)
			if resp.Error == nil {
				t.Fatalf("expected a refusal; got result %s", resp.Result)
			}
			if !strings.Contains(resp.Error.Message, tc.want) {
				t.Errorf("the refusal must name what to send: got %q, want it to contain %q", resp.Error.Message, tc.want)
			}
		})
	}
}
