// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package closure walks a Release plus its associated Manifest,
// Aggregate, workspace-config, and an injected Lookup, producing
// a typed Closure value the lockfile writer consumes.
//
// The walker is purely structural: it enumerates reference sites
// across the catalog-entity axis (Components, Features, Recipes,
// FeatureRecipes, Aggregates, Providers), the library closures
// (SchemaLibrary $ref: and MappingLibrary $import: sites with
// minimum version selection over conflicting requirements), and
// the Wasm plugin axis (embedded Recipe / FeatureRecipe pipeline
// plugins plus workspace-level plugin attachments). The Recipe
// source: field axis is not part of this package's scope; a
// dedicated source-resolver package handles structured-source
// descent separately.
//
// The walker never makes network calls. Every URI resolution is a
// method invocation on the injected Lookup interface, satisfying
// the ahead-of-time-purity discipline (no runtime fetch from
// inside the compiler core; resolution happens via callbacks the
// caller composes from filesystem-catalog and OCI-client surfaces).
package closure

import (
	"errors"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// Closure captures the full result of ComputeClosure. Fields map
// one-to-one to lockfile sections; downstream code consumes the
// Closure field-by-field when materialising the lockfile.
//
// Field declaration order is alphabetical for predictability.
type Closure struct {
	// CueModules is reserved for third-party / enterprise catalogs
	// whose Components still depend on legacy CUE modules. The
	// first-party walker never populates this slice; the field
	// exists so the lockfile-section symmetry holds when a future
	// catalog adapter writes CueModules from outside.
	CueModules       []ResolvedRef
	Entities         []ResolvedRef
	MappingLibraries []ResolvedLibrary
	SchemaLibraries  []ResolvedLibrary
	WasmPlugins      []ResolvedRef
}

// ResolvedRef captures a single catalog-entity / CUE-module /
// Wasm-plugin resolution result. Mirrors the lockfile package's
// Entity / CueModule / WasmPlugin wire shapes so the lockfile
// writer can copy field-for-field.
type ResolvedRef struct {
	URI             string
	ResolvedVersion string
	Digest          string
	ResolutionPath  string
	ReferencedFrom  []string
}

// ResolvedLibrary captures a SchemaLibrary or MappingLibrary
// resolution result with its consumer chain. Mirrors the lockfile
// package's SchemaLibrary / MappingLibrary wire shapes so the
// lockfile writer can copy field-for-field.
type ResolvedLibrary struct {
	URI             string
	ResolvedVersion string
	Digest          string
	ResolutionPath  string
	Consumers       []LibraryConsumer
}

// LibraryConsumer records a single catalog-entity → library
// reference edge. Mirrors the lockfile package's LibraryConsumer
// wire shape so the lockfile writer can copy field-for-field.
type LibraryConsumer struct {
	ConsumerURI string
	Fragments   []string
}

// Lookup is the abstract surface the walker uses to resolve URIs
// to their (version, digest, tier) tuple. The walker never makes
// network calls itself; every URI resolution is a method
// invocation on a concrete Lookup implementation.
//
// Implementations live outside this package: tests use in-memory
// mocks; production wiring composes a Lookup from a filesystem
// catalog plus an OCI client.
//
// Design note: this interface uses methods rather than the
// callback-struct or bare-function patterns established elsewhere
// in aggrega-cli (pkg/resolver/CatalogLookup is a struct of two
// callback fields; pkg/validator/ResolveInheritance takes a bare
// function-value parameter). The four-method surface here would be
// awkward as a callback struct: each test mock would need a
// 4-field literal with at least 3 non-nil function values, and a
// partial construction would panic at call site rather than fail
// at construction. Method-set interface satisfaction gives
// compile-time guarantees that every implementer provides all
// four resolution surfaces.
type Lookup interface {
	ResolveCatalogEntity(uri string) (Resolution, error)
	ResolveSchemaLibrary(uri string) (LibraryResolution, error)
	ResolveMappingLibrary(uri string) (LibraryResolution, error)
	ResolveWasmPlugin(uri string) (Resolution, error)
}

// Resolution is the common output shape for catalog-entity and
// Wasm-plugin lookups: the resolved version + content digest +
// resolution-path diagnostic tag. NotFound signals that the URI
// did not resolve through any tier; the walker treats NotFound as
// a fatal closure gap unless the URI was optional.
//
// No Body field is exposed: the walker never needs the resolved
// bytes at this package's scope (only the version/digest/tier
// tuple). Future consumers that need body content add a separate
// accessor on Lookup rather than growing this struct.
type Resolution struct {
	ResolvedVersion string
	Digest          string
	ResolutionPath  string
	NotFound        bool
}

// LibraryResolution extends Resolution with the library's entry
// index so the walker can identify which <entry-name> fragments a
// consumer references without re-parsing the body. The Entries
// set is populated by the Lookup implementation at resolution time.
type LibraryResolution struct {
	Resolution
	Entries map[string]struct{}
}

// ErrMVSConflict surfaces when two library consumers require
// incompatible major versions of the same library URI. The
// wrapped error message names the library URI, the conflicting
// versions, and the consumer chain.
var ErrMVSConflict = errors.New("closure: minimum version selection conflict — incompatible library version requirements")

// ErrUnresolvableURI wraps a Lookup NotFound return that the
// walker cannot proceed past (a missing catalog entity inside the
// Release's closure halts the walk). The wrapped error names the
// offending URI and the referenced_from path that led to it.
var ErrUnresolvableURI = errors.New("closure: URI not resolvable via Lookup")

// ErrInvalidLibraryRef surfaces when a $ref: or $import: value
// does not match the canonical library-reference URI shape per
// the file-import vocabulary spec. The wrapped error names the
// offending input string.
var ErrInvalidLibraryRef = errors.New("closure: invalid library reference URI")

// inputs bundles the four loaded structures ComputeClosure walks.
// Keeping them in one struct keeps the public signature small and
// makes future additions (e.g., a Catalog-side index of every
// entity reachable across the workspace) additive.
type inputs struct {
	release      *types.Release
	manifest     *types.Manifest
	aggregate    *types.Aggregate
	workspaceCfg workspaceconfig.Config
	lookup       Lookup
}
