// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// Every proof here drives a real listener with the authenticator the handler
// SHIPS with — the token validator — against a real authorization server
// running in the same process. Nothing installs the accept-all authenticator,
// because what is under test is that check itself.

// tokenFixture is one listener with the token validator installed, plus what
// the serving process wrote to its error stream while the proof ran.
//
// The log is captured rather than discarded because one of the properties under
// test is an absence: no part of a token reaches anything a reader or an
// operator can see. An absence asserted against a stream nothing was written to
// would hold whatever the code did.
type tokenFixture struct {
	*httpFixture
	log *lockedBuffer
}

// lockedBuffer collects the error stream. The requests that write to it arrive
// on the server's own goroutines while the test reads it from its own, so the
// two are separated by a lock rather than by hope.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// mark is how much has been written so far, and since is everything written
// after a mark. Together they let one row of a table read only the lines its own
// request produced: a proof that searched the whole stream would pass on a line
// an earlier row wrote, which is the way a per-row assertion quietly stops being
// one.
func (b *lockedBuffer) mark() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

func (b *lockedBuffer) since(from int) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	written := b.buf.String()
	if from > len(written) {
		return ""
	}
	return written[from:]
}

// newTokenFixture starts a listener for the given deployment, with whatever the
// handler installs left in place.
func newTokenFixture(t *testing.T, transport HTTPConfig) *tokenFixture {
	t.Helper()
	log := &lockedBuffer{}
	s := newCommandToolServer(t, nil)
	s.logw = log
	h, err := newHTTPHandler(context.Background(), s, transport)
	if err != nil {
		t.Fatalf("newHTTPHandler: %v", err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return &tokenFixture{httpFixture: &httpFixture{ts: ts, h: h}, log: log}
}

// call posts one well-formed request in the per-request revision, carrying the
// given authorization header. An empty header sends none.
func (f *tokenFixture) call(t *testing.T, authorization string) answer {
	t.Helper()
	return f.callAt(t, endpointPath, authorization)
}

// callAt is call against an arbitrary path, for the row that carries its token
// in the address rather than in the header.
func (f *tokenFixture) callAt(t *testing.T, path, authorization string) answer {
	t.Helper()
	headers := modernHeaders("tools/list", "")
	if authorization != "" {
		headers[headerAuthorization] = authorization
	}
	return f.send(t, http.MethodPost, path, headers, modernRequest(t, 1, "tools/list", nil))
}

// bearer is the header value a caller presenting this token sends.
func bearer(token string) string { return bearerScheme + " " + token }

// A token this deployment's authorization server issued for this deployment is
// admitted, and the caller reaches the protocol surface behind the challenge.
//
// This is the control every refusal below needs: without it each of them would
// pass against a deployment that still refused everyone, which is exactly what
// this endpoint did before there was anything here that could tell one token
// from another. The tool list is read rather than the status alone, because a
// 200 carrying an error envelope would satisfy a status check while nothing had
// been served.
func TestATokenIssuedForThisDeploymentReachesTheProtocolSurface(t *testing.T) {
	issuer := newAuthorizationServerDouble(t)
	f := newTokenFixture(t, transportFor(issuer.authorizationConfig(), issuer.egress()))

	got := f.call(t, bearer(issuer.mint(t, tokenClaimSet{scope: scopeRead + " " + scopeWorkspaceWrite})))
	wantReachedTheToolSurface(t, got)
}

// wantReachedTheProtocolSurface asserts that a call was answered by the tool
// surface rather than only with a status, and reports the tools it was offered.
//
// The list is read back because a 200 carrying an error envelope would satisfy a
// status check while nothing had been served — which is exactly what an admitted
// credential has to disprove. How many tools came back is left to the caller,
// because that is the separate question of what the credential's grant admits.
func wantReachedTheProtocolSurface(t *testing.T, got answer) []string {
	t.Helper()
	if got.status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", got.status, got.raw)
	}
	if got.frame.Error != nil {
		t.Fatalf("the answer carries an error envelope: %+v", got.frame.Error)
	}
	var listed struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(got.frame.Result, &listed); err != nil {
		t.Fatalf("the result is not a tool list: %v (body %q)", err, got.raw)
	}
	names := make([]string, 0, len(listed.Tools))
	for _, entry := range listed.Tools {
		names = append(names, entry.Name)
	}
	return names
}

// wantReachedTheToolSurface is that plus the list being non-empty, which is what
// every credential this server admits is owed where it announces its tools.
//
// The two are separate because the empty list is a real answer rather than a
// failure: a server announcing an empty set of tools answers every credential it
// admits with an empty list, and a caller asserting that is asserting something
// different from a caller asserting the surface answered at all.
// Folding them would make one helper mean two things and leave whichever proof
// used it unable to say which.
func wantReachedTheToolSurface(t *testing.T, got answer) {
	t.Helper()
	if len(wantReachedTheProtocolSurface(t, got)) == 0 {
		t.Errorf("the tool list is empty, so the credential reached the surface and was offered nothing: %q",
			got.raw)
	}
}

// What the credential asserted travels on the request, so the work answered
// afterwards is answered on behalf of someone.
//
// It is read at the handler rather than at the validator, which is the whole
// point: a validator that read a token correctly and dropped what it read would
// pass every row above and leave the next thing that needs a principal with
// nothing. The subject is recorded and compared to nothing — no answer here
// turns on who the caller is — so reading it back is the only way to see that
// it survived.
func TestWhatTheTokenAssertedReachesTheHandler(t *testing.T) {
	issuer := newAuthorizationServerDouble(t)
	f := newTokenFixture(t, transportFor(issuer.authorizationConfig(), issuer.egress()))

	for _, tc := range []struct {
		name       string
		claims     tokenClaimSet
		wantClient string
		wantScopes []string
	}{
		{
			"scopes in the space-separated form, and the client named as the client identifier",
			tokenClaimSet{scope: scopeRead + " " + scopeRegistryWrite, clientID: "studio"},
			"studio",
			[]string{scopeRead, scopeRegistryWrite},
		},
		{
			"scopes in the array form, and the client named as the authorized party",
			tokenClaimSet{scopeList: []string{scopeRead}, azp: "editor"},
			"editor",
			[]string{scopeRead},
		},
		{
			"a token naming no client and no scope at all",
			tokenClaimSet{},
			"",
			nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, endpointPath, strings.NewReader(""))
			req.Header.Set(headerAuthorization, bearer(issuer.mint(t, tc.claims)))
			authenticated, refusal := f.h.auth.authenticate(req)
			if refusal != nil {
				t.Fatalf("the token was refused: %+v", refusal)
			}
			seen := principalFrom(authenticated.Context())
			if seen == nil {
				t.Fatal("the request carries no principal, so nothing after the credential check knows " +
					"who the call is on behalf of")
			}
			if seen.Subject != testSubject {
				t.Errorf("subject = %q, want %q", seen.Subject, testSubject)
			}
			if seen.Issuer != issuer.issuer() {
				t.Errorf("issuer = %q, want %q", seen.Issuer, issuer.issuer())
			}
			if seen.ClientID != tc.wantClient {
				t.Errorf("client = %q, want %q", seen.ClientID, tc.wantClient)
			}
			if !slices.Equal(seen.Scopes, tc.wantScopes) {
				t.Errorf("scopes = %q, want %q", seen.Scopes, tc.wantScopes)
			}
		})
	}
}

// Everything that is not a token this deployment's authorization server issued
// FOR this deployment, and inside its validity window, is refused — and refused
// identically, so a caller learns whether to obtain a new token and nothing
// about which check turned it away.
//
// Each row carries exactly one fault, so the answer it earns is the one that
// fault pins. The token the valid control above presents is the same shape as
// every row here in every respect but the one named.
//
// Planted defect, demonstrated: drop AnyAudience from the claim validation in
// principalFor and the wrong-audience row reads back 200.
func TestEveryOtherCredentialIsRefusedWithTheSameAnswer(t *testing.T) {
	issuer := newAuthorizationServerDouble(t)
	f := newTokenFixture(t, transportFor(issuer.authorizationConfig(), issuer.egress()))
	strangerKey := newSigningKey(t)

	for _, tc := range []struct {
		name  string
		token func(t *testing.T) string
	}{
		{
			"an audience that is not this deployment",
			func(t *testing.T) string {
				return issuer.mint(t, tokenClaimSet{audience: []string{"https://elsewhere.example.test/mcp"}})
			},
		},
		{
			"an issuer that is not the one this deployment was configured with",
			func(t *testing.T) string {
				return issuer.mint(t, tokenClaimSet{issuer: "https://other.example.test"})
			},
		},
		{
			"an expiry in the past",
			func(t *testing.T) string {
				return issuer.mint(t, tokenClaimSet{expiry: time.Now().Add(-time.Hour)})
			},
		},
		{
			"a validity that has not begun",
			func(t *testing.T) string {
				return issuer.mint(t, tokenClaimSet{
					notBefore: time.Now().Add(time.Hour),
					expiry:    time.Now().Add(2 * time.Hour),
				})
			},
		},
		{
			"no expiry at all, so it would never stop being usable",
			func(t *testing.T) string {
				return mintWithoutExpiry(t, issuer)
			},
		},
		{
			"no signature, the algorithm declared as none",
			func(t *testing.T) string {
				return unsignedToken(t, issuer.issuer())
			},
		},
		{
			"a signature from a key the published set does not carry",
			func(t *testing.T) string {
				return issuer.mintSignedBy(t, tokenClaimSet{}, strangerKey, issuer.keyID)
			},
		},
		{
			"a key identifier the authorization server has never published",
			func(t *testing.T) string {
				return issuer.mintSignedBy(t, tokenClaimSet{}, strangerKey, "a-key-nobody-published")
			},
		},
		{
			"a token declaring a type this server does not accept",
			func(t *testing.T) string {
				return issuer.mint(t, tokenClaimSet{tokenType: aTypeThisServerDoesNotAccept})
			},
		},
		{
			"a credential that is not a token at all",
			func(t *testing.T) string { return "not.a.token" },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := tc.token(t)
			got := f.call(t, bearer(token))
			wantRefusedCredential(t, got, token, authErrorInvalidToken, http.StatusUnauthorized)
		})
	}

	t.Run("a token carried in the address rather than in the header", func(t *testing.T) {
		token := issuer.mint(t, tokenClaimSet{})
		got := f.callAt(t, endpointPath+"?"+queryTokenParameter+"="+token, "")
		wantRefusedCredential(t, got, token, authErrorInvalidRequest, http.StatusBadRequest)
	})

	t.Run("an authorization header that is not a bearer credential", func(t *testing.T) {
		got := f.call(t, "Basic c29tZXRoaW5nOmVsc2U=")
		wantRefusedCredential(t, got, "c29tZXRoaW5nOmVsc2U=", authErrorInvalidRequest, http.StatusBadRequest)
	})

	t.Run("no credential at all", func(t *testing.T) {
		got := f.call(t, "")
		if got.status != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 (body %q)", got.status, got.raw)
		}
		_, params := parseChallenge(t, got.header.Get(headerAuthenticate))
		if _, named := params["error"]; named {
			t.Errorf("the challenge names a reason (%q) for a caller that presented nothing to be wrong about",
				params["error"])
		}
		if params["resource_metadata"] != testMetadataAddress {
			t.Errorf("resource_metadata = %q, want %q", params["resource_metadata"], testMetadataAddress)
		}
	})

	// The last thing this proof reads is the whole error stream, once: no part
	// of any token above reached it. Reading it here rather than per row is
	// deliberate — a token leaked while answering one row would be in this
	// stream whichever row put it there.
	wantNoTokenInTheLog(t, f)
}

// wantRefusedCredential asserts one refusal: the status, the reason named on
// the challenge, an envelope rather than a bare status, and — the property that
// matters most — that nothing of what the caller presented came back.
func wantRefusedCredential(t *testing.T, got answer, token, wantReason string, wantStatus int) {
	t.Helper()
	if got.status != wantStatus {
		t.Fatalf("status = %d, want %d (body %q)", got.status, wantStatus, got.raw)
	}
	scheme, params := parseChallenge(t, got.header.Get(headerAuthenticate))
	if scheme != bearerScheme {
		t.Errorf("the challenge scheme is %q, want %q", scheme, bearerScheme)
	}
	if params["error"] != wantReason {
		t.Errorf("error = %q, want %q", params["error"], wantReason)
	}
	if params["resource_metadata"] != testMetadataAddress {
		t.Errorf("resource_metadata = %q, want %q", params["resource_metadata"], testMetadataAddress)
	}
	if got.frame.Error == nil {
		t.Errorf("the refusal carries no error envelope: %q", got.raw)
	}
	wantTokenAbsent(t, got, token)
}

// wantTokenAbsent asserts that neither the whole token nor any of its three
// parts appears in the body or in any response header.
//
// The parts are checked separately because a leak is as likely to be partial as
// whole — a header echoed, a payload decoded and reported — and a check for the
// whole token would pass over every one of those.
func wantTokenAbsent(t *testing.T, got answer, token string) {
	t.Helper()
	for _, part := range tokenParts(token) {
		if strings.Contains(got.raw, part) {
			t.Errorf("the response body carries %q of what was presented: %q", part, got.raw)
		}
		for name, values := range got.header {
			for _, value := range values {
				if strings.Contains(value, part) {
					t.Errorf("the %s header carries %q of what was presented: %q", name, part, value)
				}
			}
		}
	}
}

// wantNoTokenInTheLog asserts that nothing the serving process wrote to its
// error stream carries any part of any token, and that the one thing about a
// caller it does write is written only where it belongs.
//
// It reads the stream itself rather than trusting that nothing was logged: a
// deployment that logged a refused credential would look identical from outside
// and would put every caller's token into whatever collects that stream.
//
// The subject is held differently from the token, and the difference is what
// each one is. A token is the credential, and nothing anywhere may carry it. A
// subject is WHO the request was on behalf of, which the record of a request has
// to say and which the server compares to nothing — so it is held to appearing
// only as that field's value rather than being forbidden outright. A subject
// reaching any other part of a line, a refusal sentence above all, is still
// something written down that nothing decided to write, and that is what the
// count below catches.
func wantNoTokenInTheLog(t *testing.T, f *tokenFixture) {
	t.Helper()
	written := f.log.String()
	if written == "" {
		return
	}
	for _, suspicious := range []string{"eyJ", bearerScheme + " "} {
		if strings.Contains(written, suspicious) {
			t.Errorf("the error stream carries %q, so something of a credential was written down: %q",
				suspicious, written)
		}
	}
	asTheSubject := strings.Count(written, `"subject":"`+testSubject+`"`)
	if anywhere := strings.Count(written, testSubject); anywhere != asTheSubject {
		t.Errorf("the error stream names %q %d times and records it as the subject %d times, so it "+
			"reached somewhere other than the one field that carries it: %q",
			testSubject, anywhere, asTheSubject, written)
	}
}

// tokenParts is the whole token plus each of its three parts, for a leak check
// that a partial echo does not slip past. A part shorter than a few characters
// is skipped: it would match by coincidence rather than by leak.
func tokenParts(token string) []string {
	out := []string{token}
	for _, part := range strings.Split(token, ".") {
		if len(part) >= 8 {
			out = append(out, part)
		}
	}
	return out
}

// aTypeThisServerDoesNotAccept is a token type shaped the way a vendor-specific
// one is shaped and belonging to nobody.
//
// It is invented rather than copied from any real authorization server. A real
// vendor's private spelling written down here would read as a value this server
// holds an opinion about, and it holds none: what it accepts is the four forms
// the proof below pins and nothing else, whoever minted the token.
const aTypeThisServerDoesNotAccept = "application/example-private+jwt"

// The accepted token types are exactly four, and a fifth form is refused like
// every other token this deployment will not take.
//
// Each accepted form is its own row and each reads the tool list back, because a
// status alone would be satisfied by a deployment that answered 200 and served
// nothing. The set is fixed in the source and there is no setting that widens
// it, which is why no row here configures anything.
//
// Every row's token carries the read scope, the grant a well-formed token for
// this server ordinarily carries. The list read back is the signal that the
// token was admitted: the list names what this server carries whatever a grant
// reaches, and this file is about whether a token is admitted, not about what a
// grant reaches.
//
// Planted defect, demonstrated: drop JWT from acceptedTokenTypes and the JWT row
// reads back 401.
func TestTheAcceptedTokenTypesAreExactlyFourAndNothingElse(t *testing.T) {
	issuer := newAuthorizationServerDouble(t)
	f := newTokenFixture(t, transportFor(issuer.authorizationConfig(), issuer.egress()))

	for _, tc := range []struct {
		name      string
		tokenType string
	}{
		{"the short form an access token is registered under", "at+jwt"},
		{"the media-type form of that same registration", "application/at+jwt"},
		{"the bare form, which says only that the token is a JWT", "JWT"},
		{"no declared type at all", ""},
	} {
		t.Run("admitted: "+tc.name, func(t *testing.T) {
			wantReachedTheToolSurface(t, f.call(t,
				bearer(issuer.mint(t, tokenClaimSet{tokenType: tc.tokenType, scope: scopeRead}))))
		})
	}

	t.Run("refused: a type outside the accepted four", func(t *testing.T) {
		token := issuer.mint(t, tokenClaimSet{tokenType: aTypeThisServerDoesNotAccept})
		got := f.call(t, bearer(token))
		wantRefusedCredential(t, got, token, authErrorInvalidToken, http.StatusUnauthorized)
	})

	// The audience check is what accepting the bare form leans on: that form is
	// what an identity token also declares, so what separates one from an access
	// token here is who the token was issued for. It carries a row of its own in
	// this proof rather than only in the refusal table, because a change that
	// widened the accepted types and weakened this check at the same time would
	// leave that table green while an identity token minted for somebody else
	// reached the tool surface.
	t.Run("refused: an accepted type, issued for another deployment", func(t *testing.T) {
		token := issuer.mint(t, tokenClaimSet{
			tokenType: "JWT",
			audience:  []string{"https://elsewhere.example.test/mcp"},
		})
		got := f.call(t, bearer(token))
		wantRefusedCredential(t, got, token, authErrorInvalidToken, http.StatusUnauthorized)
	})

	wantNoTokenInTheLog(t, f)
}

// A refused token leaves the operator the name of the check that refused it, and
// leaves the caller exactly what every other refusal leaves it.
//
// The two halves are asserted together, per row, and that pairing is the whole
// design of this proof. Apart they drift into agreeing: a reason threaded out of
// the validator and into the caller's answer would satisfy an
// operator-stream assertion perfectly, and a refusal that told the caller
// nothing would satisfy an answer assertion perfectly while the operator stream
// stayed empty. Read together, each half is the other's control.
//
// Planted defect, demonstrated for the answer half: put a reason into the
// refusal's message and every row after the first reddens on the body
// comparison.
func TestARefusedTokenNamesTheCheckForTheOperatorAndNothingForTheCaller(t *testing.T) {
	issuer := newAuthorizationServerDouble(t)
	f := newTokenFixture(t, transportFor(issuer.authorizationConfig(), issuer.egress()))
	strangerKey := newSigningKey(t)

	// The reference answer every row below is compared against, byte for byte.
	// It is taken from the first row rather than written out here, so what is
	// asserted is that the rows agree with each other rather than that they each
	// agree with a sentence this proof could have kept in step by hand.
	var reference *answer

	for _, tc := range []struct {
		name   string
		token  func(t *testing.T) string
		reason refusalReason
	}{
		{
			"an audience that is not this deployment",
			func(t *testing.T) string {
				return issuer.mint(t, tokenClaimSet{audience: []string{"https://elsewhere.example.test/mcp"}})
			},
			reasonWrongAudience,
		},
		{
			"an issuer that is not the one this deployment was configured with",
			func(t *testing.T) string { return issuer.mint(t, tokenClaimSet{issuer: "https://other.example.test"}) },
			reasonWrongIssuer,
		},
		{
			"an expiry in the past",
			func(t *testing.T) string {
				return issuer.mint(t, tokenClaimSet{expiry: time.Now().Add(-time.Hour)})
			},
			reasonExpired,
		},
		{
			"a validity that has not begun",
			func(t *testing.T) string {
				return issuer.mint(t, tokenClaimSet{
					notBefore: time.Now().Add(time.Hour),
					expiry:    time.Now().Add(2 * time.Hour),
				})
			},
			reasonNotYetValid,
		},
		{
			"no expiry at all",
			func(t *testing.T) string { return mintWithoutExpiry(t, issuer) },
			reasonNoExpiry,
		},
		{
			"nothing about who presented it",
			func(t *testing.T) string { return issuer.mint(t, tokenClaimSet{noSubject: true}) },
			reasonNoSubject,
		},
		{
			"a type outside the accepted four",
			func(t *testing.T) string {
				return issuer.mint(t, tokenClaimSet{tokenType: aTypeThisServerDoesNotAccept})
			},
			reasonTokenType,
		},
		{
			"a signature from a key the published set does not carry",
			func(t *testing.T) string { return issuer.mintSignedBy(t, tokenClaimSet{}, strangerKey, issuer.keyID) },
			reasonSignature,
		},
		{
			"a key identifier the authorization server has never published",
			func(t *testing.T) string {
				return issuer.mintSignedBy(t, tokenClaimSet{}, strangerKey, "a-key-nobody-published")
			},
			reasonUnknownKey,
		},
		{
			"no signature, the algorithm declared as none",
			func(t *testing.T) string { return unsignedToken(t, issuer.issuer()) },
			reasonNotAToken,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			from := f.log.mark()
			token := tc.token(t)
			got := f.call(t, bearer(token))

			wantRefusedCredential(t, got, token, authErrorInvalidToken, http.StatusUnauthorized)
			wantOperatorToldWhichCheck(t, f.log.since(from), tc.reason)

			if reference == nil {
				reference = &got
				return
			}
			wantSameAnswerAs(t, got, *reference)
		})
	}

	// The two refusals of how a credential was presented rather than of what it
	// said. They earn a different answer on purpose — the request was malformed,
	// not the token — so they are read here against their own expectations and
	// are deliberately not in the byte-identical set above.
	t.Run("a token carried in the address rather than in the header", func(t *testing.T) {
		from := f.log.mark()
		token := issuer.mint(t, tokenClaimSet{})
		got := f.callAt(t, endpointPath+"?"+queryTokenParameter+"="+token, "")
		wantRefusedCredential(t, got, token, authErrorInvalidRequest, http.StatusBadRequest)
		wantOperatorToldWhichCheck(t, f.log.since(from), reasonTokenInTheAddress)
	})

	t.Run("an authorization header that is not a bearer credential", func(t *testing.T) {
		from := f.log.mark()
		got := f.call(t, "Basic c29tZXRoaW5nOmVsc2U=")
		wantRefusedCredential(t, got, "c29tZXRoaW5nOmVsc2U=", authErrorInvalidRequest, http.StatusBadRequest)
		wantOperatorToldWhichCheck(t, f.log.since(from), reasonNotABearerHeader)
	})

	// A caller that presented nothing is not a refusal worth a line, and the
	// silence is asserted rather than left to chance. Being challenged is how a
	// client discovers where to obtain a token, so it is the first thing every
	// well-behaved client does: a line for each of those would bury the refusals
	// that mean something under the ones that mean a client is starting up, and
	// would hand anything that can reach the endpoint an unbounded write to
	// whatever collects this stream.
	t.Run("no credential at all writes nothing", func(t *testing.T) {
		from := f.log.mark()
		if got := f.call(t, ""); got.status != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 (body %q)", got.status, got.raw)
		}
		if written := f.log.since(from); written != "" {
			t.Errorf("a request presenting no credential wrote %q to the error stream", written)
		}
	})

	// Clause by clause the same absence the refusal table asserts, re-read here
	// because this proof deliberately writes to the stream that absence is about.
	wantNoTokenInTheLog(t, f)
}

// wantOperatorToldWhichCheck asserts that what the serving process wrote while
// one row ran names the check that refused it, and that it is one line.
func wantOperatorToldWhichCheck(t *testing.T, written string, reason refusalReason) {
	t.Helper()
	if !strings.Contains(written, string(reason)) {
		t.Errorf("the error stream does not name the check that refused this token.\n got: %q\nwant it to carry: %q",
			written, string(reason))
	}
	if lines := strings.Count(strings.TrimSuffix(written, "\n"), "\n"); lines != 0 {
		t.Errorf("one refused request wrote %d lines, want 1: %q", lines+1, written)
	}
}

// wantSameAnswerAs asserts that two refusals are indistinguishable to whoever
// received them: the same status, the same challenge, and the same body byte for
// byte.
//
// This is the half that stops the operator-facing reason leaking. A reason that
// reached the caller would have to differ between rows, because the rows differ
// in which check refused them — so rows that stay identical are rows where
// nothing specific to the check got out.
func wantSameAnswerAs(t *testing.T, got, reference answer) {
	t.Helper()
	if got.status != reference.status {
		t.Errorf("status = %d, want %d: this refusal is distinguishable from the others", got.status, reference.status)
	}
	if got.header.Get(headerAuthenticate) != reference.header.Get(headerAuthenticate) {
		t.Errorf("challenge = %q, want %q: this refusal names something the others do not",
			got.header.Get(headerAuthenticate), reference.header.Get(headerAuthenticate))
	}
	if got.raw != reference.raw {
		t.Errorf("body = %q, want %q: this refusal tells the caller which check turned it away",
			got.raw, reference.raw)
	}
}

// mintWithoutExpiry produces a token carrying no expiry claim at all, which the
// double's own minting cannot do because it fills one in.
func mintWithoutExpiry(t *testing.T, d *authorizationServerDouble) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.ES256, Key: d.signingKey},
		(&jose.SignerOptions{}).WithHeader("kid", d.keyID),
	)
	if err != nil {
		t.Fatalf("build a signer: %v", err)
	}
	raw, err := jwt.Signed(signer).Claims(map[string]any{
		"iss": d.issuer(),
		"sub": testSubject,
		"aud": testResource,
	}).Serialize()
	if err != nil {
		t.Fatalf("mint a token: %v", err)
	}
	return raw
}

// unsignedToken composes the compact form of a token declaring its algorithm to
// be none and carrying an empty signature — the classic forgery, written by
// hand because no signer will produce it.
func unsignedToken(t *testing.T, issuer string) string {
	t.Helper()
	encode := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("compose an unsigned token: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	header := encode(map[string]any{"alg": "none", "typ": "at+jwt"})
	payload := encode(map[string]any{
		"iss": issuer,
		"sub": testSubject,
		"aud": testResource,
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	return header + "." + payload + "."
}

// transportFor is the deployment shape these proofs run as: the caps every
// other proof in this package uses, with the authorization and the egress gate
// the row supplies.
func transportFor(authorization AuthorizationConfig, gate NetworkEgress) HTTPConfig {
	cfg := defaultTransportConfig()
	cfg.Authorization = authorization
	cfg.Egress = gate
	return cfg
}

// A token naming a key the held set does not carry makes this deployment read
// the set again, once, and a second such token inside the interval makes it
// read nothing.
//
// The counts are read off the authorization server, which counts the requests
// that actually reached it, rather than off anything inside the deployment: a
// cache that believed it had refreshed without a request having been made would
// satisfy an internal counter and fail here.
func TestAnUnknownKeyIdentifierCostsOneReadAndThenNoMore(t *testing.T) {
	issuer := newAuthorizationServerDouble(t)
	f := newTokenFixture(t, transportFor(issuer.authorizationConfig(), issuer.egress()))
	stranger := newSigningKey(t)

	// Nothing has been read yet: the handler consults the gate while it is
	// built and asks the authorization server nothing until a token arrives.
	if got := issuer.keySetReads.Load(); got != 0 {
		t.Fatalf("the key set was read %d times before any caller presented anything", got)
	}

	if got := f.call(t, bearer(issuer.mint(t, tokenClaimSet{}))); got.status != http.StatusOK {
		t.Fatalf("the valid token was refused: status = %d (body %q)", got.status, got.raw)
	}
	afterFirst := issuer.keySetReads.Load()
	if afterFirst != 1 {
		t.Fatalf("the key set was read %d times to verify the first token, want 1", afterFirst)
	}

	unknown := issuer.mintSignedBy(t, tokenClaimSet{}, stranger, "a-key-nobody-published")
	if got := f.call(t, bearer(unknown)); got.status != http.StatusUnauthorized {
		t.Fatalf("a token naming an unpublished key was answered %d, want 401 (body %q)", got.status, got.raw)
	}
	afterUnknown := issuer.keySetReads.Load()
	if afterUnknown != afterFirst+1 {
		t.Errorf("a token naming a key the held set does not carry moved the read count from %d to %d, "+
			"want exactly one more read", afterFirst, afterUnknown)
	}

	second := issuer.mintSignedBy(t, tokenClaimSet{}, stranger, "another-key-nobody-published")
	if got := f.call(t, bearer(second)); got.status != http.StatusUnauthorized {
		t.Fatalf("a second token naming an unpublished key was answered %d, want 401 (body %q)",
			got.status, got.raw)
	}
	if after := issuer.keySetReads.Load(); after != afterUnknown {
		t.Errorf("a second unknown key identifier inside the interval moved the read count from %d to %d, "+
			"so a caller inventing key identifiers drives one read per request", afterUnknown, after)
	}
}

// A deployment that reads its keys from a file reaches the network exactly
// never, and a workspace that permits no outbound request and names no file is
// refused before anything binds.
//
// The gate is what both halves are read off. It is the one thing that can tell
// "did not dial" from "dialled and the double happened not to be asked", and it
// is supplied by the caller rather than being a default inside the server, so a
// proof can count what was asked of it.
func TestAKeySetFileIsTheWholeSourceAndAnOfflineDeploymentNeedsOne(t *testing.T) {
	issuer := newAuthorizationServerDouble(t)

	t.Run("with a file, tokens verify and the gate is never asked", func(t *testing.T) {
		gate := &countingEgress{}
		authorization := issuer.authorizationConfig()
		authorization.JWKSFile = issuer.keySetFile(t)
		f := newTokenFixture(t, transportFor(authorization, gate.gate()))

		got := f.call(t, bearer(issuer.mint(t, tokenClaimSet{})))
		if got.status != http.StatusOK {
			t.Fatalf("a token verifiable against the file was refused: status = %d (body %q)", got.status, got.raw)
		}
		if asked := gate.asked.Load(); asked != 0 {
			t.Errorf("the network gate was asked %d times, want 0: a deployment reading its keys from a "+
				"file has no reason to reach the network at all", asked)
		}
		if reads := issuer.metadataReads.Load() + issuer.keySetReads.Load(); reads != 0 {
			t.Errorf("the authorization server was asked for %d documents, want 0", reads)
		}
	})

	t.Run("a workspace that permits no outbound request and names no file is refused", func(t *testing.T) {
		offline := errors.New("network access refused because the workspace is offline")
		address, path := socketAddress(t)
		transport := transportFor(issuer.authorizationConfig(), refusingEgress(offline))

		ctx, endRun := context.WithCancel(context.Background())
		t.Cleanup(endRun)
		launched := make(chan error, 1)
		go func() {
			launched <- ListenAndServe(ctx,
				Config{WorkspaceRoot: rootedWorkspace(t), Stderr: io.Discard},
				transport, ListenConfig{Address: address})
		}()

		var err error
		select {
		case err = <-launched:
		case <-time.After(runReturnCeiling):
			t.Fatal("the launch neither refused nor returned within the liveness ceiling, so it is serving " +
				"for a deployment that can verify nothing")
		}
		if err == nil {
			t.Fatal("the listener served for a deployment with nowhere to read its verification keys from")
		}
		if !strings.Contains(err.Error(), "mcp.authorization.jwks_file") {
			t.Errorf("the refusal does not name mcp.authorization.jwks_file, so an operator cannot act on "+
				"it; it reads %q", err)
		}
		if !strings.Contains(err.Error(), offline.Error()) {
			t.Errorf("the refusal drops what the gate said, so a reader is not told why the network was "+
				"not an option; it reads %q", err)
		}
		if _, statErr := os.Stat(path); statErr == nil {
			t.Errorf("%s exists, so the address was bound before the refusal", path)
		}
	})

	t.Run("a workspace that permits no outbound request and names a file binds", func(t *testing.T) {
		authorization := issuer.authorizationConfig()
		authorization.JWKSFile = issuer.keySetFile(t)
		transport := transportFor(authorization, refusingEgress(errors.New("the workspace is offline")))

		address, path := socketAddress(t)
		ctx, endRun := context.WithCancel(context.Background())
		t.Cleanup(endRun)
		served := make(chan error, 1)
		go func() {
			served <- ListenAndServe(ctx,
				Config{WorkspaceRoot: rootedWorkspace(t), Stderr: io.Discard},
				transport, ListenConfig{Address: address})
		}()
		awaitSocket(t, path, served)
		endRun()
		select {
		case err := <-served:
			if err != nil {
				t.Errorf("serving returned %v, want a clean end", err)
			}
		case <-time.After(runReturnCeiling):
			t.Fatal("the listener never stopped serving within the liveness ceiling")
		}
	})

	t.Run("a file that holds no key set is refused when the handler is built", func(t *testing.T) {
		authorization := issuer.authorizationConfig()
		authorization.JWKSFile = writeKeySetFile(t, jose.JSONWebKeySet{})
		_, err := newHTTPHandler(context.Background(), newCommandToolServer(t, nil),
			transportFor(authorization, nil))
		if err == nil {
			t.Fatal("a handler was built over a key set with no keys in it, so every caller would be " +
				"refused by a deployment that looks configured")
		}
		if !strings.Contains(err.Error(), "mcp.authorization.jwks_file") {
			t.Errorf("the refusal does not name the setting that produced it; it reads %q", err)
		}
	})
}

// An authorization server that states an identifier other than the one this
// deployment was configured with is not the one that was configured, and its
// keys are not read.
//
// The check matters because the address the keys are fetched from is whatever
// that document names: a document served at the configured server's well-known
// address that pointed at another party's keys would otherwise let that party
// mint tokens this deployment accepts.
func TestAnAuthorizationServerStatingAnotherIdentifierIsNotTheOneConfigured(t *testing.T) {
	var keySetReads atomic.Int64
	mux := http.NewServeMux()
	var base string
	mux.HandleFunc(wellKnownAuthorizationServer, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":   "https://somebody.else.example.test",
			"jwks_uri": base + keySetPath,
		})
	})
	mux.HandleFunc(keySetPath, func(w http.ResponseWriter, _ *http.Request) {
		keySetReads.Add(1)
		writeJSON(w, jose.JSONWebKeySet{})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	base = ts.URL

	honest := newAuthorizationServerDouble(t)
	f := newTokenFixture(t, transportFor(
		AuthorizationConfig{Issuer: ts.URL, Resource: testResource},
		func() (*http.Client, error) { return ts.Client(), nil },
	))

	got := f.call(t, bearer(honest.mint(t, tokenClaimSet{issuer: ts.URL})))
	if got.status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503: the keys could not be read, which is not the caller's token "+
			"being wrong (body %q)", got.status, got.raw)
	}
	if challenge := got.header.Get(headerAuthenticate); challenge != "" {
		t.Errorf("the answer carries the challenge %q, so a caller holding a perfectly good token is sent "+
			"to obtain another one", challenge)
	}
	if reads := keySetReads.Load(); reads != 0 {
		t.Errorf("the key set was read %d times from a server that is not the one configured, want 0", reads)
	}
}
