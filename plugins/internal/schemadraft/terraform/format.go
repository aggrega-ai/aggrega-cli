// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package terraform

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
)

const (
	formatName = "terraform-schema"
	capability = "terraform"
	syntax     = "json"
)

// Format is the Terraform schema format, terraform-schema: the export of
// providers schema -json, whose providers each carry resource types, matched by
// exact name, with the count of types reported where a request names none. Its
// Recipes target the terraform provider. Placement is the provider's own
// location attributes; identity is name, identifier and the attribute named
// after the type's last word; a secret is a sensitive attribute that can be
// written, delivered on a featuresSchema slot. Where two providers carry one
// type name and one is the other's beta sibling, the non-beta provider is
// drafted from. Its writer is set beside it, where the Recipe's Terraform is
// written, so Write stays nil here.
var Format = schemadraft.Format{
	Name:       formatName,
	Capability: capability,
	Syntax:     syntax,
	CountOnly:  true,
	List:       list,
	Resolve:    resolve,
	Lift:       lift,
	Rules: schemadraft.Rules{
		Placement:   placement,
		Identity:    identity,
		Secret:      secret,
		SecretSlots: true,
	},
	Category: category,
}

// placements are the attributes each provider type, with the -beta suffix of
// its beta sibling counted, names where the resource is deployed.
var placements = map[string][]string{
	"aws":         {"region"},
	"azurerm":     {"location", "resource_group_name"},
	"google":      {"project", "region", "zone", "location"},
	"google-beta": {"project", "region", "zone", "location"},
}

// placement reports whether p names where its provider deploys the resource t.
func placement(t *schemadraft.Type, p *schemadraft.Property) bool {
	if t.Provider == nil {
		return false
	}
	for _, name := range placements[t.Provider.Type] {
		if p.Name == name {
			return true
		}
	}
	return false
}

// identity reports whether p is the resource's own name or identifier: name,
// identifier, or the attribute named after the last word of the type's name
// (bucket for aws_s3_bucket).
func identity(t *schemadraft.Type, p *schemadraft.Property) bool {
	last := t.Name[strings.LastIndex(t.Name, "_")+1:]
	return p.Name == "name" || p.Name == "identifier" || (last != "" && p.Name == last)
}

// secret reports whether p is a secret: a sensitive attribute that can be
// written.
func secret(_ *schemadraft.Type, p *schemadraft.Property) bool { return p.Sensitive && !p.ReadOnly }
