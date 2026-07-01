// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

var (
	deprecateSeverity  string
	deprecateMessage   string
	deprecateKeyPath   string
	deprecateFulcio    string
	deprecateRekor     string
	deprecateTimestamp string
	deprecateIDToken   string
)

func newDeprecateCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "deprecate <reference>",
		Short: "Attach a signed deprecation marker to an already-published artifact",
		Long: "Deprecate amends an already-published artifact by attaching a deprecation marker to it as an OCI " +
			"referrer, then signing that marker. It does not re-push, re-tag, or otherwise mutate the published " +
			"bytes — the artifact is immutable; the marker is a sibling manifest pointing back at it, so a marker " +
			"can be attached retroactively. The marker records a severity (warning, error, or retracted) and a " +
			"message. The marker itself is signed the same way artifacts are: its manifest digest is signed and the " +
			"signature is attached as a referrer of the marker, so the marker is verifiable by the existing " +
			"signature-verify machinery. With --key the signature is produced offline from a held ECDSA P-256 key; " +
			"without --key the keyless flow is used, which requires a live identity provider, Fulcio, and Rekor.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return deprecateRun(deprecateConfig{
				Reference:    args[0],
				Severity:     deprecateSeverity,
				Message:      deprecateMessage,
				KeyPath:      deprecateKeyPath,
				FulcioURL:    deprecateFulcio,
				RekorURL:     deprecateRekor,
				TimestampURL: deprecateTimestamp,
				IDToken:      deprecateIDToken,
				ws:           rw,
			})
		},
	}
	c.Flags().StringVar(&deprecateSeverity, "severity", "", "Deprecation severity: one of warning, error, retracted. Required.")
	c.Flags().StringVar(&deprecateMessage, "message", "", "Human-readable deprecation message, e.g. a migration pointer. Required.")
	c.Flags().StringVar(&deprecateKeyPath, "key", "", "Path to a held ECDSA P-256 private key PEM for offline keyed signing. When omitted, keyless signing is used.")
	c.Flags().StringVar(&deprecateFulcio, "fulcio-url", "", "Fulcio certificate-authority URL for keyless signing.")
	c.Flags().StringVar(&deprecateRekor, "rekor-url", "", "Rekor transparency-log URL for keyless signing.")
	c.Flags().StringVar(&deprecateTimestamp, "timestamp-url", "", "Optional RFC3161 timestamp-authority URL for keyless signing.")
	c.Flags().StringVar(&deprecateIDToken, "identity-token", "", "OIDC identity token for keyless signing. Optional at an interactive terminal (a browser flow acquires one); required for non-interactive/CI use, where it may instead be exported as AGGREGA_IDENTITY_TOKEN or SIGSTORE_ID_TOKEN.")
	_ = c.MarkFlagRequired("severity")
	_ = c.MarkFlagRequired("message")
	return c
}

// deprecateConfig is the input contract for the deprecate workflow.
// deprecateRun reads only from cfg, so test bodies stay race-free. Tests
// inject a Target and a Signer; production constructs both from the reference
// and the key/keyless flags. The signing-input fields mirror signConfig so the
// deprecate command reuses the exact signer-construction path `aggrega sign`
// and `aggrega push` use.
type deprecateConfig struct {
	Reference string

	Severity string
	Message  string

	// Keyed path.
	KeyPath string

	// Keyless path.
	FulcioURL    string
	RekorURL     string
	TimestampURL string
	IDToken      string

	// Target overrides the production remote.Repository construction. Tests
	// inject an in-memory oras.Target; when nil, deprecateRun constructs the
	// target from the reference via oci.NewRemoteRepository.
	Target oras.Target

	// SubjectRef overrides the subject reference resolved against Target.
	// Tests set the in-memory tag; empty derives the subject from the
	// reference's own tag/digest.
	SubjectRef string

	// Signer overrides signer construction. Tests inject a fake; when nil,
	// deprecateRun builds a keyed or keyless signer from the flags.
	Signer signing.Signer

	// ws is the resolved workspace, threaded in by the command's RunE. Nil
	// only when a test invokes deprecateRun directly; workspaceOrResolve then
	// resolves from the working directory.
	ws *workspaceconfig.ResolvedWorkspace

	// EnvLookup reads the environment for the identity-token secret
	// resolution. Nil means production (os.Getenv); tests inject a frozen
	// lookup so they stay race-free.
	EnvLookup func(string) string

	// TokenGetter acquires a keyless OIDC identity token interactively when no
	// explicit token is supplied. Nil means production; tests inject a fake so
	// no browser opens.
	TokenGetter func() (string, error)

	// IsTTY reports whether stdin is an interactive terminal, gating the
	// interactive acquisition path. Nil means production (isTerminal); tests
	// inject a fixed value so the decision is deterministic.
	IsTTY func() bool

	// Out is the sink for the status line. Nil defaults to os.Stdout at the
	// top of deprecateRun; tests pass a buffer to capture the output.
	Out io.Writer
}

// deprecateRun attaches a signed deprecation marker to the already-published
// artifact named by cfg.Reference. It validates the severity/message, resolves
// the subject's published digest, attaches the deprecation payload as an OCI
// referrer of the subject, then signs the marker's own manifest digest and
// attaches that signature as a referrer of the marker. The subject artifact is
// never re-pushed or mutated; both attachments are sibling manifests.
//
// The marker is signed through the same sign atom `aggrega sign` and
// `aggrega push` use, so the deprecation marker is a signed artifact verifiable
// by the existing signature-verify machinery. On the keyless path the identity
// token is resolved (or interactively acquired) once before the marker is
// attached, so a signer that cannot be constructed refuses the deprecation
// before anything is written.
func deprecateRun(cfg deprecateConfig) error {
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}
	ctx := context.Background()

	marker := deprecation.Marker{
		Severity: deprecation.Severity(cfg.Severity),
		Message:  cfg.Message,
	}
	payload, err := deprecation.Marshal(marker)
	if err != nil {
		return fmt.Errorf("aggrega deprecate: %w", err)
	}

	target := cfg.Target
	if target == nil {
		repo, rerr := oci.NewRemoteRepository(cfg.Reference)
		if rerr != nil {
			return fmt.Errorf("aggrega deprecate: %s: %w", cfg.Reference, rerr)
		}
		target = repo
	}

	subjectRef := cfg.SubjectRef
	if subjectRef == "" {
		subjectRef = referenceTag(cfg.Reference)
	}

	// The subject must already be published: deprecate never publishes the
	// artifact it deprecates, it only amends it.
	if _, exists, rerr := oci.Resolve(ctx, target, subjectRef); rerr != nil {
		return fmt.Errorf("aggrega deprecate: resolve %s: %w", cfg.Reference, rerr)
	} else if !exists {
		return fmt.Errorf("aggrega deprecate: %s is not published; nothing to deprecate", cfg.Reference)
	}

	// Build the signer BEFORE the marker is attached so a keyless flow with no
	// usable token, or a missing key, refuses the deprecation before writing.
	signer, err := deprecateSigner(cfg)
	if err != nil {
		return fmt.Errorf("aggrega deprecate: %w", err)
	}

	markerDesc, err := oci.AttachReferrer(ctx, target, subjectRef, deprecation.ArtifactType, payload)
	if err != nil {
		return fmt.Errorf("aggrega deprecate: attach marker to %s: %w", cfg.Reference, err)
	}

	// Sign the marker's own manifest digest and attach the signature as a
	// referrer of the marker. The marker is an untagged referrer, so its
	// signature attaches by descriptor — a tag-based resolve cannot recover
	// its digest.
	bundle, artifactType, err := signDigest(ctx, signer, markerDesc.Digest.String())
	if err != nil {
		return fmt.Errorf("aggrega deprecate: sign marker for %s: %w", cfg.Reference, err)
	}
	if _, err := oci.AttachReferrerToDescriptor(ctx, target, markerDesc, artifactType, bundle); err != nil {
		return fmt.Errorf("aggrega deprecate: attach signature to marker for %s: %w", cfg.Reference, err)
	}

	fmt.Fprintf(out, "Deprecation attached: %s (%s, %s)\n", cfg.Reference, cfg.Severity, markerDesc.Digest)
	return nil
}

// deprecateSigner constructs the signer for the deprecation marker, reusing the
// single signer-construction path buildSigner. An injected signer is returned
// as-is; otherwise the keyed/keyless inputs are resolved through the precedence
// chain (CLI flag > per-registry config > global config) and, on the keyless
// branch, the identity token is resolved or interactively acquired once. The
// keyed path never acquires a token.
func deprecateSigner(cfg deprecateConfig) (signing.Signer, error) {
	if cfg.Signer != nil {
		return cfg.Signer, nil
	}
	rw, err := workspaceOrResolve(cfg.ws)
	if err != nil {
		return nil, fmt.Errorf("workspaceconfig: %w", err)
	}
	resolved := rw.Config().ResolveSigning(registryhost.Of(cfg.Reference))
	keyPath := firstNonEmpty(cfg.KeyPath, resolved.KeyPath)

	var idToken string
	if keyPath == "" {
		tok, terr := resolveOrAcquireIDToken(cfg.IDToken, envLookupOrGetenv(cfg.EnvLookup), ttyOrDefault(cfg.IsTTY), tokenGetterOrDefault(cfg.TokenGetter))
		if terr != nil {
			return nil, terr
		}
		idToken = tok
	}
	return buildSigner(signerParams{
		KeyPath:      keyPath,
		IDToken:      idToken,
		FulcioURL:    firstNonEmpty(cfg.FulcioURL, resolved.FulcioURL),
		RekorURL:     firstNonEmpty(cfg.RekorURL, resolved.RekorURL),
		TimestampURL: firstNonEmpty(cfg.TimestampURL, resolved.TimestampURL),
	})
}
