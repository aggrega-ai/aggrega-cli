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
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
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
// annotations. Labels reconstructs the catalog category label from the category
// facet so a search can match records with the same label selector the catalog
// uses; it is absent when the entity declares no category. PluginType and
// Capabilities carry a plugin's socket facets and are absent on non-plugin
// entities.
//
// Versions is every published tag that conforms to strict SemVer, in ascending
// order (pre-release tags included). Version stays the selected headline
// (highest released). ManifestDigest is the resolved manifest digest of the
// headline version, recorded as a revalidation validator: a later refresh
// re-resolves the version and reuses the stored record when the digest is
// unchanged. Deprecations records, per deprecated version, the authoritative
// deprecation marker discovered from that version's referrers, so the advisory
// display reads a marker from the local index rather than probing the registry
// live. All three are omitted when empty.
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
	Deprecations   []VersionDeprecation      `json:"deprecations,omitempty"`
}

// VersionDeprecation pairs a strict-SemVer version with the authoritative
// deprecation marker discovered on that version's referrers. The whole marker is
// stored (all its fields), so the advisory display reconstructs the same view
// off the local index that a live referrer probe produced. The marshalled marker
// carries a fixed field order, so a record is byte-identical for identical
// registry state.
type VersionDeprecation struct {
	Version string             `json:"version"`
	Marker  deprecation.Marker `json:"marker"`
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
versions are skipped) and its catalog facets — kind, description, category, and,
for plugins, socket type and capabilities — are recorded from the published
manifest. A repository with no conforming version is skipped; a repository that
cannot be read is skipped with a note, so one unreadable repository does not
abandon the whole index.

The per-repository records are byte-identical across two runs against the same
registry state; the freshness timestamp is written to a sibling file so it never
disturbs the records.

A refresh revalidates each repository conditionally: it re-resolves the selected
version's manifest digest and reuses the cached record untouched when the tag set
and digest are unchanged, so an unchanged registry costs a round of cheap digest
resolves rather than a full re-read of every repository's manifest and referrers.
Use --upgrade to ignore the cached validators and re-read every repository — this
picks up a change a conditional refresh skips, such as a deprecation marker added
to an existing version whose digest did not change.`,
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
	idx, err := buildHostIndex(ctx, host, src, prior, out)
	if err != nil {
		return 0, err
	}
	if err := writeHostIndex(root, cacheDir, idx, now().UTC()); err != nil {
		return 0, err
	}
	return len(idx.Records), nil
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
func buildHostIndex(ctx context.Context, host string, src registryIndexSource, prior map[string]IndexRecord, out io.Writer) (RegistryIndex, error) {
	repos, err := searchRegistry(ctx, src, "", catalogSearchScope)
	if err != nil {
		return RegistryIndex{}, err
	}
	idx := RegistryIndex{Host: host}
	for _, repo := range repos {
		rec, ok := indexRepo(ctx, host, src, repo, prior, out)
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
// lists the tags and selects the headline version, then revalidates against the
// prior record for the repository's URI: when the tag set and the re-resolved
// headline manifest digest both match the prior record, that record is reused
// untouched — skipping the manifest read and the per-version referrer scan, so an
// unchanged repository costs one tag list plus one cheap digest resolve. Only a
// changed or previously-unseen repository re-reads the manifest annotations and
// re-scans referrers. A nil prior (a full-upgrade refresh, or the first build)
// reuses nothing.
//
// A repository with no strict-SemVer version is skipped silently (a deterministic
// non-error). A repository that cannot be read — no per-repo target, a tag-list
// failure, a resolve failure, or a manifest read failure — is skipped with a note
// so one unreadable repository does not abandon the walk, mirroring the search
// primitive's partial-on-error posture.
func indexRepo(ctx context.Context, host string, src registryIndexSource, repo string, prior map[string]IndexRecord, out io.Writer) (IndexRecord, bool) {
	target, err := src.Repository(repo)
	if err != nil {
		fmt.Fprintf(out, "  skipped %s: %v\n", repo, err)
		return IndexRecord{}, false
	}
	tags, err := oci.ListTags(ctx, target)
	if err != nil {
		fmt.Fprintf(out, "  skipped %s: %v\n", repo, err)
		return IndexRecord{}, false
	}
	version := oci.SelectLatestVersion(tags, false /* includePreRelease */)
	if version == "" {
		return IndexRecord{}, false
	}
	versions := oci.SortedSemverTags(tags)

	// Conditional revalidation: re-resolve the headline manifest digest (a cheap
	// metadata read, no content blob) and reuse the prior record when the tag set
	// and digest are unchanged.
	digest, exists, err := oci.Resolve(ctx, target, version)
	if err != nil {
		fmt.Fprintf(out, "  skipped %s: %v\n", repo, err)
		return IndexRecord{}, false
	}
	if !exists {
		fmt.Fprintf(out, "  skipped %s: selected version %s no longer resolves\n", repo, version)
		return IndexRecord{}, false
	}
	uri := "oci://" + host + "/" + repo
	if p, ok := prior[uri]; ok && p.ManifestDigest != "" && p.ManifestDigest == digest && equalStrings(p.Versions, versions) {
		return p, true
	}

	annotations, manifestDigest, err := oci.FetchManifestAnnotations(ctx, target, version)
	if err != nil {
		fmt.Fprintf(out, "  skipped %s: %v\n", repo, err)
		return IndexRecord{}, false
	}
	rec := recordFromAnnotations(host, repo, version, annotations)
	rec.Versions = versions
	rec.ManifestDigest = manifestDigest
	rec.Deprecations = collectVersionDeprecations(ctx, target, versions)
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
// referrers and records the authoritative marker for every version that carries
// one, in the ascending version order it is given. Each version's marker is
// selected by the same newest-issuedOn rule the live probe uses (with the
// manifest digest as the deterministic tiebreak), so the index-served advisory
// reproduces the live view. Every step is best-effort: a referrer-listing or
// decode failure skips that version rather than failing the build, mirroring the
// walk's partial-on-error posture. A version with no marker contributes nothing,
// so the slice is empty when no version is deprecated.
func collectVersionDeprecations(ctx context.Context, target oras.ReadOnlyTarget, versions []string) []VersionDeprecation {
	var deprecations []VersionDeprecation
	for _, version := range versions {
		markers, err := oci.ListReferrers(ctx, target, version, deprecation.ArtifactType)
		if err != nil || len(markers) == 0 {
			continue
		}
		_, marker, err := latestDeprecationMarker(ctx, target, markers)
		if err != nil {
			continue
		}
		deprecations = append(deprecations, VersionDeprecation{Version: version, Marker: marker})
	}
	return deprecations
}

// recordFromAnnotations assembles an IndexRecord from a repository's latest
// version and its manifest annotations. Every facet is read through the
// searchable-annotation constants, so no annotation key is spelled here as a
// bare string. Capabilities were stamped comma-joined in author order and are
// split back into a list. The catalog category label is reconstructed from the
// category facet so a search can match with the same label selector the catalog
// uses; the label is omitted when the entity declares no category.
func recordFromAnnotations(host, repo, version string, ann map[string]string) IndexRecord {
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
	if rec.Category != "" {
		labels := types.NewOrderedMap[string]()
		labels.Set(componentCategoryLabel, rec.Category)
		rec.Labels = labels
	}
	return rec
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
