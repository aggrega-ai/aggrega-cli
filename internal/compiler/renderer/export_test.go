// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

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
	Engine Engine
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
func RunCrossBoundaryLinkPassForTest(
	ctx context.Context,
	edges []types.IREdge,
	nodes map[string]LinkPassNode,
	allProvides NodeProvides,
	engineFactory EngineFactory,
	yamlIndent int,
) (warnings []string, filesByPath map[string]map[string][]byte, err error) {
	graph := &types.IRGraph{Spec: types.IRSpec{Edges: edges}}
	nodeDispatch := map[string]dispatchKey{}
	nodeEngine := map[string]Engine{}
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

	warnings, err = wireCrossBoundaryLinks(
		ctx, graph, allProvides, nodeDispatch, nodeEngine, nodeAnnotations,
		assembledByDispatch, engineFactory, yamlIndent, "",
	)

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
