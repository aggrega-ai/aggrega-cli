// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestARenderVerdictParseFallsBackToTheBinarysParser drives the render-verdict
// projection of a build's output over a catalog view holding no parser at all —
// the view of a workspace that reads its catalog from a registry and names no
// catalog directory — and reads what each artifact was given.
//
// A Terraform file is parsed through the HCL parser the binary carries, so the
// policy receives its tree; a format neither the view nor the binary serves is
// still refused with the view's own no-parser refusal, word for word; and a view
// refusing two parsers of different names is refused as before, the binary's
// parser never joining that choice.
//
// It fails rather than skips where the binary embeds no entity: the claim is
// about the set a release ships, and a run that never saw it proves nothing.
//
// 🔴 PLANTED DEFECT: in parserSeam (internal/compiler/pipeline/wasm_sockets.go),
// bind the resolver to the catalog's lookup alone, and the first row fails,
// refused with no parser plugin advertising "parse:hcl".
func TestARenderVerdictParseFallsBackToTheBinarysParser(t *testing.T) {
	t.Parallel()
	if len(embedded.EntityURIs()) == 0 {
		t.Fatal("this binary embeds no plugin entity, so nothing here can show its parsers being served: build it " +
			"with the repository's build script, which embeds the core plugins' entities")
	}
	emptyView := &catalog.Catalog{}
	ambiguous := fmt.Errorf("%w: format %q is advertised by parser plugins [a b]", catalog.ErrAmbiguousParserPlugin(), "hcl")
	_, viewRefusal := emptyView.ParserPluginForFormat("no-such-syntax")

	for _, row := range []struct {
		name     string
		lookup   func(string) (types.Plugin, error)
		path     string
		format   string
		wantErr  error
		wantText string
	}{
		{name: "a Terraform file, the view serving no parser", lookup: emptyView.ParserPluginForFormat,
			path: "terraform/main.tf", format: "hcl"},
		{name: "a format neither the view nor the binary serves", lookup: emptyView.ParserPluginForFormat,
			path: "terraform/main.tf", format: "no-such-syntax", wantErr: catalog.ErrNoParserForFormat(),
			wantText: viewRefusal.Error()},
		{name: "the view refusing two parsers of different names",
			lookup: func(string) (types.Plugin, error) { return types.Plugin{}, ambiguous },
			path:   "terraform/main.tf", format: "hcl", wantErr: catalog.ErrAmbiguousParserPlugin(),
			wantText: ambiguous.Error()},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			files := map[string]renderer.AssembledFile{row.path: {Content: []byte(hclSource), Format: row.format}}
			arts, err := sortedRenderedArtifacts(context.Background(), fixtureInputs(),
				resolver.CatalogLookup{ParserPlugin: row.lookup}, files)
			if row.wantErr != nil {
				if !errors.Is(err, row.wantErr) {
					t.Fatalf("the projection answered %v; want %v", err, row.wantErr)
				}
				if !strings.HasSuffix(err.Error(), row.wantText) {
					t.Errorf("the refusal reads\n  %s\nwant it to end with the view's own refusal\n  %s", err, row.wantText)
				}
				return
			}
			if err != nil {
				t.Fatalf("the projection was refused: %v", err)
			}
			if len(arts) != 1 || len(arts[0].Tree) == 0 {
				t.Fatalf("the Terraform file carries no parsed tree: %+v", arts)
			}
			var root map[string]any
			if err := json.Unmarshal(arts[0].Tree, &root); err != nil {
				t.Fatalf("the parsed tree is not a JSON object: %v", err)
			}
			if _, ok := root["resource"]; !ok {
				t.Errorf("the parsed tree carries no resource block, so the file was not read as HCL: %s", arts[0].Tree)
			}
		})
	}
}

// hclSource is a Terraform file declaring one resource block.
const hclSource = `resource "terraform_data" "gateway" {
  input = "gateway"
}
`
