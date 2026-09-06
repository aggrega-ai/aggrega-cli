// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	stderrors "errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// resolveReleaseClosure loads the Manifest + Aggregate siblings to
// releasePath and computes the catalog closure the daemon's Release-scope
// validation runs over, adapting the shared layer's stage-tagged failure into
// the daemon's wire envelope. Every caller supplies the Release its own
// request named, so no two callers can be resolving different Releases
// while believing they share one.
//
// It resolves through compilerops.ResolveValidationClosure — the entry every
// front door that validates a Release resolves through — rather than through
// the general resolver, so the footing a release-scope validation runs on is
// one value at one site and the reason for it is held by the code that sets
// it. No lockfile is supplied: this is the quiet editing-cadence load
// runTier4Validation describes, and the entry admits that absence.
//
// view is the request-scoped catalog lookup the closure walk resolves
// against — seeded with this request's pins, so a cache-resident entity
// resolves against the right pin set and another concurrent request's
// pins can never interleave into this walk. The same view is threaded
// onward into the validation core, so closure compute and validation see one
// consistent pin snapshot. skipContext reads the view's skip record for
// the same reason.
//
// On error returns a populated *jsonRPCError envelope (1004
// errEntityNotResolvable), which the caller maps into a closure-level
// diagnostic at the head of the file.
//
// The envelope carries the failure it was rendered from as well as the
// rendered text, because rendering is one-way and a caller that maps
// the envelope onto a diagnostic needs the failure to report the
// condition behind it. Every stage of the resolution wraps whatever
// refused, so the condition a closure-level failure names is reachable
// through the envelope at each of the returns below.
//
// The load-stage failures populate Data.File with the Release document whose
// load failed — File names the source document a failure is about, and nothing
// else; a lockfile-scoped failure names its lockfile in Data.Lockfile instead,
// so a client never has to read the error code to learn which artifact it was
// handed. Where the failing stage is attributable to a key the Release itself
// authored, the same payload also carries that key's span, so an editor
// underlines the reference that could not be followed rather than marking the
// head of the file; closureErrorData decides that and states which stages
// qualify. The closure-compute failure stays Data-less (the catalog spans
// multiple source dirs) and is annotated with skipContext so an in-closure
// entity skipped at the tolerant lazy enumeration is explained rather than a
// bare "not found".
func (s *Server) resolveReleaseClosure(view *catalog.LazyLookup, releasePath string) (*compilerops.ResolvedClosure, *jsonRPCError) {
	resolved, cerr := compilerops.ResolveValidationClosure(compilerops.ValidationClosureInputs{
		View:            view,
		ReleasePath:     releasePath,
		WorkspaceRoot:   s.workspaceRoot,
		WorkspaceConfig: s.wsConfig,
	})
	if cerr != nil {
		if cerr.Stage == compilerops.ClosureStageComputeClosure {
			return nil, &jsonRPCError{
				Code:    errEntityNotResolvable,
				Message: s.skipContext(view, cerr.Error()),
				cause:   cerr,
			}
		}
		return nil, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: cerr.Error(),
			Data:    s.closureErrorData(releasePath, cerr.Stage),
			cause:   cerr,
		}
	}
	return resolved, nil
}

// previewFailureEnvelope adapts a preview failure into the daemon's wire
// envelope, preserving the distinction the handler's callers already depend on:
// a failure to RESOLVE what the Release reaches is an entity-not-resolvable
// envelope carrying the catalog-skip annotation, while a failure inside the
// pipeline is an internal error. The two are told apart by the gate the preview
// reports rather than by matching the failure's text, so the mapping cannot rot
// when a message is reworded.
//
// The failure it was rendered from rides on the envelope as well as the rendered
// text, because rendering is one-way and a caller that maps the envelope onto a
// diagnostic needs the failure to report the condition behind it.
func (s *Server) previewFailureEnvelope(view *catalog.LazyLookup, releasePath string, preview *compilerops.PreviewResult, err error) *jsonRPCError {
	gate := compilerops.GateNone
	if preview != nil {
		gate = preview.FailedGate
	}
	switch gate {
	case compilerops.GateClosureResolution:
		// A closure-walk failure is annotated with the skips that could explain it;
		// an earlier-stage failure names the Release document it was loading.
		var cerr *compilerops.ClosureError
		if stderrors.As(err, &cerr) && cerr.Stage != compilerops.ClosureStageComputeClosure {
			return &jsonRPCError{
				Code:    errEntityNotResolvable,
				Message: cerr.Error(),
				Data:    s.closureErrorData(releasePath, cerr.Stage),
				cause:   err,
			}
		}
		return &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: s.skipContext(view, err.Error()),
			cause:   err,
		}
	case compilerops.GateAxisReconstruction:
		return &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: s.skipContext(view, err.Error()),
			cause:   err,
		}
	default:
		return &jsonRPCError{
			Code:    jsonRPCInternalError,
			Message: fmt.Sprintf("pipeline: %v", err),
			cause:   err,
		}
	}
}

// releaseFieldForClosureStage names the Release key a closure-resolution
// failure is attributable to, or "" when the failing stage names none.
//
// A stage earns a key only when the fault belongs to a value the Release
// itself authored: the manifest reference the resolution could not follow, or
// the aggregate reference it could not load. Composing the manifest's extends
// chain shares the manifest reference's key, because that reference is what
// selected the chain that turned out to be broken.
//
// The remaining stages name nothing, and each for its own reason. A failure
// loading the Release has no loaded document to walk and no single key to
// blame — the fault is frequently that the document does not parse at all. A
// closure-walk failure spans the whole catalog rather than one document, which
// is why that stage carries no payload here in the first place. And a manifest
// reference-shape fault belongs to the Manifest, so spanning a Release key
// would underline a document that is not at fault.
func releaseFieldForClosureStage(stage compilerops.ClosureStage) string {
	switch stage {
	case compilerops.ClosureStageResolveManifest, compilerops.ClosureStageComposeManifest:
		return ".spec.manifest"
	case compilerops.ClosureStageLoadAggregate:
		return ".spec.aggregate"
	default:
		return ""
	}
}

// closureErrorData builds the wire payload for a closure-resolution failure:
// the Release document the failure is about, plus the span of the offending key
// when the failing stage names one and the authored document yields it.
//
// The span is resolved through the same field-path walk every other positioned
// surface resolves through, so a key reported here and the same key reported as
// a document diagnostic cannot sit at different coordinates. Its columns are
// counted in the client's negotiated encoding, matching every other position
// the daemon reports.
//
// It reads the bytes ON DISK rather than any editor buffer, and that is a
// correctness requirement rather than a convenience: the resolution that failed
// read the file from disk, so a span taken from an unsaved buffer could name a
// line the fault is not on.
//
// Every miss keeps the plain file-level shape, so a client reads either a
// position or none at all and never a fabricated one. Four misses reach it: a
// stage with no key to blame, a file that does not read, a file that does not
// parse, and a path the walk resolves nothing for.
func (s *Server) closureErrorData(releasePath string, stage compilerops.ClosureStage) *errorData {
	fileLevel := newErrorData(releasePath, 0, 0)
	field := releaseFieldForClosureStage(stage)
	if field == "" {
		return fileLevel
	}
	content, readErr := os.ReadFile(releasePath)
	if readErr != nil {
		return fileLevel
	}
	var doc yaml.Node
	if unErr := yaml.Unmarshal(content, &doc); unErr != nil {
		return fileLevel
	}
	idx := yamlpos.BuildLineIndexWithEncoding(content, s.positionEncoding())
	span, ok := yamlpos.FieldRange(&doc, idx, field)
	if !ok {
		return fileLevel
	}
	return newErrorDataWithRange(releasePath, span.Line, span.Column, span.EndLine, span.EndColumn)
}

// skipContext annotates a closure-level message with the catalog files the lazy
// enumeration skipped (tolerantly) — so an in-closure entity that went missing
// because it was skipped is explained, not silently absent. Deterministic
// (sorted by path); no-op when nothing was skipped. Mirrors the CLI build's
// withSkipContext, sourced from the supplied request view's skip record — the
// same view whose enumeration produced the unresolved-URI failure, so the skips
// reported are exactly the ones that could explain it (not a session catalog's
// possibly-stale skip set).
//
// Each reason is rendered as its whole cause chain: the outermost frame names
// only which step refused the file, and the frame beneath it is the one naming
// the constraint the file violated.
//
// It stays a separate implementation from the CLI's withSkipContext rather than
// sharing one, and the reason is that the two do different jobs on different
// data. The CLI decorates an ERROR, so its whole point is that the failure
// stays in the chain and its condition remains matchable at the command
// boundary; this one composes a message STRING for a diagnostic envelope, where
// there is no chain to preserve. The CLI's refusals also carry the kind and
// name a header re-parse recovered, while this backing records a refusal by the
// path it read and deliberately does not re-read a header it already refused to
// trust — so the two have different fields to render. And the media differ: an
// indented multi-line terminal block against a single-line envelope message.
// What IS shared is the sentence they teach: both state the same
// closure-membership distinction in the same words.
//
// That shared sentence is also where the missing identity shows: with it, the
// CLI can name the one refusal that declares the identity a failing reference
// asks for and report that refusal ahead of the failure it explains, leaving
// this sentence for the case where it cannot. A refusal recorded by path alone
// declares nothing to match a reference against, so this surface always asks
// the reader to apply the distinction themselves.
func (s *Server) skipContext(view *catalog.LazyLookup, msg string) string {
	skips := view.Skips()
	if len(skips) == 0 {
		return msg
	}
	paths := make([]string, 0, len(skips))
	for p := range skips {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var b strings.Builder
	b.WriteString(msg)
	b.WriteString("; note: catalog files skipped at load — a refusal outside this Release's closure is tolerated and never fails a build, so this failure is explained only by a refusal naming something this Release reaches:")
	for _, p := range paths {
		fmt.Fprintf(&b, " %s: %s;", p, cerrors.CauseChain(skips[p]))
	}
	return b.String()
}
