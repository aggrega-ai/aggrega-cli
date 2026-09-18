// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// TrustedProxyConfig carries the two headers a deployment reads its caller's
// identity from, where a proxy in front has already validated the token.
//
// It is an opt-in, and the whole of what makes it safe is that nothing but the
// proxy can reach the listener. A header is trustworthy exactly as far as the
// set of callers that can set it: on a loopback address or a Unix socket that
// set is whatever already runs on the host, and on an address the network
// reaches it is everyone, so a listener in this mode there would let every
// caller claim to be anyone. That is why an exposed address is a startup
// refusal rather than a weaker deployment.
//
// A non-empty PrincipalHeader is what selects the mode; there is no separate
// flag. The configuration schema requires that key wherever the block is
// present, so the two statements are the same statement.
type TrustedProxyConfig struct {
	// PrincipalHeader is the header the proxy puts the caller's identity in.
	// Its value is taken exactly as written and compared to nothing, on the
	// same terms as the subject of a token this server validates itself.
	PrincipalHeader string

	// ScopesHeader is the header the proxy puts the caller's granted scopes
	// in, separated by spaces. Empty asserts no scopes for any caller.
	ScopesHeader string
}

// configured reports whether this deployment reads its caller from a proxy in
// front rather than from a bearer token it validates itself.
func (p TrustedProxyConfig) configured() bool { return p.PrincipalHeader != "" }

// proxyAssertion admits a request carrying the identity a trusted proxy
// asserted for it, and refuses one that carries none.
//
// Nothing about a bearer token is read here — not the authorization header, not
// the address, not a key set, and no authorization server is contacted at
// startup or afterwards. The proxy in front did that, which is the entire
// reason a deployment opts into this: a request validated once should not be
// validated twice, and forwarding the caller's token inward so this server
// could check it again is the shape that was deliberately not built.
//
// The two configured headers are the only ones read. Reading any other — a
// forwarded-address header, say — would make a second thing trustable that the
// confinement below does not cover.
type proxyAssertion struct {
	// resource is what this deployment publishes about itself. A caller that
	// asserts nothing is pointed at the same document a caller with no token is
	// pointed at: it is still where a client finds out where to obtain one,
	// because the proxy in front is asking for a token the client has to hold.
	resource protectedResource

	principalHeader string
	scopesHeader    string
}

// authenticate reads who a request is on behalf of from the header the proxy
// sets.
//
// There is one refusal and it is the absence of that header, because the header
// is the whole credential in this mode: its value is not checked against
// anything, since what a caller is called is the proxy's business and this
// server has no notion of one caller being another.
func (p *proxyAssertion) authenticate(r *http.Request) (*http.Request, *authRefusal) {
	asserted := r.Header.Get(p.principalHeader)
	if asserted == "" {
		return nil, &authRefusal{
			status:    http.StatusUnauthorized,
			challenge: p.resource.challenge,
			message: "this server is configured to read who a caller is from a header set in front of it, " +
				"and this request carries none",
		}
	}
	return withPrincipal(r, &principal{Subject: asserted, Scopes: p.scopes(r)}), nil
}

// scopes reads what the proxy says this caller was granted, from the one header
// named for it. A deployment that named no scopes header asserts none, rather
// than falling back to reading some other header for them.
func (p *proxyAssertion) scopes(r *http.Request) []string {
	if p.scopesHeader == "" {
		return nil
	}
	return strings.Fields(r.Header.Get(p.scopesHeader))
}

// resolveProxyAssertion settles whether this deployment reads its caller from a
// proxy in front, and returns the reader where it does.
//
// A nil reader with no error is a deployment that validates tokens itself,
// which is what the absent block means. The half-declared case is refused
// rather than silently read as the absent one, on the same ground the
// certificate and key pair is: naming a scopes header and no principal header
// is a deployment that meant to read its caller from a proxy, and reading it as
// one that did not would leave every request validated against a token the
// proxy has already consumed.
func resolveProxyAssertion(cfg AuthorizationConfig, resource protectedResource) (*proxyAssertion, error) {
	proxy := cfg.TrustedProxy
	if !proxy.configured() {
		if proxy.ScopesHeader != "" {
			return nil, errors.New(
				"mcp.authorization.trusted_proxy.scopes_header is set and " +
					"mcp.authorization.trusted_proxy.principal_header is not; the principal header is the " +
					"whole credential in that mode, so a deployment reading its caller from a proxy needs it")
		}
		return nil, nil
	}
	return &proxyAssertion{
		resource:        resource,
		principalHeader: proxy.PrincipalHeader,
		scopesHeader:    proxy.ScopesHeader,
	}, nil
}

// refuseTrustedProxyOnAnExposedAddress refuses a deployment that reads its
// caller's identity from a header on an address the network can reach.
//
// It is made where the listener is resolved and nowhere else, because it is the
// only place both halves of the question are known: the handler is handed what
// a deployment declared about its callers and is never told where it will be
// reachable from. The refusal is made before anything binds, which is what
// makes it a refusal rather than a window.
//
// The confinement here is enforced, unlike the one on the address the liveness
// and readiness checks are answered at, and the difference is what each address
// discloses. That one answers whether a process is up, which an operator may
// choose to publish. This one makes a request header the whole credential, so a
// listener in this mode on an address the network reaches is not a weaker
// deployment: every caller that can reach it can assert any identity it likes.
func refuseTrustedProxyOnAnExposedAddress(target listenTarget, cfg AuthorizationConfig, address string) error {
	if !cfg.TrustedProxy.configured() || target.confined {
		return nil
	}
	return fmt.Errorf(
		"mcp.authorization.trusted_proxy makes a request header the whole credential, which is trustworthy "+
			"only where nothing but the proxy in front can set it, and mcp.listen is %q, which the network "+
			"can reach: listen on 127.0.0.1, ::1, localhost, or unix: followed by a path, or remove "+
			"mcp.authorization.trusted_proxy so this server validates the tokens its callers present",
		address)
}
