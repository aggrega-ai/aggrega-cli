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
// under the resource's properties, which is written where it holds one. An
// input read from values or infra is rebuilt under the vendor's names
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
// a key read back below properties is reference() of the symbol and the path
// below it; any other read-back key is the full reference() of the symbol at
// the apiVersion, and its path. A key mapped to no attribute is written as the
// fail Unmapped.Value gives. An input read from the inject pipe other than at or
// under properties halts resource-type-refused naming it, since ARM evaluates a
// reference to another resource only there.
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
		target := resourceVar
		if underProperties(in.Property) {
			target = propertiesVar
		}
		statements, value := inputValue(&respeller, in)
		set := "{{ $_ := set " + target + " " + strconv.Quote(in.Property.Name) + " " + value + " }}"
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
		if in.Source.Pillar != schemadraft.PillarInject || len(in.Property.Path) > 0 && in.Property.Path[0] == keyProperties {
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
	i := slices.IndexFunc(r.Inputs, func(in schemadraft.Input) bool {
		return slices.Equal(in.Property.Path, []string{keyName}) && !in.Secret
	})
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
