// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// The codes of the diagnostics a selection halts with.
const (
	codeResourceTypesUnreadable = "resource-types-unreadable"
	codeResourceTypeNotNamed    = "resource-type-not-named"
	codeResourceTypeNotFound    = "resource-type-not-found"
)

// The codes of the diagnostics an import raises once a type is selected.
const (
	// CodeComponentExists reports a Component or path a draft would take that
	// is taken already: by a Component the catalog holds with other values
	// keys, or by another draft of the same import.
	CodeComponentExists     = "component-exists"
	codeComponentDraftedNew = "component-drafted-new"
	codeProvidesUnmapped    = "provides-unmapped"
)

// The codes a format's reader halts with, which the core raises too.
const (
	// CodeResourceSchemaUnreadable halts on a source that parses but is not the
	// format's schema shape, naming what it lacks.
	CodeResourceSchemaUnreadable = "resource-schema-unreadable"
	// CodeResourceTypeRefused halts on a requested type the format's provider
	// cannot realize, naming why.
	CodeResourceTypeRefused = "resource-type-refused"
	// CodeResourceTypeAmbiguous halts on a requested type name more than one
	// source of the format's input carries and the format cannot choose between.
	CodeResourceTypeAmbiguous = "resource-type-ambiguous"
	// CodeBetaProviderPassedOver reports that a type carried by a provider and
	// by its beta sibling was drafted from the provider.
	CodeBetaProviderPassedOver = "beta-provider-passed-over"
)

// resourceTypesUnreadable halts on a resource_types value, or an entry of it,
// the setting does not take.
func resourceTypesUnreadable(read []byte, why string) *aggrega.Diagnostic {
	return &aggrega.Diagnostic{
		Severity: aggrega.SeverityHalt,
		Code:     codeResourceTypesUnreadable,
		Message: fmt.Sprintf("the resource_types setting holds %s, which it cannot read: %s; write a list of type names "+
			"or of objects with a type and a component, so the import drafts the types you name",
			strings.TrimSpace(string(read)), why),
	}
}

// resourceTypeNotNamed halts where the sources hold several types and the
// setting names none. With countOnly the message gives the count and where to
// write the list; otherwise it lists the types. source names the one source
// holding them all, empty where several do.
func resourceTypeNotNamed(names []string, countOnly bool, source string) aggrega.Diagnostic {
	msg := fmt.Sprintf("the sources hold %d resource types and the resource_types setting names none: write the "+
		"types to draft in it, one of %s", len(names), strings.Join(names, ", "))
	if countOnly {
		msg = fmt.Sprintf("the sources hold %d resource types and the resource_types setting names none: write the "+
			"types to draft in it", len(names))
	}
	return aggrega.Diagnostic{Severity: aggrega.SeverityHalt, Code: codeResourceTypeNotNamed, Message: msg, SourceRef: source}
}

// resourceTypeNotFound halts on a requested type the sources do not hold,
// naming the nearest names they do. source names the one source the import
// reads types from, empty where it reads several.
func resourceTypeNotFound(requested string, nearest []string, source string) aggrega.Diagnostic {
	msg := fmt.Sprintf("the resource type %q is not in the sources", requested)
	if len(nearest) > 0 {
		msg += "; the nearest names are " + strings.Join(nearest, ", ")
	}
	return aggrega.Diagnostic{Severity: aggrega.SeverityHalt, Code: codeResourceTypeNotFound, Message: msg, SourceRef: source}
}

// resourceTypeVersionNotFound halts on a requested apiVersion other than the
// one the file holds for the type. source names the one source holding the
// type, empty where several do.
func resourceTypeVersionNotFound(requested, version string, held []string, source string) aggrega.Diagnostic {
	have := "the file holds no apiVersion for it"
	if len(held) > 0 {
		have = "the file holds apiVersion " + strings.Join(held, ", ")
	}
	return aggrega.Diagnostic{
		Severity: aggrega.SeverityHalt,
		Code:     codeResourceTypeNotFound,
		Message: fmt.Sprintf("the resource type %q has no apiVersion %q in the sources: %s; request that version or "+
			"write the type without one", requested, version, have),
		SourceRef: source,
	}
}

// ResourceTypeAmbiguous halts on the type name carried by each of providers,
// none of which the import can prefer: fix says how to export it from one alone.
func ResourceTypeAmbiguous(name string, providers []string, fix string) aggrega.Diagnostic {
	return aggrega.Diagnostic{
		Severity: aggrega.SeverityHalt,
		Code:     CodeResourceTypeAmbiguous,
		Message: fmt.Sprintf("the resource type %q is carried by %s, and the import cannot tell which to draft: %s",
			name, strings.Join(providers, ", "), fix),
	}
}

// BetaProviderPassedOver reports that the type name, carried by both chosen and
// its beta sibling passed, is drafted from chosen.
func BetaProviderPassedOver(name, chosen, passed string) aggrega.Diagnostic {
	return aggrega.Diagnostic{
		Severity: aggrega.SeverityInfo,
		Code:     CodeBetaProviderPassedOver,
		Message: fmt.Sprintf("the resource type %q is carried by %s and by its beta sibling %s: drafted from %s",
			name, chosen, passed, chosen),
	}
}
