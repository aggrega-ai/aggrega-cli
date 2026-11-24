// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package arm

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

const (
	// symbolLocal is the local holding the resource's symbolic name, and
	// symbol the template text reading it.
	symbolLocal = "symbol"
	symbol      = "{{ .locals.symbol }}"
	// templateArtifact is the fragment the arm provider merges into the
	// boundary's template.
	templateArtifact = "template.yaml"
	// resourceVar and propertiesVar are the variables the fragment builds the
	// resource in: its root keys, and the members of its properties.
	resourceVar   = "$resource"
	propertiesVar = "$properties"
)

// The parameter types a deploy-time secret is declared with.
const (
	secureString = "securestring"
	secureObject = "secureObject"
)

// Write drafts the arm parts of the Recipe declaring one resource of r's type:
// the local symbol, the instance's name in capitalized words; the artifact
// template.yaml, a fragment declaring the resource under that symbolic name; and
// the provides, each one whole ARM expression. It writes no outputs: the
// provider adds an output for a provided key that a consumer reads from another
// template.
//
// The fragment builds the resource as a dict, from its type, the apiVersion
// the file names and each input, and writes it with toJson: a root key on the
// resource, and a member of properties, its path opening with properties,
// under the resource's properties, which is written where it holds one, each by
// its name. An input at a deeper path is written after them, the shallower
// first, at its whole path inside the value written there: the member of the
// root key or of properties it opens with is copied, so the Release's value is
// left as it is, each object the path passes through and the value lacks is
// created, each array item it names in decimal must be in the list, the
// rendering halting naming the path where it is not, and the value replaces the
// member or the item the path ends at. So an override writes a property whole
// and an inject input sets one member of it. An input read from values or infra
// is rebuilt under the vendor's names
// (schemadraft.Respeller), and one read from infra.overrides or the inject pipe
// is written as it is read, so a neighbour's reference on the pipe lands in
// place as the expression it is. An input that is not required is written only
// where it is present, so one set to false, 0 or "" is written as set. A secret
// is always written: a top-level parameter of type securestring, or
// secureObject for an object, with no default, named the symbol followed by
// schemadraft.SecretParameter, which the property reads with parameters(); ARM
// takes its value when the template is deployed.
//
// The fragment is written for the narrowest scope the type is writable at that
// the provider deploys at: a resource group, writing no $schema; else a
// subscription or a management group, writing that scope's $schema, the header
// naming the boundary annotation the Release sets for it.
//
// A provides key the type's identifier carries is the resource ID, built for the
// fragment's scope from the type and the resource's name split at each slash;
// a key reading the root name is the name as the fragment writes it, which ARM
// reads as the resource's name does, a child type's name written whole as
// parent/child; a key read back below properties is
// reference() of the symbol and the path below it; any other read-back key is
// the full reference() of the symbol at the apiVersion, and its path. A key
// mapped to no attribute is written as the fail Unmapped.Value gives. An input
// read from the inject pipe other than at or under properties halts
// resource-type-refused naming it, since ARM evaluates a reference to another
// resource only there.
func Write(r schemadraft.Resource) (importer.RecipeDraft, []aggrega.Diagnostic) {
	if halts := injectOutsideProperties(r); len(halts) > 0 {
		return importer.RecipeDraft{}, halts
	}
	scope := contentScope(r.Type)
	d := importer.RecipeDraft{
		Header: header(scope),
		Locals: []importer.Entry{{
			Key:   symbolLocal,
			Value: "{{ capitalizedWords .context.instance_id }}",
			Comment: []string{"The resource's symbolic name: the instance's name in capitalized words, letters and " +
				"digits alone, so two instances in one template declare two resources."},
		}},
	}
	for _, p := range r.Provides {
		d.Provides = append(d.Provides, importer.Entry{Key: p.Key, Value: readBack(r, p, scope)})
	}
	for _, u := range r.Unmapped {
		d.Provides = append(d.Provides, importer.Entry{Key: u.Key, Value: u.Value(r.Type)})
	}
	if len(d.Provides) > 0 {
		d.Provides[0].Comment = []string{"The String Pipe: each value is one whole ARM expression, piped as written " +
			"into a consumer's context; ARM evaluates it when the template is deployed."}
		if slices.ContainsFunc(r.Provides, readsName) {
			d.Provides[0].Comment = []string{"The String Pipe: each value is one whole ARM expression, or the " +
				"resource's name as template.yaml writes it, piped as written into a consumer's context; ARM " +
				"evaluates it when the template is deployed."}
		}
	}
	d.Artifacts = []importer.Entry{{Key: templateArtifact, Value: fragment(r, scope)}}
	return d, nil
}

// header is the writer's own header lines for a fragment written for scope.
func header(scope armcore.Scope) []string {
	lines := []string{"template.yaml declares the resource under the symbolic name locals.symbol holds, and the arm " +
		"provider merges it into the template of the instance's boundary."}
	switch scope {
	case armcore.ScopeSubscription:
		lines = append(lines, fmt.Sprintf("The type is written for a subscription: template.yaml's $schema names the "+
			"subscription scope, so the Release annotates the instance's boundary %s: %s, or %s.",
			armcore.AnnotationDeploymentScope, armcore.ScopeSubscription, armcore.ScopeManagementGroup))
	case armcore.ScopeManagementGroup:
		lines = append(lines, fmt.Sprintf("The type is written for a management group: template.yaml's $schema "+
			"names the management group scope, so the Release annotates the instance's boundary %s: %s.",
			armcore.AnnotationDeploymentScope, armcore.ScopeManagementGroup))
	}
	return lines
}

// schemaOf is the $schema a fragment written for scope declares; none for a
// resource group.
func schemaOf(scope armcore.Scope) string {
	switch scope {
	case armcore.ScopeSubscription:
		return armcore.SchemaSubscription
	case armcore.ScopeManagementGroup:
		return armcore.SchemaManagementGroup
	}
	return ""
}

// fragment is the text of template.yaml for r, written for scope. Each line
// building the resource ends by trimming the line break after it, so the
// fragment opens with its first section.
func fragment(r schemadraft.Resource, scope armcore.Scope) string {
	var b strings.Builder
	var respeller schemadraft.Respeller
	t := r.Type
	b.WriteString("{{- " + resourceVar + " := dict \"type\" " + strconv.Quote(t.Name) + " \"apiVersion\" " +
		strconv.Quote(t.APIVersion) + " -}}\n")
	if slices.ContainsFunc(r.Inputs, func(in schemadraft.Input) bool { return underProperties(in.Property) }) {
		b.WriteString("{{- " + propertiesVar + " := dict -}}\n")
	}
	var secrets []schemadraft.Input
	for _, in := range r.Inputs {
		if in.Secret {
			secrets = append(secrets, in)
		}
	}
	for n, in := range placementOrder(r.Inputs) {
		target := resourceVar
		if underProperties(in.Property) {
			target = propertiesVar
		}
		statements, value := inputValue(&respeller, in)
		set := "{{ $_ := set " + target + " " + strconv.Quote(in.Property.Name) + " " + value + " }}"
		if target, below, ok := nestedPath(in.Property); ok {
			set = setAt(target, below, value, n)
		}
		if in.Required || in.Secret {
			b.WriteString(statements + strings.TrimSuffix(set, " }}") + " -}}\n")
			continue
		}
		b.WriteString("{{ if " + in.Source.Present() + " }}" + statements + set + "{{ end -}}\n")
	}
	if slices.ContainsFunc(r.Inputs, func(in schemadraft.Input) bool { return underProperties(in.Property) }) {
		b.WriteString("{{ if " + propertiesVar + " }}{{ $_ := set " + resourceVar + " " + strconv.Quote(keyProperties) +
			" " + propertiesVar + " }}{{ end -}}\n")
	}
	if schema := schemaOf(scope); schema != "" {
		b.WriteString("$schema: " + importer.QuotedString(schema) + "\n")
	}
	if len(secrets) > 0 {
		b.WriteString("parameters:\n")
		for _, in := range secrets {
			b.WriteString("  " + symbol + schemadraft.SecretParameter(in.Property) + ":\n    type: " +
				secretType(in.Property) + "\n")
		}
	}
	b.WriteString("resources:\n  " + symbol + ": {{ toJson " + resourceVar + " }}\n")
	return b.String()
}

// underProperties reports whether p is a member of the resource's properties,
// written under it.
func underProperties(p *schemadraft.Property) bool {
	return len(p.Path) > 1 && p.Path[0] == keyProperties
}

// nestedPath returns, for p at a path deeper than a root key or a member of
// properties, the variable holding the object its path opens in and its path
// below that object; ok is false for any other p.
func nestedPath(p *schemadraft.Property) (target string, below []string, ok bool) {
	switch {
	case underProperties(p) && len(p.Path) > 2:
		return propertiesVar, p.Path[1:], true
	case !underProperties(p) && len(p.Path) > 1:
		return resourceVar, p.Path, true
	}
	return "", nil, false
}

// placementOrder is inputs in the order the fragment writes them: each root key
// and member of properties in the inputs' order, then each input at a deeper
// path, the shallower first and in the inputs' order among paths of one depth,
// so a value is written before a member set inside it.
func placementOrder(inputs []schemadraft.Input) []schemadraft.Input {
	var out, nested []schemadraft.Input
	for _, in := range inputs {
		if _, _, ok := nestedPath(in.Property); ok {
			nested = append(nested, in)
		} else {
			out = append(out, in)
		}
	}
	slices.SortStableFunc(nested, func(a, b schemadraft.Input) int {
		return len(a.Property.Path) - len(b.Property.Path)
	})
	return append(out, nested...)
}

// setAt is the template text setting the value value evaluates to at the path
// below inside the object the variable target holds, its variables numbered n.
// The first segment of below is a member of target, whose value is copied, or
// created as an object where target lacks it; a later segment of decimal digits
// is an item of the list it reads, any other a member of the object it reads,
// created where the object lacks it. Where a value on the path is not the list
// or object a segment reads, or the list holds no such item, it halts naming the
// path. The value replaces the member or item the path ends at; a list whose
// item it replaces is rebuilt and set where the list was read, up to the first
// object. Its last action is a set, ending " }}".
func setAt(target string, below []string, value string, n int) string {
	where := func(i int) string {
		path := below[:i]
		if target == propertiesVar {
			path = append([]string{keyProperties}, path...)
		}
		return strings.Join(path, ".")
	}
	whole := where(len(below))
	suffix := strconv.Itoa(n)
	held := func(i int) string { // the variable holding the value at below[:i]
		if i == 0 {
			return target
		}
		return "$at" + suffix + "_" + strconv.Itoa(i)
	}
	set := "$set" + suffix
	fail := func(text string) string { return "{{ fail " + strconv.Quote(text) + " }}" }
	isMap := func(i int) string {
		return "{{ if not (kindIs \"map\" " + held(i) + ") }}" + fail(fmt.Sprintf("%s is not an object, and this "+
			"Recipe writes %s inside it", where(i), whole)) + "{{ end }}"
	}
	isList := func(i, item int) string {
		return "{{ if not (and (kindIs \"slice\" " + held(i) + ") (gt (len " + held(i) + ") " + strconv.Itoa(item) +
			")) }}" + fail(fmt.Sprintf("%s is not a list holding the item %d, and this Recipe writes %s into that "+
			"item: keep the item in the value the Release sets", where(i), item, whole)) + "{{ end }}"
	}

	var b strings.Builder
	b.WriteString("{{ " + set + " := " + value + " }}")
	first := strconv.Quote(below[0])
	b.WriteString("{{ if hasKey " + target + " " + first + " }}{{ $_ := set " + target + " " + first +
		" (deepCopy (index " + target + " " + first + ")) }}{{ else }}{{ $_ := set " + target + " " + first +
		" dict }}{{ end }}{{ " + held(1) + " := index " + target + " " + first + " }}")
	last := len(below) - 1
	for i := 1; i < last; i++ {
		if item, ok := itemIndex(below[i]); ok {
			b.WriteString(isList(i, item) + "{{ " + held(i+1) + " := index " + held(i) + " " + strconv.Itoa(item) + " }}")
			continue
		}
		member := strconv.Quote(below[i])
		b.WriteString(isMap(i) + "{{ if not (hasKey " + held(i) + " " + member + ") }}{{ $_ := set " + held(i) + " " +
			member + " dict }}{{ end }}{{ " + held(i+1) + " := index " + held(i) + " " + member + " }}")
	}

	// From the path's end up: an object takes its member in place and ends the
	// walk; a list is rebuilt with its item replaced, and the rebuilt list is
	// set where the list was read.
	replacing := set
	for i := last; ; i-- {
		item, ok := itemIndex(below[i])
		if !ok || i == 0 {
			if i > 0 {
				b.WriteString(isMap(i))
			}
			b.WriteString("{{ $_ := set " + held(i) + " " + strconv.Quote(below[i]) + " " + replacing + " }}")
			return b.String()
		}
		list := "$list" + suffix + "_" + strconv.Itoa(i)
		if i == last {
			b.WriteString(isList(i, item))
		}
		b.WriteString("{{ " + list + " := list }}{{ range $i, $item := " + held(i) + " }}{{ if eq $i " +
			strconv.Itoa(item) + " }}{{ " + list + " = append " + list + " " + replacing + " }}{{ else }}{{ " + list +
			" = append " + list + " $item }}{{ end }}{{ end }}")
		replacing = list
	}
}

// itemIndex reads segment as the index of an array item, written in decimal.
func itemIndex(segment string) (int, bool) {
	if segment == "" || strings.Trim(segment, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(segment)
	return n, err == nil
}

// inputValue returns the statements building in's value and the expression
// holding it.
func inputValue(respeller *schemadraft.Respeller, in schemadraft.Input) (statements, value string) {
	switch {
	case in.Secret:
		return "", "(printf \"[parameters('%s%s')]\" .locals." + symbolLocal + " " +
			strconv.Quote(schemadraft.SecretParameter(in.Property)) + ")"
	case in.Source.Pillar == schemadraft.PillarValues || in.Source.Pillar == schemadraft.PillarInfra:
		return respeller.Value(in.Property, in.Source.Template())
	}
	return "", in.Source.Template()
}

// secretType is the type of the parameter declaring the secret p.
func secretType(p *schemadraft.Property) string {
	if len(p.Kinds) == 1 && p.Kinds[0] == schemadraft.KindObject {
		return secureObject
	}
	return secureString
}

// injectOutsideProperties halts on each input of r read from the inject pipe
// other than at or under the resource's properties, where ARM evaluates a
// reference to another resource.
func injectOutsideProperties(r schemadraft.Resource) []aggrega.Diagnostic {
	var out []aggrega.Diagnostic
	for _, in := range r.Inputs {
		if in.Source.Pillar != schemadraft.PillarInject || atOrUnderProperties(in.Property) {
			continue
		}
		out = append(out, aggrega.Diagnostic{
			Severity: aggrega.SeverityHalt,
			Code:     schemadraft.CodeResourceTypeRefused,
			Message: fmt.Sprintf("the input %s of %s@%s is read from context.inject.%s.%s, and ARM evaluates a "+
				"reference to another resource only in a resource's properties: read %s from values or infra instead",
				strings.Join(in.Property.Path, "."), r.Type.Name, r.Type.APIVersion, in.Source.Mechanism, in.Source.Key,
				in.Property.Name),
			SourceRef: r.Type.Source,
		})
	}
	return out
}

// readBack is the provides value of p, read back from the resource r, written
// for scope.
func readBack(r schemadraft.Resource, p schemadraft.Provide, scope armcore.Scope) string {
	switch {
	case p.Identifier:
		return resourceID(r, p, scope)
	case readsName(p):
		return nameOf(r, p)
	case len(p.Attribute) > 1 && p.Attribute[0] == keyProperties:
		return "[reference('" + symbol + "')" + access(p.Attribute[1:]) + "]"
	}
	return "[reference('" + symbol + "', " + importer.EscapeTemplateText(armcore.StringLiteral(r.Type.APIVersion)) +
		", 'Full')" + access(p.Attribute) + "]"
}

// plainSegment matches a path segment ARM reads after a dot.
var plainSegment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// access is the accessors reading the path below a value: .<segment> where the
// segment is plain, ['<segment>'] otherwise, any template text in it escaped.
func access(path []string) string {
	var b strings.Builder
	for _, segment := range path {
		if plainSegment.MatchString(segment) {
			b.WriteString("." + segment)
		} else {
			b.WriteString("[" + armcore.StringLiteral(segment) + "]")
		}
	}
	return importer.EscapeTemplateText(b.String())
}

// resourceID is the provides value of the resource ID p of r, built for scope
// from the type and each segment of the resource's name between slashes, each
// an ARM string literal: resourceId at a resource group,
// subscriptionResourceId at a subscription, and extensionResourceId of the
// management group at a management group. Where no input holds the name, the
// value halts saying so.
func resourceID(r schemadraft.Resource, p schemadraft.Provide, scope armcore.Scope) string {
	i := nameInput(r)
	if i < 0 {
		return "{{ fail " + strconv.Quote(fmt.Sprintf("provides.%s is the resource ID of %s@%s, built from the "+
			"resource's name, and this Recipe writes no name", p.Key, r.Type.Name, r.Type.APIVersion)) + " }}"
	}
	function := "resourceId("
	switch scope {
	case armcore.ScopeSubscription:
		function = "subscriptionResourceId("
	case armcore.ScopeManagementGroup:
		function = "extensionResourceId(managementGroup().id, "
	}
	return "[" + function + importer.EscapeTemplateText(armcore.StringLiteral(r.Type.Name)) +
		"{{ range splitList \"/\" (toString " + r.Inputs[i].Source.Template() + ") }}, " +
		"'{{ replace \"'\" \"''\" . }}'{{ end }})]"
}

// nameInput is the index of the input of r writing the resource's name, -1
// where none does.
func nameInput(r schemadraft.Resource) int {
	return slices.IndexFunc(r.Inputs, func(in schemadraft.Input) bool {
		return slices.Equal(in.Property.Path, []string{keyName}) && !in.Secret
	})
}

// readsName reports whether p publishes the resource's root name, which is no
// identifier.
func readsName(p schemadraft.Provide) bool {
	return !p.Identifier && slices.Equal(p.Attribute, []string{keyName})
}

// nameOf is the provides value of p, the resource's name: the value of the input
// writing it, as template.yaml writes it, so a child type's name is the whole
// parent/child the template writes. Where no input writes the name, the value
// halts saying so.
func nameOf(r schemadraft.Resource, p schemadraft.Provide) string {
	i := nameInput(r)
	if i < 0 {
		return "{{ fail " + strconv.Quote(fmt.Sprintf("provides.%s is the name of %s@%s, and this Recipe writes no "+
			"name", p.Key, r.Type.Name, r.Type.APIVersion)) + " }}"
	}
	return "{{ toString " + r.Inputs[i].Source.Template() + " }}"
}
