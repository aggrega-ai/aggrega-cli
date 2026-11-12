// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// wellKnownProtectedResource is where a client looks for what a deployment
// publishes about itself before it holds a token.
//
// It is not the whole path. A deployment publishes an address for itself, and
// that address has a path of its own; this one is inserted between that
// address's host and its path. So a deployment published as
// https://example.test/mcp answers the document at
// https://example.test/.well-known/oauth-protected-resource/mcp, while one
// published at the root of a host answers it at this path alone. A client asks
// for the inserted form first and falls back to the bare form, which is why a
// deployment with a path answers at both.
const wellKnownProtectedResource = "/.well-known/oauth-protected-resource"

// The scopes this deployment publishes as the ones an authorization server may
// issue for it.
//
// The document below is how a client discovers what it may ask for; what each
// one admits is declared where the classes they admit are, beside the mapping
// that reads them, so a reader meets the scope names here and their meaning
// exactly once. The one on the challenge a caller with no credential receives is
// the scope it needs before anything else, which is why it is the one named
// there.
const (
	scopeRead           = "aggrega:read"
	scopeWorkspaceWrite = "aggrega:workspace-write"
	scopeRegistryWrite  = "aggrega:registry-write"
)

// publishedScopes is the scopes above as the document below lists them, in that
// order.
//
// It is one list because two readers ask it the same question — which scopes
// exist here — and must not answer it differently: the document a client reads
// before it holds a token, and the listing, which carries every tool some one of
// these scopes admits. A scope added to one and not the other would publish a
// scope whose tools no listing names, or list tools behind a scope no client is
// told to ask for.
func publishedScopes() []string {
	return []string{scopeRead, scopeWorkspaceWrite, scopeRegistryWrite}
}

// bearerMethodHeader is the one way this deployment accepts a token: on the
// request's authorization header. A token in a query string is written into
// proxy logs and browser history by everything it passes through, so no other
// method is published.
const bearerMethodHeader = "header"

// metadataCacheControl lets any cache keep the document, because every caller
// receives the same bytes and none of it is secret — it exists to be read by a
// client that has no credential yet.
//
// An hour is the freshness: the document changes only when a deployment's
// configuration changes and the process is restarted, so it is long enough that
// a client is not refetching on every connection, and short enough that a
// reconfigured deployment is picked up without anyone clearing a cache.
const metadataCacheControl = "public, max-age=3600"

// AuthorizationConfig carries what a deployment declares about the bearer
// tokens its callers present.
//
// Issuer and Resource are required for a listener, and none of it holds secret
// material: a token is presented by a caller on the request it makes, never
// configured here, and the key set names public verification keys rather than
// anything that signs. The two addresses are the whole of what an HTTP caller
// is told before it presents anything — where to obtain a token, and what this
// deployment is called as the resource one is obtained for.
type AuthorizationConfig struct {
	// Issuer is the authorization server that issues the tokens this
	// deployment accepts, as that server states its own issuer identifier.
	//
	// It is an https address, or a plain http one on this machine: a listener
	// is refused before it binds where it is plain http on any other host.
	Issuer string

	// Resource is the address this deployment publishes for itself, as a caller
	// reaches it. It is what a client hands the authorization server when it
	// asks for a token for this deployment, and the path the document below is
	// served at is derived from it.
	Resource string

	// JWKSFile is a file holding the key set tokens are verified against.
	// Empty reads that set from the authorization server instead, which is
	// what a deployment whose workspace reaches the network does.
	JWKSFile string

	// TrustedProxy carries the headers this deployment reads its caller's
	// identity from where a proxy in front has already validated the token.
	// Its zero value is a deployment that validates tokens itself, and with
	// it set no token is read at all and no key set is fetched.
	TrustedProxy TrustedProxyConfig
}

// NetworkEgress yields the client an outbound request is made with, or the
// refusal a workspace that permits no outbound request answers with.
//
// It is supplied by the command layer rather than read here, so the decision
// about whether this workspace reaches the network is taken in the one place
// that owns it and is not read a second time from inside the server.
type NetworkEgress func() (*http.Client, error)

// principal is who a request is on behalf of, as the credential it carried
// asserted.
//
// Nothing here is interpreted. The subject is recorded and compared to
// nothing: what a principal is called is the authorization server's business
// and this server has no notion of one caller being another.
//
// The scopes are the one field an answer turns on, and what they decide is not
// who the caller is but how far a call may reach: each maps to a fixed set of
// reach classes, and a call to a tool whose operation declares a class outside
// that set is refused, naming the scope that would admit it. They bound calls
// and never the tool list, which names that tool all the same. The mapping is
// fixed rather than interpreted, which is the same rule the subject is held to —
// this server evaluates nothing about a principal as meaning.
type principal struct {
	// Subject is the token's subject claim, which is required: a credential
	// asserting nothing about who presented it identifies nobody.
	Subject string

	// Issuer is the authorization server that issued the token, as the token
	// states it. It equals the configured issuer, because a token stating any
	// other is refused. It is empty where a proxy in front asserted this
	// principal instead: this server read no token there and so knows of no
	// issuer beyond what the deployment declared.
	Issuer string

	// ClientID is the client the token was issued to, where the token names
	// one. Empty where it names none.
	ClientID string

	// Scopes are what the token says its bearer may ask for, in the order the
	// token listed them.
	Scopes []string
}

// principalKey is the request-context key the principal travels under. It is
// an unexported type so nothing outside this package can read or write the
// value, and so it cannot collide with another package's key.
type principalKey struct{}

// withPrincipal returns r carrying who it is on behalf of, so everything
// answered after the credential check can read what that credential asserted.
func withPrincipal(r *http.Request, p *principal) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), principalKey{}, p))
}

// principalFrom reads who a request is on behalf of, or nil where the request
// reached here without passing a credential check — which on this transport is
// only a request for what the deployment publishes about itself.
func principalFrom(ctx context.Context) *principal {
	p, _ := ctx.Value(principalKey{}).(*principal)
	return p
}

// protectedResource is what this deployment publishes about itself, composed
// once from the configuration and then only read.
//
// Composing it once is what makes every answer consistent: the document a
// client fetches and the document the challenge points it at are the same
// bytes and the same address, rather than two compositions that agree today.
type protectedResource struct {
	// document is the metadata document's bytes, marshalled once. Field order
	// is the declaration order of the type below, so the bytes are the same on
	// every request and for every caller.
	document []byte

	// paths are the local paths the document is answered at: the inserted form,
	// and the bare well-known path where the deployment's own address has a path
	// to insert before. A deployment published at the root of a host has one.
	paths []string

	// documentAddress is the address the document is published at, as a caller
	// reaches it. Every challenge this deployment composes names it, so a
	// challenge and the document a client then fetches cannot disagree.
	documentAddress string

	// challenge is the whole header value an unauthenticated caller receives,
	// naming the document's address and the scope a caller needs first.
	challenge string
}

// protectedResourceMetadata is the document a client reads before it holds a
// token: who issues tokens for this deployment, what this deployment is called
// as the resource they are issued for, what scopes exist, and how a token is
// presented.
type protectedResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	ScopesSupported        []string `json:"scopes_supported"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
}

// resolveProtectedResource settles every refusal a deployment can meet about
// what it declares itself to be, and composes what it publishes.
//
// It reads no file, makes no request and consults no clock. The one thing it
// asks outside the configuration it is given is the system resolver, and only
// for the name localhost, which is how an issuer on this machine is told from
// one elsewhere — the same question, asked the same way, as for a listen
// address naming that name. That is what lets it be called before anything is
// built and again while the handler is being built, which is how the refusal is
// made before the expensive part of a launch without the handler having to
// trust that its caller made it.
//
// An issuer that is plain http on another machine is refused here, whatever
// the listener reads its keys and its callers from, because every listener
// publishes its issuer: the document composed below names it as where a client
// obtains its token, so a client following that document runs its
// authorization flow over the unprotected path to it — the authorization code
// and the token included — whether or not this process ever reads a key from
// it. Where this process does read its keys from it, whoever sits on that path
// could also answer with keys of their own choosing.
func resolveProtectedResource(cfg AuthorizationConfig) (protectedResource, error) {
	var missing []string
	if cfg.Issuer == "" {
		missing = append(missing, "mcp.authorization.issuer")
	}
	if cfg.Resource == "" {
		missing = append(missing, "mcp.authorization.resource")
	}
	if len(missing) > 0 {
		return protectedResource{}, fmt.Errorf(
			"a listener answers every caller with a bearer-token challenge, so it has to say which "+
				"authorization server issues those tokens and what this deployment is called as the "+
				"resource they are issued for: set %s", strings.Join(missing, " and "))
	}

	issuer, err := parseDeclaredAddress(cfg.Issuer, "mcp.authorization.issuer",
		"https://login.example.com")
	if err != nil {
		return protectedResource{}, err
	}
	if plaintextOffLoopback(issuer) {
		return protectedResource{}, fmt.Errorf(
			"mcp.authorization.issuer is %q, a plain http address on another machine: a listener publishes its "+
				"issuer to every client as where to obtain a token, and reads its verification keys there unless it "+
				"has a key file or a proxy in front checks the tokens, so whoever is on the network path to it could "+
				"read a client's token or substitute the keys; set mcp.authorization.issuer to an https address, as "+
				"in https://login.example.com (plain http is accepted only for an authorization server on this "+
				"machine: localhost, 127.0.0.1 or ::1)", cfg.Issuer)
	}
	resource, err := parseDeclaredAddress(cfg.Resource, "mcp.authorization.resource",
		"https://mcp.example.com/mcp")
	if err != nil {
		return protectedResource{}, err
	}

	// The trailing slash goes first so a deployment published as ".../mcp/" and
	// one published as ".../mcp" answer at the same path rather than at two that
	// differ by a character no reader would think mattered.
	documentPath := wellKnownProtectedResource + strings.TrimSuffix(resource.Path, "/")
	paths := []string{documentPath}
	if documentPath != wellKnownProtectedResource {
		paths = append(paths, wellKnownProtectedResource)
	}

	// The address in the challenge is derived from what the deployment declared
	// and never from the request's own host header: a caller supplies that
	// header, and an address built from it is one this deployment never
	// published and never serves.
	documentAddress := *resource
	documentAddress.Path = documentPath
	documentAddress.RawPath = ""
	documentAddress.RawQuery = ""
	documentAddress.Fragment = ""
	documentAddress.RawFragment = ""

	document, err := json.Marshal(protectedResourceMetadata{
		// What the operator wrote, rather than what parsing made of it: this is
		// the identifier a client and an authorization server both compare
		// against, so a normalisation here would publish something nobody typed.
		Resource:               cfg.Resource,
		AuthorizationServers:   []string{cfg.Issuer},
		ScopesSupported:        publishedScopes(),
		BearerMethodsSupported: []string{bearerMethodHeader},
	})
	if err != nil {
		return protectedResource{}, fmt.Errorf("compose what this deployment publishes about itself: %w", err)
	}

	pr := protectedResource{
		document:        document,
		paths:           paths,
		documentAddress: documentAddress.String(),
	}
	pr.challenge = pr.challengeWith(scopeRead, "")
	return pr, nil
}

// parseDeclaredAddress reads one address a deployment declared, and refuses one
// that could not be reached.
//
// A value with no scheme and host is the shape a reader writes by habit — a
// bare host, or a host and a path — and it cannot be published: a client
// fetches what is published here, and an authorization server compares against
// it, so half an address is one neither of them can act on. The refusal names
// the key that produced it and one address of the right shape, because a reader
// meeting it is being asked to write something rather than to remove something.
func parseDeclaredAddress(raw, key, example string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s is not an address: %w", key, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf(
			"%s is %q, which carries no scheme and host; write the whole address, as in %s",
			key, raw, example)
	}
	return parsed, nil
}

// plaintextOffLoopback reports whether an address is plain http on a host other
// than this machine, which is the one shape an authorization server may not be
// reached at.
//
// It is the one test every refusal of such an address makes — the issuer at
// startup, the key address an authorization server's metadata names, and each
// redirect a read of either answers with — and it decides nothing about loopback
// itself. It takes the address's host, with the port and any brackets around an
// IPv6 literal stripped, and asks hostIsLoopback, the question a plaintext
// listen address is already held to. A second test of loopback written here for
// addresses would agree with that one until either of them moved.
//
// The scheme comparison ignores case, which a parsed address already does; it is
// spelled out so an address assembled by hand is read the same way.
func plaintextOffLoopback(address *url.URL) bool {
	return strings.EqualFold(address.Scheme, "http") && !hostIsLoopback(address.Hostname())
}

// The three reasons this deployment gives a caller for not accepting what it
// presented, spelled as the bearer scheme spells them.
//
// The first two are the whole vocabulary a caller whose CREDENTIAL was turned
// away receives: one says the credential was not a usable one, the other that it
// was not accepted. Neither says why in any finer detail, deliberately — a caller
// learns whether to obtain a new token, and an attacker learns nothing about
// which check refused it.
//
// The third is a different statement and belongs to a caller whose credential was
// accepted: what it asked for is outside what that credential carries. It is safe
// to be specific there in a way the first two are not, because the caller is
// already authenticated and the thing it is told — which scope admits what it
// asked for — is published in the document this deployment serves unauthenticated
// anyway.
const (
	authErrorInvalidRequest    = "invalid_request"
	authErrorInvalidToken      = "invalid_token"
	authErrorInsufficientScope = "insufficient_scope"
)

// servesMetadataAt reports whether a request for this path is a request for the
// document rather than for the protocol endpoint.
func (pr protectedResource) servesMetadataAt(path string) bool {
	return slices.Contains(pr.paths, path)
}

// challengeWith is the one composer of every challenge this deployment sends:
// the address of the document it publishes about itself, the scope this
// particular refusal asks the caller to hold, and the reason where there is one.
//
// The scope is a parameter because the two refusals that carry a challenge ask
// for different things. A caller with no usable credential is asked for the
// scope it needs before anything else; a caller whose credential did not admit
// what it asked for is asked for the one scope that would have. Composing both
// here is what keeps them pointing at the same document: the address is the one
// derived from what the deployment declared, and nothing else may build one.
//
// An empty reason composes the challenge a caller that presented nothing
// receives, which states no error because none has occurred beyond the credential
// being absent.
func (pr protectedResource) challengeWith(scope, reason string) string {
	challenge := `Bearer resource_metadata="` + pr.documentAddress + `", scope="` + scope + `"`
	if reason == "" {
		return challenge
	}
	return challenge + `, error="` + reason + `"`
}

// challengeFor is the challenge a caller receives when the CREDENTIAL it
// presented was not accepted, naming the reason beside the address and the scope
// a caller needs before anything else.
//
// The scope it names is the read scope rather than whatever the call would have
// needed, and that is right for this refusal: nothing here has read a credential
// successfully, so there is no admitted caller whose grant could be compared
// against anything, and the scope to obtain first is the one that admits reading.
func (pr protectedResource) challengeFor(reason string) string {
	return pr.challengeWith(scopeRead, reason)
}

// serveMetadata answers a request for what this deployment publishes about
// itself.
//
// It asks for no credential, and that is the point rather than an oversight: a
// client reads this document in order to find out where to obtain a token, so
// requiring one would close the only door into the deployment. Nothing here is
// secret — it is the issuer, the deployment's own published address, the scope
// names and how a token is presented, all of which a client is meant to know.
//
// Reading is the only thing this path does, so a request that is not a read is
// refused the way the protocol endpoint refuses a method it does not serve.
func (h *httpHandler) serveMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set(headerAllow, http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set(headerContentType, contentTypeJSON)
	w.Header().Set(headerCacheControl, metadataCacheControl)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(h.resource.document)
}
