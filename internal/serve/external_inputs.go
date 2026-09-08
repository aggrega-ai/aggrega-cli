// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// irGraphPreviewInputs assembles the explicit state the graph inspection hands
// to the shared preview computation.
//
// It is a named method rather than a call at each site so that what the
// inspection supplies — and, as much to the point, what it does NOT supply — is
// readable from a test.
//
// The render-time external-input seam is passed frozen. Without one attached a
// guest asking for an external input receives a not-available envelope and the
// render produces different bytes than the same Release renders under a build,
// with nothing said about it. With this one, a pinned reference is served from
// the content-addressed store — so the bytes match a build's — and an unpinned
// one is declined and reported beside the render advisories rather than silently
// shortening the output. Nothing is written and nothing is fetched: acquisition
// is off and the transport client is nil, so the inspection cannot reach the
// network.
func (s *Server) irGraphPreviewInputs(
	view *catalog.LazyLookup,
	releasePath string,
	lf *lockfile.Lockfile,
	fetchSeam *externalinputs.DeclineRecorder,
) compilerops.PreviewInputs {
	return compilerops.NewPreviewInputs(
		fetchSeam,
		view,
		releasePath,
		s.workspaceRoot,
		s.wsConfig,
		s.compilerVersion,
		// The daemon never fills a lockfile, so one it holds was on disk before
		// the request. The drift verify is off under this posture regardless —
		// drift reaches the reader through the Problems panel above, not as a
		// refusal — and the flag states the fact rather than the consequence.
		compilerops.WithLockfile(lf, lf != nil),
	)
}

// irGraphExternalInputs is the seam the graph inspection renders through, built
// from this daemon's own workspace configuration through the one home the frozen
// posture has.
//
// It exists as a named method rather than inline at the call site so that what
// the inspection attaches is reachable from a test: the shared computation
// cannot be handed no seam at all, but it can be handed the wrong one, and this
// is what a test compares against.
func (s *Server) irGraphExternalInputs(lf *lockfile.Lockfile) *externalinputs.DeclineRecorder {
	return compilerops.FrozenExternalInputs(s.wsConfig.ResolvedArtifactCacheDir(), lf)
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
// The identifiers are read off the condition that declined the reference rather
// than spelled here, so the advisory and the refusal a terminal reports name one
// identity. A literal would keep compiling after the condition was re-registered
// under a different code, at which point the two surfaces would silently
// disagree about what the reader is looking at.
//
// It is the one entry on the build layer that names a REGISTERED condition, so it
// carries the quotable code and the pointer as well — which is why the keys are
// read through the condition-code constructor rather than the class one. The
// distinction is what underlies the entry and never which layer publishes it: an
// advisory's class code and a conventions rule's identity have no condition behind
// them, and this one does.
func externalInputDivergenceDiagnostics(declined []string, releasePath string) []Diagnostic {
	if len(declined) == 0 {
		return nil
	}
	codes := conditionCodesOf(externalinputs.ErrAcquisitionDisabled())
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
			ConditionCodes: codes,
		})
	}
	return out
}
