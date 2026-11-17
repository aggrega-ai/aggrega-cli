// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// End-to-end proof of the scope an arm boundary deploys at. A boundary annotated
// aggrega.ai/azure-deployment-scope: subscription assembles a template deployed
// to a subscription: content written for a subscription sits at its top level,
// and content written for a resource group sits in one nested deployment, the
// resource group level, deployed into the resource group the boundary's
// aggrega.ai/azure-resource-group names. Every build runs through the real
// compiled Wasm guest, each successful build is repeated and must give
// identical bytes, and each halt is raised twice with the same text.
//
// A boundary annotated aggrega.ai/azure-deployment-scope: managementGroup
// assembles a template deployed to a management group: content written for a
// management group sits at its top level, and subscription content and resource
// group content sit in two nested deployments side by side, the subscription
// level and the resource group level, each deployed to the subscription and
// resource group the boundary's annotations name.

package compiler_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
)

const (
	armScopeDir     = "testdata/arm-scope/"
	armScopeCatalog = armScopeDir + "catalog"

	// The literals the emitted template carries, written as it carries them.
	armSubscriptionSchema  = "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#"
	armResourceGroupSchema = "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#"
	armManagementSchema    = "https://schema.management.azure.com/schemas/2019-08-01/managementGroupDeploymentTemplate.json#"
	armLevelSymbol         = "_resourceGroup"
	armLevelName           = "[format('rg-{0}', uniqueString(deployment().name))]"
	armDeploymentsType     = "Microsoft.Resources/deployments"
	armDeploymentsVersion  = "2025-04-01"
	armSubscriptionLevel   = "_subscription"
	armSubscriptionName    = "[format('sub-{0}', uniqueString(deployment().name))]"
	armDeploymentScopeKey  = "aggrega.ai/azure-deployment-scope"
	armSubscriptionKey     = "aggrega.ai/azure-subscription"
	armResourceGroupKey    = "aggrega.ai/azure-resource-group"
)

// armScopeInput is the build input for one Release of the scope fixture, built
// against the shared catalog and the fixture's own.
func armScopeInput(release string) pipelinetest.Input {
	return armInput(armScopeDir+release, armScopeCatalog)
}

// armScopeLinkInput is armScopeInput with the Recipe of the linked datastore,
// which the two-deployment fixture's catalog holds.
func armScopeLinkInput(release string) pipelinetest.Input {
	return armInput(armScopeDir+release, armScopeCatalog, armLinkCatalog)
}

// armLevel returns the resource group level deployment of a decoded template.
func armLevel(t *testing.T, template map[string]any) map[string]any {
	t.Helper()
	return armResourceBody(t, template, armLevelSymbol)
}

// armLevelProperties returns the properties of the resource group level.
func armLevelProperties(t *testing.T, template map[string]any) map[string]any {
	t.Helper()
	properties, ok := armLevel(t, template)["properties"].(map[string]any)
	if !ok {
		t.Fatalf("the level %s carries no properties", armLevelSymbol)
	}
	return properties
}

// armLevelTemplate returns the template the resource group level carries.
func armLevelTemplate(t *testing.T, template map[string]any) map[string]any {
	t.Helper()
	inner, ok := armLevelProperties(t, template)["template"].(map[string]any)
	if !ok {
		t.Fatalf("the level %s carries no template", armLevelSymbol)
	}
	return inner
}

// armWantKeys asserts a template section holds exactly the given names.
func armWantKeys(t *testing.T, what string, section map[string]any, want ...string) {
	t.Helper()
	if got := sortedAnyKeys(section); !equalStringSlice(got, want) {
		t.Errorf("%s holds %v, want exactly %v", what, got, want)
	}
}

// TestArmSubscriptionScopeNestsResourceGroupContent builds a boundary deployed at
// subscription scope. A resource group and a sourced template written for a
// subscription sit at the top level, stamped with the subscription schema; both
// storage accounts, written for a resource group, sit merged in the resource
// group level, a nested deployment into the boundary's resource group.
func TestArmSubscriptionScopeNestsResourceGroupContent(t *testing.T) {
	t.Parallel()
	in := armScopeInput("release.yaml")
	result := pipelinetest.Build(t, in)
	template, raw := findArmTemplate(t, result.Artifacts, "dist/platform/")
	t.Logf("assembled template:\n%s", raw)

	if template["$schema"] != armSubscriptionSchema || template["languageVersion"] != "2.0" || template["contentVersion"] != "1.0.0.0" {
		t.Errorf("the template is stamped %#v, %#v, %#v; want the subscription schema, 2.0 and 1.0.0.0",
			template["$schema"], template["languageVersion"], template["contentVersion"])
	}
	armWantKeys(t, "the top level's resources", armSection(t, template, "resources"), "Group", "Zone", armLevelSymbol)

	// A template written for a subscription is wrapped at the top level, taking
	// the location of the deployment that holds it.
	if got := armResourceBody(t, template, "Zone")["location"]; got != "[deployment().location]" {
		t.Errorf("the sourced subscription template's deployment has location %#v, want [deployment().location]", got)
	}

	level := armLevel(t, template)
	for key, want := range map[string]any{
		"type":          armDeploymentsType,
		"apiVersion":    armDeploymentsVersion,
		"name":          armLevelName,
		"resourceGroup": "orders-rg",
	} {
		if level[key] != want {
			t.Errorf("the level's %s is %#v, want %#v", key, level[key], want)
		}
	}
	properties := armLevelProperties(t, template)
	if properties["mode"] != "Incremental" {
		t.Errorf("the level's mode is %#v, want Incremental", properties["mode"])
	}
	if eval, _ := properties["expressionEvaluationOptions"].(map[string]any); eval["scope"] != "inner" {
		t.Errorf("the level's expressionEvaluationOptions are %#v, want scope inner", properties["expressionEvaluationOptions"])
	}
	inner := armLevelTemplate(t, template)
	if inner["$schema"] != armResourceGroupSchema || inner["languageVersion"] != "2.0" || inner["contentVersion"] != "1.0.0.0" {
		t.Errorf("the level is stamped %#v, %#v, %#v; want the resource group schema, 2.0 and 1.0.0.0",
			inner["$schema"], inner["languageVersion"], inner["contentVersion"])
	}
	armWantKeys(t, "the level's resources", armSection(t, inner, "resources"), "Cache", "Store")
	for _, symbol := range []string{"Cache", "Store"} {
		if _, ok := armProvenanceOf(armResourceBody(t, inner, symbol)); !ok {
			t.Errorf("the level's resource %s carries no provenance", symbol)
		}
	}
	pipelinetest.AssertDeterministic(t, in)
}

// TestArmResourceGroupLevelDependsOnItsGroup builds the subscription boundary
// twice: where the resource group its components declare is the one the
// boundary's annotation names, the level depends on it; where the annotation
// names another resource group, the level depends on nothing.
func TestArmResourceGroupLevelDependsOnItsGroup(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		release string
		want    []any
	}{
		{"release.yaml", []any{"Group"}},
		{"release-other-group.yaml", nil},
	} {
		t.Run(c.release, func(t *testing.T) {
			t.Parallel()
			in := armScopeInput(c.release)
			result := pipelinetest.Build(t, in)
			template, raw := findArmTemplate(t, result.Artifacts, "dist/platform/")
			got, _ := armLevel(t, template)["dependsOn"].([]any)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("the level depends on %#v, want %#v\n%s", got, c.want, raw)
			}
			pipelinetest.AssertDeterministic(t, in)
		})
	}
}

// TestArmParametersPassThroughToTheLevel builds two storage accounts that declare
// one template parameter in the resource group level. The parameter is declared
// at the top level with the same body and passed to the level, so it is supplied
// once when the template deploys. A subscription fragment declaring that name
// with another default halts naming both contributors.
func TestArmParametersPassThroughToTheLevel(t *testing.T) {
	t.Parallel()
	in := armScopeInput("release.yaml")
	result := pipelinetest.Build(t, in)
	template, raw := findArmTemplate(t, result.Artifacts, "dist/platform/")

	body := map[string]any{"type": "string", "defaultValue": "Standard_LRS"}
	if got := armSection(t, template, "parameters")["tier"]; !reflect.DeepEqual(got, body) {
		t.Errorf("the top level declares tier as %#v, want %#v\n%s", got, body, raw)
	}
	if got := armSection(t, armLevelTemplate(t, template), "parameters")["tier"]; !reflect.DeepEqual(got, body) {
		t.Errorf("the level declares tier as %#v, want %#v", got, body)
	}
	passed := map[string]any{"tier": map[string]any{"value": "[parameters('tier')]"}}
	if got := armLevelProperties(t, template)["parameters"]; !reflect.DeepEqual(got, passed) {
		t.Errorf("the level is passed %#v, want %#v", got, passed)
	}
	pipelinetest.AssertDeterministic(t, in)

	msg := armAssertHaltsTwice(t, armScopeInput("release-parameter-conflict.yaml"), plugincondition.ErrPluginOutputNameCollision())
	for _, want := range []string{"arm-provider:", "in parameters,", `"tier"`, `"group"`, `"store"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not say %q: %s", want, msg)
		}
	}
	for _, quoted := range []string{"Premium_LRS", "Standard_LRS"} {
		if strings.Contains(msg, quoted) {
			t.Errorf("the halt quotes the default %q: %s", quoted, msg)
		}
	}
}

// TestArmCrossLevelReferenceHalts builds a storage account in the resource group
// level that names the resource group the top level declares. A reference reaches
// only its own level, so the build halts naming the pointer and the name.
func TestArmCrossLevelReferenceHalts(t *testing.T) {
	t.Parallel()
	msg := armAssertHaltsTwice(t, armScopeInput("release-cross-level.yaml"), plugincondition.ErrPluginRequiredInputUnresolved())
	for _, want := range []string{"arm-provider:", "/resources/Store/properties/groupLocation", `"store"`, `"Group"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not say %q: %s", want, msg)
		}
	}
}

// TestArmMissingResourceGroupAnnotationHalts builds the subscription boundary
// without aggrega.ai/azure-resource-group. Its resource group content has no
// resource group to deploy into, so the build halts naming the annotation.
func TestArmMissingResourceGroupAnnotationHalts(t *testing.T) {
	t.Parallel()
	msg := armAssertHaltsTwice(t, armScopeInput("release-missing-group.yaml"), plugincondition.ErrPluginRequiredInputUnresolved())
	for _, want := range []string{"arm-provider:", armResourceGroupKey, "subscription scope"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not say %q: %s", want, msg)
		}
	}
}

// TestArmManagementGroupContentInASubscriptionBoundaryHalts builds a fragment
// written for a management group into a boundary deployed at subscription
// scope. That content is wider than its boundary, so the build halts naming the
// contributor, the scopes and the annotation that chooses the boundary's.
func TestArmManagementGroupContentInASubscriptionBoundaryHalts(t *testing.T) {
	t.Parallel()
	msg := armAssertHaltsTwice(t, armScopeInput("release-management-group-content.yaml"), plugincondition.ErrPluginCannotExpressInput())
	for _, want := range []string{"arm-provider:", `"policy"`, "management-group template", "subscription scope", armDeploymentScopeKey, "resource-group, subscription or management-group scope"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not say %q: %s", want, msg)
		}
	}
}

// TestArmManagementGroupScopeNestsTwoLevels builds a boundary deployed at
// management group scope. A policy definition written for a management group
// sits at the top level, stamped with the management group schema. The resource
// group, written for a subscription, sits in the subscription level, deployed to
// the boundary's subscription at the location its outer template deploys with; the storage account,
// written for a resource group, sits in the resource group level, deployed into
// the boundary's resource group of that subscription. Both levels are nested
// directly under the management group, and the resource group level follows the
// subscription level that declares its group.
func TestArmManagementGroupScopeNestsTwoLevels(t *testing.T) {
	t.Parallel()
	in := armScopeInput("release-management-group.yaml")
	result := pipelinetest.Build(t, in)
	template, raw := findArmTemplate(t, result.Artifacts, "dist/platform/")
	t.Logf("assembled template:\n%s", raw)

	if template["$schema"] != armManagementSchema || template["languageVersion"] != "2.0" || template["contentVersion"] != "1.0.0.0" {
		t.Errorf("the template is stamped %#v, %#v, %#v; want the management group schema, 2.0 and 1.0.0.0",
			template["$schema"], template["languageVersion"], template["contentVersion"])
	}
	armWantKeys(t, "the top level's resources", armSection(t, template, "resources"), "Policy", armLevelSymbol, armSubscriptionLevel)

	subscription := armResourceBody(t, template, armSubscriptionLevel)
	for key, want := range map[string]any{
		"type":           armDeploymentsType,
		"apiVersion":     armDeploymentsVersion,
		"name":           armSubscriptionName,
		"subscriptionId": "orders-sub",
		"location":       "[deployment().location]",
	} {
		if subscription[key] != want {
			t.Errorf("%s has %s %#v, want %#v", armSubscriptionLevel, key, subscription[key], want)
		}
	}
	for _, absent := range []string{"resourceGroup", "dependsOn"} {
		if _, present := subscription[absent]; present {
			t.Errorf("%s writes %s", armSubscriptionLevel, absent)
		}
	}
	subscriptionTemplate, _ := subscription["properties"].(map[string]any)["template"].(map[string]any)
	if subscriptionTemplate["$schema"] != armSubscriptionSchema {
		t.Errorf("%s is stamped %#v, want the subscription schema", armSubscriptionLevel, subscriptionTemplate["$schema"])
	}
	armWantKeys(t, "the subscription level's resources", armSection(t, subscriptionTemplate, "resources"), "Group")

	group := armLevel(t, template)
	for key, want := range map[string]any{
		"type":           armDeploymentsType,
		"apiVersion":     armDeploymentsVersion,
		"name":           armLevelName,
		"subscriptionId": "orders-sub",
		"resourceGroup":  "orders-rg",
	} {
		if group[key] != want {
			t.Errorf("%s has %s %#v, want %#v", armLevelSymbol, key, group[key], want)
		}
	}
	if _, present := group["location"]; present {
		t.Errorf("%s writes a location", armLevelSymbol)
	}
	if got, want := group["dependsOn"], []any{armSubscriptionLevel}; !reflect.DeepEqual(got, want) {
		t.Errorf("%s depends on %#v, want %#v", armLevelSymbol, got, want)
	}
	inner := armLevelTemplate(t, template)
	if inner["$schema"] != armResourceGroupSchema {
		t.Errorf("%s is stamped %#v, want the resource group schema", armLevelSymbol, inner["$schema"])
	}
	armWantKeys(t, "the resource group level's resources", armSection(t, inner, "resources"), "Store")
	if _, ok := armProvenanceOf(armResourceBody(t, inner, "Store")); !ok {
		t.Errorf("the resource group level's Store carries no provenance")
	}
	pipelinetest.AssertDeterministic(t, in)
}

// TestArmManagementGroupScopeSubscriptionAbsentHalts builds the management group
// boundary without aggrega.ai/azure-subscription. Its subscription content and
// its resource group content have no subscription to deploy to, so the build
// halts naming the annotation and the scope.
func TestArmManagementGroupScopeSubscriptionAbsentHalts(t *testing.T) {
	t.Parallel()
	msg := armAssertHaltsTwice(t, armScopeInput("release-management-group-missing-subscription.yaml"), plugincondition.ErrPluginRequiredInputUnresolved())
	for _, want := range []string{"arm-provider:", armSubscriptionKey, "management-group scope", `"group"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not say %q: %s", want, msg)
		}
	}
}

// TestArmLinkToAManagementGroupScopeProducerIsRefused builds a datastore in a
// boundary deployed at management group scope and an application in another
// boundary that reads its host. A link reads a producer deployed at resource
// group scope, so the build halts naming the link.
func TestArmLinkToAManagementGroupScopeProducerIsRefused(t *testing.T) {
	t.Parallel()
	msg := armAssertHaltsTwice(t, armScopeLinkInput("release-link-producer-management-group.yaml"), plugincondition.ErrPluginCannotExpressInput())
	for _, want := range []string{
		"arm-provider:",
		"arm links to a producer deployed at resource-group scope; this producer's boundary deploys at management-group scope",
		`component "app"`, `component "store"`, `socket "database"`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not say %q: %s", want, msg)
		}
	}
	for _, banned := range []string{"yet", "not available", "orders-data", "orders-sub", "orders-mg"} {
		if strings.Contains(msg, banned) {
			t.Errorf("the halt says %q: %s", banned, msg)
		}
	}
}

// TestArmLinkToASubscriptionScopeProducerIsRefused builds a datastore in a
// boundary deployed at subscription scope and an application in another
// boundary that reads its host. A link reads a producer deployed at resource
// group scope, so the build halts naming the link.
func TestArmLinkToASubscriptionScopeProducerIsRefused(t *testing.T) {
	t.Parallel()
	msg := armAssertHaltsTwice(t, armScopeLinkInput("release-link-producer.yaml"), plugincondition.ErrPluginCannotExpressInput())
	for _, want := range []string{
		"arm-provider:",
		"arm links to a producer deployed at resource-group scope; this producer's boundary deploys at subscription scope",
		`component "app"`, `component "store"`, `socket "database"`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not say %q: %s", want, msg)
		}
	}
	for _, banned := range []string{"yet", "not available", "orders-data"} {
		if strings.Contains(msg, banned) {
			t.Errorf("the halt says %q: %s", banned, msg)
		}
	}
}

// TestArmLinkIntoTheResourceGroupLevel builds the application in a boundary
// deployed at subscription scope and the datastore in one deployed at resource
// group scope. The application sits in the resource group level, and the link
// writes the read of the datastore's deployment there, where the level's own
// resource group is the consumer's.
func TestArmLinkIntoTheResourceGroupLevel(t *testing.T) {
	t.Parallel()
	in := armScopeLinkInput("release-link-consumer.yaml")
	result := pipelinetest.Build(t, in)
	armNoLinkPlaceholder(t, result.Artifacts)

	consumer, raw := findArmTemplate(t, result.Artifacts, "dist/web/")
	t.Logf("consuming template:\n%s", raw)
	settings, _ := armResourceBody(t, armLevelTemplate(t, consumer), "App")["properties"].(map[string]any)["settings"].(map[string]any)
	want := "[" + armLinkSameGroupRead("aggrega--arm-scope-link-consumer--dist-zsdata-zsarm", "store__host") + "]"
	if got := settings["databaseHost"]; got != want {
		t.Errorf("the application's databaseHost is %#v, want %q", got, want)
	}
	pipelinetest.AssertDeterministic(t, in)
}
