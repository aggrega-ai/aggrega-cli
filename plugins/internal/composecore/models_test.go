// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package composecore

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
)

// modelFragment is one contributor's compose fragment, authored as text so the
// test exercises the parse path a sourced compose file or an artifacts: body
// arrives through.
func modelFragment(node, body string) renderer.EmittedFile {
	return renderer.EmittedFile{
		Name:         node + ".compose-fragment.yaml",
		Origin:       renderer.EmitOriginArtifactsInline,
		Body:         []byte(body),
		Intermediate: true,
	}
}

// assembleOne runs boundary assembly over a single contributing node.
func assembleOne(t *testing.T, frag renderer.EmittedFile) (string, error) {
	t.Helper()
	out, _, err := AssembleBoundaryCore(
		map[string][]renderer.EmittedFile{"alpha": {frag}},
		map[string]map[string]string{"alpha": {}},
		nil, nil, "default", 2,
	)
	if err != nil {
		return "", err
	}
	return string(out[AssembledOutputFile]), nil
}

// longFormModels is a fragment declaring one model and granting it to a service
// through the long form, which names the variables the platform fills.
const longFormModels = `services:
  chat:
    image: app:1.0
    models:
      llm:
        endpoint_var: AI_URL
        model_var: AI_MODEL
models:
  llm:
    model: ai/smollm2
    context_size: 4096
`

// TestComposeAssemble_ModelsReachTheAssembledFile covers the decode and the
// emission: a fragment's top-level models: entry and a service's long-form
// models: reference both reach the assembled file, written as the fragment wrote
// them.
func TestComposeAssemble_ModelsReachTheAssembledFile(t *testing.T) {
	t.Parallel()

	assembled, err := assembleOne(t, modelFragment("alpha", longFormModels))
	if err != nil {
		t.Fatalf("assemble refused a fragment declaring the model it references: %v", err)
	}
	for _, want := range []string{
		"\nmodels:\n  llm:\n    model: ai/smollm2\n    context_size: 4096\n",
		"    models:\n      llm:\n        endpoint_var: AI_URL\n        model_var: AI_MODEL\n",
	} {
		if !strings.Contains(assembled, want) {
			t.Errorf("assembled file does not carry %q:\n%s", want, assembled)
		}
	}
}

// TestComposeAssemble_ModelBodyCarriesEveryMember pins that a model's body is
// carried whole — the members Compose names and any other, an x- extension
// among them — and in a fixed order: the named members first, then the rest by
// key, whatever order the fragment wrote them in.
func TestComposeAssemble_ModelBodyCarriesEveryMember(t *testing.T) {
	t.Parallel()

	assembled, err := assembleOne(t, modelFragment("alpha", `services:
  chat:
    image: app:1.0
    models:
      - llm
models:
  llm:
    x-zeta: last
    runtime_flags:
      - --temp
      - "0.2"
    x-alpha:
      depth: 1
    context_size: 4096
    model: ai/smollm2
`))
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	const want = "\nmodels:\n  llm:\n    model: ai/smollm2\n    context_size: 4096\n" +
		"    runtime_flags:\n      - --temp\n      - \"0.2\"\n" +
		"    x-alpha:\n      depth: 1\n    x-zeta: last\n"
	if !strings.Contains(assembled, want) {
		t.Errorf("model body not carried whole in its fixed order; want %q in:\n%s", want, assembled)
	}
}

// TestComposeAssemble_ServiceModelsKeepTheirForm pins the round trip of a
// service's reference in both of Compose's forms: a short list stays a list, a
// long mapping stays a mapping, and either keeps the order the fragment listed
// its models in.
func TestComposeAssemble_ServiceModelsKeepTheirForm(t *testing.T) {
	t.Parallel()

	declared := "models:\n  zeta: {model: ai/zeta}\n  alpha: {model: ai/alpha}\n"
	for _, tc := range []struct {
		name, reference, want string
	}{
		{
			name:      "short",
			reference: "    models:\n      - zeta\n      - alpha\n",
			want:      "    models:\n      - zeta\n      - alpha\n",
		},
		{
			name:      "long",
			reference: "    models:\n      zeta:\n        model_var: ZETA_MODEL\n      alpha: {}\n",
			want:      "    models:\n      zeta:\n        model_var: ZETA_MODEL\n      alpha: {}\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assembled, err := assembleOne(t, modelFragment("alpha",
				"services:\n  chat:\n    image: app:1.0\n"+tc.reference+declared))
			if err != nil {
				t.Fatalf("assemble: %v", err)
			}
			if !strings.Contains(assembled, tc.want) {
				t.Errorf("service reference did not keep its form; want %q in:\n%s", tc.want, assembled)
			}
		})
	}
}

// TestComposeAssemble_IdenticalModelsMerge covers two contributors declaring one
// model name with bodies that emit identically — here written with their members
// in different orders. A model is one model however many services use it, so the
// build succeeds with one entry under that name, and each contributor's service
// keeps its own reference.
func TestComposeAssemble_IdenticalModelsMerge(t *testing.T) {
	t.Parallel()

	out, err := assembleTwo(
		modelFragment("alpha", "services:\n  alpha:\n    image: app:1.0\n    models: [llm]\n"+
			"models:\n  llm:\n    model: ai/smollm2\n    context_size: 4096\n"),
		modelFragment("zeta", "services:\n  zeta:\n    image: app:1.0\n    models: [llm]\n"+
			"models:\n  llm:\n    context_size: 4096\n    model: ai/smollm2\n"),
	)
	if err != nil {
		t.Fatalf("assemble refused two identical model declarations: %v", err)
	}
	assembled := string(out[AssembledOutputFile])
	if got := strings.Count(assembled, "\n  llm:\n"); got != 1 {
		t.Errorf("model emitted %d times, want exactly one entry:\n%s", got, assembled)
	}
	if got := strings.Count(assembled, "    models:\n      - llm\n"); got != 2 {
		t.Errorf("%d services carry their reference, want both:\n%s", got, assembled)
	}
}

// TestComposeAssemble_ConflictingModelsHalt covers two contributors declaring one
// model name with bodies that disagree. Keeping either body discards the other
// contributor's model from a build that would otherwise succeed, so it halts
// under the condition the volume and config conflicts raise, naming the model
// and both parties, each with the fragment it declared the model in. The whole
// message is pinned, because it is worded by the reporter the other sections
// share.
func TestComposeAssemble_ConflictingModelsHalt(t *testing.T) {
	t.Parallel()

	_, err := assembleTwo(
		modelFragment("alpha", "services:\n  alpha:\n    image: app:1.0\n"+
			"models:\n  llm:\n    model: ai/smollm2\n"),
		modelFragment("zeta", "services:\n  zeta:\n    image: app:1.0\n"+
			"models:\n  llm:\n    model: ai/gemma3\n"),
	)
	if err == nil {
		t.Fatal("assemble accepted two contributors disagreeing about one model name; want a halt")
	}
	const want = `[TRANSLATION_ERROR] compose AssembleBoundary: ` +
		`conflicting model declarations across contributors: ` +
		`model "llm" contributed by [boundary="default" node="alpha" fragment="alpha.compose-fragment.yaml", ` +
		`boundary="default" node="zeta" fragment="zeta.compose-fragment.yaml"]; ` +
		`two contributors declaring one model name must declare it identically, or one of them must declare a different name`
	if err.Error() != want {
		t.Errorf("model conflict message:\n want: %s\n  got: %s", want, err.Error())
	}
}

// TestComposeAssemble_UndeclaredModelReferenceHalts proves the reference check is
// reachable: a service granted a model nothing at its assembly path declares
// halts, under the unresolved-required-input condition the configs: check
// raises, naming the service and the model.
func TestComposeAssemble_UndeclaredModelReferenceHalts(t *testing.T) {
	t.Parallel()

	_, err := assembleOne(t, modelFragment("alpha",
		"services:\n  chat:\n    image: app:1.0\n    models:\n      ghost:\n        endpoint_var: GHOST_URL\n"))
	if err == nil {
		t.Fatal("assemble accepted a reference to a model nothing declares; want a halt")
	}
	if !errors.Is(err, plugincondition.ErrPluginRequiredInputUnresolved()) {
		t.Fatalf("error = %v, want it to carry the unresolved-required-input condition", err)
	}
	for _, want := range []string{
		`service "chat" references model "ghost"`,
		"declare each one as a top-level models: entry",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err.Error(), want)
		}
	}
}

// TestComposeAssemble_ModelDeclaredByAnotherNodeResolves pins why the reference
// check runs over the assembled boundary: one node declares the model and a
// different node's service is granted it.
func TestComposeAssemble_ModelDeclaredByAnotherNodeResolves(t *testing.T) {
	t.Parallel()

	out, err := assembleTwo(
		modelFragment("alpha", "models:\n  llm:\n    model: ai/smollm2\n"),
		modelFragment("zeta", "services:\n  zeta:\n    image: app:1.0\n    models: [llm]\n"),
	)
	if err != nil {
		t.Fatalf("assemble rejected a model declared by a sibling node: %v", err)
	}
	if !strings.Contains(string(out[AssembledOutputFile]), "\nmodels:\n  llm:\n    model: ai/smollm2\n") {
		t.Errorf("assembled output lost the declaring node's model:\n%s", out[AssembledOutputFile])
	}
}

// TestComposeAssemble_UnresolvedModelsReportedDeterministically asserts every
// unresolved reference is named, services in name order, each service's
// references in the order its fragment listed them, the same on every run.
func TestComposeAssemble_UnresolvedModelsReportedDeterministically(t *testing.T) {
	t.Parallel()

	var first string
	for range 8 {
		_, err := assembleTwo(
			modelFragment("alpha", "services:\n  alpha:\n    image: app:1.0\n    models: [zeta-model, alpha-model]\n"),
			modelFragment("zeta", "services:\n  zeta:\n    image: app:1.0\n    models: [omega-model]\n"),
		)
		if err == nil {
			t.Fatal("assemble accepted three unresolved model references; want a halt")
		}
		if first == "" {
			first = err.Error()
			continue
		}
		if err.Error() != first {
			t.Fatalf("halt message is not stable across runs:\n first: %s\nlater: %s", first, err.Error())
		}
	}
	const want = `service "alpha" references model "zeta-model"; ` +
		`service "alpha" references model "alpha-model"; ` +
		`service "zeta" references model "omega-model"`
	if !strings.Contains(first, want) {
		t.Errorf("unresolved references not reported in order; want %q in:\n%s", want, first)
	}
}

// TestComposeAssemble_ModelsEmitByteIdentically pins the determinism of the
// emitted models: several models, several extension members and two forms of
// reference, assembled repeatedly, produce one byte sequence, with the models
// written in name order.
func TestComposeAssemble_ModelsEmitByteIdentically(t *testing.T) {
	t.Parallel()

	body := `services:
  chat:
    image: app:1.0
    models:
      zeta: {endpoint_var: Z_URL, x-b: 2, x-a: 1}
      alpha: {}
models:
  zeta: {model: ai/zeta, x-two: 2, x-one: 1}
  mid: {model: ai/mid}
  alpha: {model: ai/alpha, context_size: 512}
`
	var first string
	for range 20 {
		assembled, err := assembleOne(t, modelFragment("alpha", body))
		if err != nil {
			t.Fatalf("assemble: %v", err)
		}
		if first == "" {
			first = assembled
			continue
		}
		if assembled != first {
			t.Fatalf("assembled file is not byte-identical across runs:\n first:\n%s\nlater:\n%s", first, assembled)
		}
	}
	alpha := strings.Index(first, "\n  alpha:\n    model: ai/alpha")
	mid := strings.Index(first, "\n  mid:\n    model: ai/mid")
	zeta := strings.Index(first, "\n  zeta:\n    model: ai/zeta")
	if alpha < 0 || mid < 0 || zeta < 0 || !(alpha < mid && mid < zeta) {
		t.Errorf("models are not emitted in name order:\n%s", first)
	}
}

// TestRenderSourcedCore_CarriesModels covers the sourced path end to end inside
// the provider: a compose source file's top-level models: section and a
// service's reference survive the sourced render into the fragment, and that
// fragment assembles into a file carrying both.
func TestRenderSourcedCore_CarriesModels(t *testing.T) {
	t.Parallel()

	source := `services:
  app:
    image: example/chat:1
    models:
      llm:
        endpoint_var: AI_URL
        model_var: AI_MODEL
models:
  llm:
    model: ai/smollm2
    context_size: 4096
`
	rendered, err := RenderSourcedCore([]byte(source), "compose.yaml", nil, nodeCtxFor("app"))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	frag, ok := rendered.Files["app.compose-fragment.yaml"]
	if !ok {
		t.Fatalf("want the one fragment app.compose-fragment.yaml, got %d file(s)", len(rendered.Files))
	}
	assembled, err := assembleOne(t, renderer.EmittedFile{
		Name: "app.compose-fragment.yaml", Origin: renderer.EmitOriginSource, Body: frag, Intermediate: true,
	})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	for _, want := range []string{
		"\nmodels:\n  llm:\n    model: ai/smollm2\n    context_size: 4096\n",
		"    models:\n      llm:\n        endpoint_var: AI_URL\n        model_var: AI_MODEL\n",
	} {
		if !strings.Contains(assembled, want) {
			t.Errorf("assembled file does not carry %q:\n%s", want, assembled)
		}
	}
}
