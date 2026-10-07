// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// CLIInstallRequest is what installing a command-extension plugin needs beyond
// the resolved workspace: the pinned reference to install, the write-target
// overrides, the capability decision, and the streams the confirmation reads and
// writes.
//
// AcceptCapabilities and AssumeYes both consent. They are two fields rather than
// one because the surfaces obtain consent differently — the command line has a
// flag for each and a terminal to fall back on — and collapsing them here would
// make the operation decide which spelling a surface used.
type CLIInstallRequest struct {
	Reference          string
	Global             bool
	Workspace          bool
	AcceptCapabilities bool
	AssumeYes          bool
	In                 io.Reader
	Out                io.Writer
}

// CLIInstaller installs a command-extension plugin as a top-level command.
//
// It is a seam the caller supplies rather than an implementation this package
// holds, and the reason is structural rather than stylistic. Installing such a
// plugin refuses any command name that collides with a built-in, and the
// authoritative built-in set is the freshly built command tree itself — which
// lives in the command-line package, is unimportable from here, and is
// deliberately not copied, because a second enumeration of the built-ins would
// drift from the tree that decides them.
//
// So the operation asks its caller for the install rather than reproducing it.
// The caller that owns the command tree supplies the real one; a caller reaching
// this package from another surface is handed the same function by the process
// that built the tree, so both surfaces install through one implementation and
// the collision guard reads the live tree either way.
type CLIInstaller func(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, req CLIInstallRequest) error

// AddOptions is what an addition decides, independently of who asked for it.
//
// Socket is the plugin type to add for and Query is matched case-insensitively
// against the plugin names of that type; it must resolve to exactly one plugin.
//
// Capability names the capability a provider binding serves and applies only to
// the provider type. Role names a governance attachment's role and applies only
// to the governance type. Each is refused on any other type rather than ignored,
// because a flag or field that silently does nothing reads as though it worked.
//
// Global and Workspace are the write-target overrides; leaving both unset takes
// the operation's own default, which for a provider or socket attachment is this
// project's workspace configuration and for a command extension is the global
// file. Setting both is a usage error the write-target policy refuses.
//
// AcceptCapabilities, AssumeYes, In and InstallCLIPlugin are consulted only for
// the command-extension type. Notices is where the addition's prose goes — the
// account of each withdrawal marker the registry was asked about, and, unless
// RetractionNotices redirects it, the substitution a withdrawal caused. With no
// writer for that substitution — a nil Notices and a nil RetractionNotices —
// version selection is left unscreened rather than skipping a withdrawn version
// silently, which is the honest posture when there is nowhere to announce it. That
// leaves Retraction empty as well, because there was no screen to reach a
// conclusion.
//
// Delivery selects whether the addition writes the configuration file or reports
// the content that write would have produced. The zero value writes, so a caller
// that says nothing about delivery adds exactly as before. It is refused on the
// command-extension type, for the reason Add states at that refusal.
type AddOptions struct {
	Socket     string
	Query      string
	Capability string
	Role       string

	Global    bool
	Workspace bool

	AcceptCapabilities bool
	AssumeYes          bool

	Delivery authoredcontent.Delivery

	// InstallCLIPlugin performs the command-extension install. A request for that
	// type with no installer supplied is refused rather than skipped: the caller
	// asked for an install this process was not wired to perform, and reporting
	// success would leave nothing installed.
	InstallCLIPlugin CLIInstaller

	In      io.Reader
	Notices io.Writer

	// RetractionNotices is where a withdrawal substitution is announced, for a
	// caller that wants it somewhere other than Notices. A nil RetractionNotices
	// announces on Notices, which is where the rest of the addition's prose goes,
	// so a caller with nothing to say about it supplies one writer and gets both.
	//
	// It exists for the caller that renders the substitution itself, from the
	// Retraction on the result, through WriteRetractionNotice. Such a caller sets
	// this to a discarding writer: the announcement then reaches its reader once,
	// in its own words and at the moment it chooses, while the marker account still
	// reaches Notices. Discarding Notices outright would take that account with it,
	// and it is not about the substitution at all.
	//
	// 🔴 A discarding writer, never a nil one. Version selection is screened only
	// where there is somewhere to announce a substitution, so nil here with a nil
	// Notices leaves selection unscreened and a withdrawn version would be pinned.
	// A caller with nowhere to render the advisory leaves this nil and lets the
	// prose fall to Notices rather than silencing a screen it then cannot replace.
	RetractionNotices io.Writer
}

// noticeStreams is where an addition's two kinds of prose go.
//
// They are separate because they answer different questions and one caller wants
// them in different places. The marker account says which of an artifact's
// published versions the registry was asked about and what was found; the
// substitution says which version was pinned and why it was not the newest. A
// surface rendering the substitution itself silences only the second, and a pair
// is what lets it do that without silencing the first.
type noticeStreams struct {
	// notices carries the account of each withdrawal marker the registry was asked
	// about.
	notices io.Writer
	// retractions carries the substitution a withdrawal caused. It is also what
	// decides whether selection is screened at all: with nowhere to announce a
	// substitution, selection runs unscreened rather than excluding a version
	// nobody can be told about.
	retractions io.Writer
}

// noticeStreams resolves where this addition's prose goes, defaulting the
// substitution to the same writer the marker account goes to.
func (o AddOptions) noticeStreams() noticeStreams {
	streams := noticeStreams{notices: o.Notices, retractions: o.Notices}
	if o.RetractionNotices != nil {
		streams.retractions = o.RetractionNotices
	}
	return streams
}

// AddResult is what an addition did.
//
// Exactly one of Identity and URI is filled, and which one says what kind of
// attachment landed: a config socket attaches the bare version-pinned entity
// identity, and a provider binding carries the plugin's oci:// artifact identity.
// A command-extension install fills Reference instead, because what it landed is
// a command rather than a configuration attachment.
//
// ConfigIsMachineWide is stated rather than left to be inferred, for the reason
// given on RemoveResult.
//
// Content is the configuration document the write would have produced, filled
// only under a reporting delivery and empty under a writing one — under a write
// the file is already on disk at ConfigPath, so filling it would duplicate on the
// wire what was just persisted. The file the document is for is ConfigPath, the
// one write-target resolution already on this result, so nothing here carries a
// second copy of the path.
//
// Retraction is what the withdrawal screen concluded about the version that was
// pinned, and is absent whenever it concluded nothing. It is the conclusion as
// values rather than as the sentence stating it, so every surface is handed the
// same fact: one answers with the values, and one renders them through
// WriteRetractionNotice. A reader therefore meets a substitution whichever surface
// they came through, and neither has to read prose addressed to the other.
type AddResult struct {
	Socket string `json:"socket"`

	Identity   string `json:"identity,omitempty"`
	URI        string `json:"uri,omitempty"`
	Capability string `json:"capability,omitempty"`
	Reference  string `json:"reference,omitempty"`

	ConfigPath          string `json:"configPath"`
	ConfigIsMachineWide bool   `json:"configIsMachineWide"`
	Content             []byte `json:"content,omitempty"`

	Retraction *RetractionAdvisory `json:"retraction,omitempty"`
}

// Add searches the configured registry for a plugin of the named type, resolves
// the query to one plugin identity, and either writes the attachment into a
// configuration file — a plugins.<type> list entry or a plugins.providers
// capability binding — or, for the command-extension type, installs the plugin as
// a top-level command through the supplied installer.
//
// The write destination is resolved before any registry search, so a write-target
// misuse or an unresolvable global path fails before anything is fetched. It is
// resolved off the supplied resolution, so the attachment lands in the
// configuration file that resolution read rather than in whichever file the
// ambient environment names.
//
// Under a reporting delivery it composes the same document and returns it on the
// result instead of writing it, touching no file and creating no directory. Every
// step before the write is unchanged by the delivery, and that is the contract
// rather than an implementation detail: the write target is resolved so the
// document names the file the write would have gone to, and the registry is
// reached so the identity in the document is the identity a write would have
// pinned. A report is therefore not network-free, and a document composed without
// resolving a version would not be the document a write produces.
//
// 🔴 There is no refuse-if-exists guard on this operation, so a reporting delivery
// neither inherits nor skips one, and that is stated here so nobody adds a skip by
// analogy with the configuration-seeding operation. Seeding refuses to touch a
// file that already exists because its subject is a configuration that does not
// exist yet; attaching a plugin merges into whatever configuration is already
// there, which is the whole operation. Both deliveries therefore pass through
// exactly the same guards — the type classification, the field-applicability
// refusals, the write-target policy and the merged-document schema gate — and no
// guard is conditional on which delivery was asked for.
func Add(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, opts AddOptions) (AddResult, error) {
	const command = "aggrega plugin add"

	var res AddResult
	rw, err := workspaceOrRefuse(rw)
	if err != nil {
		return res, err
	}

	socket := opts.Socket
	isProvider, isCLI, err := classifySocket(command, socket)
	if err != nil {
		return res, err
	}
	if isProvider && opts.Capability == "" {
		return res, fmt.Errorf("%s: the provider type requires --capability naming the capability the binding serves", command)
	}
	if !isProvider && opts.Capability != "" {
		return res, fmt.Errorf("%s: --capability applies only to the provider type", command)
	}
	if socket == string(workspaceconfig.SocketGovernance) {
		if _, ok := plugintype.GovernanceRoleTier(plugintype.Role(opts.Role)); !ok {
			return res, fmt.Errorf("%s: the governance type requires --role naming the attachment role (%s)", command, GovernanceRolesHint())
		}
	} else if opts.Role != "" {
		return res, fmt.Errorf("%s: --role applies only to the governance type", command)
	}
	if !isCLI && (opts.AcceptCapabilities || opts.AssumeYes) {
		return res, fmt.Errorf("%s: --accept-capabilities and --yes apply only to the cli type", command)
	}
	// 🔴 The command-extension type is the one type whose product is not content an
	// author owns, so it is the one type this delivery is refused on — and it is
	// refused rather than ignored, on the rule the type-specific fields above run
	// under: an option that silently does nothing reads exactly like one that
	// worked. What a command-extension addition records is the content digest of
	// the bytes it verified, which is a record the machine maintains rather than
	// text anyone edits; handing it to a caller to amend before saving would either
	// break verification or defeat it. The addition also caches those bytes and the
	// module's self-report as it goes, so a delivery promising to write nothing at
	// all could not honour that promise here.
	//
	// The refusal is worded without naming any one surface's spelling for the
	// delivery, because all three surfaces offer it and each would otherwise be
	// told to change something it cannot reach.
	if isCLI && opts.Delivery.Reports() {
		return res, fmt.Errorf("%s: the cli type cannot report what it would write: installing a command extension "+
			"records the content digest of the bytes it verified, which is a record the machine maintains rather than "+
			"content an author edits, and it caches those bytes as it goes — so it is written rather than reported. "+
			"Ask for a report on a provider or socket attachment, whose product is configuration you go on editing", command)
	}

	// The command-extension type installs a top-level command rather than writing
	// a configuration attachment, so it dispatches before the write-destination
	// resolution the socket and provider types share.
	if isCLI {
		return addCLI(ctx, rw, opts)
	}

	op := workspaceconfig.OpConfigSocket
	if isProvider {
		op = workspaceconfig.OpProviderBinding
	}
	configPath, machineWide, err := writeTarget(rw, op, opts.Global, opts.Workspace)
	if err != nil {
		return res, err
	}

	reg, host, err := RegistryTarget(rw.Config())
	if err != nil {
		return res, fmt.Errorf("%s: %w", command, err)
	}
	under := enumerationLocation(rw.Config(), host)
	matches, err := registryops.EnumerateRepositories(ctx, reg, opts.Query, socketScope(under, socket))
	if err != nil {
		// The enumeration's refusal is about one registry's listing, so it names
		// that registry: a reader with several configured cannot otherwise tell
		// which one to look at.
		return res, fmt.Errorf("%s: %s: %w", command, host, err)
	}
	repoPath, err := ResolveSingleRepo(matches, opts.Query, socket)
	if err != nil {
		return res, err
	}

	if isProvider {
		// The provider binding carries the oci:// artifact identity built from the
		// repository path; a config-socket attachment instead carries the bare
		// entity identity, which an oci:// reference is refused for.
		uri := "oci://" + host + "/" + repoPath
		out := AddResult{
			Socket:              socket,
			URI:                 uri,
			Capability:          opts.Capability,
			ConfigPath:          configPath,
			ConfigIsMachineWide: machineWide,
		}
		// The reporting delivery reaches the composition the write routes through,
		// rather than a second one written for this branch: one computation, two
		// deliveries, so the bytes reported and the bytes written cannot differ.
		if opts.Delivery.Reports() {
			body, derr := workspaceconfig.ProviderBindingDocument(configPath, opts.Capability, uri)
			if derr != nil {
				return res, fmt.Errorf("%s: compose %s: %w", command, configPath, derr)
			}
			out.Content = body
			return out, nil
		}
		if err := workspaceconfig.UpsertProviderBinding(configPath, opts.Capability, uri); err != nil {
			return res, fmt.Errorf("%s: write %s: %w", command, configPath, err)
		}
		return out, nil
	}

	// Config socket: strip the default's repository path and the plugin/ path
	// prefix to the plugin coordinate identity (<pluginType>/<name>) — the
	// identity a pull binds back under that same path — and pin the latest
	// published version so the attachment is version-pinned.
	entity := strings.TrimPrefix(repoPath, under.Repository(catalog.KindPathSegment("Plugin")+"/"))
	var recorder retractionRecorder
	version, err := resolveLatestVersion(ctx, rw, host, repoPath, opts.noticeStreams(), &recorder)
	if err != nil {
		return res, fmt.Errorf("%s: %w", command, err)
	}
	if version == "" {
		return res, fmt.Errorf("%s: %s has no published version tag to attach; publish a version first", command, repoPath)
	}
	identity := entity + ":" + version
	out := AddResult{
		Socket:              socket,
		Identity:            identity,
		ConfigPath:          configPath,
		ConfigIsMachineWide: machineWide,
		Retraction:          recorder.advisoryOrNil(),
	}
	if opts.Delivery.Reports() {
		body, derr := workspaceconfig.PluginSocketRefDocument(configPath, socket, identity, plugintype.Role(opts.Role))
		if derr != nil {
			return res, fmt.Errorf("%s: compose %s: %w", command, configPath, derr)
		}
		out.Content = body
		return out, nil
	}
	if err := workspaceconfig.UpsertPluginSocketRef(configPath, socket, identity, plugintype.Role(opts.Role)); err != nil {
		return res, fmt.Errorf("%s: write %s: %w", command, configPath, err)
	}
	return out, nil
}

// addCLI resolves the command-extension query to a single plugin repository, pins
// its latest published version into an oci:// reference, and installs it as a
// top-level command through the supplied installer. It reuses the same
// registry-enumeration and version-resolution primitives the config-socket path
// uses, so a command-extension plugin is resolved exactly as any other type; only
// the terminal action differs — a command is installed rather than a
// configuration attachment written.
func addCLI(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, opts AddOptions) (AddResult, error) {
	const command = "aggrega plugin add"

	var res AddResult
	if opts.InstallCLIPlugin == nil {
		return res, fmt.Errorf("%s: this process cannot install a command-extension plugin; no installer was supplied", command)
	}

	configPath, machineWide, err := writeTarget(rw, workspaceconfig.OpCLI, opts.Global, opts.Workspace)
	if err != nil {
		return res, err
	}

	reg, host, err := RegistryTarget(rw.Config())
	if err != nil {
		return res, fmt.Errorf("%s: %w", command, err)
	}
	matches, err := registryops.EnumerateRepositories(ctx, reg, opts.Query, socketScope(enumerationLocation(rw.Config(), host), string(wasm.PluginTypeCLI)))
	if err != nil {
		return res, fmt.Errorf("%s: %s: %w", command, host, err)
	}
	repoPath, err := ResolveSingleRepo(matches, opts.Query, string(wasm.PluginTypeCLI))
	if err != nil {
		return res, err
	}
	var recorder retractionRecorder
	version, err := resolveLatestVersion(ctx, rw, host, repoPath, opts.noticeStreams(), &recorder)
	if err != nil {
		return res, fmt.Errorf("%s: %w", command, err)
	}
	if version == "" {
		return res, fmt.Errorf("%s: %s has no published version tag to install; publish a version first", command, repoPath)
	}
	ref := "oci://" + host + "/" + repoPath + ":" + version

	if err := opts.InstallCLIPlugin(ctx, rw, CLIInstallRequest{
		Reference:          ref,
		Global:             opts.Global,
		Workspace:          opts.Workspace,
		AcceptCapabilities: opts.AcceptCapabilities,
		AssumeYes:          opts.AssumeYes,
		In:                 opts.In,
		Out:                opts.Notices,
	}); err != nil {
		return res, err
	}
	return AddResult{
		Socket:              string(wasm.PluginTypeCLI),
		Reference:           ref,
		ConfigPath:          configPath,
		ConfigIsMachineWide: machineWide,
		Retraction:          recorder.advisoryOrNil(),
	}, nil
}

// socketScope admits only the repository paths a plugin serving socket publishes
// under beneath the location enumerated — the location's repository path, then
// the plugin/<socket>/ path prefix — so an enumeration for one socket never
// returns a plugin bound for another, nor one published outside the location.
func socketScope(under registryhost.Location, socket string) registryops.RepoScope {
	prefix := under.Repository(catalog.KindPathSegment("Plugin") + "/" + socket + "/")
	return func(repoPath string) bool {
		return strings.HasPrefix(repoPath, prefix)
	}
}

// enumerationLocation is the location an addition enumerates: the host the
// registry target reached, under the repository path the default registry
// carries. The enumeration lists every repository the host serves, so the path
// is what scopes it to the ones published through the default, and what a
// plugin's coordinate identity is read out from beneath.
func enumerationLocation(cfg workspaceconfig.Config, host string) registryhost.Location {
	return registryhost.Location{Host: host, Path: cfg.DefaultRegistryLocation().Path}
}

// ResolveSingleRepo reduces the scoped enumeration matches to exactly one plugin
// repository. A single match is used directly; when several match the substring an
// exact plugin-name (final path segment) hit is preferred; a query that still
// resolves to zero or more than one plugin halts loudly with the candidates rather
// than guessing which the caller meant.
func ResolveSingleRepo(matches []string, query, socket string) (string, error) {
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

// RegistryTarget resolves the registry to enumerate from the resolved workspace's
// default registry, and returns that registry's host: a registry is enumerated,
// logged into and trusted per host, so a repository path the default carries
// is not part of it, and scopes the enumeration instead. Production returns an
// authenticated remote registry; a test substitutes an in-memory lister so an
// addition runs without a network.
//
// It is a package-level seam because the live path needs registry credentials a
// sandbox does not carry: the resolution logic is proven against a fake target,
// and the live registry construction is operator-gated. Construction routes
// through the one registry home, so the loopback plain-HTTP handling and the
// credential authentication match the repository client exactly.
//
// It is the one seam for every surface, so a test that points it somewhere points
// it for the command line and a session alike, and the two cannot end up driving
// different registries.
var RegistryTarget = func(cfg workspaceconfig.Config) (registryops.RepoLister, string, error) {
	host := cfg.DefaultRegistryLocation().Host
	if host == "" {
		return nil, "", errors.New("no default registry is configured to search")
	}
	httpClient, err := egress.HTTPClient(cfg)
	if err != nil {
		// The network-egress gate refused (the workspace is offline); the caller
		// decides whether that is loud or silent.
		return nil, host, err
	}
	reg, err := oci.NewRemoteRegistry(host, httpClient)
	if err != nil {
		return nil, host, err
	}
	return reg, host, nil
}

// RepoTagLister lists the tags a registry serves for a plugin repository. It is a
// package-level seam so version resolution runs against a fake in tests;
// production lists live tags through the shared repository client so the loopback
// plain-HTTP and credential wiring match every other pull.
var RepoTagLister = func(ctx context.Context, cfg workspaceconfig.Config, host, repoPath string) ([]string, error) {
	httpClient, err := egress.HTTPClient(cfg)
	if err != nil {
		return nil, err
	}
	repo, err := oci.NewRemoteRepository(host+"/"+repoPath, httpClient)
	if err != nil {
		return nil, err
	}
	return oci.ListTags(ctx, repo)
}

// RepoWithdrawalLister reads the deprecation markers published against a plugin
// repository's versions and returns the ones whose own signature verifies, so a
// reference the local index does not carry can still be screened against a signed
// withdrawal rather than pinning one blind.
//
// It is a package-level seam so version resolution runs against a fake in tests;
// production reads through the shared repository client, so the loopback
// plain-HTTP and credential wiring match every other registry read. It is separate
// from the tag-listing seam above rather than folded into it because the two are
// wanted independently: the offline path takes neither, and a caller with nowhere
// to announce a substitution takes the tags and skips the markers entirely.
//
// The marker read, its trust policy and its verification are the ones the local
// index build performs, reached through that same single home — so a version's
// withdrawal is judged identically whether the index recorded it earlier or this
// operation reads it now, and the two cannot drift apart.
var RepoWithdrawalLister = func(ctx context.Context, cfg workspaceconfig.Config, host, repoPath string, versions []string, out io.Writer) ([]registryindex.VersionDeprecation, error) {
	httpClient, err := egress.HTTPClient(cfg)
	if err != nil {
		return nil, err
	}
	repo, err := oci.NewRemoteRepository(host+"/"+repoPath, httpClient)
	if err != nil {
		return nil, err
	}
	policy, _ := cfg.RegistryTrustPolicy(host)
	// The structured per-marker notes are for a surface that answers with values;
	// this path announces its substitutions in prose on the writer it was given.
	markers, _ := registryops.CollectVersionDeprecations(ctx, cfg, policy, repo, repoPath, versions, out)
	return markers, nil
}

// liveRetractionScreen builds the withdrawal screen for a plugin repository the
// local index does not carry, from markers read during this operation. It returns
// nil — leaving selection unscreened — whenever no screen can be formed, and each
// of those cases is a deliberate posture rather than a fallthrough:
//
// With no writer for the substitution there is nowhere to announce it, and
// excluding a version nobody can be told about is the silent substitution this
// exists to prevent; the marker reads are skipped along with it, so nothing is paid
// for a screen that would not be used. With no strict-SemVer tag there is no version the
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
func liveRetractionScreen(ctx context.Context, cfg workspaceconfig.Config, host, repoPath, reference string, tags []string, streams noticeStreams, rec *retractionRecorder) catalog.RetractionScreen {
	if streams.retractions == nil {
		return nil
	}
	versions := oci.SortedSemverTags(tags)
	if len(versions) == 0 {
		return nil
	}
	// The marker read reports what it found on the writer the account goes to: what
	// it says is which versions were asked about and what came back, which is not
	// the substitution and does not travel with it.
	deps, err := RepoWithdrawalLister(ctx, cfg, host, repoPath, versions, streams.notices)
	if err != nil {
		writeUnscreenedNotice(streams.retractions, reference, err.Error())
		// The same event the prose states, recorded for a caller answering with
		// values. It is recorded here rather than through the screen decorator
		// because there is no screen to decorate: the degradation IS that none was
		// formed.
		rec.recordUnscreened(reference, err.Error())
		return nil
	}
	return rec.watch(registryindex.NewLiveScreen(reference, deps, streams.retractions))
}

// ResolveLatestVersion returns the highest strict-SemVer version published for the
// plugin repository that has not been withdrawn, or the empty string when no
// conforming version is found. Discovery reads the local registry index by default
// — the snapshot a registry index refresh builds — so version resolution costs no
// network round-trip on the common path. Pre-release versions are excluded, the
// same rule the publishing and deprecation paths apply. A version a verified marker
// records as withdrawn is skipped and the substitution announced on out; out is
// also where an announcement would go for a screened selection, so a nil writer
// leaves selection unscreened rather than skipping a version silently.
//
// When the local index does not carry the repository — never built, or the
// repository is absent from the snapshot — resolution falls back to a live tag
// list, except in an offline workspace, where discovery must not network: it fails
// loudly, pointing at the index refresh. That fallback screens too: having no
// recorded withdrawal to read, it reads the repository's markers from the registry
// before selecting, so a withdrawn version is skipped there exactly as it is on the
// snapshot path. The actual plugin fetch is always live regardless; only this
// version discovery prefers the snapshot.
//
// A tag set with no strict-SemVer version resolves to the empty string rather than
// an error, which is what lets the caller name the missing publication in its own
// words. A repository whose every selectable version is withdrawn is the different
// case and does halt: nothing is left to attach, and the withdrawal is the reason.
func ResolveLatestVersion(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, host, repoPath string, out io.Writer) (string, error) {
	return resolveLatestVersion(ctx, rw, host, repoPath, noticeStreams{notices: out, retractions: out}, nil)
}

// resolveLatestVersion is ResolveLatestVersion with the two prose channels kept
// apart, and somewhere to record what the withdrawal screen concluded.
//
// The recorder is threaded rather than returned alongside the version because the
// conclusion is reached inside the two screen constructions below, one per
// discovery path, and a return value would have to be merged at every one of them.
// A nil recorder records nothing, which is what the exported entry passes: a caller
// that only wants the prose already has it on the one writer it supplied.
func resolveLatestVersion(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, host, repoPath string, streams noticeStreams, rec *retractionRecorder) (string, error) {
	if version, ok, err := versionFromIndex(rw, host, repoPath, streams.retractions, rec); err != nil {
		return "", err
	} else if ok {
		return version, nil
	}
	cfg := rw.Config()
	if cfg.Offline {
		return "", fmt.Errorf("offline workspace has no local index entry for %s; run 'aggrega registry update' to build the discovery index", repoPath)
	}
	tags, err := RepoTagLister(ctx, cfg, host, repoPath)
	if err != nil {
		return "", fmt.Errorf("list versions for %s: %w", repoPath, err)
	}
	// The screen and the selection key off this one reference, and the screen is
	// populated before selection runs so selection itself still reaches no registry.
	reference := host + "/" + repoPath
	screen := liveRetractionScreen(ctx, cfg, host, repoPath, reference, tags, streams, rec)
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

// versionFromIndex resolves a plugin repository's version from the host's local
// discovery index, screened against the withdrawal records that same index
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
// pulled, with the substitution announced on retractions. Absent a withdrawal the
// two resolve the same version, because the set retains the headline and selection
// applies the same rule the headline was recorded with.
//
// It announces on that one writer and no other. The markers behind this screen
// were read and accounted for when the index was built, so there is no marker read
// here to report — which is why this path takes the substitution's writer alone
// rather than the pair the live path needs.
//
// A record carrying no version set holds only its headline: there is nothing to
// select over, so the headline stands. A nil retractions leaves selection
// unscreened, which is the honest posture — an exclusion nobody can be told about
// is the silent substitution this exists to prevent.
func versionFromIndex(rw *workspaceconfig.ResolvedWorkspace, host, repoPath string, retractions io.Writer, rec *retractionRecorder) (string, bool, error) {
	loaded, err := registryindex.LoadHost(rw.Root(), rw.Config().ResolvedArtifactCacheDir(), host)
	if err != nil {
		if registryindex.IsNotBuilt(err) {
			return "", false, nil
		}
		return "", false, err
	}
	// The record lookup and the screen both key off this one reference through the
	// single index-URI mapping, so they cannot disagree about which record they mean.
	reference := host + "/" + repoPath
	uri := registryindex.URIForReference(reference)
	for _, record := range loaded.Records {
		if record.URI != uri {
			continue
		}
		if len(record.Versions) == 0 {
			return record.Version, true, nil
		}
		screen, herr := registryindex.NewScreen(rw, host, retractions)
		if herr != nil {
			return "", true, herr
		}
		version, serr := catalog.SelectScreenedVersion(rec.watch(screen), reference, record.Versions)
		if serr != nil {
			return "", true, serr
		}
		return version, true, nil
	}
	return "", false, nil
}
