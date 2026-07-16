// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/spf13/cobra"
	"oras.land/oras-go/v2"

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
	reg  registryRepoLister
	host string
}

func (s remoteIndexSource) Repositories(ctx context.Context, last string, fn func(repos []string) error) error {
	return s.reg.Repositories(ctx, last, fn)
}

func (s remoteIndexSource) Repository(repo string) (oras.ReadOnlyTarget, error) {
	return oci.NewRemoteRepository(s.host + "/" + repo)
}

// registryIndexTarget resolves the index source for a registry host. Production
// returns a remote source; a test substitutes an in-memory source so the index
// build runs without a network. It is a package-level seam because the live
// path needs registry credentials the sandbox does not carry — the index-build
// logic is proven against a fake source, and the live registry resolution is
// operator-gated. It mirrors registrySearchTarget.
var registryIndexTarget = func(host string) (registryIndexSource, error) {
	reg, err := oci.NewRemoteRegistry(host)
	if err != nil {
		return nil, err
	}
	return remoteIndexSource{reg: reg, host: host}, nil
}

// IndexRecord is one entity's row in a local registry index: its OCI URI, the
// latest published version, and the searchable catalog facets read back from
// that version's manifest annotations. Labels reconstructs the catalog category
// label from the category facet so a search can match records with the same
// label selector the catalog uses; it is absent when the entity declares no
// category. PluginType and Capabilities carry a plugin's socket facets and are
// absent on non-plugin entities.
type IndexRecord struct {
	URI          string                    `json:"uri"`
	Name         string                    `json:"name"`
	Version      string                    `json:"version"`
	Kind         string                    `json:"kind,omitempty"`
	Description  string                    `json:"description,omitempty"`
	Category     string                    `json:"category,omitempty"`
	Labels       *types.OrderedMap[string] `json:"labels,omitempty"`
	PluginType   string                    `json:"pluginType,omitempty"`
	Capabilities []string                  `json:"capabilities,omitempty"`
}

// RegistryIndex is the deterministic per-host index a registry update writes:
// the host it was built from and the entity records, one per repository whose
// latest version conforms to strict SemVer. Records are sorted by URI then
// version, and the struct carries no timestamp, so the marshalled file is
// byte-identical across two builds against the same registry state. The
// freshness timestamp lives in a sibling file rather than in this record set.
type RegistryIndex struct {
	Host    string        `json:"host"`
	Records []IndexRecord `json:"records"`
}

// newRegistryUpdateCmd builds `registry update [<name|host>]`, which walks each
// configured registry's repositories, reads each repository's latest-version
// manifest annotations, and writes a deterministic local searchable index a
// later offline search reads.
func newRegistryUpdateCmd() *cobra.Command {
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

The per-host records file is byte-identical across two runs against the same
registry state; the freshness timestamp is written to a sibling file so it never
disturbs the records.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return registryUpdateRun(registryUpdateConfig{
				ws:   rw,
				Args: args,
				Out:  cmd.OutOrStdout(),
			})
		},
	}
	return c
}

// registryUpdateConfig is the input contract for `aggrega registry update`,
// populated by the cobra command from its optional argument.
type registryUpdateConfig struct {
	// Args carries the optional single registry token (host or alias). Empty
	// selects every configured registry.
	Args []string

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
		count, herr := updateHostIndex(ctx, root, cacheDir, host, now, out)
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
func updateHostIndex(ctx context.Context, root, cacheDir, host string, now func() time.Time, out io.Writer) (int, error) {
	src, err := registryIndexTarget(host)
	if err != nil {
		return 0, err
	}
	idx, err := buildHostIndex(ctx, host, src, out)
	if err != nil {
		return 0, err
	}
	if err := writeHostIndex(root, cacheDir, idx, now().UTC()); err != nil {
		return 0, err
	}
	return len(idx.Records), nil
}

// buildHostIndex enumerates the host's repositories through the one search
// enumeration primitive (progress-aware and partial-on-error) and appends one
// record per repository whose latest version conforms to strict SemVer. Records
// are sorted by URI then version so the marshalled file is deterministic.
func buildHostIndex(ctx context.Context, host string, src registryIndexSource, out io.Writer) (RegistryIndex, error) {
	repos, err := searchRegistry(ctx, src, "", catalogSearchScope)
	if err != nil {
		return RegistryIndex{}, err
	}
	idx := RegistryIndex{Host: host}
	for _, repo := range repos {
		rec, ok := indexRepo(ctx, host, src, repo, out)
		if !ok {
			continue
		}
		idx.Records = append(idx.Records, rec)
	}
	sort.Slice(idx.Records, func(i, j int) bool {
		if idx.Records[i].URI != idx.Records[j].URI {
			return idx.Records[i].URI < idx.Records[j].URI
		}
		return idx.Records[i].Version < idx.Records[j].Version
	})
	return idx, nil
}

// indexRepo builds the record for one repository's latest published version. A
// repository with no strict-SemVer version is skipped silently (a deterministic
// non-error). A repository that cannot be read — no per-repo target, a tag-list
// failure, or a manifest read failure — is skipped with a note so one
// unreadable repository does not abandon the walk, mirroring the search
// primitive's partial-on-error posture.
func indexRepo(ctx context.Context, host string, src registryIndexSource, repo string, out io.Writer) (IndexRecord, bool) {
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
	annotations, err := oci.FetchManifestAnnotations(ctx, target, version)
	if err != nil {
		fmt.Fprintf(out, "  skipped %s: %v\n", repo, err)
		return IndexRecord{}, false
	}
	return recordFromAnnotations(host, repo, version, annotations), true
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

// writeHostIndex writes the host's records file and its freshness sibling under
// the artifact cache's index subtree. The records file is deterministic —
// indented JSON with a trailing newline, ordered slices, and author-order
// labels — so two builds against the same registry state produce byte-identical
// bytes. The timestamp lives only in the sibling `.updated` file so it never
// perturbs the records.
func writeHostIndex(workspaceRoot, cacheDir string, idx RegistryIndex, now time.Time) error {
	dir := oci.IndexRoot(workspaceRoot, cacheDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("index mkdir %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("index marshal: %w", err)
	}
	data = append(data, '\n')

	base := oci.IndexBasePath(workspaceRoot, cacheDir, idx.Host)
	if err := os.WriteFile(base+".json", data, 0o644); err != nil {
		return fmt.Errorf("index write %s: %w", base+".json", err)
	}
	stamp := now.Format(time.RFC3339) + "\n"
	if err := os.WriteFile(base+".updated", []byte(stamp), 0o644); err != nil {
		return fmt.Errorf("index timestamp write %s: %w", base+".updated", err)
	}
	return nil
}
