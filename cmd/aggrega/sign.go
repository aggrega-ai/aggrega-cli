// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
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
				KeyPath:      signKeyPath,
				FulcioURL:    signFulcio,
				RekorURL:     signRekor,
				TimestampURL: signTimestamp,
				IDToken:      signIDToken,
				ws:           rw,
			})
		},
	}
	c.Flags().StringVar(&signKeyPath, "key", "", "Path to a held ECDSA P-256 private key PEM for offline keyed signing. When omitted, keyless signing is used.")
	c.Flags().StringVar(&signFulcio, "fulcio-url", "", "Fulcio certificate-authority URL for keyless signing.")
	c.Flags().StringVar(&signRekor, "rekor-url", "", "Rekor transparency-log URL for keyless signing.")
	c.Flags().StringVar(&signTimestamp, "timestamp-url", "", "Optional RFC3161 timestamp-authority URL for keyless signing.")
	c.Flags().StringVar(&signIDToken, "identity-token", "", "OIDC identity token for keyless signing. Optional at an interactive terminal (a browser flow acquires one); required for non-interactive/CI use, where it may instead be exported as AGGREGA_IDENTITY_TOKEN or SIGSTORE_ID_TOKEN.")
	return c
}

// signConfig is the input contract for the sign workflow. signRun reads
// only from cfg, so test bodies stay race-free under t.Parallel. Tests
// inject a Target and a Signer; production constructs both from the
// reference and the key/keyless flags.
type signConfig struct {
	References []string

	// Keyed path.
	KeyPath string

	// Keyless path.
	FulcioURL    string
	RekorURL     string
	TimestampURL string
	IDToken      string

	// Target overrides the production remote.Repository construction.
	// Tests inject an in-memory oras.Target shared across all references.
	// When nil, signRun constructs a target per reference via
	// oci.NewRemoteRepository.
	Target oras.Target

	// SubjectRefs maps a reference to the subject resolved against Target.
	// Tests set the in-memory tag for a reference; a reference absent from
	// the map (or a nil map) derives its subject from its own tag/digest.
	SubjectRefs map[string]string

	// Signer overrides signer construction. Tests inject a fake; when
	// nil, signRun builds a keyed or keyless signer from the flags.
	Signer signing.Signer

	// ws is the resolved workspace, threaded in by the command's RunE. Nil only
	// when a test invokes signRun directly; workspaceOrResolve then resolves
	// from the working directory.
	ws *workspaceconfig.ResolvedWorkspace

	// EnvLookup reads the environment for the identity-token secret resolution.
	// Nil means production (os.Getenv); tests inject a frozen lookup so they
	// stay race-free under t.Parallel.
	EnvLookup func(string) string

	// TokenGetter acquires a keyless OIDC identity token interactively when no
	// explicit token is supplied. Nil means production
	// (acquireInteractiveIDToken); tests inject a fake so no browser opens.
	TokenGetter func() (string, error)

	// IsTTY reports whether stdin is an interactive terminal, gating the
	// interactive acquisition path. Nil means production (isTerminal); tests
	// inject a fixed value so the decision is deterministic.
	IsTTY func() bool

	// Out is the sink for the per-reference "Signature attached" status line.
	// Nil defaults to os.Stdout at the top of signRun (production); tests pass
	// a buffer to capture the output without mutating a process global.
	Out io.Writer
}

// signRun signs every reference in cfg.References, attaching each signature
// bundle as an OCI referrer of its already-published artifact. The published
// artifacts are never re-pushed or mutated; each referrer is a sibling
// manifest, so signing is immutability-safe.
//
// One interactive authentication serves the whole batch: on the keyless path
// the identity token is acquired once before the loop and reused for every
// reference, while a fresh short-lived certificate is issued per artifact by
// the per-reference signer. The keyed path acquires no token.
//
// Signing is continue-and-report: a reference that fails to resolve, sign, or
// attach is recorded and the batch proceeds to the next reference; after the
// loop, if any reference failed, a summary is printed to stderr and a non-zero
// error is returned. One keyless exception interrupts the per-reference flow:
// when an interactively-acquired token is rejected mid-batch (it may have
// expired), the token is re-acquired once for the whole batch and the current
// reference is retried; a supplied token is never silently re-prompted.
func signRun(cfg signConfig) error {
	if len(cfg.References) == 0 {
		return fmt.Errorf("aggrega sign: at least one reference required")
	}
	ctx := context.Background()
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}

	// Resolve the batch's signing posture once. The identity token is
	// registry-independent — it identifies the signer, not the destination —
	// so the keyless branch acquires it a single time here and threads it to
	// every per-reference signer. The token getter runs even when a Signer is
	// injected so the acquire-once accounting holds for both the production and
	// the injected-signer paths.
	b := &batchSigner{cfg: cfg, out: out}
	if err := b.prepare(); err != nil {
		return err
	}

	var failures []refFailure
	for _, ref := range cfg.References {
		if err := b.signRef(ctx, ref); err != nil {
			failures = append(failures, refFailure{ref: ref, err: err})
		}
	}

	if len(failures) > 0 {
		var sb strings.Builder
		fmt.Fprintf(&sb, "aggrega sign: %d of %d references failed:", len(failures), len(cfg.References))
		for _, f := range failures {
			fmt.Fprintf(&sb, "\n  %s: %v", f.ref, f.err)
		}
		return errors.New(sb.String())
	}
	return nil
}

// refFailure pairs a reference with the error that stopped it, so the batch can
// continue past it and report every failure together at the end.
type refFailure struct {
	ref string
	err error
}

// batchSigner carries the once-resolved signing posture across the batch loop:
// whether the keyless identity was interactively acquired (so it may be
// re-acquired on rejection), the single acquired token, and the latch that
// bounds re-acquisition to once per batch.
type batchSigner struct {
	cfg signConfig

	// keyless reports whether the batch uses the keyless flow. The keyed path
	// never acquires or re-acquires a token.
	keyless bool
	// tokenWasInteractive is true iff the keyless token was acquired
	// interactively (no flag/env supplied), so a rejected token may be
	// re-acquired. A supplied token is never silently re-prompted.
	tokenWasInteractive bool
	// idToken is the single acquired/supplied identity token, reused for every
	// reference's signer.
	idToken string
	// reacquired latches true once the token has been re-acquired, bounding
	// re-acquisition to once for the whole batch regardless of how many
	// references are rejected.
	reacquired bool

	// resolved holds the workspace-config signing overlay for the batch. The
	// per-reference signer applies it together with the CLI flags.
	keyPath string

	// out is the resolved sink for the per-reference status line, set once by
	// signRun (os.Stdout in production).
	out io.Writer
}

// prepare resolves the batch's signing posture once: it decides keyless vs
// keyed and, on the keyless branch, acquires the identity token a single time.
// When cfg.Signer is injected the per-reference signer is the injected one, but
// the keyless acquire-once block still runs so the token getter is invoked at
// most once for the whole batch — the property the batch contract guarantees.
func (b *batchSigner) prepare() error {
	if b.cfg.Signer == nil {
		rw, err := workspaceOrResolve(b.cfg.ws)
		if err != nil {
			return fmt.Errorf("workspaceconfig: %w", err)
		}
		// The key path is registry-independent in the common case (a single
		// --key or global config entry). Resolve it once from the first
		// reference's host to decide keyless vs keyed for the whole batch; a
		// per-registry key path is still applied per reference in signRef.
		resolved := rw.Config().ResolveSigning(registryhost.Of(b.cfg.References[0]))
		b.keyPath = firstNonEmpty(b.cfg.KeyPath, resolved.KeyPath)
		b.keyless = b.keyPath == ""
	} else {
		// An injected signer stands in for whatever the flags would build. The
		// keyless acquire-once block still runs unless a key path is supplied,
		// so the acquire-count assertion holds for the injected path too.
		b.keyless = b.cfg.KeyPath == ""
	}

	if b.keyless {
		look := envLookupOrGetenv(b.cfg.EnvLookup)
		b.tokenWasInteractive = resolveIDToken(b.cfg.IDToken, look) == ""
		tok, err := resolveOrAcquireIDToken(b.cfg.IDToken, look, ttyOrDefault(b.cfg.IsTTY), tokenGetterOrDefault(b.cfg.TokenGetter))
		if err != nil {
			return fmt.Errorf("aggrega sign: %w", err)
		}
		b.idToken = tok
	}
	return nil
}

// signRef signs one reference and attaches its signature. It builds the
// per-reference signer from the batch's once-acquired token (keyless) or the
// resolved key path (keyed) — never acquiring a token itself; the acquire-once
// lives in prepare. On a keyless signing failure with an interactively-acquired
// token and the per-batch re-acquire latch unset, it re-acquires once, rebuilds
// the signer with the fresh token, and retries this reference a single time.
func (b *batchSigner) signRef(ctx context.Context, ref string) error {
	target := b.cfg.Target
	if target == nil {
		repo, err := oci.NewRemoteRepository(ref)
		if err != nil {
			return fmt.Errorf("%s: %w", ref, err)
		}
		target = repo
	}

	subjectRef := b.cfg.SubjectRefs[ref]
	if subjectRef == "" {
		subjectRef = referenceTag(ref)
	}

	// Resolve the published digest. signRef never re-pushes content — it signs
	// whatever digest the registry already holds for the reference.
	digest, exists, err := oci.Resolve(ctx, target, subjectRef)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", ref, err)
	}
	if !exists {
		return fmt.Errorf("%s is not published; nothing to sign", ref)
	}

	signer, err := b.buildRefSigner(ref)
	if err != nil {
		return fmt.Errorf("%s: %w", ref, err)
	}

	bundleBytes, artifactType, err := signDigest(ctx, signer, digest)
	if err != nil {
		// A keyless cert-issuance failure has no classifiable upstream signal —
		// the certificate authority's rejection arrives as an opaque
		// string-wrapped error, indistinguishable from a transient outage. When
		// the token was interactively acquired and the per-batch latch is unset,
		// treat the failure as a possibly-expired token: re-acquire once, rebuild
		// the signer, and retry this reference a single time. A supplied token is
		// never silently re-prompted; the batch fails loudly so the operator can
		// supply a fresh one.
		if b.keyless && !b.reacquired {
			if !b.tokenWasInteractive {
				return fmt.Errorf("%s: signing failed; the supplied identity token was rejected (it may have expired); re-export a fresh %s or pass a fresh --identity-token: %w", ref, envIdentityToken, err)
			}
			fresh, aerr := tokenGetterOrDefault(b.cfg.TokenGetter)()
			b.reacquired = true
			if aerr != nil {
				return fmt.Errorf("%s: signing failed and re-acquiring a fresh identity token failed: %w", ref, aerr)
			}
			b.idToken = fresh
			signer, err = b.buildRefSigner(ref)
			if err != nil {
				return fmt.Errorf("%s: %w", ref, err)
			}
			bundleBytes, artifactType, err = signDigest(ctx, signer, digest)
			if err != nil {
				return fmt.Errorf("%s: signing failed; retried once with a fresh identity token (it may have expired): %w", ref, err)
			}
		} else {
			return fmt.Errorf("%s: %w", ref, err)
		}
	}

	if err := attachBundle(ctx, target, subjectRef, artifactType, bundleBytes); err != nil {
		return fmt.Errorf("attach signature to %s: %w", ref, err)
	}

	fmt.Fprintf(b.out, "Signature attached: %s (%s)\n", ref, digest)
	return nil
}

// buildRefSigner returns the signer for one reference. An injected signer is
// reused as-is across the batch; otherwise a fresh signer is built per
// reference from that reference's resolved endpoint URLs and the batch's
// once-acquired token (keyless) or key path (keyed). It never acquires a
// token — the keyless acquire-once is owned by prepare.
func (b *batchSigner) buildRefSigner(ref string) (signing.Signer, error) {
	if b.cfg.Signer != nil {
		return b.cfg.Signer, nil
	}
	rw, err := workspaceOrResolve(b.cfg.ws)
	if err != nil {
		return nil, fmt.Errorf("workspaceconfig: %w", err)
	}
	resolved := rw.Config().ResolveSigning(registryhost.Of(ref))
	return buildSigner(signerParams{
		KeyPath:      firstNonEmpty(b.cfg.KeyPath, resolved.KeyPath),
		IDToken:      b.idToken,
		FulcioURL:    firstNonEmpty(b.cfg.FulcioURL, resolved.FulcioURL),
		RekorURL:     firstNonEmpty(b.cfg.RekorURL, resolved.RekorURL),
		TimestampURL: firstNonEmpty(b.cfg.TimestampURL, resolved.TimestampURL),
	})
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
