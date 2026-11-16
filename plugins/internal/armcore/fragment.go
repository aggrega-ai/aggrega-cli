// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"fmt"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/pkg/anchorcycle"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore/armexpr"
	"go.yaml.in/yaml/v3"
)

// Fragment is one artifact entry read as the sections of an ARM template.
type Fragment struct {
	// Scope is the deployment scope the content is written for: the one its
	// $schema names, or ScopeResourceGroup when it writes none.
	Scope Scope
	// Sections holds the entries of each carried section a fragment writes, keyed
	// by section name: resources, parameters, variables, definitions, outputs and
	// metadata by the name written, functions by "namespace.member". A section the
	// fragment does not write is absent.
	Sections map[string]map[string]any
}

// refuse wraps a halt with the condition that classifies it.
func refuse(condition func() error, message string) error {
	return cerrors.Wrap(condition(), cerrors.TranslationError, cerrors.SourceLocation{}, "arm-provider: "+message)
}

// ReadFragment reads one artifact entry, YAML or JSON, as the sections of an ARM
// template. Every top-level key is accounted for by topLevelSections: carried,
// checked, refused, or reported through the returned diagnostics. A string ARM
// reads as an expression must parse, and a resource's symbolic name must be one
// ARM admits.
func ReadFragment(content []byte) (Fragment, []renderctx.Diagnostic, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return Fragment{}, nil, fmt.Errorf("arm-provider: parse fragment: %w", err)
	}
	// Parsing does not expand aliases; the shared reader below follows them, so a
	// recursive anchor is refused here, in the wording every reader shares.
	if anchor, found := anchorcycle.Find(&root); found {
		return Fragment{}, nil, fmt.Errorf("arm-provider: parse fragment: %s", anchorcycle.Explain(anchor))
	}
	var om types.OrderedMap[any]
	if err := parse.TemplateOutput(content, &om); err != nil {
		return Fragment{}, nil, fmt.Errorf("arm-provider: parse fragment: %w", err)
	}
	body := om.TemplateAlphabeticalMapDeep()

	frag := Fragment{Scope: ScopeResourceGroup, Sections: map[string]map[string]any{}}
	var diags []renderctx.Diagnostic
	for _, key := range om.Keys() {
		value := body[key]
		disp, known := dispositionOf(key)
		if !known {
			diags = append(diags, rendercore.UnsupportedFieldDiagnostic(TargetName, key))
			continue
		}
		switch disp {
		case dispositionScope:
			scope, err := scopeOfSchema(value)
			if err != nil {
				return Fragment{}, nil, err
			}
			frag.Scope = scope
		case dispositionStampedChecked:
			if !isLanguageVersion(value) {
				return Fragment{}, nil, refuse(plugincondition.ErrPluginCannotExpressInput,
					fmt.Sprintf("the fragment writes %s with a value other than %q; the template is written in language version %s, so remove %s from the fragment or write %q",
						sectionLanguageVersion, LanguageVersion, LanguageVersion, sectionLanguageVersion, LanguageVersion))
			}
		case dispositionStampedReported:
			diags = append(diags, rendercore.UnsupportedFieldDiagnostic(TargetName, key))
		case dispositionRefused:
			return Fragment{}, nil, refuse(plugincondition.ErrPluginCannotExpressInput,
				fmt.Sprintf("the fragment writes %s, which changes the apiVersion of every resource the template holds; remove %s and write the apiVersion on each resource", key, key))
		default:
			if err := checkStrings(value, "/"+escapePointer(key)); err != nil {
				return Fragment{}, nil, err
			}
			entries, err := readSection(key, disp, value)
			if err != nil {
				return Fragment{}, nil, err
			}
			frag.Sections[key] = entries
		}
	}
	return frag, diags, nil
}

// scopeOfSchema reads the scope a $schema value names, by the file name at the
// end of its address whatever dated folder holds it.
func scopeOfSchema(value any) (Scope, error) {
	s, ok := value.(string)
	if !ok {
		return "", refuse(plugincondition.ErrPluginCannotExpressInput,
			fmt.Sprintf("%s is not text; write the address of the template schema as text, or remove it for a resource group template", sectionSchema))
	}
	name := s
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	name, _, _ = strings.Cut(name, "#")
	name, _, _ = strings.Cut(name, "?")
	name = strings.TrimSuffix(name, ".json")
	switch name {
	case "deploymentTemplate":
		return ScopeResourceGroup, nil
	case "subscriptionDeploymentTemplate":
		return ScopeSubscription, nil
	case "managementGroupDeploymentTemplate":
		return ScopeManagementGroup, nil
	case "tenantDeploymentTemplate":
		return "", refuse(plugincondition.ErrPluginCannotExpressInput,
			fmt.Sprintf("%s names a tenant scope template, which the provider does not write; use a resource group, subscription or management group template", sectionSchema))
	}
	return "", refuse(plugincondition.ErrPluginCannotExpressInput,
		fmt.Sprintf("%s does not name a template schema the provider reads (deploymentTemplate, subscriptionDeploymentTemplate or managementGroupDeploymentTemplate); correct it, or remove it for a resource group template", sectionSchema))
}

// isLanguageVersion reports whether a languageVersion value is the stamped one.
func isLanguageVersion(v any) bool {
	switch x := v.(type) {
	case string:
		return x == LanguageVersion
	case float64:
		return x == 2
	}
	return false
}

// readSection reads one carried section into its entries.
func readSection(key string, disp disposition, value any) (map[string]any, error) {
	switch disp {
	case dispositionCarriedByMember:
		return readFunctions(value)
	case dispositionMergedBySymbol:
		m, ok := value.(map[string]any)
		if !ok && value != nil {
			return nil, refuse(plugincondition.ErrPluginCannotExpressInput,
				fmt.Sprintf("%s is not an object; key resources by symbolic name, as language version %s writes them", key, LanguageVersion))
		}
		for _, name := range sortedKeys(m) {
			if !isSymbolicName(name) {
				return nil, refuse(plugincondition.ErrPluginValueNotRepresentable,
					fmt.Sprintf("the symbolic name at JSON pointer /%s/%s is not one ARM admits; use letters, digits and underscores, starting with a letter or an underscore", key, escapePointer(name)))
			}
		}
		return clone(m), nil
	}
	m, ok := value.(map[string]any)
	if !ok && value != nil {
		return nil, refuse(plugincondition.ErrPluginCannotExpressInput,
			fmt.Sprintf("%s is not an object; write its entries as an object keyed by name", key))
	}
	return clone(m), nil
}

// readFunctions reads the functions list into entries keyed "namespace.member".
func readFunctions(value any) (map[string]any, error) {
	out := map[string]any{}
	if value == nil {
		return out, nil
	}
	list, ok := value.([]any)
	if !ok {
		return nil, refuse(plugincondition.ErrPluginCannotExpressInput,
			fmt.Sprintf("%s is not a list; write a list of namespaces, each with a namespace name and its members", sectionFunctions))
	}
	for i, item := range list {
		ns, ok := item.(map[string]any)
		name, nameOK := ns["namespace"].(string)
		members, membersOK := ns["members"].(map[string]any)
		if !ok || !nameOK || !membersOK {
			return nil, refuse(plugincondition.ErrPluginCannotExpressInput,
				fmt.Sprintf("the entry at JSON pointer /%s/%d is not a namespace with a text namespace name and an object of members", sectionFunctions, i))
		}
		for _, member := range sortedKeys(members) {
			full := name + "." + member
			if _, dup := out[full]; dup {
				return nil, refuse(plugincondition.ErrPluginCannotExpressInput,
					fmt.Sprintf("the function declared at JSON pointer /%s/%d/members/%s is declared more than once in one fragment; declare it once", sectionFunctions, i, escapePointer(member)))
			}
			out[full] = members[member]
		}
	}
	return out, nil
}

// isSymbolicName reports whether name is a symbolic name ARM admits: letters,
// digits and underscores, starting with a letter or an underscore.
func isSymbolicName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
		if !letter && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// checkStrings walks a section's value in sorted key order and refuses the first
// string ARM reads as an expression that does not parse.
func checkStrings(v any, pointer string) error {
	switch x := v.(type) {
	case string:
		if !armexpr.IsExpression(x) {
			return nil
		}
		_, err := armexpr.Parse(x)
		if err == nil {
			return nil
		}
		reason, offset := err.Error(), 0
		if se, ok := err.(*armexpr.SyntaxError); ok {
			reason, offset = se.Reason, se.Offset
			if offset < len(x) && x[offset] == '[' {
				return refuse(plugincondition.ErrPluginValueNotRepresentable,
					fmt.Sprintf("the text at JSON pointer %s writes a value inside an expression, at offset %d, where ARM reads brackets as an expression and not as text; compose the value in literal text instead, writing the delivered value whole inside the text around it, or write [[ to keep the text literal", pointer, offset))
			}
		}
		return refuse(plugincondition.ErrPluginValueNotRepresentable,
			fmt.Sprintf("the text at JSON pointer %s is read by ARM as an expression but does not parse (offset %d: %s); correct the expression, or write [[ at its start to keep it literal text", pointer, offset, reason))
	case map[string]any:
		for _, k := range sortedKeys(x) {
			if err := checkStrings(x[k], pointer+"/"+escapePointer(k)); err != nil {
				return err
			}
		}
	case []any:
		for i, e := range x {
			if err := checkStrings(e, fmt.Sprintf("%s/%d", pointer, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

// escapePointer escapes one reference token of a JSON pointer.
func escapePointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

// sortedKeys returns the keys of m in sorted order, so nothing derived from a
// map depends on its iteration order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// clone returns a copy of m, never nil.
func clone(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
