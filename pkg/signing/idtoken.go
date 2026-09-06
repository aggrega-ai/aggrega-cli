// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
)

// IdentityTokenSource yields an OIDC identity token. It is called once per
// Sign invocation, so a source backed by a live provider returns a fresh token
// each time — long-running publish jobs never present a token that has expired
// since signing began. A source may also be a constant (see
// StaticIdentityToken) when the token is supplied out of band.
type IdentityTokenSource func(ctx context.Context) (string, error)

// StaticIdentityToken returns a source that always yields tok. It is the shape
// for a token supplied once at the command boundary — an explicit flag, an
// environment variable, or a single interactive acquisition — where every
// signature in a run reuses the same token.
func StaticIdentityToken(tok string) IdentityTokenSource {
	return func(context.Context) (string, error) { return tok, nil }
}

// The environment variables the identity-token ladder reads. Their names live
// here, beside the ladder that consults them, so the resolution order and the
// names it resolves have one home.
const (
	// EnvIdentityToken supplies the keyless OIDC identity token. It is a
	// short-lived secret read at resolution time and never persisted.
	EnvIdentityToken = "AGGREGA_IDENTITY_TOKEN"
	// EnvSigstoreIDToken is the sigstore-ecosystem interop alias for the
	// identity token, consulted below EnvIdentityToken and below the ambient
	// endpoint. It carries no Aggrega prefix because it is a foreign alias
	// rather than an Aggrega setting.
	EnvSigstoreIDToken = "SIGSTORE_ID_TOKEN"
)

// GitHub Actions exposes an ambient OIDC token endpoint through these
// environment variables. They are ecosystem names set by the Actions runner,
// not Aggrega settings.
const (
	envActionsIDTokenRequestURL   = "ACTIONS_ID_TOKEN_REQUEST_URL"
	envActionsIDTokenRequestToken = "ACTIONS_ID_TOKEN_REQUEST_TOKEN"
)

// sigstoreOIDCAudience is the audience requested from the ambient GitHub
// Actions OIDC endpoint. Fulcio issues signing certificates only for tokens
// minted for the Sigstore audience.
const sigstoreOIDCAudience = "sigstore"

// GitHubActionsAmbientAvailable reports whether the ambient GitHub Actions
// OIDC endpoint is reachable — true only when both the request URL and the
// request token are present in the environment look provides. Callers use it to
// decide whether to prefer the ambient source over a statically supplied token.
func GitHubActionsAmbientAvailable(look func(string) string) bool {
	return look(envActionsIDTokenRequestURL) != "" && look(envActionsIDTokenRequestToken) != ""
}

// AmbientGitHubActionsSource returns a source that fetches a fresh OIDC
// identity token from the ambient GitHub Actions endpoint on every call. Each
// call issues a GET to the request URL with the Sigstore audience appended as a
// query parameter and the request token as a bearer credential, then reads the
// JSON body's "value" field as the token. A non-2xx response, a missing
// environment variable, or an empty token value is a descriptive error.
//
// look reads the environment (pass os.Getenv in production); client performs
// the request and is obtained from the network-egress gate (a nil client means
// the workspace is offline, so the source refuses the fetch rather than reaching
// for a default client); validate, when non-nil, is applied to each freshly
// fetched token so subject validation stays with its single owner rather than
// being duplicated here.
func AmbientGitHubActionsSource(look func(string) string, client *http.Client, validate func(string) error) IdentityTokenSource {
	return func(ctx context.Context) (string, error) {
		if client == nil {
			// The client is obtained from the network-egress gate (pkg/egress); a
			// nil client means the workspace is offline, so the ambient-token fetch
			// is refused rather than reaching for a default client.
			return "", fmt.Errorf("github actions ambient identity token: %w", egress.ErrOfflineEgress())
		}
		base := look(envActionsIDTokenRequestURL)
		reqToken := look(envActionsIDTokenRequestToken)
		if base == "" || reqToken == "" {
			return "", fmt.Errorf("github actions ambient identity token: both %s and %s must be set", envActionsIDTokenRequestURL, envActionsIDTokenRequestToken)
		}
		endpoint, err := url.Parse(base)
		if err != nil {
			return "", fmt.Errorf("github actions ambient identity token: parse %s: %w", envActionsIDTokenRequestURL, err)
		}
		q := endpoint.Query()
		q.Set("audience", sigstoreOIDCAudience)
		endpoint.RawQuery = q.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return "", fmt.Errorf("github actions ambient identity token: build request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+reqToken)
		req.Header.Set("Accept", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("github actions ambient identity token: request: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			return "", fmt.Errorf("github actions ambient identity token: endpoint returned status %d: %s", resp.StatusCode, string(body))
		}
		var payload struct {
			Value string `json:"value"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return "", fmt.Errorf("github actions ambient identity token: decode response: %w", err)
		}
		if payload.Value == "" {
			return "", fmt.Errorf("github actions ambient identity token: endpoint returned an empty token value")
		}
		if validate != nil {
			if err := validate(payload.Value); err != nil {
				return "", err
			}
		}
		return payload.Value, nil
	}
}

// InteractiveIdentityTokenAcquirer supplies an identity token from a flow that
// may block on a human — a browser federated login, or an out-of-band paste.
// It reports ok when it produced a token, and ok false with a nil error when it
// declines because the conditions for asking a human are absent (no terminal to
// prompt at). A non-nil error is a genuine acquisition failure and is returned
// to the caller unchanged.
//
// The flow itself is deliberately not implemented in this package. A caller
// that may ask a human supplies one; a caller that may not passes none, and the
// absence is what makes asking impossible rather than merely discouraged.
type InteractiveIdentityTokenAcquirer func() (tok string, ok bool, err error)

// IdentityTokenSupply names the inputs one particular caller can supply an
// identity token through, in that caller's own vocabulary, so the refusal at the
// end of the ladder names something the caller receiving it can actually act on.
//
// The ladder, and the refusal it ends in, keep one home. What varies between
// callers is only the vocabulary: a command line flag is the right thing to name
// at a terminal and unsayable to a caller that has no command line, and a caller
// told to pass an option it cannot pass has been told nothing. So the phrasing
// travels in as a parameter rather than the refusal being re-authored per caller.
//
// The zero value names no caller-specific input at all. That is deliberate and it
// is the safe default: a caller that declared no vocabulary is told about the
// environment variables and the ambient endpoint, which every caller shares, and
// is told nothing that might be false of it.
type IdentityTokenSupply struct {
	// AbsentInput is the clause reporting that the caller's own input carried
	// nothing, in that caller's words: "no --identity-token was passed".
	AbsentInput string

	// AbsentPrompt is the clause reporting that nobody could be asked, for a
	// caller that has somewhere to ask: "stdin is not an interactive terminal".
	// It is carried rather than inferred because a caller with no terminal at all
	// is not reporting a terminal's absence — the clause would be meaningless
	// rather than merely unhelpful. It is stated last.
	AbsentPrompt string

	// Remedy is the imperative naming what the caller can do about it, in that
	// caller's words: "pass --identity-token".
	Remedy string
}

// ResolveIdentityToken resolves the keyless identity as a token SOURCE — the
// single home for identity-token precedence — in one strict precedence:
//
//  1. flagToken, when non-empty — an explicit token the caller supplies;
//  2. EnvIdentityToken, when non-empty;
//  3. the ambient GitHub Actions OIDC endpoint, when reachable — a live source
//     that mints a fresh token per signature;
//  4. EnvSigstoreIDToken, when non-empty — the sigstore-ecosystem interop alias;
//  5. acquire, when non-nil and it does not decline — one token acquired once;
//  6. otherwise a loud refusal naming what to supply, so keyless signing never
//     silently proceeds and never hangs waiting on a flow nobody is watching.
//
// The ambient source outranks the frozen EnvSigstoreIDToken value so a CI job
// whose exported token would expire mid-run self-heals with no workflow change,
// while an explicit token still wins over the ambient endpoint. A static
// resolution returns its token as a string, leaving source nil, and the caller
// validates the subject once at signer construction; the ambient resolution
// returns a live source that fetches and validates per fetch through validate,
// so subject derivation is not duplicated. Rung 5 returns its token as a static
// string, so a batch acquires once and reuses it.
//
// look reads the environment; client performs the ambient fetch and is obtained
// from the network-egress gate, so a nil client means the workspace is offline
// and the ambient source refuses when the signer invokes it. A nil acquire is
// the unattended shape — see ResolveIdentityTokenNonInteractive, which is the
// name to reach for when no human may be asked. supply carries the caller's own
// vocabulary into the refusal at rung 6 and is read nowhere else, so it cannot
// affect which rung resolves.
func ResolveIdentityToken(flagToken string, look func(string) string, validate func(string) error, client *http.Client, acquire InteractiveIdentityTokenAcquirer, supply IdentityTokenSupply) (idToken string, source IdentityTokenSource, err error) {
	if flagToken != "" {
		return flagToken, nil, nil
	}
	if t := look(EnvIdentityToken); t != "" {
		return t, nil, nil
	}
	if GitHubActionsAmbientAvailable(look) {
		return "", AmbientGitHubActionsSource(look, client, validate), nil
	}
	if t := look(EnvSigstoreIDToken); t != "" {
		return t, nil, nil
	}
	if acquire != nil {
		t, ok, aerr := acquire()
		if aerr != nil {
			return "", nil, aerr
		}
		if ok {
			return t, nil, nil
		}
	}
	return "", nil, noIdentityTokenRefusal(supply)
}

// ResolveIdentityTokenNonInteractive resolves the keyless identity from
// supplied and ambient inputs alone: an explicit token, EnvIdentityToken, the
// ambient GitHub Actions OIDC endpoint, or EnvSigstoreIDToken. When none of
// them yields a token it refuses, naming what supply says this caller can offer.
//
// It is the entry point for a caller that must never block on a human — a
// long-running service, or a machine-driven protocol server with no user
// watching — where hanging on a federated login flow would be a silent failure
// and a refusal is the honest one. It takes no acquirer, so such a caller has no
// parameter through which an interactive flow could be introduced.
func ResolveIdentityTokenNonInteractive(flagToken string, look func(string) string, validate func(string) error, client *http.Client, supply IdentityTokenSupply) (idToken string, source IdentityTokenSource, err error) {
	return ResolveIdentityToken(flagToken, look, validate, client, nil, supply)
}

// noIdentityTokenRefusal builds the refusal returned when every rung of the
// identity-token ladder declines. It names each input that would have satisfied
// resolution, so the caller learns what to supply rather than being told only
// that resolution failed.
//
// The two shared inputs — the environment variables and the ambient endpoint —
// are named for every caller, because every caller has them. The caller's own
// input and its remedy come from supply, and an unstated one is omitted rather
// than guessed: an omitted clause leaves a shorter true sentence, while a guessed
// one leaves a caller reaching for something it does not have.
func noIdentityTokenRefusal(supply IdentityTokenSupply) error {
	absent := make([]string, 0, 4)
	if supply.AbsentInput != "" {
		absent = append(absent, supply.AbsentInput)
	}
	absent = append(absent,
		fmt.Sprintf("no %s or %s is exported", EnvIdentityToken, EnvSigstoreIDToken),
		"no ambient GitHub Actions OIDC endpoint is available")
	if supply.AbsentPrompt != "" {
		absent = append(absent, supply.AbsentPrompt)
	}

	remedy := "export one of those variables (for example a CI workflow OIDC token)"
	if supply.Remedy != "" {
		remedy += " or " + supply.Remedy
	}

	return fmt.Errorf("keyless signing needs an identity token: %s; on GitHub Actions the workflow "+
		"OIDC token is used automatically, otherwise %s", joinWithFinalAnd(absent), remedy)
}

// joinWithFinalAnd renders clauses as an English list — comma-separated, with
// "and" before the last. The final conjunction moves with the number of clauses,
// which is why it is composed rather than written into any one clause: a caller
// that states no terminal of its own must not be left with a dangling "and".
func joinWithFinalAnd(clauses []string) string {
	switch len(clauses) {
	case 0:
		return ""
	case 1:
		return clauses[0]
	}
	return strings.Join(clauses[:len(clauses)-1], ", ") + ", and " + clauses[len(clauses)-1]
}
