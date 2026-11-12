// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// attachAuthCommands builds the `auth` command group under parent. It deals
// with the signing identity rather than registry credentials, carries a single
// leaf today, `auth token`, and leaves room for further identity-scoped verbs
// without flattening them onto the root.
//
// The group is attached before its child so that the child's invocation path is
// the one it will be invoked by when the registrar checks it against the
// operation table.
func attachAuthCommands(parent *cobra.Command) {
	c := &cobra.Command{
		Use:   "auth",
		Short: "Manage the signing identity",
		Long: "Commands for the keyless signing identity (an OIDC identity token), distinct from " +
			"`aggrega login`, which stores registry username/password credentials. The identity token " +
			"is a short-lived secret used by keyless signing; it is never persisted by these commands.",
	}
	mustAttachGroup(parent, c)
	mustAttach(c, newAuthTokenCmd(), "auth-token")
}

func newAuthTokenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "token",
		Short: "Acquire an OIDC identity token interactively and print it to stdout",
		Long: "Run the interactive OIDC flow once and print the raw identity token to stdout, so it can " +
			"populate the environment for a batch of keyless signs:\n\n" +
			"    export AGGREGA_IDENTITY_TOKEN=$(aggrega auth token)\n\n" +
			"Only the token is written to stdout; any prompt or diagnostic goes to stderr, so command " +
			"substitution captures the token alone. The token is short-lived and is not persisted by this " +
			"command — the caller decides whether to export it. This is distinct from `aggrega login`, which " +
			"stores registry credentials.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return authTokenRun(authTokenConfig{})
		},
	}
}

// authTokenConfig is the input contract for the `auth token` workflow. Tests
// set TokenGetter and Out to inject a fake acquisition and capture stdout; nil
// means production (the interactive acquisition and os.Stdout).
type authTokenConfig struct {
	// TokenGetter acquires the OIDC identity token. Nil means production
	// (acquireInteractiveIDToken, via tokenGetterOrDefault), the same
	// acquisition path sign and push use — never a second OAuth flow.
	TokenGetter func() (string, error)

	// Out receives the token. Nil means os.Stdout. Only the token (plus a
	// trailing newline) is ever written here; human-facing text goes to stderr.
	Out io.Writer
}

// authTokenRun acquires an identity token once and writes only the token to
// stdout, so `export AGGREGA_IDENTITY_TOKEN=$(aggrega auth token)` captures the
// token alone. It single-sources on the existing interactive acquisition rather
// than re-implementing the OIDC flow; on a failure it returns the error
// (rendered to stderr by Execute) and writes nothing to stdout.
func authTokenRun(cfg authTokenConfig) error {
	tok, err := tokenGetterOrDefault(cfg.TokenGetter)()
	if err != nil {
		return fmt.Errorf("aggrega auth token: %w", err)
	}
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}
	fmt.Fprintln(out, tok)
	return nil
}
