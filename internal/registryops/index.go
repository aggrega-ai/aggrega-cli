// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// RepoLister enumerates the repository names a registry serves. It is the one
// registry capability the enumeration primitive needs — the OCI catalog listing
// surfaced by the registry client as Repositories. The production value is an
// authenticated remote registry; a test substitutes an in-memory lister so the
// enumeration runs without a network.
type RepoLister interface {
	Repositories(ctx context.Context, last string, fn func(repos []string) error) error
}

// RepoScope decides whether a repository path is within an enumeration's scope.
// It is what makes one enumeration primitive serve several callers: a plugin
// search and a whole-catalog index pass different scopes over the same walk.
type RepoScope func(repoPath string) bool

// PluginScope admits the repository paths Wasm plugins publish under — the
// plugin path prefix the plugin reference convention uses. It scopes an
// enumeration to installable plugins rather than the whole catalog.
func PluginScope(repoPath string) bool {
	return strings.HasPrefix(repoPath, catalog.KindPathSegment("Plugin")+"/")
}

// CatalogScope admits every repository — a catalog enumeration spans the full
// entity surface, not one kind.
func CatalogScope(string) bool { return true }

// EnumerateRepositories is the single registry-enumeration primitive. It walks
// the registry's repositories, keeps those the scope admits whose path contains
// the case-insensitive query substring (an empty query matches every in-scope
// repository), and returns the matches in ascending order. Callers differ only
// in the scope they pass; the walk, the filtering and the ordering live here
// once so they never drift into several enumerations.
//
// The walk is progress-aware. The registry delivers each catalog page to the
// callback before it parses that page's pagination header, so a registry that
// serves a malformed pagination header fails only after the first page's
// repositories were already collected. In that case the matches gathered so far
// are returned rather than discarded — a partial result beats no result. An
// error raised before any page was delivered (a connection or first-request
// failure) still propagates, since there is nothing to fall back to.
func EnumerateRepositories(ctx context.Context, reg RepoLister, query string, scope RepoScope) ([]string, error) {
	q := strings.ToLower(query)
	var matches []string
	pageDelivered := false
	err := reg.Repositories(ctx, "", func(repos []string) error {
		pageDelivered = true
		for _, repo := range repos {
			if !scope(repo) {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(repo), q) {
				continue
			}
			matches = append(matches, repo)
		}
		return nil
	})
	if err != nil && !pageDelivered {
		return nil, fmt.Errorf("search registry: %w", err)
	}
	sort.Strings(matches)
	return matches, nil
}

// IndexSource is the registry capability a local index build needs: it
// enumerates the repositories a registry serves and hands back a per-repository
// target for tag listing and manifest reads. It embeds the same repository
// enumeration the search primitive consumes, so the walk reuses that one
// progress-aware enumeration rather than re-deriving it. Production wires a
// remote registry for enumeration and a per-repository remote repository for
// reads; a test substitutes an in-memory source so the build runs without a
// network.
type IndexSource interface {
	RepoLister
	Repository(repo string) (oras.ReadOnlyTarget, error)
	// ConditionalTags lists a repository's tags, revalidating against a stored
	// listing validator: when ifNoneMatch matches the registry's current validator
	// the registry answers that the listing is unchanged (notModified true, tags
	// nil), otherwise it returns the current tags and the new validator. truncated
	// reports a listing that did not fit one page, so its single-request validator
	// does not cover the whole set. An empty ifNoneMatch lists unconditionally.
	ConditionalTags(ctx context.Context, repo, ifNoneMatch string) (tags []string, etag string, notModified bool, truncated bool, err error)
}

// remoteIndexSource is the production IndexSource. It enumerates through an
// authenticated remote registry and constructs a per-repository remote
// repository on demand, both through the one registry-client home so the
// enumeration and the per-repo reads share loopback and credential handling.
type remoteIndexSource struct {
	reg        RepoLister
	host       string
	httpClient *http.Client
}

func (s remoteIndexSource) Repositories(ctx context.Context, last string, fn func(repos []string) error) error {
	return s.reg.Repositories(ctx, last, fn)
}

func (s remoteIndexSource) Repository(repo string) (oras.ReadOnlyTarget, error) {
	return oci.NewRemoteRepository(s.host+"/"+repo, s.httpClient)
}

func (s remoteIndexSource) ConditionalTags(ctx context.Context, repo, ifNoneMatch string) (tags []string, etag string, notModified bool, truncated bool, err error) {
	return oci.ConditionalListTags(ctx, s.host, repo, ifNoneMatch, s.httpClient)
}

// IndexTarget resolves the index source for a registry host. Production returns
// a remote source; a test substitutes an in-memory source so the index build
// runs without a network. It is a package-level seam because the live path
// needs registry credentials a sandbox does not carry — the index-build logic
// is proven against a fake source, and the live registry resolution is
// operator-gated.
//
// It is the one seam for every surface, so a test that points it somewhere
// points it for the command line and the session alike, and the two cannot end
// up driving different registries.
var IndexTarget = func(cfg workspaceconfig.Config, host string) (IndexSource, error) {
	httpClient, err := egress.HTTPClient(cfg)
	if err != nil {
		return nil, err
	}
	reg, err := oci.NewRemoteRegistry(host, httpClient)
	if err != nil {
		return nil, err
	}
	return remoteIndexSource{reg: reg, host: host, httpClient: httpClient}, nil
}

// HostIndex is the in-memory aggregate an index build produces for one host:
// the host it was built from and the entity records, one per repository whose
// latest version conforms to strict SemVer. It is the shape the writer iterates
// to emit one file per repository under the host's index directory; the on-disk
// layout is per-repository files, not this struct serialized whole. Records are
// held in URI-then-version order.
type HostIndex struct {
	Host    string                 `json:"host"`
	Records []registryindex.Record `json:"records"`
}

// SkippedRepo is one repository the walk could not read, with what stopped it.
//
// A skip is reported rather than swallowed because one unreadable repository
// does not abandon the walk: the index that results is incomplete, and a caller
// acting on it needs to know which repositories are missing from it rather than
// reading a short answer as a complete one.
type SkippedRepo struct {
	Repo   string `json:"repo"`
	Reason string `json:"reason"`
}

// DeprecationNote is one deprecation marker the walk met, and what it did with
// it.
//
// Recorded distinguishes a marker whose own signature verified — and which the
// index therefore carries — from one that was present and not honoured. The two
// are reported on the same footing because reporting only the failures would
// invert which outcome is audible: recording many markers would pass in silence
// while declining one would announce itself.
type DeprecationNote struct {
	Repo     string `json:"repo"`
	Version  string `json:"version"`
	Recorded bool   `json:"recorded"`
	Severity string `json:"severity,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// HostUpdate is what an index build did for one registry.
//
// DeprecationsExamined says whether the walk looked for markers at all. It is
// separate from the count because "this registry published no honoured marker"
// and "this run had no trust basis to judge one" are different facts, and a
// caller shown only a zero cannot tell them apart.
//
// Err carries a host-level failure as text, so a walk over several registries
// reports which of them failed without the caller having to correlate an error
// against a list.
type HostUpdate struct {
	Host                 string            `json:"host"`
	Indexed              int               `json:"indexed"`
	DeprecationsExamined bool              `json:"deprecationsExamined"`
	DeprecationsRecorded int               `json:"deprecationsRecorded"`
	Skipped              []SkippedRepo     `json:"skipped"`
	Deprecations         []DeprecationNote `json:"deprecations"`
	Err                  string            `json:"error,omitempty"`
}

// UpdateResult is what a whole index build did, per host.
//
// NoRegistriesConfigured distinguishes "nothing is configured" from "every
// configured registry was indexed and none held anything", which are different
// answers to a caller deciding what to do next.
//
// IndexRoot is the directory the indexes were written under. It is reported
// because it is what makes the scope of this operation readable: the index is
// written inside the workspace's own configured cache directory, so an index
// build changes nothing any other workspace resolves through — unlike a
// registry registration, which defaults to the machine-wide configuration.
type UpdateResult struct {
	Hosts                  []HostUpdate `json:"hosts"`
	Failed                 []string     `json:"failed"`
	IndexRoot              string       `json:"indexRoot"`
	NoRegistriesConfigured bool         `json:"noRegistriesConfigured"`
}

// UpdateOptions is what an index build decides.
//
// Registry selects a single configured registry by host or friendly alias;
// empty indexes every configured registry, and the two paths differ in how a
// host-level failure is handled. Upgrade forces a full re-read of every
// repository, ignoring the cached revalidation validators — it is the escape
// from the conditional refresh's one bound: a deprecation added to an existing
// version whose tag set and manifest digest are unchanged is invisible to a
// digest comparison, and a full re-read picks it up.
type UpdateOptions struct {
	Registry string
	Upgrade  bool
	// Now supplies the freshness timestamp written to each host's sibling file.
	// Nil selects the live clock; a test injects a frozen time for determinism.
	Now func() time.Time
}

// Update walks each selected registry's repositories, reads each repository's
// latest-version manifest annotations, and writes a deterministic local
// searchable index a later offline search reads.
//
// 🔴 It REACHES REGISTRIES, and it is the only operation in this package that
// does. Identity resolves from a supplied or ambient credential source and from
// nothing else: nothing here prompts, so a registry that needs a credential no
// source carries produces a refusal naming what it wanted rather than a wait.
// That is what makes the operation safe on a surface with nobody watching it.
//
// What it WRITES is inside the workspace: the index lands under the workspace's
// own configured cache directory, so an index build changes nothing another
// workspace on the machine resolves through.
//
// Host-level failure handling depends on which path selected the hosts. When the
// caller targets a single registry, a failure there is returned immediately —
// the caller asked for that one registry and gets a loud error. When no
// selection is made, one unreachable registry must not abandon the reachable
// ones: a host whose source resolution, walk, or index write fails is noted and
// the walk continues to the next host, mirroring the per-repository
// skip-and-continue posture. After the walk, if any host failed the run returns
// a non-nil error summarising the count so the exit status stays non-zero, but
// only after every reachable registry has been indexed and written.
//
// notices carries the walk's running commentary — a skipped repository, a
// deprecation marker met — for a surface with a person watching it. It is a
// writer rather than returned data because the prose is written for a reader,
// and the same facts travel structurally on the returned result for a surface
// that has no reader. A caller with nobody watching discards it.
//
// The result is returned alongside any error rather than instead of it, so a
// partial walk is never reported as no walk at all. A stop is reported the same
// way and for the same reason: the hosts already indexed have been written, so a
// withdrawn walk reports what it indexed rather than claiming it indexed nothing.
func Update(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, opts UpdateOptions, notices io.Writer) (UpdateResult, error) {
	var res UpdateResult
	if notices == nil {
		notices = io.Discard
	}
	// Walking the registries is this operation's entire work, so an offline
	// workspace refuses it here rather than at the first host's transport. There
	// is no offline-safe form to carve out: the index records what each registry
	// currently serves, which is a fact about registry state and cannot be
	// learned without reaching one. Reusing the index already on disk is what a
	// later search does, and needs no walk at all.
	if err := egress.RefuseIfOffline(rw, "building the registry index is a network round trip"); err != nil {
		return res, fmt.Errorf("aggrega registry update: %w", err)
	}
	cfg := rw.Config()

	singleExplicit := opts.Registry != ""
	var hosts []string
	if singleExplicit {
		host, rerr := cfg.ResolveRegistry(opts.Registry)
		if rerr != nil {
			return res, fmt.Errorf("aggrega registry update: %w", rerr)
		}
		hosts = []string{host}
	} else {
		for _, r := range cfg.Registries {
			hosts = append(hosts, r.Host)
		}
	}
	if len(hosts) == 0 {
		res.NoRegistriesConfigured = true
		fmt.Fprintln(notices, "No registries configured.")
		return res, nil
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}

	root := rw.Root()
	cacheDir := cfg.ResolvedArtifactCacheDir()
	res.IndexRoot = oci.IndexRoot(root, cacheDir)
	for _, host := range hosts {
		// One interruption point per host, which is this walk's real seam: each
		// iteration is a network round trip that enumerates a registry's
		// repositories and writes an index. A host not yet reached is one never
		// contacted, and the check precedes the round trip rather than following it.
		if serr := stopped(ctx); serr != nil {
			return res, serr
		}
		hu, herr := updateHostIndex(ctx, cfg, root, cacheDir, host, now, opts.Upgrade, notices)
		if herr != nil {
			hu.Err = herr.Error()
			res.Hosts = append(res.Hosts, hu)
			if singleExplicit {
				return res, fmt.Errorf("aggrega registry update: %s: %w", host, herr)
			}
			fmt.Fprintf(notices, "registry %s: %v\n", host, herr)
			res.Failed = append(res.Failed, host)
			continue
		}
		res.Hosts = append(res.Hosts, hu)
		fmt.Fprintf(notices, "Indexed %d entities from %s\n", hu.Indexed, host)
	}
	if len(res.Failed) > 0 {
		return res, fmt.Errorf("aggrega registry update: %d of %d registries failed to update", len(res.Failed), len(hosts))
	}
	return res, nil
}

// updateHostIndex resolves one host's index source, builds its index, and writes
// the records file plus its freshness sibling, returning what the host's walk
// found. The returned error is unwrapped by host so the caller frames it for the
// single-registry path (returned immediately) or the all-registries path (noted
// per host, walk continues).
func updateHostIndex(
	ctx context.Context,
	cfg workspaceconfig.Config,
	root, cacheDir, host string,
	now func() time.Time,
	upgrade bool,
	notices io.Writer,
) (HostUpdate, error) {
	hu := HostUpdate{Host: host}
	src, err := IndexTarget(cfg, host)
	if err != nil {
		return hu, err
	}
	var prior map[string]registryindex.Record
	if !upgrade {
		prior = loadPriorRecords(root, cacheDir, host)
	}
	// The host's own trust policy, resolved once for the whole walk: it decides
	// which signers may withdraw a version of anything this host serves, so every
	// repository under the host is verified against the same set.
	policy, _ := cfg.RegistryTrustPolicy(host)
	idx, err := buildHostIndex(ctx, cfg, policy, host, src, prior, &hu, notices)
	if err != nil {
		return hu, err
	}
	if err := WriteHostIndex(root, cacheDir, idx, now().UTC()); err != nil {
		return hu, err
	}
	hu.Indexed = len(idx.Records)
	hu.DeprecationsExamined = deprecationScanEnabled(cfg, policy)
	hu.DeprecationsRecorded = countRecordedDeprecations(idx)
	reportDeprecationScan(notices, hu, host)
	return hu, nil
}

// reportDeprecationScan states the outcome of the host's deprecation-marker scan,
// which the per-marker notices alone cannot: they are written only when there is a
// marker to write about, so on their own an empty walk and a walk that never looked
// produce the same silence. The two are different facts — one says the registry
// published no honored marker, the other says this run had no trust basis to judge
// one — and an operator who attached a marker and sees neither notice needs to know
// which of them happened.
func reportDeprecationScan(notices io.Writer, hu HostUpdate, host string) {
	if !hu.DeprecationsExamined {
		fmt.Fprintf(notices, "Deprecation markers not examined for %s: signature verification is disabled for this registry\n", host)
		return
	}
	fmt.Fprintf(notices, "Recorded %d deprecation markers from %s\n", hu.DeprecationsRecorded, host)
}

// loadPriorRecords loads the previous per-host index records keyed by URI, so a
// conditional refresh can reuse an unchanged repository's record. A not-yet-built
// or unreadable prior index yields a nil map, degrading the refresh to a full
// build rather than failing — the first update after a cache wipe re-reads
// everything.
func loadPriorRecords(root, cacheDir, host string) map[string]registryindex.Record {
	loaded, err := registryindex.LoadHost(root, cacheDir, host)
	if err != nil {
		return nil
	}
	prior := make(map[string]registryindex.Record, len(loaded.Records))
	for _, rec := range loaded.Records {
		prior[rec.URI] = rec
	}
	return prior
}

// buildHostIndex enumerates the host's repositories through the one enumeration
// primitive (progress-aware and partial-on-error) and appends one record per
// repository whose latest version conforms to strict SemVer. Each repository is
// revalidated conditionally against prior (keyed by URI): a repository whose tag
// set and headline manifest digest match its prior record reuses that record,
// skipping the manifest read and the per-version referrer scan. A nil prior
// forces a full read of every repository. Records are sorted by URI then version
// so the emitted files are deterministic. A repository that dropped out of the
// registry is simply not enumerated, so it contributes no record and its file is
// not rewritten (the writer resets the host directory).
func buildHostIndex(
	ctx context.Context,
	wsCfg workspaceconfig.Config,
	policy workspaceconfig.RegistryConfig,
	host string,
	src IndexSource,
	prior map[string]registryindex.Record,
	hu *HostUpdate,
	notices io.Writer,
) (HostIndex, error) {
	repos, err := EnumerateRepositories(ctx, src, "", CatalogScope)
	if err != nil {
		return HostIndex{}, err
	}
	idx := HostIndex{Host: host}
	for _, repo := range repos {
		rec, ok := indexRepo(ctx, wsCfg, policy, host, src, repo, prior, hu, notices)
		if !ok {
			continue
		}
		idx.Records = append(idx.Records, rec)
	}
	registryindex.SortRecords(idx.Records)
	return idx, nil
}

// skipRepo records one unreadable repository on the host's result and writes the
// running commentary for a reader, so the prose and the structured account of a
// skip are produced from one place and cannot come to disagree.
func skipRepo(hu *HostUpdate, notices io.Writer, repo string, reason string) {
	hu.Skipped = append(hu.Skipped, SkippedRepo{Repo: repo, Reason: reason})
	fmt.Fprintf(notices, "  skipped %s: %s\n", repo, reason)
}

// indexRepo builds the record for one repository's latest published version. It
// revalidates against the prior record for the repository's URI in two conditional
// steps before deciding to re-read. First a conditional tags listing: when the
// stored listing validator still matches, the tag set is unchanged and the tag
// re-list is skipped. Then a headline manifest digest re-resolve (a cheap metadata
// read, no content blob): when the digest also matches the prior record, that
// record is reused untouched — skipping the manifest read and the per-version
// referrer scan, so an unchanged repository costs one conditional tag listing plus
// one cheap digest resolve. Only a changed or previously-unseen repository re-reads
// the manifest annotations and re-scans referrers. A nil prior (a full-upgrade
// refresh, or the first build) reuses nothing.
//
// A repository with no strict-SemVer version is skipped silently (a deterministic
// non-error). A repository that cannot be read — no per-repo target, a tag-list
// failure, a resolve failure, or a manifest read failure — is skipped with a note
// so one unreadable repository does not abandon the walk, mirroring the
// enumeration primitive's partial-on-error posture.
func indexRepo(
	ctx context.Context,
	wsCfg workspaceconfig.Config,
	policy workspaceconfig.RegistryConfig,
	host string,
	src IndexSource,
	repo string,
	prior map[string]registryindex.Record,
	hu *HostUpdate,
	notices io.Writer,
) (registryindex.Record, bool) {
	target, err := src.Repository(repo)
	if err != nil {
		skipRepo(hu, notices, repo, err.Error())
		return registryindex.Record{}, false
	}
	uri := "oci://" + host + "/" + repo
	p, hasPrior := prior[uri]

	// Conditional tags listing: present the prior record's listing validator so an
	// unchanged tag set answers as not-modified and skips the re-list.
	priorListing := ""
	if hasPrior {
		priorListing = p.ListingDigest
	}
	tags, listingDigest, notModified, truncated, err := src.ConditionalTags(ctx, repo, priorListing)
	if err != nil {
		skipRepo(hu, notices, repo, err.Error())
		return registryindex.Record{}, false
	}
	if notModified {
		// The tag set is unchanged. Confirm the headline manifest digest is unchanged
		// too — a tag re-pushed to new content changes the digest without changing the
		// set — and reuse the prior record when it is.
		digest, exists, rerr := oci.Resolve(ctx, target, p.Version)
		if rerr == nil && exists && p.ManifestDigest != "" && p.ManifestDigest == digest {
			return p, true
		}
		// The headline manifest moved (or the resolve failed): re-list unconditionally
		// to get the current tag set and validator, then fall through to a full read.
		tags, listingDigest, _, truncated, err = src.ConditionalTags(ctx, repo, "")
		if err != nil {
			skipRepo(hu, notices, repo, err.Error())
			return registryindex.Record{}, false
		}
	}
	// A truncated listing's single-request validator does not cover the whole tag set,
	// so fall back to the full enumeration and record no validator (the next refresh
	// re-reads rather than falsely matching a partial validator).
	if truncated {
		tags, err = oci.ListTags(ctx, target)
		if err != nil {
			skipRepo(hu, notices, repo, err.Error())
			return registryindex.Record{}, false
		}
		listingDigest = ""
	}

	version := oci.SelectLatestVersion(tags, false /* includePreRelease */)
	if version == "" {
		return registryindex.Record{}, false
	}
	versions := oci.SortedSemverTags(tags)

	digest, exists, err := oci.Resolve(ctx, target, version)
	if err != nil {
		skipRepo(hu, notices, repo, err.Error())
		return registryindex.Record{}, false
	}
	if !exists {
		skipRepo(hu, notices, repo, fmt.Sprintf("selected version %s no longer resolves", version))
		return registryindex.Record{}, false
	}
	if hasPrior && p.ManifestDigest != "" && p.ManifestDigest == digest && equalStrings(p.Versions, versions) {
		// The manifest and tag set are unchanged; reuse the prior record but carry the
		// refreshed listing validator forward (it may have been absent before).
		p.ListingDigest = listingDigest
		return p, true
	}

	annotations, manifestDigest, err := oci.FetchManifestAnnotations(ctx, target, version)
	if err != nil {
		skipRepo(hu, notices, repo, err.Error())
		return registryindex.Record{}, false
	}
	rec, err := recordFromAnnotations(host, repo, version, annotations)
	if err != nil {
		skipRepo(hu, notices, repo, err.Error())
		return registryindex.Record{}, false
	}
	rec.Versions = versions
	rec.ManifestDigest = manifestDigest
	rec.ListingDigest = listingDigest
	deprecations, notes := CollectVersionDeprecations(ctx, wsCfg, policy, target, repo, versions, notices)
	rec.Deprecations = deprecations
	hu.Deprecations = append(hu.Deprecations, notes...)
	return rec, true
}

// equalStrings reports whether two string slices are element-wise equal. The
// conditional refresh compares a repository's live tag set against the prior
// record's Versions to decide reuse.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// collectVersionDeprecations scans each conforming version's deprecation
// referrers and records the authoritative marker for every version that carries a
// marker whose OWN signature verifies, in the ascending version order it is given.
// Each version's marker is selected by the same newest-issuedOn rule the closure
// discovery pass uses (with the manifest digest as the deterministic tiebreak), and
// verified through the same single verification home, so the two passes cannot
// disagree about which marker is authoritative or which signer is honored.
//
// Recording implies verification, and that invariant is what the record is for: a
// consumer reading the index offline — including the discovery step that refuses to
// select a withdrawn version — acts on a signed claim it does not have to re-judge.
// A marker with no signature, or one that does not verify against the honored set,
// is not recorded at all rather than recorded as unverified: a record that had to be
// checked before use would put the trust judgement back at every read site.
//
// A registry whose policy skips signature verification records nothing. That is the
// operator's own choice rather than a gap — with verification off there is no trust
// basis for a withdrawal, and inventing one from an unauthenticated marker is
// exactly what must not happen.
//
// The original artifact's own signer is recovered only on a keyless policy, where
// the honored set folds it in so a publisher can withdraw their own artifact. A
// keyed policy pins one key, so the same key already IS the same signer and the two
// extra reads are skipped.
//
// Every step stays best-effort: a referrer-listing, decode or verification failure
// skips that version rather than failing the index build, mirroring the walk's
// partial-on-error posture. A version with no honored marker contributes nothing, so
// the slice is empty when no version is withdrawn or deprecated.
//
// A marker that is PRESENT but not honored is noted rather than dropped in silence.
// Silence there would be the worst of the three outcomes: the operator would see an
// index with no withdrawal, having attached one, and nothing would say why. The note
// mirrors the closure discovery pass, which reports the same condition in the same
// words.
//
// A marker that IS honored is noted on the same footing, stating the severity
// through the shared wording rule so this line and every other notice about a
// recorded severity describe the same value the same way. The parenthesis is left
// to the two failure lines, where it carries the fault; stating the severity in a
// second parenthesis here would nest one inside the other for the advisory tiers,
// whose wording names the tier as well as the value. Reporting only the failures
// would invert which outcome is audible: recording many markers would pass in
// silence while declining one would announce itself, and an operator watching the
// walk could not tell a registry that published no marker from a run that recorded
// a dozen.
// It returns the notes alongside the markers so a caller with a structured
// answer to give reports the same three outcomes the prose reports, from the
// same pass. A caller that only needs the markers — the attach path, screening a
// reference the local index does not carry — discards them.
func CollectVersionDeprecations(
	ctx context.Context,
	wsCfg workspaceconfig.Config,
	policy workspaceconfig.RegistryConfig,
	target oras.ReadOnlyTarget,
	repo string,
	versions []string,
	notices io.Writer,
) ([]registryindex.VersionDeprecation, []DeprecationNote) {
	if !deprecationScanEnabled(wsCfg, policy) {
		return nil, nil
	}
	if notices == nil {
		notices = io.Discard
	}
	var deprecations []registryindex.VersionDeprecation
	var notes []DeprecationNote
	for _, version := range versions {
		markers, err := oci.ListReferrers(ctx, target, version, deprecation.ArtifactType)
		if err != nil || len(markers) == 0 {
			continue
		}
		markerDesc, marker, err := registryindex.LatestMarker(ctx, target, markers)
		if err != nil {
			notes = append(notes, DeprecationNote{Repo: repo, Version: version, Reason: err.Error()})
			fmt.Fprintf(notices, "  deprecation: %s:%s: marker present but not readable (%v); not recorded\n", repo, version, err)
			continue
		}
		var origSigner *pullverify.Result
		if policy.PublicKey == "" {
			origSigner = registryindex.VerifiedArtifactSigner(ctx, wsCfg, target, version, policy)
		}
		signer, err := registryindex.VerifyMarkerSignature(ctx, wsCfg, target, markerDesc.Digest.String(), policy, origSigner)
		if err != nil {
			notes = append(notes, DeprecationNote{Repo: repo, Version: version, Reason: err.Error()})
			fmt.Fprintf(notices, "  deprecation: %s:%s: marker present but not honored (%v); not recorded\n", repo, version, err)
			continue
		}
		notes = append(notes, DeprecationNote{
			Repo: repo, Version: version, Recorded: true, Severity: marker.Severity.StatedAs(),
		})
		fmt.Fprintf(notices, "  deprecation: %s:%s: marker present and honored; recorded as %s\n", repo, version, marker.Severity.StatedAs())
		deprecations = append(deprecations, registryindex.VersionDeprecation{Version: version, Marker: marker, Signer: signer})
	}
	return deprecations, notes
}

// deprecationScanEnabled reports whether this run examines a host's published
// versions for deprecation markers at all. Verification switched off — globally or
// for this one registry — means there is no trust basis on which a marker could be
// recorded, so the walk does not look.
//
// It is the single definition of that condition. The scan reads it to decide whether
// to look, and the walk's summary reads the same function to decide which of the two
// outcomes it is reporting, so "no marker was recorded" and "no version was examined"
// cannot come to disagree about which of them happened.
func deprecationScanEnabled(wsCfg workspaceconfig.Config, policy workspaceconfig.RegistryConfig) bool {
	return !wsCfg.SkipSignatureVerification && !policy.SkipSignatureVerification
}

// countRecordedDeprecations totals the deprecation markers a host's assembled index
// records across every repository, so the walk can state the outcome of its scan
// rather than leaving the operator to infer it from the absence of notices.
func countRecordedDeprecations(idx HostIndex) int {
	n := 0
	for _, rec := range idx.Records {
		n += len(rec.Deprecations)
	}
	return n
}

// recordFromAnnotations assembles an index record from a repository's latest
// version and its manifest annotations. Every facet is read through the
// searchable-annotation constants, so no annotation key is spelled here as a
// bare string. Capabilities were stamped comma-joined in author order and are
// split back into a list.
//
// Labels are decoded from the label-map annotation the publish stamped, so a
// search matches on every label the entity's author wrote. The decode preserves
// author order, which is what keeps a rewritten record byte-identical for
// unchanged registry state. A record carries no labels when the published
// manifest carries no label map — an entity that declares none, and an artifact
// published before the whole map travelled.
//
// A label map that is present but undecodable is an error rather than a record
// with the labels dropped: a partially-populated label map would make a selector
// silently match nothing, which is indistinguishable from the entity not
// existing.
func recordFromAnnotations(host, repo, version string, ann map[string]string) (registryindex.Record, error) {
	rec := registryindex.Record{
		URI:         "oci://" + host + "/" + repo,
		Name:        repo,
		Version:     version,
		Kind:        ann[oci.AnnotationKind],
		Description: ann[ocispec.AnnotationDescription],
		Category:    ann[oci.AnnotationCategory],
		PluginType:  ann[oci.AnnotationPluginType],
	}
	if caps := ann[oci.AnnotationCapabilities]; caps != "" {
		rec.Capabilities = strings.Split(caps, ",")
	}
	if raw := ann[oci.AnnotationLabels]; raw != "" {
		labels := types.NewOrderedMap[string]()
		if err := labels.UnmarshalJSON([]byte(raw)); err != nil {
			return registryindex.Record{}, fmt.Errorf("decoding labels of %s:%s: %w", repo, version, err)
		}
		rec.Labels = labels
	}
	return rec, nil
}

// WriteHostIndex writes one file per repository under the host's index
// directory, plus the host's freshness sibling, under the artifact cache's index
// subtree. The host directory is rebuilt from scratch — removed then recreated —
// so the file set on disk is exactly the current record set: a repository that
// dropped out of the registry leaves no stale file behind. Each per-repo file is
// deterministic — indented JSON with a trailing newline, ordered slices, and
// author-order labels — so two builds against the same registry state produce
// byte-identical files and an identical file set. The timestamp lives only in
// the freshness sibling file, beside the host directory, so it never perturbs
// the records.
//
// It is exported so a test can lay out an index fixture through the very writer
// the product uses, rather than through a second hand-written layout that would
// agree with it until the day the layout changed.
func WriteHostIndex(workspaceRoot, cacheDir string, idx HostIndex, now time.Time) error {
	hostDir := oci.IndexBasePath(workspaceRoot, cacheDir, idx.Host)
	if err := os.RemoveAll(hostDir); err != nil {
		return fmt.Errorf("index reset %s: %w", hostDir, err)
	}
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		return fmt.Errorf("index mkdir %s: %w", hostDir, err)
	}
	for _, rec := range idx.Records {
		data, err := json.MarshalIndent(rec, "", "  ")
		if err != nil {
			return fmt.Errorf("index marshal %s: %w", rec.Name, err)
		}
		data = append(data, '\n')
		repoPath := oci.IndexRepoPath(workspaceRoot, cacheDir, idx.Host, rec.Name)
		if err := os.WriteFile(repoPath, data, 0o644); err != nil {
			return fmt.Errorf("index write %s: %w", repoPath, err)
		}
	}

	base := oci.IndexBasePath(workspaceRoot, cacheDir, idx.Host)
	stamp := now.Format(time.RFC3339) + "\n"
	if err := os.WriteFile(base+".updated", []byte(stamp), 0o644); err != nil {
		return fmt.Errorf("index timestamp write %s: %w", base+".updated", err)
	}
	return nil
}
