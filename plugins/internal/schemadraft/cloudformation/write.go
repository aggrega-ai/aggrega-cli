// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudformation

import (
	"regexp"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

const (
	// logicalIDLocal is the local holding the resource's logical id, and
	// logicalID the template text reading it.
	logicalIDLocal = "logical_id"
	logicalID      = "{{ .locals.logical_id }}"
	// templateArtifact is the fragment the cloudformation provider merges into
	// the stack.
	templateArtifact = "template.yaml"
)

// Write drafts the cloudformation parts of the Recipe declaring one resource of
// r's type: the local logical_id, the instance's name in capitalized words; the
// artifact template.yaml, a fragment declaring the resource under that logical
// id with one line under Properties for each input, in r's order; and the
// provides, repeated in the fragment's provides block, which the provider
// projects into the stack's Outputs.
//
// An input is written whole, as JSON: an input read from values or infra is
// rebuilt under the vendor's names (schemadraft.Respell), and one read from
// infra.overrides or the inject pipe is written as it is read, so a neighbour's
// reference on the pipe lands in place as the intrinsic it names. An input that
// is not required is written only where it is present, so one set to false, 0
// or "" is written as set; where no input is sure to be written, Properties
// defaults to an empty object. A secret is a deploy-time parameter the fragment
// declares, of type String with NoEcho, named the logical id followed by
// schemadraft.SecretParameter, and the property reads it with !Ref.
//
// A provides key the type's primary identifier carries is the resource's !Ref,
// any other its !GetAtt by the attribute's dotted path; a key mapped to no
// attribute is written as the fail Unmapped.Value gives, in both places. No
// other resource is written.
func Write(r schemadraft.Resource) (importer.RecipeDraft, []aggrega.Diagnostic) {
	d := importer.RecipeDraft{
		Header: []string{"template.yaml declares the resource under the logical id in locals, and the " +
			"cloudformation provider projects its provides block into the stack's Outputs."},
		Locals: []importer.Entry{{
			Key:   logicalIDLocal,
			Value: "{{ capitalizedWords .context.instance_id }}",
			Comment: []string{"The resource's logical id: the instance's name in capitalized words, letters and " +
				"digits alone, so two instances in one stack declare two resources."},
		}},
	}
	var fragmentProvides []string
	for _, p := range r.Provides {
		d.Provides = append(d.Provides, importer.Entry{Key: p.Key, Value: readBack(p)})
		fragmentProvides = append(fragmentProvides, yamlName(p.Key)+": "+fragmentReadBack(p))
	}
	for _, u := range r.Unmapped {
		value := u.Value(r.Type)
		d.Provides = append(d.Provides, importer.Entry{Key: u.Key, Value: value})
		fragmentProvides = append(fragmentProvides, yamlName(u.Key)+": "+value)
	}
	if len(d.Provides) > 0 {
		d.Provides[0].Comment = []string{"The String Pipe: each value is piped as written into a consumer's " +
			"context; the provides block of template.yaml carries the same values into the stack's Outputs."}
	}
	d.Artifacts = []importer.Entry{{Key: templateArtifact, Value: fragment(r, fragmentProvides)}}
	return d, nil
}

// fragment is the text of template.yaml for r, its provides block holding the
// lines given.
func fragment(r schemadraft.Resource, provides []string) string {
	var b strings.Builder
	var secrets []schemadraft.Input
	for _, in := range r.Inputs {
		if in.Secret {
			secrets = append(secrets, in)
		}
	}
	if len(secrets) > 0 {
		b.WriteString("Parameters:\n")
		for _, in := range secrets {
			b.WriteString("  " + parameter(in.Property) + ":\n    Type: String\n    NoEcho: true\n")
		}
	}
	b.WriteString("Resources:\n  " + logicalID + ":\n    Type: " + yamlName(r.Type.Name) + "\n")
	b.WriteString(propertiesLine(r.Inputs))
	for _, in := range r.Inputs {
		line := "      " + yamlName(in.Property.Name) + ": " + inputValue(in) + "\n"
		if in.Required || in.Secret {
			b.WriteString(line)
			continue
		}
		b.WriteString("      {{- if " + in.Source.Present() + " }}\n" + line + "      {{- end }}\n")
	}
	if len(provides) > 0 {
		b.WriteString("provides:\n")
		for _, line := range provides {
			b.WriteString("  " + line + "\n")
		}
	}
	return b.String()
}

// propertiesLine opens the resource's Properties: an empty object where there is
// no input, and where every input is optional, an empty object unless one is
// present.
func propertiesLine(inputs []schemadraft.Input) string {
	if len(inputs) == 0 {
		return "    Properties: {}\n"
	}
	var present []string
	for _, in := range inputs {
		if in.Required || in.Secret {
			return "    Properties:\n"
		}
		present = append(present, in.Source.Present())
	}
	return "    Properties:{{ if not (or " + strings.Join(present, " ") + ") }} {}{{ end }}\n"
}

// inputValue is the template text writing in's value.
func inputValue(in schemadraft.Input) string {
	switch {
	case in.Secret:
		return "!Ref " + parameter(in.Property)
	case in.Source.Pillar == schemadraft.PillarValues || in.Source.Pillar == schemadraft.PillarInfra:
		return schemadraft.Respell(in.Property, in.Source.Template())
	}
	return "{{ toJson " + in.Source.Template() + " }}"
}

// parameter is the template text naming the deploy-time parameter of the
// secret p.
func parameter(p *schemadraft.Property) string {
	return logicalID + schemadraft.SecretParameter(p)
}

// readBack is the provides value of p a Recipe publishes: the resource's !Ref
// for its identifier, its !GetAtt by the attribute's dotted path otherwise.
func readBack(p schemadraft.Provide) string {
	if p.Identifier {
		return "!Ref " + logicalID
	}
	return "!GetAtt " + logicalID + "." + importer.EscapeTemplateText(strings.Join(p.Attribute, "."))
}

// plainAttribute matches the dotted attribute path a plain YAML scalar carries
// after !GetAtt as it is.
var plainAttribute = regexp.MustCompile(`^[A-Za-z0-9]+(\.[A-Za-z0-9]+)*$`)

// fragmentReadBack is readBack written into template.yaml: as the short form
// where the path is plain, and otherwise as the short form quoted, which the
// provider reads alike.
func fragmentReadBack(p schemadraft.Provide) string {
	path := strings.Join(p.Attribute, ".")
	if p.Identifier || plainAttribute.MatchString(path) {
		return readBack(p)
	}
	quoted := importer.QuotedString(importer.EscapeTemplateText(path))
	return `"!GetAtt ` + logicalID + "." + quoted[1:len(quoted)-1] + `"`
}

// plainName matches a vendor name, or a key, a plain YAML scalar carries as it
// is: letters, digits and underscores opening with a letter, segments of them
// joined by ::.
var plainName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(::[A-Za-z0-9_]+)*$`)

// yamlName writes name into template.yaml so it reads back as written: plain
// where a plain scalar carries it, and otherwise double-quoted, any template
// text in it escaped.
func yamlName(name string) string {
	if plainName.MatchString(name) && !yamlWord(name) {
		return name
	}
	return importer.QuotedString(importer.EscapeTemplateText(name))
}

// yamlWord reports whether a YAML reader gives the plain scalar s a meaning
// other than a string.
func yamlWord(s string) bool {
	switch strings.ToLower(s) {
	case "true", "false", "null", "yes", "no", "on", "off", "y", "n":
		return true
	}
	return false
}
