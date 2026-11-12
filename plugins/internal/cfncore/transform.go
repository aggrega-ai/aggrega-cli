// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfncore

import (
	"fmt"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// The transforms CloudFormation hosts, each spelled as the literal string a
// template declares it by. A transform not listed here is a macro an account
// defines, which this core treats as custom.
const (
	transformCodeDeployBlueGreen = "AWS::CodeDeployBlueGreen"
	transformInclude             = "AWS::Include"
	transformLanguageExtensions  = "AWS::LanguageExtensions"
	transformSecretsManager2020  = "AWS::SecretsManager-2020-07-23"
	transformSecretsManager2024  = "AWS::SecretsManager-2024-09-16"
	transformServerless          = "AWS::Serverless-2016-10-31"
	transformServiceCatalog      = "AWS::ServiceCatalog"
)

// serverlessTypePrefix opens the type of every resource the AWS::Serverless
// transform expands.
const serverlessTypePrefix = "AWS::Serverless::"

// The SAM resource attributes an event source is read through.
const (
	samEvents         = "Events"
	samEventProperty  = "Properties"
	transformMapName  = "Name"
	samFunctionType   = "AWS::Serverless::Function"
	samStateMachineTy = "AWS::Serverless::StateMachine"
)

// hostedTransforms is every transform CloudFormation hosts.
var hostedTransforms = []string{
	transformCodeDeployBlueGreen, transformInclude, transformLanguageExtensions,
	transformSecretsManager2020, transformSecretsManager2024, transformServerless,
	transformServiceCatalog,
}

// transformContributor is what one contributor to an assembled stack brings to its
// Transform: the transforms it declares, each fragment's own order of them, and the
// facts about its content the dispositions read.
type transformContributor struct {
	node string
	// names are the transforms the contributor declares, each once, in the order its
	// fragments first declare them.
	names []string
	// orders are each declaring fragment's own list, in order.
	orders [][]string
	// written is the Transform exactly as the one declaring fragment wrote it, kept
	// for a stack whose only contributor declares it in only one fragment.
	written any
	// serverless reports that the contributor declares an AWS::Serverless::*
	// resource, which the AWS::Serverless transform expands wherever it comes from.
	serverless bool
	// globals are the contributor's Globals entries, carried only where the
	// contributor itself declares the AWS::Serverless transform.
	globals []stackEntry
	// resources are the logical IDs the contributor declares.
	resources map[string]bool
	// eventTargets are the logical IDs the event sources of its SAM resources name,
	// each with the resource and event naming it.
	eventTargets []samEventTarget
}

// samEventTarget is one logical ID a SAM resource's event source names.
type samEventTarget struct {
	resource string
	event    string
	target   string
}

// addFragment reads one decoded fragment into the contributor.
func (c *transformContributor) addFragment(frag map[string]any) error {
	names, err := fragmentTransforms(frag)
	if err != nil {
		return fmt.Errorf("cloudformation-provider: component %q: %w", c.node, err)
	}
	if len(names) > 0 {
		c.orders = append(c.orders, names)
		if len(c.orders) == 1 {
			c.written = frag[sectionTransform]
		}
		for _, n := range names {
			if !slices.Contains(c.names, n) {
				c.names = append(c.names, n)
			}
		}
	}
	if globals, ok := frag[sectionGlobals].(map[string]any); ok {
		for _, k := range sortedKeys(globals) {
			c.globals = append(c.globals, stackEntry{key: k, value: globals[k]})
		}
	}
	resources, _ := frag[sectionResources].(map[string]any)
	if c.resources == nil {
		c.resources = map[string]bool{}
	}
	for _, id := range sortedKeys(resources) {
		c.resources[id] = true
		body, ok := resources[id].(map[string]any)
		if !ok {
			continue
		}
		typ, _ := body["Type"].(string)
		if !strings.HasPrefix(typ, serverlessTypePrefix) {
			continue
		}
		c.serverless = true
		if typ != samFunctionType && typ != samStateMachineTy {
			continue
		}
		props, _ := body[resourceProperty].(map[string]any)
		events, _ := props[samEvents].(map[string]any)
		for _, event := range sortedKeys(events) {
			eventBody, _ := events[event].(map[string]any)
			walkReferences(eventBody[samEventProperty], nil, func(r resourceReference) {
				c.eventTargets = append(c.eventTargets, samEventTarget{resource: id, event: event, target: r.id})
			})
		}
	}
	return nil
}

// fragmentTransforms returns the transforms one fragment declares, in its own
// order: a single string, a list of them, or the mapping form AWS::Include and a
// macro taking parameters are written in, read by its Name.
func fragmentTransforms(frag map[string]any) ([]string, error) {
	raw, present := frag[sectionTransform]
	if !present {
		return nil, nil
	}
	items := []any{raw}
	if list, ok := raw.([]any); ok {
		items = list
	}
	names := make([]string, 0, len(items))
	for _, item := range items {
		switch v := item.(type) {
		case string:
			names = append(names, v)
		case map[string]any:
			name, ok := v[transformMapName].(string)
			if !ok || name == "" {
				return nil, fmt.Errorf("a %s entry written as a mapping carries no %s naming the transform", sectionTransform, transformMapName)
			}
			names = append(names, name)
		default:
			return nil, fmt.Errorf("a %s entry is neither a transform's name nor a mapping naming one", sectionTransform)
		}
	}
	return names, nil
}

// assembleTransform decides the assembled stack's Transform from its contributors:
// the value to write, or nil where no contributor declares one, and whether SAM's
// Globals are carried.
//
// Each transform takes the disposition CloudFormation's own documentation of it
// supports ([transformDisposition]); a set the stack cannot carry without changing
// how another contributor's content deploys is refused, naming the transform and
// every contributor. A stack whose only contributor declares a Transform in one
// fragment keeps it exactly as written. Otherwise the value is the union of every
// contributor's transforms, in CloudFormation's documented order — a macro an
// account defines before the transforms CloudFormation hosts, AWS::LanguageExtensions
// before AWS::Serverless — with each contributor's own order kept and anything
// left unordered in the order the contributors first declare it, so the same
// contributors give the same bytes. Two orders that cannot both hold halt. One
// transform is written as a string, several as a list.
func assembleTransform(contributors []*transformContributor) (any, bool, error) {
	var declaring []*transformContributor
	for _, c := range contributors {
		if len(c.names) > 0 {
			declaring = append(declaring, c)
		}
	}
	if len(declaring) == 0 {
		return nil, false, nil
	}
	if err := transformDisposition(contributors, declaring); err != nil {
		return nil, false, err
	}
	carryGlobals := false
	for _, c := range declaring {
		if slices.Contains(c.names, transformServerless) {
			carryGlobals = true
		}
	}
	if len(contributors) == 1 && len(declaring[0].orders) == 1 {
		return declaring[0].written, carryGlobals, nil
	}
	union, err := transformUnion(declaring)
	if err != nil {
		return nil, false, err
	}
	if len(union) == 1 {
		return union[0], carryGlobals, nil
	}
	list := make([]any, 0, len(union))
	for _, n := range union {
		list = append(list, n)
	}
	return list, carryGlobals, nil
}

// transformDisposition refuses a transform the assembled stack cannot carry,
// following what CloudFormation documents each one to process:
//
//   - AWS::CodeDeployBlueGreen, always: its templates may declare no Outputs and no
//     Fn::ImportValue, and the assembled stack carries the Outputs its contributors
//     publish and the imports a cross-stack link writes.
//   - AWS::Include, always: it inserts content fetched from S3 when the stack
//     deploys, which no build can read.
//   - a macro an account defines, beside any other contributor: a macro may process
//     the whole template, and what it does is unknown to the build.
//   - AWS::ServiceCatalog, beside any other contributor: it substitutes its
//     reference pattern anywhere in the template.
//   - AWS::LanguageExtensions, beside another contributor's serverless content:
//     AWS::Serverless names what it generates differently once language extensions
//     have resolved its values.
//   - AWS::Serverless: Globals beside another contributor's serverless resources,
//     which they would apply to; and an event source naming a resource another
//     contributor declares, which the transform rewrites.
//   - AWS::SecretsManager is carried: it processes only rotation schedules that
//     ask for it.
func transformDisposition(contributors, declaring []*transformContributor) error {
	all := contributorList(contributors)
	refuse := func(transform, why string) error {
		return cerrors.Wrap(plugincondition.ErrPluginCannotExpressInput(),
			cerrors.TranslationError, cerrors.SourceLocation{},
			fmt.Sprintf("cloudformation-provider: the assembled stack of %s cannot carry the transform %q, declared by %s: %s",
				all, transform, contributorList(declaringOf(declaring, transform)), why))
	}
	multi := len(contributors) > 1
	for _, c := range declaring {
		for _, t := range c.names {
			switch {
			case t == transformCodeDeployBlueGreen:
				return refuse(t, "CloudFormation refuses Outputs and Fn::ImportValue anywhere in a template that declares it, and the assembled stack carries the Outputs its components publish; deploy it as a stack of its own, outside this build")
			case t == transformInclude:
				return refuse(t, "it inserts content fetched from S3 when the stack deploys, which the build cannot read; write the content into the template instead")
			case !slices.Contains(hostedTransforms, t) && multi:
				return refuse(t, "it is a macro the account defines rather than one CloudFormation hosts, it may process the whole template, and what it does to the other components' content is unknown to the build; place the component in a boundary of its own")
			case t == transformServiceCatalog && multi:
				return refuse(t, "it substitutes its reference pattern anywhere in the template, which reaches the other components' content; place the component in a boundary of its own")
			case t == transformLanguageExtensions:
				for _, d := range contributors {
					if d != c && (d.serverless || slices.Contains(d.names, transformServerless)) {
						return refuse(t, fmt.Sprintf("component %q declares serverless content, and AWS::Serverless names what it generates differently once language extensions have resolved its values; declare both transforms in one component, or place them in different boundaries", d.node))
					}
				}
			}
		}
	}
	for _, c := range declaring {
		if !slices.Contains(c.names, transformServerless) {
			continue
		}
		for _, d := range contributors {
			if d == c || !d.serverless || len(c.globals) == 0 {
				continue
			}
			return refuse(transformServerless, fmt.Sprintf("component %q declares Globals, which apply to every serverless resource in the template, and component %q declares serverless resources of its own; move the Globals onto the resources they are meant for, or place the components in different boundaries", c.node, d.node))
		}
		for _, target := range c.eventTargets {
			if c.resources[target.target] {
				continue
			}
			for _, d := range contributors {
				if d != c && d.resources[target.target] {
					return refuse(transformServerless, fmt.Sprintf("the event %q of resource %q in component %q names resource %q, which component %q declares, and the transform rewrites the resource an event names; declare the resource in the same component as its event, or place the components in different boundaries", target.event, target.resource, c.node, target.target, d.node))
				}
			}
		}
	}
	return nil
}

// transformUnion orders every declared transform: each contributor's own order,
// CloudFormation's documented order, and otherwise the order the contributors first
// declare them in. Two orders that cannot both hold halt, naming the transforms and
// the contributors that ordered them.
func transformUnion(declaring []*transformContributor) ([]string, error) {
	var names []string
	for _, c := range declaring {
		for _, n := range c.names {
			if !slices.Contains(names, n) {
				names = append(names, n)
			}
		}
	}
	type pair struct{ first, second string }
	orderedBy := map[pair][]string{}
	add := func(first, second, by string) {
		p := pair{first, second}
		if !slices.Contains(orderedBy[p], by) {
			orderedBy[p] = append(orderedBy[p], by)
		}
	}
	for _, c := range declaring {
		for _, order := range c.orders {
			for i := range order {
				for j := i + 1; j < len(order); j++ {
					if order[i] != order[j] {
						add(order[i], order[j], fmt.Sprintf("component %q", c.node))
					}
				}
			}
		}
	}
	const documented = "CloudFormation's documented order"
	for _, a := range names {
		for _, b := range names {
			if a == b {
				continue
			}
			if a == transformLanguageExtensions && b == transformServerless {
				add(a, b, documented)
			}
			if !slices.Contains(hostedTransforms, a) && slices.Contains(hostedTransforms, b) {
				add(a, b, documented)
			}
		}
	}
	for _, a := range names {
		for _, b := range names {
			forward, backward := orderedBy[pair{a, b}], orderedBy[pair{b, a}]
			if a < b && len(forward) > 0 && len(backward) > 0 {
				return nil, cerrors.Wrap(plugincondition.ErrPluginRequiredInputUnresolved(),
					cerrors.TranslationError, cerrors.SourceLocation{},
					fmt.Sprintf("cloudformation-provider: the transforms %q and %q are ordered both ways — %q before %q by %s, and %q before %q by %s — and the assembled stack's Transform runs them in one order; declare them in one order",
						a, b, a, b, joinPhrases(forward), b, a, joinPhrases(backward)))
			}
		}
	}

	var out []string
	placed := map[string]bool{}
	for len(out) < len(names) {
		next := ""
		for _, candidate := range names {
			if placed[candidate] {
				continue
			}
			ready := true
			for _, before := range names {
				if !placed[before] && before != candidate && len(orderedBy[pair{before, candidate}]) > 0 {
					ready = false
					break
				}
			}
			if ready {
				next = candidate
				break
			}
		}
		if next == "" {
			var left []string
			for _, n := range names {
				if !placed[n] {
					left = append(left, fmt.Sprintf("%q", n))
				}
			}
			return nil, cerrors.Wrap(plugincondition.ErrPluginRequiredInputUnresolved(),
				cerrors.TranslationError, cerrors.SourceLocation{},
				fmt.Sprintf("cloudformation-provider: the components' orders of the transforms %s cannot all hold at once, and the assembled stack's Transform runs them in one order; declare them in one order across the components", joinPhrases(left)))
		}
		placed[next] = true
		out = append(out, next)
	}
	return out, nil
}

// declaringOf returns the contributors declaring one transform.
func declaringOf(declaring []*transformContributor, transform string) []*transformContributor {
	var out []*transformContributor
	for _, c := range declaring {
		if slices.Contains(c.names, transform) {
			out = append(out, c)
		}
	}
	return out
}

// contributorList names contributors for a message, in the order supplied.
func contributorList(contributors []*transformContributor) string {
	names := make([]string, 0, len(contributors))
	for _, c := range contributors {
		names = append(names, fmt.Sprintf("component %q", c.node))
	}
	return joinPhrases(names)
}
