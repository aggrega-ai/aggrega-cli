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
// It is the scope admitting the operations that reach nothing. Granting that
// one alone, rather than every scope this server publishes, is what makes the
// bound observable at all: a listing names every tool the server carries
// whatever the caller's scopes, so the list reads the same for any
// credential, and only a call to a tool this grant does not reach shows whether
// the credential bounds its bearer. A token carrying every scope would have
// that call admitted, and the proof would read nothing.
const mcpCallerScope = "aggrega:read"

// mcpToolsBeyondTheReadScope are two tools whose operations write inside the
// workspace the server serves, so the grant the token below carries reaches
// neither. Each is asserted listed and each is called, so the listing and the
// bound at the call are read on the same tools.
var mcpToolsBeyondTheReadScope = []string{"pull", "gen-catalog"}

// mcpWorkspaceWriteScope is the scope that admits an operation writing inside
// the workspace the server serves, and so the one a refused call to either
// tool above is told to obtain.
const mcpWorkspaceWriteScope = "aggrega:workspace-write"

// mcpStepUpChallenge is the whole challenge that refusal carries: the scheme,
// the address of the document that says where to obtain a token, the one scope
// that admits the refused call, and the reason, which says the credential was
// accepted and does not reach what it asked for. It is written out rather than
// composed, as the challenge a caller with no token receives is, so what is
// compared is an independent statement of what a client will read.
const mcpStepUpChallenge = `Bearer resource_metadata=` +
	`"https://mcp.example.test/.well-known/oauth-protected-resource/mcp", ` +
	`scope="` + mcpWorkspaceWriteScope + `", error="insufficient_scope"`

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
	return s.mintGranting(t, mcpCallerScope)
}

// mintGranting is mint with the grant the token carries named by the caller: one
// scope, or several separated by spaces.
func (s *mcpAuthorizationServer) mintGranting(t *testing.T, scope string) string {
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
	}).Claims(map[string]any{"scope": scope}).Serialize()
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

// mcpToolCallRequest is one tools/call in the per-request revision, naming the
// given tool with no arguments and stating the revision and the sender's
// capabilities as the tool list request does.
//
// No arguments is deliberate: the refusal the call is sent to draw is decided
// from the reach the tool declares, so an argument could only earn a refusal
// this proof is not about, such as one naming a workspace a listener does not
// let a call name.
func mcpToolCallRequest(tool string) string {
	return `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"` + tool + `","arguments":{},"_meta":{` +
		`"io.modelcontextprotocol/protocolVersion":"` + mcpPerRequestRevision + `",` +
		`"io.modelcontextprotocol/clientCapabilities":{}}}}`
}

// mcpAnswer is one exchange with the endpoint, read whole so every row below is
// compared on the same three things.
type mcpAnswer struct {
	status int
	header http.Header
	body   []byte
}

// askForTheToolList posts the tool list request above.
func askForTheToolList(t *testing.T, client *http.Client, token string) mcpAnswer {
	t.Helper()
	return postToTheEndpoint(t, client, token, "tools/list", "", mcpToolListRequest)
}

// callTheTool posts a call to the named tool.
func callTheTool(t *testing.T, client *http.Client, token, tool string) mcpAnswer {
	t.Helper()
	return postToTheEndpoint(t, client, token, "tools/call", tool, mcpToolCallRequest(tool))
}

// postToTheEndpoint posts one request body, carrying the credential the caller
// supplies and nothing else. name is what the request addresses by name, and
// empty for a method that addresses nothing that way.
//
// An empty token sends no authorization header at all, which is what makes the
// rows each other's control: everything about a request that is not the
// credential is the same bytes.
func postToTheEndpoint(t *testing.T, client *http.Client, token, method, name, body string) mcpAnswer {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://socket/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// The per-request revision asks a request to mirror its body in headers, so
	// an intermediary can route a message without parsing one.
	req.Header.Set("MCP-Protocol-Version", mcpPerRequestRevision)
	req.Header.Set("Mcp-Method", method)
	if name != "" {
		req.Header.Set("Mcp-Name", name)
	}
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
// process, with one token that server minted, carrying the read scope alone,
// and no token at all, as each other's control.
//
// One listener answers every row, so nothing separates the token's rows from
// the last one but the authorization header: the same revision, the same
// headers, the same socket. The admitted listing is read back rather than
// stopping at the status, because a server that answered 200 with nothing
// in it would pass a status check while serving nothing at all.
//
// The token is bounded at the call rather than at the list. A listing names
// every tool the server carries whatever the caller's scopes, so this token
// is listed tools its grant does not reach, and a call to one of them is
// refused before it runs, with the challenge naming the one scope that would
// admit it — the refusal a client acting for a user answers by obtaining that
// scope. That refusal is the one proof here that the grant BOUNDS a caller of
// the built binary rather than merely admitting one: every listing assertion
// would still pass if the credential's scopes had stopped being read at all.
//
// A planted defect is visible from each side. Take the token off the rows that
// present one and each draws the challenge for a caller with no credential
// instead of a tool list or the scope refusal; give one to the last row and the
// challenge it is asserted on stops arriving; let the call check stop reading
// the credential's scopes and the refused calls run instead of drawing the
// scope challenge; and let a listing read those scopes again and the two tools
// drop out of the list.
func TestATokenFromTheDeclaredIssuerReachesTheToolListAndACallerWithoutOneDoesNot(t *testing.T) {
	// Not parallel: spawns a server subprocess.
	authorization := startMCPAuthorizationServer(t)
	socket := filepath.Join(mcpSocketDir(t), "s")
	startMCPServer(t, makeConnectedMCPWorkspace(t, authorization.issuer()), "--transport", "http", "--listen", "unix:"+socket)
	awaitSocket(t, socket)
	client := mcpUnixClient(socket)
	// One token for every row that presents one, so the tools it is listed and
	// the calls it is refused are the same credential's answers.
	token := authorization.mint(t)

	t.Run("a token this deployment's authorization server minted reaches the tool list", func(t *testing.T) {
		got := askForTheToolList(t, client, token)
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
		// And the tools this token's grant does not reach, which a listing names
		// all the same: it says what this server carries, and the credential
		// decides only which of those a call may run. A client acting for a user
		// offers a model only the tools it listed, so a tool left out for want of
		// a scope is one no call ever names, and the refusal the rows below read
		// would never arrive.
		for _, beyond := range mcpToolsBeyondTheReadScope {
			if !slices.Contains(offered, beyond) {
				t.Errorf("the tool list does not offer %q, which this server carries; a listing names every "+
					"tool the server carries whatever the caller's scopes, and it offers %v", beyond, offered)
			}
		}
	})

	for _, tool := range mcpToolsBeyondTheReadScope {
		t.Run("a call to "+tool+" with the same token is refused naming the scope that admits it", func(t *testing.T) {
			got := callTheTool(t, client, token, tool)
			if got.status != http.StatusForbidden {
				t.Fatalf("status = %d, want %d (body %q)", got.status, http.StatusForbidden, got.body)
			}
			if challenge := got.header.Get("WWW-Authenticate"); challenge != mcpStepUpChallenge {
				t.Errorf("WWW-Authenticate = %q, want %q", challenge, mcpStepUpChallenge)
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
			if frame.Result != nil {
				t.Errorf("the refusal carries a result, so something past the refusal answered: %q", got.body)
			}
			if frame.Error == nil {
				t.Fatalf("the refusal carries no error envelope: %q", got.body)
			}
			// The body names the scope as the challenge does, because a caller
			// reading only the answer is told what to obtain too.
			if !strings.Contains(frame.Error.Message, mcpWorkspaceWriteScope) {
				t.Errorf("the refusal does not name %s, the scope that admits the call: %q",
					mcpWorkspaceWriteScope, frame.Error.Message)
			}
		})
	}

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
