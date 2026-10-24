// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"slices"
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
	publishFiles    []renderer.RenderedFile
	resolveFiles    []renderer.RenderedFile
	publishReadKeys []string
	resolveReadKeys []string
}

func (r *recordingLinker) Render(context.Context, map[string]any, renderer.NodeRenderContext) ([]renderer.RenderedFile, error) {
	return nil, nil
}

func (r *recordingLinker) Contract() renderer.ProviderContract { return renderer.ProviderContract{} }

func (r *recordingLinker) LinksCrossBoundary() bool { return true }

func (r *recordingLinker) LinkPublish(_ context.Context, in renderer.LinkPublishInput) (renderer.LinkPublishOutput, error) {
	r.publishFiles = in.ProducerFiles
	r.publishReadKeys = in.Edge.ReadProvidesKeys
	return renderer.LinkPublishOutput{Handle: map[string]string{}}, nil
}

func (r *recordingLinker) LinkResolve(_ context.Context, in renderer.LinkResolveInput) (renderer.LinkResolveOutput, error) {
	r.resolveFiles = in.ConsumerFiles
	r.resolveReadKeys = in.Edge.ReadProvidesKeys
	return renderer.LinkResolveOutput{}, nil
}

// TestCrossBoundaryLink_ReadKeysReachBothHalvesInProducerOrder holds the link
// pass to handing each half the keys the consuming end's templates read off the
// edge, in the producer's author order of its provides rather than the order they
// were recorded in or sorted order, and to handing none where nothing was read —
// the field then stays absent, so the payload is the one a build without such a
// read hands over.
func TestCrossBoundaryLink_ReadKeysReachBothHalvesInProducerOrder(t *testing.T) {
	var provides types.OrderedMap[string]
	provides.Set("port", "5432")
	provides.Set("host", "!GetAtt Db.Endpoint.Address")
	provides.Set("protocol", "postgres")
	provides.Set("database", "appdb")
	allProvides := renderer.NodeProvides{"producer": provides}
	edge := types.IREdge{Source: "consumer", Target: "producer", Socket: "db"}

	for _, tc := range []struct {
		name  string
		reads []string
		want  []string
	}{
		{name: "keys read in another order land in the producer's", reads: []string{"protocol", "port", "host"}, want: []string{"port", "host", "protocol"}},
		{name: "one key read", reads: []string{"database"}, want: []string{"database"}},
		{name: "nothing read", reads: nil, want: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recordingLinker{}
			nodes := map[string]renderer.LinkPassNode{
				"consumer": {Path: "app", Provider: "recording", Engine: rec, Files: map[string][]byte{"a.yaml": []byte("a: 1\n")}},
				"producer": {Path: "data", Provider: "recording", Engine: rec, Files: map[string][]byte{"b.yaml": []byte("b: 1\n")}},
			}
			reads := make([]renderer.LinkReadForTest, 0, len(tc.reads))
			for _, key := range tc.reads {
				reads = append(reads, renderer.LinkReadForTest{Source: edge.Source, Target: edge.Target, Socket: edge.Socket, Key: key})
			}
			if _, _, err := renderer.RunCrossBoundaryLinkPassWithReadsForTest(
				context.Background(), []types.IREdge{edge}, nodes, allProvides, nil, 2, reads); err != nil {
				t.Fatalf("link pass: %v", err)
			}
			if !slices.Equal(rec.publishReadKeys, tc.want) {
				t.Errorf("the publish received read keys %q, want %q", rec.publishReadKeys, tc.want)
			}
			if !slices.Equal(rec.resolveReadKeys, tc.want) {
				t.Errorf("the resolve received read keys %q, want %q", rec.resolveReadKeys, tc.want)
			}
			if tc.want == nil && (rec.publishReadKeys != nil || rec.resolveReadKeys != nil) {
				t.Errorf("a link nothing read carries a read-keys list: publish %q, resolve %q", rec.publishReadKeys, rec.resolveReadKeys)
			}
		})
	}
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
