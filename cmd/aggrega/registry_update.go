// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

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
	"github.com/spf13/cobra"
	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// registryIndexSource is the registry capability a local index build needs: it
// enumerates the repositories a registry serves and hands back a per-repository
// target for tag listing and manifest reads. It embeds the same repository
// enumeration the search primitive consumes, so the walk reuses that one
// progress-aware enumeration rather than re-deriving it. Production wires an
// oras remote registry for enumeration and a per-repository remote repository
// for reads; a test substitutes an in-memory source so the build runs without a
// network.
type registryIndexSource interface {
	registryRepoLister
	Repository(repo string) (oras.ReadOnlyTarget, error)
	// ConditionalTags lists a repository's tags, revalidating against a stored
	// listing validator: when ifNoneMatch matches the registry's current validator
	// the registry answers that the listing is unchanged (notModified true, tags
	// nil), otherwise it returns the current tags and the new validator. truncated
	// reports a listing that did not fit one page, so its single-request validator
	// does not cover the whole set. An empty ifNoneMatch lists unconditionally.
	ConditionalTags(ctx context.Context, repo, ifNoneMatch string) (tags []string, etag string, notModified bool, truncated bool, err error)
}

// remoteIndexSource is the production registryIndexSource. It enumerates through
// an authenticated remote registry and constructs a per-repository remote
// repository on demand, both through the one registry-client home so the
// enumeration and the per-repo reads share loopback and credential handling.
type remoteIndexSource struct {
	reg        registryRepoLister
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

// registryIndexTarget resolves the index source for a registry host. Production
// returns a remote source; a test substitutes an in-memory source so the index
// build runs without a network. It is a package-level seam because the live
// path needs registry credentials the sandbox does not carry — the index-build
// logic is proven against a fake source, and the live registry resolution is
// operator-gated. It mirrors registrySearchTarget.
var registryIndexTarget = func(cfg workspaceconfig.Config, host string) (registryIndexSource, error) {
	httpClient, err := workspaceEgressClient(cfg)
	if err != nil {
		return nil, err
	}
	reg, err := oci.NewRemoteRegistry(host, httpClient)
	if err != nil {
		return nil, err
	}
	return remoteIndexSource{reg: reg, host: host, httpClient: httpClient}, nil
}

// IndexRecord is one entity's row in a local registry index: its OCI URI, the
// latest published version, the full list of published versions, and the
// searchable catalog facets read back from the latest version's manifest
// annotations. Labels is the entity's whole authored label map in author order,
// so a search matches records with the same label selector the catalog uses; it
// is absent when the published manifest carries no label map. PluginType and
// Capabilities carry a plugin's socket facets and are absent on non-plugin
// entities.
//
// Versions is every published tag that conforms to strict SemVer, in ascending
// order (pre-release tags included). Version stays the selected headline
// (highest released). ManifestDigest is the resolved manifest digest of the
// headline version, recorded as a revalidation validator: a later refresh
// re-resolves the version and reuses the stored record when the digest is
// unchanged. ListingDigest is the registry's strong validator for the
// repository's tag listing, recorded so a later refresh can revalidate the tag
// set with a conditional request and skip the re-list when the registry answers
// that the listing is unchanged; it is empty when the registry serves no
// validator or the listing spanned more than one page. Deprecations records, per
// deprecated version, the authoritative deprecation marker discovered from that
// version's referrers together with that marker's verified signature, so a
// consumer reads a signed claim from the local index rather than probing the
// registry live; a version whose marker does not verify contributes no entry. All
// are omitted when empty.
type IndexRecord struct {
	URI            string                    `json:"uri"`
	Name           string                    `json:"name"`
	Version        string                    `json:"version"`
	Versions       []string                  `json:"versions,omitempty"`
	Kind           string                    `json:"kind,omitempty"`
	Description    string                    `json:"description,omitempty"`
	Category       string                    `json:"category,omitempty"`
	Labels         *types.OrderedMap[string] `json:"labels,omitempty"`
	PluginType     string                    `json:"pluginType,omitempty"`
	Capabilities   []string                  `json:"capabilities,omitempty"`
	ManifestDigest string                    `json:"manifestDigest,omitempty"`
	ListingDigest  string                    `json:"listingDigest,omitempty"`
	Deprecations   []VersionDeprecation      `json:"deprecations,omitempty"`
}

// VersionDeprecation pairs a strict-SemVer version with the authoritative
// deprecation marker discovered on that version's referrers, plus the verified
// signature of that marker. The whole marker is stored (all its fields), so a
// display reconstructs the same view off the local index that a live referrer
// probe produced. The marshalled marker carries a fixed field order, so a record
// is byte-identical for identical registry state.
//
// Signer is what makes the record a trust decision rather than a hint: a marker
// is recorded only when its OWN signature verifies against the honored signer set,
// so a present record implies a verified one and a consumer needs no second
// judgement. It is a pointer so a record written before verification existed
// decodes with no signer and is therefore not honored — the safe direction, since
// an unverified record can then only fail to exclude a version, never exclude one
// on unauthenticated evidence.
type VersionDeprecation struct {
	Version string                      `json:"version"`
	Marker  deprecation.Marker          `json:"marker"`
	Signer  *lockfile.VerifiedSignature `json:"signer,omitempty"`
}

// RegistryIndex is the in-memory aggregate a registry update builds for one
// host: the host it was built from and the entity records, one per repository
// whose latest version conforms to strict SemVer. It is the shape the writer
// iterates to emit one file per repository under the host's index directory;
// the on-disk layout is per-repository files, not this struct serialized whole.
// Records are held in URI-then-version order.
type RegistryIndex struct {
	Host    string        `json:"host"`
	Records []IndexRecord `json:"records"`
}

// newRegistryUpdateCmd builds `registry update [<name|host>]`, which walks each
// configured registry's repositories, reads each repository's latest-version
// manifest annotations, and writes a deterministic local searchable index a
// later offline search reads.
func newRegistryUpdateCmd() *cobra.Command {
	var upgrade bool
	c := &cobra.Command{
		Use:   "update [<name|host>]",
		Short: "Build the local searchable index of configured registries",
		Long: `Walk each configured registry's repositories, read the latest published version
of each, and write a deterministic local searchable index to the artifact cache.

With no argument, every configured registry is indexed. With a single registry
argument (a host or a friendly alias), only that registry is indexed.

For each repository the highest strict-SemVer version is selected (pre-release
versions are skipped) and its catalog facets — kind, description, category,
labels, and, for plugins, socket type and capabilities — are recorded from the
published manifest. A repository with no conforming version is skipped; a
repository that cannot be read is skipped with a note, so one unreadable
repository does not abandon the whole index.

The per-repository records are byte-identical across two runs against the same
registry state; the freshness timestamp is written to a sibling file so it never
disturbs the records.

A refresh revalidates each repository conditionally: it re-resolves the selected
version's manifest digest and reuses the cached record untouched when the tag set
and digest are unchanged, so an unchanged registry costs a round of cheap digest
resolves rather than a full re-read of every repository's manifest and referrers.
Use --upgrade to ignore the cached validators and re-read every repository — this
picks up a change a conditional refresh skips, such as a deprecation marker added
to an existing version whose digest did not change.

Each published version's deprecation markers are read as the walk goes, and the ones
whose own signature verifies are recorded into the index. Every marker met is
reported, whether or not it was recorded, and each registry reports how many markers
its index now records — including when that number is zero. Where signature
verification is disabled for a registry, the walk reports that its markers were not
examined rather than a count, so a registry that published nothing is never confused
with one this run had no basis to judge.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return registryUpdateRun(registryUpdateConfig{
				ws:      rw,
				Args:    args,
				Upgrade: upgrade,
				Out:     cmd.OutOrStdout(),
			})
		},
	}
	c.Flags().BoolVar(&upgrade, "upgrade", false, "Re-read every repository from the registry, ignoring the cached revalidation validators (picks up changes a conditional refresh would skip)")
	return c
}

// registryUpdateConfig is the input contract for `aggrega registry update`,
// populated by the cobra command from its optional argument and flags.
type registryUpdateConfig struct {
	// Args carries the optional single registry token (host or alias). Empty
	// selects every configured registry.
	Args []string
	// Upgrade forces a full re-read of every repository, ignoring the cached
	// revalidation validators. It is the escape from the conditional refresh's one
	// bound: a deprecation added to an existing version whose tag set and manifest
	// digest are unchanged is invisible to digest-compare, and a full re-read picks
	// it up.
	Upgrade bool

	ws  *workspaceconfig.ResolvedWorkspace
	Out io.Writer
	// now supplies the freshness timestamp written to each host's sibling file.
	// Nil selects the live clock; tests inject a frozen time for determinism.
	now func() time.Time
}

// registryUpdateRun resolves the target host set and builds a local index for
// each. With no argument it indexes every configured registry; with one it
// resolves the token to a single host. Each host's records file plus its
// freshness sibling are written under the artifact cache's index subtree.
//
// Host-level failure handling depends on which path selected the hosts. When the
// operator targets a single registry by argument, a failure there is returned
// immediately — the operator asked for that one registry and gets a loud error.
// When no argument selects every configured registry, one unreachable registry
// must not abandon the reachable ones: a host whose source resolution,
// enumeration, or index write fails is noted and the walk continues to the next
// host, mirroring the per-repository skip-and-continue posture. After the walk,
// if any host failed the run returns a non-nil error summarizing the count so
// the exit code stays non-zero, but only after every reachable registry has been
// indexed and written.
func registryUpdateRun(cfg registryUpdateConfig) error {
	rw, err := workspaceOrResolve(cfg.ws)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}
	wsCfg := rw.Config()

	singleExplicit := len(cfg.Args) == 1
	var hosts []string
	if singleExplicit {
		host, rerr := wsCfg.ResolveRegistry(cfg.Args[0])
		if rerr != nil {
			return fmt.Errorf("aggrega registry update: %w", rerr)
		}
		hosts = []string{host}
	} else {
		for _, r := range wsCfg.Registries {
			hosts = append(hosts, r.Host)
		}
	}
	if len(hosts) == 0 {
		fmt.Fprintln(out, "No registries configured.")
		return nil
	}

	now := cfg.now
	if now == nil {
		now = time.Now
	}

	ctx := context.Background()
	root := rw.Root()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	var failed []string
	for _, host := range hosts {
		count, herr := updateHostIndex(ctx, wsCfg, root, cacheDir, host, now, cfg.Upgrade, out)
		if herr != nil {
			if singleExplicit {
				return fmt.Errorf("aggrega registry update: %s: %w", host, herr)
			}
			fmt.Fprintf(out, "registry %s: %v\n", host, herr)
			failed = append(failed, host)
			continue
		}
		fmt.Fprintf(out, "Indexed %d entities from %s\n", count, host)
	}
	if len(failed) > 0 {
		return fmt.Errorf("aggrega registry update: %d of %d registries failed to update", len(failed), len(hosts))
	}
	return nil
}

// updateHostIndex resolves one host's index source, builds its index, and writes
// the records file plus its freshness sibling, returning the record count on
// success. The returned error is unwrapped by host so the caller frames it for
// the single-registry path (returned immediately) or the all-registries path
// (noted per host, walk continues).
func updateHostIndex(ctx context.Context, cfg workspaceconfig.Config, root, cacheDir, host string, now func() time.Time, upgrade bool, out io.Writer) (int, error) {
	src, err := registryIndexTarget(cfg, host)
	if err != nil {
		return 0, err
	}
	var prior map[string]IndexRecord
	if !upgrade {
		prior = loadPriorRecords(root, cacheDir, host)
	}
	// The host's own trust policy, resolved once for the whole walk: it decides
	// which signers may withdraw a version of anything this host serves, so every
	// repository under the host is verified against the same set.
	policy, _ := cfg.RegistryTrustPolicy(host)
	idx, err := buildHostIndex(ctx, cfg, policy, host, src, prior, out)
	if err != nil {
		return 0, err
	}
	if err := writeHostIndex(root, cacheDir, idx, now().UTC()); err != nil {
		return 0, err
	}
	reportDeprecationScan(out, cfg, policy, host, idx)
	return len(idx.Records), nil
}

// reportDeprecationScan states the outcome of the host's deprecation-marker scan,
// which the per-marker notices alone cannot: they are written only when there is a
// marker to write about, so on their own an empty walk and a walk that never looked
// produce the same silence. The two are different facts — one says the registry
// published no honored marker, the other says this run had no trust basis to judge
// one — and an operator who attached a marker and sees neither notice needs to know
// which of them happened.
func reportDeprecationScan(out io.Writer, cfg workspaceconfig.Config, policy workspaceconfig.RegistryConfig, host string, idx RegistryIndex) {
	if !deprecationScanEnabled(cfg, policy) {
		fmt.Fprintf(out, "Deprecation markers not examined for %s: signature verification is disabled for this registry\n", host)
		return
	}
	fmt.Fprintf(out, "Recorded %d deprecation markers from %s\n", countRecordedDeprecations(idx), host)
}

// loadPriorRecords loads the previous per-host index records keyed by URI, so a
// conditional refresh can reuse an unchanged repository's record. A not-yet-built
// or unreadable prior index yields a nil map, degrading the refresh to a full
// build rather than failing — the first update after a cache wipe re-reads
// everything.
func loadPriorRecords(root, cacheDir, host string) map[string]IndexRecord {
	loaded, err := loadHostIndex(root, cacheDir, host)
	if err != nil {
		return nil
	}
	prior := make(map[string]IndexRecord, len(loaded.Records))
	for _, rec := range loaded.Records {
		prior[rec.URI] = rec
	}
	return prior
}

// buildHostIndex enumerates the host's repositories through the one search
// enumeration primitive (progress-aware and partial-on-error) and appends one
// record per repository whose latest version conforms to strict SemVer. Each
// repository is revalidated conditionally against prior (keyed by URI): a
// repository whose tag set and headline manifest digest match its prior record
// reuses that record, skipping the manifest read and the per-version referrer
// scan. A nil prior forces a full read of every repository. Records are sorted
// by URI then version so the emitted files are deterministic. A repository that
// dropped out of the registry is simply not enumerated, so it contributes no
// record and its file is not rewritten (the writer resets the host directory).
func buildHostIndex(
	ctx context.Context,
	wsCfg workspaceconfig.Config,
	policy workspaceconfig.RegistryConfig,
	host string,
	src registryIndexSource,
	prior map[string]IndexRecord,
	out io.Writer,
) (RegistryIndex, error) {
	repos, err := searchRegistry(ctx, src, "", catalogSearchScope)
	if err != nil {
		return RegistryIndex{}, err
	}
	idx := RegistryIndex{Host: host}
	for _, repo := range repos {
		rec, ok := indexRepo(ctx, wsCfg, policy, host, src, repo, prior, out)
		if !ok {
			continue
		}
		idx.Records = append(idx.Records, rec)
	}
	sortIndexRecords(idx.Records)
	return idx, nil
}

// lessIndexRecordURIVersion is the deterministic order for index records: by URI,
// then by version within a URI. It is the single ordering both the local index
// writer and any consumer that aggregates records across hosts sort by, so a
// reader cannot drift from the order the writer stamped.
func lessIndexRecordURIVersion(a, b IndexRecord) bool {
	if a.URI != b.URI {
		return a.URI < b.URI
	}
	return a.Version < b.Version
}

// sortIndexRecords sorts records in place into the deterministic
// URI-then-version order shared by the index writer and its consumers.
func sortIndexRecords(records []IndexRecord) {
	sort.Slice(records, func(i, j int) bool {
		return lessIndexRecordURIVersion(records[i], records[j])
	})
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
// so one unreadable repository does not abandon the walk, mirroring the search
// primitive's partial-on-error posture.
func indexRepo(
	ctx context.Context,
	wsCfg workspaceconfig.Config,
	policy workspaceconfig.RegistryConfig,
	host string,
	src registryIndexSource,
	repo string,
	prior map[string]IndexRecord,
	out io.Writer,
) (IndexRecord, bool) {
	target, err := src.Repository(repo)
	if err != nil {
		fmt.Fprintf(out, "  skipped %s: %v\n", repo, err)
		return IndexRecord{}, false
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
		fmt.Fprintf(out, "  skipped %s: %v\n", repo, err)
		return IndexRecord{}, false
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
			fmt.Fprintf(out, "  skipped %s: %v\n", repo, err)
			return IndexRecord{}, false
		}
	}
	// A truncated listing's single-request validator does not cover the whole tag set,
	// so fall back to the full enumeration and record no validator (the next refresh
	// re-reads rather than falsely matching a partial validator).
	if truncated {
		tags, err = oci.ListTags(ctx, target)
		if err != nil {
			fmt.Fprintf(out, "  skipped %s: %v\n", repo, err)
			return IndexRecord{}, false
		}
		listingDigest = ""
	}

	version := oci.SelectLatestVersion(tags, false /* includePreRelease */)
	if version == "" {
		return IndexRecord{}, false
	}
	versions := oci.SortedSemverTags(tags)

	digest, exists, err := oci.Resolve(ctx, target, version)
	if err != nil {
		fmt.Fprintf(out, "  skipped %s: %v\n", repo, err)
		return IndexRecord{}, false
	}
	if !exists {
		fmt.Fprintf(out, "  skipped %s: selected version %s no longer resolves\n", repo, version)
		return IndexRecord{}, false
	}
	if hasPrior && p.ManifestDigest != "" && p.ManifestDigest == digest && equalStrings(p.Versions, versions) {
		// The manifest and tag set are unchanged; reuse the prior record but carry the
		// refreshed listing validator forward (it may have been absent before).
		p.ListingDigest = listingDigest
		return p, true
	}

	annotations, manifestDigest, err := oci.FetchManifestAnnotations(ctx, target, version)
	if err != nil {
		fmt.Fprintf(out, "  skipped %s: %v\n", repo, err)
		return IndexRecord{}, false
	}
	rec, err := recordFromAnnotations(host, repo, version, annotations)
	if err != nil {
		fmt.Fprintf(out, "  skipped %s: %v\n", repo, err)
		return IndexRecord{}, false
	}
	rec.Versions = versions
	rec.ManifestDigest = manifestDigest
	rec.ListingDigest = listingDigest
	rec.Deprecations = collectVersionDeprecations(ctx, wsCfg, policy, target, repo, versions, out)
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
// A marker that IS honored is noted on the same footing, in the same shape, naming
// the severity value the marker's author recorded. Reporting only the failures would
// invert which outcome is audible: recording many markers would pass in silence while
// declining one would announce itself, and an operator watching the walk could not
// tell a registry that published no marker from a run that recorded a dozen.
func collectVersionDeprecations(
	ctx context.Context,
	wsCfg workspaceconfig.Config,
	policy workspaceconfig.RegistryConfig,
	target oras.ReadOnlyTarget,
	repo string,
	versions []string,
	out io.Writer,
) []VersionDeprecation {
	if !deprecationScanEnabled(wsCfg, policy) {
		return nil
	}
	var deprecations []VersionDeprecation
	for _, version := range versions {
		markers, err := oci.ListReferrers(ctx, target, version, deprecation.ArtifactType)
		if err != nil || len(markers) == 0 {
			continue
		}
		markerDesc, marker, err := latestDeprecationMarker(ctx, target, markers)
		if err != nil {
			fmt.Fprintf(out, "  deprecation: %s:%s: marker present but not readable (%v); not recorded\n", repo, version, err)
			continue
		}
		var origSigner *pullverify.Result
		if policy.PublicKey == "" {
			origSigner = verifiedArtifactSigner(ctx, wsCfg, target, version, policy)
		}
		signer, err := verifyMarkerSignature(ctx, wsCfg, target, markerDesc.Digest.String(), policy, origSigner)
		if err != nil {
			fmt.Fprintf(out, "  deprecation: %s:%s: marker present but not honored (%v); not recorded\n", repo, version, err)
			continue
		}
		fmt.Fprintf(out, "  deprecation: %s:%s: marker present and honored (%s); recorded\n", repo, version, marker.Severity)
		deprecations = append(deprecations, VersionDeprecation{Version: version, Marker: marker, Signer: signer})
	}
	return deprecations
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
func countRecordedDeprecations(idx RegistryIndex) int {
	n := 0
	for _, rec := range idx.Records {
		n += len(rec.Deprecations)
	}
	return n
}

// recordFromAnnotations assembles an IndexRecord from a repository's latest
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
func recordFromAnnotations(host, repo, version string, ann map[string]string) (IndexRecord, error) {
	rec := IndexRecord{
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
			return IndexRecord{}, fmt.Errorf("decoding labels of %s:%s: %w", repo, version, err)
		}
		rec.Labels = labels
	}
	return rec, nil
}

// writeHostIndex writes one file per repository under the host's index
// directory, plus the host's freshness sibling, under the artifact cache's index
// subtree. The host directory is rebuilt from scratch — removed then recreated —
// so the file set on disk is exactly the current record set: a repository that
// dropped out of the registry leaves no stale file behind. Each per-repo file is
// deterministic — indented JSON with a trailing newline, ordered slices, and
// author-order labels — so two builds against the same registry state produce
// byte-identical files and an identical file set. The timestamp lives only in
// the sibling `.updated` file, beside the host directory, so it never perturbs
// the records.
func writeHostIndex(workspaceRoot, cacheDir string, idx RegistryIndex, now time.Time) error {
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
