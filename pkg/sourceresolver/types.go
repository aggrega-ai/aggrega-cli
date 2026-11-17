// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package sourceresolver dispatches source-artifact resolution
// requests to per-kind adapter implementations. The package owns
// the abstract Adapter interface, the SourceSpec input shape, the
// ResolvedSource lock-time output shape, and the kind-dispatching
// framework that routes by source kind.
//
// The per-kind transport adapters live in sub-packages and register
// with the dispatcher at import time: helm for the two Helm kinds,
// compose for the compose fragment, gitrepo for a Git repository and
// ociartifact for each OCI module kind. Consumers that want the full
// registered set blank-import the adapter sub-packages alongside this
// one; the dispatcher then routes any registered kind without further
// wiring. The kinds the program serves are declared in package
// sourcekind, and a kind declared nowhere there is refused as the
// document naming it is read.
//
// The adapters split on where an archive's trust anchor comes from, and
// the split is a determinism contract rather than an implementation
// detail. An adapter that pulls a pre-built archive pins the upstream
// digest verbatim and re-archives nothing, because upstream's own bytes
// are the thing being trusted. An adapter that builds the archive itself
// — from a fetched fragment, or from a cloned working tree — has no
// upstream digest to inherit, so it writes through the strict archive
// writer (zero timestamps, fixed ownership and modes, alphabetical
// order) and two runs over the same input produce identical bytes.
//
// The framework is the orchestrator-facing entry point: render
// engines never call adapters directly. The orchestrator resolves
// once per node before render dispatch and threads the resulting
// ResolvedSource through NodeRenderContext, keeping engines as
// stateless string-consumers of the resolved extracted path.
package sourceresolver

import (
	"context"
	"net/http"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/netrc"
)

// SourceSpec is the lock-time input to source resolution: the
// declared kind, the base URL, the version constraint, plus
// kind-specific side knobs. OriginalSource preserves the verbatim
// Recipe `source:` field as-authored so downstream consumers can
// surface it in diagnostics without re-fetching.
//
// The side knobs (Chart, ModuleSubdir, Ref) are optional and
// consumed only by the kinds that need them: Chart by helm-oci-chart
// and helm-https-chart (when the URL is a repo root rather than a
// direct chart artifact); ModuleSubdir by git-repository (a directory
// of the cloned repository) and by an OCI module kind whose row takes
// one (a directory of the module package); Ref by git-repository
// (tag, branch, or commit SHA). Adapters ignore knobs they don't
// recognise.
//
// OriginalSource is typed `any` because Recipe source: declarations
// take a union of shapes — a plain string URI for the simple case,
// a structured object with kind-specific fields for the structured
// case. The framework preserves both shapes faithfully through
// round-trip; consumers reading OriginalSource type-assert against
// the expected shape per their context.
//
// WorkspaceRoot and CacheDir name the workspace-side context the
// adapter needs to compute the cache slot path. WorkspaceRoot is
// the absolute path of the workspace root (the directory containing
// the aggregate manifest); CacheDir is the workspace's configured
// cache directory (typically ".aggrega/cache", per
// workspaceconfig.Default). Both are caller-populated at Resolve
// time and live on the spec rather than on the Adapter struct so
// adapters remain stateless across workspace contexts.
type SourceSpec struct {
	Kind           string
	URL            string
	Version        string
	Chart          string
	ModuleSubdir   string
	Ref            string
	WorkspaceRoot  string
	CacheDir       string
	OriginalSource any
	// Client is the HTTP client an adapter uses for its remote fetch, obtained
	// from the network-egress gate (pkg/egress) by the caller that builds the
	// spec. It is non-nil only when the workspace permits egress; the gate refuses
	// an offline workspace before a spec reaches an adapter, so an adapter that
	// reaches its fetch always has a permitted client.
	Client *http.Client
	// Netrc is where the adapters fetching over HTTP — helm-https-chart,
	// compose-https-fragment, and git-repository over http or https — read the
	// credential each request sends, chosen by the request's host. It is read when
	// the fetch runs, and what it holds is written into nothing the resolution
	// returns or records. The production caller sets it to the file the
	// environment names (netrc.FromEnvironment); nil reads no file, so a request
	// sends no credential.
	Netrc *netrc.Source
}

// ResolvedSource is the lock-time output of source resolution: the
// canonical resolved URL, the concrete pinned version, the content
// digest over the cached artifact, and the cache paths where the
// artifact and its extracted form live locally.
//
// The YAML tags map field-for-field to the lockfile's future
// `source_artifacts:` section. Once the lockfile schema extends to
// include that section, the integration is a straightforward
// per-field copy — no shape adaptation required.
//
// CachePath names the artifact's location in the local cache
// (a single file under the workspace cache directory: a `.tar.gz`
// archive, or the selected layer of an OCI module as published).
// ExtractedPath names where the archive's extracted contents live
// (the always-re-extract-on-read discipline keeps the extracted
// form fresh; the path is populated by the cache layer downstream
// of the adapter). Both paths are absolute at runtime.
//
// OriginalSource carries the verbatim Recipe `source:` field as
// supplied at SourceSpec time. Preservation across resolution lets
// downstream diagnostics surface the author-time declaration
// alongside the resolved form, which is load-bearing for the
// "structured-source rejection retired" workflow at render time.
//
// Mediatype is the canonical media-type for the kind (e.g.,
// "application/vnd.cncf.helm.chart.content.v1.tar+gzip" for helm
// charts, "application/x-yaml" for compose fragments,
// "application/gzip" for git-repository archives, the selected
// layer's media type for an OCI module). The adapter
// populates this from its kind-specific constant or from the
// upstream registry's manifest mediatype when available. The
// lockfile writer copies the field verbatim into the lockfile's
// `source_artifacts[].mediatype` slot.
type ResolvedSource struct {
	Kind string `yaml:"kind"`
	// URL is the authored identity URL — spec.URL as authored (a repo root or a
	// direct .tgz for helm-https), NOT the resolved artifact location. Build
	// reconstructs this same URL offline, so it is the stable identity the pin is
	// keyed on. The resolved location a repo-root helm source fetched from is
	// carried separately on ResolvedURL as provenance.
	URL string `yaml:"url"`
	// Chart names the chart within a repo-root helm source (spec.Chart), part of
	// the resolved-source identity so two charts served from one repo root at the
	// same version resolve distinctly. Empty for a direct .tgz, OCI, compose, or
	// git source, none of which selects a chart within the URL.
	Chart string `yaml:"chart,omitempty"`
	// ModuleSubdir names the directory that is the artifact within a git-sourced
	// repository or within an OCI module package (spec.ModuleSubdir), part of the
	// resolved-source identity so two directories of one repository at one commit,
	// or of one package at one version, resolve distinctly. Empty when the whole
	// repository or package is the artifact, and for every kind that reaches its
	// artifact by URL alone.
	ModuleSubdir  string `yaml:"module_subdir,omitempty"`
	PinnedVersion string `yaml:"pinned_version"`
	Digest        string `yaml:"digest"`
	// ResolvedURL is provenance only: the concrete artifact location the authored
	// URL resolved to at pull time — for a repo-root helm source, the direct .tgz
	// URL the repository index resolved the chart+version to. It is NOT part of
	// the artifact identity. Empty when the resolved location equals URL.
	ResolvedURL string `yaml:"resolved_url,omitempty"`
	// ManifestDigest is the OCI manifest digest the cached archive is verified
	// against — the independent root of trust. It differs from Digest, the
	// portable content digest. Only an OCI-distributed source (a Helm chart or an
	// OCI module) populates it; https and git resolvers leave it empty.
	ManifestDigest string `yaml:"manifest_digest,omitempty"`
	Mediatype      string `yaml:"mediatype"`
	CachePath      string `yaml:"cache_path,omitempty"`
	ExtractedPath  string `yaml:"extracted_path,omitempty"`
	OriginalSource any    `yaml:"original_source,omitempty"`
}

// Adapter is the contract every per-kind source resolver implements.
//
// Resolve performs the kind-specific fetch (helm chart pull, https
// download, git clone, OCI pull). It returns the ResolvedSource the
// lockfile writer consumes, or an error wrapped with one of the
// four fetch-error sentinels (see errors.go) plus source-resolver
// context (use the package's Wrap helper for the standard
// formatting).
//
// # Ending a resolution
//
// The ctx Resolve receives belongs to the caller, and ending it ends
// the fetch rather than only the reply: an implementation hands ctx to
// whatever reaches the network, so a withdrawal aborts the transfer on
// the wire instead of leaving it running behind a caller that has
// stopped waiting. An implementation whose transport takes no context
// of its own is responsible for reaching the stop down to that
// transport by some other means; returning early while the fetch
// continues does not satisfy this contract.
//
// A resolution that ends part way writes nothing. Every adapter
// computes the content digest from the complete fetched bytes and only
// then derives its cache-slot path from that digest, so a fetch that
// does not finish never reaches the slot computation: no directory is
// created and no file is written. The cache is left exactly as it was
// found, and because a slot path encodes the digest of its own
// contents, no partial artifact can occupy the slot of a complete one.
//
// Kind returns the spec.Kind value the adapter handles. Dispatcher
// reads this when an adapter is registered, populating its routing
// table. The convention is one adapter per kind across the program's
// lifetime; the kind string is stable and matches the Recipe
// `source.kind:` field.
//
// Adapters are expected to be stateless and goroutine-safe. The
// dispatcher protects its routing table with a read-write mutex
// but offers no synchronisation guarantees around individual
// Resolve invocations.
type Adapter interface {
	Resolve(ctx context.Context, spec SourceSpec) (*ResolvedSource, error)
	Kind() string
}
