// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Negative-authoring corpus — the reserved delivery-mechanism name. Every
// template reads every delivery mechanism merged into one view at
// .context.inject.all, so a mechanism named `all` would be hidden by that view.
// Each row below writes `all` where one authoring surface names a mechanism and
// drives the workspace through the real build pipeline, asserting the document
// envelope's own sentinel and the reason the schema gives for the refusal.
//
// Every row has a twin that writes `vault_ref` — an ordinary custom mechanism —
// in the same place and nothing else differently, so each refusal is
// attributable to the name alone. A twin whose surface the minimal workspace can
// carry all the way through is built to completion; the rest are held to the
// one claim the envelope can be asked about, that it admits the document.
package compiler_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// reservedMechanismReason is the reason each published schema writes on the
// exclusion that refuses the name, and so the sentence a refusal must print.
const reservedMechanismReason = `"all" is the merged view of every delivery mechanism, read at .context.inject.all, so it cannot name a mechanism`

// reservedCorpusFeature is a Feature the minimal workspace can attach, with one
// dependency socket whose allowlist the feature-socket row varies. An empty
// injectLine declares no allowlist.
func reservedCorpusFeature(injectLine string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/marker
  version: "1.0.0"
spec:
  usesSchema:
    peer:
      selector: "component.aggrega.ai/type=server"
` + injectLine
}

const reservedCorpusFeatureRecipe = `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: marker-compose
  version: "1.0.0"
spec:
  capability: compose
  implements: core/marker
`

const reservedCorpusManifest = `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: corpus-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server: server-recipe:1.0.0
    features:
      type:
        core/marker: marker-compose:1.0.0
`

// delivery is the one entry every authored mechanism below carries. It reads a
// key with `from:`, the shape every inject surface admits.
func delivery(indent string) string {
	return indent + "- in: DELIVERED\n" + indent + "  from: endpoint\n"
}

// reservedRow is one authoring surface. write lays down the workspace for the
// given mechanism name; wantPath is the key path the refusal names in the
// document that wrote the name — the inject block itself, the connection whose
// alternatives the block was read under, or the list entry; builds reports
// whether the `vault_ref` twin is carried through a complete build rather than
// through the envelope alone.
type reservedRow struct {
	name     string
	wantPath string
	write    func(mechanism string) (workspaceFiles, map[string]string)
	builds   bool
}

func reservedRows() []reservedRow {
	return []reservedRow{
		{name: "aggregate-component-own-block", wantPath: "spec.components.api.inject", builds: true, write: func(m string) (workspaceFiles, map[string]string) {
			wf := baseWorkspaceFiles()
			// The component admits exactly the twin's mechanism, so the twin builds
			// and the reserved name is refused before any allowlist is consulted.
			wf.component = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  inject: [vault_ref]
  schema:
    values:
      port: { type: integer }
`
			wf.aggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: corpus-app
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server
      values:
        port: 8080
      inject:
        ` + m + `:
          - in: DELIVERED
            value: a-literal
`
			return wf, nil
		}},
		{name: "aggregate-connection", wantPath: "spec.components.api.uses.peer", builds: true, write: func(m string) (workspaceFiles, map[string]string) {
			wf := baseWorkspaceFiles()
			wf.component = socketedCorpusComponent("      inject: [env, vault_ref]\n")
			wf.recipe = providingCorpusRecipe
			wf.aggregate = dictatingCorpusAggregate(m)
			wf.release = dictatingCorpusRelease()
			return wf, nil
		}},
		{name: "aggregate-attachment", wantPath: "spec.components.api.features.marker.inject", write: func(m string) (workspaceFiles, map[string]string) {
			wf := baseWorkspaceFiles()
			wf.aggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: corpus-app
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server
      values:
        port: 8080
      features:
        marker:
          type: core/marker
          inject:
            ` + m + `:
` + delivery("              ")
			return wf, nil
		}},
		{name: "aggregate-attachment-connection", wantPath: "spec.components.api.features.marker.uses.peer", write: func(m string) (workspaceFiles, map[string]string) {
			wf := baseWorkspaceFiles()
			wf.aggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: corpus-app
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server
      values:
        port: 8080
      features:
        marker:
          type: core/marker
          uses:
            peer:
              upstream:
                target: api
                inject:
                  ` + m + `:
` + delivery("                    ")
			return wf, nil
		}},
		{name: "aggregate-subsystem-attachment", wantPath: "spec.subsystems.edge.features.marker.inject", write: func(m string) (workspaceFiles, map[string]string) {
			wf := baseWorkspaceFiles()
			wf.aggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: corpus-app
  version: "1.0.0"
spec:
  subsystems:
    edge:
      features:
        marker:
          type: core/marker
          inject:
            ` + m + `:
` + delivery("              ") + `      components:
        api:
          type: core/server
          values:
            port: 8080
`
			return wf, nil
		}},
		{name: "release-connection", wantPath: "spec.boundaries.main.components.api.features.marker.uses.peer", write: func(m string) (workspaceFiles, map[string]string) {
			wf := baseWorkspaceFiles()
			wf.release = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: corpus-rel
  environment: dev
  version: "1.0.0"
spec:
  aggregate: corpus-app
  manifest: corpus-manifest
  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          infra:
            replicas: 1
          features:
            marker:
              type: core/marker
              uses:
                peer:
                  upstream:
                    target: api
                    inject:
                      ` + m + `:
` + delivery("                        ")
			return wf, nil
		}},
		{name: "component-own-allowlist", wantPath: "spec.inject[1]", builds: true, write: func(m string) (workspaceFiles, map[string]string) {
			wf := baseWorkspaceFiles()
			wf.component = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  inject: [env, ` + m + `]
  schema:
    values:
      port: { type: integer }
`
			return wf, nil
		}},
		{name: "component-socket-allowlist", wantPath: "spec.usesSchema.peer.inject[1]", builds: true, write: func(m string) (workspaceFiles, map[string]string) {
			wf := baseWorkspaceFiles()
			wf.component = socketedCorpusComponent("      inject: [env, " + m + "]\n")
			wf.recipe = providingCorpusRecipe
			return wf, nil
		}},
		{name: "feature-socket-allowlist", wantPath: "spec.usesSchema.peer.inject[1]", builds: true, write: func(m string) (workspaceFiles, map[string]string) {
			wf := baseWorkspaceFiles()
			wf.manifest = reservedCorpusManifest
			wf.aggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: corpus-app
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server
      values:
        port: 8080
      features:
        marker:
          type: core/marker
`
			return wf, map[string]string{
				"catalog/features/marker.yaml":        reservedCorpusFeature("      inject: [env, " + m + "]\n"),
				"catalog/feature-recipes/marker.yaml": reservedCorpusFeatureRecipe,
			}
		}},
	}
}

// writeReservedWorkspace lays down the minimal workspace with the row's
// documents and any catalog entries the row adds beside it.
func writeReservedWorkspace(t *testing.T, wf workspaceFiles, extra map[string]string) pipelinetest.Input {
	t.Helper()
	in := writeMinimalWorkspace(t, wf)
	dir := filepath.Dir(in.ReleasePath)
	for rel, content := range extra {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return in
}

// TestReservedMechanismNameIsRefusedWhereItIsWritten drives `all` through each
// authoring surface and requires the build to halt at the document envelope,
// with a finding at the key path that wrote the name giving the reason the
// schema declares for the refusal.
//
// The finding is read from the envelope's own report, reached through the
// error chain, rather than from the halt's rendered text. A refusal the
// catalog load raises reaches the build inside a group whose text states the
// pass and a count and carries each finding only on its chain, so the text of
// the halt is not where a catalog entity's finding can be read.
func TestReservedMechanismNameIsRefusedWhereItIsWritten(t *testing.T) {
	t.Parallel()
	for _, r := range reservedRows() {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			wf, extra := r.write("all")
			_, err := pipelinetest.BuildE(t, writeReservedWorkspace(t, wf, extra))
			if err == nil {
				t.Fatalf("%s: a mechanism named all must halt the build; got nil", r.name)
			}
			if !errors.Is(err, envelope.ErrEntityEnvelope()) {
				t.Fatalf("%s: want errors.Is(err, ErrEntityEnvelope); got %v", r.name, err)
			}
			var refusal *envelope.Error
			if !errors.As(err, &refusal) {
				t.Fatalf("%s: no envelope report is reachable from the halt: %v", r.name, err)
			}
			var found []string
			for _, f := range refusal.Findings {
				found = append(found, f.FieldPath+": "+f.Message)
				if f.FieldPath == r.wantPath && strings.Contains(f.Message, reservedMechanismReason) {
					return
				}
			}
			t.Fatalf("%s: want a finding at %q giving the schema's reason %q; the envelope reported %q",
				r.name, r.wantPath, reservedMechanismReason, found)
		})
	}
}

// TestReservedMechanismNameTwinsAreAdmitted is the control for every row
// above: the same workspace naming `vault_ref` instead is admitted by the
// envelope, and where the row is marked to build, it builds.
func TestReservedMechanismNameTwinsAreAdmitted(t *testing.T) {
	t.Parallel()
	for _, r := range reservedRows() {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			wf, extra := r.write("vault_ref")
			_, err := pipelinetest.BuildE(t, writeReservedWorkspace(t, wf, extra))
			if r.builds {
				if err != nil {
					t.Fatalf("%s: a mechanism named vault_ref must build; got %v", r.name, err)
				}
				return
			}
			if errors.Is(err, envelope.ErrEntityEnvelope()) {
				t.Fatalf("%s: the envelope must admit a mechanism named vault_ref; got %v", r.name, err)
			}
		})
	}
}
