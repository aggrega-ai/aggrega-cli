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
// the request (nil selects http.DefaultClient); validate, when non-nil, is
// applied to each freshly fetched token so subject validation stays with its
// single owner rather than being duplicated here.
func AmbientGitHubActionsSource(look func(string) string, client *http.Client, validate func(string) error) IdentityTokenSource {
	if client == nil {
		client = http.DefaultClient
	}
	return func(ctx context.Context) (string, error) {
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
