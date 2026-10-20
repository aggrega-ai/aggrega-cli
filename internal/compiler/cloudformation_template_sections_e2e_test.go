// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — end-to-end proof that a CloudFormation dispatch group
// assembles a stack template declaring every name it references, that a
// contributor's Transform is carried into the stack, and that the description no
// merge can fold is reported to its author rather than dropped in silence.

package compiler_test

import (
	"sort"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

const cfnSectionsCatalog = "testdata/cloudformation-template-sections/catalog/base"

// cfnSectionsInput is the build input for one of the template-sections fixture's
// Releases.
func cfnSectionsInput(releasePath string) pipelinetest.Input {
	return pipelinetest.Input{ReleasePath: releasePath, CatalogPaths: []string{cfnSectionsCatalog}}
}

// cfnStackReferences is every name an assembled stack refers to, grouped by the
// top-level section that has to declare it.
//
// It is collected by walking the emitted document rather than listed by hand,
// because the property under test is about the document as a whole: a hand-written
// list of four names would keep passing after a fifth reference appeared that
// nothing declares.
type cfnStackReferences struct {
	// refs are the targets of Ref, each of which the stack must declare as a
	// parameter or as a resource. Pseudo-parameters (the AWS:: namespace) are
	// supplied by the deployment and are excluded.
	refs []string
	// mappings are the map names Fn::FindInMap reads.
	mappings []string
	// conditions are the condition names a resource's Condition attribute names.
	conditions []string
}

// collectCfnStackReferences walks a decoded stack template and gathers the names
// it refers to. The Ref and Fn::FindInMap walk is recursive because an intrinsic
// can sit at any depth; the Condition attribute is read only at a resource body's
// top level, where it selects the resource, rather than anywhere the word appears
// (inside Conditions itself it is an intrinsic naming another condition).
func collectCfnStackReferences(t *testing.T, stack map[string]any) cfnStackReferences {
	t.Helper()
	var out cfnStackReferences
	var walk func(v any)
	walk = func(v any) {
		switch node := v.(type) {
		case map[string]any:
			for k, child := range node {
				switch k {
				case "Ref":
					if name, ok := child.(string); ok && !strings.HasPrefix(name, "AWS::") {
						out.refs = append(out.refs, name)
					}
				case "Fn::FindInMap":
					if args, ok := child.([]any); ok && len(args) > 0 {
						if name, ok := args[0].(string); ok {
							out.mappings = append(out.mappings, name)
						}
					}
				}
				walk(child)
			}
		case []any:
			for _, item := range node {
				walk(item)
			}
		}
	}
	walk(stack)

	if resources, ok := stack["Resources"].(map[string]any); ok {
		for id, raw := range resources {
			body, ok := raw.(map[string]any)
			if !ok {
				t.Fatalf("resource %q is not an object; got %#v", id, raw)
			}
			if name, ok := body["Condition"].(string); ok {
				out.conditions = append(out.conditions, name)
			}
		}
	}

	sort.Strings(out.refs)
	sort.Strings(out.mappings)
	sort.Strings(out.conditions)
	return out
}

// cfnSectionKeys returns the names one top-level section of a stack declares, and
// nil when the section is absent.
func cfnSectionKeys(stack map[string]any, section string) []string {
	entries, ok := stack[section].(map[string]any)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func cfnContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// TestCloudFormationTemplateSections_StackDeclaresEveryNameItReferences is the
// positive proof. A dispatch group whose contributors declare parameters,
// mappings, conditions, rules and metadata — with resources and outputs
// referencing the first three — assembles a stack that carries all five sections,
// so every name the emitted document refers to is a name the same document
// declares.
//
// 🔴 The reference sets are asserted to be NON-EMPTY before they are checked
// against the declarations. A walk that found no references would otherwise
// satisfy "every reference resolves" vacuously, which is exactly the state this
// fixture exists to distinguish from the fixed one.
func TestCloudFormationTemplateSections_StackDeclaresEveryNameItReferences(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, cfnSectionsInput("testdata/cloudformation-template-sections/release.yaml"))
	stack, raw := findCfnStack(t, result.Artifacts, "stack")

	refs := collectCfnStackReferences(t, stack)
	if len(refs.refs) == 0 || len(refs.mappings) == 0 || len(refs.conditions) == 0 {
		t.Fatalf("the emitted stack refers to no parameter, no mapping or no condition, so this test would pass vacuously: %+v\n--- stack ---\n%s", refs, raw)
	}

	parameters := cfnSectionKeys(stack, "Parameters")
	resources := cfnSectionKeys(stack, "Resources")
	for _, name := range refs.refs {
		if !cfnContains(parameters, name) && !cfnContains(resources, name) {
			t.Errorf("the stack refers to %q, which it declares neither as a parameter (%v) nor as a resource (%v)\n--- stack ---\n%s",
				name, parameters, resources, raw)
		}
	}
	mappings := cfnSectionKeys(stack, "Mappings")
	for _, name := range refs.mappings {
		if !cfnContains(mappings, name) {
			t.Errorf("the stack reads the map %q, which it does not declare; Mappings = %v\n--- stack ---\n%s", name, mappings, raw)
		}
	}
	conditions := cfnSectionKeys(stack, "Conditions")
	for _, name := range refs.conditions {
		if !cfnContains(conditions, name) {
			t.Errorf("the stack selects on the condition %q, which it does not declare; Conditions = %v\n--- stack ---\n%s", name, conditions, raw)
		}
	}

	// The two sections carrying no reference are carried on the same terms: each is
	// a map of names its author chose, so each lands under those names.
	if got := cfnSectionKeys(stack, "Rules"); !cfnContains(got, "InstanceClassIsOffered") {
		t.Errorf("the stack does not carry the authored rule; Rules = %v\n--- stack ---\n%s", got, raw)
	}
	if got := cfnSectionKeys(stack, "Metadata"); !cfnContains(got, "StoreDocumentation") {
		t.Errorf("the stack does not carry the authored metadata; Metadata = %v\n--- stack ---\n%s", got, raw)
	}
}

// TestCloudFormationTemplateSections_UnfoldableSectionsAreNamedNotDropped proves
// the other half of the accounting: a template-wide description stays out of the
// emitted bytes and its author is told through the out-of-band class rather than
// left to find out at deploy time, while the one contributor's transform — beside
// contributors declaring no serverless content — is carried into the stack and
// reported to nobody.
func TestCloudFormationTemplateSections_UnfoldableSectionsAreNamedNotDropped(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, cfnSectionsInput("testdata/cloudformation-template-sections/release.yaml"))
	stack, raw := findCfnStack(t, result.Artifacts, "stack")

	if _, present := stack["Description"]; present {
		t.Errorf("the assembled stack carries a top-level Description, which no merge across contributors can decide\n--- stack ---\n%s", raw)
	}
	if got := stack["Transform"]; got != "AWS::LanguageExtensions" {
		t.Errorf("the assembled stack's Transform is %#v, want the one contributor's \"AWS::LanguageExtensions\"\n--- stack ---\n%s", got, raw)
	}

	byRef := map[string]renderer.Diagnostic{}
	for _, d := range result.Advisories {
		byRef[d.SourceRef] = d
	}
	if d, reported := byRef["Transform"]; reported {
		t.Errorf("a diagnostic reports the Transform, which the stack carries; got %+v", d)
	}
	for _, section := range []string{"Description"} {
		d, ok := byRef[section]
		if !ok {
			t.Errorf("no diagnostic names the dropped %s section; got %+v", section, result.Advisories)
			continue
		}
		if d.Severity != abi.SeverityWarn {
			t.Errorf("the %s diagnostic is %q-severity; it must be warn, because a template carrying one renders correctly without it", section, d.Severity)
		}
		if d.Code != renderer.UnsupportedFieldCode {
			t.Errorf("the %s diagnostic carries the code %q, want the shared unsupported-input code %q so one suppress flag covers the whole class", section, d.Code, renderer.UnsupportedFieldCode)
		}
		if !strings.Contains(d.Message, section) {
			t.Errorf("the %s diagnostic does not name the section: %q", section, d.Message)
		}
		if d.Location == nil || d.Location.URI != "store" {
			t.Errorf("the %s diagnostic is not attributed to the node that authored it; got %+v", section, d.Location)
		}
	}
}

// TestCloudFormationTemplateSections_CollisionInBothDirections proves the
// per-key-space collision policy end to end, both ways: two components declaring
// one parameter with the same body build and reach one entry, and with different
// bodies the build halts naming the section, the parameter and both components.
//
// 🔴 Both legs are required together. The merge leg alone cannot tell a policy from
// a silent last-writer-wins, and the halt leg alone cannot tell a policy from a
// refusal of every repeat.
func TestCloudFormationTemplateSections_CollisionInBothDirections(t *testing.T) {
	t.Parallel()

	t.Run("the same body merges", func(t *testing.T) {
		t.Parallel()
		result := pipelinetest.Build(t, cfnSectionsInput("testdata/cloudformation-template-sections/release-agreeing.yaml"))
		stack, raw := findCfnStack(t, result.Artifacts, "stack")
		if got := cfnSectionKeys(stack, "Parameters"); len(got) != 1 || got[0] != "SharedTier" {
			t.Errorf("the merged Parameters section carries %v, want exactly [SharedTier]\n--- stack ---\n%s", got, raw)
		}
		// Both contributors' resources are present, so the merge folded the parameter
		// without dropping either component.
		if got := cfnSectionKeys(stack, "Resources"); len(got) != 2 {
			t.Errorf("the merged stack carries the resources %v, want both contributors'\n--- stack ---\n%s", got, raw)
		}
	})

	t.Run("a different body halts naming both contributors", func(t *testing.T) {
		t.Parallel()
		_, err := pipelinetest.BuildE(t, cfnSectionsInput("testdata/cloudformation-template-sections/release-conflicting.yaml"))
		if err == nil {
			t.Fatal("two components declaring one parameter with different bodies must halt; one of the two defaults would otherwise be lost in silence")
		}
		// The halt's own text sits beneath the render frame, which renders one frame
		// per line by design, so the assertion reads the flattened cause chain — the
		// same text the command surface prints under "caused by".
		msg := cerrors.CauseChain(err)
		for _, want := range []string{"Parameters", "SharedTier", "tier", "tier-rival"} {
			if !strings.Contains(msg, want) {
				t.Errorf("the halt does not name %q; got %q", want, msg)
			}
		}
	})
}

// TestCloudFormationTemplateSections_Deterministic asserts the carried sections do
// not cost the Deterministic Output invariant: the same input assembles the same
// bytes across repeated runs.
func TestCloudFormationTemplateSections_Deterministic(t *testing.T) {
	pipelinetest.AssertDeterministic(t, cfnSectionsInput("testdata/cloudformation-template-sections/release.yaml"))
}

// cfnSectionsCarriedByThisFixture is the number of top-level sections the
// template-sections fixture authors across its two components and the stack
// carries. It exists so the assertion below states what it counted rather than a
// bare number.
const cfnSectionsCarriedByThisFixture = 6

// TestCloudFormationTemplateSections_EverySectionTheFixtureAuthorsIsAccountedFor
// closes the loop between the fixture and the accounting: the fixture authors
// seven top-level sections beyond the resources and outputs, six of which must
// appear in the emitted stack and one of which must appear as a diagnostic. Nothing
// it authors may go unreported and unemitted.
func TestCloudFormationTemplateSections_EverySectionTheFixtureAuthorsIsAccountedFor(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, cfnSectionsInput("testdata/cloudformation-template-sections/release.yaml"))
	stack, raw := findCfnStack(t, result.Artifacts, "stack")

	authoredAndCarried := []string{"Conditions", "Mappings", "Metadata", "Parameters", "Rules", "Transform"}
	if len(authoredAndCarried) != cfnSectionsCarriedByThisFixture {
		t.Fatalf("the fixture's carried-section list has %d entries, want %d", len(authoredAndCarried), cfnSectionsCarriedByThisFixture)
	}
	missing := make([]string, 0, len(authoredAndCarried))
	for _, section := range authoredAndCarried {
		if _, present := stack[section]; !present {
			missing = append(missing, section)
		}
	}
	if len(missing) > 0 {
		t.Errorf("the fixture authors %v and the assembled stack carries none of them\n--- stack ---\n%s", missing, raw)
	}

	authoredAndNamed := []string{"Description"}
	reported := map[string]bool{}
	for _, d := range result.Advisories {
		reported[d.SourceRef] = true
	}
	for _, section := range authoredAndNamed {
		if _, present := stack[section]; present {
			t.Errorf("the assembled stack carries %s", section)
		}
		if !reported[section] {
			t.Errorf("the fixture authors %s and nothing reported it; diagnostics = %+v", section, result.Advisories)
		}
	}
}
