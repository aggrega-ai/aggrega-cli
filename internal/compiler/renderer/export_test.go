// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"errors"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// AttachmentFileSeparatorForTest is the sequence an attachment's encoded
// filename prefix meets the file's own name at, read from the encoding that
// writes that prefix rather than spelled out again.
//
// It is exposed because the external test package cannot name that encoding, and
// a test spelling the two characters for itself would be asserting against its
// own copy of the rule the prefix's injectivity rests on — so it would keep
// passing after the encoding changed the character it opens with.
var AttachmentFileSeparatorForTest = attachmentFileEncoding.SegmentSeparator()

// WrapSourceResolutionErrorForTest exposes the unexported source-resolution
// error classifier so the external test package can assert a sentinel's
// fetch-family categorization directly.
func WrapSourceResolutionErrorForTest(err error) (*cerrors.CompilerError, bool) {
	wrapped := wrapSourceResolutionError(err, "node", "source resolution")
	var ce *cerrors.CompilerError
	if !errors.As(wrapped, &ce) {
		return nil, false
	}
	return ce, true
}

// LinkPassNode is the test-facing description of one endpoint the
// cross-boundary link pass operates over. The external test package cannot
// name the unexported dispatchKey / assembledGroup types the pass signature
// uses, so this bridge accepts exported/primitive fields and marshals them
// into the pass's internal map shapes.
type LinkPassNode struct {
	// Path is the endpoint's dispatch-group path. Two endpoints sharing a Path
	// are intra-boundary; distinct Paths are cross-boundary.
	Path string
	// Provider is the endpoint's resolved provider engine.
	Provider string
	// Engine is the endpoint's provider engine (a real wasmengine engine).
	Engine Provider
	// Annotations is the endpoint's resolved placement annotations.
	Annotations map[string]string
	// Files is the endpoint's group-relative assembled output, keyed by
	// emitted file name.
	Files map[string][]byte
}

// RunCrossBoundaryLinkPassForTest drives the unexported wireCrossBoundaryLinks
// over a minimal graph the external test package assembles from exported
// inputs. It returns the pass's warnings, the per-path assembled files after
// the pass edited them in place, and the pass's error. It is a thin marshalling
// bridge only — the pass logic under test is unchanged.
//
// The warnings come back as the lines a reader would see, rendered through the
// one display formatter, because that is the surface the rows assert on: a
// row checking what a build tells its operator is checking the text, and
// rendering it here keeps that assertion pinned to the formatter every real
// display goes through.
func RunCrossBoundaryLinkPassForTest(
	ctx context.Context,
	edges []types.IREdge,
	nodes map[string]LinkPassNode,
	allProvides NodeProvides,
	engineFactory EngineFactory,
	yamlIndent int,
) (warnings []string, filesByPath map[string]map[string][]byte, err error) {
	return runCrossBoundaryLinkPass(ctx, edges, nodes, allProvides, engineFactory, yamlIndent, nil)
}

// EvalOverLinkReadsForTest evaluates one template the way a consuming node's
// template is evaluated when its connection crosses into another output: the
// context's connection "primary" — consumer "consumer" to producer "producer" on
// socket "db", classified as one the link pass wires — carries the given provides
// stamped as read placeholders. It returns the template's output, the provides
// keys recorded as read off the link in the producer's order, and the error.
func EvalOverLinkReadsForTest(name, tmplText string, provides types.OrderedMap[string]) (string, []string, error) {
	edge := types.IREdge{Source: "consumer", Target: "producer", Socket: "db", Alias: "primary"}
	links := newCrossBoundaryLinks()
	links.fires[linkTripleOf(edge)] = true
	links.aliases[linkTripleOf(edge)] = "primary"
	var uses types.OrderedMap[types.EnrichedEdge]
	uses.Set("primary", types.EnrichedEdge{Target: "producer", Socket: "db", Provides: provides})
	ctx := &types.TemplateContext{Context: types.ContextBlock{InstanceID: "consumer", Uses: uses}}
	graph := &types.IRGraph{Spec: types.IRSpec{Edges: []types.IREdge{edge}}}
	stampCrossBoundaryLinkReads(ctx, "consumer", graph, links)
	out, err := EvalTemplate(name, tmplText, ctx, redact.Keys{})
	return out, links.readKeys(edge, NodeProvides{"producer": provides}), err
}

// LinkReadForTest is one provides key a consuming end's templates read off an
// edge, named by the edge's consuming node, consumed node and socket.
type LinkReadForTest struct {
	Source, Target, Socket, Key string
}

// RunCrossBoundaryLinkPassWithReadsForTest drives the pass exactly as
// RunCrossBoundaryLinkPassForTest does, with the render's record already holding
// the given reads — what the render would have recorded had templates copied those
// keys' placeholders — so a test can read what the pass hands each provider.
func RunCrossBoundaryLinkPassWithReadsForTest(
	ctx context.Context,
	edges []types.IREdge,
	nodes map[string]LinkPassNode,
	allProvides NodeProvides,
	engineFactory EngineFactory,
	yamlIndent int,
	reads []LinkReadForTest,
) (warnings []string, filesByPath map[string]map[string][]byte, err error) {
	links := newCrossBoundaryLinks()
	for _, r := range reads {
		link := linkTriple{source: r.Source, target: r.Target, socket: r.Socket}
		if links.read[link] == nil {
			links.read[link] = map[string]bool{}
		}
		links.read[link][r.Key] = true
	}
	return runCrossBoundaryLinkPass(ctx, edges, nodes, allProvides, engineFactory, yamlIndent, links)
}

func runCrossBoundaryLinkPass(
	ctx context.Context,
	edges []types.IREdge,
	nodes map[string]LinkPassNode,
	allProvides NodeProvides,
	engineFactory EngineFactory,
	yamlIndent int,
	links *crossBoundaryLinks,
) (warnings []string, filesByPath map[string]map[string][]byte, err error) {
	graph := &types.IRGraph{Spec: types.IRSpec{Edges: edges}}
	nodeDispatch := map[string]dispatchKey{}
	nodeEngine := map[string]Provider{}
	nodeAnnotations := map[string]map[string]string{}
	assembledByDispatch := map[dispatchKey]*assembledGroup{}

	for id, n := range nodes {
		key := dispatchKey{Path: n.Path, Provider: n.Provider}
		nodeDispatch[id] = key
		nodeEngine[id] = n.Engine
		nodeAnnotations[id] = n.Annotations
		grp, ok := assembledByDispatch[key]
		if !ok {
			grp = &assembledGroup{provider: n.Provider, prefix: n.Path, files: map[string]AssembledFile{}}
			assembledByDispatch[key] = grp
		}
		for name, body := range n.Files {
			grp.files[name] = AssembledFile{Content: body}
		}
	}

	raised, err := wireCrossBoundaryLinks(
		ctx, graph, allProvides, nodeDispatch, nodeEngine, nodeAnnotations,
		assembledByDispatch, engineFactory, yamlIndent, "", links,
	)
	for _, d := range raised {
		warnings = append(warnings, RenderDiagnosticText(d))
	}

	filesByPath = map[string]map[string][]byte{}
	for key, grp := range assembledByDispatch {
		byName := make(map[string][]byte, len(grp.files))
		for name, af := range grp.files {
			byName[name] = af.Content
		}
		filesByPath[key.Path] = byName
	}
	return warnings, filesByPath, err
}
