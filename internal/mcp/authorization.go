// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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
// Publishing a scope says it exists and says nothing about what it admits: the
// document below is how a client discovers what it may ask for, and no request
// is admitted or refused on a scope. The one on the challenge is the scope a
// caller needs before anything else, which is why it is the one named there.
const (
	scopeRead           = "aggrega:read"
	scopeWorkspaceWrite = "aggrega:workspace-write"
	scopeRegistryWrite  = "aggrega:registry-write"
)

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
// and this server has no notion of one caller being another. The scopes are
// carried for the same reason a request's method is — something downstream
// reads them — and no answer on this transport turns on them yet.
type principal struct {
	// Subject is the token's subject claim, which is required: a credential
	// asserting nothing about who presented it identifies nobody.
	Subject string

	// Issuer is the authorization server that issued the token, as the token
	// states it. It equals the configured issuer, because a token stating any
	// other is refused.
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
// It reaches nothing outside the configuration it is given: no file, no
// network, no clock. That is what lets it be called before anything is built
// and again while the handler is being built, which is how the refusal is made
// before the expensive part of a launch without the handler having to trust
// that its caller made it.
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

	if _, err := parseDeclaredAddress(cfg.Issuer, "mcp.authorization.issuer",
		"https://login.example.com"); err != nil {
		return protectedResource{}, err
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
		ScopesSupported:        []string{scopeRead, scopeWorkspaceWrite, scopeRegistryWrite},
		BearerMethodsSupported: []string{bearerMethodHeader},
	})
	if err != nil {
		return protectedResource{}, fmt.Errorf("compose what this deployment publishes about itself: %w", err)
	}

	return protectedResource{
		document: document,
		paths:    paths,
		challenge: `Bearer resource_metadata="` + documentAddress.String() +
			`", scope="` + scopeRead + `"`,
	}, nil
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

// The two reasons this deployment gives a caller for not accepting what it
// presented, spelled as the bearer scheme spells them.
//
// They are the whole vocabulary a refused caller receives: one says the
// credential was not a usable one, the other that it was not accepted. Neither
// says why in any finer detail, deliberately — a caller learns whether to
// obtain a new token, and an attacker learns nothing about which check refused
// it.
const (
	authErrorInvalidRequest = "invalid_request"
	authErrorInvalidToken   = "invalid_token"
)

// servesMetadataAt reports whether a request for this path is a request for the
// document rather than for the protocol endpoint.
func (pr protectedResource) servesMetadataAt(path string) bool {
	return slices.Contains(pr.paths, path)
}

// challengeFor is the challenge a caller receives when what it presented was
// not accepted, naming the reason beside the address and scope every challenge
// carries.
//
// The reason is added to the same composed challenge a caller with nothing
// receives rather than to one built beside it, so every refusal on this
// endpoint points at the same document.
func (pr protectedResource) challengeFor(reason string) string {
	return pr.challenge + `, error="` + reason + `"`
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
