// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const (
	// subscriptionSchemaLine opens a fragment written for a subscription.
	subscriptionSchemaLine = "$schema: " + SchemaSubscription + "\n"
	// managementGroupSchemaLine opens a fragment written for a management group.
	managementGroupSchemaLine = "$schema: " + SchemaManagementGroup + "\n"
	// levelGroup is the resource group the fixtures' resource group content is
	// deployed into.
	levelGroup = "orders-rg"
)

// atScope is c with its merged annotations set to the given pairs of key and
// value.
func atScope(c Contribution, pairs ...string) Contribution {
	c.Annotations = map[string]string{}
	for i := 0; i+1 < len(pairs); i += 2 {
		c.Annotations[pairs[i]] = pairs[i+1]
	}
	return c
}

// subscriptionBoundary is c in a boundary deployed at subscription scope whose
// resource group content lands in levelGroup.
func subscriptionBoundary(c Contribution) Contribution {
	return atScope(c, AnnotationDeploymentScope, string(ScopeSubscription), AnnotationResourceGroup, levelGroup)
}

// groupFragment is a subscription fragment declaring the resource group named
// name under the symbolic name Group.
func groupFragment(name string) string {
	return subscriptionSchemaLine + "resources:\n  Group:\n    type: Microsoft.Resources/resourceGroups\n    apiVersion: '2024-03-01'\n    name: " + name + "\n    location: '[deployment().location]'\n"
}

// levelDeploymentOf returns the resource group level deployment of an assembled template.
func levelDeploymentOf(t *testing.T, template map[string]any) map[string]any {
	t.Helper()
	resources, _ := template[sectionResources].(map[string]any)
	level, ok := resources[LevelResourceGroupSymbol].(map[string]any)
	if !ok {
		t.Fatalf("the template holds no %s; resources = %v", LevelResourceGroupSymbol, resources)
	}
	return level
}

// levelTemplateOf returns the template the resource group level deployment
// carries.
func levelTemplateOf(t *testing.T, template map[string]any) map[string]any {
	t.Helper()
	properties, _ := levelDeploymentOf(t, template)["properties"].(map[string]any)
	inner, ok := properties["template"].(map[string]any)
	if !ok {
		t.Fatalf("the level carries no template: %v", properties)
	}
	return inner
}

// keysOf returns the sorted keys of a template section.
func keysOf(m any) []string {
	obj, _ := m.(map[string]any)
	return sortedKeys(obj)
}

func TestBoundaryScope(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name        string
		annotations map[string]string
		want        Scope
	}{
		{"absent", nil, ScopeResourceGroup},
		{"empty", map[string]string{AnnotationDeploymentScope: ""}, ScopeResourceGroup},
		{"resource group", map[string]string{AnnotationDeploymentScope: "resourceGroup"}, ScopeResourceGroup},
		{"subscription", map[string]string{AnnotationDeploymentScope: "subscription"}, ScopeSubscription},
		{"management group", map[string]string{AnnotationDeploymentScope: "managementGroup"}, ScopeManagementGroup},
		{"other annotations are not read", map[string]string{AnnotationResourceGroup: "subscription"}, ScopeResourceGroup},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := BoundaryScope(c.annotations)
			if err != nil || got != c.want {
				t.Errorf("BoundaryScope(%v) = %q, %v; want %q", c.annotations, got, err, c.want)
			}
		})
	}

	t.Run("a value outside the three", func(t *testing.T) {
		t.Parallel()
		for _, value := range []string{"Subscription", "tenant", "rg-secret-name"} {
			_, err := BoundaryScope(map[string]string{AnnotationDeploymentScope: value})
			if !errors.Is(err, plugincondition.ErrPluginRequiredInputUnresolved()) {
				t.Fatalf("value %q: err = %v, want a required input unresolved", value, err)
			}
			for _, want := range []string{"arm-provider:", AnnotationDeploymentScope, "resourceGroup", "subscription", "managementGroup"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message %q does not contain %q", err, want)
				}
			}
			if strings.Contains(err.Error(), value) {
				t.Errorf("message %q quotes the value", err)
			}
		}
	})

	t.Run("contributors disagreeing", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct {
			name   string
			second Contribution
		}{
			{"two values", atScope(contributes("orders-api", 0, storage("Api")), AnnotationDeploymentScope, "resourceGroup")},
			{"one absent", contributes("orders-api", 0, storage("Api"))},
		} {
			_, err := Assemble([]Contribution{
				atScope(contributes("payments-db", 0, storage("Db")), AnnotationDeploymentScope, "subscription", AnnotationResourceGroup, levelGroup),
				c.second,
			})
			if !errors.Is(err, plugincondition.ErrPluginRequiredInputUnresolved()) {
				t.Fatalf("%s: err = %v, want a required input unresolved", c.name, err)
			}
			for _, want := range []string{"arm-provider:", `"payments-db"`, `"orders-api"`, AnnotationDeploymentScope} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%s: message %q does not contain %q", c.name, err, want)
				}
			}
		}
	})

	t.Run("a value outside the three halts the assembly naming the contributor", func(t *testing.T) {
		t.Parallel()
		_, err := Assemble([]Contribution{atScope(contributes("payments-db", 0, storage("Db")), AnnotationDeploymentScope, "tenant")})
		if !errors.Is(err, plugincondition.ErrPluginRequiredInputUnresolved()) {
			t.Fatalf("err = %v, want a required input unresolved", err)
		}
		if !strings.Contains(err.Error(), `"payments-db"`) {
			t.Errorf("message %q does not name the contributor", err)
		}
	})
}

func TestScopeSubscriptionNestsResourceGroupContent(t *testing.T) {
	t.Parallel()
	m, _ := assembled(t,
		subscriptionBoundary(contributes("group", 0, groupFragment("other-rg"))),
		subscriptionBoundary(contributes("payments-db", 0, storage("Db"))),
		subscriptionBoundary(contributes("orders-api", 0, storage("Api"))),
	)
	if m[sectionSchema] != SchemaSubscription || m[sectionLanguageVersion] != LanguageVersion || m[sectionContentVersion] != ContentVersion {
		t.Errorf("stamped top level = %v %v %v", m[sectionSchema], m[sectionLanguageVersion], m[sectionContentVersion])
	}
	if got, want := keysOf(m[sectionResources]), []string{"Group", LevelResourceGroupSymbol}; !reflect.DeepEqual(got, want) {
		t.Errorf("top-level resources = %v, want %v", got, want)
	}
	level := levelDeploymentOf(t, m)
	for key, want := range map[string]any{
		"type":          "Microsoft.Resources/deployments",
		"apiVersion":    DeploymentsAPIVersion,
		"name":          LevelHistoryName(ScopeResourceGroup),
		"resourceGroup": levelGroup,
	} {
		if level[key] != want {
			t.Errorf("level %s = %v, want %v", key, level[key], want)
		}
	}
	if _, present := level["dependsOn"]; present {
		t.Errorf("the level depends on %v, but no resource group named %s is declared", level["dependsOn"], levelGroup)
	}
	properties, _ := level["properties"].(map[string]any)
	if properties["mode"] != "Incremental" {
		t.Errorf("mode = %v", properties["mode"])
	}
	if eval, _ := properties["expressionEvaluationOptions"].(map[string]any); eval["scope"] != "inner" {
		t.Errorf("expressionEvaluationOptions = %v", properties["expressionEvaluationOptions"])
	}
	if _, present := properties["parameters"]; present {
		t.Errorf("parameters passed with none declared: %v", properties["parameters"])
	}
	inner := levelTemplateOf(t, m)
	if inner[sectionSchema] != SchemaResourceGroup || inner[sectionLanguageVersion] != LanguageVersion || inner[sectionContentVersion] != ContentVersion {
		t.Errorf("stamped level = %v %v %v", inner[sectionSchema], inner[sectionLanguageVersion], inner[sectionContentVersion])
	}
	if got, want := keysOf(inner[sectionResources]), []string{"Api", "Db"}; !reflect.DeepEqual(got, want) {
		t.Errorf("level resources = %v, want %v", got, want)
	}
}

func TestScopeSubscriptionWithoutResourceGroupContentHasNoLevel(t *testing.T) {
	t.Parallel()
	// No resource group content: no level, and no resource group to name.
	m, _ := assembled(t, atScope(contributes("group", 0, groupFragment(levelGroup)), AnnotationDeploymentScope, "subscription"))
	if got, want := keysOf(m[sectionResources]), []string{"Group"}; !reflect.DeepEqual(got, want) {
		t.Errorf("resources = %v, want %v", got, want)
	}
}

func TestScopeLevelDependsOnItsGroup(t *testing.T) {
	t.Parallel()
	existing := subscriptionSchemaLine + "resources:\n  Known:\n    type: Microsoft.Resources/resourceGroups\n    apiVersion: '2024-03-01'\n    name: " + levelGroup + "\n    existing: true\n"
	expression := subscriptionSchemaLine + "resources:\n  Computed:\n    type: microsoft.resources/RESOURCEGROUPS\n    apiVersion: '2024-03-01'\n    name: \"[format('{0}', '" + levelGroup + "')]\"\n    location: westeurope\n"
	cases := []struct {
		name  string
		group string
		want  []any
	}{
		{"named by the literal", groupFragment(levelGroup), []any{"Group"}},
		{"another name", groupFragment("other-rg"), nil},
		{"declared existing", existing, nil},
		{"named by an expression", expression, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			m, _ := assembled(t,
				subscriptionBoundary(contributes("group", 0, c.group)),
				subscriptionBoundary(contributes("payments-db", 0, storage("Db"))),
			)
			got, _ := levelDeploymentOf(t, m)["dependsOn"].([]any)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("dependsOn = %v, want %v", got, c.want)
			}
		})
	}
}

func TestScopeParametersPassThroughToTheLevel(t *testing.T) {
	t.Parallel()
	declares := "parameters:\n  tier:\n    type: string\n    defaultValue: small\n" + storage("Db")
	m, _ := assembled(t,
		subscriptionBoundary(contributes("group", 0, groupFragment(levelGroup))),
		subscriptionBoundary(contributes("payments-db", 0, declares)),
	)
	body := map[string]any{"type": "string", "defaultValue": "small"}
	if got := m[sectionParameters].(map[string]any)["tier"]; !reflect.DeepEqual(got, body) {
		t.Errorf("top-level tier = %v, want %v", got, body)
	}
	if got := levelTemplateOf(t, m)[sectionParameters].(map[string]any)["tier"]; !reflect.DeepEqual(got, body) {
		t.Errorf("level tier = %v, want %v", got, body)
	}
	properties, _ := levelDeploymentOf(t, m)["properties"].(map[string]any)
	want := map[string]any{"tier": map[string]any{"value": "[parameters('tier')]"}}
	if !reflect.DeepEqual(properties["parameters"], want) {
		t.Errorf("passed parameters = %v, want %v", properties["parameters"], want)
	}

	t.Run("the same body at both levels merges once", func(t *testing.T) {
		t.Parallel()
		top := subscriptionSchemaLine + "parameters:\n  tier:\n    type: string\n    defaultValue: small\n"
		m, _ := assembled(t,
			subscriptionBoundary(contributes("group", 0, top)),
			subscriptionBoundary(contributes("payments-db", 0, declares)),
		)
		if got := keysOf(m[sectionParameters]); !reflect.DeepEqual(got, []string{"tier"}) {
			t.Errorf("top-level parameters = %v", got)
		}
	})

	t.Run("two bodies halt", func(t *testing.T) {
		t.Parallel()
		top := subscriptionSchemaLine + "parameters:\n  tier:\n    type: string\n    defaultValue: large\n"
		_, err := Assemble([]Contribution{
			subscriptionBoundary(contributes("group", 0, top)),
			subscriptionBoundary(contributes("payments-db", 0, declares)),
		})
		if !errors.Is(err, plugincondition.ErrPluginOutputNameCollision()) {
			t.Fatalf("err = %v, want an output name collision", err)
		}
		for _, want := range []string{"arm-provider:", "in parameters,", `"tier"`, `"group"`, `"payments-db"`} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("message %q does not contain %q", err, want)
			}
		}
		if strings.Contains(err.Error(), "large") || strings.Contains(err.Error(), "small") {
			t.Errorf("message %q quotes a value", err)
		}
	})
}

func TestScopeCrossLevelReferenceHalts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		group, db string
		pointer   string
	}{
		{"resource group content naming a subscription resource",
			groupFragment(levelGroup),
			"resources:\n  Db:\n    type: T\n    name: db\n    properties:\n      group: \"[reference('Group').location]\"\n",
			"/resources/Db/properties/group"},
		{"subscription content naming a resource group resource",
			groupFragment(levelGroup) + "  Reader:\n    type: T\n    name: reader\n    properties:\n      db: \"[reference('Db').id]\"\n",
			storage("Db"),
			"/resources/Reader/properties/db"},
		{"subscription content naming a resource group variable",
			groupFragment(levelGroup) + "  Reader:\n    type: T\n    name: reader\n    properties:\n      label: \"[variables('label')]\"\n",
			"variables:\n  label: x\n" + storage("Db"),
			"/resources/Reader/properties/label"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := Assemble([]Contribution{
				subscriptionBoundary(contributes("group", 0, c.group)),
				subscriptionBoundary(contributes("payments-db", 0, c.db)),
			})
			if !errors.Is(err, plugincondition.ErrPluginRequiredInputUnresolved()) {
				t.Fatalf("err = %v, want a required input unresolved", err)
			}
			if !strings.Contains(err.Error(), c.pointer) {
				t.Errorf("message %q does not name the pointer %s", err, c.pointer)
			}
		})
	}
}

func TestScopeMissingResourceGroupAnnotationHalts(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		pairs []string
	}{
		{"absent", []string{AnnotationDeploymentScope, "subscription"}},
		{"empty", []string{AnnotationDeploymentScope, "subscription", AnnotationResourceGroup, ""}},
	} {
		_, err := Assemble([]Contribution{
			atScope(contributes("group", 0, groupFragment(levelGroup)), c.pairs...),
			atScope(contributes("payments-db", 0, storage("Db")), c.pairs...),
		})
		if !errors.Is(err, plugincondition.ErrPluginRequiredInputUnresolved()) {
			t.Fatalf("%s: err = %v, want a required input unresolved", c.name, err)
		}
		for _, want := range []string{"arm-provider:", `"payments-db"`, AnnotationResourceGroup} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: message %q does not contain %q", c.name, err, want)
			}
		}
	}
}

func TestScopeDisagreeingResourceGroupAnnotationsHalt(t *testing.T) {
	t.Parallel()
	_, err := Assemble([]Contribution{
		subscriptionBoundary(contributes("payments-db", 0, storage("Db"))),
		atScope(contributes("orders-api", 0, storage("Api")), AnnotationDeploymentScope, "subscription", AnnotationResourceGroup, "elsewhere-rg"),
	})
	if !errors.Is(err, plugincondition.ErrPluginRequiredInputUnresolved()) {
		t.Fatalf("err = %v, want a required input unresolved", err)
	}
	for _, want := range []string{"arm-provider:", `"payments-db"`, `"orders-api"`, AnnotationResourceGroup} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not contain %q", err, want)
		}
	}
	for _, quoted := range []string{levelGroup, "elsewhere-rg"} {
		if strings.Contains(err.Error(), quoted) {
			t.Errorf("message %q quotes the value %q", err, quoted)
		}
	}
	// A contributor whose content stays at the top level is not read for it.
	if _, err := Assemble([]Contribution{
		atScope(contributes("group", 0, groupFragment(levelGroup)), AnnotationDeploymentScope, "subscription", AnnotationResourceGroup, "elsewhere-rg"),
		subscriptionBoundary(contributes("payments-db", 0, storage("Db"))),
	}); err != nil {
		t.Errorf("subscription content carrying another resource group: %v", err)
	}
}

func TestScopeManagementGroupContentInASubscriptionBoundaryHalts(t *testing.T) {
	t.Parallel()
	_, err := Assemble([]Contribution{subscriptionBoundary(contributes("policy", 0, managementGroupSchemaLine+storage("Policy")))})
	if !errors.Is(err, plugincondition.ErrPluginCannotExpressInput()) {
		t.Fatalf("err = %v, want cannot express input", err)
	}
	for _, want := range []string{"arm-provider:", `"policy"`, "management-group", AnnotationDeploymentScope} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not contain %q", err, want)
		}
	}
}

func TestScopeManagementGroupBoundaryHalts(t *testing.T) {
	t.Parallel()
	_, err := Assemble([]Contribution{atScope(contributes("payments-db", 0, storage("Db")), AnnotationDeploymentScope, "managementGroup")})
	if !errors.Is(err, plugincondition.ErrPluginCannotExpressInput()) {
		t.Fatalf("err = %v, want cannot express input", err)
	}
	for _, want := range []string{"arm-provider:", `"payments-db"`, AnnotationDeploymentScope, "management-group scope", "resource-group or subscription scope"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not contain %q", err, want)
		}
	}
	for _, banned := range []string{"yet", "not available"} {
		if strings.Contains(err.Error(), banned) {
			t.Errorf("message %q contains %q", err, banned)
		}
	}
}

func TestScopeLevelSymbolTakenHalts(t *testing.T) {
	t.Parallel()
	taken := subscriptionSchemaLine + "resources:\n  _resourceGroup:\n    type: T\n    name: x\n"
	_, err := Assemble([]Contribution{
		subscriptionBoundary(contributes("group", 0, taken)),
		subscriptionBoundary(contributes("payments-db", 0, storage("Db"))),
	})
	if !errors.Is(err, plugincondition.ErrPluginOutputNameCollision()) {
		t.Fatalf("err = %v, want an output name collision", err)
	}
	for _, want := range []string{"arm-provider:", `"group"`, LevelResourceGroupSymbol} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not contain %q", err, want)
		}
	}
}

func TestScopeLevelTakesPartInWaves(t *testing.T) {
	t.Parallel()
	m, _ := assembled(t,
		subscriptionBoundary(contributes("group", 0, groupFragment(levelGroup))),
		subscriptionBoundary(contributes("payments-db", 1, storage("Db"))),
		subscriptionBoundary(contributes("orders-api", 2, storage("Api"))),
	)
	// The level follows the group by name and by wave; the group is named once.
	if got, want := levelDeploymentOf(t, m)["dependsOn"], []any{"Group"}; !reflect.DeepEqual(got, want) {
		t.Errorf("level dependsOn = %v, want %v", got, want)
	}
	inner := levelTemplateOf(t, m)[sectionResources].(map[string]any)
	if got, want := inner["Api"].(map[string]any)["dependsOn"], []any{"Db"}; !reflect.DeepEqual(got, want) {
		t.Errorf("inner Api dependsOn = %v, want %v", got, want)
	}
	if _, present := inner["Db"].(map[string]any)["dependsOn"]; present {
		t.Errorf("inner Db depends on %v; it holds the level's earliest wave", inner["Db"].(map[string]any)["dependsOn"])
	}

	// A later top-level wave waits for the level.
	m, _ = assembled(t,
		subscriptionBoundary(contributes("group", 1, groupFragment("other-rg"))),
		subscriptionBoundary(contributes("payments-db", 0, storage("Db"))),
	)
	group := m[sectionResources].(map[string]any)["Group"].(map[string]any)
	if got, want := group["dependsOn"], []any{LevelResourceGroupSymbol}; !reflect.DeepEqual(got, want) {
		t.Errorf("Group dependsOn = %v, want %v", got, want)
	}
}

func TestScopeAssemblyIsDeterministic(t *testing.T) {
	t.Parallel()
	contribs := []Contribution{
		subscriptionBoundary(contributes("group", 0, groupFragment(levelGroup))),
		subscriptionBoundary(contributes("payments-db", 0, "parameters:\n  b:\n    type: string\n  a:\n    type: string\nvariables:\n  y: 1\n  x: 2\n"+storage("Db"))),
		subscriptionBoundary(contributes("orders-api", 1, "parameters:\n  c:\n    type: int\n"+storage("Api"))),
	}
	_, first := assembled(t, contribs...)
	for i := 0; i < 20; i++ {
		if _, again := assembled(t, contribs...); !bytes.Equal(first, again) {
			t.Fatalf("run %d differs", i)
		}
	}
}

func TestScopeWrapsAWiderSourcedTemplateWithTheDeploymentLocation(t *testing.T) {
	t.Parallel()
	template := []byte(`{"$schema": "` + SchemaSubscription + `", "contentVersion": "1.0.0.0", "resources": [{"type": "Microsoft.Resources/resourceGroups", "apiVersion": "2024-03-01", "name": "rg", "location": "westeurope"}]}`)
	fragment, symbol, _, err := RenderSourced(SourcedNode{Identity: "landing-zone", Content: template})
	if err != nil {
		t.Fatalf("RenderSourced: %v", err)
	}
	frag, _, err := ReadFragment(fragment)
	if err != nil {
		t.Fatalf("ReadFragment: %v", err)
	}
	if frag.Scope != ScopeSubscription {
		t.Errorf("the wrapper is written for %q, want the scope of the template it carries", frag.Scope)
	}
	wrapper, _ := frag.Sections[sectionResources][symbol].(map[string]any)
	if wrapper["location"] != "[deployment().location]" {
		t.Errorf("wrapper location = %v", wrapper["location"])
	}

	// In a subscription boundary it lands at the top level.
	m, _ := assembled(t, atScope(Contribution{NodeID: "landing-zone", Files: [][]byte{fragment}}, AnnotationDeploymentScope, "subscription"))
	if got := keysOf(m[sectionResources]); !reflect.DeepEqual(got, []string{symbol}) {
		t.Errorf("resources = %v, want the wrapper alone", got)
	}

	// In a resource group boundary it is wider content.
	if _, err := Assemble([]Contribution{{NodeID: "landing-zone", Files: [][]byte{fragment}}}); !errors.Is(err, plugincondition.ErrPluginCannotExpressInput()) {
		t.Errorf("err = %v, want cannot express input", err)
	}

	// A resource group template's wrapper writes neither.
	values := types.OrderedMap[any]{}
	values.Set("name", "storeone")
	plain, plainSymbol, _, err := RenderSourced(SourcedNode{Identity: "payments-db", Content: fixture(t, "module-2.0.json"), Values: values, ValuesMapped: true})
	if err != nil {
		t.Fatalf("RenderSourced: %v", err)
	}
	frag, _, err = ReadFragment(plain)
	if err != nil {
		t.Fatalf("ReadFragment: %v", err)
	}
	if frag.Scope != ScopeResourceGroup {
		t.Errorf("a resource group wrapper is written for %q", frag.Scope)
	}
	var top map[string]any
	if err := json.Unmarshal(plain, &top); err != nil {
		t.Fatal(err)
	}
	if _, present := top[sectionSchema]; present {
		t.Errorf("a resource group wrapper writes a $schema:\n%s", plain)
	}
	if _, present := frag.Sections[sectionResources][plainSymbol].(map[string]any)["location"]; present {
		t.Errorf("a resource group wrapper writes a location:\n%s", plain)
	}
}
