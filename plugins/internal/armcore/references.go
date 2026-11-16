// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore/armexpr"
)

// deploymentsResourceType is the resource type of a nested deployment, matched
// without regard to case.
const deploymentsResourceType = "Microsoft.Resources/deployments"

// level is one template level under assembly: the merged entries of each carried
// section, keyed by section name and then by the name the entry is written under
// in the assembled template, a function by "namespace.member" as Fragment keys
// it. Each entry records the contributor that wrote it and where, so every halt
// raised over the level names both.
//
// A level holds what ReadFragment admitted, so every string in it that ARM reads
// as an expression parses. composeLiterals runs once on a level, before
// checkReferences: it rewrites literal text into format() expressions, and a
// second run would read those as expressions an author wrote.
type level struct {
	sections map[string]map[string]*levelEntry
}

// levelEntry is one top-level entry of a level.
type levelEntry struct {
	// body is the entry as the template writes it. composeLiterals replaces it
	// with a rewritten copy where it rewrites a string inside it, and never
	// changes the value it was given in place.
	body any
	// contributor is the identity of the contributor that wrote the entry, as a
	// halt names it.
	contributor string
	// pointer is the JSON pointer of the entry in that contributor's fragment,
	// such as /resources/App or /outputs/host. A halt names a string inside the
	// entry by extending it.
	pointer string
}

// newLevel returns a level holding no entries.
func newLevel() *level {
	return &level{sections: map[string]map[string]*levelEntry{}}
}

// put records e as the entry named name in section, replacing any entry already
// recorded under that name.
func (l *level) put(section, name string, e *levelEntry) {
	if l.sections[section] == nil {
		l.sections[section] = map[string]*levelEntry{}
	}
	l.sections[section][name] = e
}

// site is where one string sits in a level: its section, the entry holding it,
// and the keys and list indexes leading from the entry down to it.
type site struct {
	section string
	entry   *levelEntry
	path    []string
	// deployment reports that the entry is a nested deployment resource.
	deployment bool
}

// child returns the site one key or index below s.
func (s site) child(key string) site {
	s.path = append(slices.Clip(s.path), key)
	return s
}

// pointer returns the JSON pointer of the site in its contributor's fragment.
func (s site) pointer() string {
	var b strings.Builder
	b.WriteString(s.entry.pointer)
	for _, key := range s.path {
		b.WriteByte('/')
		b.WriteString(escapePointer(key))
	}
	return b.String()
}

// under reports whether the site lies in section, below the entry key first.
func (s site) under(section, first string) bool {
	return s.section == section && len(s.path) > 0 && s.path[0] == first
}

// readsRuntimeState reports whether ARM evaluates reference and the list
// functions at the site: inside a resource's properties or an output's value.
// Those are the places the template function reference documents for them; a
// resource's name, location, tags and other top-level fields are not among them.
func (s site) readsRuntimeState() bool {
	return s.under(sectionResources, "properties") || s.under(sectionOutputs, "value")
}

// isDependsOnEntry reports whether the site is one entry of a resource's
// dependsOn list.
func (s site) isDependsOnEntry() bool {
	return s.under(sectionResources, "dependsOn") && len(s.path) == 2
}

// isNestedTemplate reports whether the site is the template body of a nested
// deployment. That body is a template level of its own, evaluated in its own
// scope: the only scope language version 2.0 admits for a nested template.
func (s site) isNestedTemplate() bool {
	return s.deployment && len(s.path) == 2 && s.path[0] == "properties" && s.path[1] == "template"
}

// located returns the JSON pointer of the site and the contributor whose
// fragment holds it, as a halt names them.
func (s site) located() string {
	return fmt.Sprintf("%s in the fragment of %q", s.pointer(), s.entry.contributor)
}

// named returns the phrase that opens a halt raised at the site.
func (s site) named() string {
	return "at JSON pointer " + s.located()
}

// parse reads text, which ARM reads as an expression, at the site.
func (s site) parse(text string) (armexpr.Node, error) {
	n, err := armexpr.Parse(text)
	if err == nil {
		return n, nil
	}
	// The fragment reader refuses such text before a level holds it; its refusal
	// is the one sentence for an expression that does not parse, extended here to
	// name the contributor.
	return nil, checkStrings(text, s.located())
}

// visitFunc is called with each string of a level and returns the string to keep
// in its place.
type visitFunc func(s site, text string) (string, error)

// eachString visits every string of the level: sections and entries in name
// order, keys in sorted order and list items in order, so the first halt is the
// same on every run. The template body of a nested deployment is passed over. A
// string the visit replaces is written into a copy of each map and list above
// it, and the entry's body becomes that copy.
func (l *level) eachString(visit visitFunc) error {
	for _, section := range sortedKeys(l.sections) {
		entries := l.sections[section]
		for _, name := range sortedKeys(entries) {
			e := entries[name]
			s := site{section: section, entry: e, deployment: section == sectionResources && isDeployment(e.body)}
			body, changed, err := walkStrings(e.body, s, visit)
			if err != nil {
				return err
			}
			if changed {
				e.body = body
			}
		}
	}
	return nil
}

// walkStrings visits the strings of v, returning v, or a copy of it holding each
// string the visit replaced, and whether anything was replaced.
func walkStrings(v any, s site, visit visitFunc) (any, bool, error) {
	switch x := v.(type) {
	case string:
		out, err := visit(s, x)
		if err != nil {
			return nil, false, err
		}
		return out, out != x, nil
	case map[string]any:
		var out map[string]any
		for _, key := range sortedKeys(x) {
			child := s.child(key)
			if child.isNestedTemplate() {
				continue
			}
			value, changed, err := walkStrings(x[key], child, visit)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if out == nil {
					out = clone(x)
				}
				out[key] = value
			}
		}
		if out == nil {
			return x, false, nil
		}
		return out, true, nil
	case []any:
		var out []any
		for i, item := range x {
			value, changed, err := walkStrings(item, s.child(strconv.Itoa(i)), visit)
			if err != nil {
				return nil, false, err
			}
			if changed {
				if out == nil {
					out = slices.Clone(x)
				}
				out[i] = value
			}
		}
		if out == nil {
			return x, false, nil
		}
		return out, true, nil
	}
	return v, false, nil
}

// isDeployment reports whether a resource body is a nested deployment.
func isDeployment(body any) bool {
	m, _ := body.(map[string]any)
	typ, _ := m["type"].(string)
	return strings.EqualFold(typ, deploymentsResourceType)
}

// declarations is what one level declares for its expressions to name: resource
// symbolic names as written, since ARM compares them with case, and parameter
// and variable names in lower case, since ARM reads those without regard to case.
type declarations struct {
	symbols, parameters, variables map[string]bool
}

// declared returns what the level declares.
func (l *level) declared() declarations {
	r := declarations{symbols: map[string]bool{}, parameters: map[string]bool{}, variables: map[string]bool{}}
	for _, name := range sortedKeys(l.sections[sectionResources]) {
		r.symbols[name] = true
	}
	for _, name := range sortedKeys(l.sections[sectionParameters]) {
		r.parameters[strings.ToLower(name)] = true
	}
	variables := l.sections[sectionVariables]
	for _, name := range sortedKeys(variables) {
		r.variables[strings.ToLower(name)] = true
		// The copy entry declares one variable per item, named by the item.
		if name != "copy" {
			continue
		}
		items, _ := variables[name].body.([]any)
		for _, item := range items {
			m, _ := item.(map[string]any)
			if n, ok := m["name"].(string); ok {
				r.variables[strings.ToLower(n)] = true
			}
		}
	}
	return r
}

// checkReferences checks every reference in the level against what the level
// declares, and every call to reference or a list function against where it
// sits. Each symbolic name must name a resource of the level: the first argument
// of reference, references, resourceInfo or a list function when it is text that
// does not start with a slash (text that does is a resource ID), and each text
// entry of a resource's dependsOn on the same terms. The text argument of
// parameters or variables must name a declaration of the level, compared without
// regard to case. A dangling reference halts wrapping
// ErrPluginRequiredInputUnresolved. A call to reference or a list function
// outside a resource's properties or an output's value halts wrapping
// ErrPluginValueNotRepresentable. Each halt names the contributor, the JSON
// pointer and, for a dangling reference, the name.
//
// The template body of a nested deployment is a level of its own and is not
// read. A user-defined function's body reads its own parameters and no variable,
// so only the placement of its calls is checked.
func (l *level) checkReferences() error {
	declared := l.declared()
	return l.eachString(func(s site, text string) (string, error) {
		if s.isDependsOnEntry() && !armexpr.IsExpression(text) {
			if !strings.HasPrefix(text, "/") && !declared.symbols[text] {
				return "", danglingSymbol(s, "dependsOn", text)
			}
			return text, nil
		}
		if !armexpr.IsExpression(text) {
			return text, nil
		}
		n, err := s.parse(text)
		if err != nil {
			return "", err
		}
		var fault error
		armexpr.Walk(n, func(node armexpr.Node) bool {
			if fault != nil {
				return false
			}
			if c, ok := node.(armexpr.Call); ok && c.Namespace == "" {
				fault = declared.check(s, c)
			}
			return fault == nil
		})
		return text, fault
	})
}

// check checks one call to a template function at site s.
func (r declarations) check(s site, c armexpr.Call) error {
	name := strings.ToLower(c.Name)
	readsState := name == "reference" || isListFunction(name)
	if readsState && !s.readsRuntimeState() {
		return refuse(plugincondition.ErrPluginValueNotRepresentable,
			fmt.Sprintf("%s, the expression calls %s, which ARM evaluates only inside a resource's properties or an output's value; move the call into a resource's properties or an output's value",
				s.named(), c.Name))
	}
	if s.section == sectionFunctions {
		return nil
	}
	arg, literal := firstTextArgument(c)
	if !literal {
		return nil
	}
	switch {
	case readsState || name == "references" || name == "resourceinfo":
		if !strings.HasPrefix(arg, "/") && !r.symbols[arg] {
			return danglingSymbol(s, c.Name, arg)
		}
	case name == "parameters":
		if !r.parameters[strings.ToLower(arg)] {
			return danglingDeclaration(s, c.Name, "parameter", arg, sectionParameters)
		}
	case name == "variables":
		if !r.variables[strings.ToLower(arg)] {
			return danglingDeclaration(s, c.Name, "variable", arg, sectionVariables)
		}
	}
	return nil
}

// danglingSymbol is the halt for a symbolic name no resource of the level
// declares.
func danglingSymbol(s site, by, symbol string) error {
	return refuse(plugincondition.ErrPluginRequiredInputUnresolved,
		fmt.Sprintf("%s, %s names the resource %q, which no resource in this template declares by that symbolic name; correct the name (symbolic names are case-sensitive), declare the resource in this template, or name a resource deployed elsewhere by its resource ID",
			s.named(), by, symbol))
}

// danglingDeclaration is the halt for a parameter or variable name the level
// does not declare.
func danglingDeclaration(s site, by, kind, name, section string) error {
	return refuse(plugincondition.ErrPluginRequiredInputUnresolved,
		fmt.Sprintf("%s, %s names the %s %q, which this template does not declare; declare it under %s or correct the name",
			s.named(), by, kind, name, section))
}

// firstTextArgument returns a call's first argument when it is a string literal.
func firstTextArgument(c armexpr.Call) (string, bool) {
	if len(c.Args) == 0 {
		return "", false
	}
	str, ok := c.Args[0].(armexpr.String)
	return str.Value, ok
}

// isListFunction reports whether name is a function of the list family: list
// itself or any name beginning with list, as the template function reference
// documents it, matched without regard to case.
func isListFunction(name string) bool {
	return len(name) >= len("list") && strings.EqualFold(name[:len("list")], "list")
}

// IsSecretExpression reports whether evaluating n can yield a secret: n calls a
// list function (listKeys, listSecrets, listOutputsWithSecureValues and every
// other name beginning with list), or reads through parameters a parameter that
// secureParameters marks true, the producer template's securestring and
// secureObject parameters. Parameter names match without regard to case, as ARM
// reads them.
func IsSecretExpression(n armexpr.Node, secureParameters map[string]bool) bool {
	secret := false
	armexpr.Walk(n, func(node armexpr.Node) bool {
		if secret {
			return false
		}
		c, ok := node.(armexpr.Call)
		if !ok || c.Namespace != "" {
			return true
		}
		switch {
		case isListFunction(c.Name):
			secret = true
		case strings.EqualFold(c.Name, "parameters"):
			if p, literal := firstTextArgument(c); literal && isSecureParameter(p, secureParameters) {
				secret = true
			}
		}
		return !secret
	})
	return secret
}

// isSecureParameter reports whether secureParameters marks the parameter name
// true, matching without regard to case.
func isSecureParameter(name string, secureParameters map[string]bool) bool {
	if secureParameters[name] {
		return true
	}
	for _, declared := range sortedKeys(secureParameters) {
		if secureParameters[declared] && strings.EqualFold(declared, name) {
			return true
		}
	}
	return false
}
