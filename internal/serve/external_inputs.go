// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// The seam the inspection renders through is the recorder over the store, so it
// owes the loader's fetch contract; the assertion holds the two packages'
// contracts together at compile time.
var _ wasm.FetchService = (*externalinputs.DeclineRecorder)(nil)

// irGraphPreviewInputs assembles the explicit state the graph inspection hands
// to the shared preview computation.
//
// It is a named method rather than a literal at the call site so that what the
// inspection supplies — and, as much to the point, what it does NOT supply — is
// readable from a test. The external-input seam is the field that motivated
// this: its effect on rendered bytes is observable only through a provider guest
// that requests an external input, so without a construction site a test can
// call, dropping the field would go unnoticed by every gate.
func (s *Server) irGraphPreviewInputs(
	view *catalog.LazyLookup,
	releasePath string,
	lf *lockfile.Lockfile,
	fetchSeam *externalinputs.DeclineRecorder,
) compilerops.PreviewInputs {
	return compilerops.PreviewInputs{
		View:            view,
		ReleasePath:     releasePath,
		WorkspaceRoot:   s.workspaceRoot,
		WorkspaceConfig: s.wsConfig,
		Lockfile:        lf,
		CompilerVersion: s.compilerVersion,
		// The daemon never fills a lockfile, so one it holds was on disk before
		// the request. The drift verify is off under this posture regardless —
		// drift reaches the reader through the Problems panel above, not as a
		// refusal — and the flag states the fact rather than the consequence.
		LockfilePreexisted: lf != nil,
		// The render-time external-input seam, frozen. Without one attached a
		// guest asking for an external input receives a not-available envelope
		// and the render produces different bytes than the same Release renders
		// under a build, with nothing said about it. With this one, a pinned
		// reference is served from the content-addressed store — so the bytes
		// match a build's — and an unpinned one is declined and reported beside
		// the render advisories rather than silently shortening the output.
		// Nothing is written and nothing is fetched: acquisition is off and the
		// transport client is nil, so the inspection cannot reach the network.
		ExternalInputs: fetchSeam,
	}
}

// irGraphExternalInputs is the seam the graph inspection renders through, built
// from this daemon's own workspace configuration.
//
// It exists as a named method rather than inline at the call site so that what
// the inspection attaches is reachable from a test. The seam's EFFECT on
// rendered bytes is only observable through a provider guest that requests an
// external input, and the fixture corpus has none, so a test asserting the
// attachment is the strongest available guard against the field being dropped —
// which would silently restore the not-available envelope and the divergent
// bytes with it.
func (s *Server) irGraphExternalInputs(lf *lockfile.Lockfile) *externalinputs.DeclineRecorder {
	return frozenExternalInputs(s.wsConfig.ResolvedArtifactCacheDir(), lf)
}

// frozenExternalInputs builds the external-input seam an inspection renders
// through, over the workspace artifact cache's inputs store and seeded with the
// Release lockfile's pinned view.
//
// Acquisition is OFF, and that is the whole of the doctrine here: a pinned
// reference is served from the content-addressed store, an unpinned one refuses
// loudly, and nothing is written. Serving a pinned reference from a local
// content-addressed store is a read of content already on disk, not a fetch, so
// rendering the same bytes a build renders and never reaching the network at
// build time are not in tension — the store read is what satisfies both at once.
//
// The transport client is nil for the same reason acquisition is off. With no
// client, a fetch of any kind refuses rather than proceeding, so the seam cannot
// reach the network even if a later edit turned acquisition on by mistake: the
// two are independent switches and both are closed here.
//
// Returning the recorder rather than the service is deliberate — the caller
// needs to know which references were declined in order to say so, and the
// service does not keep that list.
func frozenExternalInputs(cacheDir string, lf *lockfile.Lockfile) *externalinputs.DeclineRecorder {
	var pinned []lockfile.ExternalInput
	if lf != nil {
		pinned = lf.ExternalInputs
	}
	return externalinputs.NewDeclineRecorder(
		externalinputs.NewService(externalinputs.StoreRoot(cacheDir), false, nil, pinned),
	)
}

// externalInputDivergenceDiagnostics renders the advisory an inspection owes
// when its render requested an external reference the lockfile does not pin.
// The reference was declined, so the rendered bytes stopped short of the ones a
// build — which would have acquired and pinned it — would have produced.
//
// It is the second half of attaching the seam at all, and the half that carries
// the honesty: attaching the seam alone closes the byte gap for pinned
// references and leaves an unpinned one rendering incomplete output with nothing
// said, which a reader cannot distinguish from an accurate render. One entry per
// declined reference names it and gives the remedy, so the reader learns which
// reference is missing rather than only that something is.
//
// Warning rather than error severity: the graph and the partial render are still
// worth reading, and the reference is a pin the reader can supply. Empty input
// returns nil, so a fully-pinned render adds nothing.
//
// The code is read off the condition that declined the reference rather than
// spelled here, so the advisory and the refusal a terminal reports name one
// identity. A literal would keep compiling after the condition was re-registered
// under a different code, at which point the two surfaces would silently
// disagree about what the reader is looking at.
func externalInputDivergenceDiagnostics(declined []string, releasePath string) []Diagnostic {
	if len(declined) == 0 {
		return nil
	}
	code, _ := cerrors.CodeOf(externalinputs.ErrAcquisitionDisabled())
	out := make([]Diagnostic, 0, len(declined))
	for _, url := range declined {
		out = append(out, Diagnostic{
			Line:     1,
			Column:   1,
			Severity: editorSeverityWarning,
			Message: fmt.Sprintf(
				"the lockfile does not pin the external reference %s, so it was declined and this render "+
					"cannot show what a build would emit; run `aggrega acquire --release %s` to pin the "+
					"inputs the render needs",
				url, releasePath),
			Code: code,
		})
	}
	return out
}
