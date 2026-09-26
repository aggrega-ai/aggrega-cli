// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryops

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/registrytrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// Scope says how far a configuration write reaches.
//
// It is carried on every result rather than inferred by the reader, because the
// reach is not a property of the operation — it is a property of the file the
// write resolved to, and that resolution depends on a flag, on the operation's
// own nature, and on the environment. A surface that reconstructed the reach
// from the operation's name would be right until any one of those moved.
type Scope string

const (
	// ScopeMachine means the write landed in the global configuration file, which
	// every workspace on the machine resolves through. A registry defaults here.
	ScopeMachine Scope = "machine"
	// ScopeWorkspace means the write landed in this project's own configuration
	// file, and reaches nothing outside the workspace.
	ScopeWorkspace Scope = "workspace"
)

// WriteTarget is the configuration file a registry mutation resolved to, with
// how far that file reaches.
//
// Path and Scope are computed together from the one write-target policy, so a
// caller cannot be told a path from one resolution and a reach from another.
type WriteTarget struct {
	Path  string `json:"path"`
	Scope Scope  `json:"scope"`
}

// resolveWriteTarget resolves the configuration file a registry mutation writes
// to and classifies its reach.
//
// The path comes from the single write-target site, which is what makes the
// default correct: a registry is tool-scoped, so with neither override it
// resolves to the global file shared across every workspace on the machine. The
// reach is then read off the resolution rather than off the flags, because the
// flags are only two of its three inputs.
//
// 🔴 It resolves against THE SESSION'S OWN resolution, never against the ambient
// process environment, and that is not a default a caller may vary. The two come
// apart whenever the workspace was resolved with its global layer pointed at a
// particular file, and reading a configuration from one file while writing the
// change to another is a silent wrong answer. A test freezes the environment by
// resolving the workspace against a frozen reader, which is the same seam
// production uses rather than a second one beside it.
func resolveWriteTarget(rw *workspaceconfig.ResolvedWorkspace, global, workspace bool) (WriteTarget, error) {
	path, err := rw.WriteDestination(workspaceconfig.OpRegistry, global, workspace)
	if err != nil {
		return WriteTarget{}, err
	}
	scope := ScopeMachine
	if workspace || (!global && workspaceconfig.OpRegistry.Nature() != workspaceconfig.NatureTool) {
		scope = ScopeWorkspace
	}
	return WriteTarget{Path: path, Scope: scope}, nil
}

// AddOptions is what a registration decides, independently of who asked for it.
//
// Global and Workspace are the write-target overrides; leaving both unset takes
// the operation's own default, which for a registry is the global file. Setting
// both is a usage error the write-target policy refuses.
//
// FetchTrust enables the trust-on-first-use fetch. A manual policy — any
// Identity, or a Key — overrides the fetched set and skips the fetch, so
// FetchTrust is consulted only where no manual policy was supplied.
//
// RequireSignatures reports whether the requirement was stated at all and
// RequireValue carries what it was stated as, because "not stated" and "stated
// as false" are different: the hub baseline turns the requirement on by default,
// and an unstated flag must not switch it back off.
// Delivery selects whether the registration writes the configuration file or
// reports the content that write would have produced. The zero value writes, so
// a caller that says nothing about delivery registers exactly as before.
type AddOptions struct {
	Host              string
	Name              string
	Identities        []string
	KeyPath           string
	TrustedRoot       string
	RequireSignatures bool
	RequireValue      bool
	Advisory          bool
	FetchTrust        bool
	Global            bool
	Workspace         bool

	Delivery authoredcontent.Delivery

	// TrustClient transports the advertised-trust fetch. Nil selects a default
	// client in the fetch path; a test injects one so no live network is reached.
	TrustClient *http.Client
}

// TrustFetchOutcome is what the advertised-trust fetch did, as a value rather
// than as a line of prose.
//
// The three outcomes are distinguished because they mean different things to a
// caller. Pinned says the registry advertised a policy and it was taken.
// NotAdvertised says the registry advertised nothing, which is not a failure —
// the registration proceeds on the flags — and Reason carries what the endpoint
// did. Skipped says no fetch was attempted, because a manual policy overrode it
// or the caller asked for none.
type TrustFetchOutcome struct {
	Pinned        bool   `json:"pinned"`
	NotAdvertised bool   `json:"notAdvertised,omitempty"`
	Skipped       bool   `json:"skipped,omitempty"`
	Identities    int    `json:"identities,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

// AddResult is what a registration wrote and where.
//
// Host is the normalised host the entry is keyed under, which is not always the
// token the caller supplied. Target carries the file and its reach, so a caller
// is told the scope of what it just changed rather than left to infer it from
// the absence of a flag it did not send.
// Content is the configuration document the write would have produced, filled
// only under a reporting delivery and empty under a writing one. The file it is
// for is Target.Path — the one write-target resolution already on this result —
// so nothing here carries a second copy of the path.
type AddResult struct {
	Host    string            `json:"host"`
	Target  WriteTarget       `json:"target"`
	Trust   TrustFetchOutcome `json:"trust"`
	Content []byte            `json:"content,omitempty"`
}

// Add registers a registry host and its signature trust policy in the resolved
// configuration file, and reports what it wrote and where.
//
// Trust-policy precedence, which is the whole of what this decides:
//
//   - Manual flags win. When an identity or a key is supplied, the policy is
//     built from those alone and the advertised-trust fetch is skipped — the
//     operator has stated the trust set explicitly.
//   - Otherwise, trust-on-first-use. With FetchTrust set and no manual policy,
//     the registry's advertised trust document is fetched once from its own
//     origin and its identities pinned locally. Verification at pull time runs
//     against this pinned copy, never a live re-fetch.
//   - Graceful fallback. A registry that advertises nothing — a not-found, or an
//     unreachable endpoint — is not an error: the registration proceeds on the
//     flags and the result says the policy was not advertised. A document that
//     fetched but did not decode IS an error, because the endpoint answered with
//     content that is not a usable trust policy.
//
// The write target is resolved before any fetch, so a write-target misuse or an
// unresolvable global path fails before the network is reached.
//
// Under a reporting delivery it composes the same document and returns it on the
// result instead of writing it, touching no file and creating no directory. The
// trust-policy precedence above is unchanged by the delivery: the fetch is a read
// and happens either way, so the reported content is the content a write would
// have produced rather than a different document assembled for reporting.
// ctx ends the registration where the caller stops wanting it. It reaches the
// advertised-trust fetch, which is the only step here that leaves the machine
// and therefore the only one worth abandoning. A stop is a hard error rather
// than a tolerated not-advertised outcome, for the reason pinAdvertisedTrust
// states: the two are indistinguishable from the fetch's own error and they must
// not be treated alike, because one of them would have the registration write a
// trust policy the registry never declined.
func Add(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, opts AddOptions) (AddResult, error) {
	var res AddResult
	host := registryhost.Of(opts.Host)
	if host == "" {
		return res, errors.New("aggrega registry add: a registry host is required")
	}
	// The host the token names is keyed exactly as written, so it is held to the
	// one reading of a registry host before anything is fetched or written: a
	// host in capitals, with its default port, or with no dot and no port would
	// be stored under a key no lookup ever uses. The token may still be a
	// reference — its scheme, path and credentials are what Of drops — but the
	// host it names is not rewritten on the caller's behalf.
	if err := registryhost.Check("the host", host); err != nil {
		return res, fmt.Errorf("aggrega registry add: %w", err)
	}
	res.Host = host

	target, err := resolveWriteTarget(rw, opts.Global, opts.Workspace)
	if err != nil {
		return res, err
	}
	res.Target = target

	manualPolicy := len(opts.Identities) > 0 || opts.KeyPath != ""

	reg := workspaceconfig.RegistryConfig{Host: host}
	// Adding the official hub with no explicit manual policy pins the embedded
	// baseline (the maintainer and continuous-integration identity set, with
	// signatures required).
	if host == workspaceconfig.HubBaselineHost && !manualPolicy {
		reg = workspaceconfig.DefaultBaseline()
	}
	// The friendly alias rides on the same host-keyed entry regardless of how the
	// trust policy is assembled below. Empty leaves the entry host-only.
	reg.Name = opts.Name

	switch {
	case opts.FetchTrust && !manualPolicy:
		outcome, ferr := pinAdvertisedTrust(ctx, &reg, host, opts.TrustClient)
		if ferr != nil {
			return res, ferr
		}
		res.Trust = outcome
	default:
		res.Trust = TrustFetchOutcome{Skipped: true}
	}

	for _, spec := range opts.Identities {
		id, issuer, ok := strings.Cut(spec, "=")
		if !ok || id == "" || issuer == "" {
			return res, fmt.Errorf("aggrega registry add: --identity %q must be in the form identity=issuer", spec)
		}
		reg.TrustedIdentities = append(reg.TrustedIdentities, workspaceconfig.TrustedIdentity{
			Identity: id,
			Issuer:   issuer,
		})
	}

	if opts.KeyPath != "" {
		pem, rerr := os.ReadFile(opts.KeyPath)
		if rerr != nil {
			return res, fmt.Errorf("aggrega registry add: read public key %s: %w", opts.KeyPath, rerr)
		}
		reg.PublicKey = string(pem)
	}
	if opts.TrustedRoot != "" {
		reg.TrustedRoot = opts.TrustedRoot
	}
	if opts.RequireSignatures {
		reg.Signing.Required = opts.RequireValue
	}
	if opts.Advisory {
		reg.Advisory = true
	}

	// A reporting delivery composes the document and stops there. Seeding a
	// configuration produces content an operator will own and go on editing, so a
	// caller holding a document model is handed the content rather than finding
	// the file already changed underneath it.
	if opts.Delivery.Reports() {
		body, derr := workspaceconfig.RegistryDocument(target.Path, reg)
		if derr != nil {
			return res, derr
		}
		res.Content = body
		return res, nil
	}
	if err := workspaceconfig.UpsertRegistry(target.Path, reg); err != nil {
		return res, err
	}
	return res, nil
}

// pinAdvertisedTrust fetches host's advertised trust document and copies its
// fields onto reg, reporting which of the three outcomes happened.
//
// A registry that advertises nothing — a not-found mapped to the no-trust
// sentinel, or an unreachable endpoint reported as a transport error — is
// tolerated: it leaves reg's policy for the caller's manual flags, or the
// empty-or-baseline default, to fill. A MALFORMED advertised document, one that
// fetched but did not decode, is a hard error: the endpoint answered with
// content that is not a usable trust policy, which is a real misconfiguration
// the operator should see rather than a registry that simply advertises none.
//
// 🔴 A caller that withdrew is the third outcome, and it is a hard error — it
// must not be folded into the tolerated one. A cancelled fetch and an
// unreachable registry are indistinguishable from the transport error alone, and
// treating a withdrawal as "advertises nothing" would let the registration
// proceed and write a trust policy the registry never declined: the operator
// would be left holding a configuration that requires no signatures because a
// request was interrupted. The context is consulted rather than the error's
// prose, because a transport error's wording is not a contract.
func pinAdvertisedTrust(ctx context.Context, reg *workspaceconfig.RegistryConfig, host string, client *http.Client) (TrustFetchOutcome, error) {
	adv, ferr := registrytrust.Fetch(ctx, client, host)
	if ferr != nil {
		if cerr := ctx.Err(); cerr != nil {
			return TrustFetchOutcome{}, fmt.Errorf(
				"aggrega registry add: stopped before %s answered whether it advertises a trust policy: %w", host, cerr)
		}
		if errors.Is(ferr, registrytrust.ErrMalformedTrust()) {
			return TrustFetchOutcome{}, fmt.Errorf("aggrega registry add: %w", ferr)
		}
		return TrustFetchOutcome{NotAdvertised: true, Reason: ferr.Error()}, nil
	}

	reg.Signing.Required = adv.RequiresSignatures
	reg.TrustedIdentities = advertisedIdentities(adv.TrustedIdentities)
	reg.TrustedDeprecationIdentities = advertisedIdentities(adv.TrustedDeprecationIdentities)
	if adv.PublicKey != "" {
		reg.PublicKey = adv.PublicKey
	}
	return TrustFetchOutcome{Pinned: true, Identities: len(reg.TrustedIdentities)}, nil
}

// advertisedIdentities converts the wire-shape identities from an advertised
// trust document into the per-registry config's identity type. The two shapes
// are field-identical; the copy keeps the config package unaware of the
// advertised-trust wire type.
func advertisedIdentities(ids []registrytrust.AdvertisedIdentity) []workspaceconfig.TrustedIdentity {
	if len(ids) == 0 {
		return nil
	}
	out := make([]workspaceconfig.TrustedIdentity, 0, len(ids))
	for _, id := range ids {
		out = append(out, workspaceconfig.TrustedIdentity{
			Identity:       id.Identity,
			IdentityRegexp: id.IdentityRegexp,
			Issuer:         id.Issuer,
		})
	}
	return out
}

// RemoveOptions is what a removal decides. Token addresses the registry by host
// or by friendly alias; Global and Workspace are the same write-target
// overrides a registration takes, and default the same way.
type RemoveOptions struct {
	Token     string
	Global    bool
	Workspace bool
}

// RemoveResult is what a removal dropped and from where.
//
// Host is the token resolved to the host that was dropped, falling back to the
// token verbatim where the resolution is ambiguous — the removal itself matched
// on the token, so the fallback never misreports which entry went.
type RemoveResult struct {
	Host   string      `json:"host"`
	Target WriteTarget `json:"target"`
}

// Remove drops the registry entry addressed by token from the resolved
// configuration file, and reports what it dropped and from where.
//
// A token that matches nothing in that file fails loudly rather than silently
// rewriting an unchanged configuration: a removal that found nothing and
// reported success would leave a caller believing the machine's resolution had
// changed when it had not.
//
// The default_registry pointer is left untouched. Removing a registry's trust
// policy reverts that host to default trust; it does not change which registry
// is the default.
func Remove(rw *workspaceconfig.ResolvedWorkspace, opts RemoveOptions) (RemoveResult, error) {
	var res RemoveResult
	target, err := resolveWriteTarget(rw, opts.Global, opts.Workspace)
	if err != nil {
		return res, err
	}
	res.Target = target

	removed, err := workspaceconfig.RemoveRegistry(target.Path, opts.Token)
	if err != nil {
		return res, err
	}
	if !removed {
		return res, fmt.Errorf("aggrega registry remove: registry %q is not configured in %s", opts.Token, target.Path)
	}

	// Resolve the token to its host for the caller's confirmation, through the
	// same primitive the removal matched on. On the success path the entry
	// existed, so this resolves to the host that was dropped; an ambiguity or
	// other resolver error falls back to echoing the token verbatim.
	res.Host = opts.Token
	if h, rerr := rw.Config().ResolveRegistry(opts.Token); rerr == nil {
		res.Host = h
	}
	return res, nil
}
