// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// End-to-end proof that an ARM template assembles every section its
// contributors declare, that a section no merge can decide is refused by name,
// and that a section the provider does not carry is reported to its author
// rather than dropped in silence.

package compiler_test

import (
	"errors"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

const (
	armSectionsDir     = "testdata/arm-template-sections/"
	armSectionsCatalog = armSectionsDir + "catalog"
)

func armSectionsInput(release string) pipelinetest.Input {
	return armInput(armSectionsDir+release, armSectionsCatalog)
}

// TestArmTemplateSectionsCarriedAndMerged builds two components whose fragments
// declare every section and asserts the assembled template carries each of them:
// the entries both components write the same way merge into one, the entries each
// wrote for itself sit side by side, and the outputs arrive under names scoped by
// the node that declared them.
func TestArmTemplateSectionsCarriedAndMerged(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, armSectionsInput("release.yaml"))
	template, raw := findArmTemplate(t, result.Artifacts, "stack")
	t.Logf("assembled template:\n%s", raw)

	if template["languageVersion"] != "2.0" || template["contentVersion"] != "1.0.0.0" {
		t.Errorf("the template is stamped languageVersion %#v and contentVersion %#v, want \"2.0\" and \"1.0.0.0\"",
			template["languageVersion"], template["contentVersion"])
	}
	if got, want := template["$schema"], "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#"; got != want {
		t.Errorf("the template's $schema is %#v, want %q", got, want)
	}

	// The same body merges into one entry; the check is on the name set, so a
	// merge that kept both copies under distinct names, or neither, shows.
	for section, want := range map[string][]string{
		"parameters":  {"tier"},
		"definitions": {"size"},
		"variables":   {"AlphaLabel", "BetaLabel"},
		"resources":   {"Alpha", "Beta"},
		"outputs":     {"alpha__label", "beta__label"},
	} {
		if got := sortedAnyKeys(armSection(t, template, section)); !equalStringSlice(got, want) {
			t.Errorf("section %s carries %v, want exactly %v\n--- template ---\n%s", section, got, want, raw)
		}
	}
	if note := armSection(t, template, "metadata")["note"]; note != "Written by the sections fixture." {
		t.Errorf("the template's metadata note is %#v, want the one both components wrote", note)
	}

	// Functions travel as a list of namespaces; both components declare the same
	// member, which arrives once.
	namespaces, ok := template["functions"].([]any)
	if !ok || len(namespaces) != 1 {
		t.Fatalf("functions = %#v, want one namespace\n--- template ---\n%s", template["functions"], raw)
	}
	namespace, _ := namespaces[0].(map[string]any)
	if namespace["namespace"] != "sections" {
		t.Errorf("the function namespace is %#v, want \"sections\"", namespace["namespace"])
	}
	if got := sortedAnyKeys(armSection(t, namespace, "members")); !equalStringSlice(got, []string{"describe"}) {
		t.Errorf("the namespace carries the members %v, want exactly [describe]", got)
	}
}

// TestArmCarriedSectionConflictHalts builds two components declaring one
// parameter with different defaults. One of the two defaults would be lost in
// silence, so the build halts as an output name collision naming the section,
// the parameter and both components.
func TestArmCarriedSectionConflictHalts(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, armSectionsInput("release-conflict.yaml"))
	if err == nil {
		t.Fatal("two components declaring one parameter with different defaults built cleanly")
	}
	if !errors.Is(err, plugincondition.ErrPluginOutputNameCollision()) {
		t.Fatalf("the build failed for a different reason than the conflicting parameter: %v", err)
	}
	msg := cerrors.CauseChain(err)
	for _, want := range []string{"arm-provider:", "parameters", `"tier"`, `"alpha"`, `"beta"`, "/parameters/tier"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not name %s: %s", want, msg)
		}
	}
}

// TestArmCaseOnlyCarriedNameHalts builds two components declaring parameters
// whose names differ only in case. ARM reads a parameter's name without regard
// to case, so the two would address one parameter; the build halts naming both
// spellings rather than emit a template that resolves one of them to the other.
func TestArmCaseOnlyCarriedNameHalts(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, armSectionsInput("release-case-only.yaml"))
	if err == nil {
		t.Fatal("two parameter names differing only in case built cleanly")
	}
	if !errors.Is(err, plugincondition.ErrPluginOutputNameCollision()) {
		t.Fatalf("the build failed for a different reason than the case-only name: %v", err)
	}
	msg := cerrors.CauseChain(err)
	for _, want := range []string{"arm-provider:", "parameters", `"Tier"`, `"tier"`, "without regard to case"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not name %s: %s", want, msg)
		}
	}
}

// TestArmApiProfileRefused builds a fragment that writes an API profile. The
// profile changes the apiVersion of every resource the template holds, so the
// provider refuses it by name as an input it cannot express.
func TestArmApiProfileRefused(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, armSectionsInput("release-api-profile.yaml"))
	if err == nil {
		t.Fatal("a fragment writing apiProfile built cleanly")
	}
	if !errors.Is(err, plugincondition.ErrPluginCannotExpressInput()) {
		t.Fatalf("the build failed for a different reason than the API profile: %v", err)
	}
	msg := cerrors.CauseChain(err)
	for _, want := range []string{"arm-provider:", "apiProfile", "write the apiVersion on each resource"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not say %q: %s", want, msg)
		}
	}
}

// TestArmLanguageVersionRefused builds a fragment that writes a language version
// other than the one the provider writes. Every resource is keyed by symbolic
// name in the version the provider stamps, so the fragment is refused by name.
func TestArmLanguageVersionRefused(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, armSectionsInput("release-language-version.yaml"))
	if err == nil {
		t.Fatal("a fragment writing languageVersion 1.0 built cleanly")
	}
	if !errors.Is(err, plugincondition.ErrPluginCannotExpressInput()) {
		t.Fatalf("the build failed for a different reason than the language version: %v", err)
	}
	msg := cerrors.CauseChain(err)
	for _, want := range []string{"arm-provider:", "languageVersion", `"2.0"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not say %q: %s", want, msg)
		}
	}
}

// TestArmUnknownSectionWarns builds a fragment that writes a top-level key no
// section accounts for. The build completes, the key is left out of the template,
// and its author is told through the shared unsupported-input warning, attributed
// to the node that wrote it.
func TestArmUnknownSectionWarns(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, armSectionsInput("release-unknown-section.yaml"))
	template, raw := findArmTemplate(t, result.Artifacts, "stack")
	if _, present := template["x-owner"]; present {
		t.Errorf("the template carries the unaccounted key x-owner\n--- template ---\n%s", raw)
	}

	reported := armAdvisoryBySourceRef(result.Advisories, "x-owner")
	if len(reported) == 0 {
		t.Fatalf("no diagnostic names the dropped key; got %+v", result.Advisories)
	}
	d := reported[0]
	if d.Severity != abi.SeverityWarn {
		t.Errorf("the diagnostic is %q-severity; it must be warn, because the template renders correctly without the key", d.Severity)
	}
	if d.Code != renderer.UnsupportedFieldCode {
		t.Errorf("the diagnostic carries the code %q, want the shared unsupported-input code %q so one suppress flag covers the class", d.Code, renderer.UnsupportedFieldCode)
	}
	if !strings.Contains(d.Message, "x-owner") {
		t.Errorf("the diagnostic does not name the key: %q", d.Message)
	}
	if d.Location == nil || d.Location.Instance != "alpha" {
		t.Errorf("the diagnostic is not attributed to the node that wrote the key; got %+v", d.Location)
	}
}

// TestArmTemplateSectionsAreDeterministic asserts carrying every section costs
// nothing in determinism: the same input assembles the same bytes across runs.
func TestArmTemplateSectionsAreDeterministic(t *testing.T) {
	pipelinetest.AssertDeterministic(t, armSectionsInput("release.yaml"))
}
