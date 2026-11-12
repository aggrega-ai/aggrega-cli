// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"errors"
	"net/http"
	"strings"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// bearerScheme is the credential scheme this deployment accepts, spelled as the
// authorization header spells it. The comparison against a request is
// case-insensitive, because the header's scheme is.
const bearerScheme = "Bearer"

// queryTokenParameter is the name a token carried in the address rather than in
// the header would arrive under. Nothing is ever read from it: a request
// carrying it is refused, because a token in an address is written into every
// proxy log, browser history and referrer it passes through, and having been
// exposed that way it should not go on being accepted.
const queryTokenParameter = "access_token"

// acceptedTokenTypes are the values a token may state for itself in its type
// header. A token stating nothing at all is accepted as well, which is why the
// absent case is not a member here.
//
// The first two are the spellings registered for an access token; the third is
// what a token says when it says only that it is a JWT, and authorization
// servers in wide use mint access tokens under either. The set is fixed rather
// than configurable, because a security setting whose safe value is not obvious
// is worse than a narrower product.
//
// 🔴 Accepting the bare form gives up telling an access token from an identity
// token by what the token calls itself, and the audience check below is what
// carries that distinction instead: an identity token's audience is the client
// it was issued to, never this deployment's own published address, so a token
// issued for something else is refused there. That makes the audience check
// load-bearing rather than one check among several, and it is never optional.
var acceptedTokenTypes = []string{"at+jwt", "application/at+jwt", "JWT"}

// signatureAlgorithms are the algorithms a token may be signed with.
//
// Every one is asymmetric, and that is the whole list rather than a starting
// point. An unsigned token names an algorithm that is not here and is refused
// at the parse. So is a token naming a symmetric algorithm, which matters more
// than it looks: verifying one takes the same bytes to make a signature as to
// check it, so a deployment accepting a symmetric algorithm against a set of
// PUBLIC keys would accept a signature anyone holding those public keys could
// have produced.
var signatureAlgorithms = []jose.SignatureAlgorithm{
	jose.RS256, jose.RS384, jose.RS512,
	jose.PS256, jose.PS384, jose.PS512,
	jose.ES256, jose.ES384, jose.ES512,
	jose.EdDSA,
}

// tokenClaims are the claims read from a token beyond the registered ones the
// JWT library already models, in both spellings each is published under.
type tokenClaims struct {
	// ClientID and AuthorizedParty each name the client a token was issued to.
	// Which one an authorization server writes varies, so both are read and
	// whichever is present is recorded.
	ClientID        string `json:"client_id"`
	AuthorizedParty string `json:"azp"`

	// Scope is the space-separated form, ScopeList the array form. An
	// authorization server publishes one or the other.
	Scope     string   `json:"scope"`
	ScopeList []string `json:"scp"`
}

// bearerValidator admits a request carrying a token this deployment's
// authorization server issued for it, and refuses everything else.
//
// It is a resource server and nothing more: it reads a token a caller
// presented, decides whether that token was issued by the configured
// authorization server for this deployment, and forgets it. It never obtains a
// token, never exchanges one, never sends one anywhere, and never writes one
// down — so a token that is refused leaves no trace beyond the request having
// been refused, and a token that is accepted leaves only what it asserted.
type bearerValidator struct {
	// resource is what this deployment publishes about itself, which is where
	// every challenge this composes comes from: a refused caller is pointed at
	// the same document a caller with nothing is pointed at.
	resource protectedResource

	// issuer and audience are what a token has to state to be accepted: the
	// authorization server it must have come from, and this deployment's own
	// published address, which its audience must name.
	issuer   string
	audience string

	// keys are the keys a token may have been signed with.
	keys verificationKeys
}

// refusalReason names, for whoever runs this deployment, which check turned a
// credential away.
//
// It is a distinct type rather than a string on purpose. Every refusal a caller
// receives is composed from fixed text, and the whole value of that is that a
// caller learns whether to obtain another token and nothing about which check
// refused the one it sent. Putting a reason into a refusal's message or its
// challenge therefore does not compile without someone writing a conversion,
// which is a thing nobody does by accident.
type refusalReason string

// The reasons, one per check that can turn a credential away.
//
// Each is a sentence an operator can act on: the setting to look at, or the
// property of the token that was wrong. None of them carries any part of what
// the caller presented — not a claim value, not a key identifier, not a
// fragment of the token — because the stream these reach is collected, shipped
// and read by people and machines a caller never chose.
const (
	reasonTokenInTheAddress refusalReason = "a token was sent in the address rather than on the authorization header"
	reasonNotABearerHeader  refusalReason = "the authorization header does not carry a bearer credential"
	reasonNotAToken         refusalReason = "the credential is not a signed token, or names a signature algorithm " +
		"this server does not accept; only asymmetric algorithms are accepted"
	reasonSeveralSignatures refusalReason = "the token carries more than one signature"
	reasonTokenType         refusalReason = "the token declares a type this server does not accept; the accepted " +
		"types are at+jwt, application/at+jwt, JWT, and no declared type at all"
	reasonKeysOutOfReach refusalReason = "the verification keys could not be read, so no token could be checked; " +
		"look at mcp.authorization.issuer and mcp.authorization.jwks_file"
	// reasonKeysOverPlaintext is kept apart from the one above because the two
	// ask an operator for different things: that one is an authorization server
	// this process could not reach, this one a server it reached that pointed
	// the key read at an unprotected path, which only that server can fix.
	reasonKeysOverPlaintext refusalReason = "the verification keys were not read, because the authorization " +
		"server named by mcp.authorization.issuer pointed the read at a plain http address on another machine, " +
		"in its metadata or with a redirect; nothing was fetched from that address, and that server has to " +
		"publish its metadata and keys over https"
	reasonUnknownKey    refusalReason = "the token names a key identifier the verification keys do not carry"
	reasonSignature     refusalReason = "the token's signature does not verify against any of the verification keys"
	reasonNoExpiry      refusalReason = "the token carries no expiry, so it would never stop being usable"
	reasonNoSubject     refusalReason = "the token says nothing about who presented it"
	reasonWrongIssuer   refusalReason = "the token was issued by somebody other than mcp.authorization.issuer"
	reasonWrongAudience refusalReason = "the token's audience does not name mcp.authorization.resource, so it was " +
		"issued for something other than this deployment"
	reasonExpired     refusalReason = "the token has expired"
	reasonNotYetValid refusalReason = "the token is not valid yet, which a clock difference between this host and " +
		"the authorization server will also produce"
	reasonIssuedInTheFuture refusalReason = "the token states an issue time in the future, which a clock difference " +
		"between this host and the authorization server will also produce"
	reasonClaimsRefused refusalReason = "the token's claims were not accepted"
)

// authenticate reads the credential a request carries and reports who it is on
// behalf of.
//
// The order of the checks below is not an arrangement. A token in the address
// is refused before anything is read from the header, because a request
// carrying one has already leaked it wherever it passed and the point is to
// refuse it rather than to evaluate it. Everything after that is refused with
// one of two reasons and no finer detail, so a caller learns whether to obtain
// a new token and nothing about which check turned it away.
//
// Every refusal of a credential that was presented carries, for whoever runs
// this deployment, the check that turned it away — recorded as a field of the
// request's own record rather than written here, so a refused request leaves one
// line rather than two. A request presenting nothing carries no reason and so
// leaves no line at all: being challenged is how a client discovers where to
// obtain a token, so it is the first thing every well-behaved client does, and a
// line for each of those would bury the refusals that mean something under the
// ones that mean a client is starting up.
func (v *bearerValidator) authenticate(r *http.Request) (*http.Request, *authRefusal) {
	if r.URL.Query().Has(queryTokenParameter) {
		return nil, &authRefusal{
			status:    http.StatusBadRequest,
			challenge: v.resource.challengeFor(authErrorInvalidRequest),
			message: "this server takes a bearer token on the authorization header and nowhere else; a " +
				"token in the address is recorded by everything the request passes through, so one sent " +
				"that way is refused rather than used",
			reason: reasonTokenInTheAddress,
		}
	}

	presented := r.Header.Get(headerAuthorization)
	if presented == "" {
		return nil, &authRefusal{
			status:    http.StatusUnauthorized,
			challenge: v.resource.challenge,
			message: "this server answers a caller carrying a bearer token; the challenge names the " +
				"document that says where to obtain one",
		}
	}
	scheme, raw, split := strings.Cut(presented, " ")
	if !strings.EqualFold(scheme, bearerScheme) || strings.TrimSpace(raw) == "" || !split {
		return nil, &authRefusal{
			status:    http.StatusBadRequest,
			challenge: v.resource.challengeFor(authErrorInvalidRequest),
			message:   "the authorization header is not a bearer credential; it reads " + bearerScheme + " followed by a token",
			reason:    reasonNotABearerHeader,
		}
	}

	p, outcome, reason := v.principalFor(r, strings.TrimSpace(raw))
	switch outcome {
	case credentialAccepted:
		return withPrincipal(r, p), nil
	case credentialUnverifiable:
		// No challenge: nothing the caller presented was found wanting, so
		// asking it to present something else would send it after a token it
		// already holds. The status is the one this transport already uses for
		// work it is momentarily unable to do.
		return nil, &authRefusal{
			status: http.StatusServiceUnavailable,
			message: "this server could not read the keys the authorization server it was configured " +
				"with publishes, so no token can be verified at the moment",
			reason: reason,
		}
	default:
		return nil, &authRefusal{
			status:    http.StatusUnauthorized,
			challenge: v.resource.challengeFor(authErrorInvalidToken),
			message: "the bearer token presented was not accepted; obtain one from the authorization " +
				"server the challenge names, for the resource it names",
			reason: reason,
		}
	}
}

// credentialOutcome separates the two things a token that does not admit its
// bearer can mean, because the two owe a caller different answers: a token this
// deployment read and would not accept, and a token it could not read at all
// because the keys were out of reach.
type credentialOutcome int

const (
	credentialAccepted credentialOutcome = iota
	credentialRefused
	credentialUnverifiable
)

// principalFor verifies one token and reads who it says presented it.
//
// Nothing about why a token failed reaches the caller that presented it. Every
// way of failing comes back as the same outcome, and authenticate turns each of
// them into the same answer, so the checks below can be as specific as they
// need to be without any of that specificity being visible from outside.
//
// The reason returned beside the outcome is for the process's own error stream
// and for nothing else. It is captured here rather than composed afterwards
// because this is the only place that knows which check refused: by the time
// the outcome has been turned into an answer, every way of failing has become
// one. The token itself is never returned, logged, or placed in anything the
// caller receives, and no reason carries any part of one.
func (v *bearerValidator) principalFor(r *http.Request, raw string) (*principal, credentialOutcome, refusalReason) {
	parsed, err := jwt.ParseSigned(raw, signatureAlgorithms)
	if err != nil {
		return nil, credentialRefused, reasonNotAToken
	}
	if len(parsed.Headers) != 1 {
		return nil, credentialRefused, reasonSeveralSignatures
	}
	header := parsed.Headers[0]
	if declared, stated := header.ExtraHeaders[jose.HeaderType].(string); stated &&
		!statesAcceptedTokenType(declared) {
		return nil, credentialRefused, reasonTokenType
	}

	candidates, err := v.keys.keysFor(r.Context(), header.KeyID)
	if err != nil {
		// The same outcome either way, so the caller is told the same thing; only
		// the operator's line says which of the two it was.
		if errors.Is(err, errKeysOverPlaintext) {
			return nil, credentialUnverifiable, reasonKeysOverPlaintext
		}
		return nil, credentialUnverifiable, reasonKeysOutOfReach
	}
	if len(candidates) == 0 {
		return nil, credentialRefused, reasonUnknownKey
	}

	var registered jwt.Claims
	var extra tokenClaims
	verified := false
	for i := range candidates {
		if err := parsed.Claims(candidates[i].Key, &registered, &extra); err == nil {
			verified = true
			break
		}
	}
	if !verified {
		return nil, credentialRefused, reasonSignature
	}

	// A token with no expiry never stops being usable, and one with no subject
	// asserts nothing about who presented it. The claim validation below skips
	// a claim that is absent, so both are required here rather than left to it.
	if registered.Expiry == nil {
		return nil, credentialRefused, reasonNoExpiry
	}
	if registered.Subject == "" {
		return nil, credentialRefused, reasonNoSubject
	}
	if err := registered.Validate(jwt.Expected{
		Issuer:      v.issuer,
		AnyAudience: jwt.Audience{v.audience},
	}); err != nil {
		return nil, credentialRefused, claimRefusalReason(err)
	}

	return &principal{
		Subject:  registered.Subject,
		Issuer:   registered.Issuer,
		ClientID: firstNonEmpty(extra.ClientID, extra.AuthorizedParty),
		Scopes:   extra.scopes(),
	}, credentialAccepted, ""
}

// claimRefusalReason names which claim the validation found wanting, for the
// operator-facing line alone.
//
// It reads the condition through the sentinels the validation returns rather
// than through the sentence each of them carries, so a reworded message cannot
// quietly turn every claim refusal into the unnamed one. A condition this does
// not recognise falls through to the general reason rather than to nothing: a
// line saying only that the claims were refused is still a line an operator can
// act on, while a missing one is the opacity this exists to remove.
func claimRefusalReason(err error) refusalReason {
	switch {
	case errors.Is(err, jwt.ErrInvalidIssuer):
		return reasonWrongIssuer
	case errors.Is(err, jwt.ErrInvalidAudience):
		return reasonWrongAudience
	case errors.Is(err, jwt.ErrExpired):
		return reasonExpired
	case errors.Is(err, jwt.ErrNotValidYet):
		return reasonNotYetValid
	case errors.Is(err, jwt.ErrIssuedInTheFuture):
		return reasonIssuedInTheFuture
	default:
		return reasonClaimsRefused
	}
}

// statesAcceptedTokenType reports whether a token declaring a type declares one
// this deployment accepts. The comparison is case-insensitive, as the
// registrations these values come from are.
func statesAcceptedTokenType(declared string) bool {
	for _, accepted := range acceptedTokenTypes {
		if strings.EqualFold(declared, accepted) {
			return true
		}
	}
	return false
}

// scopes reads what a token says its bearer may ask for, from whichever of the
// two forms the authorization server published.
//
// The space-separated form wins where both are present rather than the two
// being merged: a server publishing both is publishing one fact twice, and
// taking the union of two spellings of one fact would grant whatever the wider
// of them said.
func (c tokenClaims) scopes() []string {
	if c.Scope != "" {
		return strings.Fields(c.Scope)
	}
	return c.ScopeList
}

// firstNonEmpty is the "whichever of these was published" rule the client
// identifier needs, kept as one expression so the preference between the two
// spellings is stated once.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
