// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginfetchverify"
	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress/livetrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
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

// CatalogScope admits every repository — a catalog enumeration spans the full
// entity surface, not one kind.
func CatalogScope(string) bool { return true }

// errRepositoryListingRepeated is the refusal a registry whose repository
// listing repeats itself produces: a page named a repository the listing had
// already delivered, or two pages in a row named no repository and still linked
// to a next one. The walk of that registry stops there, so a listing that never
// reaches its end cannot hold a caller forever. Match it with errors.Is; the
// error returned wraps it with what the listing repeated.
var errRepositoryListingRepeated = errors.New("the registry's repository listing repeated itself")

// EnumerateRepositories is the single registry-enumeration primitive. It walks
// the registry's repositories, keeps those the scope admits whose path contains
// the case-insensitive query substring (an empty query matches every in-scope
// repository), and returns the matches in ascending order. Callers differ only
// in the scope they pass; the walk, the filtering and the ordering live here
// once so they never drift into several enumerations.
//
// 🔴 The walk ends even when the listing does not. The registry client follows
// each page's link to the next for as long as the registry supplies one, so a
// listing whose link leads back to where it has been would be followed without
// end. The listing continues after the last repository of the page before, so
// no listing that advances names a repository twice; the walk therefore refuses
// the registry, with errRepositoryListingRepeated, at the first repository the
// listing delivers a second time — on the same page or a later one — and at a
// page naming no repository that follows another such page. One page naming
// nothing is not refused, because it may be the last. The check applies to every
// repository the listing names, before the scope and the query narrow them, so
// every caller stops on the same listing. A refused registry yields no
// repositories at all, so none is counted, read or reported twice.
//
// The walk is progress-aware. The registry delivers each catalog page to the
// callback before it parses that page's pagination header, so a registry that
// serves a malformed pagination header fails only after the first page's
// repositories were already collected. In that case the matches gathered so far
// are returned rather than discarded — a partial result beats no result. An
// error raised before any page was delivered (a connection or first-request
// failure) still propagates, since there is nothing to fall back to. A caller
// that stopped the walk gets the stop rather than a partial result, because a
// partial result from a withdrawn walk reads as a complete one.
func EnumerateRepositories(ctx context.Context, reg RepoLister, query string, scope RepoScope) ([]string, error) {
	q := strings.ToLower(query)
	var matches []string
	pageDelivered := false
	delivered := make(map[string]struct{})
	previousPageEmpty := false
	err := reg.Repositories(ctx, "", func(repos []string) error {
		pageDelivered = true
		if len(repos) == 0 {
			if previousPageEmpty {
				return fmt.Errorf("%w: two pages in a row named no repository and linked to a next one, so the walk stopped rather than follow the listing round again",
					errRepositoryListingRepeated)
			}
			previousPageEmpty = true
			return nil
		}
		previousPageEmpty = false
		for _, repo := range repos {
			if _, again := delivered[repo]; again {
				return fmt.Errorf("%w: it named %s a second time, so the walk stopped rather than follow the listing round again",
					errRepositoryListingRepeated, repo)
			}
			delivered[repo] = struct{}{}
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
	if err != nil {
		if errors.Is(err, errRepositoryListingRepeated) {
			return nil, err
		}
		if serr := stopped(ctx); serr != nil {
			return nil, serr
		}
		if !pageDelivered {
			return nil, fmt.Errorf("search registry: %w", err)
		}
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

// ReferrerRevalidator is the registry capability a refresh revalidates a
// version's referrers with: one conditional request for the unfiltered referrer
// listing of a manifest digest, answering whether it is unchanged since the
// validator ifNoneMatch, and otherwise the listing's current validator (empty
// where the registry serves none).
//
// It is a capability of its own rather than part of IndexSource because a source
// without it is still a complete index source: every version it serves is read in
// full on every refresh, since nothing can say its referrers did not change. The
// production source has it.
type ReferrerRevalidator interface {
	ConditionalReferrers(ctx context.Context, repo, digest, ifNoneMatch string) (etag string, notModified bool, err error)
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

func (s remoteIndexSource) ConditionalReferrers(ctx context.Context, repo, digest, ifNoneMatch string) (etag string, notModified bool, err error) {
	return oci.ConditionalListReferrers(ctx, s.host, repo, digest, ifNoneMatch, s.httpClient)
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
//
// Every read the source makes goes through readRetryingTransport, so a read the
// registry refused or dropped in passing is asked again before the walk decides
// anything from it.
var IndexTarget = func(cfg workspaceconfig.Config, host string) (IndexSource, error) {
	httpClient, err := retryingReadClient(cfg)
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
//
// Revalidations holds, keyed by record URI, what the writer stores beside each
// record for the next refresh to compare against (registryindex.Revalidation). A
// record with no entry is written with none, and the next refresh reads it in
// full. It is looked up per record and never iterated.
type HostIndex struct {
	Host          string                                `json:"host"`
	Records       []registryindex.Record                `json:"records"`
	Revalidations map[string]registryindex.Revalidation `json:"-"`
}

// SkippedRepo is one repository the walk could not read, with what stopped it.
//
// A skip is reported rather than swallowed because one unreadable repository
// does not abandon the walk: the index that results is incomplete, and a caller
// acting on it needs to know which repositories are missing from it rather than
// reading a short answer as a complete one.
//
// Where a read of the repository failed, Reason opens with the kind of its last
// failure — the status the registry answered, a timeout, a reset, an answer cut
// short — and then gives the failure itself, so the account of the walk says what
// went wrong without a reader having to recognise it in an error's words.
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
//
// Unreachable says the failure Err carries is the registry never being reached,
// as oci.Unreachable reads it from the failure itself. It is set beside Err and
// nowhere else, so a caller tells a registry that could not be reached from one
// that answered and failed — a listing that repeated itself, a refusal — by
// reading a field rather than the words of Err, whose wording follows the
// machine's resolver as much as the condition.
//
// ReadsRetried is how many of the walk's reads the registry refused or dropped in
// passing and were asked again (readRetryingTransport), each read counted once
// however many times it was asked; zero where none was. It says how near the walk
// came to repositories it could not read: a read asked again that then succeeded
// leaves nothing in Skipped, and only this count shows it happened. The command
// line's closing summary states it on the registry's row, and the encoded form of
// this result carries it as readsRetried, zero included.
type HostUpdate struct {
	Host                 string            `json:"host"`
	Indexed              int               `json:"indexed"`
	DeprecationsExamined bool              `json:"deprecationsExamined"`
	DeprecationsRecorded int               `json:"deprecationsRecorded"`
	Skipped              []SkippedRepo     `json:"skipped"`
	Deprecations         []DeprecationNote `json:"deprecations"`
	ReadsRetried         int               `json:"readsRetried"`
	Err                  string            `json:"error,omitempty"`
	Unreachable          bool              `json:"unreachable,omitempty"`
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
// host-level failure is handled. Upgrade reads every repository in full,
// comparing nothing against the index already on disk. A refresh without it
// already re-reads everything a full read would find changed (see indexRepo), so
// Upgrade is for rebuilding an index from the registry alone — after a doubt about
// the index on disk, say — rather than for picking up a kind of change a refresh
// misses.
type UpdateOptions struct {
	Registry string
	Upgrade  bool
	// Now supplies the freshness timestamp written to each host's sibling file.
	// Nil selects the live clock; a test injects a frozen time for determinism.
	Now func() time.Time
	// workers bounds how many repositories one registry's walk reads at once.
	// Zero selects indexWalkWorkers; a test sets one to compare the parallel walk
	// with a sequential one.
	workers int
}

// indexWalkWorkers is how many repositories one registry's walk reads at once.
// The walk's cost is round-trip latency rather than data volume — a repository
// costs a handful of small sequential requests — so reading several repositories
// at once shortens it roughly in proportion, and the bound keeps the load one
// refresh puts on a registry modest. Every record, skip, notice and note is still
// emitted in repository order, so the bound changes how long a walk takes and
// nothing it writes or prints.
const indexWalkWorkers = 16

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
// workspace on the machine resolves through. Where the resolution found no
// workspace and none was named, there is no workspace to write it in, and a walk
// with a host to walk is refused before any registry is reached
// (workspaceconfig.ResolvedWorkspace.CacheWriteRefusal).
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
// 🔴 Only a finished walk writes. A registry whose walk failed, was refused
// because its repository listing repeated itself, or was stopped part-way keeps
// the index it had before the run, byte for byte: writing replaces a host's whole
// index, so writing what an unfinished walk gathered would put a short index in
// place of a complete one and nothing would say so. Such a host is reported with
// its error and counts as not indexed. A listing that errors after delivering
// pages is not a failed walk: EnumerateRepositories returns what those pages
// named, and that partial result is indexed and written.
//
// notices carries the walk's running commentary — a skipped repository, a
// deprecation marker met — for a surface with a person watching it. It is a
// writer rather than returned data because the prose is written for a reader,
// and the same facts travel structurally on the returned result for a surface
// that has no reader. A caller with nobody watching discards it.
//
// The result is returned alongside any error rather than instead of it, so a
// partial walk is never reported as no walk at all. That holds on the
// single-registry path too: the host whose failure is returned is on the result
// as its last row, Unreachable included, so a caller refusing the request can
// still say whether that registry was reached. A stop is reported the same
// way and for the same reason: the hosts already indexed have been written, so a
// withdrawn walk reports what it indexed rather than claiming it indexed nothing,
// and the host it was walking when it stopped is reported as not indexed.
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
	// The index is written under the workspace's artifact cache, and a workspace
	// that was found nowhere and named by nobody gets no cache: a walk that would
	// write one is refused here, before any registry is reached, with the folder
	// and the way out in the refusal. A walk with no host to walk writes nothing and
	// is answered above.
	if err := rw.CacheWriteRefusal(); err != nil {
		return res, fmt.Errorf("aggrega registry update: %w", err)
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
		hu, herr := updateHostIndex(ctx, cfg, root, cacheDir, host, now, opts, notices)
		if herr != nil {
			hu.Err = herr.Error()
			hu.Unreachable = oci.Unreachable(herr)
			res.Hosts = append(res.Hosts, hu)
			// A stop that landed inside this host's walk ends the run here, as one
			// landing between hosts does: the registry did not fail, the caller left.
			if serr := stopped(ctx); serr != nil {
				return res, serr
			}
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
	opts UpdateOptions,
	notices io.Writer,
) (hu HostUpdate, err error) {
	hu = HostUpdate{Host: host}
	// Every read of this registry's walk is counted where it is asked again, and the
	// count is the walk's to report however it ends.
	ctx, retried := countingReadRetries(ctx)
	defer func() { hu.ReadsRetried = int(retried.Load()) }()
	// Every signature this walk verifies against the live public trust root
	// verifies against one fetch of it, made by the first verification that needs
	// it: the walk's workers verify at once, and a fetch per verification would
	// have their TUF clients rewrite one cache directory together.
	ctx = livetrust.WithSharedRoot(ctx)
	src, err := IndexTarget(cfg, host)
	if err != nil {
		return hu, err
	}
	// The prior index is loaded on every walk: a refresh compares against it, and
	// every walk, a full re-read included, keeps a repository's earlier record when
	// the repository cannot be read this time. A full re-read compares nothing with
	// it, so a prior index the read hold refuses is, there, simply not kept from.
	prior, err := loadPriorIndex(root, cacheDir, host)
	if err != nil {
		if !opts.Upgrade {
			return hu, err
		}
		prior = priorIndex{}
	}
	// The host's own trust policy, resolved once for the whole walk: it decides
	// which signers may withdraw a version of anything this host serves, so every
	// repository under the host is verified against the same set.
	policy, _ := cfg.RegistryTrustPolicy(host)
	workers := opts.workers
	if workers <= 0 {
		workers = indexWalkWorkers
	}
	hw := &hostWalk{
		wsCfg:  cfg,
		policy: policy,
		trust:  registryindex.TrustPolicyFingerprint(cfg, policy),
		host:   host,
		src:    src,
		prior:  prior,
		reuse:  !opts.Upgrade,
	}
	idx, err := buildHostIndex(ctx, hw, workers, &hu, notices)
	if err != nil {
		return hu, err
	}
	// A stop during the repository reads leaves every read after it skipped, so
	// what was gathered is not this registry's index and is not written over the
	// one already on disk.
	if serr := stopped(ctx); serr != nil {
		return hu, serr
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

// priorIndex is the index a conditional refresh compares against: the previous
// records and the revalidation stored beside each, both keyed by URI. Both are
// looked up per repository and never iterated.
type priorIndex struct {
	records       map[string]registryindex.Record
	revalidations map[string]registryindex.Revalidation
}

// loadPriorIndex loads the previous per-host index keyed by URI, so a conditional
// refresh can keep what is unchanged. A not-yet-built or unreadable prior index
// yields an empty one, degrading the refresh to a full build rather than failing —
// the first update after a cache wipe re-reads everything.
//
// A prior index the read hold refuses — leading through a link out of the root the
// cache is held to — is not one of those: it is returned as the error, naming the
// link, because a record reused from it would carry whatever the link leads to into
// the index this refresh writes.
func loadPriorIndex(root, cacheDir, host string) (priorIndex, error) {
	loaded, err := registryindex.LoadHost(root, cacheDir, host)
	if err != nil {
		if errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
			return priorIndex{}, err
		}
		return priorIndex{}, nil
	}
	prior := priorIndex{
		records:       make(map[string]registryindex.Record, len(loaded.Records)),
		revalidations: loaded.Revalidations,
	}
	for _, rec := range loaded.Records {
		prior.records[rec.URI] = rec
	}
	return prior, nil
}

// hostWalk is what every repository of one registry's walk is read against: the
// workspace configuration, the host's resolved trust policy and its fingerprint,
// the host, its index source, the index already on disk, and whether the walk
// keeps from that index what is unchanged (a refresh) or reads everything (a full
// re-read). It is shared by the walk's workers and none of them changes it.
type hostWalk struct {
	wsCfg  workspaceconfig.Config
	policy workspaceconfig.RegistryConfig
	trust  string
	host   string
	src    IndexSource
	prior  priorIndex
	reuse  bool
}

// unreadable settles a repository the walk could not read: one of its reads
// failed, and still failed once asked again. Nothing this walk read of it is
// recorded or reported — a record built around a read that failed would state the
// failure as a fact about the repository: a version with no signature, no date or
// no deprecation marker, or no repository at all. The repository keeps the record
// the index already holds for it, with what was stored beside that record, where
// that record was judged under the trust policy in force; a record judged under
// another policy, or none, leaves the repository out of this index. Either way it
// is named as a repository that could not be read, with the read that failed and
// the kind of its last failure (unreadReason).
func (hw *hostWalk) unreadable(w *repoWalk, repo, uri string, err error) {
	w.notes = nil
	w.text.Reset()
	reason := unreadReason(err)
	p, hasPrior := hw.prior.records[uri]
	pr, hasRevalidation := hw.prior.revalidations[uri]
	if !hasPrior || !hasRevalidation || pr.TrustPolicy != hw.trust {
		w.skip(repo, reason)
		return
	}
	w.rec, w.revalidation, w.ok = p, pr, true
	w.skipped = &SkippedRepo{Repo: repo, Reason: reason}
	fmt.Fprintf(&w.text, "  could not read %s: %s; its earlier record is kept\n", repo, reason)
}

// unreadReason is what a repository that could not be read is reported with: the
// kind of the read's last failure, as oci.ReadFailureKind names it — the status the
// registry answered, a timeout, a reset, an answer cut short — followed by the
// failure itself, which says which read it was. A failure of no such kind — a
// trust root that could not be obtained to verify a signature against, say — is
// reported as the failure alone.
func unreadReason(err error) string {
	if kind := oci.ReadFailureKind(err); kind != "" {
		return kind + ": " + err.Error()
	}
	return err.Error()
}

// readFailed settles a repository one of whose own reads failed — its tag listing,
// or its latest version's manifest. Where the registry answered that it holds
// nothing there (registryindex.IsAbsent), that is an answer: the repository is no
// longer there to record, and it is skipped as one whose version no longer
// resolves is. Anything else is a read that did not complete (unreadable).
func (hw *hostWalk) readFailed(w *repoWalk, repo, uri string, err error) {
	if registryindex.IsAbsent(err) {
		w.skip(repo, err.Error())
		return
	}
	hw.unreadable(w, repo, uri, err)
}

// repoWalk is one repository's share of a host's walk: its record and the
// revalidation stored beside it, whether there is a record at all, and everything
// the walk reports about it — the skip, the deprecation notes, and the running
// commentary in the words a reader sees. A worker fills it; the walk reports it,
// in repository order, once every repository before it has been reported.
type repoWalk struct {
	rec          registryindex.Record
	revalidation registryindex.Revalidation
	ok           bool
	skipped      *SkippedRepo
	notes        []DeprecationNote
	text         bytes.Buffer
}

// skip records the repository as unreadable and writes the commentary for a
// reader, so the prose and the structured account of a skip are produced from one
// place and cannot come to disagree.
func (w *repoWalk) skip(repo, reason string) {
	w.skipped = &SkippedRepo{Repo: repo, Reason: reason}
	fmt.Fprintf(&w.text, "  skipped %s: %s\n", repo, reason)
}

// buildHostIndex enumerates the host's repositories through the one enumeration
// primitive (progress-aware and partial-on-error) and builds one record per
// repository whose latest version conforms to strict SemVer, each revalidated
// against the prior index as indexRepo describes. Records are sorted by URI then
// version so the emitted files are deterministic. A repository that dropped out
// of the registry is simply not enumerated, so it contributes no record and its
// file is not rewritten (the writer resets the host directory).
//
// Up to workers repositories are read at once, each by a worker taking the next
// repository in the enumeration's order. What a repository's read reports — its
// skip, its deprecation notes, its commentary and its record — is held until every
// repository before it has been reported, then reported in turn, so the result and
// the commentary come out in repository order whatever order the reads finish in.
// The commentary still streams: each repository's lines are written as soon as the
// repositories before it are done, not at the end of the walk. A walk of one
// worker and a walk of many therefore write the same files and the same
// commentary for the same registry state.
//
// A stop ends the walk at the next repository a worker would take: no worker
// starts another, the reads in flight end with the stop their requests carry, and
// the caller sees the stop before anything is written.
func buildHostIndex(ctx context.Context, hw *hostWalk, workers int, hu *HostUpdate, notices io.Writer) (HostIndex, error) {
	repos, err := EnumerateRepositories(ctx, hw.src, "", CatalogScope)
	if err != nil {
		return HostIndex{}, err
	}
	idx := HostIndex{Host: hw.host, Revalidations: map[string]registryindex.Revalidation{}}
	walks := make([]repoWalk, len(repos))
	done := make([]bool, len(repos))
	var (
		mu       sync.Mutex
		next     int // the next repository a worker takes
		reported int // how many repositories, from the first, have been reported
	)
	// report hands one finished repository to the walk's result and its reader. It
	// runs under mu, one repository at a time and in repository order.
	report := func(w *repoWalk) {
		if w.skipped != nil {
			hu.Skipped = append(hu.Skipped, *w.skipped)
		}
		hu.Deprecations = append(hu.Deprecations, w.notes...)
		_, _ = notices.Write(w.text.Bytes())
		w.text = bytes.Buffer{}
		if w.ok {
			idx.Records = append(idx.Records, w.rec)
			idx.Revalidations[w.rec.URI] = w.revalidation
		}
	}
	var wg sync.WaitGroup
	for range min(workers, len(repos)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				if next >= len(repos) || stopped(ctx) != nil {
					mu.Unlock()
					return
				}
				i := next
				next++
				mu.Unlock()

				indexRepo(ctx, hw, repos[i], &walks[i])

				mu.Lock()
				done[i] = true
				for reported < len(repos) && done[reported] {
					report(&walks[reported])
					reported++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	registryindex.SortRecords(idx.Records)
	return idx, nil
}

// indexRepo builds the record for one repository's latest published version,
// reading again only what changed since the prior index and keeping the rest.
//
// What it keeps rests on four checks, each a cheap conditional read, and a fact is
// kept only where every check bearing on it says nothing changed:
//
//   - The tag listing is revalidated against the validator stored with the
//     record. An unchanged listing keeps the recorded version list; a changed one
//     is read again, and a version no longer listed is dropped with it.
//   - Every version's tag is resolved to its manifest digest. A version whose
//     digest differs from the recorded one — an older version published again
//     under its tag as much as a new latest — is read again in full, and a latest
//     version whose digest changed has its manifest's annotations read again.
//   - Every version's referrer listing — the one listing holding its signatures,
//     its published-at statements and its deprecation markers — is revalidated
//     against the validator stored for that version, and where a marker was
//     judged, so is that marker's own referrer listing, which holds the marker's
//     signatures. Only where both are unchanged are the version's signer, date and
//     marker kept; otherwise the version is read again and every signature and
//     marker it carries is verified again, exactly as a full read verifies them.
//   - The trust policy the prior index was judged under is compared with this
//     host's, by fingerprint. A different fingerprint, or a prior record with
//     none, keeps nothing: every version is read again and judged under the
//     policy in force.
//
// A verdict is therefore reused only where neither the version's bytes, nor
// anything attached to it, nor the policy judging it changed — the same basis on
// which a recorded verdict is trusted when the index is read. A version whose
// marker is present and not honored stores no validator and is read again on
// every refresh, since a signature attached to the marker later changes the
// verdict without changing the version's referrers. A registry serving no
// validators has every version read again on every refresh: nothing can then say
// the version is unchanged. Without a prior index — the first build, or a full
// read forced by the caller — every repository is read in full.
//
// 🔴 A read that fails is never taken as an answer. Every read goes through the
// walk's retrying transport, which asks again where the registry refused or
// dropped it in passing; a read still failing after that leaves the repository
// unreadable (hostWalk.unreadable), never recorded with what the failure left out
// — a version with no signer, no date or no marker, or a repository dropped from
// the index. Only what the registry ANSWERED is recorded as absent: a listing that
// names no signature, no marker, no tag; and a marker, a marker's signature or a
// published-at statement the registry answers it does not hold
// (registryindex.IsAbsent), which a later read answers the same way.
//
// A repository with no strict-SemVer version is skipped silently (a deterministic
// non-error). A repository the registry answers is not what the index can record —
// one it no longer holds, a version that no longer resolves, or a manifest whose
// label map does not decode — is skipped with a note, and so is one that cannot be
// read and has no earlier record to keep, so one such repository does not abandon
// the walk, mirroring the enumeration primitive's partial-on-error posture.
func indexRepo(ctx context.Context, hw *hostWalk, repo string, w *repoWalk) {
	uri := "oci://" + hw.host + "/" + repo
	target, err := hw.src.Repository(repo)
	if err != nil {
		hw.unreadable(w, repo, uri, err)
		return
	}
	p, hasPrior := hw.prior.records[uri]
	pr, hasRevalidation := hw.prior.revalidations[uri]
	reusable := hw.reuse && hasPrior && hasRevalidation && pr.TrustPolicy == hw.trust && p.CoversVersions()

	priorListing := ""
	if reusable {
		priorListing = p.ListingDigest
	}
	tags, listingDigest, notModified, truncated, err := hw.src.ConditionalTags(ctx, repo, priorListing)
	if err != nil {
		hw.readFailed(w, repo, uri, err)
		return
	}
	var version string
	var versions []string
	if notModified {
		// The tag set is the one the prior record was built from, so its version list
		// and its selected version stand.
		version = p.Version
		versions = slices.Clone(p.Versions)
	} else {
		// A truncated listing's single-request validator does not cover the whole tag
		// set, so fall back to the full enumeration and record no validator (the next
		// refresh re-lists rather than falsely matching a partial validator).
		if truncated {
			tags, err = oci.ListTags(ctx, target)
			if err != nil {
				hw.readFailed(w, repo, uri, err)
				return
			}
			listingDigest = ""
		}
		version = oci.SelectLatestVersion(tags, false /* includePreRelease */)
		if version == "" {
			return
		}
		versions = oci.SortedSemverTags(tags)
	}

	digests, gone, err := resolveVersions(ctx, target, versions, version)
	if err != nil {
		hw.unreadable(w, repo, uri, err)
		return
	}
	if gone != "" {
		w.skip(repo, gone)
		return
	}
	latest := slices.Index(versions, version)

	var rec registryindex.Record
	if reusable && p.ManifestDigest == digests[latest] {
		// The latest version's manifest is the one the prior record's facets were read
		// from, so they stand.
		rec = p
	} else {
		annotations, manifestDigest, err := oci.FetchManifestAnnotations(ctx, target, version)
		if err != nil {
			hw.readFailed(w, repo, uri, err)
			return
		}
		if rec, err = recordFromAnnotations(hw.host, repo, version, annotations); err != nil {
			w.skip(repo, err.Error())
			return
		}
		digests[latest] = manifestDigest
	}
	rec.Version = version
	rec.Versions = versions
	rec.ManifestDigest = digests[latest]
	rec.ListingDigest = listingDigest
	rec.Published = make([]registryindex.PublishedVersion, 0, len(versions))
	rec.Deprecations = nil

	revalidation := registryindex.Revalidation{
		TrustPolicy: hw.trust,
		Versions:    make([]registryindex.VersionRevalidation, 0, len(versions)),
	}
	for i, v := range versions {
		var (
			pv   registryindex.PublishedVersion
			vr   registryindex.VersionRevalidation
			dep  *registryindex.VersionDeprecation
			kept bool
		)
		if reusable {
			pv, vr, dep, kept = keepVersion(ctx, hw, repo, p, pr, v, digests[i])
		}
		if !kept {
			var unread error
			if pv, vr, dep, unread = readVersion(ctx, hw, target, repo, v, digests[i], w); unread != nil {
				hw.unreadable(w, repo, uri, fmt.Errorf("version %s: %w", v, unread))
				return
			}
		}
		rec.Published = append(rec.Published, pv)
		revalidation.Versions = append(revalidation.Versions, vr)
		if dep != nil {
			rec.Deprecations = append(rec.Deprecations, *dep)
		}
	}
	w.rec, w.revalidation, w.ok = rec, revalidation, true
}

// resolveVersions resolves every version's tag to the manifest digest it names
// now, in the order given. A version that no longer resolves is the registry's
// answer, and gone says which: the whole repository is skipped rather than
// recorded with a gap, since a record listing a version it gives no digest for
// would send a reader addressing versions by digest to a version it cannot
// address. A resolve that fails is returned as the error: that is no answer.
func resolveVersions(ctx context.Context, target oras.ReadOnlyTarget, versions []string, latest string) (digests []string, gone string, err error) {
	digests = make([]string, len(versions))
	for i, v := range versions {
		d, exists, rerr := oci.Resolve(ctx, target, v)
		if rerr != nil {
			return nil, "", rerr
		}
		if !exists {
			if v == latest {
				return nil, fmt.Sprintf("selected version %s no longer resolves", v), nil
			}
			return nil, fmt.Sprintf("version %s no longer resolves", v), nil
		}
		digests[i] = d
	}
	return digests, "", nil
}

// keepVersion decides whether one version's recorded signer, date and marker
// still hold, and returns them when they do. They hold where the version's tag
// still names the recorded digest, a referrer validator was stored for it, the
// registry answers that the version's referrer listing is unchanged, and — where a
// marker was judged — that the marker's own referrer listing is unchanged too.
// Anything else, including a registry that cannot answer, is a reason to read the
// version again.
func keepVersion(
	ctx context.Context,
	hw *hostWalk,
	repo string,
	p registryindex.Record,
	pr registryindex.Revalidation,
	version, digest string,
) (registryindex.PublishedVersion, registryindex.VersionRevalidation, *registryindex.VersionDeprecation, bool) {
	pv, found := publishedEntry(p.Published, version)
	if !found || pv.ManifestDigest != digest {
		return registryindex.PublishedVersion{}, registryindex.VersionRevalidation{}, nil, false
	}
	vr, found := revalidationEntry(pr.Versions, version)
	if !found || vr.Referrers == "" {
		return registryindex.PublishedVersion{}, registryindex.VersionRevalidation{}, nil, false
	}
	if !referrersUnchanged(ctx, hw.src, repo, digest, vr.Referrers) {
		return registryindex.PublishedVersion{}, registryindex.VersionRevalidation{}, nil, false
	}
	if vr.MarkerDigest != "" && (vr.MarkerReferrers == "" || !referrersUnchanged(ctx, hw.src, repo, vr.MarkerDigest, vr.MarkerReferrers)) {
		return registryindex.PublishedVersion{}, registryindex.VersionRevalidation{}, nil, false
	}
	var dep *registryindex.VersionDeprecation
	for i := range p.Deprecations {
		if p.Deprecations[i].Version == version {
			d := p.Deprecations[i]
			dep = &d
			break
		}
	}
	return pv, vr, dep, true
}

// readVersion reads one version in full, as a refresh with nothing to compare
// against does: its signer verified under the host's policy, its publication
// date, and its deprecation marker verified against the signers the policy
// honors, with the marker's commentary written to the repository's account. The
// version's referrer validator is taken BEFORE those reads, and the marker's
// before its signature is verified, so anything attached while the version is
// being read changes a validator the next refresh compares against rather than
// slipping in under one taken afterwards. Validators are stored only where every
// validator was served and the marker, if any, was honored.
//
// unread is non-nil where a read the version's record rests on failed — the
// signature walk, the publication date, or the deprecation judgement could not be
// completed — and then nothing else it returns is to be recorded: it is what a
// failure left, not what the version carries. A failed validator read is not one
// of those: the version's record is complete, and with no validator stored it is
// simply read again on the next refresh.
func readVersion(
	ctx context.Context,
	hw *hostWalk,
	target oras.ReadOnlyTarget,
	repo, version, digest string,
	w *repoWalk,
) (pv registryindex.PublishedVersion, vr registryindex.VersionRevalidation, dep *registryindex.VersionDeprecation, unread error) {
	referrers, answered := currentReferrers(ctx, hw.src, repo, digest)
	pv, unread = readPublishedVersion(ctx, hw.wsCfg, hw.policy, target, version, digest)
	if unread != nil {
		return registryindex.PublishedVersion{}, registryindex.VersionRevalidation{}, nil, unread
	}
	vr = registryindex.VersionRevalidation{Version: version}
	if deprecationScanEnabled(hw.wsCfg, hw.policy) {
		var note *DeprecationNote
		var settled bool
		dep, note, settled, unread = collectVersionDeprecation(ctx, hw.wsCfg, hw.policy, target, repo, version, &w.text, func(markerDigest string) {
			vr.MarkerDigest = markerDigest
			var markerAnswered bool
			vr.MarkerReferrers, markerAnswered = currentReferrers(ctx, hw.src, repo, markerDigest)
			if !markerAnswered {
				answered = false
			}
		})
		if unread != nil {
			return registryindex.PublishedVersion{}, registryindex.VersionRevalidation{}, nil, unread
		}
		if note != nil {
			w.notes = append(w.notes, *note)
		}
		if !settled {
			answered = false
		}
	}
	if !answered {
		return pv, registryindex.VersionRevalidation{Version: version}, dep, nil
	}
	vr.Referrers = referrers
	return pv, vr, dep, nil
}

// referrersUnchanged asks the registry whether a digest's referrer listing is
// still the one validator names. A source that cannot revalidate referrers, and a
// registry that does not answer, both read as changed.
func referrersUnchanged(ctx context.Context, src IndexSource, repo, digest, validator string) bool {
	rv, ok := src.(ReferrerRevalidator)
	if !ok {
		return false
	}
	_, notModified, err := rv.ConditionalReferrers(ctx, repo, digest, validator)
	return err == nil && notModified
}

// currentReferrers returns the validator a digest's referrer listing carries now,
// and whether there is one to store: a source that cannot revalidate referrers, a
// registry that serves no validator, and a registry that does not answer all give
// none.
func currentReferrers(ctx context.Context, src IndexSource, repo, digest string) (string, bool) {
	rv, ok := src.(ReferrerRevalidator)
	if !ok {
		return "", false
	}
	etag, _, err := rv.ConditionalReferrers(ctx, repo, digest, "")
	if err != nil || etag == "" {
		return "", false
	}
	return etag, true
}

// publishedEntry finds a version's published entry in a record.
func publishedEntry(published []registryindex.PublishedVersion, version string) (registryindex.PublishedVersion, bool) {
	for _, pv := range published {
		if pv.Version == version {
			return pv, true
		}
	}
	return registryindex.PublishedVersion{}, false
}

// revalidationEntry finds a version's revalidation entry.
func revalidationEntry(versions []registryindex.VersionRevalidation, version string) (registryindex.VersionRevalidation, bool) {
	for _, vr := range versions {
		if vr.Version == version {
			return vr, true
		}
	}
	return registryindex.VersionRevalidation{}, false
}

// readPublishedVersion records one published version: the manifest digest its tag
// resolved to; where this host's policy gives a basis to judge one, the verified
// signer of that version's own signature; and its publication date with the
// date's source. The error is non-nil where a read could not be completed — the
// signature walk or the date read did not finish — and the entry returned beside
// it is then the best of what was read.
//
// A signature is judged through the one verification home, against the host's
// own trust policy: a version whose signatures do not verify is recorded with no
// signer, never with an unverified one. Where several verify, the one recorded is
// the earliest (registryindex.FindVerifiedSigner), and the date is read from that
// same signature, so a version signed again keeps both its signer and its date.
// The date is the verified signature's log time where it carries one, and
// otherwise what the version's published-at referrers state
// (registryindex.ReadPublicationDate); that second read needs no trust anchor, so
// it is made on every host. Only signature referrers and published-at referrers
// are read; no entity content is fetched.
func readPublishedVersion(
	ctx context.Context,
	wsCfg workspaceconfig.Config,
	policy workspaceconfig.RegistryConfig,
	target oras.ReadOnlyTarget,
	version, digest string,
) (registryindex.PublishedVersion, error) {
	pv := registryindex.PublishedVersion{Version: version, ManifestDigest: digest}
	var verified *pullverify.Result
	var unfinished error
	if signerVerificationEnabled(wsCfg, policy) {
		res, err := registryindex.FindVerifiedSigner(ctx, wsCfg, target, version, digest, policy)
		if err != nil {
			unfinished = err
		}
		if res != nil {
			pv.Signer = pluginfetchverify.SignatureFact(*res)
			verified = res
		}
	}
	var err error
	pv.PublishedAt, pv.PublishedAtSource, err = registryindex.ReadPublicationDate(ctx, target, version, verified)
	if err != nil {
		unfinished = err
	}
	return pv, unfinished
}

// signerVerificationEnabled reports whether the walk judges each published
// version's own signature. It needs verification switched on for the host and a
// trust anchor the host's policy pins — a public key, or keyless identities. With
// no anchor there is nothing a signature could verify against, so no signature is
// read at all rather than read and refused.
func signerVerificationEnabled(wsCfg workspaceconfig.Config, policy workspaceconfig.RegistryConfig) bool {
	return deprecationScanEnabled(wsCfg, policy) && TrustOf(policy).Anchor != ""
}

// CollectVersionDeprecations scans each conforming version's deprecation
// referrers and records the authoritative marker for every version that carries a
// marker whose OWN signature verifies, in the ascending version order it is given.
// Each version's marker is selected by the same newest-issuedOn rule the closure
// discovery pass uses (with the manifest digest as the deterministic tiebreak), and
// verified through the same single verification home, so the two passes cannot
// disagree about which marker is authoritative or how its signature is judged
// against the signers they honor.
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
// extra reads are skipped. The original signer is the earliest of the version's
// signatures that verify (registryindex.VerifiedArtifactSigner) — the one whose
// log time the index records as the version's date — so a signature attached later
// by another trusted identity never makes that identity's markers count. The
// closure discovery pass reads its original signer from the pull-time signature
// pass instead, which ranks a version's verifying signatures by the same function
// (pullverify.EarlierSignature), so it folds in the same earliest signer rather
// than whichever signature the registry lists first.
//
// Every step stays best-effort: a referrer-listing, decode or verification failure
// skips that version rather than failing the scan. The index walk does not read it
// this way: it reads collectVersionDeprecation's failure to read and records nothing
// of a repository whose marker could not be read. A version with no honored marker
// contributes nothing, so the slice is empty when no version is withdrawn or
// deprecated.
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
		dep, note, _, _ := collectVersionDeprecation(ctx, wsCfg, policy, target, repo, version, notices, nil)
		if note != nil {
			notes = append(notes, *note)
		}
		if dep != nil {
			deprecations = append(deprecations, *dep)
		}
	}
	return deprecations, notes
}

// collectVersionDeprecation is CollectVersionDeprecations for one version: the
// authoritative marker it records, if any, the note it reports, if any, and
// whether every read the judgement needed was answered. It is the one home of that
// judgement, so the scan over a version list and an index refresh reading one
// version again cannot come to judge a marker differently.
//
// answered is false where a marker is present and not honored, and wherever the
// judgement could not be completed: neither is an answer a later read could not
// change, since a passing failure and a signature attached to the marker
// afterwards both turn into a different verdict without the version's own
// referrers changing.
//
// unread is non-nil where the judgement could not be completed because a read it
// needed failed — the marker listing, a marker's payload, the marker's signatures,
// or, on a keyless policy, the version's own signer that the honored set folds in —
// and the verdict beside it is then no verdict: a withdrawal that could not be
// read is not shown to be absent, and a caller that keeps what it records must not
// record its absence. The note and the commentary say what they said before for a
// marker that could not be read or not honored, so a caller reporting them reports
// the same words.
//
// markerSelected, when non-nil, is called with the authoritative marker's digest
// once it is chosen and before its signature is verified, so a caller can note
// the state of the marker's own referrers ahead of the judgement that reads them.
// The caller must have checked deprecationScanEnabled.
func collectVersionDeprecation(
	ctx context.Context,
	wsCfg workspaceconfig.Config,
	policy workspaceconfig.RegistryConfig,
	target oras.ReadOnlyTarget,
	repo, version string,
	notices io.Writer,
	markerSelected func(markerDigest string),
) (dep *registryindex.VersionDeprecation, note *DeprecationNote, answered bool, unread error) {
	markers, err := oci.ListReferrers(ctx, target, version, deprecation.ArtifactType)
	if err != nil {
		return nil, nil, false, fmt.Errorf("list deprecation markers: %w", err)
	}
	if len(markers) == 0 {
		return nil, nil, true, nil
	}
	markerDesc, marker, err := registryindex.LatestMarkerFully(ctx, target, markers)
	if err != nil {
		fmt.Fprintf(notices, "  deprecation: %s:%s: marker present but not readable (%v); not recorded\n", repo, version, err)
		note := &DeprecationNote{Repo: repo, Version: version, Reason: err.Error()}
		if registryindex.IsUnread(err) {
			return nil, note, false, err
		}
		return nil, note, false, nil
	}
	if markerSelected != nil {
		markerSelected(markerDesc.Digest.String())
	}
	// The version's own signer joins the honored set only on a keyless policy with an
	// anchor to verify it against; with no anchor no signature could verify, so it
	// is not read and the set is the configured deprecation identities alone.
	var origSigner *pullverify.Result
	var origUnread error
	if policy.PublicKey == "" && signerVerificationEnabled(wsCfg, policy) {
		origSigner, origUnread = registryindex.ReadArtifactSigner(ctx, wsCfg, target, version, policy)
	}
	signer, err := registryindex.VerifyMarkerSignature(ctx, wsCfg, target, markerDesc.Digest.String(), policy, origSigner)
	if err != nil {
		fmt.Fprintf(notices, "  deprecation: %s:%s: marker present but not honored (%v); not recorded\n", repo, version, err)
		note := &DeprecationNote{Repo: repo, Version: version, Reason: err.Error()}
		switch {
		case registryindex.IsUnread(err):
			return nil, note, false, err
		case origUnread != nil:
			// The honored set lacked the version's own signer only because reading it
			// failed, so "not honored" is not known.
			return nil, note, false, fmt.Errorf("read the version's own signer: %w", origUnread)
		}
		return nil, note, false, nil
	}
	fmt.Fprintf(notices, "  deprecation: %s:%s: marker present and honored; recorded as %s\n", repo, version, marker.Severity.StatedAs())
	return &registryindex.VersionDeprecation{Version: version, Marker: marker, Signer: signer},
		&DeprecationNote{Repo: repo, Version: version, Recorded: true, Severity: marker.Severity.StatedAs()},
		true, nil
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
// subtree. The host directory is replaced whole, so the file set on disk is
// exactly the current record set: a repository that dropped out of the registry
// leaves no stale file behind. Each per-repo file is deterministic — indented
// JSON with a trailing newline, ordered slices, and author-order labels — so two
// builds against the same registry state produce byte-identical files and an
// identical file set. The timestamp lives only in the freshness sibling file,
// beside the host directory, so it never perturbs the records.
//
// The new index is written aside and swapped in, so a process ended at any point
// of the write, with no chance to clean up after itself, never leaves a partial
// record set where a reader looks, nor a freshness time beside records it was not
// written for. The records and the timestamp are first written in full
// under names of their own beside the host's; then the host's freshness sibling
// is removed, its directory is moved aside, the new directory takes its name, and
// the new timestamp takes the sibling's name last. Between any two of those steps
// a reader of the host's index finds the previous record set with its timestamp
// or with none, no index at all, or the new record set with no timestamp or with
// its own. Whatever an ended write left under the names beside the host's is
// cleared by the host's next write, and a reader never looks there.
//
// It is exported so a test can lay out an index fixture through the very writer
// the product uses, rather than through a second hand-written layout that would
// agree with it until the day the layout changed.
func WriteHostIndex(workspaceRoot, cacheDir string, idx HostIndex, now time.Time) error {
	hostDir := oci.IndexBasePath(workspaceRoot, cacheDir, idx.Host)
	stampPath := hostDir + ".updated"
	staging := hostDir + indexStagingSuffix
	retired := hostDir + indexRetiredSuffix
	stagedStamp := hostDir + indexStagedStampSuffix
	// The index sits in the workspace's artifact cache, so the directory replaced,
	// the freshness file beside it and every name the new index is laid out under
	// first are held to the root the cache directory picks before anything is
	// removed or written.
	root := writescope.RootForCache(workspaceRoot, cacheDir)
	if err := writescope.CheckDelete(root, hostDir); err != nil {
		return err
	}
	for _, path := range []string{stampPath, staging, retired, stagedStamp} {
		if err := writescope.CheckWrite(root, path); err != nil {
			return err
		}
	}
	// The freshness file is removed before the swap, and a removal would take an
	// empty directory standing at its name with it, so one is refused here, before
	// anything changes, as writing the file over it would be.
	if info, err := os.Lstat(stampPath); err == nil && info.IsDir() {
		return fmt.Errorf("index timestamp write %s: is a directory", stampPath)
	}
	for _, leftover := range []string{staging, retired, stagedStamp} {
		if err := indexFS.removeAll(leftover); err != nil {
			return fmt.Errorf("index reset %s: %w", leftover, err)
		}
	}
	if err := indexFS.mkdirAll(staging, 0o755); err != nil {
		return fmt.Errorf("index mkdir %s: %w", staging, err)
	}
	for _, rec := range idx.Records {
		file := registryindex.RecordFile{Record: rec}
		if r, ok := idx.Revalidations[rec.URI]; ok {
			file.Revalidation = &r
		}
		data, err := json.MarshalIndent(file, "", "  ")
		if err != nil {
			return fmt.Errorf("index marshal %s: %w", rec.Name, err)
		}
		data = append(data, '\n')
		// The record's file name is the one the host directory holds it under, so
		// the directory that takes the host's name carries exactly that layout.
		repoPath := filepath.Join(staging, filepath.Base(oci.IndexRepoPath(workspaceRoot, cacheDir, idx.Host, rec.Name)))
		if err := indexFS.writeFile(repoPath, data, 0o644); err != nil {
			return fmt.Errorf("index write %s: %w", repoPath, err)
		}
	}
	stamp := now.Format(time.RFC3339) + "\n"
	if err := indexFS.writeFile(stagedStamp, []byte(stamp), 0o644); err != nil {
		return fmt.Errorf("index timestamp write %s: %w", stagedStamp, err)
	}

	// The swap. The previous timestamp goes first, so it never stands beside the
	// new records; the new one comes last, so it never stands beside the old ones.
	if err := indexFS.remove(stampPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("index timestamp reset %s: %w", stampPath, err)
	}
	if err := indexFS.rename(hostDir, retired); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("index swap: %w", err)
	}
	if err := indexFS.rename(staging, hostDir); err != nil {
		return fmt.Errorf("index swap: %w", err)
	}
	if err := indexFS.rename(stagedStamp, stampPath); err != nil {
		return fmt.Errorf("index timestamp write: %w", err)
	}
	if err := indexFS.removeAll(retired); err != nil {
		return fmt.Errorf("index reset %s: %w", retired, err)
	}
	return nil
}

// The suffixes naming what WriteHostIndex lays out beside a host's index before
// it takes the host's place: the new record directory, the directory the previous
// one is moved aside to, and the new timestamp. Each opens with a tilde, a
// character pathsegment.Encode — which spells every host's name in the index —
// never writes, so no host's own directory or freshness sibling can carry one of
// these names, and clearing one never reaches another host's index. Two hosts'
// names stay apart too: a name with a tilde is a host's spelled name, which holds
// none, followed by one of these suffixes.
const (
	indexStagingSuffix     = "~staging"
	indexRetiredSuffix     = "~retired"
	indexStagedStampSuffix = "~updated"
)

// indexFS is every file-system change WriteHostIndex makes, one field per call.
// The product never replaces it; it is a variable so a test in this package can
// end the write between any two changes, as a process ended part-way would.
var indexFS = struct {
	removeAll func(path string) error
	mkdirAll  func(path string, perm os.FileMode) error
	writeFile func(path string, data []byte, perm os.FileMode) error
	remove    func(path string) error
	rename    func(oldPath, newPath string) error
}{
	removeAll: os.RemoveAll,
	mkdirAll:  os.MkdirAll,
	writeFile: os.WriteFile,
	remove:    os.Remove,
	rename:    os.Rename,
}
