// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"crypto/ecdsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// The whole exchange a deployment reached over the network takes part in,
// driven end to end: an authorization server on one address, the compiled
// binary serving a workspace on another, and a caller that holds a token from
// the first knocking on the second.
//
// Nothing here is arranged inside the process under test. The binary is spawned
// as a client would spawn it, it discovers where its verification keys are by
// asking the authorization server the workspace configuration names, and it
// fetches them over the same network gate every outbound request in the binary
// goes through. That is the half the proofs living beside the handler cannot
// reach: they install what they need directly, and so never exercise the wiring
// that decides what a launched process reads its keys with.

// mcpKeySetPath is where the authorization server below publishes its keys.
//
// It is deliberately not a well-known path and not derived from the issuer: the
// point of asking an authorization server where its keys are is that it
// decides, so keys read from a path the deployment could have guessed would
// prove less.
const mcpKeySetPath = "/keys/current"

// mcpPerRequestRevision is the protocol revision the request below states, in
// its body and in the header that mirrors it.
const mcpPerRequestRevision = "2026-07-28"

// mcpCallerSubject is who the token below says presented it. A deployment
// records it and compares it to nothing, so it exists only so a reader of this
// proof can see that a real subject was asserted.
const mcpCallerSubject = "caller@example.test"

// mcpCallerScope is what the token below says its bearer may ask for.
//
// It is the scope admitting the operations that reach nothing, which is what
// every tool this proof looks for declares. Granting that one alone, rather than
// every scope this deployment publishes, is what makes the assertions below
// worth anything: a token carrying all of them would reach the tool list even if
// the narrowest scope had stopped admitting a single operation, so the narrow
// grant is the one that proves the credential carried reach rather than merely
// being accepted.
const mcpCallerScope = "aggrega:read"

// mcpAuthorizationServer is an authorization server running in this test
// process, for the binary running in another one.
//
// It publishes what it is at the well-known path an authorization server
// publishes at, serves its key set at the address that document names, and
// mints tokens for the deployment these proofs declare. What it is not is a
// real authorization server: no client is registered with it and no flow of any
// kind is driven, because what is under test is the deployment's side of the
// exchange — whether a token that arrives is accepted, and what a caller
// holding none is told.
//
// It answers on loopback, which is what makes it usable from another process at
// all: the binary dials the address this server states for itself.
type mcpAuthorizationServer struct {
	ts *httptest.Server

	// signingKey mints the tokens this proof presents. The published half is
	// what the key set carries, and is the only half that leaves this process.
	signingKey *ecdsa.PrivateKey
}

// startMCPAuthorizationServer starts one, with a single signing key.
func startMCPAuthorizationServer(t *testing.T) *mcpAuthorizationServer {
	t.Helper()
	key, published := newMCPVerificationKey(t)
	s := &mcpAuthorizationServer{signingKey: key}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		// The identifier stated here is the one the workspace configuration
		// declares. A deployment refuses a document naming any other, which is
		// what stops one server's well-known address directing it at another
		// server's keys.
		writeMCPJSON(w, map[string]any{
			"issuer":   s.issuer(),
			"jwks_uri": s.issuer() + mcpKeySetPath,
		})
	})
	mux.HandleFunc(mcpKeySetPath, func(w http.ResponseWriter, _ *http.Request) {
		writeMCPJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{published}})
	})

	s.ts = httptest.NewServer(mux)
	t.Cleanup(s.ts.Close)
	return s
}

// issuer is the identifier this server states for itself, which is what a
// deployment declares as mcp.authorization.issuer.
func (s *mcpAuthorizationServer) issuer() string { return s.ts.URL }

// mint signs one token of the kind this deployment accepts: issued by this
// server, for the resource the deployment publishes for itself, inside its
// validity window, and signed with the key the published set carries.
//
// It declares itself an access token, which is what an authorization server
// issuing one writes. A deployment accepts several spellings of that
// declaration and a token declaring nothing at all; which ones are exercised
// against every other claim a token can carry belongs with the validator, and
// what this proof needs is one token a real issuer would have minted.
//
// The grant travels as the space-separated claim rather than as the array one.
// A deployment reads both, so the spelling is a choice rather than a
// requirement, and this one is chosen because it is the form registered for a
// token issued as an access token — the shape a real authorization server is
// likeliest to publish. The array spelling is exercised where the validator's
// own proofs are.
func (s *mcpAuthorizationServer) mint(t *testing.T) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.ES256, Key: s.signingKey},
		(&jose.SignerOptions{ExtraHeaders: map[jose.HeaderKey]any{
			jose.HeaderType: "at+jwt",
		}}).WithHeader("kid", mcpVerificationKeyID))
	if err != nil {
		t.Fatalf("build a signer: %v", err)
	}
	raw, err := jwt.Signed(signer).Claims(jwt.Claims{
		Issuer:   s.issuer(),
		Subject:  mcpCallerSubject,
		Audience: jwt.Audience{mcpPublishedResource},
		Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}).Claims(map[string]any{"scope": mcpCallerScope}).Serialize()
	if err != nil {
		t.Fatalf("mint a token: %v", err)
	}
	return raw
}

// writeMCPJSON answers one document from the authorization server.
//
// It is handed no testing handle, deliberately: it runs on the goroutine the
// server gave the request rather than on the test's own, and the testing
// package's failure calls belong to the test's goroutine alone. A document that
// cannot be composed is therefore answered as a server failure, which the
// deployment reports as keys it could not read — a failure the proof sees.
func writeMCPJSON(w http.ResponseWriter, body any) {
	raw, err := json.Marshal(body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

// makeConnectedMCPWorkspace writes a workspace that reads its verification keys
// from the authorization server at issuer.
//
// It is the counterpart of makeMCPWorkspace, and the two differ in exactly the
// thing that decides where a deployment's keys come from. That one declares the
// offline posture and names a key-set file, because a workspace making no
// outbound request has nowhere else for the keys to come from. This one makes
// no such declaration and names no file, so the deployment asks its
// authorization server where its keys are and fetches them — which is the path
// a deployment on a connected host takes.
func makeConnectedMCPWorkspace(t *testing.T, issuer string) string {
	t.Helper()
	dir := t.TempDir()
	content := "workspace: {}\noffline: false\n" +
		"mcp:\n" +
		"  authorization:\n" +
		"    issuer: " + issuer + "\n" +
		"    resource: " + mcpPublishedResource + "\n"
	if err := os.WriteFile(filepath.Join(dir, ".aggrega-config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return dir
}

// mcpToolListRequest is one tools/list in the per-request revision: the
// revision it speaks and the capabilities its sender offers stated in the
// message itself, which is what that revision asks of every request.
const mcpToolListRequest = `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{` +
	`"io.modelcontextprotocol/protocolVersion":"` + mcpPerRequestRevision + `",` +
	`"io.modelcontextprotocol/clientCapabilities":{}}}}`

// mcpAnswer is one exchange with the endpoint, read whole so the two rows below
// are compared on the same three things.
type mcpAnswer struct {
	status int
	header http.Header
	body   []byte
}

// askForTheToolList posts the request above, carrying the credential the caller
// supplies and nothing else.
//
// An empty token sends no authorization header at all, which is what makes the
// two rows each other's control: everything about the request that is not the
// credential is the same bytes.
func askForTheToolList(t *testing.T, client *http.Client, token string) mcpAnswer {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://socket/mcp", strings.NewReader(mcpToolListRequest))
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// The per-request revision asks a request to mirror its body in headers, so
	// an intermediary can route a message without parsing one.
	req.Header.Set("MCP-Protocol-Version", mcpPerRequestRevision)
	req.Header.Set("Mcp-Method", "tools/list")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return mcpAnswer{status: resp.StatusCode, header: resp.Header, body: raw}
}

// TestATokenFromTheDeclaredIssuerReachesTheToolListAndACallerWithoutOneDoesNot
// drives the compiled binary against an authorization server in this test
// process, with the two credentials as each other's control.
//
// One listener answers both rows, so nothing separates them but the
// authorization header: the same body, the same headers, the same socket. The
// admitted row reads the tool list back rather than stopping at the status,
// because a deployment that answered 200 with nothing in it would pass a status
// check while serving nothing at all.
//
// A planted defect is visible from either side: take the token off the admitted
// row and it draws the challenge instead of a tool list, and give one to the
// other row and the challenge it is asserted on stops arriving.
func TestATokenFromTheDeclaredIssuerReachesTheToolListAndACallerWithoutOneDoesNot(t *testing.T) {
	// Not parallel: spawns a server subprocess.
	authorization := startMCPAuthorizationServer(t)
	socket := filepath.Join(mcpSocketDir(t), "s")
	startMCPServer(t, makeConnectedMCPWorkspace(t, authorization.issuer()), "--transport", "http", "--listen", "unix:"+socket)
	awaitSocket(t, socket)
	client := mcpUnixClient(socket)

	t.Run("a token this deployment's authorization server minted reaches the tool list", func(t *testing.T) {
		got := askForTheToolList(t, client, authorization.mint(t))
		if got.status != http.StatusOK {
			t.Fatalf("status = %d, want %d (body %q)", got.status, http.StatusOK, got.body)
		}

		var frame struct {
			Result *struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"result"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(got.body, &frame); err != nil {
			t.Fatalf("the answer is not a protocol frame: %v (body %q)", err, got.body)
		}
		if frame.Error != nil {
			t.Fatalf("the request was refused: %s", frame.Error.Message)
		}
		if frame.Result == nil {
			t.Fatalf("the answer carries no result: %q", got.body)
		}

		offered := make([]string, 0, len(frame.Result.Tools))
		for _, tool := range frame.Result.Tools {
			offered = append(offered, tool.Name)
		}
		if len(offered) == 0 {
			t.Fatalf("the tool list is empty, so nothing behind the credential answered: %q", got.body)
		}
		// The named tools are the ones the client setup guidance tells a reader
		// to look for once a client is wired up, so a rename that made that
		// guidance wrong is read here rather than by whoever follows it. Each
		// name is asserted rather than the list merely being non-empty, so a
		// narrowing that left some tools behind is read here too.
		for _, want := range []string{"scaffold", "validate", "describe", "get-schema"} {
			if !slices.Contains(offered, want) {
				t.Errorf("the tool list does not offer %q; it offers %v", want, offered)
			}
		}
		// And the other direction, which the names above cannot reach: the grant
		// this token carries admits the operations that reach nothing and no
		// others, so a tool whose operation writes inside the workspace is absent
		// from what this caller is offered. Asserting the absence is what makes
		// this the one proof that the grant BOUNDS a caller of the built binary
		// rather than merely admitting one — every assertion above would still
		// pass if the credential's reach had stopped being read at all.
		for _, withheld := range []string{"pull", "gen-catalog"} {
			if slices.Contains(offered, withheld) {
				t.Errorf("the tool list offers %q, which this credential's grant does not reach; it offers %v",
					withheld, offered)
			}
		}
	})

	t.Run("the same request carrying no token is challenged", func(t *testing.T) {
		got := askForTheToolList(t, client, "")
		if got.status != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d (body %q)", got.status, http.StatusUnauthorized, got.body)
		}
		if challenge := got.header.Get("WWW-Authenticate"); challenge != mcpPublishedChallenge {
			t.Errorf("WWW-Authenticate = %q, want %q", challenge, mcpPublishedChallenge)
		}

		var frame struct {
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(got.body, &frame); err != nil {
			t.Fatalf("the refusal is not a protocol frame: %v (body %q)", err, got.body)
		}
		if frame.Error == nil {
			t.Errorf("the refusal carries no error envelope: %q", got.body)
		}
		if frame.Result != nil {
			t.Errorf("the refusal carries a result, so something behind the challenge answered: %q", got.body)
		}
	})
}
