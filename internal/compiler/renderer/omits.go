// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/truthy"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// EvaluateOmits resolves the omit entries one document declares into the entries a
// render of that document's source hands its provider.
//
// It is the one evaluation of an omits list, called for a Recipe's node, for a
// component-scope attachment's FeatureRecipe and for a boundary-scope FeatureRecipe,
// each against the context that document's values mapping is evaluated against —
// the context that carries the name the target gives the node's resources, which is
// what a chart names its own resources after. withheld is the set that context's
// failures are withheld by, so a template failure here is worded on the terms a
// values-mapping failure is.
//
// Entries are taken in author order. An entry's when is evaluated first and judged
// by the shared truthiness rule: a falsy result drops the entry, so its other fields
// are never evaluated, it is not handed over and it is not checked; an empty when
// keeps the entry; a when that fails to render halts as any failed template does.
// The kind, name, namespace and subchart of a kept entry then render. A namespace or
// a subchart rendering to the empty string is left absent, which places the entry
// exactly where an entry naming none is placed.
//
// Each field whose template reads a value withheld names is recorded on the entry's
// Withheld, read from the template by the same reading the values mapping's own mark
// is read by. That one record serves both places the entry is shown: a report about
// the entry quotes none of those fields, and the entry reaches its provider marked
// sensitive when any of them is recorded.
//
// Nothing about what the strings mean is read here. Which documents an entry selects
// is the rendering provider's own contract; the host evaluates the templates and
// hands the resolved strings over.
//
// A document declaring no entries, or whose every entry its when dropped, yields
// none, and a render handed none carries none on its input — so its payload is the
// one it would be if the key did not exist.
func EvaluateOmits(entries []types.OmitEntry, data *types.TemplateContext, withheld redact.Keys) ([]ResolvedOmit, error) {
	var out []ResolvedOmit
	for i, entry := range entries {
		if entry.When != "" {
			rendered, err := EvalTemplate(omitFieldLabel(i, omitFieldWhen), entry.When, data, withheld)
			if err != nil {
				return nil, err
			}
			if !truthy.Bool(rendered) {
				continue
			}
		}
		kind, err := EvalTemplate(omitFieldLabel(i, omitFieldKind), entry.Kind, data, withheld)
		if err != nil {
			return nil, err
		}
		name, err := EvalTemplate(omitFieldLabel(i, omitFieldName), entry.Name, data, withheld)
		if err != nil {
			return nil, err
		}
		namespace := ""
		if entry.Namespace != "" {
			if namespace, err = EvalTemplate(omitFieldLabel(i, omitFieldNamespace), entry.Namespace, data, withheld); err != nil {
				return nil, err
			}
		}
		subchart := ""
		if entry.Subchart != "" {
			if subchart, err = EvalTemplate(omitFieldLabel(i, omitFieldSubchart), entry.Subchart, data, withheld); err != nil {
				return nil, err
			}
		}
		out = append(out, ResolvedOmit{
			Entry:    OmitEntry{Kind: kind, Name: name, Namespace: namespace, Subchart: subchart},
			Position: i,
			Withheld: OmitFieldsWithheld{
				Kind:      templateReadsWithheld(entry.Kind, withheld),
				Name:      templateReadsWithheld(entry.Name, withheld),
				Namespace: templateReadsWithheld(entry.Namespace, withheld),
				Subchart:  templateReadsWithheld(entry.Subchart, withheld),
			},
		})
	}
	return out, nil
}

// declaresOmits reports whether a document's omits list declares any entry, as the
// author wrote it — whatever each entry's when would evaluate to. It is the one
// reading of "this document declares omits", which the rule refusing a provider
// that does not honour them reads for every kind of document carrying the list.
//
// The schema refuses an empty list, so a list with no entries and an absent key
// are one answer here and one state in any document that loaded.
func declaresOmits(entries []types.OmitEntry) bool {
	return len(entries) > 0
}

// RequireOmitCounts reads the counts a provider returned from a source render
// against the omit entries it was handed, and halts on the first count that says an
// entry left nothing out.
//
// It is the one place both checks are made, and it is exported because the crossing
// that hands the entries over and reads the counts back is in another package. The
// two checks are asked in this order because the second means nothing without the
// first:
//
//   - The report must hold one count per entry handed over, none below zero. A
//     provider declaring that it honours omits owes exactly that, and a report that
//     does not line up cannot say which entry left what out; it halts with
//     [ErrOmitReportMismatch], naming how many entries went over and how many counts
//     came back. A provider handed no entries owes no counts, and one that returns
//     some anyway is refused the same way.
//   - Every count must be above zero. The first entry, in the order the author wrote
//     them, whose count is zero halts with [ErrOmitMatchedNothing], naming its
//     position in the list its document declares and the kind, name, namespace and
//     subchart it resolved to — with any field whose template read a value declared
//     sensitive shown as the placeholder rather than as what it resolved to.
//
// Only the counts are read. What the provider left out is absent from what it
// returned, and nothing here looks at a returned document to confirm it.
func RequireOmitCounts(sent []ResolvedOmit, counts []int, document, nodeID, provider string) error {
	if len(counts) != len(sent) {
		return omitReportMismatchErr(nodeID, provider,
			fmt.Sprintf("handed %d omits entries", len(sent)),
			fmt.Sprintf("returned %d omitted counts", len(counts)))
	}
	for i, count := range counts {
		if count < 0 {
			return omitReportMismatchErr(nodeID, provider,
				fmt.Sprintf("handed %d omits entries", len(sent)),
				fmt.Sprintf("returned a count of %d for the entry at omits[%d]", count, sent[i].Position))
		}
	}
	for i, count := range counts {
		if count == 0 {
			return omitMatchedNothingErr(sent[i], document, nodeID, provider)
		}
	}
	return nil
}

// omitMatchedNothingAdvice is what a reader of the zero-count halt can do about it.
// It is the raise path's advice rather than the condition's sentence, because the
// sentence states the condition and the remedy depends on which of two mistakes was
// made. It names no unit of any one format — a chart renders documents, a compose
// file declares services, volumes and configs — because every provider that honours
// the entries reports through it.
const omitMatchedNothingAdvice = "correct the entry so it names something the source renders, or remove it; " +
	"a source upgrade that renamed or removed it is the usual cause"

// omitMatchedNothingErr raises [ErrOmitMatchedNothing] for one entry, composed from
// the condition's registered sentence and the entry's own coordinates.
func omitMatchedNothingErr(entry ResolvedOmit, document, nodeID, provider string) error {
	position := fmt.Sprintf("omits[%d]", entry.Position)
	if document != "" {
		position = fmt.Sprintf("%q %s", document, position)
	}
	values := []string{fmt.Sprintf("node %q", nodeID)}
	if provider != "" {
		values = append(values, fmt.Sprintf("provider %q", provider))
	}
	values = append(values,
		position,
		omitFieldValue("kind", entry.Entry.Kind, entry.Withheld.Kind),
		omitFieldValue("name", entry.Entry.Name, entry.Withheld.Name),
	)
	if entry.Entry.Namespace != "" {
		values = append(values, omitFieldValue("namespace", entry.Entry.Namespace, entry.Withheld.Namespace))
	}
	if entry.Entry.Subchart != "" {
		values = append(values, omitFieldValue("subchart", entry.Entry.Subchart, entry.Withheld.Subchart))
	}
	return cerrors.Wrap(ErrOmitMatchedNothing(), cerrors.CatalogError,
		cerrors.SourceLocation{File: nodeID},
		cerrors.ComposeMessage(ErrOmitMatchedNothing(), values, omitMatchedNothingAdvice))
}

// omitFieldValue renders one resolved field of an entry for a report, after the
// field's name: quoted as it resolved, or the placeholder where its template read a
// value declared sensitive, through the one rendering a provider's own refusal of an
// entry uses too.
func omitFieldValue(field, resolved string, withheld bool) string {
	return field + " " + rendercore.OmitFieldText(resolved, withheld)
}

// omitReportMismatchErr raises [ErrOmitReportMismatch], composed from the
// condition's registered sentence and what this report got wrong.
func omitReportMismatchErr(nodeID, provider, handed, returned string) error {
	values := []string{fmt.Sprintf("node %q", nodeID)}
	if provider != "" {
		values = append(values, fmt.Sprintf("provider %q", provider))
	}
	values = append(values, handed, returned)
	return cerrors.Wrap(ErrOmitReportMismatch(), cerrors.CatalogError,
		cerrors.SourceLocation{File: nodeID},
		cerrors.ComposeMessage(ErrOmitReportMismatch(), values, ""))
}
