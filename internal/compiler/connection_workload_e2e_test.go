// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package compiler_test — end-to-end proof that a Workload delivers a credential a
// connection carries by naming the connection beside the slot its socket declares,
// through the real pipeline and the embedded helm guest.
//
// The fixture's router draws three connections through one socket: two carry a
// credential each, one carries none, and the router holds a credential of the same
// Feature on its own block. Its Workload names each connection's slot through the
// connection, imports one connection's whole credential through envFrom, and names
// its own slot directly.

package compiler_test

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

const connectionWorkloadCatalog = "testdata/connection-workload/catalog/base"

// routerDeployment returns the router's rendered Deployment, decoded.
func routerDeployment(t *testing.T, result *pipeline.Result) map[string]any {
	t.Helper()
	paths := make([]string, 0, len(result.Artifacts))
	for p := range result.Artifacts {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if !strings.HasSuffix(p, "/helm/router-deployment.yaml") {
			continue
		}
		var doc map[string]any
		if err := yaml.Unmarshal(result.Artifacts[p], &doc); err != nil {
			t.Fatalf("decode %s: %v", p, err)
		}
		return doc
	}
	t.Fatalf("no router Deployment among the rendered artifacts: %v", paths)
	return nil
}

// routerContainer reads the router Deployment's one container.
func routerContainer(t *testing.T, rendered map[string]any) map[string]any {
	t.Helper()
	spec, _ := rendered["spec"].(map[string]any)
	template, _ := spec["template"].(map[string]any)
	podSpec, _ := template["spec"].(map[string]any)
	containers, _ := podSpec["containers"].([]any)
	if len(containers) != 1 {
		t.Fatalf("router Deployment carries %d containers; want 1", len(containers))
	}
	c, _ := containers[0].(map[string]any)
	return c
}

// TestConnectionWorkload_EachConnectionPresentsItsOwnSecret renders the fixture
// and reads, per environment variable, the Secret and data key the Deployment
// references: each connection's variable references that connection's own
// credential by the locators it published, the router's own variable its own,
// and the connection carrying no credential contributes nothing.
func TestConnectionWorkload_EachConnectionPresentsItsOwnSecret(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/connection-workload/release.yaml",
		CatalogPaths: []string{connectionWorkloadCatalog},
	})
	container := routerContainer(t, routerDeployment(t, result))

	type ref struct{ name, key string }
	got := map[string]ref{}
	env, _ := container["env"].([]any)
	for _, raw := range env {
		e, _ := raw.(map[string]any)
		from, _ := e["valueFrom"].(map[string]any)
		sel, _ := from["secretKeyRef"].(map[string]any)
		name, _ := sel["name"].(string)
		key, _ := sel["key"].(string)
		got[e["name"].(string)] = ref{name: name, key: key}
	}
	want := map[string]ref{
		"ROUTER_TOKEN":  {name: "router-token-secret", key: "token"},
		"MODEL_KEY_GPT": {name: "gpt-team-secret", key: "api-key"},
		"MODEL_KEY_NIM": {name: "nim-team-secret", key: "api-key"},
	}
	for variable, w := range want {
		if got[variable] != w {
			t.Errorf("%s references %+v; want %+v", variable, got[variable], w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("router delivers %v; want exactly %v — the connection carrying no credential delivers nothing", got, want)
	}

	envFrom, _ := container["envFrom"].([]any)
	if len(envFrom) != 1 {
		t.Fatalf("router imports %d environment sources; want the one connection's credential", len(envFrom))
	}
	source, _ := envFrom[0].(map[string]any)
	secretRef, _ := source["secretRef"].(map[string]any)
	if secretRef["name"] != "gpt-team-secret" || source["prefix"] != "GPT_" {
		t.Errorf("envFrom = %v; want the gpt connection's Secret under prefix GPT_", source)
	}
}

// TestConnectionWorkload_RefusesWhatTheConnectionsDoNotDeclare builds a Recipe
// whose Workload names a connection the router does not draw and a slot its
// socket does not declare, beside an admitted entry: the build halts with the
// slot-declaration condition, naming each refused entry at its own position and
// what it named, and saying nothing about the admitted one.
func TestConnectionWorkload_RefusesWhatTheConnectionsDoNotDeclare(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  "testdata/connection-workload/release-refused.yaml",
		CatalogPaths: []string{connectionWorkloadCatalog},
	})
	if !errors.Is(err, validator.ErrWorkloadFeaturesSchema()) {
		t.Fatalf("the build must halt with the slot-declaration condition; got %v", err)
	}
	var wve *validator.WorkloadValidationError
	if !errors.As(err, &wve) {
		t.Fatalf("the refusal must carry the grouped workload report; got %T", err)
	}
	got := map[string]string{}
	for _, f := range wve.Findings {
		got[f.FieldPath] = f.Constraint
	}
	for _, tc := range []struct {
		path      string
		fragments []string
	}{
		{path: ".containers.main.attachments.MODEL_KEY_CLAUDE.connection", fragments: []string{`connection "claude"`, `slot "credential"`}},
		{path: ".containers.main.attachments.MODEL_KEY_NIM.from", fragments: []string{`slot "token"`, `connection "nim"`, `socket "models"`}},
	} {
		constraint, ok := got[tc.path]
		if !ok {
			t.Errorf("no finding at %s; findings: %v", tc.path, got)
			continue
		}
		for _, fragment := range tc.fragments {
			if !strings.Contains(constraint, fragment) {
				t.Errorf("finding at %s does not name %s: %s", tc.path, fragment, constraint)
			}
		}
	}
	if len(got) != 2 {
		t.Errorf("findings = %v; want exactly the two refused entries — the admitted gpt entry is not a finding", got)
	}
}
