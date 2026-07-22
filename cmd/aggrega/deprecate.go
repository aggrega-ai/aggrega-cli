// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

var (
	deprecateSeverity    string
	deprecateReason      string
	deprecateReplacement string
	deprecateMessage     string
	deprecateFrom        string
	deprecateKeyPath     string
	deprecateFulcio      string
	deprecateRekor       string
	deprecateTimestamp   string
	deprecateIDToken     string
)

func newDeprecateCmd() *cobra.Command {
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
			"referrer of the marker, so the marker is verifiable by the existing signature-verify machinery. With " +
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
				Severity:     deprecateSeverity,
				Reason:       deprecateReason,
				Replacement:  deprecateReplacement,
				Message:      deprecateMessage,
				From:         deprecateFrom,
				KeyPath:      deprecateKeyPath,
				FulcioURL:    deprecateFulcio,
				RekorURL:     deprecateRekor,
				TimestampURL: deprecateTimestamp,
				IDToken:      deprecateIDToken,
				In:           cmd.InOrStdin(),
				ws:           rw,
			})
		},
	}
	c.Flags().StringVar(&deprecateSeverity, "severity", "", "Deprecation severity: one of warning, error, retracted. Required unless --from or an interactive prompt supplies it.")
	c.Flags().StringVar(&deprecateReason, "reason", "", "Human-readable reason for the deprecation — why it is deprecated and what to do instead. Required unless --from or an interactive prompt supplies it.")
	c.Flags().StringVar(&deprecateReplacement, "replacement", "", "Optional artifact reference that replaces the deprecated one.")
	c.Flags().StringVar(&deprecateMessage, "message", "", "Optional legacy message retained for audit and for the recorded build-time diagnostic; defaults to --reason when unset.")
	c.Flags().StringVar(&deprecateFrom, "from", "", "Batch file (YAML, or JSON as a subset) whose top-level list attaches one marker per entry. Each entry carries reference and severity and reason, plus optional replacement and message. Mutually exclusive with a positional reference.")
	c.Flags().StringVar(&deprecateKeyPath, "key", "", "Path to a held ECDSA P-256 private key PEM for offline keyed signing. When omitted, keyless signing is used.")
	c.Flags().StringVar(&deprecateFulcio, "fulcio-url", "", "Fulcio certificate-authority URL for keyless signing.")
	c.Flags().StringVar(&deprecateRekor, "rekor-url", "", "Rekor transparency-log URL for keyless signing.")
	c.Flags().StringVar(&deprecateTimestamp, "timestamp-url", "", "Optional RFC3161 timestamp-authority URL for keyless signing.")
	c.Flags().StringVar(&deprecateIDToken, "identity-token", "", "OIDC identity token for keyless signing. Optional at an interactive terminal (a browser flow acquires one) and on GitHub Actions (the workflow OIDC token is minted automatically); required for other non-interactive/CI use, where it may instead be exported as AGGREGA_IDENTITY_TOKEN or SIGSTORE_ID_TOKEN.")
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

	Severity    string
	Reason      string
	Replacement string
	Message     string

	// From, when set, names a batch file of deprecation entries. It is mutually
	// exclusive with a positional Reference and the single-shot content fields.
	From string

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

	// In is the source for interactive prompts when the single-shot content
	// flags are absent at a terminal. Nil defaults to os.Stdin; tests pass a
	// reader carrying the prompt answers.
	In io.Reader

	// Now supplies the issuedOn timestamp. Nil defaults to time.Now; tests
	// inject a frozen clock so the marshalled marker is deterministic.
	Now func() time.Time
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
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	ctx := context.Background()

	if cfg.From != "" {
		return deprecateBatch(ctx, cfg, out, now)
	}
	return deprecateSingle(ctx, cfg, out, now)
}

// deprecateSingle attaches one marker to cfg.Reference. Its content comes from
// the --severity/--reason/--replacement flags, or — when the required flags are
// absent at an interactive terminal — from a prompt. issuedOn is stamped now.
func deprecateSingle(ctx context.Context, cfg deprecateConfig, out io.Writer, now func() time.Time) error {
	severity, reason, replacement, err := resolveSingleContent(cfg, out)
	if err != nil {
		return err
	}
	marker := deprecation.Marker{
		Severity:    deprecation.Severity(severity),
		Message:     firstNonEmpty(cfg.Message, reason),
		Reason:      reason,
		Replacement: replacement,
		IssuedOn:    now().UTC().Format(time.RFC3339),
	}

	target, subjectRef, err := deprecateTargetFor(cfg, cfg.Reference)
	if err != nil {
		return err
	}

	// Build the signer BEFORE the marker is attached so a keyless flow with no
	// usable token, or a missing key, refuses the deprecation before writing.
	signer, err := deprecateSigner(cfg)
	if err != nil {
		return fmt.Errorf("aggrega deprecate: %w", err)
	}
	return attachSignedMarker(ctx, out, target, subjectRef, cfg.Reference, marker, signer)
}

// deprecateBatch attaches one marker per entry in the file named by cfg.From.
// The signer is built once from the first entry's registry and reused for every
// entry, so a keyless flow acquires a single identity for the whole batch. Each
// entry's issuedOn is stamped at attach time.
func deprecateBatch(ctx context.Context, cfg deprecateConfig, out io.Writer, now func() time.Time) error {
	entries, err := loadDeprecationBatch(cfg.From)
	if err != nil {
		return fmt.Errorf("aggrega deprecate: %w", err)
	}
	if len(entries) == 0 {
		return fmt.Errorf("aggrega deprecate: %s carries no deprecation entries", cfg.From)
	}

	signerCfg := cfg
	signerCfg.Reference = entries[0].Reference
	signer, err := deprecateSigner(signerCfg)
	if err != nil {
		return fmt.Errorf("aggrega deprecate: %w", err)
	}

	for i, e := range entries {
		if e.Reference == "" {
			return fmt.Errorf("aggrega deprecate: %s: entry %d is missing its reference", cfg.From, i+1)
		}
		marker := deprecation.Marker{
			Severity:    deprecation.Severity(e.Severity),
			Message:     firstNonEmpty(e.Message, e.Reason),
			Reason:      e.Reason,
			Replacement: e.Replacement,
			IssuedOn:    now().UTC().Format(time.RFC3339),
		}
		target, subjectRef, terr := deprecateTargetFor(cfg, e.Reference)
		if terr != nil {
			return terr
		}
		if aerr := attachSignedMarker(ctx, out, target, subjectRef, e.Reference, marker, signer); aerr != nil {
			return aerr
		}
	}
	return nil
}

// deprecationBatchEntry is one line of a --from batch file: the reference to
// deprecate plus the marker content. YAML decodes JSON too, so the same struct
// parses either surface.
type deprecationBatchEntry struct {
	Reference   string `yaml:"reference" json:"reference"`
	Severity    string `yaml:"severity" json:"severity"`
	Reason      string `yaml:"reason" json:"reason"`
	Replacement string `yaml:"replacement" json:"replacement"`
	Message     string `yaml:"message" json:"message"`
}

// loadDeprecationBatch reads and parses a --from batch file. The file is a
// top-level YAML (or JSON) list of entries.
func loadDeprecationBatch(path string) ([]deprecationBatchEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read batch file %q: %w", path, err)
	}
	var entries []deprecationBatchEntry
	if err := yaml.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse batch file %q: %w", path, err)
	}
	return entries, nil
}

// resolveSingleContent returns the severity, reason, and replacement for a
// single-shot deprecate. When both required fields are present as flags it
// returns them unchanged (the non-interactive path). Otherwise, at an
// interactive terminal, it prompts for the missing fields; away from a terminal
// it refuses with a precise message rather than blocking on a prompt no one can
// answer.
func resolveSingleContent(cfg deprecateConfig, out io.Writer) (severity, reason, replacement string, err error) {
	severity, reason, replacement = cfg.Severity, cfg.Reason, cfg.Replacement
	if severity != "" && reason != "" {
		return severity, reason, replacement, nil
	}
	if !ttyOrDefault(cfg.IsTTY)() {
		return "", "", "", fmt.Errorf("aggrega deprecate: --severity and --reason are required; provide them as flags, use --from <file>, or run at an interactive terminal to be prompted")
	}
	in := cfg.In
	if in == nil {
		in = os.Stdin
	}
	return promptDeprecationContent(in, out, severity, reason, replacement)
}

// promptDeprecationContent prompts for each absent content field on out and
// reads the answer from in. A blank replacement means none. It errors when a
// required field is still empty after prompting.
func promptDeprecationContent(in io.Reader, out io.Writer, severity, reason, replacement string) (string, string, string, error) {
	r := bufio.NewReader(in)
	read := func(prompt string) string {
		fmt.Fprint(out, prompt)
		line, _ := r.ReadString('\n')
		return strings.TrimSpace(line)
	}
	if severity == "" {
		severity = read("Severity (warning, error, retracted): ")
	}
	if reason == "" {
		reason = read("Reason: ")
	}
	if replacement == "" {
		replacement = read("Replacement (optional, blank for none): ")
	}
	if severity == "" || reason == "" {
		return "", "", "", fmt.Errorf("aggrega deprecate: severity and reason are required")
	}
	return severity, reason, replacement, nil
}

// deprecateTargetFor resolves the OCI target and subject reference for one
// reference. An injected cfg.Target (tests) is used as-is; otherwise a remote
// repository is constructed from the reference. The subject reference is the
// injected cfg.SubjectRef when set, else the reference's own tag.
func deprecateTargetFor(cfg deprecateConfig, reference string) (oras.Target, string, error) {
	target := cfg.Target
	if target == nil {
		repo, rerr := oci.NewRemoteRepository(reference)
		if rerr != nil {
			return nil, "", fmt.Errorf("aggrega deprecate: %s: %w", reference, rerr)
		}
		target = repo
	}
	subjectRef := cfg.SubjectRef
	if subjectRef == "" {
		subjectRef = referenceTag(reference)
	}
	return target, subjectRef, nil
}

// attachSignedMarker validates the subject is published, attaches the marker as
// an OCI referrer of the subject, then signs the marker's own manifest digest
// and attaches that signature as a referrer of the marker. The subject artifact
// is never re-pushed or mutated; both attachments are sibling manifests.
func attachSignedMarker(ctx context.Context, out io.Writer, target oras.Target, subjectRef, reference string, marker deprecation.Marker, signer signing.Signer) error {
	payload, err := deprecation.Marshal(marker)
	if err != nil {
		return fmt.Errorf("aggrega deprecate: %w", err)
	}

	// The subject must already be published: deprecate never publishes the
	// artifact it deprecates, it only amends it.
	if _, exists, rerr := oci.Resolve(ctx, target, subjectRef); rerr != nil {
		return fmt.Errorf("aggrega deprecate: resolve %s: %w", reference, rerr)
	} else if !exists {
		return fmt.Errorf("aggrega deprecate: %s is not published; nothing to deprecate", reference)
	}

	markerDesc, err := oci.AttachReferrer(ctx, target, subjectRef, deprecation.ArtifactType, payload)
	if err != nil {
		return fmt.Errorf("aggrega deprecate: attach marker to %s: %w", reference, err)
	}

	// Sign the marker's own manifest digest and attach the signature as a
	// referrer of the marker. The marker is an untagged referrer, so its
	// signature attaches by descriptor — a tag-based resolve cannot recover
	// its digest.
	bundle, artifactType, err := signDigest(ctx, signer, markerDesc.Digest.String())
	if err != nil {
		return fmt.Errorf("aggrega deprecate: sign marker for %s: %w", reference, err)
	}
	if _, err := oci.AttachReferrerToDescriptor(ctx, target, markerDesc, artifactType, bundle); err != nil {
		return fmt.Errorf("aggrega deprecate: attach signature to marker for %s: %w", reference, err)
	}

	fmt.Fprintf(out, "Deprecation attached: %s (%s, %s)\n", reference, marker.Severity, markerDesc.Digest)
	return nil
}

// deprecateSigner constructs the signer for the deprecation marker, reusing the
// single signer-construction path buildSigner. An injected signer is returned
// as-is; otherwise the keyed/keyless inputs are resolved through the precedence
// chain (CLI flag > per-registry config > global config) and, on the keyless
// branch, the identity is resolved as a token SOURCE through the single
// resolution home so an ambient GitHub Actions OIDC endpoint self-mints exactly
// as it does for a push. The keyed path never resolves an identity.
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
	var tokenSource signing.IdentityTokenSource
	if keyPath == "" {
		var terr error
		idToken, tokenSource, terr = resolveIdentityTokenSource(cfg.IDToken, envLookupOrGetenv(cfg.EnvLookup), ttyOrDefault(cfg.IsTTY), tokenGetterOrDefault(cfg.TokenGetter), checkIDTokenSubject)
		if terr != nil {
			return nil, terr
		}
	}
	return buildSigner(signerParams{
		KeyPath:      keyPath,
		IDToken:      idToken,
		TokenSource:  tokenSource,
		FulcioURL:    firstNonEmpty(cfg.FulcioURL, resolved.FulcioURL),
		RekorURL:     firstNonEmpty(cfg.RekorURL, resolved.RekorURL),
		TimestampURL: firstNonEmpty(cfg.TimestampURL, resolved.TimestampURL),
	})
}
