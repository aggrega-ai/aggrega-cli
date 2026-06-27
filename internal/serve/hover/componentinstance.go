// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package hover

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// ComponentInstance renders the hover content for a sibling
// Component instance referenced via uses.target. Distinct from the
// catalog-entity Component renderer because the instance carries
// type:, values:, uses:, features: rather than spec.schema: + spec.extends:.
//
// The Entity bundled here is structured differently from other
// renderers: Doc is the ComponentInstance YAML node (the value side
// of the spec.components.<key>: mapping pair), NOT a document root.
// The instance-Key value (the local name like "payment-svc") is
// passed via the Key field, since it lives in the parent mapping's
// key node, not inside the instance value.
type ComponentInstance struct {
	Key      string
	Instance *yaml.Node
}

// RenderComponentInstance returns the hover content for a sibling
// Component instance. Unlike the per-kind entity renderers, this
// function takes the discriminated input shape directly (the YAML
// node walker already located the instance value; no need to walk
// metadata.name etc.).
func RenderComponentInstance(ci ComponentInstance) string {
	if ci.Instance == nil || ci.Instance.Kind != yaml.MappingNode {
		return ""
	}
	typeURI := directScalarChild(ci.Instance, "type")
	desc := directScalarChild(ci.Instance, "description")

	header := fmt.Sprintf("**Component instance** `%s`", ci.Key)
	var typeLine string
	if typeURI != "" {
		typeLine = fmt.Sprintf("Type: `%s`", typeURI)
	}

	body := []string{}
	if n := mappingChild(ci.Instance, "values"); n != nil && n.Kind == yaml.MappingNode {
		body = append(body, fmt.Sprintf("**Values**: %d entries", mappingPairCount(n)))
	}
	if n := mappingChild(ci.Instance, "uses"); n != nil && n.Kind == yaml.MappingNode {
		body = append(body, fmt.Sprintf("**Uses**: %d sockets", mappingPairCount(n)))
	}
	if n := mappingChild(ci.Instance, "features"); n != nil && n.Kind == yaml.MappingNode {
		body = append(body, fmt.Sprintf("**Features**: %d attached", mappingPairCount(n)))
	}
	if n := mappingChild(ci.Instance, "secrets"); n != nil && n.Kind == yaml.MappingNode {
		body = append(body, fmt.Sprintf("**Secrets**: %d attached", mappingPairCount(n)))
	}

	return Assemble(
		header,
		typeLine,
		Description(desc),
		Separator(),
		joinLines(body),
	)
}
