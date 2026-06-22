// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

var (
	signKeyPath   string
	signFulcio    string
	signRekor     string
	signTimestamp string
	signIDToken   string
)

func newSignCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "sign <reference>",
		Short: "Sign an already-published artifact and attach the signature",
		Long: "Sign amends an already-published artifact by attaching a signature as an OCI referrer. " +
			"It does not re-push, re-tag, or otherwise mutate the published bytes — the artifact is immutable; " +
			"the signature is a sibling manifest pointing back at it. With --key the signature is produced offline " +
			"from a held ECDSA P-256 key; without --key the keyless flow is used, which requires a live identity " +
			"provider, Fulcio, and Rekor.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return signRun(signConfig{
				Reference:    args[0],
				KeyPath:      signKeyPath,
				FulcioURL:    signFulcio,
				RekorURL:     signRekor,
				TimestampURL: signTimestamp,
				IDToken:      signIDToken,
			})
		},
	}
	c.Flags().StringVar(&signKeyPath, "key", "", "Path to a held ECDSA P-256 private key PEM for offline keyed signing. When omitted, keyless signing is used.")
	c.Flags().StringVar(&signFulcio, "fulcio-url", "", "Fulcio certificate-authority URL for keyless signing.")
	c.Flags().StringVar(&signRekor, "rekor-url", "", "Rekor transparency-log URL for keyless signing.")
	c.Flags().StringVar(&signTimestamp, "timestamp-url", "", "Optional RFC3161 timestamp-authority URL for keyless signing.")
	c.Flags().StringVar(&signIDToken, "identity-token", "", "OIDC identity token for keyless signing (or read from the ambient OIDC environment).")
	return c
}

// signConfig is the input contract for the sign workflow. signRun reads
// only from cfg, so test bodies stay race-free under t.Parallel. Tests
// inject a Target and a Signer; production constructs both from the
// reference and the key/keyless flags.
type signConfig struct {
	Reference string

	// Keyed path.
	KeyPath string

	// Keyless path.
	FulcioURL    string
	RekorURL     string
	TimestampURL string
	IDToken      string

	// Target overrides the production remote.Repository construction.
	// Tests inject an in-memory oras.Target. When nil, signRun
	// constructs the target via oci.NewRemoteRepository from Reference.
	Target oras.Target

	// SubjectRef overrides the reference resolved against Target. Tests
	// set this to the in-memory tag; production derives the subject from
	// Reference's tag/digest.
	SubjectRef string

	// Signer overrides signer construction. Tests inject a fake; when
	// nil, signRun builds a keyed or keyless signer from the flags.
	Signer signing.Signer
}

// signRun resolves the published artifact named by cfg.Reference, signs
// its manifest digest, and attaches the signature bundle as an OCI
// referrer. The published artifact is never re-pushed or mutated; the
// referrer is a sibling manifest, so signing is immutability-safe.
func signRun(cfg signConfig) error {
	if cfg.Reference == "" {
		return fmt.Errorf("aggrega sign: reference required")
	}
	ctx := context.Background()

	target := cfg.Target
	if target == nil {
		repo, err := oci.NewRemoteRepository(cfg.Reference)
		if err != nil {
			return fmt.Errorf("aggrega sign: %s: %w", cfg.Reference, err)
		}
		target = repo
	}

	subjectRef := cfg.SubjectRef
	if subjectRef == "" {
		subjectRef = referenceTag(cfg.Reference)
	}

	// Resolve the published digest. signRun never re-pushes content — it
	// signs whatever digest the registry already holds for the reference.
	digest, exists, err := oci.Resolve(ctx, target, subjectRef)
	if err != nil {
		return fmt.Errorf("aggrega sign: resolve %s: %w", cfg.Reference, err)
	}
	if !exists {
		return fmt.Errorf("aggrega sign: %s is not published; nothing to sign", cfg.Reference)
	}

	signer := cfg.Signer
	if signer == nil {
		signer, err = signerFromFlags(cfg)
		if err != nil {
			return fmt.Errorf("aggrega sign: %w", err)
		}
	}

	bundleBytes, artifactType, err := signer.Sign(ctx, digest)
	if err != nil {
		return fmt.Errorf("aggrega sign: %s: %w", cfg.Reference, err)
	}
	if _, err := oci.AttachReferrer(ctx, target, subjectRef, artifactType, bundleBytes); err != nil {
		return fmt.Errorf("aggrega sign: attach signature to %s: %w", cfg.Reference, err)
	}

	fmt.Printf("Signature attached: %s (%s)\n", cfg.Reference, digest)
	return nil
}

// signerFromFlags builds a keyed signer when a key path is supplied, and
// a keyless signer otherwise. The keyless signer is live-only: its Sign
// contacts Fulcio, Rekor, and an OIDC identity provider.
func signerFromFlags(cfg signConfig) (signing.Signer, error) {
	if cfg.KeyPath != "" {
		pem, err := os.ReadFile(cfg.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("read key %s: %w", cfg.KeyPath, err)
		}
		return signing.LoadKeyedSigner(pem, nil)
	}
	return &signing.KeylessSigner{
		IDToken:      cfg.IDToken,
		FulcioURL:    cfg.FulcioURL,
		RekorURL:     cfg.RekorURL,
		TimestampURL: cfg.TimestampURL,
	}, nil
}

// referenceTag returns the tag or digest portion of an OCI reference.
// A digest reference (…@sha256:…) yields the digest; otherwise the tag
// after the path's last ":" is returned, taking care not to mistake a
// host:port colon for a tag separator. A reference with no tag returns
// the reference unchanged so the target's own resolution decides.
func referenceTag(reference string) string {
	if i := strings.LastIndexByte(reference, '@'); i >= 0 {
		return reference[i+1:]
	}
	tail := reference
	if slash := strings.LastIndexByte(reference, '/'); slash >= 0 {
		tail = reference[slash+1:]
	}
	if c := strings.LastIndexByte(tail, ':'); c >= 0 {
		return tail[c+1:]
	}
	return reference
}
