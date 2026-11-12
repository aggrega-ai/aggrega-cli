// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pullops

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aggrega-ai/aggrega-cli/internal/entitypin"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// sourceResolver is the one place a run decides which source resolver it uses:
// the caller's injected one where there is one, and the production shim
// otherwise. Both passes that resolve a source in a run — the descent that pins
// the closure, and the re-ask a verify performs over the references already
// recorded — read the decision from here, so a test that injects a resolver
// reaches both and neither pass can end up resolving through something the other
// does not.
func (c Config) sourceResolver(ctx context.Context, wsCfg workspaceconfig.Config) closure.SourceResolver {
	if c.SourceResolver != nil {
		return c.SourceResolver
	}
	return entitypin.SourceResolver(ctx, wsCfg)
}

// recordedReference names one question this pass asks a remote: which artifact
// identity, at which authored reference. It is the dedupe key for the re-ask, so
// two Recipes recorded against one reference of one repository produce one fetch.
type recordedReference struct {
	Kind         string
	URL          string
	Chart        string
	ModuleSubdir string
	RequestedRef string
}

// resolveRecordedSourceReferences re-asks, for every source reference the
// lockfile records an authored ref for, what that reference resolves to now.
//
// # Why a verify has to do this rather than read a closure
//
// The other axes a verify checks are answered by computing the closure and
// comparing: what a Release reaches is a property of documents on disk. A source
// reference is not. A tag or a branch is a name at a remote, and the only way to
// learn where it points today is to ask. So this reaches the network, once per
// distinct recorded reference, and a verify that cannot reach it says so rather
// than reporting a match it did not establish.
//
// # Why it re-resolves through the same resolver the descent uses
//
// A cheaper answer is imaginable — asking a remote for one ref without fetching
// what it names. It is not taken, because it would be a SECOND implementation of
// "what does this reference resolve to", and the verify's whole value is that its
// answer is the answer the next pull would pin. Two implementations agreeing
// today is what hides the day they stop.
//
// # The identity is the recorded one, and only the version comes from the answer
//
// Each artifact returned below carries the identity terms the lockfile recorded
// and takes only the pinned version from the fresh resolution. Reading the
// identity back off the resolution instead would let an adapter that reshapes a
// URL produce an artifact the comparison cannot join to the row it came from —
// which would read as "nothing to compare" rather than as a failure.
//
// Returns the freshly-resolved artifacts in lockfile order. A reference recorded
// more than once — two Recipes reaching the same ref of the same repository —
// is asked once.
func resolveRecordedSourceReferences(
	lf *lockfile.Lockfile,
	resolver closure.SourceResolver,
	workspaceRoot, cacheDir string,
) ([]closure.SourceArtifact, error) {
	if lf == nil || resolver == nil {
		return nil, nil
	}
	var out []closure.SourceArtifact
	asked := map[recordedReference]struct{}{}
	for _, sa := range lf.SourceArtifacts {
		for _, ref := range sa.ReferencedBy {
			if ref.RequestedRef == "" {
				continue
			}
			// The asked-set is keyed on what the resolution takes, not on the
			// referencing entity: two entities naming one reference of one repository
			// ask one question.
			key := recordedReference{sa.Kind, sa.URL, sa.Chart, sa.ModuleSubdir, ref.RequestedRef}
			if _, done := asked[key]; done {
				continue
			}
			asked[key] = struct{}{}

			resolved, err := resolver.Resolve(closure.SourceSpec{
				Kind:         sa.Kind,
				URL:          sa.URL,
				Chart:        sa.Chart,
				ModuleSubdir: sa.ModuleSubdir,
				// The ref is the whole question, and no authored version is supplied
				// because none would be read: an adapter that resolves a reference
				// selects what to fetch from the reference alone, so the ref is the
				// only input the recorded pin was derived from. A row carries a
				// requested ref whenever its Recipe authored one, which is the same
				// set of rows whose pin can move while the document stays unchanged.
				Ref:           ref.RequestedRef,
				WorkspaceRoot: workspaceRoot,
				CacheDir:      cacheDir,
			})
			if err != nil {
				return nil, fmt.Errorf(
					"re-resolve source reference %q of %s: %w", ref.RequestedRef, sa.URL, err)
			}
			out = append(out, closure.SourceArtifact{
				Kind:          sa.Kind,
				URL:           sa.URL,
				Chart:         sa.Chart,
				ModuleSubdir:  sa.ModuleSubdir,
				PinnedVersion: resolved.PinnedVersion,
				ReferencedBy: []closure.SourceArtifactReference{{
					Entity:       ref.Entity,
					URI:          ref.URI,
					Field:        ref.Field,
					RequestedRef: ref.RequestedRef,
				}},
			})
		}
	}
	return out, nil
}

// verifyRecordedSourceReferences is the verify path's source-reference check: it
// re-asks each recorded reference where it points and holds the answer to the
// recorded pin.
//
// An offline workspace cannot ask, so the check is NARROWED rather than failed —
// an air-gapped verify of the pinned closure is a thing the ahead-of-time
// boundary is built to support, and refusing it here would take that away to
// report on an axis nothing could have answered. The narrowing is written to the
// diagnostic stream, because a verify that covered less than usual and said
// nothing about it is the same silence this check exists to end.
func verifyRecordedSourceReferences(
	lf *lockfile.Lockfile,
	resolver closure.SourceResolver,
	wsCfg workspaceconfig.Config,
	workspaceRoot, cacheDir string,
	diagOut io.Writer,
) error {
	if lf == nil || len(lf.SourceArtifacts) == 0 {
		return nil
	}
	// The offline decision is read from the egress gate rather than off the
	// configuration field, so this pre-flight and the fetch it stands in for read
	// one decision.
	if _, err := egress.RoundTripper(wsCfg); errors.Is(err, egress.ErrOfflineEgress()) {
		fmt.Fprintf(diagOut, "  verify: offline, so a source reference recorded in the lockfile "+
			"was not re-asked at its remote; this verify covers the pinned dependencies only\n")
		return nil
	}
	fresh, err := resolveRecordedSourceReferences(lf, resolver, workspaceRoot, cacheDir)
	if err != nil {
		return err
	}
	return lockfile.VerifySourceReferences(lf, fresh)
}
