// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
)

// BoundaryScope returns the scope a boundary deploys at, read from the merged
// annotations of one of its components: the value of AnnotationDeploymentScope,
// or ScopeResourceGroup where the annotation is absent or empty. A value that is
// none of ScopeResourceGroup, ScopeSubscription and ScopeManagementGroup returns
// an error wrapping ErrPluginRequiredInputUnresolved that names the annotation
// and the values it takes, and does not quote the value.
func BoundaryScope(annotations map[string]string) (Scope, error) {
	return scopeAnnotation(annotations, "")
}

// scopeAnnotation reads AnnotationDeploymentScope as BoundaryScope does. A
// refusal names the contributor whose annotations were read, where one is given.
func scopeAnnotation(annotations map[string]string, contributor string) (Scope, error) {
	value := annotations[AnnotationDeploymentScope]
	if value == "" {
		return ScopeResourceGroup, nil
	}
	switch s := Scope(value); s {
	case ScopeResourceGroup, ScopeSubscription, ScopeManagementGroup:
		return s, nil
	}
	whose := "the annotation " + AnnotationDeploymentScope
	if contributor != "" {
		whose = fmt.Sprintf("the annotation %s of %q", AnnotationDeploymentScope, contributor)
	}
	return "", refuse(plugincondition.ErrPluginRequiredInputUnresolved,
		fmt.Sprintf("%s holds a value that names no deployment scope; write %s, %s or %s, or remove the annotation to deploy at resource-group scope",
			whose, ScopeResourceGroup, ScopeSubscription, ScopeManagementGroup))
}

// scopeWords names a scope as a sentence does.
func scopeWords(s Scope) string {
	switch s {
	case ScopeSubscription:
		return "subscription"
	case ScopeManagementGroup:
		return "management-group"
	}
	return "resource-group"
}

// scopeRank orders the scopes from the narrowest, a resource group, to the
// widest, a management group.
func scopeRank(s Scope) int {
	switch s {
	case ScopeSubscription:
		return 1
	case ScopeManagementGroup:
		return 2
	}
	return 0
}

// assembledScopes says, in a sentence, which scopes the provider assembles a
// boundary at and how a boundary chooses one.
var assembledScopes = fmt.Sprintf("a boundary deploys at the scope its %s annotation names, resource-group when it names none, and the arm provider assembles a boundary at resource-group or subscription scope",
	AnnotationDeploymentScope)

// boundaryScopeOf returns the scope the contributions of one dispatch group
// deploy at. Every contribution's annotations must resolve AnnotationDeploymentScope
// to the same scope, an absent annotation resolving to ScopeResourceGroup, or the
// assembly halts wrapping ErrPluginRequiredInputUnresolved, naming two
// contributors that disagree. A group with no contributions deploys at
// ScopeResourceGroup.
func boundaryScopeOf(contribs []Contribution) (Scope, error) {
	scope, first := ScopeResourceGroup, ""
	for i, c := range contribs {
		identity := ContributorIdentity(c)
		s, err := scopeAnnotation(c.Annotations, identity)
		if err != nil {
			return "", err
		}
		if i == 0 {
			scope, first = s, identity
			continue
		}
		if s != scope {
			return "", refuse(plugincondition.ErrPluginRequiredInputUnresolved,
				fmt.Sprintf("%q and %q assemble into one template, and their %s annotations resolve to different scopes, an absent annotation resolving to resource-group scope; one template deploys at one scope, so give every component of the boundary the same value",
					first, identity, AnnotationDeploymentScope))
		}
	}
	return scope, nil
}

// refuseBoundaryScope halts wrapping ErrPluginCannotExpressInput for a boundary
// deploying at a scope the provider does not assemble a template for: a
// management group. contributor is a component of the boundary, named in the
// halt.
func refuseBoundaryScope(scope Scope, contributor string) error {
	if scope != ScopeManagementGroup {
		return nil
	}
	return refuse(plugincondition.ErrPluginCannotExpressInput,
		fmt.Sprintf("the boundary of %q deploys at %s scope; %s, so set that annotation to %s or %s",
			contributor, scopeWords(scope), assembledScopes, ScopeResourceGroup, ScopeSubscription))
}

// widerContent is the halt for a fragment written for a wider scope than the one
// its boundary deploys at.
func widerContent(identity string, content, boundary Scope) error {
	remedy := "write these resources in a resource-group or subscription template"
	if content == ScopeSubscription {
		remedy = fmt.Sprintf("annotate the boundary %s: %s, or write these resources in a resource-group template", AnnotationDeploymentScope, ScopeSubscription)
	}
	return refuse(plugincondition.ErrPluginCannotExpressInput,
		fmt.Sprintf("the fragment of %q is written for a %s template, wider than the %s scope its boundary deploys at; %s, so %s",
			identity, scopeWords(content), scopeWords(boundary), assembledScopes, remedy))
}

// levelResourceGroup returns the resource group the resource group level of a
// subscription scope template deploys into: the value of AnnotationResourceGroup
// every contributor holding resource group content resolves. An absent or empty
// value halts wrapping ErrPluginRequiredInputUnresolved naming the contributor,
// and so do two contributors resolving it to different values, naming both and
// neither value.
func levelResourceGroup(nested []read) (string, error) {
	group, first := "", ""
	for _, r := range nested {
		value := r.annotations[AnnotationResourceGroup]
		if value == "" {
			return "", refuse(plugincondition.ErrPluginRequiredInputUnresolved,
				fmt.Sprintf("the fragment of %q is written for a resource group, and its boundary deploys at subscription scope, where the resources of a resource-group template deploy into the resource group the %s annotation names; annotate the boundary with %s",
					r.identity, AnnotationResourceGroup, AnnotationResourceGroup))
		}
		if first == "" {
			group, first = value, r.identity
			continue
		}
		if value != group {
			return "", refuse(plugincondition.ErrPluginRequiredInputUnresolved,
				fmt.Sprintf("%q and %q write resource-group content into one subscription-scope template, and their %s annotations name different resource groups; the template deploys its resource-group content into one resource group, so give both the same value, or place them in boundaries of their own",
					first, r.identity, AnnotationResourceGroup))
		}
	}
	return group, nil
}

// nestResourceGroupLevel assembles the resource group content of a subscription
// scope template into a level of its own and adds it to top, the template's
// top level, as the nested deployment LevelResourceGroupSymbol.
//
// The level is merged, composed, checked for references and ordered by wave as
// any template level is (mergeReads, finishLevel), and checked against the
// limits ARM sets on one template. It is deployed into the resource group
// levelResourceGroup reads, evaluated in its own scope and deployed
// incrementally, under the name LevelHistoryName(ScopeResourceGroup). Each
// parameter the level declares is declared at the top level too, under the same
// name and with the same body, and passed to the level as
// {"value": "[parameters('<name>')]"}; a name the top level already declares
// with another body halts as any collision does. The nested deployment depends on
// each top-level resource of type Microsoft.Resources/resourceGroups, matched
// without regard to case and not declared existing: true, whose name is exactly
// that resource group's, and on nothing else. At the top level it takes the
// earliest wave of the level's contributors. A top-level resource that already
// holds the symbolic name halts wrapping ErrPluginOutputNameCollision.
//
// It returns the composition diagnostics of the level.
func nestResourceGroupLevel(top *levelBuild, nested []read) ([]renderctx.Diagnostic, error) {
	group, err := levelResourceGroup(nested)
	if err != nil {
		return nil, err
	}
	inner := newLevelBuild()
	if err := inner.mergeReads(nested); err != nil {
		return nil, err
	}
	diags, err := inner.finishLevel()
	if err != nil {
		return nil, err
	}
	template := inner.template(ScopeResourceGroup)
	encoded, err := encodeTemplate(template)
	if err != nil {
		return nil, err
	}
	if err := CheckLimits(template, encoded); err != nil {
		return nil, err
	}

	parameters := inner.l.sections[sectionParameters]
	passed := map[string]any{}
	for _, name := range sortedKeys(parameters) {
		e := parameters[name]
		if err := top.insert(sectionParameters, name, &levelEntry{body: e.body, contributor: e.contributor, pointer: e.pointer}); err != nil {
			return nil, err
		}
		passed[name] = map[string]any{"value": "[parameters(" + StringLiteral(name) + ")]"}
	}

	if other, taken := top.l.sections[sectionResources][LevelResourceGroupSymbol]; taken {
		return nil, refuse(plugincondition.ErrPluginOutputNameCollision,
			fmt.Sprintf("the fragment of %q declares a resource under the symbolic name %s (at JSON pointer %s), which a subscription-scope template gives the nested deployment holding its resource-group content; rename the resource",
				other.contributor, LevelResourceGroupSymbol, other.pointer))
	}
	properties := map[string]any{
		"mode":                        "Incremental",
		"expressionEvaluationOptions": map[string]any{"scope": "inner"},
		"template":                    template,
	}
	if len(passed) > 0 {
		properties["parameters"] = passed
	}
	body := map[string]any{
		"type":          deploymentsResourceType,
		"apiVersion":    DeploymentsAPIVersion,
		"name":          LevelHistoryName(ScopeResourceGroup),
		"resourceGroup": ArmLiteral(group),
		"properties":    properties,
	}
	if depends := groupResources(top.l.sections[sectionResources], group); len(depends) > 0 {
		body["dependsOn"] = depends
	}
	top.l.put(sectionResources, LevelResourceGroupSymbol, &levelEntry{body: body, pointer: "/" + sectionResources + "/" + LevelResourceGroupSymbol})
	wave := nested[0].wave
	for _, r := range nested[1:] {
		wave = min(wave, r.wave)
	}
	top.waves[LevelResourceGroupSymbol] = wave
	return diags, nil
}

// resourceGroupsType is the resource type of a resource group, matched without
// regard to case.
const resourceGroupsType = "Microsoft.Resources/resourceGroups"

// groupResources returns, sorted, the symbolic names of the resources that
// declare the resource group named exactly group: of type resourceGroupsType and
// not declared existing: true.
func groupResources(resources map[string]*levelEntry, group string) []any {
	var out []any
	for _, symbol := range sortedKeys(resources) {
		body, _ := resources[symbol].body.(map[string]any)
		typ, _ := body["type"].(string)
		name, _ := body["name"].(string)
		if strings.EqualFold(typ, resourceGroupsType) && name == group && !isExisting(body) {
			out = append(out, symbol)
		}
	}
	return out
}
