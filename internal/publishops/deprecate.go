// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// DeprecateConfig is the input contract for the deprecation-marking workflow.
// Deprecate reads only from cfg, so concurrent callers cannot observe each
// other. The signing-input fields mirror SignConfig so deprecation marking
// reuses the exact signer-construction path publication and signing use.
type DeprecateConfig struct {
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

	// Target overrides the production remote-repository construction. A caller
	// may inject an in-memory oras.Target; when nil, Deprecate constructs the
	// target from the reference.
	Target oras.Target

	// SubjectRef overrides the subject reference resolved against Target. Empty
	// derives the subject from the reference's own tag/digest.
	SubjectRef string

	// Signer overrides signer construction. When nil, Deprecate builds a keyed
	// or keyless signer from the resolved signing inputs.
	Signer signing.Signer

	// Workspace is the resolved workspace the caller threads in. Required:
	// Deprecate refuses a nil rather than resolving one of its own.
	Workspace *workspaceconfig.ResolvedWorkspace

	// EnvLookup reads the environment for the identity-token secret
	// resolution. Nil means the live process environment.
	EnvLookup func(string) string

	// TokenGetter acquires a keyless OIDC identity token interactively when no
	// explicit token is supplied. Nil is the unattended shape and is the
	// default: no acquisition is attempted and the shared identity ladder
	// refuses, naming what to supply.
	TokenGetter func() (string, error)

	// IsTTY reports whether a person is watching. It gates BOTH the interactive
	// identity rung and the content prompt below. Nil reports false, so an
	// unattended caller can reach neither: missing content HALTS, naming what to
	// supply, rather than blocking on an answer that will never come.
	IsTTY func() bool

	// IdentitySupply carries the caller's own vocabulary into the identity
	// ladder's refusal, so a caller is told to supply something it can supply.
	// Zero yields a refusal naming only the inputs every caller shares.
	IdentitySupply signing.IdentityTokenSupply

	// Out is the sink for the status line. Nil defaults to os.Stdout, so a
	// caller that wants to capture the output passes a buffer rather than
	// mutating a process global.
	Out io.Writer

	// In is the source for the content prompt when the single-shot content
	// fields are absent AND IsTTY reports a person is watching. Nil defaults to
	// os.Stdin.
	In io.Reader

	// Now supplies the issuedOn timestamp. Nil defaults to time.Now; a caller
	// wanting a deterministic marker supplies a frozen clock.
	Now func() time.Time
}

// signerRequest projects the deprecation's credential inputs onto the shared
// credential boundary's input contract.
func (cfg DeprecateConfig) signerRequest() SignerRequest {
	return SignerRequest{
		Workspace:            cfg.Workspace,
		Registry:             cfg.Reference,
		Signer:               cfg.Signer,
		KeyPath:              cfg.KeyPath,
		FulcioURL:            cfg.FulcioURL,
		RekorURL:             cfg.RekorURL,
		TimestampURL:         cfg.TimestampURL,
		IDToken:              cfg.IDToken,
		EnvLookup:            cfg.EnvLookup,
		AcquireIdentityToken: interactiveAcquirer(cfg.TokenGetter, cfg.IsTTY),
		IdentitySupply:       cfg.IdentitySupply,
	}
}

// tty reports whether a person is watching. A nil check reports false, which is
// the unattended default: neither the interactive identity rung nor the content
// prompt is reachable without a caller declaring otherwise.
func (cfg DeprecateConfig) tty() bool {
	return cfg.IsTTY != nil && cfg.IsTTY()
}

// DeprecateResult reports which markers were attached, in the order they were
// attached, as the facts themselves rather than as the status lines Deprecate also
// writes to cfg.Out. A single-shot deprecation reports one entry and a batch
// reports one per entry.
type DeprecateResult struct {
	Marked []DeprecationMarked
}

// DeprecationMarked is one attached marker: the artifact it withdraws, the
// severity it carries, the timestamp it was stamped with, and the marker's own
// manifest digest.
//
// The digest is the marker's identity. A marker is attached as an untagged
// referrer of its subject, so nothing else addresses it, and a caller told only
// that a deprecation succeeded could not name what was attached.
type DeprecationMarked struct {
	Reference string
	Severity  string
	IssuedOn  string
	Digest    string
}

// Deprecate attaches a signed deprecation marker to the already-published
// artifact named by cfg.Reference. It validates the severity/message, resolves
// the subject's published digest, attaches the deprecation payload as an OCI
// referrer of the subject, then signs the marker's own manifest digest and
// attaches that signature as a referrer of the marker. The subject artifact is
// never re-published or mutated; both attachments are sibling manifests.
//
// The marker is signed through the same sign atom publication and signing use,
// so the deprecation marker is a signed artifact verifiable by the existing
// signature-verify machinery. On the keyless path the identity token is resolved
// once before the marker is attached, so a signer that cannot be constructed
// refuses the deprecation before anything is written.
func Deprecate(cfg DeprecateConfig) (DeprecateResult, error) {
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
// the caller's severity/reason/replacement fields, or — when the required fields
// are absent and a person is watching — from a prompt. issuedOn is stamped now.
func deprecateSingle(ctx context.Context, cfg DeprecateConfig, out io.Writer, now func() time.Time) (DeprecateResult, error) {
	severity, reason, replacement, err := resolveSingleContent(cfg, out)
	if err != nil {
		return DeprecateResult{}, err
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
		return DeprecateResult{}, err
	}

	// Build the signer BEFORE the marker is attached so a keyless flow with no
	// usable token, or a missing key, refuses the deprecation before writing.
	signer, err := deprecateSigner(cfg)
	if err != nil {
		return DeprecateResult{}, err
	}
	digest, err := registryindex.AttachSignedMarker(ctx, out, target, subjectRef, cfg.Reference, marker, signer)
	if err != nil {
		return DeprecateResult{}, err
	}
	return DeprecateResult{Marked: []DeprecationMarked{markedFrom(cfg.Reference, marker, digest)}}, nil
}

// markedFrom projects one attached marker onto the reported shape, so the
// single-shot and batch paths report the same fields from the same place.
func markedFrom(reference string, marker deprecation.Marker, digest string) DeprecationMarked {
	return DeprecationMarked{
		Reference: reference,
		Severity:  string(marker.Severity),
		IssuedOn:  marker.IssuedOn,
		Digest:    digest,
	}
}

// deprecateBatch attaches one marker per entry in the file named by cfg.From.
// The signer is built once from the first entry's registry and reused for every
// entry, so a keyless flow acquires a single identity for the whole batch. Each
// entry's issuedOn is stamped at attach time.
func deprecateBatch(ctx context.Context, cfg DeprecateConfig, out io.Writer, now func() time.Time) (DeprecateResult, error) {
	entries, err := loadDeprecationBatch(cfg.From)
	if err != nil {
		return DeprecateResult{}, err
	}
	if len(entries) == 0 {
		return DeprecateResult{}, fmt.Errorf("%s carries no deprecation entries", cfg.From)
	}

	signerCfg := cfg
	signerCfg.Reference = entries[0].Reference
	signer, err := deprecateSigner(signerCfg)
	if err != nil {
		return DeprecateResult{}, err
	}

	var marked []DeprecationMarked
	for i, e := range entries {
		if e.Reference == "" {
			return DeprecateResult{}, fmt.Errorf("%s: entry %d is missing its reference", cfg.From, i+1)
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
			return DeprecateResult{}, terr
		}
		digest, aerr := registryindex.AttachSignedMarker(ctx, out, target, subjectRef, e.Reference, marker, signer)
		if aerr != nil {
			return DeprecateResult{}, aerr
		}
		marked = append(marked, markedFrom(e.Reference, marker, digest))
	}
	return DeprecateResult{Marked: marked}, nil
}

// deprecationBatchEntry is one line of a batch file: the reference to
// deprecate plus the marker content. YAML decodes JSON too, so the same struct
// parses either surface.
type deprecationBatchEntry struct {
	Reference   string `yaml:"reference" json:"reference"`
	Severity    string `yaml:"severity" json:"severity"`
	Reason      string `yaml:"reason" json:"reason"`
	Replacement string `yaml:"replacement" json:"replacement"`
	Message     string `yaml:"message" json:"message"`
}

// loadDeprecationBatch reads and parses a batch file. The file is a
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
// single-shot deprecation. When both required fields are supplied it
// returns them unchanged (the unattended path). Otherwise, when a person is
// watching, it prompts for the missing fields; with nobody watching it refuses
// with a precise message rather than blocking on a prompt no one can answer.
func resolveSingleContent(cfg DeprecateConfig, out io.Writer) (severity, reason, replacement string, err error) {
	severity, reason, replacement = cfg.Severity, cfg.Reason, cfg.Replacement
	if severity != "" && reason != "" {
		return severity, reason, replacement, nil
	}
	if !cfg.tty() {
		return "", "", "", fmt.Errorf("--severity and --reason are required; provide them as flags, use --from <file>, or run at an interactive terminal to be prompted")
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
		return "", "", "", fmt.Errorf("severity and reason are required")
	}
	return severity, reason, replacement, nil
}

// deprecateTargetFor resolves the OCI target and subject reference for one
// reference. A supplied cfg.Target is used as-is; otherwise a remote
// repository is constructed from the reference. The subject reference is the
// supplied cfg.SubjectRef when set, else the reference's own tag.
func deprecateTargetFor(cfg DeprecateConfig, reference string) (oras.Target, string, error) {
	target := cfg.Target
	if target == nil {
		rw, cerr := requireWorkspace(cfg.Workspace)
		if cerr != nil {
			return nil, "", fmt.Errorf("workspaceconfig: %w", cerr)
		}
		httpClient, cerr := egressClient(rw.Config())
		if cerr != nil {
			return nil, "", fmt.Errorf("%s: %w", reference, cerr)
		}
		repo, rerr := oci.NewRemoteRepository(reference, httpClient)
		if rerr != nil {
			return nil, "", fmt.Errorf("%s: %w", reference, rerr)
		}
		target = repo
	}
	subjectRef := cfg.SubjectRef
	if subjectRef == "" {
		subjectRef = referenceTag(reference)
	}
	return target, subjectRef, nil
}

// deprecateSigner constructs the signer for the deprecation marker, reusing the
// single signer-construction path buildSigner. A supplied signer is returned
// as-is; otherwise the keyed/keyless inputs are resolved through the precedence
// chain (caller-supplied > per-registry config > global config) and, on the
// keyless branch, the identity is resolved as a token SOURCE through the single
// resolution home so an ambient GitHub Actions OIDC endpoint self-mints exactly
// as it does for a publication. The keyed path never resolves an identity.
func deprecateSigner(cfg DeprecateConfig) (signing.Signer, error) {
	if cfg.Signer != nil {
		return cfg.Signer, nil
	}
	rw, err := requireWorkspace(cfg.Workspace)
	if err != nil {
		return nil, fmt.Errorf("workspaceconfig: %w", err)
	}
	resolved := rw.Config().ResolveSigning(registryhost.Of(cfg.Reference))
	keyPath := firstNonEmpty(cfg.KeyPath, resolved.KeyPath)

	var idToken string
	var tokenSource signing.IdentityTokenSource
	if keyPath == "" {
		var terr error
		idToken, tokenSource, terr = resolveIdentity(cfg.signerRequest(), rw.Config())
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
		Config:       rw.Config(),
	})
}
