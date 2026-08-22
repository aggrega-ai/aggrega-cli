// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The link pass hands each half of a cross-boundary edge its OWN end's assembled
// files: the publish runs on the consumed (target) end and receives that end's
// files, the resolve runs on the consuming (source) end and receives that end's.
// The two calls sit one line apart and take the same-shaped argument, so passing
// one end's files to the other end's guest is a change nothing else here would
// report — every patch still applies and every rendered byte is unchanged, and
// only a provider reading the document would ever notice. These tests pin which
// group reaches which half.

// recordingLinker is a provider that records the payloads the link pass hands it
// and returns no wiring, so the pass applies no patch and needs no codec.
type recordingLinker struct {
	publishFiles []renderer.RenderedFile
	resolveFiles []renderer.RenderedFile
}

func (r *recordingLinker) Render(context.Context, map[string]any, renderer.NodeRenderContext) ([]renderer.RenderedFile, error) {
	return nil, nil
}

func (r *recordingLinker) Contract() renderer.ProviderContract { return renderer.ProviderContract{} }

func (r *recordingLinker) LinksCrossBoundary() bool { return true }

func (r *recordingLinker) LinkPublish(_ context.Context, in renderer.LinkPublishInput) (renderer.LinkPublishOutput, error) {
	r.publishFiles = in.ProducerFiles
	return renderer.LinkPublishOutput{Handle: map[string]string{}}, nil
}

func (r *recordingLinker) LinkResolve(_ context.Context, in renderer.LinkResolveInput) (renderer.LinkResolveOutput, error) {
	r.resolveFiles = in.ConsumerFiles
	return renderer.LinkResolveOutput{}, nil
}

func TestCrossBoundaryLink_EachHalfReceivesItsOwnEndsFiles(t *testing.T) {
	const (
		provider   = "recording"
		sourceFile = "source-only.yaml"
		targetFile = "target-only.yaml"
	)
	rec := &recordingLinker{}
	// Each end emits a distinctly named file, so which group reached which half
	// is readable from the recorded payload rather than inferred.
	nodes := map[string]renderer.LinkPassNode{
		"consumer": {Path: "app", Provider: provider, Engine: rec, Files: map[string][]byte{sourceFile: []byte("kind: consuming\n")}},
		"producer": {Path: "data", Provider: provider, Engine: rec, Files: map[string][]byte{targetFile: []byte("kind: consumed\n")}},
	}

	warnings, _, err := renderer.RunCrossBoundaryLinkPassForTest(
		context.Background(),
		[]types.IREdge{{Source: "consumer", Target: "producer", Socket: "db"}},
		nodes, nil, nil, 2)
	if err != nil {
		t.Fatalf("link pass: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("a same-provider wire must not warn, got %v", warnings)
	}

	// The publish runs on the consumed end, so it receives the consumed end's file.
	if got := paths(rec.publishFiles); len(got) != 1 || got[0] != targetFile {
		t.Errorf("the publish received %v, want the consumed end's %q", got, targetFile)
	}
	// The resolve runs on the consuming end, so it receives the consuming end's.
	if got := paths(rec.resolveFiles); len(got) != 1 || got[0] != sourceFile {
		t.Errorf("the resolve received %v, want the consuming end's %q", got, sourceFile)
	}
}

func paths(files []renderer.RenderedFile) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}
