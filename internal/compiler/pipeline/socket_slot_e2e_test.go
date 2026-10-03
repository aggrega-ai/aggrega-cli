// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end proofs that a slot a dependency socket declares holds each
// connection's fill to the demands a component's own slot holds its fill to: the
// slot's selector against the Feature filling it, and its requirement against
// every connection wired through the socket. Every row compiles a real workspace
// through the entry point the validation surfaces use, and reads the finding the
// way those surfaces do — its condition by identity, its position by the file and
// the authoring key it carries, and the surface it is filed under by the document
// that wrote it.

package pipeline_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// connectionAttachmentDir is the committed workspace whose gateway draws a
// connection carrying an attachment in the slot its socket declares.
const connectionAttachmentDir = "../testdata/connection-attachment/"

// gatewayDeclaring is the gateway Component with the given body under the slot
// its "models" socket declares, indented to the depth the slot's keys sit at.
func gatewayDeclaring(slotBody string) string {
	return "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: conn/gateway\n" +
		"  version: \"1.0.0\"\n  labels:\n    component.aggrega.ai/type: \"server\"\n" +
		"spec:\n  featuresSchema:\n    db_password:\n      sensitive: true\n" +
		"  usesSchema:\n    models:\n      selector: \"component.aggrega.ai/category=model\"\n" +
		"      featuresSchema:\n        credential:\n" + slotBody +
		"  schema:\n    values:\n      region: { type: string }\n    provides:\n      host: { type: string, required: true }\n"
}

// socketSlotWorkspace writes one row's workspace: a copy of the committed catalog
// with the gateway declaring the given slot body, the committed Manifest, and the
// row's own Aggregate components and Release boundaries.
func socketSlotWorkspace(t *testing.T, slotBody, components, boundaries string) pipelinetest.Input {
	t.Helper()
	dir := t.TempDir()
	catalog := filepath.Join(dir, "catalog")
	if err := os.CopyFS(catalog, os.DirFS(connectionAttachmentDir+"catalog/base")); err != nil {
		t.Fatalf("copy the catalog: %v", err)
	}
	gateway := filepath.Join(catalog, "components", "gateway.yaml")
	if _, err := os.Stat(gateway); err != nil {
		t.Fatalf("the committed catalog no longer holds the gateway the rows rewrite: %v", err)
	}
	manifest, err := os.ReadFile(connectionAttachmentDir + "manifest.yaml")
	if err != nil {
		t.Fatalf("read the committed manifest: %v", err)
	}
	documents := map[string]string{
		gateway:                             gatewayDeclaring(slotBody),
		filepath.Join(dir, "manifest.yaml"): string(manifest),
		filepath.Join(dir, "aggregate.yaml"): "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\n" +
			"metadata:\n  name: connection-attachment-app\n  version: \"1.0.0\"\nspec:\n  components:\n" + components,
		filepath.Join(dir, "release.yaml"): "apiVersion: aggrega.ai/v1alpha1\nkind: Release\n" +
			"metadata:\n  name: connection-attachment-release\n  environment: local\n  version: \"1.0.0\"\n" +
			"spec:\n  aggregate: ./aggregate.yaml\n  manifest: ./manifest.yaml\n  boundaries:\n" + boundaries,
	}
	for path, body := range documents {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return pipelinetest.Input{
		ReleasePath:  filepath.Join(dir, "release.yaml"),
		CatalogPaths: []string{catalog},
	}
}

// gatewayWith is the Aggregate's components block: the gateway drawing the given
// connections on its "models" socket, beside the model they point at.
func gatewayWith(connections string) string {
	return "    gateway:\n      type: conn/gateway\n      values:\n        region: eu\n" +
		"      features:\n        db_password:\n          type: feature.conn/token\n" +
		"          values:\n            key: db-key\n" +
		"      uses:\n        models:\n" + connections +
		"    model:\n      type: conn/model\n"
}

// filledConnection and bareConnection are one connection on the gateway's
// "models" socket, written under the given alias, carrying a fill of the
// socket's slot or none.
func filledConnection(alias string) string {
	return "          " + alias + ":\n            target: model\n            features:\n" +
		"              credential:\n                type: feature.conn/token\n" +
		"                values:\n                  key: " + alias + "-key\n"
}

func bareConnection(alias string) string {
	return "          " + alias + ":\n            target: model\n"
}

// placedInCluster places both components in one boundary, with the given keys
// written under the gateway's own entry, already indented under it.
func placedInCluster(gatewayEntry string) string {
	gateway := "        gateway: {}\n"
	if gatewayEntry != "" {
		gateway = "        gateway:\n" + gatewayEntry
	}
	return "    cluster:\n      directory: ./dist/cluster\n      components:\n" + gateway + "        model: {}\n"
}

// operatorFill is the gateway entry's per-edge surface writing one attachment in
// the socket's slot on the connection under the given alias, creating that
// connection where target is non-empty.
func operatorFill(alias, target string) string {
	entry := "          uses:\n            models:\n              " + alias + ":\n"
	if target != "" {
		entry += "                target: " + target + "\n"
	}
	return entry + "                features:\n                  credential:\n                    type: feature.conn/token\n"
}

// socketSlotFinding is one finding read the way a validation surface reads it:
// where it is, what it says, and the authoring surface it is filed under once
// converted for a terminal.
type socketSlotFinding struct {
	file     string
	field    string
	message  string
	category cerrors.Category
}

// filedUnder is the surface a finding written in the named file is filed under:
// the operator's for the Release, the developer's for the Aggregate. It is
// spelled here rather than derived from the conversion under test, so a
// conversion that stopped reading the document fails the rows below.
func filedUnder(file string) cerrors.Category {
	if file == "release.yaml" {
		return cerrors.OpsError
	}
	return cerrors.DeveloperError
}

// socketSlotFindings runs the workspace through the validation entry point and
// keeps the findings carrying the given condition, failing the test where the
// run stopped before its findings could be read or panicked instead of
// returning.
func socketSlotFindings(t *testing.T, in pipelinetest.Input, condition error) []socketSlotFinding {
	t.Helper()
	var out []socketSlotFinding
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("the run panicked instead of returning: %v", recovered)
			}
		}()
		t4, err := pipelinetest.Tier4E(t, in)
		if err != nil {
			t.Fatalf("the run failed before the graph's findings could be read: %v", err)
		}
		if t4 == nil || t4.Analysis == nil {
			t.Fatal("the run returned no analysis to read the findings from")
		}
		for i := range t4.Analysis.Errors {
			finding := &t4.Analysis.Errors[i]
			if !errors.Is(finding.Sentinel, condition) {
				continue
			}
			out = append(out, socketSlotFinding{
				file:     filepath.Base(finding.OriginFile),
				field:    finding.Field,
				message:  finding.Message,
				category: finding.ToCompilerError().Category(),
			})
		}
	}()
	return out
}

// TestSocketSlotSelectorHoldsEachConnectionsFill proves the label constraint a
// socket's slot declares is evaluated against the Feature each connection fills
// it with, whichever document wrote the fill, and that the finding names the
// slot, the socket and the connection and is positioned at the connection entry
// the fill was written on — in the document that wrote it.
//
// The rows differ in one variable each: the satisfied row and the first refused
// row differ in the constraint alone, and the two refused rows in which document
// wrote the fill.
func TestSocketSlotSelectorHoldsEachConnectionsFill(t *testing.T) {
	t.Parallel()

	const (
		satisfied = "          selector: \"feature.aggrega.ai/kind=secret\"\n"
		refused   = "          selector: \"example.com/grade=gold\"\n"
	)
	for _, row := range []struct {
		name       string
		slot       string
		components string
		boundaries string
		want       []socketSlotFinding
	}{
		{
			name:       "the developer's fill satisfies the slot's selector",
			slot:       satisfied,
			components: gatewayWith(filledConnection("gpt")),
			boundaries: placedInCluster(""),
		},
		{
			name:       "the developer's fill does not satisfy the slot's selector",
			slot:       refused,
			components: gatewayWith(filledConnection("gpt")),
			boundaries: placedInCluster(""),
			want: []socketSlotFinding{{
				file:  "aggregate.yaml",
				field: ".spec.components.gateway.uses.models.gpt.features.credential",
			}},
		},
		{
			name:       "the operator's fill does not satisfy the slot's selector",
			slot:       refused,
			components: gatewayWith(bareConnection("gpt")),
			boundaries: placedInCluster(operatorFill("gpt", "")),
			want: []socketSlotFinding{{
				file:  "release.yaml",
				field: ".spec.boundaries.cluster.components.gateway.uses.models.gpt.features.credential",
			}},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			got := socketSlotFindings(t,
				socketSlotWorkspace(t, row.slot, row.components, row.boundaries),
				analyzer.ErrSlotSelectorUnsatisfied())
			if len(got) != len(row.want) {
				t.Fatalf("the run carries %d slot-selector finding(s) %+v; want %d", len(got), got, len(row.want))
			}
			for i, want := range row.want {
				if got[i].file != want.file || got[i].field != want.field {
					t.Errorf("finding %d is positioned at %s %s; want %s %s",
						i, got[i].file, got[i].field, want.file, want.field)
				}
				if wantCategory := filedUnder(want.file); got[i].category != wantCategory {
					t.Errorf("finding %d, written in %s, is filed under %q; want %q",
						i, want.file, got[i].category, wantCategory)
				}
				for _, mention := range []string{
					`"example.com/grade=gold"`, `declared on slot "credential"`, `of socket "models"`,
					`filled on the connection "gpt"`, `not satisfied by the labels of the Feature filling it, "feature.conn/token`,
				} {
					if !strings.Contains(got[i].message, mention) {
						t.Errorf("finding %d does not name %s: %q", i, mention, got[i].message)
					}
				}
			}
		})
	}
}

// TestRequiredSocketSlotIsFilledOnEveryConnection proves a slot a socket declares
// required is owed by each connection wired through the socket: a socket with two
// connections of which one fills the slot reports the other alone, naming it and
// positioned at its entry; a connection the Release wires is held to it as well
// and reported at the Release's entry; and an optional slot, or one every
// connection fills from either document, reports nothing.
func TestRequiredSocketSlotIsFilledOnEveryConnection(t *testing.T) {
	t.Parallel()

	const (
		required = "          required: true\n"
		optional = "          required: false\n"
	)
	for _, row := range []struct {
		name       string
		slot       string
		components string
		boundaries string
		// want is the finding the run must carry; empty means none.
		want []socketSlotFinding
		// unfilled is the connection the finding must name.
		unfilled string
	}{
		{
			name:       "two connections, of which one fills the required slot",
			slot:       required,
			components: gatewayWith(filledConnection("gpt") + bareConnection("gpt-b")),
			boundaries: placedInCluster(""),
			want: []socketSlotFinding{{
				file:  "aggregate.yaml",
				field: ".spec.components.gateway.uses.models.gpt-b",
			}},
			unfilled: "gpt-b",
		},
		{
			name:       "the second connection filled by the Release",
			slot:       required,
			components: gatewayWith(filledConnection("gpt") + bareConnection("gpt-b")),
			boundaries: placedInCluster(operatorFill("gpt-b", "")),
		},
		{
			name:       "the slot declared optional",
			slot:       optional,
			components: gatewayWith(filledConnection("gpt") + bareConnection("gpt-b")),
			boundaries: placedInCluster(""),
		},
		{
			name:       "a connection the Release wires and leaves unfilled",
			slot:       required,
			components: gatewayWith(filledConnection("gpt")),
			boundaries: placedInCluster("          uses:\n            models:\n              gpt-c:\n" +
				"                target: model\n"),
			want: []socketSlotFinding{{
				file:  "release.yaml",
				field: ".spec.boundaries.cluster.components.gateway.uses.models.gpt-c",
			}},
			unfilled: "gpt-c",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			got := socketSlotFindings(t,
				socketSlotWorkspace(t, row.slot, row.components, row.boundaries),
				analyzer.ErrRequiredSlotUnfilled())
			if len(got) != len(row.want) {
				t.Fatalf("the run carries %d required-slot finding(s) %+v; want %d", len(got), got, len(row.want))
			}
			for i, want := range row.want {
				if got[i].file != want.file || got[i].field != want.field {
					t.Errorf("finding %d is positioned at %s %s; want %s %s",
						i, got[i].file, got[i].field, want.file, want.field)
				}
				if wantCategory := filedUnder(want.file); got[i].category != wantCategory {
					t.Errorf("finding %d, written in %s, is filed under %q; want %q",
						i, want.file, got[i].category, wantCategory)
				}
				for _, mention := range []string{
					`"credential"`, `declared required by socket "models"`, `of component type "conn/gateway`,
					`left unfilled by the connection "` + row.unfilled + `"`,
				} {
					if !strings.Contains(got[i].message, mention) {
						t.Errorf("finding %d does not name %s: %q", i, mention, got[i].message)
					}
				}
				if strings.Contains(got[i].message, `left unfilled by the connection "gpt"`) {
					t.Errorf("finding %d names the connection that fills the slot: %q", i, got[i].message)
				}
			}
		})
	}
}
