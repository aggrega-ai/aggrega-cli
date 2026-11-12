// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/sigstore/sigstore/pkg/oauthflow"
	"golang.org/x/term"

	"github.com/aggrega-ai/aggrega-cli/internal/opprogress"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// isTerminal reports whether stdin is an interactive terminal. It is a
// package-level seam: tests replace it to drive the acquisition decision
// without a real TTY.
var isTerminal = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// acquireInteractiveIDToken acquires a short-lived OIDC identity token
// interactively through the public-good Sigstore OAuth flow. It opens a
// browser to complete the federated login and, when no browser is available,
// falls back to an out-of-band paste flow. The returned raw token is fed to
// the keyless signer; it is a runtime secret, never persisted.
//
// It lives in the command line and nowhere else, and that placement is the
// mechanism rather than a filing choice: this is the one rung of the identity
// ladder that needs a person, so keeping it here — in a package no other surface
// can import — is what makes it unreachable from a surface with nobody watching.
// The shared publication layer takes the rung as an optional input that defaults
// to absent; a caller that cannot answer a federated login supplies none, and the
// ladder refuses loudly instead of waiting.
func acquireInteractiveIDToken() (string, error) {
	tok, err := oauthflow.OIDConnect(signing.DefaultOIDCIssuer, signing.DefaultOIDCClientID, "", "", oauthflow.DefaultIDTokenGetter)
	if err != nil {
		return "", fmt.Errorf("acquire identity token: %w", err)
	}
	return tok.RawString, nil
}

// tokenGetterOrDefault returns getter when non-nil, else the production
// interactive acquisition function. The one place the seam falls through to
// production, so every command that may ask a person defaults identically.
func tokenGetterOrDefault(getter func() (string, error)) func() (string, error) {
	if getter != nil {
		return getter
	}
	return acquireInteractiveIDToken
}

// signInGetter is tokenGetterOrDefault for a command whose operation may be in
// the middle of a step it waits on when it asks a person to sign in: the progress
// surface is taken out of the way (opprogress.Yield) before the sign-in prints its
// address and waits, so the person reads it undisturbed.
func signInGetter(ctx context.Context, getter func() (string, error)) func() (string, error) {
	acquire := tokenGetterOrDefault(getter)
	return func() (string, error) {
		opprogress.Yield(ctx)
		return acquire()
	}
}

// ttyOrDefault returns tty when non-nil, else the production terminal check.
func ttyOrDefault(tty func() bool) func() bool {
	if tty != nil {
		return tty
	}
	return isTerminal
}

// commandLineIdentitySupply is how a refusal names an identity token to somebody
// typing at a terminal: as the flag they can pass, and with the terminal's own
// absence reported, because here there genuinely is a terminal to report on.
//
// The vocabulary belongs to this surface rather than to the operation, for the
// same reason the command name does: a caller reaching the same operation from
// elsewhere never typed a flag, and telling it to pass one names something it
// cannot do. The operation states the condition; this states what to do about it
// here.
var commandLineIdentitySupply = signing.IdentityTokenSupply{
	AbsentInput:  "no --identity-token was passed",
	AbsentPrompt: "stdin is not an interactive terminal",
	Remedy:       "pass --identity-token",
}

// commandLineDestinationSupply is how a refusal names a publication's missing
// destination to somebody typing at a terminal: the flag, or the workspace
// setting spelled as it is written in the workspace file.
const commandLineDestinationSupply = "set --registry or default_registry"

// commandLinePinSourcesSupply is how the refusal over an entity whose lock is
// missing a pin names, to somebody typing at a terminal, the way to have this one
// push run the pin step: the flag they can pass on the command they just ran.
const commandLinePinSourcesSupply = "re-run it with `--pin-sources`"
