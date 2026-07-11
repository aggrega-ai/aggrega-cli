// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compilerops is the shared compiler-operations layer that language
// front ends drive to run the compiler pipeline against explicit inputs. It
// single-sources the three request-scoped operations a front end needs —
// resolving a Release's catalog closure, running the semantic-validation
// pipeline, and building the IR graph to an in-memory buffer — so every front
// end runs one implementation rather than its own copy.
//
// The layer takes explicit inputs (a workspace root and configuration, the
// Release path, a request-scoped catalog lookup view) and returns plain
// results and typed errors. It holds no session state and reads no front-end
// fields: a caller assembles the inputs from whatever state it holds and adapts
// the plain results back into its own wire shapes. The layer performs no
// filesystem writes — BuildToBuffer returns rendered artifacts in memory.
package compilerops

import (
	"fmt"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// ClosureInputs carries the explicit state ResolveClosure needs. View is the
// request-scoped catalog lookup the closure walk resolves against — seeded with
// this request's pins, so a cache-resident entity resolves against the right
// pin set and another concurrent request's pins can never interleave into this
// walk. ReleasePath is the absolute path of the Release whose siblings and
// closure are resolved; WorkspaceRoot and WorkspaceConfig supply the closure
// walk's workspace anchor and resolved artifact-cache directory.
type ClosureInputs struct {
	View            *catalog.LazyLookup
	ReleasePath     string
	WorkspaceRoot   string
	WorkspaceConfig workspaceconfig.Config
}

// ResolvedClosure bundles the entities a build or drift flow needs together —
// the parsed Release, its resolved Manifest and Aggregate, the raw Aggregate
// bytes the resolver interpolates, the Aggregate's source path, and the
// computed catalog closure.
type ResolvedClosure struct {
	Release       *types.Release
	Manifest      *types.Manifest
	Aggregate     *types.Aggregate
	AggBytes      []byte
	AggregatePath string
	Closure       closure.Closure
}

// ClosureStage names the point in ResolveClosure at which resolution failed, so
// a caller can shape a stage-specific diagnostic (which path to attribute the
// failure to, whether to annotate with catalog-skip context).
type ClosureStage int

const (
	// ClosureStageLoadRelease marks a failure loading the Release document.
	ClosureStageLoadRelease ClosureStage = iota
	// ClosureStageResolveManifest marks a failure resolving the Manifest sibling.
	ClosureStageResolveManifest
	// ClosureStageComposeManifest marks a failure composing the Manifest extends chain.
	ClosureStageComposeManifest
	// ClosureStageLoadAggregate marks a failure loading the top-level Aggregate.
	ClosureStageLoadAggregate
	// ClosureStageComputeClosure marks a failure walking the catalog closure.
	ClosureStageComputeClosure
)

// ClosureError reports a ResolveClosure failure. Stage names which step failed;
// the message carries the same detail the CLI renders for that step. A caller
// that needs catalog-skip annotation applies it to the ComputeClosure stage's
// message.
type ClosureError struct {
	Stage ClosureStage
	msg   string
	err   error
}

// Error returns the rendered failure detail for the failing stage.
func (e *ClosureError) Error() string { return e.msg }

// Unwrap returns the underlying resolution error.
func (e *ClosureError) Unwrap() error { return e.err }

// ResolveClosure loads the Manifest and Aggregate siblings to the Release at
// in.ReleasePath and computes the catalog closure over the request-scoped view.
//
// The top-level Aggregate is resolved by same-directory scan or a `./`/`../`
// file (nil catalog tier): a Release sits beside its Aggregate, and a
// catalog-resident top-level Aggregate is not part of this co-located
// resolution.
//
// The closure compute uses a hermetic lookup (nil puller): registry pull never
// fires during resolution; a missing cache entry surfaces as a closure-walk
// failure rather than an out-of-band network fetch. A broken out-of-closure
// entity is never enumerated-fatal.
//
// On failure the returned *ClosureError names the failing stage and carries the
// rendered detail; the *ResolvedClosure is nil. On success the error is nil.
func ResolveClosure(in ClosureInputs) (*ResolvedClosure, *ClosureError) {
	rel, err := resolver.LoadRelease(in.ReleasePath)
	if err != nil {
		return nil, &ClosureError{
			Stage: ClosureStageLoadRelease,
			msg:   fmt.Sprintf("load release %s: %v", in.ReleasePath, err),
			err:   err,
		}
	}
	releaseDir := filepath.Dir(in.ReleasePath)
	manRes, err := resolver.ResolveManifest(releaseDir, rel.Spec.Manifest)
	if err != nil {
		return nil, &ClosureError{
			Stage: ClosureStageResolveManifest,
			msg:   fmt.Sprintf("load manifest %q: %v", rel.Spec.Manifest, err),
			err:   err,
		}
	}
	manifest, err := resolver.ResolveManifestExtends(releaseDir, manRes.Manifest)
	if err != nil {
		return nil, &ClosureError{
			Stage: ClosureStageComposeManifest,
			msg:   fmt.Sprintf("compose manifest %q extends chain: %v", rel.Spec.Manifest, err),
			err:   err,
		}
	}
	aggRes, err := resolver.ResolveAggregate(releaseDir, rel.Spec.Aggregate, nil, nil)
	if err != nil {
		return nil, &ClosureError{
			Stage: ClosureStageLoadAggregate,
			msg:   fmt.Sprintf("load aggregate %q: %v", rel.Spec.Aggregate, err),
			err:   err,
		}
	}
	aggregate := aggRes.Aggregate
	aggBytes := aggRes.Bytes
	externals := lockfile.ExternalEntitiesFromBytes(rel, aggRes.Bytes, manRes.Bytes)
	cacheDir := in.WorkspaceConfig.ResolvedArtifactCacheDir()
	lookup := in.View.ClosureLookup(nil, in.WorkspaceRoot, cacheDir, externals)
	cl, err := closure.ComputeClosure(rel, manifest, aggregate, in.WorkspaceConfig, lookup)
	if err != nil {
		return nil, &ClosureError{
			Stage: ClosureStageComputeClosure,
			msg:   fmt.Sprintf("closure compute: %v", err),
			err:   err,
		}
	}
	return &ResolvedClosure{
		Release:       rel,
		Manifest:      manifest,
		Aggregate:     aggregate,
		AggBytes:      aggBytes,
		AggregatePath: aggRes.Path,
		Closure:       cl,
	}, nil
}
