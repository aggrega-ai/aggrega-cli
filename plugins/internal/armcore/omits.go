// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore/armexpr"
)

// removedResource is one resource an omit entry left out of a template: its type,
// its name as the entry matches it, and which of the entry's fields are withheld
// from any text a reader is shown.
type removedResource struct {
	typ, name string
	withheld  rendercore.OmitFieldsWithheld
}

// text names the resource for a halt, quoting nothing an entry withholds.
func (r removedResource) text() string {
	return rendercore.OmitFieldText(r.name, r.withheld.Name)
}

// applyOmits removes from a parsed template every resource an omit entry names
// and returns one count per entry, in the order the entries arrived: the number of
// resources that entry matched. An entry's kind is a resource's type and its name
// is the symbolic name under a language version 2.x template, or the name property
// exactly as written under a template without one; both compare exactly. Two
// entries that name one resource each count it.
//
// An entry that carries a namespace or a subchart halts wrapping
// ErrPluginCannotExpressInput, since a template has neither, before any resource
// is matched. Once resources are out, the template is read for what still names
// one, and a reference halts wrapping ErrPluginRequiredInputUnresolved, naming
// the resource, the form and the JSON pointer: under 2.x a dependsOn entry (a
// parent::child entry by its head too) and the symbolic first argument of
// reference, references, resourceInfo or a list function; without a language
// version a dependsOn name, the name given to reference or a list function, and a
// resourceId call whose type is the resource's type and whose last name is its
// name. A string that is an expression the reader cannot parse, in a template an
// entry matched, halts wrapping ErrPluginValueNotRepresentable rather than let a
// reference through unproven; a template no entry matched is never read for
// expressions. The template is changed only by the removal.
func applyOmits(template map[string]any, entries []rendercore.ResolvedOmit) (counts []int, err error) {
	return leaveOut(templateSubject(""), template, entries)
}

// leaveOut is applyOmits with subject naming the template in its halts.
func leaveOut(subject string, template map[string]any, entries []rendercore.ResolvedOmit) ([]int, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	for _, e := range entries {
		if err := refuseOmitScope(e); err != nil {
			return nil, err
		}
	}
	counts := make([]int, len(entries))
	v2 := isLanguageVersion2(template[sectionLanguageVersion])
	var removed []removedResource
	var position []int
	switch resources := template[sectionResources].(type) {
	case map[string]any:
		kept := make(map[string]any, len(resources))
		for _, symbol := range sortedKeys(resources) {
			r, matched := matchOmits(resources[symbol], symbol, entries, counts)
			if matched {
				removed = append(removed, r...)
				continue
			}
			kept[symbol] = resources[symbol]
		}
		if len(kept) != len(resources) {
			template[sectionResources] = kept
		}
	case []any:
		kept := make([]any, 0, len(resources))
		for i, body := range resources {
			name, _ := body.(map[string]any)["name"].(string)
			r, matched := matchOmits(body, name, entries, counts)
			if matched {
				removed = append(removed, r...)
				continue
			}
			kept = append(kept, body)
			position = append(position, i)
		}
		if len(kept) != len(resources) {
			template[sectionResources] = kept
		}
	}
	if len(removed) == 0 {
		return counts, nil
	}
	s := omitScan{subject: subject, v2: v2, removed: removed, position: position}
	if err := s.walk(template, nil); err != nil {
		return nil, err
	}
	return counts, nil
}

// refuseOmitScope refuses an entry that names a namespace or a subchart.
func refuseOmitScope(e rendercore.ResolvedOmit) error {
	var what, text string
	switch {
	case e.Entry.Namespace != "":
		what, text = "namespace", rendercore.OmitFieldText(e.Entry.Namespace, e.Withheld.Namespace)
	case e.Entry.Subchart != "":
		what, text = "subchart", rendercore.OmitFieldText(e.Entry.Subchart, e.Withheld.Subchart)
	default:
		return nil
	}
	return refuse(plugincondition.ErrPluginCannotExpressInput,
		fmt.Sprintf("an omits entry (kind %s, name %s) names the %s %s, and an ARM template has neither namespaces nor subcharts: an entry names a resource by its type as the kind and by its symbolic name (languageVersion 2.x) or its name property (no languageVersion) as the name; remove the %s from the entry",
			rendercore.OmitFieldText(e.Entry.Kind, e.Withheld.Kind), rendercore.OmitFieldText(e.Entry.Name, e.Withheld.Name),
			what, text, what))
}

// matchOmits counts one resource against every entry and returns, for each entry
// that names it, the resource as that entry removes it. A body that is not an
// object, or has no type, matches nothing.
func matchOmits(body any, name string, entries []rendercore.ResolvedOmit, counts []int) ([]removedResource, bool) {
	typ, _ := body.(map[string]any)["type"].(string)
	if typ == "" {
		return nil, false
	}
	var out []removedResource
	for i, e := range entries {
		if e.Entry.Kind == typ && e.Entry.Name == name {
			counts[i]++
			out = append(out, removedResource{typ: typ, name: name, withheld: e.Withheld})
		}
	}
	return out, len(out) > 0
}

// omitScan reads a template that an omit entry matched for what still names a
// removed resource.
type omitScan struct {
	subject string
	v2      bool
	removed []removedResource
	// position is, for a resources list, the index each remaining resource had
	// before the removal, so a halt names the place the author wrote.
	position []int
	// skipNested reports that the walk is inside a resource whose nested template
	// is evaluated in a scope of its own and cannot name the parent's resources.
	skipNested bool
}

// walk visits every value of v in sorted key order and list order, so the first
// halt is the same on every run.
func (s *omitScan) walk(v any, path []string) error {
	switch x := v.(type) {
	case string:
		return s.check(x, path)
	case map[string]any:
		if len(path) == 2 && path[0] == sectionResources {
			saved := s.skipNested
			s.skipNested = nestedScopeIsOwn(x, s.v2)
			defer func() { s.skipNested = saved }()
		}
		for _, key := range sortedKeys(x) {
			if s.skipNested && len(path) == 3 && path[2] == "properties" && key == "template" {
				continue
			}
			if err := s.walk(x[key], append(path[:len(path):len(path)], key)); err != nil {
				return err
			}
		}
	case []any:
		for i, item := range x {
			if err := s.walk(item, append(path[:len(path):len(path)], fmt.Sprint(i))); err != nil {
				return err
			}
		}
	}
	return nil
}

// nestedScopeIsOwn reports whether a resource is a nested deployment whose
// template cannot name a resource of the template that holds it: always under
// language version 2.x, and without one when its scope is inner.
func nestedScopeIsOwn(resource map[string]any, v2 bool) bool {
	if !isDeployment(resource) {
		return false
	}
	if v2 {
		return true
	}
	properties, _ := resource["properties"].(map[string]any)
	options, _ := properties["expressionEvaluationOptions"].(map[string]any)
	scope, _ := options["scope"].(string)
	return strings.EqualFold(scope, "inner")
}

// check reads one string at path.
func (s *omitScan) check(text string, path []string) error {
	if len(path) == 4 && path[0] == sectionResources && path[2] == "dependsOn" && !armexpr.IsExpression(text) {
		if r, ok := s.named(text); ok {
			return s.dangling(r, "dependsOn", path)
		}
		return nil
	}
	if !armexpr.IsExpression(text) {
		return nil
	}
	n, err := armexpr.Parse(text)
	if err != nil {
		return refuse(plugincondition.ErrPluginValueNotRepresentable,
			fmt.Sprintf("%s holds an expression at JSON pointer %s that the provider cannot read, so it cannot show the expression leaves unnamed what an omits entry left out; correct the expression, or remove the entry",
				s.subject, s.pointer(path)))
	}
	var fault error
	armexpr.Walk(n, func(node armexpr.Node) bool {
		c, ok := node.(armexpr.Call)
		if fault != nil || !ok || c.Namespace != "" {
			return fault == nil
		}
		fault = s.checkCall(c, path)
		return fault == nil
	})
	return fault
}

// checkCall halts where one call names a removed resource.
func (s *omitScan) checkCall(c armexpr.Call, path []string) error {
	if isSymbolReferenceCall(strings.ToLower(c.Name)) {
		if arg, literal := firstTextArgument(c); literal {
			if r, ok := s.named(arg); ok {
				return s.dangling(r, c.Name, path)
			}
		}
	}
	if s.v2 || !strings.EqualFold(c.Name, "resourceId") {
		return nil
	}
	last, literal := c.Args[len(c.Args)-1].(armexpr.String)
	if !literal {
		return nil
	}
	for _, r := range s.removed {
		if r.name != last.Value {
			continue
		}
		for _, arg := range c.Args[:len(c.Args)-1] {
			if t, ok := arg.(armexpr.String); ok && strings.EqualFold(t.Value, r.typ) {
				return s.dangling(r, c.Name, path)
			}
		}
	}
	return nil
}

// named returns the removed resource a name written in a template names: under
// 2.x the symbolic name, or the head of a parent::child name; without a language
// version the name as written.
func (s *omitScan) named(text string) (removedResource, bool) {
	if s.v2 {
		text, _, _ = strings.Cut(text, "::")
	}
	for _, r := range s.removed {
		if r.name == text {
			return r, true
		}
	}
	return removedResource{}, false
}

// dangling is the halt for a remaining reference to a removed resource.
func (s *omitScan) dangling(r removedResource, form string, path []string) error {
	return refuse(plugincondition.ErrPluginRequiredInputUnresolved,
		fmt.Sprintf("%s names the resource %s, which an omits entry left out, through %s at JSON pointer %s; ARM refuses a template that names a resource it does not declare: leave out what names it as well, or remove the entry",
			s.subject, r.text(), form, s.pointer(path)))
}

// pointer spells path as a JSON pointer from the root of the template as it was
// read, before any resource was removed.
func (s *omitScan) pointer(path []string) string {
	var b strings.Builder
	for i, seg := range path {
		if i == 1 && path[0] == sectionResources && s.position != nil {
			if n, err := strconv.Atoi(seg); err == nil && n < len(s.position) {
				seg = strconv.Itoa(s.position[n])
			}
		}
		b.WriteByte('/')
		b.WriteString(escapePointer(seg))
	}
	return b.String()
}
