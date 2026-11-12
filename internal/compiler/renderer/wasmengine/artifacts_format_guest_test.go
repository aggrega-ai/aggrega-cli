// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasmengine_test

import (
	"context"
	"maps"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// TestEmitArtifacts_EachPlacedFileKeepsTheFormatTheGuestDeclared drives a
// Recipe-authored body through the real wire to the embedded terraform guest and
// reads back the format on every file the adapter hands the renderer. The guest
// declares a format for each file it places — native Terraform syntax for a .tf
// file, JSON for a .tf.json file — and the adapter must carry that declaration on
// the emitted record rather than drop it, because the render policies choose a
// parser by it. The JSON row also covers the companion provenance file the guest
// adds beside a JSON body, which no artifacts entry names.
func TestEmitArtifacts_EachPlacedFileKeepsTheFormatTheGuestDeclared(t *testing.T) {
	terraformURI := embeddedGuestURI(t, "terraform")
	e := wasmengine.New(embeddedGuestLookup, terraformURI, wasm.NewCachingLoader(nil))

	cases := []struct {
		name    string
		entries []renderer.ArtifactProjectionEntry
		want    map[string]string // emitted name → declared format
	}{
		{
			name: "native Terraform body",
			entries: []renderer.ArtifactProjectionEntry{{
				Name:    "main.tf",
				Origin:  "inline",
				Content: []byte("resource \"aws_db_instance\" \"db\" {\n  engine = \"postgres\"\n}\n"),
			}},
			want: map[string]string{"main.tf": "hcl"},
		},
		{
			name: "JSON Terraform body and its provenance companion",
			entries: []renderer.ArtifactProjectionEntry{{
				Name:    "main.tf.json",
				Origin:  "inline",
				Content: []byte(`{"resource":{"aws_db_instance":{"db":{"engine":"postgres"}}}}` + "\n"),
			}},
			want: map[string]string{"main.tf.json": "json", "aggrega.provenance.tf": "hcl"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files, err := e.EmitArtifacts(context.Background(), renderer.ArtifactsProjection{Entries: tc.entries}, synthNodeCtx("db"))
			if err != nil {
				t.Fatalf("EmitArtifacts: %v", err)
			}
			got := make(map[string]string, len(files))
			for _, f := range files {
				got[f.Name] = f.Format
			}
			for _, name := range slices.Sorted(maps.Keys(tc.want)) {
				gotFormat, placed := got[name]
				if !placed {
					t.Errorf("the guest placed no %q, so its format was not exercised (placed: %v)", name, slices.Sorted(maps.Keys(got)))
					continue
				}
				if gotFormat != tc.want[name] {
					t.Errorf("%q reaches the renderer with format %q, want the %q the guest declared for it", name, gotFormat, tc.want[name])
				}
			}
			for _, name := range slices.Sorted(maps.Keys(got)) {
				if _, expected := tc.want[name]; !expected {
					t.Errorf("the guest placed %q, which this case does not expect; extend the case rather than ignore it", name)
				}
			}
		})
	}
}
