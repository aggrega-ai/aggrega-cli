// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// The assertions every drafted Recipe shares, whatever its target.
const (
	assertOverridesRead     = "overrides_this_recipe_reads"
	assertRequiredOverrides = "required_overrides_set"
	assertSecretsParameters = "secrets_are_parameters"
)

// recipeSpec is what the core settles of one drafted Recipe before its target
// writer drafts the body.
type recipeSpec struct {
	name, implements string
	// component is the Component the Recipe implements.
	component string
	// format is the format of the source the Recipe is drafted from, which its
	// description names.
	format string
	res    Resource
	// rules holds the rule of each input of res, in the inputs' order.
	rules []string
	// secretSlots reports that a secret is delivered on a featuresSchema slot
	// rather than declared as a deploy-time parameter.
	secretSlots bool
	// recipeOnly holds what a Recipe drafted for a catalog Component matched of
	// it; nil for a Recipe drafted beside its Component.
	recipeOnly *recipeOnlyMatch
	// module is the published module a Recipe sourcing one whole is drafted
	// for; nil for a Recipe declaring a resource.
	module *WrappedModule
}

// recipeDraft completes the target parts d a writer drafted for s into the
// Recipe of format f: its name, version, description, capability and
// implements, the header listing the keys it reads ahead of the writer's own
// lines, and the shared assertions ahead of the writer's own.
func recipeDraft(f Format, s recipeSpec, d importer.RecipeDraft) importer.RecipeDraft {
	d.Name, d.Version, d.Capability, d.Implements = s.name, draftVersion, f.Capability, s.implements
	d.Description = fmt.Sprintf("Draft Recipe realizing %s on %s with one resource of the type %s, imported from a "+
		"source in the %s format.", s.component, f.Capability, typeName(s.res.Type), s.format)
	if s.module != nil {
		d.Description = fmt.Sprintf("Draft Recipe realizing %s on %s that sources the published module %s whole, "+
			"imported from a source in the %s format.", s.component, f.Capability, s.module.label(), s.format)
	}
	d.Header = append(recipeHeader(s), d.Header...)
	d.Assertions = append(sharedAssertions(s), d.Assertions...)
	return d
}

// recipeHeader lists every key the Recipe reads, each on one line with its type,
// whether it is required and its rule, followed by a line quoting the first
// sentence of the vendor's description, marked, or saying the vendor schema, or
// the source writing the resource, gives none; on a Recipe drafted for a catalog
// Component, each of its values keys the Recipe reads nothing for, and each of
// its provides keys that halts until it is mapped. It opens saying what each
// line gives: for a resource read from a source writing it, whose properties
// are Written, no vendor's description.
func recipeHeader(s recipeSpec) []string {
	t := s.res.Type
	opening := fmt.Sprintf("This Recipe realizes %s with one resource of the type %s. It reads the keys below, "+
		"each with its type, its rule and the first sentence of the vendor's description; any other key under "+
		"infra.overrides halts the build naming it.", s.component, typeName(t))
	switch {
	case s.module != nil:
		opening = fmt.Sprintf("This Recipe realizes %s by sourcing the published module %s whole; no vendor schema "+
			"was read. It reads the keys below, each with its type and its rule; any other key under "+
			"infra.overrides halts the build naming it.", s.component, s.module.label())
	case slices.ContainsFunc(t.Properties, func(p *Property) bool { return p.Written }):
		opening = fmt.Sprintf("This Recipe realizes %s with one resource of the type %s, drafted from the "+
			"resource as a source writes it. It reads the keys below, each with its type and its rule, and none "+
			"with a vendor's description, which a source does not state; any other key under infra.overrides "+
			"halts the build naming it.", s.component, typeName(t))
	}
	lines := []string{opening}
	for _, in := range s.res.Inputs {
		if in.Source.Pillar == PillarSecret && !s.secretSlots {
			lines = append(lines, "A secret is a deploy-time parameter whose name is the instance's name in "+
				"capitalized words, written <instance> below, followed by the name the line gives.")
			break
		}
	}
	for i, in := range s.res.Inputs {
		line := s.readFrom(in) + ": " + kindNames(in.Property.Kinds)
		if in.Required {
			line += ", required"
		}
		lines = append(lines, line+"; "+s.rules[i], firstSentenceOf(in.Property))
	}
	if s.recipeOnly != nil {
		for _, key := range s.recipeOnly.Unmatched {
			lines = append(lines, fmt.Sprintf("values.%s of %s matches no property of %s this Recipe writes, so it "+
				"reads nothing for it.", key, s.component, typeName(t)))
		}
		for _, u := range s.res.Unmapped {
			lines = append(lines, fmt.Sprintf("provides.%s of %s matches no attribute %s reads back, so it halts "+
				"the build until it is mapped.", u.Key, s.component, typeName(t)))
		}
	}
	return lines
}

// readFrom names where the Recipe reads in from.
func (s recipeSpec) readFrom(in Input) string {
	switch in.Source.Pillar {
	case PillarValues:
		return "values." + in.Source.Key
	case PillarInfra:
		return "infra." + in.Source.Key
	case PillarOverrides:
		return "infra.overrides." + in.Source.Key
	case PillarSecret:
		if s.secretSlots {
			return "features." + in.Source.Key
		}
		return "parameter <instance>" + SecretParameter(in.Property)
	case PillarInject:
		return "context.inject." + in.Source.Mechanism + "." + in.Source.Key
	}
	return in.Source.Key
}

// sharedAssertions are the assertions a drafted Recipe holds whatever its
// target: overrides_this_recipe_reads, admitting exactly the override names it
// reads; on a Recipe drafted for a catalog Component that requires overrides,
// required_overrides_set; and where secrets are deploy-time parameters and the
// Recipe reads one, secrets_are_parameters.
func sharedAssertions(s recipeSpec) []importer.Entry {
	t := s.res.Type
	var overrides, secretKeys []string
	var secrets []Input
	for _, in := range s.res.Inputs {
		switch in.Source.Pillar {
		case PillarOverrides:
			overrides = append(overrides, in.Source.Key)
		case PillarSecret:
			secrets = append(secrets, in)
			secretKeys = append(secretKeys, in.Source.Key)
		}
	}

	reads := "nothing"
	if len(overrides) > 0 {
		reads = andList(overrides)
	}
	out := []importer.Entry{{
		Key:     assertOverridesRead,
		Comment: []string{"Halts naming any key under infra.overrides this Recipe does not read."},
		Value: lines(
			`{{- $o := optional .infra "overrides" dict -}}`,
			`{{- range $k, $v := $o -}}`,
			`{{- if not (has $k (list`+quotedList(overrides)+`)) -}}`,
			`{{- fail (printf "infra.overrides.%s is not a setting this Recipe reads: it reads %s from `+
				`infra.overrides; remove infra.overrides.%s from the Release" $k `+strconv.Quote(reads)+` $k) -}}`,
			`{{- end -}}`,
			`{{- end -}}`),
	}}

	if s.recipeOnly != nil && len(s.recipeOnly.Required) > 0 {
		out = append(out, importer.Entry{
			Key: assertRequiredOverrides,
			Comment: []string{fmt.Sprintf("Halts until the Release sets each property %s requires that no values "+
				"key of %s carries.", typeName(t), s.component)},
			Value: lines(
				`{{- $o := optional .infra "overrides" dict -}}`,
				`{{- range $k := list`+quotedList(s.recipeOnly.Required)+` -}}`,
				`{{- if not (hasKey $o $k) -}}`,
				`{{- fail (printf "infra.overrides.%s is not set, and %s requires it: %s declares no values key `+
					`of its name, so this Recipe reads it from infra.overrides; set infra.overrides.%s in the `+
					`Release" $k `+strconv.Quote(typeName(t))+` `+strconv.Quote(s.component)+` $k) -}}`,
				`{{- end -}}`,
				`{{- end -}}`),
		})
	}

	if !s.secretSlots && len(secrets) > 0 {
		body := []string{`{{- $f := default dict .features -}}`}
		for _, in := range secrets {
			body = append(body,
				`{{- if hasKey $f `+strconv.Quote(in.Source.Key)+` -}}`,
				`{{- fail (printf "a secret is attached under %s, and this Recipe declares the secret %s of %s as `+
					`the deploy-time parameter %s%s, supplied when the template is deployed: remove the attachment `+
					`and supply the value to that parameter when deploying" `+strconv.Quote(in.Source.Key)+` `+
					strconv.Quote(in.Property.Name)+` `+strconv.Quote(typeName(t))+
					` (capitalizedWords .metadata.instance_id) `+strconv.Quote(SecretParameter(in.Property))+`) -}}`,
				`{{- end -}}`)
		}
		out = append(out, importer.Entry{
			Key: assertSecretsParameters,
			Comment: []string{"Halts on an attachment under " + andList(secretKeys) + ": this Recipe declares " +
				"each secret as a deploy-time parameter instead."},
			Value: lines(body...),
		})
	}
	return out
}

// lines joins template lines into one multi-line text, which the Recipe writer
// writes as a literal block.
func lines(ls ...string) string { return strings.Join(ls, "\n") + "\n" }

// quotedList writes names as the arguments of a template list, each quoted and
// opened by a space.
func quotedList(names []string) string {
	var b strings.Builder
	for _, n := range names {
		b.WriteString(" " + strconv.Quote(n))
	}
	return b.String()
}

// andList joins items for a sentence: "a", "a and b", "a, b and c".
func andList(items []string) string {
	switch len(items) {
	case 0:
		return "none"
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// kindNames names the kinds of a property for the header.
func kindNames(kinds []Kind) string {
	if len(kinds) == 0 {
		return string(KindAny)
	}
	names := make([]string, len(kinds))
	for i, k := range kinds {
		names[i] = string(k)
	}
	return strings.Join(names, " or ")
}

// firstSentenceOf is the first sentence of p's vendor description, marked, or
// composed text saying the vendor schema gives none.
func firstSentenceOf(p *Property) string {
	text := strings.TrimSpace(p.Description)
	if text == "" {
		return describedBy(p)
	}
	for i := 0; i+1 < len(text); i++ {
		if text[i] == '.' && (text[i+1] == ' ' || text[i+1] == '\n' || text[i+1] == '\t') {
			text = text[:i+1]
			break
		}
	}
	return Marked(text)
}
