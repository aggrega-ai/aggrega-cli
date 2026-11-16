// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// End-to-end proofs that a flow platform's definition carried in an Aggregate's
// values reaches the platform exactly as the platform exported it when every
// opening double brace in it is written as the template action that prints
// one, and that the same definition written as it stands halts the build at
// the Aggregate.
//
// An Aggregate's text is read as a Go template before it is read as YAML, so
// the double-brace expressions a flow platform writes into its own definitions
// — n8n's ={{ $json.body.message }}, Flowise's {{question}} and the
// {{chatOpenAI_0.data.instance}} it writes for a connection between two nodes
// — are template actions unless each {{ is written {{"{{"}}, which renders as
// the two braces. These proofs build the catalog's n8n and Flowise Components
// through both Recipes each one has, helm and compose, with the build-faithful
// harness, and compare what reaches the platform with the export byte for byte.
//
// These tests read catalog entities from the aggrega-hub repository checked out
// beside this one (the paths leave this repository for aggrega-hub). Without that checkout they skip, naming it.

package compiler_test

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// flowEscapeDir is the fixture workspace: the two exports under exports/, an
// Aggregate per platform written the escaped way and one written as exported,
// a Manifest and a Release per target, and the provider Plugin entities under
// catalog/.
const flowEscapeDir = "testdata/flow-definition-escape"

// flowEscapeHub is the sibling catalog the Components and Recipes come from.
const flowEscapeHub = "../../../aggrega-hub/aggrega-catalog"

// flowEscapeAction is the template action that prints an opening double brace:
// the form every {{ of a definition is written in, inside an Aggregate.
const flowEscapeAction = `{{"{{"}}`

// flowEscapeCatalogPaths names every catalog entity the fixture's Releases
// resolve: the n8n and Flowise Components and both Recipes of each, the model
// Component they are wired to and its two Recipes, the schema library their
// values reference, the bundle both Flowise Recipes import to seed a chatflow,
// and the secret Feature and FeatureRecipe each Flowise target fills the owner
// password through. The provider Plugin entities are the fixture's own: the
// catalog keeps its Plugin entities under a directory named for the release
// version, which this fixture would otherwise have to follow at every release.
// Their guests resolve from the binary's embedded set.
func flowEscapeCatalogPaths() []string {
	hub := func(p string) string { return flowEscapeHub + "/" + p }
	return []string{
		flowEscapeDir + "/catalog",
		hub("component/core/ai-agent/n8n/1.0.0/component.yaml"),
		hub("component/core/ai-agent/flowise/1.0.0/component.yaml"),
		hub("component/core/ai-model/openai-compatible/1.0.0/component.yaml"),
		hub("recipe/helm/n8n/1.0.0/recipe.yaml"),
		hub("recipe/compose/n8n/1.0.0/recipe.yaml"),
		hub("recipe/helm/flowise/1.0.0/recipe.yaml"),
		hub("recipe/compose/flowise/1.0.0/recipe.yaml"),
		hub("recipe/helm/openai-compatible/1.0.0/recipe.yaml"),
		hub("recipe/compose/openai-compatible/1.0.0/recipe.yaml"),
		hub("artifactlibrary/flowise-flow-seed/1.0.0/library.yaml"),
		hub("schemalibrary/base-components/1.0.0/library.yaml"),
		hub("feature/core/referenced-secret/1.0.0/feature.yaml"),
		hub("feature/core/secret-value/1.0.0/feature.yaml"),
		hub("feature-recipe/helm/k8s-existing-secret/1.0.0/recipe.yaml"),
		hub("feature-recipe/compose/dotenv-secret/1.0.0/recipe.yaml"),
	}
}

// TestFlowDefinitionWithEscapedBracesReachesThePlatformAsExported builds each
// escaped Aggregate on each target its platform's Recipes render and reads the
// definition back out of the file the platform imports it from.
//
// What a failure here catches:
//   - a second template pass over a value on its way into a Recipe: the restored
//     {{ $json.body.message }} would be read as an action again, and the build
//     would halt or the bytes would change;
//   - any change between the Aggregate and the platform — an indentation, a
//     trimmed line end, an added escape — since the comparison is the whole
//     export, byte for byte;
//   - on compose, a definition whose $ is not doubled exactly once: Docker
//     Compose substitutes $NAME and ${NAME} anywhere in a config's content and
//     reads $$ as one $, so the content must be the export with every $ doubled.
//     Asserting that exact form is the same as reading every $$ back as $ and
//     finding the export, with no single $ left for Compose to substitute;
//   - the action ceasing to print the two braces, were the template surface the
//     Aggregate is read through ever to change.
func TestFlowDefinitionWithEscapedBracesReachesThePlatformAsExported(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		release   string
		aggregate string
		export    string
		// file is the end of the rendered file's path the platform imports from.
		file string
		// read takes the delivered definition out of that file.
		read func(t *testing.T, doc []byte) string
		// compose marks a delivery Docker Compose reads, which doubles every $.
		compose bool
	}{
		{
			name:      "n8n/helm",
			release:   "release-n8n-helm.yaml",
			aggregate: "aggregate-n8n.yaml",
			export:    "exports/n8n-workflow.json",
			file:      "/helm/automation/n8n-flow.configmap.yaml",
			read:      flowEscapeConfigMapData("workflow.json"),
		},
		{
			name:      "n8n/compose",
			release:   "release-n8n-compose.yaml",
			aggregate: "aggregate-n8n.yaml",
			export:    "exports/n8n-workflow.json",
			file:      "/compose/docker-compose.yaml",
			read:      flowEscapeComposeConfig("automation-flow"),
			compose:   true,
		},
		{
			name:      "flowise/helm",
			release:   "release-flowise-helm.yaml",
			aggregate: "aggregate-flowise.yaml",
			export:    "exports/flowise-chatflow.json",
			file:      "/helm/chat/flowise-flow/flowise-flow.yaml",
			read:      flowEscapeConfigMapData("flow.json"),
		},
		{
			name:      "flowise/compose",
			release:   "release-flowise-compose.yaml",
			aggregate: "aggregate-flowise.yaml",
			export:    "exports/flowise-chatflow.json",
			file:      "/compose/docker-compose.yaml",
			read:      flowEscapeComposeConfig("chat-flow"),
			compose:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			export := flowEscapeRead(t, tc.export)
			flowEscapeAssertWrittenEscaped(t, flowEscapeRead(t, tc.aggregate), export)

			result := pipelinetest.Build(t, pipelinetest.Input{
				ReleasePath:  filepath.Join(flowEscapeDir, tc.release),
				CatalogPaths: flowEscapeCatalogPaths(),
			})
			got := tc.read(t, flowEscapeArtifact(t, result.Artifacts, tc.file))

			want := export
			if tc.compose {
				want = strings.ReplaceAll(export, "$", "$$")
			}
			if got != want {
				t.Errorf("the definition the platform imports differs from the export (compose: %t)\n--- delivered ---\n%s\n--- want ---\n%s",
					tc.compose, got, want)
			}
		})
	}
}

// TestFlowDefinitionWrittenAsExportedHaltsAtTheAggregate is the other half: the
// same definitions written into an Aggregate as they stand halt the build with
// the invalid-parameter-reference condition, naming the Aggregate and the first
// token of the platform's expression the template engine could not read.
//
// What a failure here catches: the Aggregate's text ceasing to be read as a
// template, which would let a definition through unescaped and make the escaped
// form deliver its action verbatim; and a halt that no longer carries the
// condition, its short code, or what the author must find in their file.
func TestFlowDefinitionWrittenAsExportedHaltsAtTheAggregate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		release   string
		aggregate string
		// token is the variable the platform's first expression opens with,
		// which the template engine refuses as undefined.
		token string
	}{
		{name: "n8n", release: "release-n8n-as-exported.yaml", aggregate: "aggregate-n8n-as-exported.yaml", token: "$json"},
		{name: "flowise", release: "release-flowise-as-exported.yaml", aggregate: "aggregate-flowise-as-exported.yaml", token: "$vars"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result, err := pipelinetest.BuildE(t, pipelinetest.Input{
				ReleasePath:  filepath.Join(flowEscapeDir, tc.release),
				CatalogPaths: flowEscapeCatalogPaths(),
			})
			if err == nil {
				t.Fatalf("a definition carrying the platform's own double braces built unescaped; rendered %d file(s)",
					len(result.Artifacts))
			}
			if !errors.Is(err, resolver.ErrInvalidParameterRef()) {
				t.Fatalf("the halt does not carry the invalid-parameter-reference condition: %s", cerrors.CauseChain(err))
			}
			if short, ok := cerrors.ShortCodeOf(err); !ok || short != "E0158" {
				t.Errorf("short code = %q (present %t), want %q", short, ok, "E0158")
			}
			chain := cerrors.CauseChain(err)
			for _, want := range []string{tc.aggregate, "template parse error", "undefined variable", tc.token} {
				if !strings.Contains(chain, want) {
					t.Errorf("the halt does not carry %q: %s", want, chain)
				}
			}
		})
	}
}

// flowEscapeAssertWrittenEscaped holds the escaped Aggregate to the documented
// form: every {{ of the export written as the action, and no other action in the
// Aggregate's text. Checked so that a fixture rewritten another way — the
// definition read in through a parameter, say — cannot keep the proof above
// green without exercising the escape.
func flowEscapeAssertWrittenEscaped(t *testing.T, aggregate, export string) {
	t.Helper()
	opened := strings.Count(export, "{{")
	if opened == 0 {
		t.Fatal("the export carries no double brace, so this fixture proves nothing about the escape")
	}
	if got := strings.Count(aggregate, flowEscapeAction); got != opened {
		t.Errorf("the Aggregate writes %d escape action(s); the export opens %d double brace(s)", got, opened)
	}
	// The action itself opens two double braces, so an Aggregate whose only
	// actions are escapes opens exactly twice as many as it has escapes.
	if got, escapes := strings.Count(aggregate, "{{"), strings.Count(aggregate, flowEscapeAction); got != 2*escapes {
		t.Errorf("the Aggregate opens %d double brace(s) beyond its escapes", got-2*escapes)
	}
}

// flowEscapeRead reads a fixture file as text.
func flowEscapeRead(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(flowEscapeDir, name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(data)
}

// flowEscapeArtifact returns the one rendered file whose path ends with suffix.
// The directory a Release names leads every path, so the end of the path is what
// identifies the file a Recipe rendered.
func flowEscapeArtifact(t *testing.T, artifacts map[string][]byte, suffix string) []byte {
	t.Helper()
	var matched []string
	for path := range artifacts {
		if strings.HasSuffix(path, suffix) {
			matched = append(matched, path)
		}
	}
	if len(matched) != 1 {
		all := make([]string, 0, len(artifacts))
		for path := range artifacts {
			all = append(all, path)
		}
		sort.Strings(all)
		t.Fatalf("want one rendered file ending %q, found %d among %v", suffix, len(matched), all)
	}
	return artifacts[matched[0]]
}

// flowEscapeConfigMapData reads key out of a rendered ConfigMap's data.
func flowEscapeConfigMapData(key string) func(t *testing.T, doc []byte) string {
	return func(t *testing.T, doc []byte) string {
		t.Helper()
		var cm struct {
			Kind string            `yaml:"kind"`
			Data map[string]string `yaml:"data"`
		}
		if err := yaml.Unmarshal(doc, &cm); err != nil {
			t.Fatalf("the rendered ConfigMap does not parse: %v\n%s", err, doc)
		}
		if cm.Kind != "ConfigMap" {
			t.Fatalf("the rendered file is a %q, want a ConfigMap:\n%s", cm.Kind, doc)
		}
		value, ok := cm.Data[key]
		if !ok {
			t.Fatalf("the rendered ConfigMap carries no data key %q:\n%s", key, doc)
		}
		return value
	}
}

// flowEscapeComposeConfig reads the content of a rendered compose file's named
// top-level config.
func flowEscapeComposeConfig(name string) func(t *testing.T, doc []byte) string {
	return func(t *testing.T, doc []byte) string {
		t.Helper()
		var compose struct {
			Configs map[string]struct {
				Content *string `yaml:"content"`
			} `yaml:"configs"`
		}
		if err := yaml.Unmarshal(doc, &compose); err != nil {
			t.Fatalf("the rendered compose file does not parse: %v\n%s", err, doc)
		}
		config, ok := compose.Configs[name]
		if !ok || config.Content == nil {
			t.Fatalf("the rendered compose file carries no config %q with inline content:\n%s", name, doc)
		}
		return *config.Content
	}
}
