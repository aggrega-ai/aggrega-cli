// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The rules over the shape of what an author wrote under one delivery mechanism,
// expressed as constraint declarations. See checks.go for what a declaration here
// is.
//
// Each is asked once per mechanism of every injection block either authoring
// document writes — a component's own, an attachment's, and the one on each
// connection either draws, in the Release as well as the Aggregate — over one
// enumeration and one payload fact, so they cannot come to disagree about which
// blocks exist. They are separate declarations because they are separate rules:
// each check carries the one condition it reports, and a finding is matched by
// that condition.
//
// The Aggregate and Release schemas admit neither of the first two faults, so a
// document read from a file is refused when it loads and neither of those rules
// finds anything in it. They refuse the same faults in a document built through
// the Go API, which no schema has read. The third, a literal in an attachment's
// own block, is admitted by the schemas and refused only here. All three are
// asked while the documents are resolved, so a build halts before anything is
// assembled from the block.

// injectDeliveryIsAList reports each delivery mechanism whose value is not a list
// of entries: a mapping, a scalar, any other shape.
//
// Every reader of a mechanism reads the list and nothing else, so a mechanism
// holding anything else would deliver nothing, silently. A mechanism carrying no
// value at all delivers nothing either, and that is what an author leaving it
// empty means, so it is not refused.
//
// The finding is positioned at the mechanism, and its detail is the mechanism's
// name: a locator stops short of a key carrying its separator, so the name is
// what still says which mechanism is meant where the position cannot.
var injectDeliveryIsAList = constraint.Check[
	constraint.NormalizedView,
	constraint.InjectDeliverySubject,
]{
	Sentinel: ErrInjectDeliveryShape(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.InjectDeliverySubject,
	) []constraint.Objection {
		payload := v.AuthoredInjectDelivery(s.AuthoringDocumentKind(), s.InjectBlockPath(), s.MechanismName())
		if payload == nil {
			return nil
		}
		if _, isList := payload.([]any); isList {
			return nil
		}
		return []constraint.Objection{s.Raise(ErrInjectDeliveryShape(), s.MechanismName())}
	},
}

// injectDeliveryEntryIsUnambiguous reports each delivery mechanism holding an
// entry that carries both `value:` and `from:`.
//
// It reads the list form alone. A mechanism holding anything else is the rule
// above's to report, and its entries are not entries this one could read.
//
// An entry this rule cannot read as an entry — one lacking `in:`, or not a mapping
// at all — is not its question either, and every reader of a mechanism skips such
// an entry exactly as this one does.
//
// One finding is raised per mechanism, positioned at it, and its detail is the
// mechanism's name followed by the `in:` of each offending entry in the order the
// list holds them: a locator names keys and cannot reach into a list, so the
// entries are named by the key the author wrote for each.
var injectDeliveryEntryIsUnambiguous = constraint.Check[
	constraint.NormalizedView,
	constraint.InjectDeliverySubject,
]{
	Sentinel: ErrInjectEntryAmbiguous(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.InjectDeliverySubject,
	) []constraint.Objection {
		entries, isList := v.AuthoredInjectDelivery(
			s.AuthoringDocumentKind(), s.InjectBlockPath(), s.MechanismName()).([]any)
		if !isList {
			return nil
		}
		detail := []string{s.MechanismName()}
		for _, entry := range entries {
			in, _, _, hasValue, hasFrom, ok := types.DecodeVerboseInjectEntry(entry)
			if ok && hasValue && hasFrom {
				detail = append(detail, in)
			}
		}
		if len(detail) == 1 {
			return nil
		}
		return []constraint.Objection{s.Raise(ErrInjectEntryAmbiguous(), detail...)}
	},
}

// attachmentInjectEntryNamesAKey reports each entry of an attachment's own
// injection block that carries `value:`.
//
// An attachment's block delivers into its component what the attachment's
// FeatureRecipe publishes, so each of its entries names a published key with
// `from:`. A literal written there names nothing the attachment publishes, and a
// build delivered nothing for it into the component, without a word; it is
// refused instead, on the terms a connection's literal is. A literal the
// component needs belongs in the component's own injection block, where
// `value:` is admitted and delivered.
//
// It is asked of every block on the delivery axis and reads only those an
// attachment writes directly, which the owner fact names: a component's own
// block admits `value:`, and a block on a connection — a component's or one an
// attachment draws — is the connection rule's. That covers an attachment on a
// component and one on a scope alike, which is why the rule walks the delivery
// axis rather than the attachments a component carries.
//
// It reads the list form alone, and an entry carrying both keys is the
// ambiguity rule's to report, so one authoring fault is reported once. An entry
// this rule cannot read as an entry — one lacking `in:`, or not a mapping at all
// — is skipped exactly as every reader of a mechanism skips it.
//
// One finding is raised per offending entry, positioned at the mechanism, and
// its detail is the mechanism and the entry's `in:`: a locator names keys and
// cannot reach into a list, so the entry is named by the key the author wrote
// for it.
var attachmentInjectEntryNamesAKey = constraint.Check[
	constraint.NormalizedView,
	constraint.InjectDeliverySubject,
]{
	Sentinel: ErrAttachmentInjectValue(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.InjectDeliverySubject,
	) []constraint.Objection {
		if v.AuthoredInjectBlockAttachment(s.AuthoringDocumentKind(), s.InjectBlockPath()) == "" {
			return nil
		}
		var out []constraint.Objection
		for _, entry := range literalDeliveryEntries(v.AuthoredInjectDelivery(
			s.AuthoringDocumentKind(), s.InjectBlockPath(), s.MechanismName())) {
			// An entry carrying `from:` beside its literal says two things at once,
			// and is refused as ambiguous — by the ambiguity rule, or by the schema
			// when the document is read; reporting it here as well would name one
			// authoring fault under two conditions.
			if entry.withFrom {
				continue
			}
			out = append(out, s.Raise(ErrAttachmentInjectValue(), s.MechanismName(), entry.in))
		}
		return out
	},
}

// literalDeliveryEntry is one entry of a delivery mechanism's authored list that
// carries `value:`: the `in:` it is written under, and whether it carries
// `from:` beside the literal.
type literalDeliveryEntry struct {
	in       string
	withFrom bool
}

// literalDeliveryEntries returns the entries of one mechanism's authored payload
// that carry `value:`, in the order the list holds them.
//
// It is the one reading of "this entry writes a literal" for the rules over the
// surfaces where a literal delivers nothing — a connection's block and an
// attachment's own block — so the two cannot come to disagree about which
// entries are literals. Whether an entry also carrying `from:` is reported is
// each rule's own decision, which is why that fact is returned rather than
// filtered here: the connection rule reports it, the attachment rule leaves it
// to the ambiguity rule.
//
// It reads the list form alone; a payload of any other shape is the shape rule's
// to report and yields nothing here. An entry it cannot read as an entry — one
// lacking `in:`, or not a mapping at all — is skipped, as every reader of a
// mechanism skips it.
func literalDeliveryEntries(payload any) []literalDeliveryEntry {
	entries, isList := payload.([]any)
	if !isList {
		return nil
	}
	var out []literalDeliveryEntry
	for _, entry := range entries {
		in, _, _, hasValue, hasFrom, ok := types.DecodeVerboseInjectEntry(entry)
		if ok && hasValue {
			out = append(out, literalDeliveryEntry{in: in, withFrom: hasFrom})
		}
	}
	return out
}
