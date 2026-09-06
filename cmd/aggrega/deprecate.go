// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/publishops"
)

// deprecateFlags carries the `deprecate` command's flag state. It is constructed
// per command so two invocations in one process cannot observe each other's flag
// values. It is distinct from deprecateConfig, which is the input contract
// deprecateRun reads.
type deprecateFlags struct {
	severity    string
	reason      string
	replacement string
	message     string
	from        string
	keyPath     string
	fulcio      string
	rekor       string
	timestamp   string
	idToken     string
}

// deprecateConfig is the deprecation-marking operation's input contract, which
// lives with the operation rather than with this command.
type deprecateConfig = publishops.DeprecateConfig

func newDeprecateCmd() *cobra.Command {
	opts := &deprecateFlags{}
	c := &cobra.Command{
		Use:   "deprecate <reference>",
		Short: "Attach a signed deprecation marker to an already-published artifact",
		Long: "Deprecate amends an already-published artifact by attaching a deprecation marker to it as an OCI " +
			"referrer, then signing that marker. It does not re-push, re-tag, or otherwise mutate the published " +
			"bytes — the artifact is immutable; the marker is a sibling manifest pointing back at it, so a marker " +
			"can be attached retroactively. The marker records a severity (warning, error, or retracted), a reason, " +
			"an optional replacement reference, and the timestamp it was issued. Run it three ways: pass the content " +
			"as flags; omit the content flags at an interactive terminal to be prompted for severity, reason, and " +
			"replacement; or pass --from <file> to attach one marker per entry from a batch file. The marker itself " +
			"is signed the same way artifacts are: its manifest digest is signed and the signature is attached as a " +
			"referrer of the marker, so the marker is verifiable the same way artifact signatures are. With " +
			"--key the signature is produced offline from a held ECDSA P-256 key; without --key the keyless flow is " +
			"used, which requires a live identity provider, Fulcio, and Rekor.",
		// One positional reference in the normal path; none with --from, where the
		// references are read from the batch file.
		Args: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("from") {
				if len(args) > 0 {
					return fmt.Errorf("deprecate --from <file> reads references from the file and takes no positional reference")
				}
				return nil
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			reference := ""
			if len(args) == 1 {
				reference = args[0]
			}
			return deprecateRun(deprecateConfig{
				Reference:    reference,
				Severity:     opts.severity,
				Reason:       opts.reason,
				Replacement:  opts.replacement,
				Message:      opts.message,
				From:         opts.from,
				KeyPath:      opts.keyPath,
				FulcioURL:    opts.fulcio,
				RekorURL:     opts.rekor,
				TimestampURL: opts.timestamp,
				IDToken:      opts.idToken,
				In:           cmd.InOrStdin(),
				Workspace:    rw,
			})
		},
	}
	c.Flags().StringVar(&opts.severity, "severity", "", "Deprecation severity: one of warning, error, retracted. Required unless --from or an interactive prompt supplies it.")
	c.Flags().StringVar(&opts.reason, "reason", "", "Human-readable reason for the deprecation — why it is deprecated and what to do instead. Required unless --from or an interactive prompt supplies it.")
	c.Flags().StringVar(&opts.replacement, "replacement", "", "Optional artifact reference that replaces the deprecated one.")
	c.Flags().StringVar(&opts.message, "message", "", "Optional legacy message retained for audit and for the recorded build-time diagnostic; defaults to --reason when unset.")
	c.Flags().StringVar(&opts.from, "from", "", "Batch file (YAML, or JSON as a subset) whose top-level list attaches one marker per entry. Each entry carries reference and severity and reason, plus optional replacement and message. Mutually exclusive with a positional reference.")
	c.Flags().StringVar(&opts.keyPath, "key", "", "Path to a held ECDSA P-256 private key PEM for offline keyed signing. When omitted, keyless signing is used.")
	c.Flags().StringVar(&opts.fulcio, "fulcio-url", "", "Fulcio certificate-authority URL for keyless signing.")
	c.Flags().StringVar(&opts.rekor, "rekor-url", "", "Rekor transparency-log URL for keyless signing.")
	c.Flags().StringVar(&opts.timestamp, "timestamp-url", "", "Optional RFC3161 timestamp-authority URL for keyless signing.")
	c.Flags().StringVar(&opts.idToken, "identity-token", "", "OIDC identity token for keyless signing. Optional at an interactive terminal (a browser flow acquires one) and on GitHub Actions (the workflow OIDC token is minted automatically); required for other non-interactive/CI use, where it may instead be exported as AGGREGA_IDENTITY_TOKEN or SIGSTORE_ID_TOKEN.")
	return c
}

// deprecateRun is the command line's adapter onto the deprecation-marking
// operation. It fills the seams whose production value belongs to a terminal —
// the interactive identity rung, the terminal check that also gates the content
// prompt — and names the command in any failure.
func deprecateRun(cfg deprecateConfig) error {
	cfg.TokenGetter = tokenGetterOrDefault(cfg.TokenGetter)
	cfg.IsTTY = ttyOrDefault(cfg.IsTTY)
	cfg.EnvLookup = envLookupOrGetenv(cfg.EnvLookup)
	cfg.IdentitySupply = commandLineIdentitySupply
	if _, err := publishops.Deprecate(cfg); err != nil {
		return fmt.Errorf("aggrega deprecate: %w", err)
	}
	return nil
}
