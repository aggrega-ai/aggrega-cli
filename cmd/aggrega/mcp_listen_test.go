// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// Every proof in this file drives the compiled binary, spawned as a client would
// spawn it, with nothing of this test process inside it. That is what the
// credential proof below needs: the refusing authenticator is installed by the
// construction path the command takes and there is no exported route by which a
// test could replace it, so the only way to show what a deployment actually
// serves is to be outside the process and knock.
//
// The listeners here bind a Unix socket inside a temporary directory rather than
// a port, for two reasons that both matter: the path is known before the process
// starts, so readiness is observable without asking the child what it bound, and
// nothing about the run depends on a port being free.

// mcpSocketDir returns a directory for a Unix socket whose path stays well
// inside the length a socket address permits. It is deliberately not t.TempDir(),
// whose path carries the test's own name.
func mcpSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "aggrega-mcp-*")
	if err != nil {
		t.Fatalf("create socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// mcpDeclaredDeployment is what a listener has to be told before it will bind:
// which authorization server issues the tokens it accepts, the address it
// publishes for itself as the resource they are issued for, and where its
// verification keys are. It is written beneath an `mcp:` key, so a row that
// also names a listen address writes both under one.
//
// The key set is named as a file because the workspace these proofs run in is
// offline, and an offline workspace makes no request to its authorization
// server — so a listener there is refused before it binds unless the keys come
// from disk. The path is relative, and resolves against the configuration file
// that declared it, which is what makeMCPWorkspace writes it beside.
//
// A row that starts no listener does not need it, and the stdio row below
// deliberately leaves it out: nothing about speaking over the process's own
// streams reads any of it.
const mcpDeclaredDeployment = "  authorization:\n" +
	"    issuer: https://login.example.test\n" +
	"    resource: " + mcpPublishedResource + "\n" +
	"    jwks_file: " + mcpKeySetFileName + "\n"

// mcpPublishedResource is the address every deployment in these proofs
// publishes for itself, and what a token has to name among its audiences to be
// issued for it. It is never dialled — a resource identifier is what a client
// hands its authorization server, not a place this test connects to — so it
// names a host that does not exist and that is deliberate.
//
// It has one home because two deployments declare it: the one that reads its
// keys from a file, and the one that reads them from an authorization server. A
// token minted for one address and presented to a deployment declaring another
// is refused, so the two agreeing by eye would be a proof that passes for the
// wrong reason the day one of them is edited.
const mcpPublishedResource = "https://mcp.example.test/mcp"

// mcpKeySetFileName is where makeMCPWorkspace writes the key set, beside the
// configuration that names it.
const mcpKeySetFileName = "verification-keys.json"

// mcpPublishedChallenge is the whole challenge a caller with no token receives
// from a deployment declared as above: the scheme, the address of the document
// that says where to obtain a token, and the scope needed before anything else.
// It is written out rather than composed, so what is compared is an independent
// statement of what a client will read.
const mcpPublishedChallenge = `Bearer resource_metadata=` +
	`"https://mcp.example.test/.well-known/oauth-protected-resource/mcp", scope="aggrega:read"`

// makeMCPWorkspace writes a workspace whose configuration carries whatever the
// caller adds beneath the workspace marker. It declares the offline posture so a
// server started over it reaches no registry: what these proofs are about is the
// transport, and a network round trip on the way to it would be measuring
// something else.
//
// A key set is written beside that configuration, because the offline posture
// is exactly what makes a listener need one: with no network there is nowhere
// else for the keys a token would be verified against to come from.
func makeMCPWorkspace(t *testing.T, extra string) string {
	t.Helper()
	dir := t.TempDir()
	content := "workspace: {}\noffline: true\n" + extra
	if err := os.WriteFile(filepath.Join(dir, ".aggrega-config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	writeMCPKeySet(t, filepath.Join(dir, mcpKeySetFileName))
	return dir
}

// writeMCPKeySet writes a key set holding one public verification key.
//
// The key is generated rather than committed for the same reason the test
// certificate elsewhere in this package is: a committed one is a blob nobody
// can read, and nothing here depends on which key it is. No private half is
// ever written down — the file holds only what an authorization server
// publishes.
func writeMCPKeySet(t *testing.T, path string) {
	t.Helper()
	_, published := newMCPVerificationKey(t)
	raw, err := json.Marshal(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{published}})
	if err != nil {
		t.Fatalf("marshal a key set: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write a key set: %v", err)
	}
}

// mcpVerificationKeyID is what a key these proofs publish is named by, and what
// a token signed with it names in its header so a deployment knows which key to
// verify it against.
const mcpVerificationKeyID = "the-verification-key"

// newMCPVerificationKey produces one signing key and the half of it that gets
// published, which is the only half that is ever written down.
//
// Both ways a deployment reads its keys are served from here — a key set
// written to a file, and one an authorization server publishes — so the shape
// of what is published has one home rather than two that agree until one is
// edited.
//
// ES256 rather than RSA because it is asymmetric, which is what a deployment
// requires, and small enough to generate per proof without the generation
// showing up as wall clock.
func newMCPVerificationKey(t *testing.T) (*ecdsa.PrivateKey, jose.JSONWebKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate a verification key: %v", err)
	}
	return key, jose.JSONWebKey{
		Key:       key.Public(),
		KeyID:     mcpVerificationKeyID,
		Algorithm: string(jose.ES256),
		Use:       "sig",
	}
}

// startMCPServer spawns the built binary as `aggrega mcp` with the given
// arguments, in the given workspace, and returns the running command.
//
// Stopping it is the cleanup's job, and it asks first: the process is
// interrupted, which is the request a supervisor makes, and only a process that
// has not gone by the liveness ceiling is ended outright.
func startMCPServer(t *testing.T, workspace string, args ...string) *exec.Cmd {
	t.Helper()
	bin := buildServeBinary(t)
	cmd := exec.Command(bin, append([]string{"mcp"}, args...)...)
	cmd.Dir = workspace
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the server: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(responseLivenessCeiling(t)):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	return cmd
}

// awaitSocket waits for the server to be answering on the socket at path, within
// the liveness ceiling. A dial that connects is the readiness signal, because the
// socket file appears before the listener is necessarily accepting on it.
func awaitSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(responseLivenessCeiling(t))
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", path)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("nothing was listening at %s within the liveness ceiling", path)
}

// wantNothingListening asserts that nothing is at path.
//
// It is read ONCE, after the address the invocation did select is already
// answering, and that ordering is what makes it a decision rather than a race:
// the process has finished choosing by then, so an address it has not bound is
// one it never will. Polling for an absence would measure a duration instead,
// and no duration can tell "never" from "not yet".
func wantNothingListening(t *testing.T, path, why string) {
	t.Helper()
	conn, err := net.Dial("unix", path)
	if err == nil {
		_ = conn.Close()
		t.Errorf("something is listening at %s: %s", path, why)
	}
}

// mcpUnixClient dials one Unix socket whatever host a request names.
func mcpUnixClient(socketPath string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}}
}

// TestTheProtocolEndpointServesNothingWithoutACredential drives the production
// wiring, with no seam of any kind, and asserts that every shape of request is
// refused with the challenge a bearer-token deployment answers.
//
// The rows are chosen so that a handler serving ANYTHING would show it: a
// well-formed request in the current revision, one in the handshake revision, a
// body that is not a message at all, and a method the server does not serve. All
// four are refused identically, which is the property — a caller carrying no
// credential learns nothing about the surface behind the challenge, and nothing
// behind it runs, whatever it sent.
//
// A planted defect is visible: admit a request carrying no authorization header
// and every row reads back a status and a body that are not this one.
//
// Every row is a request to the protocol endpoint. The one thing a deployment
// does answer without a credential is the document it publishes about itself,
// which is not on this path and is read on its own path instead.
func TestTheProtocolEndpointServesNothingWithoutACredential(t *testing.T) {
	// Not parallel: spawns a server subprocess.
	socket := filepath.Join(mcpSocketDir(t), "s")
	startMCPServer(t, makeMCPWorkspace(t, "mcp:\n"+mcpDeclaredDeployment), "--listen", "unix:"+socket)
	awaitSocket(t, socket)
	client := mcpUnixClient(socket)

	for _, tc := range []struct {
		name string
		body string
	}{
		{
			"a well-formed request in the current revision",
			`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{` +
				`"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
				`"io.modelcontextprotocol/clientCapabilities":{}}}}`,
		},
		{
			"a handshake-revision initialize",
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18",` +
				`"capabilities":{},"clientInfo":{"name":"probe","version":"0.1"}}}`,
		},
		{"a body that is not a message", `not json at all`},
		{"a method the server does not serve", `{"jsonrpc":"2.0","id":1,"method":"nothing/here"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := client.Post("http://socket/mcp", "application/json", strings.NewReader(tc.body))
			if err != nil {
				t.Fatalf("post: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			raw, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read response body: %v", err)
			}
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d (body %q)", resp.StatusCode, http.StatusUnauthorized, raw)
			}
			if got := resp.Header.Get("WWW-Authenticate"); got != mcpPublishedChallenge {
				t.Errorf("WWW-Authenticate = %q, want %q", got, mcpPublishedChallenge)
			}
			// The refusal says it is one, and says nothing else. A body
			// carrying a result, or anything the row sent, would mean
			// something behind the challenge had answered.
			var frame struct {
				ID     any             `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(raw, &frame); err != nil {
				t.Fatalf("the refusal is not a protocol frame: %v (body %q)", err, raw)
			}
			if frame.Error == nil {
				t.Errorf("the refusal carries no error envelope: %q", raw)
			}
			if frame.Result != nil {
				t.Errorf("the refusal carries a result, so something behind the challenge answered: %q", raw)
			}
			if frame.ID != nil {
				t.Errorf("the refusal names an id, which is read out of a body this server never parsed: %q", raw)
			}
			if strings.Contains(string(raw), "tools/list") || strings.Contains(string(raw), "initialize") {
				t.Errorf("the refusal echoes what was sent: %q", raw)
			}
		})
	}
}

// TestTheListenAddressIsTheFlagThenTheConfigKey drives the three readings of the
// precedence ladder against the built binary.
//
// The unset-flag row is the one that cannot be shown by asserting on a value: an
// unset string flag and a flag set to the empty string carry the same bits, so
// the only thing that separates "fell through to the config key" from "forced an
// empty value over it" is where the process actually listened. Each row below is
// therefore read off a socket.
func TestTheListenAddressIsTheFlagThenTheConfigKey(t *testing.T) {
	// Not parallel: spawns a server subprocess.
	t.Run("the config key is honoured when the flag is unset", func(t *testing.T) {
		socket := filepath.Join(mcpSocketDir(t), "s")
		ws := makeMCPWorkspace(t, "mcp:\n  listen: unix:"+socket+"\n"+mcpDeclaredDeployment)
		startMCPServer(t, ws)
		awaitSocket(t, socket)
	})

	t.Run("the flag wins over the config key", func(t *testing.T) {
		dir := mcpSocketDir(t)
		configured := filepath.Join(dir, "config")
		requested := filepath.Join(dir, "flag")
		ws := makeMCPWorkspace(t, "mcp:\n  listen: unix:"+configured+"\n"+mcpDeclaredDeployment)
		startMCPServer(t, ws, "--listen", "unix:"+requested)

		awaitSocket(t, requested)
		wantNothingListening(t, configured,
			"that is the address the config key named, and the flag named another one")
	})

	t.Run("with neither, the server speaks over standard input and output", func(t *testing.T) {
		ws := makeMCPWorkspace(t, "")
		bin := buildServeBinary(t)
		cmd := exec.Command(bin, "mcp")
		cmd.Dir = ws
		cmd.Stderr = os.Stderr
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatalf("stdin pipe: %v", err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatalf("stdout pipe: %v", err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatalf("start the server: %v", err)
		}
		t.Cleanup(func() {
			_ = stdin.Close()
			_ = cmd.Wait()
		})

		request := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18",` +
			`"capabilities":{},"clientInfo":{"name":"probe","version":"0.1"}}}` + "\n"
		if _, err := io.WriteString(stdin, request); err != nil {
			t.Fatalf("write the request: %v", err)
		}
		frame := readOneStdioLine(t, stdout)
		if _, refused := frame["error"]; refused {
			t.Fatalf("the handshake was refused: %+v", frame["error"])
		}
		if _, answered := frame["result"]; !answered {
			t.Fatalf("the answer carries no result, so nothing spoke the protocol on standard output: %+v", frame)
		}
	})
}

// readOneStdioLine reads one newline-delimited protocol message, within the
// liveness ceiling. The ceiling separates "answered" from "never will" and
// measures nothing else.
func readOneStdioLine(t *testing.T, r io.Reader) map[string]any {
	t.Helper()
	type result struct {
		frame map[string]any
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := bufio.NewReader(r).ReadString('\n')
		if err != nil {
			ch <- result{err: fmt.Errorf("read a line: %w", err)}
			return
		}
		var frame map[string]any
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			ch <- result{err: fmt.Errorf("the line is not a protocol message: %q: %w", line, err)}
			return
		}
		ch <- result{frame: frame}
	}()
	select {
	case got := <-ch:
		if got.err != nil {
			t.Fatalf("%v", got.err)
		}
		return got.frame
	case <-time.After(responseLivenessCeiling(t)):
		t.Fatal("nothing was framed on standard output within the liveness ceiling")
		return nil
	}
}
