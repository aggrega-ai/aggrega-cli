// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"fmt"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore/armexpr"
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
var assembledScopes = fmt.Sprintf("a boundary deploys at the scope its %s annotation names, resource-group when it names none, and the arm provider assembles a boundary at resource-group, subscription or management-group scope",
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

// widerContent is the halt for a fragment written for a wider scope than the one
// its boundary deploys at.
func widerContent(identity string, content, boundary Scope) error {
	remedy := fmt.Sprintf("annotate the boundary %s: %s, or write these resources in a resource-group or subscription template", AnnotationDeploymentScope, ScopeManagementGroup)
	if content == ScopeSubscription {
		remedy = fmt.Sprintf("annotate the boundary %s: %s or %s, or write these resources in a resource-group template", AnnotationDeploymentScope, ScopeSubscription, ScopeManagementGroup)
	}
	return refuse(plugincondition.ErrPluginCannotExpressInput,
		fmt.Sprintf("the fragment of %q is written for a %s template, wider than the %s scope its boundary deploys at; %s, so %s",
			identity, scopeWords(content), scopeWords(boundary), assembledScopes, remedy))
}

// levelSymbolOf returns the symbolic name of the nested deployment that holds
// the content written for a scope: LevelSubscriptionSymbol or
// LevelResourceGroupSymbol.
func levelSymbolOf(content Scope) string {
	if content == ScopeSubscription {
		return LevelSubscriptionSymbol
	}
	return LevelResourceGroupSymbol
}

// levelAnnotation returns the value of annotation that every contributor of
// nested content resolves, for a nested deployment of the content written for a
// scope inside a template at the boundary's scope. noun names what the value
// is, as "resource group" or "subscription". An absent or empty value halts
// wrapping ErrPluginRequiredInputUnresolved naming the contributor and the
// annotation, and so do two contributors resolving it to different values,
// naming both and neither value.
func levelAnnotation(nested []read, boundary, content Scope, annotation, noun string) (string, error) {
	value, first := "", ""
	for _, r := range nested {
		v := r.annotations[annotation]
		if v == "" {
			return "", refuse(plugincondition.ErrPluginRequiredInputUnresolved,
				fmt.Sprintf("the fragment of %q is written for a %s template, and its boundary deploys at %s scope, where %s content deploys into the %s the %s annotation names; annotate the boundary with %s",
					r.identity, scopeWords(content), scopeWords(boundary), scopeWords(content), noun, annotation, annotation))
		}
		if first == "" {
			value, first = v, r.identity
			continue
		}
		if v != value {
			return "", refuse(plugincondition.ErrPluginRequiredInputUnresolved,
				fmt.Sprintf("%q and %q write %s content into one %s-scope template, and their %s annotations name different values; the template deploys its %s content into one %s, so give both the same value, or place them in boundaries of their own",
					first, r.identity, scopeWords(content), scopeWords(boundary), annotation, scopeWords(content), noun))
		}
	}
	return value, nil
}

// nestedLevel is a nested deployment added to a template's top level: the level
// it holds, where it deploys and the waves of its contributors.
type nestedLevel struct {
	// inner is the level the nested deployment's template is written from.
	inner *levelBuild
	// subscription is the subscription the deployment targets by subscriptionId,
	// empty where it does not set one.
	subscription string
	// first and last are the earliest and latest waves of the contributors of the
	// content.
	first, last int
}

// nestLevel assembles the content written for the scope content, which is
// narrower than the scope boundary its template deploys at, into a level of its
// own and adds it to top, the template's top level, as the nested deployment
// levelSymbolOf(content).
//
// The level is merged, composed, checked for references and ordered by wave as
// any template level is (mergeReads, finishLevel), and checked against the
// limits ARM sets on one template. It is deployed incrementally, evaluated in its
// own scope, under the name LevelHistoryName(content), at the place its
// annotations name:
//
//   - a resource group level of a subscription-scope template deploys into the
//     resource group AnnotationResourceGroup names;
//   - a subscription level of a management-group-scope template deploys to the
//     subscription AnnotationSubscription names, at the location of the
//     deployment that holds it;
//   - a resource group level of a management-group-scope template deploys into
//     the resource group AnnotationResourceGroup names, in the subscription
//     AnnotationSubscription names.
//
// Each parameter the level declares is declared at the top level too, under the
// same name and with the same body, and passed to the level as
// {"value": "[parameters('<name>')]"}; a name the top level already declares
// with another body halts as any collision does. Every definition such a
// parameter references through a $ref of the form #/definitions/<name>, and
// every definition those reference in turn, is declared at the top level too on
// the same terms (carryDefinitions). A parameter whose defaultValue calls a
// user-defined function halts (refuseLevelFunctionDefault): the functions stay in
// the level. A resource group level depends
// on each resource of type Microsoft.Resources/resourceGroups, matched without
// regard to case and not declared existing: true, whose name is exactly that
// resource group's: of the top level in a subscription-scope template, and in a
// management-group-scope template of the subscription level, when that level
// deploys to the same subscription, as a dependency on the level itself. It
// depends on nothing else. At the top level a nested deployment takes the
// earliest wave of its contributors. A top-level resource that already holds the
// symbolic name halts wrapping ErrPluginOutputNameCollision.
//
// It returns the composition diagnostics of the level.
func nestLevel(top *levelBuild, boundary, content Scope, nested []read) ([]renderctx.Diagnostic, error) {
	symbol := levelSymbolOf(content)
	var subscription, group string
	var err error
	if boundary == ScopeManagementGroup {
		if subscription, err = levelAnnotation(nested, boundary, content, AnnotationSubscription, "subscription"); err != nil {
			return nil, err
		}
	}
	if content == ScopeResourceGroup {
		if group, err = levelAnnotation(nested, boundary, content, AnnotationResourceGroup, "resource group"); err != nil {
			return nil, err
		}
	}
	inner := newLevelBuild()
	if err := inner.mergeReads(nested); err != nil {
		return nil, err
	}
	diags, err := inner.finishLevel()
	if err != nil {
		return nil, err
	}
	template := inner.template(content)
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
		if err := refuseLevelFunctionDefault(symbol, content, name, e); err != nil {
			return nil, err
		}
		if err := top.insert(sectionParameters, name, &levelEntry{body: e.body, contributor: e.contributor, pointer: e.pointer}); err != nil {
			return nil, err
		}
		passed[name] = map[string]any{"value": "[parameters(" + StringLiteral(name) + ")]"}
	}
	if err := carryDefinitions(top, inner, parameters); err != nil {
		return nil, err
	}

	if other, taken := top.l.sections[sectionResources][symbol]; taken {
		return nil, refuse(plugincondition.ErrPluginOutputNameCollision,
			fmt.Sprintf("the fragment of %q declares a resource under the symbolic name %s (at JSON pointer %s), which a %s-scope template gives the nested deployment holding its %s content; rename the resource",
				other.contributor, symbol, other.pointer, scopeWords(boundary), scopeWords(content)))
	}
	if len(passed) == 0 {
		passed = nil
	}
	body := nestedDeploymentBody(LevelHistoryName(content), template, passed)
	if subscription != "" {
		body["subscriptionId"] = ArmLiteral(subscription)
	}
	if content == ScopeSubscription {
		body["location"] = "[deployment().location]"
	}
	if group != "" {
		body["resourceGroup"] = ArmLiteral(group)
	}
	var depends []any
	if content == ScopeResourceGroup {
		switch boundary {
		case ScopeSubscription:
			depends = groupResources(top.l.sections[sectionResources], group)
		case ScopeManagementGroup:
			if above, ok := top.levels[LevelSubscriptionSymbol]; ok && above.subscription == subscription &&
				len(groupResources(above.inner.l.sections[sectionResources], group)) > 0 {
				depends = []any{LevelSubscriptionSymbol}
			}
		}
	}
	if len(depends) > 0 {
		body["dependsOn"] = depends
	}
	top.l.put(sectionResources, symbol, &levelEntry{body: body, pointer: "/" + sectionResources + "/" + symbol})
	level := nestedLevel{inner: inner, subscription: subscription, first: nested[0].wave, last: nested[0].wave}
	for _, r := range nested[1:] {
		level.first = min(level.first, r.wave)
		level.last = max(level.last, r.wave)
	}
	top.levels[symbol] = level
	top.waves[symbol] = level.first
	return diags, nil
}

// definitionRefPrefix opens a $ref to a definition of the same template.
const definitionRefPrefix = "#/definitions/"

// carryDefinitions declares at the top level each definition of the level that
// a parameter in parameters references through a $ref, followed through the
// definitions it reaches in turn, with the body and the contributor the level
// holds it under. A definition the top level declares with another body halts
// as any collision does (insert). A $ref naming a definition the level does not
// declare is passed over: the level's own template carries it as written.
func carryDefinitions(top, inner *levelBuild, parameters map[string]*levelEntry) error {
	definitions := inner.l.sections[sectionDefinitions]
	carried := map[string]bool{}
	var pending []string
	reach := func(body any) {
		for _, name := range definitionRefs(body) {
			spelled := name
			if _, declared := definitions[spelled]; !declared {
				spelled = inner.folded[sectionDefinitions][strings.ToLower(name)]
			}
			if _, declared := definitions[spelled]; declared && !carried[spelled] {
				carried[spelled] = true
				pending = append(pending, spelled)
			}
		}
	}
	for _, name := range sortedKeys(parameters) {
		reach(parameters[name].body)
	}
	for len(pending) > 0 {
		name := pending[0]
		pending = pending[1:]
		reach(definitions[name].body)
	}
	for _, name := range sortedKeys(carried) {
		e := definitions[name]
		if err := top.insert(sectionDefinitions, name, &levelEntry{body: e.body, contributor: e.contributor, pointer: e.pointer}); err != nil {
			return err
		}
	}
	return nil
}

// definitionRefs returns, sorted, the definition names the $ref members inside
// v name, each unescaped from its JSON pointer token.
func definitionRefs(v any) []string {
	found := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if ref, ok := x["$ref"].(string); ok && strings.HasPrefix(ref, definitionRefPrefix) {
				token := strings.TrimPrefix(ref, definitionRefPrefix)
				found[strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")] = true
			}
			for _, k := range sortedKeys(x) {
				walk(x[k])
			}
		case []any:
			for _, item := range x {
				walk(item)
			}
		}
	}
	walk(v)
	return sortedKeys(found)
}

// refuseLevelFunctionDefault halts wrapping ErrPluginCannotExpressInput where
// the defaultValue of the parameter name, which the nested deployment symbol
// holding content written for a scope declares and which is declared at the top
// level too, calls a user-defined function. The top level declares no function
// of the level, so the default could not be evaluated there; the halt names the
// parameter, the function, the contributor and the nested deployment.
func refuseLevelFunctionDefault(symbol string, content Scope, name string, e *levelEntry) error {
	body, _ := e.body.(map[string]any)
	var called string
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			if called != "" || !armexpr.IsExpression(x) {
				return
			}
			n, err := armexpr.Parse(x)
			if err != nil {
				return
			}
			armexpr.Walk(n, func(node armexpr.Node) bool {
				if c, ok := node.(armexpr.Call); ok && c.Namespace != "" && called == "" {
					called = c.Namespace + "." + c.Name
				}
				return called == ""
			})
		case map[string]any:
			for _, k := range sortedKeys(x) {
				walk(x[k])
			}
		case []any:
			for _, item := range x {
				walk(item)
			}
		}
	}
	walk(body[paramDefault])
	if called == "" {
		return nil
	}
	return refuse(plugincondition.ErrPluginCannotExpressInput,
		fmt.Sprintf("the parameter %q of the fragment of %q (at JSON pointer %s/%s) is declared at the top level too, to be passed to the nested deployment %s that holds the %s content, and its defaultValue calls the user-defined function %s, which only %s declares; remove the default and supply the value when deploying, or compute it in a variable of the %s content",
			name, e.contributor, e.pointer, paramDefault, symbol, scopeWords(content), called, symbol, scopeWords(content)))
}

// orderLevels writes the wave order onto the nested deployments of a template,
// and halts wrapping ErrPluginCannotExpressInput where it cannot be written. It
// runs before the waves are applied to the top level.
//
// At the top level a nested deployment is one unit in the earliest wave of its
// contributors, holding the content of every wave up to its latest, and each
// resource of a wave depends on the resources of the preceding populated wave
// (applyWaveOrdering). So:
//
//   - A nested deployment that depends on a unit whose content reaches a later
//     wave than the deployment's earliest halts: the resource declaring the
//     resource group it deploys into would depend on it and be depended on by
//     it, and the subscription level holding that resource would deploy its
//     later content before the deployment's earlier content.
//   - A top-level unit in a wave strictly between the earliest and the latest
//     wave of a nested deployment's contributors halts: it cannot be ordered both
//     after the content of the earliest wave and before the content of the
//     latest.
//   - A top-level unit in the earliest wave of a nested deployment that holds the
//     content of a later wave too is added to the deployment's dependsOn, after
//     the dependencies it already lists, so that later content follows it. No
//     top-level resource can name a nested deployment, so the added dependency on
//     a resource closes no cycle; two nested deployments that would each have to
//     be added to the other's dependsOn halt, naming both.
func orderLevels(top *levelBuild) error {
	resources := top.l.sections[sectionResources]
	for _, symbol := range sortedKeys(top.levels) {
		level := top.levels[symbol]
		for _, dependency := range levelDependencies(resources[symbol]) {
			if other, nested := top.levels[dependency]; nested {
				if other.last > level.first {
					return refuse(plugincondition.ErrPluginCannotExpressInput,
						fmt.Sprintf("the nested deployment %s depends on %s, which declares the resource group it deploys into, and the content of %s reaches wave %d while the content of %s starts in wave %d; deploying %s first would put its wave %d content before the wave %d content, so put the contributors of %s in an earlier wave, no later than wave %d, or the contributors of %s in wave %d or later",
							symbol, dependency, dependency, other.last, symbol, level.first, dependency, other.last, level.first, dependency, level.first, symbol, other.last))
				}
				continue
			}
			if top.waves[dependency] > level.first {
				return refuse(plugincondition.ErrPluginCannotExpressInput,
					fmt.Sprintf("the nested deployment %s depends on %s, which declares the resource group it deploys into, and %s is in wave %d while the content of %s starts in wave %d; the wave order would make each depend on the other, which ARM rejects, so put the contributors of %s in an earlier wave, no later than wave %d",
						symbol, describeResource(resources, dependency), dependency, top.waves[dependency], symbol, level.first, dependency, level.first))
			}
		}
		for _, other := range sortedKeys(resources) {
			if other == symbol || isExisting(resources[other].body) {
				continue
			}
			if w := top.waves[other]; w > level.first && w < level.last {
				who := describeResource(resources, other)
				return refuse(plugincondition.ErrPluginCannotExpressInput,
					fmt.Sprintf("the nested deployment %s holds the content of waves %d to %d and is ordered as one unit in wave %d, and %s is in wave %d, between them; the order of the waves cannot be written, so move that resource to wave %d or earlier, or to wave %d or later, or put the contributors of %s in one wave",
						symbol, level.first, level.last, level.first, who, w, level.first, level.last, symbol))
			}
		}
	}

	added := map[string][]any{}
	for _, symbol := range sortedKeys(top.levels) {
		level := top.levels[symbol]
		if level.first == level.last {
			continue
		}
		depends := levelDependencies(resources[symbol])
		for _, other := range sortedKeys(resources) {
			if other == symbol || isExisting(resources[other].body) || top.waves[other] != level.first || slices.Contains(depends, other) {
				continue
			}
			added[symbol] = append(added[symbol], other)
		}
	}
	for _, symbol := range sortedKeys(added) {
		for _, other := range added[symbol] {
			name := other.(string)
			if name > symbol && slices.Contains(added[name], any(symbol)) {
				first, second := top.levels[symbol], top.levels[name]
				return refuse(plugincondition.ErrPluginCannotExpressInput,
					fmt.Sprintf("the nested deployments %s and %s both hold content of wave %d and of a later wave (%s up to wave %d, %s up to wave %d), so each would have to wait for the wave %d content of the other, which ARM rejects as a cycle; put the contributors of one of them in one wave, or start one of them in the wave the other ends in",
						symbol, name, first.first, symbol, first.last, name, second.last, first.first))
			}
		}
	}
	for _, symbol := range sortedKeys(added) {
		body, _ := resources[symbol].body.(map[string]any)
		out := clone(body)
		existing, _ := body["dependsOn"].([]any)
		out["dependsOn"] = append(slices.Clone(existing), added[symbol]...)
		resources[symbol].body = out
	}
	return nil
}

// levelDependencies returns the symbolic names a nested deployment's dependsOn
// lists, as nestLevel wrote it.
func levelDependencies(e *levelEntry) []string {
	body, _ := e.body.(map[string]any)
	depends, _ := body["dependsOn"].([]any)
	out := make([]string, 0, len(depends))
	for _, d := range depends {
		if name, ok := d.(string); ok {
			out = append(out, name)
		}
	}
	return out
}

// describeResource names the top-level resource symbol, with the contributor
// that wrote it where one did.
func describeResource(resources map[string]*levelEntry, symbol string) string {
	if owner := resources[symbol].contributor; owner != "" {
		return fmt.Sprintf("%s of %q", symbol, owner)
	}
	return symbol
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
