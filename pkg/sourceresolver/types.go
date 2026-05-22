// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package sourceresolver dispatches source-artifact resolution
// requests to per-kind adapter implementations. The package owns
// the abstract Adapter interface, the SourceSpec input shape, the
// ResolvedSource lock-time output shape, and the kind-dispatching
// framework that routes by source kind.
//
// Per-kind transport adapters (helm-oci-chart, helm-https-chart,
// compose-https-fragment, terraform-module-git, terraform-module-oci)
// live in sub-packages and register with the dispatcher at import
// time. Consumers that want the full registered set blank-import
// the adapter sub-packages alongside this one; the dispatcher then
// routes any registered kind without further wiring.
//
// The framework is the orchestrator-facing entry point: render
// engines never call adapters directly. The orchestrator resolves
// once per node before render dispatch and threads the resulting
// ResolvedSource through NodeRenderContext, keeping engines as
// stateless string-consumers of the resolved extracted path.
package sourceresolver

// SourceSpec is the lock-time input to source resolution: the
// declared kind, the base URL, the version constraint, plus
// kind-specific side knobs. OriginalSource preserves the verbatim
// Recipe `source:` field as-authored so downstream consumers can
// surface it in diagnostics without re-fetching.
//
// The side knobs (Chart, ModuleSubdir, Ref) are optional and
// consumed only by the kinds that need them: Chart by helm-oci-chart
// and helm-https-chart (when the URL is a repo root rather than a
// direct chart artifact); ModuleSubdir by terraform-module-git and
// terraform-module-oci (when the module sits inside a larger
// repository or registry artifact); Ref by terraform-module-git
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
}

// ResolvedSource is the lock-time output of source resolution: the
// canonical resolved URL, the concrete pinned version, the content
// digest over the always-`.tar.gz` archive, and the cache paths
// where the archive and its extracted form live locally.
//
// The YAML tags map field-for-field to the lockfile's future
// `source_artifacts:` section. Once the lockfile schema extends to
// include that section, the integration is a straightforward
// per-field copy — no shape adaptation required.
//
// CachePath names the archive's location in the local cache
// (a single `.tar.gz` file under the workspace cache directory).
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
type ResolvedSource struct {
	Kind           string `yaml:"kind"`
	URL            string `yaml:"url"`
	PinnedVersion  string `yaml:"pinned_version"`
	Digest         string `yaml:"digest"`
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
	Resolve(spec SourceSpec) (*ResolvedSource, error)
	Kind() string
}
