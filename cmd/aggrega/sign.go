// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/publishops"
)

// signFlags carries the `sign` command's flag state. It is constructed per
// command so two invocations in one process cannot observe each other's flag
// values. It is distinct from signConfig, which is the input contract signRun
// reads.
type signFlags struct {
	keyPath   string
	fulcio    string
	rekor     string
	timestamp string
	idToken   string
}

// signConfig is the signing operation's input contract, which lives with the
// operation rather than with this command.
type signConfig = publishops.SignConfig

func newSignCmd() *cobra.Command {
	opts := &signFlags{}
	c := &cobra.Command{
		Use:   "sign <reference>...",
		Short: "Sign one or more already-published artifacts and attach their signatures",
		Long: "Sign amends already-published artifacts by attaching a signature to each as an OCI referrer. " +
			"It does not re-push, re-tag, or otherwise mutate the published bytes — the artifacts are immutable; " +
			"each signature is a sibling manifest pointing back at its artifact. With --key signatures are produced " +
			"offline from a held ECDSA P-256 key; without --key the keyless flow is used, which requires a live " +
			"identity provider, Fulcio, and Rekor. Multiple references are signed in one invocation: the keyless " +
			"identity is acquired once and reused across every artifact, and a fresh short-lived certificate is " +
			"issued per artifact. Every signable reference is signed; failures are collected and reported at the " +
			"end, and the command exits non-zero if any reference failed.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return signRun(signConfig{
				References:   args,
				KeyPath:      opts.keyPath,
				FulcioURL:    opts.fulcio,
				RekorURL:     opts.rekor,
				TimestampURL: opts.timestamp,
				IDToken:      opts.idToken,
				Workspace:    rw,
			})
		},
	}
	c.Flags().StringVar(&opts.keyPath, "key", "", "Path to a held ECDSA P-256 private key PEM for offline keyed signing. When omitted, keyless signing is used.")
	c.Flags().StringVar(&opts.fulcio, "fulcio-url", "", "Fulcio certificate-authority URL for keyless signing.")
	c.Flags().StringVar(&opts.rekor, "rekor-url", "", "Rekor transparency-log URL for keyless signing.")
	c.Flags().StringVar(&opts.timestamp, "timestamp-url", "", "Optional RFC3161 timestamp-authority URL for keyless signing.")
	c.Flags().StringVar(&opts.idToken, "identity-token", "", "OIDC identity token for keyless signing. Optional at an interactive terminal (a browser flow acquires one) and on GitHub Actions (the workflow OIDC token is minted automatically); required for other non-interactive/CI use, where it may instead be exported as AGGREGA_IDENTITY_TOKEN or SIGSTORE_ID_TOKEN.")
	return c
}

// signRun is the command line's adapter onto the signing operation. It fills the
// seams whose production value belongs to a terminal — the interactive identity
// rung and the terminal check — and names the command in any failure.
func signRun(cfg signConfig) error {
	cfg.TokenGetter = tokenGetterOrDefault(cfg.TokenGetter)
	cfg.IsTTY = ttyOrDefault(cfg.IsTTY)
	cfg.EnvLookup = envLookupOrGetenv(cfg.EnvLookup)
	if err := publishops.Sign(cfg); err != nil {
		return fmt.Errorf("aggrega sign: %w", err)
	}
	return nil
}
