// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// VendorTextMarker opens every text a draft takes from a vendor schema, so the
// vendor's text is found by a search rather than by memory. Text the importer
// composes itself never carries it.
const VendorTextMarker = "[vendor schema text]"

// Marked returns text opened by VendorTextMarker and a space; empty text stays
// empty.
func Marked(text string) string {
	if text == "" {
		return ""
	}
	return VendorTextMarker + " " + text
}

// draftVersion is the version of every entity a schema import drafts.
const draftVersion = "0.1.0"

// The labels of a drafted Component and the shape of its secret slots.
const (
	labelType      = "component.aggrega.ai/type"
	labelCategory  = "component.aggrega.ai/category"
	labelHosting   = "component.aggrega.ai/hosting"
	typeResource   = "resource"
	hostingManaged = "managed"
	secretSelector = "feature.aggrega.ai/kind=secret"
	secretInject   = "env"
)

// ComponentFor describes the Component named name drafted from t, whose
// properties placed holds as Split placed them, imported in the format named
// format. It is labelled a managed resource, and with its category where the
// name's segment after the namespace is one of Categories. Its description is
// t's marked as vendor text, where t has one, and a sentence naming the format
// and the type. values holds the identity keys, infra the placement and
// required keys and the open overrides map, provides the read-back scalars,
// each key in the order placed holds it. Each key's comment names its rule and
// quotes the vendor's description, marked, or says the vendor schema gives
// none; its description is the vendor's, marked. With r.SecretSlots each secret
// is a sensitive featuresSchema slot a secret Feature fills on the env
// mechanism, required where the vendor requires the property, described by its
// rule and the vendor's description; otherwise no slot is drafted, since the
// Recipe declares the secret as a deploy-time parameter.
func ComponentFor(name string, t *Type, placed []Placed, format string, r Rules) importer.ComponentDraft {
	d := importer.ComponentDraft{
		Name:        name,
		Version:     draftVersion,
		Description: componentDescription(t, format),
		Labels:      []importer.Label{{Key: labelType, Value: typeResource}},
	}
	if category := categoryOf(name); category != "" {
		d.Labels = append(d.Labels, importer.Label{Key: labelCategory, Value: category})
	}
	d.Labels = append(d.Labels, importer.Label{Key: labelHosting, Value: hostingManaged})

	for _, pl := range placed {
		switch pl.Pillar {
		case PillarValues:
			d.Values = append(d.Values, pillarNode(pl))
		case PillarInfra:
			d.Infra = append(d.Infra, pillarNode(pl))
		case PillarProvides:
			d.Provides = append(d.Provides, pillarNode(pl))
		case PillarSecret:
			if r.SecretSlots {
				d.Slots = append(d.Slots, importer.ComponentSlot{
					Name:        pl.Key,
					Selector:    secretSelector,
					Description: pl.Rule + "\n\n" + describedBy(pl.Property),
					Required:    pl.Required,
					Sensitive:   true,
					Inject:      []string{secretInject},
				})
			}
		}
	}
	d.Infra = append(d.Infra, importer.SchemaNode{
		Name:    overridesKey,
		Comment: []string{ruleOverridesMap},
		Type:    []string{string(KindObject)},
		Description: fmt.Sprintf("Writable settings of %s, each under the vendor's own name, which the Recipe "+
			"passes to the resource as written; its header lists the names it reads.", typeName(t)),
		Open: true,
	})
	return d
}

// pillarNode is the pillar key of pl: what Carry describes, required as placed,
// under the comment naming its rule and its description.
func pillarNode(pl Placed) importer.SchemaNode {
	n, _ := Carry(pl.Property, pl.Key)
	n.Required = pl.Required
	n.Comment = []string{pl.Rule, describedBy(pl.Property)}
	return n
}

// describedBy is p's vendor description, marked, or composed text saying the
// vendor schema gives none, or for a property read from a source writing the
// resource, that the source gives none.
func describedBy(p *Property) string {
	switch {
	case p.Description != "":
		return Marked(p.Description)
	case p.Written:
		return "The source writing the resource gives no description of " + vendorPath(p) + "."
	}
	return "The vendor schema gives no description of " + vendorPath(p) + "."
}

// componentDescription is t's description, marked, where it has one, and a
// composed sentence naming the format and the type.
func componentDescription(t *Type, format string) string {
	composed := fmt.Sprintf("Draft Component of the resource type %s, imported from a source in the %s format.",
		typeName(t), format)
	if t.Description == "" {
		return composed
	}
	return Marked(t.Description) + "\n\n" + composed
}

// typeName is t's name, with its apiVersion where it has one.
func typeName(t *Type) string {
	if t.APIVersion == "" {
		return t.Name
	}
	return t.Name + "@" + t.APIVersion
}
