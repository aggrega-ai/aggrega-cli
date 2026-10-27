// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// omitTarget is what an omit entry names in a CloudFormation template, stated once
// so every refusal of an entry and the matching below describe the same thing.
const omitTarget = "cloudformation leaves out a resource, named by its Type as the entry's kind and its logical ID under Resources as the entry's name"

// Intrinsic functions and the resource attribute through which a template names a
// resource by its logical ID. Each is the key an author writes.
const (
	intrinsicRef    = "Ref"
	intrinsicGetAtt = "Fn::GetAtt"
	intrinsicSub    = "Fn::Sub"
)

// leftOutResource is one resource a render left out, as a report may name it: its
// Type, and whether an entry that matched it withheld its kind or its name. A
// resource two entries left out is withheld where either entry withheld it.
type leftOutResource struct {
	typ          string
	withheldKind bool
	withheldName bool
}

// leftOutResources records every resource a render left out, by logical ID.
type leftOutResources map[string]leftOutResource

// add records one resource an entry left out.
func (l leftOutResources) add(id, typ string, withheld rendercore.OmitFieldsWithheld) {
	r := l[id]
	r.typ = typ
	r.withheldKind = r.withheldKind || withheld.Kind
	r.withheldName = r.withheldName || withheld.Name
	l[id] = r
}

// text names one left-out resource for a report.
func (r leftOutResource) text(id string) string {
	return "resource " + rendercore.OmitFieldText(id, r.withheldName) +
		" (type " + rendercore.OmitFieldText(r.typ, r.withheldKind) + ")"
}

// remainingTemplate is one template of a sourced source as the reference check
// reads it: where it was packed, and its decoded content once the left-out
// resources are gone.
type remainingTemplate struct {
	path string
	doc  map[string]any
}

// LeaveOutSourced applies a sourced render's omit entries to the templates a
// sourced source carries, already normalized, and returns the files with every
// resource an entry matched removed, together with one count per entry in the
// order the entries arrived — nil for a render handed none, whose files come back
// exactly as they were handed in.
//
// It runs between normalization and everything that reads the templates after it,
// so a left-out resource is never stamped with provenance, never declared as a name
// the stack carries, and never reaches the assembled stack.
//
// An entry names a resource by its Type — the entry's kind, such as AWS::S3::Bucket
// — and by its logical ID, its key under Resources — the entry's name. Every entry
// is checked before any is matched, and the first in the order they arrived that
// carries a namespace or a subchart is refused: a template has neither, so there is
// nothing such an entry could select. Each template the source carries is matched: a
// single-file source's one template, and every template of a directory source, the
// same files [NormalizeSourced] normalizes; a file it vendors verbatim is never
// matched. Each entry's count is the number of resources it matched across all of
// them, so two entries naming one resource each count it.
//
// A template every resource of which is left out keeps an empty Resources section,
// which is what the template reads as without them. A template no entry matched is
// returned byte for byte as it was handed in, and one an entry did match is written
// in the form normalization writes, so what remains of it is byte-identical to the
// same template authored without the resources.
//
// Once the resources are out, every reference the remaining templates still make
// to one of them halts the render (see requireNoReferenceToLeftOut): the stack would
// name a resource it does not declare.
//
// A field of an entry is never quoted where the entry withholds it, here or in the
// reference halt.
func LeaveOutSourced(files []SourcedFile, rootIsDir bool, entries []rendercore.ResolvedOmit, nodeID string) ([]SourcedFile, []int, error) {
	if len(entries) == 0 {
		return files, nil, nil
	}
	for _, entry := range entries {
		if err := refuseOmitNamespace(entry, nodeID); err != nil {
			return nil, nil, err
		}
		if err := refuseOmitSubchart(entry, nodeID); err != nil {
			return nil, nil, err
		}
	}

	counts := make([]int, len(entries))
	left := leftOutResources{}
	out := make([]SourcedFile, len(files))
	var templates []remainingTemplate
	for i, f := range files {
		out[i] = f
		if rootIsDir && !isTemplateFile(f.Path) {
			continue
		}
		doc, err := decodeNormalizedTemplate(f.Content)
		if err != nil {
			return nil, nil, fmt.Errorf("cloudformation-provider: read sourced template %q for its omits entries: %w", f.Path, err)
		}
		if doc == nil {
			continue
		}
		if removeMatchedResources(doc, entries, counts, left) {
			body, err := encodeNormalizedTemplate(doc)
			if err != nil {
				return nil, nil, fmt.Errorf("cloudformation-provider: write sourced template %q without its left-out resources: %w", f.Path, err)
			}
			out[i] = SourcedFile{Path: f.Path, Content: body}
		}
		templates = append(templates, remainingTemplate{path: f.Path, doc: doc})
	}

	if err := requireNoReferenceToLeftOut(templates, left, nodeID); err != nil {
		return nil, nil, err
	}
	return out, counts, nil
}

// refuseOmitNamespace refuses an entry that carries a namespace, which nothing in a
// CloudFormation template has.
func refuseOmitNamespace(entry rendercore.ResolvedOmit, nodeID string) error {
	if entry.Entry.Namespace == "" {
		return nil
	}
	return cerrors.Wrap(wasm.ErrPluginCannotExpressInput(),
		cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
		fmt.Sprintf("cloudformation-provider: an omits entry (kind %s, name %s) names namespace %s, and a CloudFormation template has no namespaces: %s; remove the namespace from the entry",
			rendercore.OmitFieldText(entry.Entry.Kind, entry.Withheld.Kind),
			rendercore.OmitFieldText(entry.Entry.Name, entry.Withheld.Name),
			rendercore.OmitFieldText(entry.Entry.Namespace, entry.Withheld.Namespace),
			omitTarget))
}

// refuseOmitSubchart refuses an entry that names a subchart, which nothing a
// CloudFormation template declares came from.
func refuseOmitSubchart(entry rendercore.ResolvedOmit, nodeID string) error {
	if entry.Entry.Subchart == "" {
		return nil
	}
	return cerrors.Wrap(wasm.ErrPluginCannotExpressInput(),
		cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
		fmt.Sprintf("cloudformation-provider: an omits entry (kind %s, name %s) names subchart %s, and a CloudFormation template has no subcharts: %s; remove the subchart from the entry",
			rendercore.OmitFieldText(entry.Entry.Kind, entry.Withheld.Kind),
			rendercore.OmitFieldText(entry.Entry.Name, entry.Withheld.Name),
			rendercore.OmitFieldText(entry.Entry.Subchart, entry.Withheld.Subchart),
			omitTarget))
}

// omitMatchesResource reports whether one entry names one resource: the entry's
// kind is the resource's Type and its name is the resource's logical ID, each
// compared exactly as written. A resource whose body is not a mapping, or whose Type
// is not a string, has no Type an entry can name, so it matches nothing.
func omitMatchesResource(entry rendercore.OmitEntry, id string, body any) (string, bool) {
	resource, ok := body.(map[string]any)
	if !ok {
		return "", false
	}
	typ, ok := resource["Type"].(string)
	if !ok || typ == "" {
		return "", false
	}
	return typ, entry.Kind == typ && entry.Name == id
}

// removeMatchedResources removes from one decoded template every resource an entry
// matches, counting each match against the entry and recording the resource as left
// out, and reports whether anything was removed. The Resources section is replaced
// rather than edited, so no other reader of the decoded value sees it change.
func removeMatchedResources(doc map[string]any, entries []rendercore.ResolvedOmit, counts []int, left leftOutResources) bool {
	resources, ok := doc[sectionResources].(map[string]any)
	if !ok {
		return false
	}
	matched := map[string]bool{}
	for _, id := range sortedKeys(resources) {
		for e, entry := range entries {
			typ, match := omitMatchesResource(entry.Entry, id, resources[id])
			if !match {
				continue
			}
			counts[e]++
			left.add(id, typ, entry.Withheld)
			matched[id] = true
		}
	}
	if len(matched) == 0 {
		return false
	}
	kept := make(map[string]any, len(resources)-len(matched))
	for id, body := range resources {
		if !matched[id] {
			kept[id] = body
		}
	}
	doc[sectionResources] = kept
	return true
}

// decodeNormalizedTemplate reads one template [NormalizeSourced] wrote back into a
// value tree, keeping every number as the text it was written as so writing the
// tree again reproduces it. A template that is not a mapping holds no Resources
// section, and reads as nil.
func decodeNormalizedTemplate(content []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(content))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	doc, _ := v.(map[string]any)
	return doc, nil
}

// encodeNormalizedTemplate writes a template's value tree in the form
// [normalizeTemplateBytes] writes it: two-space indented JSON with sorted keys and a
// trailing newline. It is the same form on purpose — a template a resource was
// removed from must read exactly as normalization writes the template authored
// without it, and a test holds the two to each other.
func encodeNormalizedTemplate(doc map[string]any) ([]byte, error) {
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// resourceReference is one place a template names a resource by its logical ID: the
// logical ID, the intrinsic function or attribute it is named through, and where in
// the template that sits, as the path of keys and list positions from the root.
type resourceReference struct {
	id   string
	form string
	at   []string
}

// requireNoReferenceToLeftOut halts when the remaining templates still name a
// resource the render left out, naming every such reference: the template, where the
// reference sits, the intrinsic function or attribute it is made through, and the
// resource it names.
//
// What counts as a reference is what CloudFormation's template reference defines as
// a template naming a resource by its logical ID, read in the two sections the
// reference lets an intrinsic function name a resource from — every value of every
// resource's body, its properties, metadata and policies included, and every value
// of every output:
//
//   - Ref, whose argument is the logical ID;
//   - Fn::GetAtt, as a list whose first item is the logical ID, or as one string
//     whose text before its first dot is;
//   - a resource's DependsOn, as one logical ID or a list of them;
//   - each variable of Fn::Sub, in its string form and in its list form: ${Id} and
//     ${Id.Attribute} name the resource Id, a variable written ${!…} is literal text,
//     and a variable the list form's map names is that map's value and no resource.
//
// Nothing else names a resource: a Condition attribute and the first argument of
// Fn::If name a condition, Fn::FindInMap a mapping, Fn::ImportValue another stack's
// export and Fn::GetAZs a region — and every value nested inside any of them is
// read like any other value. The Conditions, Mappings, Parameters and Rules sections
// cannot name a resource, and the template-level Metadata section is data
// CloudFormation does not process, so none of them is read. A Ref or a Fn::GetAtt
// whose logical ID is itself written as an intrinsic function — valid only under
// the AWS::LanguageExtensions transform — names no resource this check can read
// before that transform runs, and is not reported.
//
// A resource another template of the same source still declares under the
// left-out logical ID is still declared, so a reference to it is not reported; nor
// is a reference to a resource the source never declared: only what an entry left
// out is the render's to answer for.
//
// References are reported template by template in the order the source carries
// them, each template's resources and then its outputs in key order, and each body
// walked in key order, so one source yields one message.
func requireNoReferenceToLeftOut(templates []remainingTemplate, left leftOutResources, nodeID string) error {
	if len(left) == 0 {
		return nil
	}
	declared := map[string]bool{}
	for _, tpl := range templates {
		resources, _ := tpl.doc[sectionResources].(map[string]any)
		for id := range resources {
			declared[id] = true
		}
	}

	var findings []string
	for _, tpl := range templates {
		for _, ref := range templateReferences(tpl.doc) {
			resource, out := left[ref.id]
			if !out || declared[ref.id] {
				continue
			}
			findings = append(findings, fmt.Sprintf("template %q names %s by %s at %s",
				tpl.path, resource.text(ref.id), ref.form, pointerText(ref.at, ref.id, resource.withheldName)))
		}
	}
	if len(findings) == 0 {
		return nil
	}
	return cerrors.Wrap(wasm.ErrPluginRequiredInputUnresolved(),
		cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
		fmt.Sprintf("cloudformation-provider: what an omits entry left out is still named by the source's templates: %s; CloudFormation refuses a template naming a resource it does not declare: leave out what names it as well, or remove the entry",
			strings.Join(findings, "; ")))
}

// pointerText spells where a reference sits as a JSON Pointer from the template's
// root. A segment that spells a withheld name is shown as the placeholder rather
// than quoted.
func pointerText(at []string, name string, withheld bool) string {
	var b strings.Builder
	for _, seg := range at {
		b.WriteByte('/')
		if withheld && seg == name {
			b.WriteString(rendercore.OmitFieldText(seg, true))
			continue
		}
		b.WriteString(rfc6901Escape(seg))
	}
	return b.String()
}

// templateReferences lists every reference one template makes to a resource by its
// logical ID, in the order they are reported: each resource in key order — its
// DependsOn, then everything in its body — and then each output in key order.
func templateReferences(doc map[string]any) []resourceReference {
	var refs []resourceReference
	emit := func(r resourceReference) { refs = append(refs, r) }
	if resources, ok := doc[sectionResources].(map[string]any); ok {
		for _, id := range sortedKeys(resources) {
			at := []string{sectionResources, id}
			if body, ok := resources[id].(map[string]any); ok {
				for _, target := range authoredDependsOn(body[dependsOnAttribute]) {
					emit(resourceReference{id: target, form: dependsOnAttribute, at: appendPath(at, dependsOnAttribute)})
				}
			}
			walkReferences(resources[id], at, emit)
		}
	}
	if outputs, ok := doc[sectionOutputs].(map[string]any); ok {
		for _, id := range sortedKeys(outputs) {
			walkReferences(outputs[id], []string{sectionOutputs, id}, emit)
		}
	}
	return refs
}

// walkReferences visits one value of a template and everything inside it, emitting
// each Ref, Fn::GetAtt and Fn::Sub reference to a logical ID it finds, and where the
// intrinsic function making it sits. A mapping's keys are walked in sorted order and
// a list's items in their own, so the references come out in one order for one
// value.
//
// An intrinsic function is recognised by its key wherever it is written, the way
// the template language reads a mapping holding one; its argument is still walked,
// so a reference nested inside it — a Ref naming an attribute of Fn::GetAtt, a
// value of a Fn::Sub variable map — is read too.
func walkReferences(v any, at []string, emit func(resourceReference)) {
	switch node := v.(type) {
	case map[string]any:
		for _, key := range sortedKeys(node) {
			arg := node[key]
			switch key {
			case intrinsicRef:
				if id, ok := arg.(string); ok {
					emit(resourceReference{id: id, form: intrinsicRef, at: at})
				}
			case intrinsicGetAtt:
				if id, ok := getAttLogicalID(arg); ok {
					emit(resourceReference{id: id, form: intrinsicGetAtt, at: at})
				}
			case intrinsicSub:
				for _, id := range subLogicalIDs(arg) {
					emit(resourceReference{id: id, form: intrinsicSub, at: at})
				}
			}
			walkReferences(arg, appendPath(at, key), emit)
		}
	case []any:
		for i, item := range node {
			walkReferences(item, appendPath(at, strconv.Itoa(i)), emit)
		}
	}
}

// getAttLogicalID returns the logical ID a Fn::GetAtt argument names: the first item
// of its list form, or the text before the first dot of its string form, read
// through the same split a short-form GetAtt is expanded with.
func getAttLogicalID(arg any) (string, bool) {
	parts, ok := getAttValue(arg).([]any)
	if !ok || len(parts) == 0 {
		return "", false
	}
	id, ok := parts[0].(string)
	return id, ok && id != ""
}

// subLogicalIDs returns the logical IDs a Fn::Sub argument's variables name, each
// once, in the order its string first names them: every ${Name} or ${Name.Attribute}
// whose whole text the list form's variable map does not name, as the text before
// its first dot. A variable written ${!…} is literal text and names nothing.
func subLogicalIDs(arg any) []string {
	// The string and the variable map are read by the one reading the parameter
	// binding uses, so a variable the binding writes into the map and a variable
	// this check reads out of it are the same variable.
	text, mapped := subParts(arg)
	var ids []string
	seen := map[string]bool{}
	for _, variable := range subVariables(text) {
		if _, named := mapped[variable]; named {
			continue
		}
		id, _, _ := strings.Cut(variable, ".")
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

// appendPath returns a path one segment deeper, never sharing the caller's backing
// array, so a path a reference recorded is not rewritten by a sibling's.
func appendPath(at []string, seg string) []string {
	out := make([]string, len(at), len(at)+1)
	copy(out, at)
	return append(out, seg)
}
