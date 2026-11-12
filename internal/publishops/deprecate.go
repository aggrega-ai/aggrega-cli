// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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

	"github.com/aggrega-ai/aggrega-cli/internal/authoredshape"
	"github.com/aggrega-ai/aggrega-cli/internal/opprogress"
	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
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

	// From, when set, names a batch file of deprecation entries. Each marker's
	// reference and content are then read from the file's entries, and Reference and
	// the single-shot content fields are not read at all; ContentBesideBatch names
	// the content a caller would be refusing.
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

	// ConfiguredKeyNotConsulted withholds the signing key this workspace's own
	// configuration names, so only a KeyPath this caller supplied can select the
	// keyed branch. See SignerRequest.ConfiguredKeyNotConsulted for what it
	// declares; false is every caller that owns the machine the key sits on.
	ConfiguredKeyNotConsulted bool

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
		Workspace:                 cfg.Workspace,
		Signer:                    cfg.Signer,
		KeyPath:                   cfg.KeyPath,
		FulcioURL:                 cfg.FulcioURL,
		RekorURL:                  cfg.RekorURL,
		TimestampURL:              cfg.TimestampURL,
		IDToken:                   cfg.IDToken,
		EnvLookup:                 cfg.EnvLookup,
		ConfiguredKeyNotConsulted: cfg.ConfiguredKeyNotConsulted,
		AcquireIdentityToken:      interactiveAcquirer(cfg.TokenGetter, cfg.IsTTY),
		IdentitySupply:            cfg.IdentitySupply,
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
// The marker is signed through the same credential home and sign atom
// publication and signing use, so the deprecation marker is a signed artifact
// verifiable by the existing signature-verify machinery. The credential is
// resolved — and on the keyless path the identity token with it, once — before
// any marker is attached, so a signer that cannot be constructed refuses the
// deprecation before anything is written, and a batch whose entries would need
// different credentials is refused the same way (signing.ErrMixedSigningCredentials).
func Deprecate(ctx context.Context, cfg DeprecateConfig) (DeprecateResult, error) {
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}
	// Each marker is stated as it is attached, so its lines go where they land
	// clear of the step attaching it.
	out = opprogress.Beside(ctx, out)
	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	// Ahead of either arm, both of which attach a marker at the registry. A marker
	// is published content, so a withdrawal observed afterwards would withhold the
	// reply while the deprecation was already visible to every consumer.
	if serr := stopped(ctx); serr != nil {
		return DeprecateResult{}, serr
	}

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
	signer, err := resolveSigner(cfg.signerRequest(), cfg.Reference, nil)
	if err != nil {
		return DeprecateResult{}, err
	}
	end := opprogress.Begin(ctx, "attaching a deprecation marker to "+cfg.Reference)
	digest, err := registryindex.AttachSignedMarker(ctx, out, target, subjectRef, cfg.Reference, marker, signer)
	end(err)
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
//
// The credential is resolved once, over every entry's reference, before any
// marker is attached: entries that would need different credentials refuse the
// batch with nothing written, and a keyless batch acquires a single identity.
// Every entry's signer is then built — from that credential and the endpoints its
// own registry configures — still before the first marker is attached, so a key
// that cannot be read or an identity with no usable subject refuses the batch
// with nothing written too. Each entry's issuedOn is stamped at attach time.
func deprecateBatch(ctx context.Context, cfg DeprecateConfig, out io.Writer, now func() time.Time) (DeprecateResult, error) {
	entries, err := loadDeprecationBatch(cfg.From)
	if err != nil {
		return DeprecateResult{}, err
	}
	if len(entries) == 0 {
		return DeprecateResult{}, fmt.Errorf("%s carries no deprecation entries", cfg.From)
	}

	references := make([]string, len(entries))
	for i, e := range entries {
		if e.Reference == "" {
			return DeprecateResult{}, fmt.Errorf("%s: entry %d is missing its reference", cfg.From, i+1)
		}
		references[i] = e.Reference
	}
	cred, err := resolveCredential(cfg.signerRequest(), references, nil)
	if err != nil {
		return DeprecateResult{}, err
	}
	signers := make([]signing.Signer, len(entries))
	for i, ref := range references {
		if signers[i], err = cred.signerFor(ref); err != nil {
			return DeprecateResult{}, err
		}
	}

	var marked []DeprecationMarked
	for i, e := range entries {
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
		end := opprogress.Begin(ctx, "attaching a deprecation marker to "+e.Reference)
		digest, aerr := registryindex.AttachSignedMarker(ctx, out, target, subjectRef, e.Reference, marker, signers[i])
		end(aerr)
		if aerr != nil {
			return DeprecateResult{}, aerr
		}
		marked = append(marked, markedFrom(e.Reference, marker, digest))
	}
	return DeprecateResult{Marked: marked}, nil
}

// ContentBesideBatch names the single-shot marker content a request carries —
// severity, reason, replacement and message, in that fixed order, so one request
// always draws one answer — and returns nil when it carries none. A value of only
// white space counts as absent.
//
// A batch file supplies each marker's content from its own entry, so content sent
// beside one is read by nothing. This is what a surface reads to refuse that
// combination, naming each field it returns, rather than drop it: a field silently
// ignored reads to its sender as a field that was honoured, and on a withdrawal the
// sender would then be wrong about what every consumer is now told. A surface words
// the refusal in its own vocabulary; which fields count, and in what order, is
// decided here once.
func ContentBesideBatch(severity, reason, replacement, message string) []string {
	var sent []string
	for _, f := range []struct{ name, value string }{
		{"severity", severity},
		{"reason", reason},
		{"replacement", replacement},
		{"message", message},
	} {
		if strings.TrimSpace(f.value) != "" {
			sent = append(sent, f.name)
		}
	}
	return sent
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
		return nil, fmt.Errorf("parse batch file %q: %w", path, authoredshape.DescribeYAMLDecodeErrorIn(err, data))
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
			return nil, "", cerr
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
