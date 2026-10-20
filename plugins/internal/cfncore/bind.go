// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The attributes of a CloudFormation parameter declaration this core reads, each
// the key an author writes under the parameter's logical ID.
const (
	parameterType           = "Type"
	parameterNoEcho         = "NoEcho"
	parameterAllowedValues  = "AllowedValues"
	parameterAllowedPattern = "AllowedPattern"
	parameterMinLength      = "MinLength"
	parameterMaxLength      = "MaxLength"
	parameterMinValue       = "MinValue"
	parameterMaxValue       = "MaxValue"
)

// The parameter types whose value is not one string. A Number is converted to the
// string a Ref to it yields; a list type to the list of strings a Ref to it yields;
// a Systems Manager value type names a Parameter Store key whose stored value
// CloudFormation reads at deploy, which no literal written into the template
// reproduces.
const (
	parameterTypeNumber             = "Number"
	parameterTypeCommaDelimitedList = "CommaDelimitedList"
	parameterTypeListPrefix         = "List<"
	parameterTypeSSMValuePrefix     = "AWS::SSM::Parameter::Value<"
)

// The places CloudFormation does not resolve a dynamic reference, or shows it to
// whoever reads the stack: an EC2 instance's user data, the
// AWS::CloudFormation::Init metadata a bootstrap script reads, and the stack's
// Outputs, which CloudFormation never redacts.
const (
	propertyUserData  = "UserData"
	metadataCfnInit   = "AWS::CloudFormation::Init"
	resourceMetadata  = "Metadata"
	resourceProperty  = "Properties"
	ruleFunctionValue = "Fn::ValueOf"
)

// dynamicReference matches a value that is wholly one CloudFormation dynamic
// reference — {{resolve:ssm:…}}, {{resolve:ssm-secure:…}} or
// {{resolve:secretsmanager:…}} — the form CloudFormation resolves when the stack is
// created or updated, so the value it names is never written into the template.
var dynamicReference = regexp.MustCompile(`^\{\{resolve:(ssm|ssm-secure|secretsmanager):[^{}]+\}\}$`)

// shortFormIntrinsicTags are the YAML short forms CloudFormation defines for its
// intrinsic functions, each written as the tag an author types. A bound value that
// is wholly one of them is read as that intrinsic; a value opening with any other
// "!" stays text, so a value that merely starts with an exclamation mark keeps its
// meaning.
var shortFormIntrinsicTags = map[string]bool{
	"Ref": true, "Condition": true, "GetAtt": true, "Base64": true, "Cidr": true,
	"FindInMap": true, "GetAZs": true, "ImportValue": true, "Join": true, "Select": true,
	"Split": true, "Sub": true, "Transform": true, "And": true, "Equals": true,
	"If": true, "Not": true, "Or": true,
}

// BindInput is what a sourced render hands the binding: the node's values, whether
// a values mapping produced them, and whether that mapping read a value an author
// declared sensitive.
//
// A mapping's output keys are the mapping's own invention, so no declaration names
// one of them: the payload marks a mapped node's values only as a whole, never key
// by key, and the whole-map mark is the one sensitivity marking a binding can see.
type BindInput struct {
	// Values are the node's values, keyed by the names a values mapping gave them.
	Values types.OrderedMap[any]
	// Mapped reports that a values mapping produced Values. Only then are they read:
	// values the author wrote for something else — the Recipe's artifacts, say —
	// name no parameter and are left alone.
	Mapped bool
	// SensitiveValues reports that the mapping producing Values read a value an
	// author declared sensitive, so no literal it produced is written into a
	// template.
	SensitiveValues bool
}

// boundKind is what a bound value is to CloudFormation, which decides what the
// binding may do with it.
type boundKind int

const (
	// boundLiteral is a value written into the template as it stands: a string, a
	// number, a boolean or a list of them.
	boundLiteral boundKind = iota
	// boundIntrinsic is an intrinsic function, which CloudFormation evaluates at
	// deploy: a long-form mapping, or a string that is wholly one short form.
	boundIntrinsic
	// boundDynamicReference is a {{resolve:…}} reference, which CloudFormation
	// resolves at deploy without writing the value it names into the template.
	boundDynamicReference
	// boundLink is the placeholder a cross-stack link replaces with an import once
	// every stack has assembled.
	boundLink
)

// boundValue is one value the binding substitutes, as CloudFormation reads it.
type boundValue struct {
	kind  boundKind
	value any
}

// literal reports whether the value is written into the template as it stands.
func (b boundValue) literal() bool { return b.kind == boundLiteral }

// BindSourced binds a sourced source's declared parameters to the node's values,
// returning the templates with every bound parameter's references replaced by its
// value and its declaration removed. It runs after normalization and before
// anything that reads the templates, so the omit entries, the provenance stamp and
// the declared names all read the bound templates.
//
// It reads the values only where a values mapping produced them (BindInput.Mapped)
// and the node carries any; otherwise every file comes back exactly as it was
// handed in, so a sourced render that binds nothing is byte-identical to one made
// before binding existed.
//
// Each key names a parameter some template of the source declares. A key naming no
// declared parameter halts, naming the key and what each template declares — the
// same failure a deploy would report for an unknown parameter, reported at build
// time.
//
// The source is the unit that declares a parameter, because its templates assemble
// into one stack, so a bound parameter is bound in every template of the source:
// one that declares it and one that only references it alike. In each, every Ref to
// it in Resources, Outputs, Conditions, Rules and SAM's Globals is replaced by the
// value, and every Fn::Sub naming it as a variable receives it through the list
// form's variable map; a map that already names the variable keeps its own value,
// because CloudFormation reads that map's value for it. The parameter then leaves
// the Parameters of every template declaring it. Each declaring template converts
// and checks the value against its own declaration, and a template that only
// references the parameter takes the value as the first template of the source
// declaring it converted it. A template that neither declares nor references a
// bound parameter comes back exactly as it was. A parameter no key names is left
// exactly as it was, for a deploy-time value or its Default to fill.
//
// What a value is decides what the binding does with it. A string that is wholly
// one short-form intrinsic — the forms the template's own normalization reads — is
// read as that intrinsic, and a long-form mapping is one already; a {{resolve:…}}
// string is a dynamic reference; the placeholder a cross-stack link later replaces
// is kept whole. Each of those reaches the reference site as it is, for
// CloudFormation to evaluate. Anything else is a literal, converted only as
// CloudFormation converts the same value passed for the parameter's type at deploy
// and checked against the parameter's declared constraints; a value with no
// faithful conversion, or one the constraints refuse, halts naming the parameter.
//
// A literal that may carry secret material is refused, while a dynamic reference
// and an intrinsic are written as given. A NoEcho parameter takes a dynamic
// reference or an intrinsic and refuses a literal. Where the mapping read a value an
// author declared sensitive, every literal binding of the node is refused, and a
// dynamic reference or an intrinsic is admitted as written — the text inside an
// intrinsic included, so a sensitive value the mapping splices into an Fn::Sub
// string reaches the template as that text. A dynamic reference is refused where
// its parameter is referenced in EC2 user data, AWS::CloudFormation::Init metadata
// or the template's Outputs. No refusal quotes a value: each names the parameters,
// the templates and what to bind instead.
func BindSourced(files []SourcedFile, rootIsDir bool, in BindInput, nodeID string) ([]SourcedFile, error) {
	if !in.Mapped || in.Values.Len() == 0 {
		return files, nil
	}

	type template struct {
		index int
		path  string
		doc   map[string]any
	}
	var templates []template
	declaredIn := map[string][]int{}
	for i, f := range files {
		if rootIsDir && !isTemplateFile(f.Path) {
			continue
		}
		doc, err := decodeNormalizedTemplate(f.Content)
		if err != nil {
			return nil, fmt.Errorf("cloudformation-provider: read sourced template %q to bind its parameters: %w", f.Path, err)
		}
		if doc == nil {
			continue
		}
		params, _ := doc[sectionParameters].(map[string]any)
		for name := range params {
			declaredIn[name] = append(declaredIn[name], len(templates))
		}
		templates = append(templates, template{index: i, path: f.Path, doc: doc})
	}

	keys := in.Values.Keys()
	sort.Strings(keys)
	var unknown []string
	for _, k := range keys {
		if len(declaredIn[k]) == 0 {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		var declared []string
		for _, t := range templates {
			params, _ := t.doc[sectionParameters].(map[string]any)
			names := sortedKeys(params)
			if len(names) == 0 {
				declared = append(declared, fmt.Sprintf("template %q declares no parameter", t.path))
				continue
			}
			declared = append(declared, fmt.Sprintf("template %q declares %s", t.path, quotedList(names)))
		}
		if len(declared) == 0 {
			declared = append(declared, "the source carries no template")
		}
		return nil, cerrors.Wrap(wasm.ErrPluginRequiredInputUnresolved(),
			cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
			fmt.Sprintf("cloudformation-provider: the Recipe's or FeatureRecipe's valuesMapping writes %s, and no template of the source declares a parameter of that name: %s; a sourced template takes values only through the parameters it declares, so rename the key to the parameter it is meant for, or remove it from the valuesMapping",
				pluralKeys(unknown), strings.Join(declared, "; ")))
	}

	bound := make(map[string]boundValue, len(keys))
	for _, k := range keys {
		raw, _ := in.Values.Get(k)
		v, err := plainValue(raw)
		if err != nil {
			return nil, fmt.Errorf("cloudformation-provider: read the value bound to parameter %q: %w", k, err)
		}
		bound[k] = classifyBound(v)
	}

	// Each declaring template converts and checks the value against its own
	// declaration. The first conversion in the source's order is the one a template
	// that only references the parameter takes, so which template that is never
	// depends on anything but the order the source lists them in.
	var sensitiveLiterals []string
	declaredValues := make([]map[string]any, len(templates))
	sourceValue := map[string]any{}
	for ti, t := range templates {
		params, _ := t.doc[sectionParameters].(map[string]any)
		values := map[string]any{}
		for _, name := range sortedKeys(params) {
			b, ok := bound[name]
			if !ok {
				continue
			}
			decl, _ := params[name].(map[string]any)
			if err := refuseSensitiveBinding(name, t.path, b, decl, in, &sensitiveLiterals); err != nil {
				return nil, err
			}
			v, err := parameterValue(name, t.path, b, decl)
			if err != nil {
				return nil, err
			}
			values[name] = v
			if _, seen := sourceValue[name]; !seen {
				sourceValue[name] = v
			}
		}
		declaredValues[ti] = values
	}

	out := make([]SourcedFile, len(files))
	copy(out, files)
	for ti, t := range templates {
		params, _ := t.doc[sectionParameters].(map[string]any)
		values := make(map[string]any, len(sourceValue))
		for name, v := range sourceValue {
			values[name] = v
		}
		for name, v := range declaredValues[ti] {
			values[name] = v
		}
		for _, name := range keys {
			if bound[name].kind != boundDynamicReference {
				continue
			}
			if site := unresolvedDynamicReferenceSite(t.doc, name); site != "" {
				return nil, cerrors.Wrap(wasm.ErrPluginValueNotRepresentable(),
					cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
					fmt.Sprintf("cloudformation-provider: the value bound to parameter %q is a dynamic reference, and template %q references the parameter in %s, where CloudFormation does not resolve a dynamic reference or shows it to whoever reads the stack; bind an intrinsic function instead, or leave the parameter unbound for a deploy-time value",
						name, t.path, site))
			}
		}
		if err := refuseRuleNamingBound(t.doc, t.path, values, nodeID); err != nil {
			return nil, err
		}
		changed := len(declaredValues[ti]) > 0
		for _, section := range boundSections {
			body, present := t.doc[section]
			if !present {
				continue
			}
			replaced, err := replaceParameterReferences(body, values, t.path, nodeID)
			if err != nil {
				return nil, err
			}
			if !reflect.DeepEqual(replaced, body) {
				changed = true
			}
			t.doc[section] = replaced
		}
		// A template that neither declares nor references a bound parameter keeps
		// the bytes it was handed, as a source binding nothing does.
		if !changed {
			continue
		}
		kept := make(map[string]any, len(params))
		for name, decl := range params {
			if _, isBound := values[name]; !isBound {
				kept[name] = decl
			}
		}
		if len(kept) == 0 {
			delete(t.doc, sectionParameters)
		} else {
			t.doc[sectionParameters] = kept
		}
		body, err := encodeNormalizedTemplate(t.doc)
		if err != nil {
			return nil, fmt.Errorf("cloudformation-provider: write sourced template %q with its parameters bound: %w", t.path, err)
		}
		out[t.index] = SourcedFile{Path: t.path, Content: body}
	}

	if len(sensitiveLiterals) > 0 {
		sort.Strings(sensitiveLiterals)
		return nil, cerrors.Wrap(wasm.ErrPluginValueNotRepresentable(),
			cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
			fmt.Sprintf("cloudformation-provider: the Recipe's or FeatureRecipe's valuesMapping reads a value declared sensitive, so a literal it produces is refused, and %s would write a literal into the template as it stands; bind each to a dynamic reference ({{resolve:secretsmanager:…}} or {{resolve:ssm-secure:…}}) or an intrinsic function, or leave it unbound for a deploy-time value",
				pluralParameters(sensitiveLiterals)))
	}
	return out, nil
}

// boundSections are the sections whose references to a bound parameter are
// replaced: the ones CloudFormation evaluates a parameter reference in, and SAM's
// Globals, which its transform reads into every serverless resource. Mappings may
// not reference a parameter, the template-level Metadata is stored rather than
// evaluated, and Parameters is where the declaration itself sits.
var boundSections = []string{sectionResources, sectionOutputs, sectionConditions, sectionRules, sectionGlobals}

// refuseSensitiveBinding refuses a literal binding that may carry secret material
// into the template: a literal bound to a NoEcho parameter is refused here, and a
// literal binding of a node whose whole values are marked is recorded rather than
// refused, so one refusal names every such parameter. A dynamic reference and an
// intrinsic pass either way.
func refuseSensitiveBinding(name, path string, b boundValue, decl map[string]any, in BindInput, sensitiveLiterals *[]string) error {
	if truthy(decl[parameterNoEcho]) && b.literal() {
		return cerrors.Wrap(wasm.ErrPluginValueNotRepresentable(),
			cerrors.TranslationError, cerrors.SourceLocation{},
			fmt.Sprintf("cloudformation-provider: parameter %q of template %q is declared NoEcho, and the value bound to it would be written into the template as it stands; bind a dynamic reference ({{resolve:secretsmanager:…}} or {{resolve:ssm-secure:…}}) or an intrinsic function instead, or leave the parameter unbound for a deploy-time value",
				name, path))
	}
	if in.SensitiveValues && b.literal() {
		for _, seen := range *sensitiveLiterals {
			if seen == name {
				return nil
			}
		}
		*sensitiveLiterals = append(*sensitiveLiterals, name)
	}
	return nil
}

// parameterValue returns what replaces a reference to one bound parameter. A value
// CloudFormation evaluates at deploy is returned as it is. A literal is converted
// as CloudFormation converts the same value passed for the parameter at deploy — a
// Number to the string a Ref to it yields, a list type to a list of strings, every
// other type to one string — and then checked against the parameter's declared
// constraints, so binding removes no check the template declared.
func parameterValue(name, path string, b boundValue, decl map[string]any) (any, error) {
	if !b.literal() {
		return deepCopy(b.value), nil
	}
	typ, _ := decl[parameterType].(string)
	notRepresentable := func(why string) error {
		return cerrors.Wrap(wasm.ErrPluginValueNotRepresentable(),
			cerrors.TranslationError, cerrors.SourceLocation{},
			fmt.Sprintf("cloudformation-provider: parameter %q of template %q is of type %s, and %s", name, path, typeText(typ), why))
	}

	switch {
	case strings.HasPrefix(typ, parameterTypeSSMValuePrefix):
		return nil, notRepresentable("CloudFormation reads such a parameter's value from Parameter Store when the stack deploys, which no value written into the template reproduces; bind a {{resolve:ssm:…}} dynamic reference or an intrinsic function instead, or leave the parameter unbound")
	case typ == parameterTypeCommaDelimitedList || strings.HasPrefix(typ, parameterTypeListPrefix):
		members, ok := listMembers(b.value)
		if !ok {
			return nil, notRepresentable("the value bound to it is neither a list nor a comma-separated string")
		}
		if typ == "List<Number>" {
			for _, m := range members {
				if _, err := strconv.ParseFloat(m, 64); err != nil {
					return nil, notRepresentable("a member of the value bound to it is not a number")
				}
			}
		}
		if err := checkConstraints(name, path, typ, members, decl); err != nil {
			return nil, err
		}
		list := make([]any, 0, len(members))
		for _, m := range members {
			list = append(list, m)
		}
		return list, nil
	default:
		text, ok := scalarText(b.value)
		if !ok {
			return nil, notRepresentable("the value bound to it is not one value; a parameter of this type takes one")
		}
		if typ == parameterTypeNumber {
			if _, err := strconv.ParseFloat(text, 64); err != nil {
				return nil, notRepresentable("the value bound to it is not a number")
			}
		}
		if err := checkConstraints(name, path, typ, []string{text}, decl); err != nil {
			return nil, err
		}
		return text, nil
	}
}

// checkConstraints checks a literal against the constraints its parameter declares,
// each member of a list on its own, the way CloudFormation checks a value passed at
// deploy. A pattern Go's regular expressions cannot read is one this build cannot
// evaluate, so it is left for CloudFormation to check, as an intrinsic is.
func checkConstraints(name, path, typ string, members []string, decl map[string]any) error {
	violated := func(constraint string) error {
		return cerrors.Wrap(wasm.ErrPluginValueNotRepresentable(),
			cerrors.TranslationError, cerrors.SourceLocation{},
			fmt.Sprintf("cloudformation-provider: the value bound to parameter %q of template %q does not satisfy its %s, which CloudFormation would refuse when the stack deploys; bind a value the parameter admits",
				name, path, constraint))
	}
	if allowed, ok := decl[parameterAllowedValues].([]any); ok {
		admitted := map[string]bool{}
		for _, a := range allowed {
			if text, ok := scalarText(a); ok {
				admitted[text] = true
			}
		}
		for _, m := range members {
			if !admitted[m] {
				return violated(parameterAllowedValues)
			}
		}
	}
	if pattern, ok := decl[parameterAllowedPattern].(string); ok {
		if re, err := regexp.Compile("^(?:" + pattern + ")$"); err == nil {
			for _, m := range members {
				if !re.MatchString(m) {
					return violated(parameterAllowedPattern)
				}
			}
		}
	}
	if typ == parameterTypeNumber {
		for _, m := range members {
			v, _ := strconv.ParseFloat(m, 64)
			if limit, ok := numberAttribute(decl[parameterMinValue]); ok && v < limit {
				return violated(parameterMinValue)
			}
			if limit, ok := numberAttribute(decl[parameterMaxValue]); ok && v > limit {
				return violated(parameterMaxValue)
			}
		}
		return nil
	}
	for _, m := range members {
		n := float64(utf8.RuneCountInString(m))
		if limit, ok := numberAttribute(decl[parameterMinLength]); ok && n < limit {
			return violated(parameterMinLength)
		}
		if limit, ok := numberAttribute(decl[parameterMaxLength]); ok && n > limit {
			return violated(parameterMaxLength)
		}
	}
	return nil
}

// refuseRuleNamingBound halts where a rule of the template reads a bound parameter
// through Fn::ValueOf, which names a declared parameter by its logical ID: the
// parameter leaves every template of the source when it is bound, so the rule
// would name nothing.
// Rules are read in the order of their names and each rule's content in the order
// walkValues keeps, so a rule reading several bound parameters halts on the same
// one every time.
func refuseRuleNamingBound(doc map[string]any, path string, values map[string]any, nodeID string) error {
	rules, ok := doc[sectionRules].(map[string]any)
	if !ok {
		return nil
	}
	for _, rule := range sortedKeys(rules) {
		var found string
		walkValues(rules[rule], func(node map[string]any) {
			if found != "" {
				return
			}
			args, ok := node[ruleFunctionValue].([]any)
			if !ok || len(args) == 0 {
				return
			}
			if name, ok := args[0].(string); ok {
				if _, isBound := values[name]; isBound {
					found = name
				}
			}
		})
		if found != "" {
			return cerrors.Wrap(wasm.ErrPluginCannotExpressInput(),
				cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
				fmt.Sprintf("cloudformation-provider: rule %q of template %q reads parameter %q through Fn::ValueOf, which names a declared parameter by its logical ID, and binding the parameter removes its declaration from the source; leave the parameter unbound so the rule can read it at deploy",
					rule, path, found))
		}
	}
	return nil
}

// unresolvedDynamicReferenceSite names the first place a template references one
// parameter where a dynamic reference bound to it would not be resolved, or would be
// shown to whoever reads the stack — EC2 user data, AWS::CloudFormation::Init
// metadata, or the Outputs — and returns the empty string where there is none.
func unresolvedDynamicReferenceSite(doc map[string]any, name string) string {
	if outputs, ok := doc[sectionOutputs].(map[string]any); ok && referencesParameter(outputs, name) {
		return "the Outputs section"
	}
	resources, _ := doc[sectionResources].(map[string]any)
	for _, id := range sortedKeys(resources) {
		body, ok := resources[id].(map[string]any)
		if !ok {
			continue
		}
		if metadata, ok := body[resourceMetadata].(map[string]any); ok {
			if init, present := metadata[metadataCfnInit]; present && referencesParameter(init, name) {
				return fmt.Sprintf("the %s metadata of resource %q", metadataCfnInit, id)
			}
		}
		var inUserData bool
		walkKeyed(body[resourceProperty], func(key string, v any) {
			if key == propertyUserData && referencesParameter(v, name) {
				inUserData = true
			}
		})
		if inUserData {
			return fmt.Sprintf("the %s of resource %q", propertyUserData, id)
		}
	}
	return ""
}

// referencesParameter reports whether a value references one parameter, by Ref or
// as an Fn::Sub variable its variable map does not name.
func referencesParameter(v any, name string) bool {
	var found bool
	walkValues(v, func(node map[string]any) {
		if ref, ok := node[intrinsicRef].(string); ok && ref == name {
			found = true
		}
		if arg, ok := node[intrinsicSub]; ok {
			text, mapped := subParts(arg)
			if _, isMapped := mapped[name]; !isMapped {
				for _, variable := range subVariables(text) {
					if variable == name {
						found = true
					}
				}
			}
		}
	})
	return found
}

// replaceParameterReferences returns one section with every reference to a bound
// parameter replaced by its value: a Ref to it becomes the value, and an Fn::Sub
// naming it as a variable carries it in its variable map. The input is not
// modified. A mapping's children are visited in the lexical order of their keys, so
// where several sites cannot take their value, the halt always names the same one.
func replaceParameterReferences(v any, values map[string]any, path, nodeID string) (any, error) {
	switch node := v.(type) {
	case map[string]any:
		if len(node) == 1 {
			if ref, ok := node[intrinsicRef].(string); ok {
				if value, isBound := values[ref]; isBound {
					return deepCopy(value), nil
				}
			}
			if arg, ok := node[intrinsicSub]; ok {
				bound, err := bindSub(arg, values, path, nodeID)
				if err != nil {
					return nil, err
				}
				return map[string]any{intrinsicSub: bound}, nil
			}
		}
		out := make(map[string]any, len(node))
		for _, k := range sortedKeys(node) {
			replaced, err := replaceParameterReferences(node[k], values, path, nodeID)
			if err != nil {
				return nil, err
			}
			out[k] = replaced
		}
		return out, nil
	case []any:
		out := make([]any, len(node))
		for i, item := range node {
			replaced, err := replaceParameterReferences(item, values, path, nodeID)
			if err != nil {
				return nil, err
			}
			out[i] = replaced
		}
		return out, nil
	default:
		return v, nil
	}
}

// bindSub binds the variables of one Fn::Sub argument that name a bound parameter,
// writing each into the list form's variable map. A variable the map already names
// keeps the map's value, because that is what CloudFormation reads for it; a string
// form naming no bound parameter stays a string. The map's own values are bound
// too, in the order of the map's keys, since a Ref inside one is a reference like
// any other. A variable substitutes one string, so a list bound to a parameter
// named as a variable has no faithful form there and halts.
func bindSub(arg any, values map[string]any, path, nodeID string) (any, error) {
	list, isList := arg.([]any)
	if isList {
		// A list form whose first item is not a string, or whose second is not a
		// variable map, is not one this core reads as Fn::Sub; its contents are bound
		// like any other value and its shape is left for CloudFormation to judge.
		if len(list) == 0 {
			return arg, nil
		}
		_, textOK := list[0].(string)
		mapOK := true
		if len(list) > 1 {
			_, mapOK = list[1].(map[string]any)
		}
		if !textOK || !mapOK {
			return replaceParameterReferences(list, values, path, nodeID)
		}
	} else if _, isText := arg.(string); !isText {
		return replaceParameterReferences(arg, values, path, nodeID)
	}

	text, mapped := subParts(arg)
	var added []string
	for _, variable := range subVariables(text) {
		if _, isBound := values[variable]; !isBound {
			continue
		}
		if _, isMapped := mapped[variable]; isMapped {
			continue
		}
		added = append(added, variable)
	}
	if len(added) == 0 && (!isList || len(list) < 2) {
		return arg, nil
	}
	vars := make(map[string]any, len(mapped)+len(added))
	for _, k := range sortedKeys(mapped) {
		replaced, err := replaceParameterReferences(mapped[k], values, path, nodeID)
		if err != nil {
			return nil, err
		}
		vars[k] = replaced
	}
	for _, variable := range added {
		if _, isList := values[variable].([]any); isList {
			return nil, cerrors.Wrap(wasm.ErrPluginValueNotRepresentable(),
				cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
				fmt.Sprintf("cloudformation-provider: template %q names parameter %q as an Fn::Sub variable, which substitutes one string, and the value bound to it is a list; bind it where a list is accepted, or leave the parameter unbound",
					path, variable))
		}
		vars[variable] = deepCopy(values[variable])
	}
	return []any{text, vars}, nil
}

// subParts splits an Fn::Sub argument into its string and its variable map, which
// is empty for the string form.
func subParts(arg any) (string, map[string]any) {
	switch a := arg.(type) {
	case string:
		return a, map[string]any{}
	case []any:
		if len(a) == 0 {
			return "", map[string]any{}
		}
		text, _ := a[0].(string)
		if len(a) > 1 {
			if vars, ok := a[1].(map[string]any); ok {
				return text, vars
			}
		}
		return text, map[string]any{}
	default:
		return "", map[string]any{}
	}
}

// subVariables returns the variables one Fn::Sub string names, each once, in the
// order they first appear, as written between ${ and }. A variable written ${!…} is
// literal text and is not returned.
func subVariables(text string) []string {
	var vars []string
	seen := map[string]bool{}
	for rest := text; ; {
		open := strings.Index(rest, "${")
		if open < 0 {
			return vars
		}
		rest = rest[open+2:]
		end := strings.IndexByte(rest, '}')
		if end < 0 {
			return vars
		}
		variable := rest[:end]
		rest = rest[end+1:]
		if strings.HasPrefix(variable, "!") || variable == "" || seen[variable] {
			continue
		}
		seen[variable] = true
		vars = append(vars, variable)
	}
}

// classifyBound reads what one bound value is to CloudFormation.
func classifyBound(v any) boundValue {
	switch value := v.(type) {
	case map[string]any:
		if isIntrinsicObject(value) {
			return boundValue{kind: boundIntrinsic, value: value}
		}
	case string:
		switch {
		case dynamicReference.MatchString(value):
			return boundValue{kind: boundDynamicReference, value: value}
		case strings.HasPrefix(value, rendercore.CrossBoundaryLinkSentinelPrefix):
			return boundValue{kind: boundLink, value: value}
		}
		if intrinsic, ok := shortFormIntrinsic(value); ok {
			return boundValue{kind: boundIntrinsic, value: intrinsic}
		}
	}
	return boundValue{kind: boundLiteral, value: v}
}

// shortFormIntrinsic reads a string that is wholly one short-form intrinsic — !Ref,
// !GetAtt, !Sub and the other forms CloudFormation defines — as the long-form
// intrinsic the template's normalization would read it as, through the same YAML
// reading a fragment's short forms take.
func shortFormIntrinsic(s string) (map[string]any, bool) {
	if !strings.HasPrefix(s, "!") || strings.HasPrefix(s, "!!") || strings.ContainsAny(s, "\r\n") {
		return nil, false
	}
	tag, _, _ := strings.Cut(s[1:], " ")
	if !shortFormIntrinsicTags[tag] {
		return nil, false
	}
	v, err := decodeToValue([]byte("value: " + s))
	if err != nil {
		return nil, false
	}
	doc, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	intrinsic, ok := doc["value"].(map[string]any)
	if !ok || !isIntrinsicObject(intrinsic) {
		return nil, false
	}
	plain, err := plainValue(intrinsic)
	if err != nil {
		return nil, false
	}
	out, ok := plain.(map[string]any)
	return out, ok
}

// isIntrinsicObject reports whether a mapping is one intrinsic function: a single
// key that is Ref, Condition or an Fn:: function.
func isIntrinsicObject(m map[string]any) bool {
	if len(m) != 1 {
		return false
	}
	for k := range m {
		return k == intrinsicRef || k == "Condition" || strings.HasPrefix(k, "Fn::")
	}
	return false
}

// plainValue rewrites a value into the tree a normalized template is read as —
// maps of string keys, lists, strings, booleans and numbers kept as the text they
// were written as — so a bound value lands in a template in the same form the
// template's own values take.
func plainValue(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// scalarText returns the text CloudFormation reads one scalar as: a string as it
// stands, a number as written, a boolean as true or false. A list, a mapping or no
// value at all is not one scalar.
func scalarText(v any) (string, bool) {
	switch value := v.(type) {
	case string:
		return value, true
	case json.Number:
		return value.String(), true
	case bool:
		return strconv.FormatBool(value), true
	case int64:
		return strconv.FormatInt(value, 10), true
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64), true
	default:
		return "", false
	}
}

// listMembers returns the members of a value bound to a list-typed parameter: a
// list's items, or a comma-separated string's members with surrounding spaces
// trimmed, as CloudFormation splits a CommaDelimitedList passed at deploy.
func listMembers(v any) ([]string, bool) {
	switch value := v.(type) {
	case []any:
		members := make([]string, 0, len(value))
		for _, item := range value {
			text, ok := scalarText(item)
			if !ok {
				return nil, false
			}
			members = append(members, text)
		}
		return members, true
	default:
		text, ok := scalarText(v)
		if !ok {
			return nil, false
		}
		parts := strings.Split(text, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return parts, true
	}
}

// numberAttribute reads a numeric constraint, which a template writes as a number or
// as a string holding one.
func numberAttribute(v any) (float64, bool) {
	text, ok := scalarText(v)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(text, 64)
	return f, err == nil
}

// truthy reports whether an attribute a template writes as a boolean is set: the
// boolean true, or the string true in any case.
func truthy(v any) bool {
	switch value := v.(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(value, "true")
	default:
		return false
	}
}

// typeText spells a parameter's declared type for a message, naming its absence.
func typeText(typ string) string {
	if typ == "" {
		return "no declared Type"
	}
	return typ
}

// deepCopy copies a value tree so a value substituted at several sites is not
// shared between them.
func deepCopy(v any) any {
	switch value := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for k, child := range value {
			out[k] = deepCopy(child)
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = deepCopy(item)
		}
		return out
	default:
		return v
	}
}

// walkValues visits every mapping inside a value, the value itself included, each
// mapping's children in the lexical order of their keys. The order is part of the
// contract: a caller that stops at the first mapping it matches names that mapping
// in a halt, so the same template must always yield the same first match.
func walkValues(v any, visit func(map[string]any)) {
	switch node := v.(type) {
	case map[string]any:
		visit(node)
		for _, k := range sortedKeys(node) {
			walkValues(node[k], visit)
		}
	case []any:
		for _, item := range node {
			walkValues(item, visit)
		}
	}
}

// walkKeyed visits every key of every mapping inside a value, with the value it
// holds, in no particular order: its one caller asks only whether some key matches,
// which no order changes. A caller whose result depends on which key is met first
// needs the lexical order walkValues keeps.
func walkKeyed(v any, visit func(key string, value any)) {
	switch node := v.(type) {
	case map[string]any:
		for k, child := range node {
			visit(k, child)
			walkKeyed(child, visit)
		}
	case []any:
		for _, item := range node {
			walkKeyed(item, visit)
		}
	}
}

// quotedList spells names as a quoted, comma-separated list.
func quotedList(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, strconv.Quote(n))
	}
	return joinPhrases(quoted)
}

// pluralKeys names one key or several.
func pluralKeys(keys []string) string {
	if len(keys) == 1 {
		return "the key " + strconv.Quote(keys[0])
	}
	return "the keys " + quotedList(keys)
}

// pluralParameters names one parameter or several, as the subject of a sentence.
func pluralParameters(names []string) string {
	if len(names) == 1 {
		return "binding parameter " + strconv.Quote(names[0])
	}
	return "binding parameters " + quotedList(names)
}
