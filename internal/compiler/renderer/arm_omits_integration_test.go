// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer_test

import (
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// These tests drive an arm Recipe's omits key end to end through RenderAll: a
// compiled template fetched from a git repository, seeded in the cache as a pull
// leaves it, wrapped by the embedded arm guest as one nested deployment. The guest
// decides what an entry leaves out of the template and whether what remains still
// names it; the host evaluates the entries, hands them over, and halts on an entry
// that left nothing out.

// armOmits20Template bundles a log workspace beside the storage account the
// Release wants. It writes resources by symbolic name, as languageVersion 2.0 does.
const armOmits20Template = `{
  "$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
  "languageVersion": "2.0",
  "contentVersion": "1.0.0.0",
  "resources": {
    "store": {
      "type": "Microsoft.Storage/storageAccounts",
      "apiVersion": "2023-05-01",
      "name": "store",
      "location": "[resourceGroup().location]"
    },
    "logs": {
      "type": "Microsoft.OperationalInsights/workspaces",
      "apiVersion": "2023-09-01",
      "name": "store-logs",
      "location": "[resourceGroup().location]"
    }
  }
}
`

// armOmits10Template is the same pair written without a languageVersion, where
// resources are a list and an entry names a resource by its name property.
const armOmits10Template = `{
  "$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
  "contentVersion": "1.0.0.0",
  "resources": [
    {
      "type": "Microsoft.Storage/storageAccounts",
      "apiVersion": "2023-05-01",
      "name": "store",
      "location": "[resourceGroup().location]"
    },
    {
      "type": "Microsoft.OperationalInsights/workspaces",
      "apiVersion": "2023-09-01",
      "name": "store-logs",
      "location": "[resourceGroup().location]"
    }
  ]
}
`

// armNestedResourceNames returns what the nested deployment's inner template
// declares: the symbolic names of an object, or the name property of each
// resource of a list, in the order written.
func armNestedResourceNames(t *testing.T, files map[string]renderer.AssembledFile) []string {
	t.Helper()
	inner, _ := armNested(t, files)["template"].(map[string]any)
	switch resources := inner["resources"].(type) {
	case map[string]any:
		return slices.Sorted(maps.Keys(resources))
	case []any:
		var names []string
		for _, r := range resources {
			body, _ := r.(map[string]any)
			name, _ := body["name"].(string)
			names = append(names, name)
		}
		return names
	}
	t.Fatalf("the inner template declares no resources: %v", inner)
	return nil
}

// requireArmNotRefusedForTheContract fails the test when the build was refused
// because the arm provider's declared contract does not claim to honour omits,
// which is what a stale embedded guest produces.
func requireArmNotRefusedForTheContract(t *testing.T, err error) {
	t.Helper()
	if errors.Is(err, renderer.ErrUnsupportedOmits()) {
		t.Fatalf("the arm Recipe declaring omits was refused before it rendered, because the provider does "+
			"not claim to honour the key; is the embedded arm guest built from this tree?\n%s", cerrors.CauseChain(err))
	}
}

// armOmitsLeaveOut builds the template with and without an entry and asserts what
// the nested deployment's inner template declares in each build.
func armOmitsLeaveOut(t *testing.T, template string, entry types.OmitEntry, before, after []string) {
	t.Helper()
	repo := map[string]string{"main.json": template}
	control, err := renderArmSourced(t, armSourcedCase{files: repo})
	if err != nil {
		t.Fatalf("the build without the key: %s", cerrors.CauseChain(err))
	}
	if got := armNestedResourceNames(t, control); !slices.Equal(got, before) {
		t.Fatalf("the build without the key declares %v, want %v; the rows below read a template the source no longer holds", got, before)
	}

	c := armSourcedCase{files: repo, omits: []types.OmitEntry{entry}}
	once, err := renderArmSourced(t, c)
	requireArmNotRefusedForTheContract(t, err)
	if err != nil {
		t.Fatalf("the build leaving %q out: %s", entry.Name, cerrors.CauseChain(err))
	}
	if got := armNestedResourceNames(t, once); !slices.Equal(got, after) {
		t.Errorf("the build leaving %q out declares %v, want %v", entry.Name, got, after)
	}
	twice, err := renderArmSourced(t, c)
	if err != nil {
		t.Fatalf("the second build leaving %q out: %s", entry.Name, cerrors.CauseChain(err))
	}
	a, _ := json.Marshal(once)
	b, _ := json.Marshal(twice)
	if string(a) != string(b) {
		t.Errorf("two builds leaving %q out assembled different files", entry.Name)
	}
}

// TestRenderAll_ArmOmitsLeaveAResourceOutOfA20Template is the key's purpose read
// through the build for a template that writes resources by symbolic name: the
// entry names the log workspace by its type and symbolic name and the nested
// deployment's template keeps the storage account alone.
func TestRenderAll_ArmOmitsLeaveAResourceOutOfA20Template(t *testing.T) {
	t.Parallel()
	armOmitsLeaveOut(t, armOmits20Template,
		types.OmitEntry{Kind: "Microsoft.OperationalInsights/workspaces", Name: "logs"},
		[]string{"logs", "store"}, []string{"store"})
}

// TestRenderAll_ArmOmitsLeaveAResourceOutOfA10Template is the same for a template
// without a languageVersion, where the entry names the resource by its name
// property.
func TestRenderAll_ArmOmitsLeaveAResourceOutOfA10Template(t *testing.T) {
	t.Parallel()
	armOmitsLeaveOut(t, armOmits10Template,
		types.OmitEntry{Kind: "Microsoft.OperationalInsights/workspaces", Name: "store-logs"},
		[]string{"store", "store-logs"}, []string{"store"})
}

// TestRenderAll_ArmOmitsRemainingReferenceHalts holds the provider's halt on a
// template in which a remaining resource still names the one an entry left out: the
// storage account depends on the log workspace, so leaving the workspace out would
// assemble a template Resource Manager refuses, and the build stops with the
// provider's own sentence naming the template, the resource, the form and where it
// sits.
func TestRenderAll_ArmOmitsRemainingReferenceHalts(t *testing.T) {
	t.Parallel()

	source := strings.Replace(armOmits20Template, `"name": "store",`, `"name": "store",
      "dependsOn": ["logs"],`, 1)
	repo := map[string]string{"main.json": source}
	if _, err := renderArmSourced(t, armSourcedCase{files: repo}); err != nil {
		t.Fatalf("the template builds without the key, so the halt below is the reference's: %s", cerrors.CauseChain(err))
	}

	_, err := renderArmSourced(t, armSourcedCase{
		files: repo,
		omits: []types.OmitEntry{{Kind: "Microsoft.OperationalInsights/workspaces", Name: "logs"}},
	})
	requireArmNotRefusedForTheContract(t, err)
	chain := requireArmHalt(t, err, plugincondition.ErrPluginRequiredInputUnresolved())
	for _, want := range []string{
		"arm-provider:",
		`"logs"`,
		"/resources/store/dependsOn/0",
	} {
		if !strings.Contains(chain, want) {
			t.Errorf("the halt does not carry %q:\n%s", want, chain)
		}
	}
}

// TestRenderAll_ArmOmitsEntryThatLeftNothingOutHalts holds the host's halt on an
// entry the arm provider reported as having left nothing out, naming the entry's
// position and the kind and name it resolved to, and not the entry that did leave
// its resource out.
func TestRenderAll_ArmOmitsEntryThatLeftNothingOutHalts(t *testing.T) {
	t.Parallel()

	_, err := renderArmSourced(t, armSourcedCase{
		files: map[string]string{"main.json": armOmits20Template},
		omits: []types.OmitEntry{
			{Kind: "Microsoft.OperationalInsights/workspaces", Name: "logs"},
			{Kind: "Microsoft.OperationalInsights/workspaces", Name: "archive"},
		},
	})
	requireArmNotRefusedForTheContract(t, err)
	if !errors.Is(err, renderer.ErrOmitMatchedNothing()) {
		t.Fatalf("an entry naming a resource the template does not declare: got %v, want the halt on an entry that left nothing out", err)
	}
	chain := cerrors.CauseChain(err)
	for _, want := range []string{`"api-recipe" omits[1]`, `kind "Microsoft.OperationalInsights/workspaces"`, `name "archive"`} {
		if !strings.Contains(chain, want) {
			t.Errorf("the halt does not carry %q:\n%s", want, chain)
		}
	}
	if strings.Contains(chain, "omits[0]") {
		t.Errorf("the halt names the entry that left its resource out:\n%s", chain)
	}
}
