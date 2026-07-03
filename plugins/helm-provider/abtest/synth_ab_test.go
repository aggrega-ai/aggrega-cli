// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package abtest

import (
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestHelmProviderSynthABByteIdentity proves the synthesized render path (a
// Recipe declaring workload:, no chart source) reproduces the in-tree engine
// byte-for-byte through the provider Wasm guest, end to end:
//
//   - The workload arrives in the payload (WorkloadProjection), not the
//     filesystem: the host hands the guest the evaluated Workload and its
//     per-slot secret provides, and the guest synthesizes in memory.
//   - The guest passes the production wasm loader's AOT-purity import firewall
//     (firewall ON via invokeGuest → loadGuest), which denies filesystem WASI
//     imports. Driving through the production loader is the proof the guest
//     imports no filesystem capability.
//   - The guest returns each file's serialized bytes and the host writes them
//     verbatim: the decoded RenderNodeOutput files are compared, in emission
//     order, byte-for-byte to the in-tree engine's EmittedFiles.
//
// The oracle is the in-tree HelmEngine.SynthesizeWorkload, fed the NATURAL
// in-memory Workload (never JSON-round-tripped), with empty provenance and
// YAMLIndent 0. The candidate is the guest driven through render_node via the
// production loader, receiving the same Workload marshalled onto the wire.
//
// Fixtures deliberately carry no numeric values inside the opaque
// any / map[string]any / []any pass-through fields (Scheduling.Affinity,
// Tolerations, TopologySpreadConstraints, lifecycle httpGet, volume items):
// the JSON wire widens those ints to float64, a wire-fidelity concern for the
// production dispatch path rather than this synthesis A/B, which exercises the
// typed translation surface.
func TestHelmProviderSynthABByteIdentity(t *testing.T) {
	ctx := context.Background()
	wasmPath := buildPluginWasm(t)

	cases := []struct {
		name   string
		nodeID string
		build  func() (*types.Workload, map[string]types.OrderedMap[string], *types.TemplateContext)
	}{
		{"deployment-with-secrets-and-service", "api", buildDeploymentCase},
		{"all-volume-variants", "store", buildVolumeCase},
		{"statefulset-headless", "postgres", buildStatefulSetCase},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, secretProvides, tmplCtx := tc.build()

			// ORACLE — the in-tree engine, fed the natural Workload with empty
			// provenance so the comparison isolates the synthesis path rather
			// than the (deliberately out-of-ABI) provenance labels.
			oracle, err := (&helm.HelmEngine{}).SynthesizeWorkload(ctx, tmplCtx, w, renderer.NodeRenderContext{NodeID: tc.nodeID})
			if err != nil {
				t.Fatalf("oracle SynthesizeWorkload: %v", err)
			}

			// CANDIDATE — the Wasm guest through the production loader. The
			// workload and its per-slot secret provides arrive in the payload's
			// WorkloadProjection; the guest synthesizes in memory.
			input := renderer.RenderNodeInput{
				Node:               renderer.IRNodeProjection{ID: tc.nodeID},
				Context:            renderctx.RenderContext{},
				WorkloadProjection: &renderer.WorkloadProjection{Workload: *w, Secrets: secretProvides},
			}
			payload, err := input.MarshalJSON()
			if err != nil {
				t.Fatalf("marshal input: %v", err)
			}

			rawOut := invokeGuest(t, ctx, wasmPath, payload)

			var out renderer.RenderNodeOutput
			if err := out.UnmarshalJSON(rawOut); err != nil {
				t.Fatalf("decode RenderNodeOutput: %v (raw: %s)", err, rawOut)
			}

			assertSynthByteIdentical(t, oracle, out.Files)
		})
	}
}

// assertSynthByteIdentical compares the oracle's EmittedFiles to the guest's
// RenderedFiles pairwise AND in order: synth emission order is deterministic and
// load-bearing, so the two file lists must line up index-for-index with equal
// name/path and equal bytes.
func assertSynthByteIdentical(t *testing.T, oracle []renderer.EmittedFile, candidate []renderer.RenderedFile) {
	t.Helper()
	if len(oracle) != len(candidate) {
		t.Fatalf("file count differs: oracle %d, candidate %d\n oracle:    %v\n candidate: %v",
			len(oracle), len(candidate), emittedNames(oracle), renderedPaths(candidate))
	}
	for i := range oracle {
		if oracle[i].Name != candidate[i].Path {
			t.Fatalf("file[%d] name/path differ: oracle %q, candidate %q", i, oracle[i].Name, candidate[i].Path)
		}
		if string(oracle[i].Body) != string(candidate[i].Content) {
			t.Errorf("BYTE MISMATCH for %q:\n--- oracle ---\n%s\n--- candidate ---\n%s", oracle[i].Name, oracle[i].Body, candidate[i].Content)
		} else {
			t.Logf("BYTE-IDENTICAL %q (%d bytes)", oracle[i].Name, len(oracle[i].Body))
		}
	}
}

func emittedNames(files []renderer.EmittedFile) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Name
	}
	return out
}

func renderedPaths(files []renderer.RenderedFile) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Path
	}
	return out
}

// synthProvides builds a resolved secret-slot provides bag carrying the
// referenceable-secret contract keys (name, key) the synthesizer reads.
func synthProvides(name, key string) types.OrderedMap[string] {
	p := types.OrderedMap[string]{}
	p.Set("name", name)
	p.Set("key", key)
	return p
}

// buildDeploymentCase: one container with a named port, one env secret, one
// file secret, and a ClusterIP service → Deployment + Service, exercising the
// secret env/file translation and named-port service wiring.
func buildDeploymentCase() (*types.Workload, map[string]types.OrderedMap[string], *types.TemplateContext) {
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("web", 8080)
	c.Variables.Set("LOG_LEVEL", "info")
	c.Secrets.Set("DB_PASSWORD", types.SecretRef{From: "db-password", As: "env"})
	c.Secrets.Set("TLS_CERT", types.SecretRef{From: "tls-cert", As: "file", Path: "/etc/tls/cert.pem"})
	w.Containers.Set("main", c)

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("web", 8080)
	w.Services.Set("api", svc)

	secretProvides := map[string]types.OrderedMap[string]{
		"db-password": synthProvides("db-creds", "password"),
		"tls-cert":    synthProvides("tls-bundle", "tls.crt"),
	}
	tmplCtx := &types.TemplateContext{}
	tmplCtx.Secrets.Set("db-password", types.TemplateSecret{Provides: secretProvides["db-password"]})
	tmplCtx.Secrets.Set("tls-cert", types.TemplateSecret{Provides: secretProvides["tls-cert"]})
	return w, secretProvides, tmplCtx
}

// buildVolumeCase: one container with a named port and all four pod-spec volume
// variants (ephemeral, configmap, secret, persistent) plus a ClusterIP service
// → Deployment + PVC + Service, exercising volume translation and standalone
// PVC emission.
func buildVolumeCase() (*types.Workload, map[string]types.OrderedMap[string], *types.TemplateContext) {
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("web", 8080)
	w.Containers.Set("main", c)

	w.Volumes.Set("cache", types.VolumeSpec{Ephemeral: &types.EphemeralVolume{SizeLimit: "100Mi"}})
	w.Volumes.Set("config", types.VolumeSpec{ConfigMap: &types.ConfigMapVolume{Source: "app-config"}})
	w.Volumes.Set("secret-store", types.VolumeSpec{Secret: &types.SecretVolume{Source: "app-secret"}})
	w.Volumes.Set("data", types.VolumeSpec{Persistent: &types.PersistentVolume{Size: "10Gi", Class: "gp3"}})

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("web", 8080)
	w.Services.Set("api", svc)

	return w, map[string]types.OrderedMap[string]{}, &types.TemplateContext{}
}

// buildStatefulSetCase: a persistentTemplate volume plus a Headless service →
// StatefulSet (with serviceName and volumeClaimTemplates) + Service, exercising
// the StatefulSet selection and governing-service wiring.
func buildStatefulSetCase() (*types.Workload, map[string]types.OrderedMap[string], *types.TemplateContext) {
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "db:1.0"}
	c.Ports.Set("db", 5432)
	w.Containers.Set("main", c)

	w.Volumes.Set("data", types.VolumeSpec{
		PersistentTemplate: &types.PersistentTemplateVolume{Size: "10Gi", Class: "gp3"},
	})

	svc := types.ServiceSpec{Type: "Headless"}
	svc.Ports.Set("db", 5432)
	w.Services.Set("db", svc)

	return w, map[string]types.OrderedMap[string]{}, &types.TemplateContext{}
}
