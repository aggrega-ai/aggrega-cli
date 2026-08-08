// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// LintUnreadParameters reports every parameter an Aggregate declares that no
// reference in that Aggregate's own text reads.
//
// A declared parameter is the authoring surface for varying one value per
// deployment: the Release fulfils the declaration, and a
// `{{ .parameters.<key> }}` reference carries the fulfilled value into the
// component values it belongs to. A declaration no reference reads leaves that
// variation with nowhere to land — whatever a Release supplies for it, the
// rendered output is the same. That is the parameter-scoped form of the same
// smell an authored values key nothing consumes carries.
//
// The default is deliberately not an exemption. A default no reference reads
// reaches the output no more than a Release-supplied value does, so a defaulted
// declaration is reported exactly as a bare one is.
//
// The reading is advisory and never halts. A parameter may be declared ahead of
// the reference that will read it, and a static walk cannot tell that from a
// mistake — the same reason the authored-key reading only ever advises.
//
// Reference collection reads the Aggregate's own text and nothing else, which
// bounds what can be claimed:
//
//   - A parameter reached only by a Component schema's validate predicate is not
//     visible here. Such a predicate names the parameter through the resolved
//     parameter map rather than through a dotted reference in the Aggregate, so
//     the declaration is reported as unread even though the predicate reads it.
//   - Text that does not parse as a template yields no reference set at all.
//     Reporting every declared parameter as unread on a parse failure would bury
//     the real diagnostic the interpolation raises over the same text, so nothing
//     is reported instead. The two agree on which text parses because both reach
//     one shared function set.
//
// A reference to the whole parameter map — iterating it, or reaching one entry
// through index — counts every declaration as read, because the collected
// reference then names the map rather than a key beneath it and no per-key claim
// can be made.
//
// Findings come back sorted by parameter key, so the same Aggregate produces the
// same advisory sequence on every run. Each finding carries a Field locator
// naming the declaration site, so a front end that maps locators to source
// positions can place the advisory on the offending key rather than at the head
// of the file.
//
// Returns nil when the Aggregate declares no parameters.
func LintUnreadParameters(rawAggregate []byte, contract types.OrderedMap[any], aggregateName string) []*ResolverError {
	if contract.Len() == 0 {
		return nil
	}
	refs, err := gotmpl.CollectTemplateFieldAccesses(string(rawAggregate))
	if err != nil {
		return nil
	}

	entity := fmt.Sprintf("Aggregate %s", aggregateName)
	var out []*ResolverError
	for _, key := range gotmpl.UnreadKeys(refs, ".parameters", contract) {
		out = append(out, &ResolverError{
			Entity: entity,
			Field:  fmt.Sprintf(".spec.parameters.%s", key),
			Message: fmt.Sprintf(
				"parameter %q is declared but no {{ .parameters.%s }} reference in the Aggregate reads it",
				key, key),
			Err: ErrUnreadParameter(),
		})
	}
	return out
}
