// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"oras.land/oras-go/v2/registry/remote/credentials"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
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

A registry entry is written to the global config shared across every workspace
on this machine, because a registry and the identities you trust on it are
usually the same wherever you work. That makes adding or removing one a
machine-wide change: every project on this machine resolves through the entry
you just wrote or dropped. Pass --workspace to write it to this project's config
file instead. Building the searchable index is the exception — that lands in
this workspace's own artifact cache and reaches no other project.`,
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
			return registryListRun(cmd.Context(), registryListConfig{
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

// registryListRun renders the effective registries in ascending host order. It
// reads the merged effective config — the same set resolution and push consult —
// and marks the default registry.
//
// The set itself comes from the shared layer, so this command and every other
// surface report one resolution rather than each assembling its own. Only the
// rendering is decided here: the table shows no credential state, so the default
// invocation passes no resolver and opens no credential store at all.
func registryListRun(ctx context.Context, cfg registryListConfig) error {
	rw, err := workspaceOrResolve(cfg.ws)
	if err != nil {
		return err
	}
	wsCfg := rw.Config()

	if cfg.JSON {
		entries := registryops.List(wsCfg, registryops.AuthResolver(ctx, cfg.store))
		return writeStructured(cfg.Out, "registry list", registryListDoc{Registries: entries})
	}

	entries := registryops.List(wsCfg, nil)
	if len(entries) == 0 {
		fmt.Fprintln(cfg.Out, "No registries configured.")
		return nil
	}

	w := tabwriter.NewWriter(cfg.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "HOST\tALIAS\tTRUST\tDEFAULT")
	for _, e := range entries {
		marker := ""
		if e.Default {
			marker = "*"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.Host, e.Name, registryTrustSummary(e.Trust), marker)
	}
	return w.Flush()
}

// registryListDoc is the structured -o json listing: every configured registry
// in the same ascending host order the table uses. The slice is non-nil on the
// wire even when nothing is configured, so a consumer iterates unconditionally
// instead of special-casing null.
type registryListDoc struct {
	Registries []registryops.Registry `json:"registries"`
}

// registryTrustSummary renders a short, plain-language description of a
// registry's trust posture: whether signatures are required, whether the trust
// anchor is a pinned key or a keyless identity set, and whether verification
// failures are advisory or skipped.
// It reads the posture the shared layer resolved rather than the raw config
// entry, so the sentence this table prints and the discrete fields the structured
// renderings emit are two projections of one value and cannot describe the same
// registry differently.
func registryTrustSummary(t registryops.Trust) string {
	var parts []string
	switch {
	case t.VerificationSkipped:
		parts = append(parts, "verification skipped")
	case t.SignaturesRequired:
		parts = append(parts, "signatures required")
	default:
		parts = append(parts, "signatures optional")
	}
	switch {
	case t.Anchor == "keyed":
		parts = append(parts, "keyed")
	case t.Anchor == "keyless" && t.KeylessIdentities == 1:
		parts = append(parts, "keyless (1 identity)")
	case t.Anchor == "keyless" && t.KeylessIdentities > 1:
		parts = append(parts, fmt.Sprintf("keyless (%d identities)", t.KeylessIdentities))
	}
	if t.Advisory {
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
the default. Where default_registry names the removed entry by its alias, that
alias names nothing afterwards, and the configuration is refused until
default_registry is written as a host or as another entry's name.`,
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
}

// registryRemoveRun drops the registry entry addressed by cfg.Token from the
// resolved config file and confirms what it dropped and from where.
//
// The removal itself comes from the shared layer, so this command and every
// other surface perform one removal rather than each performing its own. Only
// the confirmation line is decided here, and it names the file so a person
// reading it can see whether the change was machine-wide or this project's.
func registryRemoveRun(cfg registryRemoveConfig) error {
	rw, err := workspaceOrResolve(cfg.ws)
	if err != nil {
		return err
	}
	res, err := registryops.Remove(rw, registryops.RemoveOptions{
		Token:     cfg.Token,
		Global:    cfg.Global,
		Workspace: cfg.Workspace,
	})
	if err != nil {
		return err
	}
	if cfg.Out != nil {
		fmt.Fprintf(cfg.Out, "Removed registry %s from %s\n", res.Host, res.Target.Path)
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

The entry is keyed under the host exactly as written, and every lookup of it is
made in one spelling: a lowercase host name containing a dot, localhost, a single
name followed by a port (registry:5000), an IPv4 address, or an IPv6 address in
brackets, each with an optional :port. A reference may be given instead — its
scheme, path and credentials are dropped and the entry is keyed under the host it
names — but a host written in capitals, with the default HTTPS port, or as a
single name with no port is refused, naming the spelling to write where there is
one.

The entry is written to the global config shared across every workspace, since a
registry and the identities you trust on it are usually the same wherever you
work. Pass --workspace to write it to this project's config file instead; the two
flags are mutually exclusive.

By default the registry's advertised trust policy is fetched once from the
registry's own origin (https://<host>/.well-known/aggrega-trust) and pinned
locally — trust-on-first-use. Verification at pull time always runs against the
pinned local policy, never a live re-fetch. Pass --fetch-trust=false to skip the
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
and prints a notice suggesting manual --identity / --key pinning. The official
hub writes its built-in baseline instead, and the notice says the baseline was
pinned.

When the workspace is offline the advertised policy cannot be fetched, so the
registry is never asked. Adding a registry other than the official hub is then
refused unless --identity or --key supplies the policy, rather than writing an
entry that trusts nothing: register it once while online, or pin it by hand.
Adding the official hub offline pins its built-in baseline, with a notice saying
so, and a manual policy registers offline exactly as it does online.

Existing settings in the target config file are preserved.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return registryAddRun(cmd.Context(), registryAddConfig{
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
				ErrOut:            cmd.ErrOrStderr(),
			})
		},
	}
	c.Flags().StringArrayVar(&identities, "identity", nil, "Trusted keyless identity as `identity=issuer`, for example dev@example.com=https://accounts.google.com (repeatable; OR-set). Overrides the fetched trust policy.")
	c.Flags().StringVar(&name, "name", "", "Optional friendly alias for this registry. The host stays the identity; the alias resolves to it wherever a registry is named (e.g. --registry). Must be unique across registries.")
	c.Flags().StringVar(&keyPath, "key", "", "Path to a pinned cosign-compatible public-key PEM for keyed verification. Overrides the fetched trust policy.")
	c.Flags().StringVar(&trustedRoot, "trusted-root", "", "Path to a pinned Sigstore trust-root bundle for air-gapped keyless verification")
	c.Flags().BoolVar(&requireSignatures, "require-signatures", false, "Require a signature on every artifact pulled from (and pushed to) this registry")
	c.Flags().BoolVar(&advisory, "advisory", false, "Downgrade verification failures for this registry to warnings instead of halting")
	c.Flags().BoolVar(&fetchTrust, "fetch-trust", true, "Fetch and pin the registry's advertised trust policy on first use. Use --fetch-trust=false to skip; manual --identity/--key also skip the fetch.")
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

	// Delivery selects whether the registration writes the config file or reports
	// the content that write would have produced. The zero value writes.
	//
	// `registry add` itself never sets it: registering a registry is not one of
	// the capabilities whose product is content an author composes, so its command
	// offers no reporting mode. It is here because seeding a configuration
	// delegates its whole registration through this path, and that capability does
	// carry the mode — so the delivery travels with the request rather than being
	// re-decided by a second registration path written for the reporting case.
	Delivery authoredcontent.Delivery

	ws *workspaceconfig.ResolvedWorkspace
	// Out carries the answer: the confirmation of what was registered under a
	// writing delivery, and the one parseable document under a reporting one.
	Out interface{ Write([]byte) (int, error) }
	// ErrOut carries an advisory the answer on Out cannot convey.
	//
	// It exists because a reporting delivery leaves Out holding one parseable
	// document and nothing else, so an advisory has nowhere on that stream to go
	// and would simply be dropped. Which advisory qualifies, and why only one of
	// the fetch outcomes does, is writeTrustFetchNotice's decision.
	ErrOut interface{ Write([]byte) (int, error) }
	// trustClient transports the well-known trust fetch. Nil takes the
	// network-egress gate's client in the registration; tests inject an httptest
	// client so no live network is contacted.
	trustClient *http.Client
}

// registryAddRun registers the host and its trust policy in the resolved config
// file and confirms what it wrote and where.
//
// The registration itself — the write-target resolution, the trust-policy
// precedence and the advertised-trust fetch — comes from the shared layer, so
// this command and every other surface perform one registration rather than each
// performing its own. What is decided here is the prose: the trust fetch's
// outcome is reported where it happened, before any later refusal, so a person
// watching sees the same sequence they always did.
func registryAddRun(ctx context.Context, cfg registryAddConfig) error {
	rw, err := workspaceOrResolve(cfg.ws)
	if err != nil {
		return err
	}
	res, addErr := registryops.Add(ctx, rw, registryops.AddOptions{
		Host:              cfg.Host,
		Name:              cfg.Name,
		Identities:        cfg.Identities,
		KeyPath:           cfg.KeyPath,
		TrustedRoot:       cfg.TrustedRoot,
		RequireSignatures: cfg.RequireSignatures,
		RequireValue:      cfg.RequireValue,
		Advisory:          cfg.Advisory,
		FetchTrust:        cfg.FetchTrust,
		Global:            cfg.Global,
		Workspace:         cfg.Workspace,
		Delivery:          cfg.Delivery,
		TrustClient:       cfg.trustClient,
	})
	return writeRegistrationOutcome(cfg.Out, cfg.ErrOut, cfg.Delivery, res, addErr)
}

// writeRegistrationOutcome renders what a registration did on the two streams a
// terminal offers, and returns whatever the registration itself returned.
//
// It is the one rendering of that result. `aggrega registry add` performs the
// registration directly and `aggrega config init` reaches the same registration
// through the configuration-seeding operation, so both print one set of sentences
// rather than two that agree on the day they are written and drift afterwards.
//
// The notice goes out before the error is consulted, because the fetch happened
// either way and a reader who is told nothing about it cannot tell a skipped
// fetch from a failed one. Which stream carries it — and which outcomes are worth
// carrying at all — is decided by the notice writer, where the reason for the
// asymmetry lives with the branch that acts on it.
func writeRegistrationOutcome(out, errOut io.Writer, delivery authoredcontent.Delivery, res registryops.AddResult, addErr error) error {
	writeTrustFetchNotice(out, errOut, delivery, res)
	if addErr != nil {
		return addErr
	}
	if delivery.Reports() {
		if out == nil {
			return nil
		}
		// The path comes from the write-target resolution the registration already
		// performed, so the file the content is addressed to is the file the write
		// would have gone to rather than a second resolution taken here.
		return authoredcontent.Report(out, []authoredcontent.Document{{
			Path:    res.Target.Path,
			Content: string(res.Content),
		}})
	}
	if out != nil {
		fmt.Fprintf(out, "Registered registry %s in %s\n", res.Host, res.Target.Path)
	}
	return nil
}

// writeTrustFetchNotice puts the trust-fetch notice on whichever stream the
// delivery leaves free for prose, and says nothing where the outcome is already
// recoverable from the answer.
//
// Under a writing delivery every outcome goes to Out, which is where a person
// watching a terminal reads it.
//
// Under a reporting delivery Out holds one parseable document and nothing else,
// so a notice there would leave a caller unable to read the answer it asked for.
// The outcomes are not equally recoverable from that document, and it is the
// difference between them — not the delivery alone — that decides this. A pinned
// policy IS the reported content: it is the trust policy the notice would have
// described, so a notice about it would be a duplicate, and it is dropped. The
// built-in baseline is dropped on the same ground: it too is a pinned policy the
// reported content carries, and what its notice adds — why no advertised policy
// was taken — leaves the caller nothing to supply by hand. The not-advertised
// outcome with no baseline behind it is not in the content at all — it reports an
// absence and names the flags that supply a policy by hand, and content that
// simply carries no identities conveys neither — so it goes to ErrOut, where it is
// beside the answer instead of inside it.
//
// A caller that discards ErrOut still loses that advisory. That is the accepted
// cost of leaving the reported document's shape alone, and the alternative —
// carrying the advisory inside the document — would change a contract callers
// parse.
func writeTrustFetchNotice(out, errOut io.Writer, delivery authoredcontent.Delivery, res registryops.AddResult) {
	notice := trustFetchNotice(res.Host, res.Trust)
	if notice == "" {
		return
	}
	if delivery.Reports() {
		if res.Trust.Pinned || res.Trust.Baseline {
			return
		}
		out = errOut
	}
	if out == nil {
		return
	}
	_, _ = io.WriteString(out, notice)
}

// trustFetchNotice composes what the advertised-trust fetch did, in the three
// cases that have something to say. A skipped fetch says nothing: the operator
// either supplied a policy outright or asked for no fetch, so there is no outcome
// to report and a line saying so would be noise on every manual add.
//
// The built-in baseline is stated ahead of the not-advertised case, because both
// are set where the hub was asked and advertised nothing, and the entry then
// carries a policy: telling the reader to pin identities by hand would send them
// to replace trust they already have.
//
// It is the one composition of these sentences, and every stream that carries
// one consumes what it returns. A second phrasing written for another stream
// would agree with this one right up to the first edit of either, and the
// agreement is what would hide the divergence — so the words a reader needs,
// the flags that supply a policy by hand among them, exist here and nowhere
// else.
//
// The baseline and not-advertised sentences open with the reason, which is the
// registration's own account of what happened to the request — what the registry
// answered, or why it was not asked — already opening with the registry's name in
// the reader's terms, and go on to say what follows for the entry. The reason is
// used as the registration worded it; nothing is cut from it here.
func trustFetchNotice(host string, t registryops.TrustFetchOutcome) string {
	switch {
	case t.Pinned:
		return fmt.Sprintf("Pinned advertised trust policy from %s (%d identities)\n", host, t.Identities)
	case t.Baseline:
		return fmt.Sprintf("%s; using the built-in trust baseline for %s instead\n", t.Reason, host)
	case t.NotAdvertised:
		return fmt.Sprintf("Notice: %s; pin its trust by hand with --identity or --key\n", t.Reason)
	}
	return ""
}
