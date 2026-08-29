// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// providerSocketKind is the socket argument that binds a capability to a
// provider-rendering plugin (the `plugins.providers` list), as opposed to the
// config sockets that take a bare plugin attachment.
const providerSocketKind = "provider"

// configSocketKinds is the set of workspace-level config sockets a plugin
// attaches to by its bare entity identity. It is the plugin-type registry's
// socket-attached subset minus the provider socket, which is handled separately
// because it binds a capability rather than attaching a plugin to a socket list.
var configSocketKinds = buildConfigSocketKinds()

// buildConfigSocketKinds derives the config-socket membership set from the
// canonical plugin-type registry so the socket vocabulary lives once: every
// plugin type that names a workspace socket, except the provider socket. The
// result is a membership set read by key, never range-iterated for output.
func buildConfigSocketKinds() map[string]bool {
	kinds := map[string]bool{}
	for _, e := range plugintype.Entries() {
		if e.Socket != "" && e.Socket != providerSocketKind {
			kinds[e.Socket] = true
		}
	}
	return kinds
}

// pluginKindsHint is the ordered list of accepted <type> arguments the plugin
// add and remove commands name in their unknown-type errors: the provider
// socket, the config sockets, then the cli type. The kind names are sourced from
// the plugin-type registry (via the workspace socket constants and the cli plugin
// type) so the vocabulary lives once; the order is the fixed presentation order
// the surface has always shown, which differs from the registry's author order.
func pluginKindsHint() string {
	kinds := []string{
		providerSocketKind,
		string(workspaceconfig.SocketGovernance),
		string(workspaceconfig.SocketLinter),
		string(workspaceconfig.SocketImporter),
		string(wasm.PluginTypeCLI),
	}
	return strings.Join(kinds, ", ")
}

// newPluginAddCmd builds the `plugin add` command: it searches the configured
// registry for a plugin of the named type, resolves the match to one plugin
// identity, and either writes the attachment into the workspace config file — a
// `plugins.<type>` list entry (mutator / policy / linter / importer) or a
// `plugins.providers` capability binding (provider) — or, for the cli type,
// installs the plugin as a top-level aggrega command.
func newPluginAddCmd() *cobra.Command {
	var capability string
	var role string
	var global bool
	var workspace bool
	var acceptCaps bool
	var assumeYes bool
	c := &cobra.Command{
		Use:   "add <type> <query>",
		Short: "Search the registry and add a plugin of the named type",
		Long: `Search the configured registry for a plugin of the named type, resolve the
query to one plugin, and add it to your configuration.

Arguments:
  <type>   The plugin socket to add for. One of: provider, governance,
           linter, importer, or cli. The governance type requires --role.
  <query>  A case-insensitive substring matched against the plugin names of the
           chosen type. It must resolve to exactly one plugin — an exact
           plugin-name match wins when several share the substring; otherwise the
           command lists the candidates so you can narrow it.

What add writes depends on the type:

  - A config type (governance, linter, importer) pins the latest published
    version and adds the bare plugin identity (<type>/<name>:<version>) to the
    plugins.<type> list. A governance entry also records its --role
    (label-transform, ir-verdict, or render-verdict).
  - The provider type writes the plugin's oci:// identity as a plugins.providers
    entry; --capability names the capability the binding serves, preserving
    existing settings.
  - The cli type fetches the plugin from the registry, verifies it, and caches
    its bytes, then pins it into the plugins.cli list as a top-level aggrega
    command — no separate pull step. Its resolved reference and the content
    digest of the verified bytes are recorded so the command runs those exact
    bytes, resolving from the local cache on later runs with no further network
    access. A cli plugin defaults to the global config, so its command is
    available in every workspace; --workspace pins it to this project instead.
    Its declared capabilities are confirmed before installing, and
    --accept-capabilities or --yes skips that prompt for non-interactive use.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return pluginAddRun(cmd.Context(), pluginAddConfig{
				Socket:     args[0],
				Query:      args[1],
				Capability: capability,
				Role:       role,
				Global:     global,
				Workspace:  workspace,
				AcceptCaps: acceptCaps,
				AssumeYes:  assumeYes,
				ws:         rw,
				In:         cmd.InOrStdin(),
				Out:        cmd.OutOrStdout(),
			})
		},
	}
	c.Flags().StringVar(&capability, "capability", "", "Capability the provider binding serves (required for, and only valid with, the provider type)")
	c.Flags().StringVar(&role, "role", "", "Governance attachment role: label-transform, ir-verdict, or render-verdict (required for, and only valid with, the governance type)")
	c.Flags().BoolVar(&global, "global", false, "Write the attachment to the global config shared across every workspace.")
	c.Flags().BoolVar(&workspace, "workspace", false, "Write the attachment to this project's workspace config. A provider or socket attachment defaults here; a cli plugin defaults to the global config.")
	c.Flags().BoolVar(&acceptCaps, "accept-capabilities", false, "Accept a cli plugin's declared capabilities without the interactive prompt (cli type only)")
	c.Flags().BoolVar(&assumeYes, "yes", false, "Skip the cli capability-confirmation prompt for non-interactive use (cli type only)")
	return c
}

// pluginAddConfig is the input contract for `aggrega plugin add`, populated by
// the cobra command from its arguments and flags.
type pluginAddConfig struct {
	Socket     string
	Query      string
	Capability string
	Role       string

	// Global and Workspace are the operator's write-target overrides. A plugin
	// attachment is build-scoped, so it defaults to this project's workspace
	// config; --global forces the global file and --workspace forces the
	// workspace file. Setting both is a usage error.
	Global    bool
	Workspace bool

	// AcceptCaps and AssumeYes gate the cli type's capability-confirmation
	// prompt. They are consulted only when the type is cli; on any other type a
	// set flag is a usage error, mirroring the provider-only --capability guard.
	AcceptCaps bool
	AssumeYes  bool

	ws  *workspaceconfig.ResolvedWorkspace
	In  io.Reader
	Out interface{ Write([]byte) (int, error) }
	// lookup reads the environment for global-write-path resolution. Nil selects
	// the live process environment; tests inject a frozen lookup so resolution
	// is deterministic and parallel-safe.
	lookup func(string) string
}

// pluginAddRun searches the workspace registry for a plugin serving cfg.Socket,
// resolves the query to a single plugin repository, and writes the attachment
// into the workspace config file. A config socket attaches the bare plugin
// entity identity at its latest published version; the provider socket binds
// cfg.Capability to the plugin's oci:// identity.
func pluginAddRun(ctx context.Context, cfg pluginAddConfig) error {
	rw, err := workspaceOrResolve(cfg.ws)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}

	socket := cfg.Socket
	isProvider := socket == providerSocketKind
	isCLI := socket == string(wasm.PluginTypeCLI)
	if !isProvider && !isCLI && !configSocketKinds[socket] {
		return fmt.Errorf("aggrega plugin add: unknown type %q; expected one of %s", socket, pluginKindsHint())
	}
	if isProvider && cfg.Capability == "" {
		return fmt.Errorf("aggrega plugin add: the provider type requires --capability naming the capability the binding serves")
	}
	if !isProvider && cfg.Capability != "" {
		return fmt.Errorf("aggrega plugin add: --capability applies only to the provider type")
	}
	isGovernance := socket == string(workspaceconfig.SocketGovernance)
	if isGovernance {
		if _, ok := plugintype.GovernanceRoleTier(plugintype.Role(cfg.Role)); !ok {
			return fmt.Errorf("aggrega plugin add: the governance type requires --role naming the attachment role (label-transform, ir-verdict, or render-verdict)")
		}
	} else if cfg.Role != "" {
		return fmt.Errorf("aggrega plugin add: --role applies only to the governance type")
	}
	if !isCLI && (cfg.AcceptCaps || cfg.AssumeYes) {
		return fmt.Errorf("aggrega plugin add: --accept-capabilities and --yes apply only to the cli type")
	}

	// The cli type installs a top-level command rather than writing a config
	// attachment, so it dispatches to the install path before the config
	// write-destination resolution the socket and provider types share.
	if isCLI {
		return pluginAddCLIRun(ctx, rw, cfg)
	}

	// Resolve the write destination up front — it does not depend on the search,
	// so a write-target flag misuse (both --global and --workspace) or an
	// unresolvable global path fails fast, before any registry search.
	op := workspaceconfig.OpConfigSocket
	if isProvider {
		op = workspaceconfig.OpProviderBinding
	}
	configPath, err := workspaceconfig.WriteDestination(op, cfg.Global, cfg.Workspace, rw.Root(), cfg.lookup)
	if err != nil {
		return err
	}

	reg, host, err := registrySearchTarget(rw.Config())
	if err != nil {
		return fmt.Errorf("aggrega plugin add: %w", err)
	}
	matches, err := searchRegistry(ctx, reg, cfg.Query, socketSearchScope(socket))
	if err != nil {
		return err
	}
	repoPath, err := resolveSinglePluginRepo(matches, cfg.Query, socket)
	if err != nil {
		return err
	}

	if isProvider {
		// The provider binding carries the oci:// artifact identity built from the
		// repository path; a config-socket attachment instead carries the bare
		// entity identity, which an oci:// reference is refused for.
		uri := "oci://" + host + "/" + repoPath
		if err := workspaceconfig.UpsertProviderBinding(configPath, cfg.Capability, uri); err != nil {
			return fmt.Errorf("aggrega plugin add: write %s: %w", configPath, err)
		}
		fmt.Fprintf(cfg.Out, "Bound capability %q to %s in %s\n", cfg.Capability, uri, configPath)
		return nil
	}

	// Config socket: strip the plugin/ path prefix to the plugin coordinate
	// identity (<pluginType>/<name>) and pin the latest published version so the
	// attachment is version-pinned.
	entity := strings.TrimPrefix(repoPath, catalog.KindPathSegment("Plugin")+"/")
	version, err := resolveLatestPluginVersion(ctx, rw, host, repoPath, cfg.Out)
	if err != nil {
		return fmt.Errorf("aggrega plugin add: %w", err)
	}
	if version == "" {
		return fmt.Errorf("aggrega plugin add: %s has no published version tag to attach; publish a version first", repoPath)
	}
	identity := entity + ":" + version
	if err := workspaceconfig.UpsertPluginSocketRef(configPath, socket, identity, plugintype.Role(cfg.Role)); err != nil {
		return fmt.Errorf("aggrega plugin add: write %s: %w", configPath, err)
	}
	fmt.Fprintf(cfg.Out, "Added %s as a %s plugin in %s\n", identity, socket, configPath)
	return nil
}

// pluginAddCLIRun resolves the cli-scoped query to a single plugin repository,
// pins its latest published version into an oci:// reference, and installs it as
// a top-level aggrega command through the shared install path. It reuses the same
// registry-search and version-resolution primitives the config-socket path uses,
// so a cli plugin is resolved exactly as any other type; only the terminal action
// differs — a command is installed rather than a config attachment written.
func pluginAddCLIRun(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, cfg pluginAddConfig) error {
	reg, host, err := registrySearchTarget(rw.Config())
	if err != nil {
		return fmt.Errorf("aggrega plugin add: %w", err)
	}
	matches, err := searchRegistry(ctx, reg, cfg.Query, socketSearchScope(string(wasm.PluginTypeCLI)))
	if err != nil {
		return err
	}
	repoPath, err := resolveSinglePluginRepo(matches, cfg.Query, string(wasm.PluginTypeCLI))
	if err != nil {
		return err
	}
	version, err := resolveLatestPluginVersion(ctx, rw, host, repoPath, cfg.Out)
	if err != nil {
		return fmt.Errorf("aggrega plugin add: %w", err)
	}
	if version == "" {
		return fmt.Errorf("aggrega plugin add: %s has no published version tag to install; publish a version first", repoPath)
	}
	ref := "oci://" + host + "/" + repoPath + ":" + version
	return pluginInstallRun(ctx, rw, ref, cfg.Global, cfg.Workspace, cfg.AcceptCaps, cfg.AssumeYes, cfg.lookup, cfg.In, cfg.Out)
}

// socketSearchScope admits only the repository paths a plugin serving socket
// publishes under — the plugin/<socket>/ path prefix — so a search for one
// socket never returns a plugin bound for another.
func socketSearchScope(socket string) searchScope {
	prefix := catalog.KindPathSegment("Plugin") + "/" + socket + "/"
	return func(repoPath string) bool {
		return strings.HasPrefix(repoPath, prefix)
	}
}

// resolveSinglePluginRepo reduces the scoped search matches to exactly one
// plugin repository. A single match is used directly; when several match the
// substring an exact plugin-name (final path segment) hit is preferred; a query
// that still resolves to zero or more than one plugin halts loudly with the
// candidates rather than guessing which the operator meant.
func resolveSinglePluginRepo(matches []string, query, socket string) (string, error) {
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("aggrega plugin add: no %s plugin in the registry matches %q", socket, query)
	case 1:
		return matches[0], nil
	}
	var exact []string
	for _, m := range matches {
		if path.Base(m) == query {
			exact = append(exact, m)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	return "", fmt.Errorf("aggrega plugin add: %q matches %d %s plugins (%s); narrow the query to one",
		query, len(matches), socket, strings.Join(matches, ", "))
}

// pluginRepoTagLister lists the tags a registry serves for a plugin repository.
// It is a package-level seam so config-socket version resolution runs against a
// fake in tests; production lists live tags through the shared repository client
// so the loopback plain-HTTP and credential wiring match every other pull.
var pluginRepoTagLister = func(ctx context.Context, cfg workspaceconfig.Config, host, repoPath string) ([]string, error) {
	httpClient, err := workspaceEgressClient(cfg)
	if err != nil {
		return nil, err
	}
	repo, err := oci.NewRemoteRepository(host+"/"+repoPath, httpClient)
	if err != nil {
		return nil, err
	}
	return oci.ListTags(ctx, repo)
}

// pluginRepoWithdrawalLister reads the deprecation markers published against a
// plugin repository's versions and returns the ones whose own signature verifies,
// so a reference the local index does not carry can still be screened against a
// signed withdrawal rather than pinning one blind.
//
// It is a package-level seam so version resolution runs against a fake in tests;
// production reads through the shared repository client, so the loopback plain-HTTP
// and credential wiring match every other registry read. It is separate from the
// tag-listing seam above rather than folded into it because the two are wanted
// independently: the offline path takes neither, and a caller with nowhere to
// announce a substitution takes the tags and skips the markers entirely.
//
// The marker read, its trust policy and its verification are the ones the local
// index build performs, reached through that same single home — so a version's
// withdrawal is judged identically whether the index recorded it earlier or this
// command reads it now, and the two cannot drift apart.
var pluginRepoWithdrawalLister = func(ctx context.Context, cfg workspaceconfig.Config, host, repoPath string, versions []string, out io.Writer) ([]VersionDeprecation, error) {
	httpClient, err := workspaceEgressClient(cfg)
	if err != nil {
		return nil, err
	}
	repo, err := oci.NewRemoteRepository(host+"/"+repoPath, httpClient)
	if err != nil {
		return nil, err
	}
	policy, _ := cfg.RegistryTrustPolicy(host)
	return collectVersionDeprecations(ctx, cfg, policy, repo, repoPath, versions, out), nil
}

// livePluginRetractionScreen builds the withdrawal screen for a plugin repository
// the local index does not carry, from markers read during this command. It returns
// nil — leaving selection unscreened — whenever no screen can be formed, and each
// of those cases is a deliberate posture rather than a fallthrough:
//
// With no writer there is nowhere to announce a substitution, and excluding a
// version nobody can be told about is the silent substitution this exists to
// prevent; the marker reads are skipped along with it, so nothing is paid for a
// screen that would not be used. With no strict-SemVer tag there is no version the
// discovery step could select, so there is nothing to screen. And when the marker
// read itself fails, selection proceeds unscreened rather than halting: the same
// best-effort posture the index build takes on the same failure, which keeps a
// transient registry fault from failing an attach that would otherwise succeed.
// Degrading can only ever fail to exclude a version, never exclude one on evidence
// that did not verify.
//
// The failure is reported rather than swallowed. A caller told that markers could
// not be read knows the selection was not screened, which is the difference between
// a degraded result and a wrong one. It reports on the exclusion channel rather than
// the advisory one, because what it states is what happened to selection — the same
// event the exclusion notices carry — and the two channels stay distinct precisely so
// a reader can tell "you are being told something" from "you did not get something".
func livePluginRetractionScreen(ctx context.Context, cfg workspaceconfig.Config, host, repoPath, reference string, tags []string, out io.Writer) catalog.RetractionScreen {
	if out == nil {
		return nil
	}
	versions := oci.SortedSemverTags(tags)
	if len(versions) == 0 {
		return nil
	}
	deps, err := pluginRepoWithdrawalLister(ctx, cfg, host, repoPath, versions, out)
	if err != nil {
		fmt.Fprintf(out, "  retraction: %s: markers could not be read (%v); published versions were not screened for retraction\n",
			repoPath, err)
		return nil
	}
	return newLiveRetractionScreen(reference, deps, out)
}

// resolveLatestPluginVersion returns the highest strict-SemVer version published
// for the plugin repository that has not been withdrawn, or the empty string when
// no conforming version is found. Discovery reads the local registry index by
// default — the snapshot a `registry update` builds — so version resolution costs
// no network round-trip on the common path. Pre-release versions are excluded, the
// same rule the publishing and deprecation paths apply. A version a verified marker
// records as withdrawn is skipped and the substitution announced on out; out is
// also where an announcement would go for a screened selection, so a nil writer
// leaves selection unscreened rather than skipping a version silently.
//
// When the local index does not carry the repository — never built, or the
// repository is absent from the snapshot — resolution falls back to a live tag
// list, except in an offline workspace, where discovery must not network: it
// fails loudly, pointing at `registry update`. That fallback screens too: having
// no recorded withdrawal to read, it reads the repository's markers from the
// registry before selecting, so a withdrawn version is skipped there exactly as it
// is on the snapshot path. The actual plugin fetch/add is always live regardless;
// only this version discovery prefers the snapshot.
//
// A tag set with no strict-SemVer version resolves to the empty string rather than
// an error, which is what lets the caller name the missing publication in its own
// words. A repository whose every selectable version is withdrawn is the different
// case and does halt: nothing is left to attach, and the withdrawal is the reason.
func resolveLatestPluginVersion(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, host, repoPath string, out io.Writer) (string, error) {
	if version, ok, err := pluginVersionFromIndex(rw, host, repoPath, out); err != nil {
		return "", err
	} else if ok {
		return version, nil
	}
	cfg := rw.Config()
	if cfg.Offline {
		return "", fmt.Errorf("offline workspace has no local index entry for %s; run 'aggrega registry update' to build the discovery index", repoPath)
	}
	tags, err := pluginRepoTagLister(ctx, cfg, host, repoPath)
	if err != nil {
		return "", fmt.Errorf("list versions for %s: %w", repoPath, err)
	}
	// The screen and the selection key off this one reference, and the screen is
	// populated before selection runs so selection itself still reaches no registry.
	reference := host + "/" + repoPath
	screen := livePluginRetractionScreen(ctx, cfg, host, repoPath, reference, tags, out)
	version, err := catalog.SelectScreenedVersion(screen, reference, tags)
	if err != nil {
		// No conforming tag is the caller's empty-version case, not a failure: it
		// reports the absent publication naming this repository. Every other halt —
		// a fully-withdrawn repository above all — carries a reason of its own and
		// must not be flattened into "no version published".
		if errors.Is(err, oci.ErrFetchNotFound()) {
			return "", nil
		}
		return "", err
	}
	return version, nil
}

// pluginVersionFromIndex resolves a plugin repository's version from the host's
// local discovery index, screened against the withdrawal records that same index
// carries. It reports the version and whether the repository was present. An index
// that has not been built reports (absent) so the caller can fall back to a live
// lookup; a genuine read or parse fault of a built index surfaces as an error
// rather than a silent fallback.
//
// Selection runs over the record's published-version set rather than taking its
// headline, because the headline is recorded from an unscreened selection: it names
// the highest published version whether or not that version has since been
// withdrawn. Running the shared screened selection over the set instead is what
// makes a withdrawn version skipped here exactly as it is skipped when an entity is
// pulled, with the substitution announced on out. Absent a withdrawal the two
// resolve the same version, because the set retains the headline and selection
// applies the same rule the headline was recorded with.
//
// A record carrying no version set holds only its headline: there is nothing to
// select over, so the headline stands. A nil out leaves selection unscreened, which
// is the honest posture — an exclusion nobody can be told about is the silent
// substitution this exists to prevent.
func pluginVersionFromIndex(rw *workspaceconfig.ResolvedWorkspace, host, repoPath string, out io.Writer) (string, bool, error) {
	loaded, err := loadHostIndex(rw.Root(), rw.Config().ResolvedArtifactCacheDir(), host)
	if err != nil {
		if errors.Is(err, errIndexNotBuilt) {
			return "", false, nil
		}
		return "", false, err
	}
	// The record lookup and the screen both key off this one reference through the
	// single index-URI mapping, so they cannot disagree about which record they mean.
	reference := host + "/" + repoPath
	uri := indexURIForReference(reference)
	for _, rec := range loaded.Records {
		if rec.URI != uri {
			continue
		}
		if len(rec.Versions) == 0 {
			return rec.Version, true, nil
		}
		version, serr := catalog.SelectScreenedVersion(newRetractionScreen(rw, host, out), reference, rec.Versions)
		if serr != nil {
			return "", true, serr
		}
		return version, true, nil
	}
	return "", false, nil
}
