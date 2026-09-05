// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"

	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/registrytrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// attachRegistryCommands builds the `registry` command family under parent: the
// grouping command, then each subcommand named by the operation it carries.
//
// The group is attached before its children so that a child's invocation path is
// the one it will be invoked by when the registrar checks it against the
// operation table.
func attachRegistryCommands(parent *cobra.Command) {
	c := &cobra.Command{
		Use:   "registry",
		Short: "Manage registries and their signature trust policy",
		Long: `Manage the OCI registries Aggrega publishes to and resolves from, together with
the signature trust policy for each.

A registry entry records where to reach the registry and what to require of the
signatures on artifacts it serves — which identities are trusted, and whether a
signature is required at all. That policy is what "aggrega pull" enforces when
it verifies an artifact and what "aggrega push" honours when it decides whether
to sign.

"aggrega registry add" can fetch a registry's own advertised trust policy rather
than having you write it out. "aggrega registry update" builds the local
searchable index that "aggrega search" and "aggrega plugin add" read.

Which config file an entry is written to follows the same rule as every other
setting: the workspace file by default, the global file with --global.`,
		Example: `  # Configured registries and their trust posture
  aggrega registry list

  # Register a registry, taking its advertised trust policy
  aggrega registry add registry.example.com

  # Refresh the local searchable index
  aggrega registry update

  # Remove an entry, by host or by alias
  aggrega registry remove registry.example.com`,
	}
	mustAttachGroup(parent, c)
	mustAttach(c, newRegistryAddCmd(), "registry-add")
	mustAttach(c, newRegistryListCmd(), "registry-list")
	mustAttach(c, newRegistryRemoveCmd(), "registry-remove")
	mustAttach(c, newRegistryUpdateCmd(), "registry-update")
}

// newRegistryListCmd builds `registry list`, which prints the effective merged
// set of configured registries with each entry's alias and trust posture.
func newRegistryListCmd() *cobra.Command {
	var output string
	c := &cobra.Command{
		Use:   "list",
		Short: "List configured registries and their trust posture",
		Long: `List every registry configured in the effective merged config, one per line,
with its friendly alias (if any), a short trust-posture summary, and a marker on
the default registry.

This reads the resolved configuration and never contacts a registry. Registries
are listed in ascending host order.

With -o json the same set is emitted as one structured document per registry:
its host, its alias, whether it is the default, its trust posture as discrete
fields rather than the table's rendered sentence, and whether a credential for
that host is present in the shared credential store. The credential state reports
presence only — a credential is never read out, measured, or shown in any form,
and whether the registry would accept it is not asked, since that would take a
network round trip. Reading it consults the local credential store, which honors
a configured credential helper. The order is the same ascending host order the
table uses.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := parseFormat("registry list", output, "table", "json")
			if err != nil {
				return err
			}
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return registryListRun(registryListConfig{
				ws:   rw,
				Out:  cmd.OutOrStdout(),
				JSON: format == "json",
			})
		},
	}
	c.Flags().StringVarP(&output, "output", "o", "table", "Output format: table|json.")
	return c
}

// registryListConfig is the input contract for `aggrega registry list`,
// populated by the cobra command from its flags.
type registryListConfig struct {
	ws  *workspaceconfig.ResolvedWorkspace
	Out io.Writer
	// JSON selects the structured rendering over the human table. Both render the
	// same resolved model, so the two cannot describe the same registry set
	// differently.
	JSON bool
	// store reads whether a credential is present for a host. Nil selects the
	// shared OCI credential store, opened only on the structured path — the table
	// shows no credential state, so the default invocation touches no credential
	// store at all. Tests inject an isolated store.
	store credentials.Store
}

// registryListEntry is one registry as both renderings see it: the resolved
// config entry, whether it is the default, and the credential state resolved for
// its host. It exists so the table and the structured document project one model
// rather than each assembling its own.
type registryListEntry struct {
	cfg       workspaceconfig.RegistryConfig
	isDefault bool
	auth      string
}

// registryAuth* are the credential states a listing reports for a host. They say
// whether a credential is present, never what it is and never whether the
// registry would accept it — establishing the latter takes a network round trip,
// which a listing does not make.
const (
	// registryAuthConfigured means the credential store holds an entry for the host.
	registryAuthConfigured = "configured"
	// registryAuthNone means it holds none.
	registryAuthNone = "none"
	// registryAuthUnknown means the credential store could not be consulted, so
	// presence is genuinely undetermined rather than absent.
	registryAuthUnknown = "unknown"
)

// resolveRegistryList returns the effective registries in ascending host order,
// each with its default marker and, when authOf is non-nil, its credential state.
//
// The order is a total sort over the host, which is the entry's identity and
// therefore unique across the merged set — so the sequence is fixed for a given
// configuration regardless of which files contributed which entries or in what
// order they merged. Both renderings consume this one sequence, which is what
// keeps them from disagreeing about order.
func resolveRegistryList(cfg workspaceconfig.Config, authOf func(host string) string) []registryListEntry {
	regs := make([]workspaceconfig.RegistryConfig, len(cfg.Registries))
	copy(regs, cfg.Registries)
	sort.Slice(regs, func(i, j int) bool { return regs[i].Host < regs[j].Host })

	out := make([]registryListEntry, 0, len(regs))
	for _, r := range regs {
		e := registryListEntry{cfg: r, isDefault: r.Host == cfg.DefaultRegistry}
		if authOf != nil {
			e.auth = authOf(r.Host)
		}
		out = append(out, e)
	}
	return out
}

// registryListRun renders the effective registries in ascending host order. It
// reads the merged effective config — the same set resolution and push consult —
// and marks the default registry.
func registryListRun(cfg registryListConfig) error {
	rw, err := workspaceOrResolve(cfg.ws)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}
	wsCfg := rw.Config()

	if cfg.JSON {
		return writeRegistryListJSON(cfg.Out, resolveRegistryList(wsCfg, registryAuthResolver(cfg.store)))
	}

	entries := resolveRegistryList(wsCfg, nil)
	if len(entries) == 0 {
		fmt.Fprintln(cfg.Out, "No registries configured.")
		return nil
	}

	w := tabwriter.NewWriter(cfg.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "HOST\tALIAS\tTRUST\tDEFAULT")
	for _, e := range entries {
		marker := ""
		if e.isDefault {
			marker = "*"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.cfg.Host, e.cfg.Name, registryTrustSummary(e.cfg), marker)
	}
	return w.Flush()
}

// registryAuthResolver returns a per-host credential-state lookup over store,
// opening the shared OCI credential store when store is nil.
//
// A store that cannot be opened yields registryAuthUnknown for every host rather
// than failing the listing: the credential state is one column of a read-only
// enumeration, and an unreadable keychain is no reason to withhold the registry
// set. The returned function reports presence by asking the store for the host's
// entry; it never inspects, measures, or retains the credential.
func registryAuthResolver(store credentials.Store) func(host string) string {
	if store == nil {
		s, err := credentials.NewStoreFromDocker(credentials.StoreOptions{})
		if err != nil {
			return func(string) string { return registryAuthUnknown }
		}
		store = s
	}
	return func(host string) string {
		cred, err := store.Get(context.Background(), host)
		if err != nil {
			return registryAuthUnknown
		}
		if cred == auth.EmptyCredential {
			return registryAuthNone
		}
		return registryAuthConfigured
	}
}

// registryListDoc is the structured -o json listing: every configured registry
// in the same ascending host order the table uses. The slice is non-nil on the
// wire even when nothing is configured, so a consumer iterates unconditionally
// instead of special-casing null.
type registryListDoc struct {
	Registries []registryDoc `json:"registries"`
}

// registryDoc is one registry on the -o json surface. Host is the identity;
// Name is the optional friendly alias that resolves to it.
//
// AuthState reports whether a credential for Host is present in the shared
// credential store — configured, none, or unknown when the store could not be
// consulted. No field on this document carries credential material, its length,
// its shape, or any rendering of it: presence is the whole of what is reported,
// which is what makes the document safe to log, paste into an issue, or commit.
type registryDoc struct {
	Host      string           `json:"host"`
	Name      string           `json:"name,omitempty"`
	Default   bool             `json:"default"`
	AuthState string           `json:"authState"`
	Trust     registryTrustDoc `json:"trust"`
}

// registryTrustDoc is one registry's trust posture as discrete fields, the same
// configuration the table renders as a plain-language sentence. Anchor is keyed
// when a public key is pinned, keyless when identities are, and empty when the
// entry pins neither.
type registryTrustDoc struct {
	SignaturesRequired  bool   `json:"signaturesRequired"`
	VerificationSkipped bool   `json:"verificationSkipped,omitempty"`
	Advisory            bool   `json:"advisory,omitempty"`
	Anchor              string `json:"anchor,omitempty"`
	KeylessIdentities   int    `json:"keylessIdentities,omitempty"`
}

// writeRegistryListJSON emits the resolved listing through the shared structured
// seam, in the readable indented shape.
func writeRegistryListJSON(w io.Writer, entries []registryListEntry) error {
	docs := make([]registryDoc, 0, len(entries))
	for _, e := range entries {
		docs = append(docs, registryDoc{
			Host:      e.cfg.Host,
			Name:      e.cfg.Name,
			Default:   e.isDefault,
			AuthState: e.auth,
			Trust:     registryTrustOf(e.cfg),
		})
	}
	return writeStructured(w, "registry list", registryListDoc{Registries: docs})
}

// registryTrustOf projects a registry's configured trust posture onto the
// structured shape.
func registryTrustOf(r workspaceconfig.RegistryConfig) registryTrustDoc {
	d := registryTrustDoc{
		SignaturesRequired:  r.Signing.Required,
		VerificationSkipped: r.SkipSignatureVerification,
		Advisory:            r.Advisory,
	}
	switch {
	case r.PublicKey != "":
		d.Anchor = "keyed"
	case len(r.TrustedIdentities) > 0:
		d.Anchor = "keyless"
		d.KeylessIdentities = len(r.TrustedIdentities)
	}
	return d
}

// registryTrustSummary renders a short, plain-language description of a
// registry's trust posture: whether signatures are required, whether the trust
// anchor is a pinned key or a keyless identity set, and whether verification
// failures are advisory or skipped.
func registryTrustSummary(r workspaceconfig.RegistryConfig) string {
	var parts []string
	switch {
	case r.SkipSignatureVerification:
		parts = append(parts, "verification skipped")
	case r.Signing.Required:
		parts = append(parts, "signatures required")
	default:
		parts = append(parts, "signatures optional")
	}
	switch {
	case r.PublicKey != "":
		parts = append(parts, "keyed")
	case len(r.TrustedIdentities) == 1:
		parts = append(parts, "keyless (1 identity)")
	case len(r.TrustedIdentities) > 1:
		parts = append(parts, fmt.Sprintf("keyless (%d identities)", len(r.TrustedIdentities)))
	}
	if r.Advisory {
		parts = append(parts, "advisory")
	}
	return strings.Join(parts, ", ")
}

// newRegistryRemoveCmd builds `registry remove <name|host>`, which drops a
// registry's trust-policy entry addressed by its host or friendly alias.
func newRegistryRemoveCmd() *cobra.Command {
	var global bool
	var workspace bool

	c := &cobra.Command{
		Use:   "remove <name|host>",
		Short: "Remove a registry and its trust policy from a config file",
		Long: `Remove a registry's trust-policy entry from a config file, addressed by its host
or its friendly alias.

By default the entry is removed from the global config shared across every
workspace; pass --workspace to remove it from this project's workspace config
instead. Setting both --global and --workspace is a usage error.

The default_registry pointer is left untouched — removing a registry's trust
policy reverts that host to default trust; it does not change which registry is
the default.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return registryRemoveRun(registryRemoveConfig{
				Token:     args[0],
				Global:    global,
				Workspace: workspace,
				ws:        rw,
				Out:       cmd.OutOrStdout(),
			})
		},
	}
	c.Flags().BoolVar(&global, "global", false, "Remove the entry from the global config shared across every workspace.")
	c.Flags().BoolVar(&workspace, "workspace", false, "Remove the entry from this project's workspace config instead of the global config.")
	return c
}

// registryRemoveConfig is the input contract for `aggrega registry remove`,
// populated by the cobra command from its argument and flags.
type registryRemoveConfig struct {
	// Token is the registry to remove, addressed by its host or friendly alias.
	Token string
	// Global and Workspace are the operator's write-target overrides, mirroring
	// `registry add`. A registry is tool-scoped, so removal defaults to the global
	// config; --global forces the global file and --workspace forces this
	// project's workspace file. Setting both is a usage error.
	Global    bool
	Workspace bool

	ws  *workspaceconfig.ResolvedWorkspace
	Out io.Writer
	// lookup reads the environment for global-write-path resolution. Nil selects
	// the live process environment; tests inject a frozen lookup so resolution is
	// deterministic and parallel-safe.
	lookup func(string) string
}

// registryRemoveRun resolves the target config file and drops the registry entry
// addressed by cfg.Token. A token that matches nothing in that file fails loudly
// rather than silently rewriting an unchanged config.
func registryRemoveRun(cfg registryRemoveConfig) error {
	rw, err := workspaceOrResolve(cfg.ws)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}
	cfgPath, err := workspaceconfig.WriteDestination(workspaceconfig.OpRegistry, cfg.Global, cfg.Workspace, rw.Root(), cfg.lookup)
	if err != nil {
		return err
	}
	removed, err := workspaceconfig.RemoveRegistry(cfgPath, cfg.Token)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("aggrega registry remove: registry %q is not configured in %s", cfg.Token, cfgPath)
	}
	if cfg.Out != nil {
		// Resolve the token to its host for the confirmation line, through the same
		// primitive the removal matched on. On the success path the entry existed,
		// so this resolves to the host that was dropped; an ambiguity or other
		// resolver error falls back to echoing the token verbatim.
		host := cfg.Token
		if h, rerr := rw.Config().ResolveRegistry(cfg.Token); rerr == nil {
			host = h
		}
		fmt.Fprintf(cfg.Out, "Removed registry %s from %s\n", host, cfgPath)
	}
	return nil
}

func newRegistryAddCmd() *cobra.Command {
	var identities []string
	var name string
	var keyPath string
	var trustedRoot string
	var requireSignatures bool
	var advisory bool
	var fetchTrust bool
	var global bool
	var workspace bool

	c := &cobra.Command{
		Use:   "add <host>",
		Short: "Register a registry and its signature trust policy",
		Long: `Register a registry host and its signature trust policy.

The entry is written to the global config shared across every workspace, since a
registry and the identities you trust on it are usually the same wherever you
work. Pass --workspace to write it to this project's config file instead; the two
flags are mutually exclusive.

By default the registry's advertised trust policy is fetched once from the
registry's own origin (https://<host>/.well-known/aggrega-trust) and pinned
locally — trust-on-first-use. Verification at pull time always runs against the
pinned local policy, never a live re-fetch. Pass --no-fetch-trust to skip the
fetch and pin only what the flags supply.

A trust policy can also be set by hand: keyless (one or more --identity entries,
each an "identity=issuer" pair) or keyed (--key pointing at a pinned public-key
PEM). Manual --identity / --key flags OVERRIDE the fetched set — when either is
supplied, the fetch is skipped and the policy is built from the flags alone.
Pass --trusted-root to pin a Sigstore trust-root bundle for air-gapped keyless
verification. Adding the official hub host with no policy flags pins the built-in
official baseline.

When the registry advertises no trust policy (a 404 or an unreachable endpoint),
the add does not fail: it writes the policy from the flags (or an empty policy)
and prints a notice suggesting manual --identity / --key pinning.

The entry is written to the workspace config file, preserving existing settings.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return registryAddRun(registryAddConfig{
				Host:              args[0],
				Name:              name,
				Identities:        identities,
				KeyPath:           keyPath,
				TrustedRoot:       trustedRoot,
				RequireSignatures: cmd.Flags().Changed("require-signatures"),
				RequireValue:      requireSignatures,
				Advisory:          advisory,
				FetchTrust:        fetchTrust,
				Global:            global,
				Workspace:         workspace,
				ws:                rw,
				Out:               cmd.OutOrStdout(),
			})
		},
	}
	c.Flags().StringArrayVar(&identities, "identity", nil, "Trusted keyless identity as `identity=issuer` (repeatable; OR-set). E.g. dev@example.com=https://accounts.google.com. Overrides the fetched trust policy.")
	c.Flags().StringVar(&name, "name", "", "Optional friendly alias for this registry. The host stays the identity; the alias resolves to it wherever a registry is named (e.g. --registry). Must be unique across registries.")
	c.Flags().StringVar(&keyPath, "key", "", "Path to a pinned cosign-compatible public-key PEM for keyed verification. Overrides the fetched trust policy.")
	c.Flags().StringVar(&trustedRoot, "trusted-root", "", "Path to a pinned Sigstore trust-root bundle for air-gapped keyless verification")
	c.Flags().BoolVar(&requireSignatures, "require-signatures", false, "Require a signature on every artifact pulled from (and pushed to) this registry")
	c.Flags().BoolVar(&advisory, "advisory", false, "Downgrade verification failures for this registry to warnings instead of halting")
	c.Flags().BoolVar(&fetchTrust, "fetch-trust", true, "Fetch and pin the registry's advertised trust policy on first use. Use --no-fetch-trust to skip; manual --identity/--key also skip the fetch.")
	c.Flags().BoolVar(&global, "global", false, "Write the entry to the global config shared across every workspace. A registry defaults here.")
	c.Flags().BoolVar(&workspace, "workspace", false, "Write the entry to this project's workspace config instead of the global config.")
	return c
}

// registryAddConfig is the input contract for `aggrega registry add`,
// populated by the cobra command from its flags.
type registryAddConfig struct {
	Host string
	// Name is the optional friendly alias threaded onto RegistryConfig.Name.
	// Empty leaves the entry host-only, as before.
	Name        string
	Identities  []string
	KeyPath     string
	TrustedRoot string
	// RequireSignatures reports whether --require-signatures was passed;
	// RequireValue carries its value. For the hub baseline the requirement
	// is on by default even when the flag is absent.
	RequireSignatures bool
	RequireValue      bool
	Advisory          bool
	// FetchTrust enables the trust-on-first-use fetch: when true and no manual
	// --identity / --key is supplied, the registry's advertised trust policy is
	// fetched from its own origin and pinned locally. Manual flags override the
	// fetched set, so a manual policy skips the fetch regardless of this value.
	FetchTrust bool

	// Global and Workspace are the operator's write-target overrides. A registry
	// is tool-scoped, so it defaults to the global config; --global forces the
	// global file and --workspace forces this project's workspace file. Setting
	// both is a usage error.
	Global    bool
	Workspace bool

	ws  *workspaceconfig.ResolvedWorkspace
	Out interface{ Write([]byte) (int, error) }
	// trustClient transports the well-known trust fetch. Nil selects a default
	// client in the fetch path; tests inject an httptest client so no live
	// network is contacted.
	trustClient *http.Client
	// lookup reads the environment for global-write-path resolution. Nil selects
	// the live process environment; tests inject a frozen lookup so resolution
	// is deterministic and parallel-safe.
	lookup func(string) string
}

// registryAddRun assembles a RegistryConfig for the host and upserts it into
// the workspace config file at the resolved workspace root.
//
// Trust-policy precedence:
//
//   - Manual flags win. When --identity or --key is supplied, the policy is
//     built from the flags alone and the advertised-trust fetch is skipped —
//     the operator has stated the trust set explicitly.
//   - Otherwise, trust-on-first-use. With FetchTrust set and no manual policy,
//     the registry's advertised trust document is fetched once from its own
//     origin and its identities pinned locally. This is the first-contact
//     decision; verification at pull time runs against this pinned copy, never
//     a live re-fetch.
//   - Graceful fallback. A registry that advertises nothing (a 404 or an
//     unreachable endpoint) is not an error: the add prints a notice and writes
//     an empty/permissive policy (or, for the hub host, the embedded baseline),
//     exactly as before trust-on-first-use existed.
func registryAddRun(cfg registryAddConfig) error {
	rw, err := workspaceOrResolve(cfg.ws)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}
	host := registryhost.Of(cfg.Host)
	if host == "" {
		return fmt.Errorf("aggrega registry add: a registry host is required")
	}

	// Resolve the write destination up front — it does not depend on the trust
	// fetch, so a write-target flag misuse (both --global and --workspace) or an
	// unresolvable global path fails fast, before any network fetch.
	cfgPath, err := workspaceconfig.WriteDestination(workspaceconfig.OpRegistry, cfg.Global, cfg.Workspace, rw.Root(), cfg.lookup)
	if err != nil {
		return err
	}

	manualPolicy := len(cfg.Identities) > 0 || cfg.KeyPath != ""

	reg := workspaceconfig.RegistryConfig{Host: host}

	// Adding the official hub with no explicit manual policy pins the embedded
	// baseline (the maintainer + CI OR-set, signatures required).
	if host == workspaceconfig.HubBaselineHost && !manualPolicy {
		reg = workspaceconfig.DefaultBaseline()
	}

	// The friendly alias rides on the same host-keyed entry regardless of how the
	// trust policy is assembled below. Empty leaves the entry host-only.
	reg.Name = cfg.Name

	// Trust-on-first-use: fetch and pin the advertised policy unless a manual
	// policy overrides it. A manual --identity/--key skips the fetch entirely.
	if cfg.FetchTrust && !manualPolicy {
		if perr := pinAdvertisedTrust(&reg, host, cfg.trustClient, cfg.Out); perr != nil {
			return perr
		}
	}

	for _, spec := range cfg.Identities {
		id, issuer, ok := strings.Cut(spec, "=")
		if !ok || id == "" || issuer == "" {
			return fmt.Errorf("aggrega registry add: --identity %q must be in the form identity=issuer", spec)
		}
		reg.TrustedIdentities = append(reg.TrustedIdentities, workspaceconfig.TrustedIdentity{
			Identity: id,
			Issuer:   issuer,
		})
	}

	if cfg.KeyPath != "" {
		pem, rerr := os.ReadFile(cfg.KeyPath)
		if rerr != nil {
			return fmt.Errorf("aggrega registry add: read public key %s: %w", cfg.KeyPath, rerr)
		}
		reg.PublicKey = string(pem)
	}
	if cfg.TrustedRoot != "" {
		reg.TrustedRoot = cfg.TrustedRoot
	}
	if cfg.RequireSignatures {
		reg.Signing.Required = cfg.RequireValue
	}
	if cfg.Advisory {
		reg.Advisory = true
	}

	if err := workspaceconfig.UpsertRegistry(cfgPath, reg); err != nil {
		return err
	}
	if cfg.Out != nil {
		fmt.Fprintf(cfg.Out, "Registered registry %s in %s\n", host, cfgPath)
	}
	return nil
}

// pinAdvertisedTrust fetches host's advertised trust document and copies its
// fields onto reg. A registry that advertises nothing — a 404 (mapped to
// ErrNoTrustAdvertised) or an unreachable endpoint (a transport error) — is
// tolerated: it prints a notice and leaves reg's policy for the caller's
// manual flags (or the empty/baseline default) to fill, rather than failing
// the add. A MALFORMED advertised document (one that fetched but did not
// decode, ErrMalformedTrust) is a hard error, since the endpoint answered with
// content that is not a usable trust policy — a real misconfiguration the
// operator should see.
func pinAdvertisedTrust(reg *workspaceconfig.RegistryConfig, host string, client *http.Client, out interface{ Write([]byte) (int, error) }) error {
	adv, ferr := registrytrust.Fetch(context.Background(), client, host)
	if ferr != nil {
		if errors.Is(ferr, registrytrust.ErrMalformedTrust()) {
			return fmt.Errorf("aggrega registry add: %w", ferr)
		}
		if out != nil {
			fmt.Fprintf(out, "Notice: registry %s advertises no trust config (%v); pin identities manually with --identity/--key\n", host, ferr)
		}
		return nil
	}

	reg.Signing.Required = adv.RequiresSignatures
	reg.TrustedIdentities = advertisedIdentities(adv.TrustedIdentities)
	reg.TrustedDeprecationIdentities = advertisedIdentities(adv.TrustedDeprecationIdentities)
	if adv.PublicKey != "" {
		reg.PublicKey = adv.PublicKey
	}
	if out != nil {
		fmt.Fprintf(out, "Pinned advertised trust policy from %s (%d identities)\n", host, len(reg.TrustedIdentities))
	}
	return nil
}

// advertisedIdentities converts the wire-shape identities from an advertised
// trust document into the per-registry config's identity type. The two shapes
// are field-identical; the copy keeps the config package unaware of the
// registrytrust wire type.
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
