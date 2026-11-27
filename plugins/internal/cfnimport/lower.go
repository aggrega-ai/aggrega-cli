// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cfncore/cfntemplate"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// target is the target the cloudformation format's own Recipes deploy on,
// which evaluates the format's deploy-time text.
const target = "cloudformation"

// Read reads the CloudFormation templates and the parameters file a request
// carries into the core's graph, as read reads them; reads each nested stack
// and serverless application deploying a template it reads as that
// template's resources ([reader.flatten]); leaves out what the serverless
// transform writes for its serverless resources, reading each event and
// connector as an edge ([reader.serverless]); lowers each value of its
// nodes, their conditions and its outputs into the core's values, as
// [reader.lower] says; and reads a resource that is part of another as the
// cfnimport rules say ([reader.relate]). It is the cloudformation format's
// reader.
func Read(in aggrega.ImportInput, s cloudimport.Settings, parse cloudimport.ParseFunc) (*cloudimport.Graph,
	[]aggrega.Diagnostic) {
	r := &reader{in: in, parse: parse}
	g := r.read(s)
	if g == nil {
		return nil, r.diags
	}
	r.flatten(g)
	r.lower(g)
	r.relate(g)
	return g, r.diags
}

// lower lowers each value r read into g, held as the long form the template
// reader writes, into the core's values, as CloudFormation reads each when
// the stack deploys, each in the template it is written in:
//
//   - Ref to a template parameter is that Param — but for a parameter of a
//     Systems Manager parameter type naming one value and given a literal
//     value, which is the dynamic reference resolving that Systems Manager
//     parameter —; a parameter of a nested template reads what the resource
//     deploying it binds it to, as [reader.resolve] says; Ref to a pseudo
//     parameter is deploy-time text; Ref to AWS::NoValue leaves the member or
//     the item holding it absent; Ref to any other name is a Ref to the
//     identifier of the node that resource is;
//   - Fn::GetAtt, as a list or as one string, is a Ref to the attribute, its
//     path the attribute's name split at each dot; an output of a nested
//     template is the value it holds ([lowerer.reference]);
//   - Fn::Sub, in its string or its list form, and Fn::Join with a literal
//     delimiter and a literal list are composed text where each part is
//     literal text, a parameter or a reference: a variable the list form
//     binds is read before one the template declares, ${X.A} is a reference
//     to the attribute, and ${!X} is the literal text ${X}. One that is wholly
//     a Secrets Manager dynamic reference whose secret is a resource of the
//     template is the credential of that resource it reads. Any other is
//     deploy-time text;
//   - Fn::FindInMap with literal keys is the value the mapping holds there;
//   - Fn::If on a condition comparing one template parameter with a literal
//     is the branch the parameter's value selects, the other named in a
//     value-not-carried warning;
//   - Fn::GetStackOutput, which takes a mapping, is not carried;
//   - every other function — Fn::ImportValue, Fn::Select, Fn::Split,
//     Fn::GetAZs, Fn::Base64, Fn::Cidr, and those above where they read
//     anything else — is deploy-time text, as [lowerer.opaque] writes it;
//   - text that is wholly a dynamic reference is the secret it reads where it
//     reads Secrets Manager or a SecureString Systems Manager parameter
//     ([cloudimport.Stored]), and deploy-time text where it reads a plain
//     Systems Manager parameter; text that is wholly a short form, which
//     CloudFormation reads as text and a catalog Recipe placing it would read
//     as the function, is not carried; any other literal is itself.
//
// A value not carried is reported value-not-carried, naming where the
// template writes it, and its member is kept with no value. A parameter's
// default or supplied value that is wholly a short form is not carried
// either, and a resource's condition is lowered as [lowerer.condition] says,
// a resource of a nested template taking that of each resource deploying it
// beside its own ([reader.inherit]). A nested function is reported on, where
// it is, only once the value holding it is lowered to more than deploy-time
// text. What the serverless transform writes for the graph's serverless
// resources is left out first, each event and connector read as an edge
// ([reader.serverless]), so no value the draft leaves out is lowered. g is
// the graph [reader.flatten] flattened.
func (r *reader) lower(g *cloudimport.Graph) {
	var notes []aggrega.Diagnostic
	r.lowering = &notes
	r.scope(r.stacks[0]).parameterValues()
	r.serverless(g)
	for _, n := range g.Nodes {
		h := r.home[n]
		r.scope(h.s).condition(n, h.id)
		r.inherit(h)
	}
	for _, n := range g.Nodes {
		h := r.home[n]
		n.Body = r.scope(h.s).props(n.Body, jsonPointer("Resources", h.id), true)
	}
	root := r.scope(r.stacks[0])
	for i := range g.Outputs {
		g.Outputs[i].Value = root.output(g.Outputs[i])
	}
	for _, d := range notes {
		r.note(d)
	}
}

// lowerer lowers the values of one template.
type lowerer struct {
	r *reader
	t *template
	// s is the stack whose template t is.
	s *stack
	// params are the graph's parameters: the root template's, filled from the
	// parameters file, and those the graph takes for parameters of nested
	// templates.
	params []cloudimport.Parameter
	// notes collects what lowering reports.
	notes *[]aggrega.Diagnostic
	// spent counts the parts the lowering of one value composes, shared by
	// every lowerer of it.
	spent *cloudimport.Budget
}

// warn records a diagnostic of code on the value at the JSON pointer at of
// the lowerer's template.
func (l *lowerer) warn(code cloudimport.Code, at, message string) {
	d := cloudimport.Report(code, message)
	d.SourceRef = l.t.file + "#" + at
	*l.notes = append(*l.notes, d)
}

// trial returns a lowerer whose reports are held apart from l's, and keep,
// which hands them to l: a value lowered as a part of composed text reports
// only where the text is composed.
func (l *lowerer) trial() (*lowerer, func()) {
	var held []aggrega.Diagnostic
	t := *l
	t.notes = &held
	return &t, func() { *l.notes = append(*l.notes, held...) }
}

// param returns the graph's parameter a Ref to the template parameter name
// reads ([lowerer.binding]), nil where the template declares none or the
// parameter reads a value.
func (l *lowerer) param(name string) *cloudimport.Parameter {
	return l.binding(name).param
}

// graphParam returns the graph's parameter named name, nil where the graph
// holds none.
func (l *lowerer) graphParam(name string) *cloudimport.Parameter {
	for i := range l.params {
		if l.params[i].Name == name {
			return &l.params[i]
		}
	}
	return nil
}

// isResource reports whether the template declares a resource named id.
func (l *lowerer) isResource(id string) bool {
	return l.t.resources.get(id) != nil
}

// isPseudo reports whether name is a pseudo parameter, AWS:: and its name; a
// template's own parameter and resource are named in letters and digits.
func isPseudo(name string) bool {
	return strings.HasPrefix(name, "AWS::")
}

// given returns the text of the literal value the parameter p is given — the
// value the parameters file supplies, else its default; a number or a boolean
// as written, and a list parameter's members joined by commas, the text
// CloudFormation reads them from — false where it is given none.
func given(p *cloudimport.Parameter) (string, bool) {
	lit, ok := p.Given()
	if !ok {
		return "", false
	}
	if items, isList := itemsOf(decoded(lit.JSON)); isList {
		members := make([]string, len(items))
		for i, item := range items {
			text, isText := schemadraft.JSONText(item.raw)
			if !isText {
				return "", false
			}
			members[i] = text
		}
		return strings.Join(members, ","), true
	}
	return draftyaml.ScalarText(lit.JSON)
}

// isList reports whether a parameter of type typ is a list: CloudFormation
// reads its value as the comma-separated members of its text.
func isList(typ string) bool {
	return typ == "CommaDelimitedList" || strings.HasPrefix(typ, "List<")
}

// ssmValuePrefix opens the type of a parameter whose value is that of the
// Systems Manager parameter its own value names.
const ssmValuePrefix = "AWS::SSM::Parameter::Value<"

// isSSMValue reports whether a parameter of type typ reads its value from the
// Systems Manager parameter its own value names.
func isSSMValue(typ string) bool {
	return strings.HasPrefix(typ, ssmValuePrefix)
}

// parameterValues leaves out each parameter's default, and each value the
// parameters file supplies, that is wholly a short form: CloudFormation reads
// it as text, and a catalog Recipe placing it would read it as the function.
// Each is reported value-not-carried.
func (l *lowerer) parameterValues() {
	for i := range l.params {
		p := &l.params[i]
		if shortFormText(p.Default) {
			p.Default = nil
			l.warn(cloudimport.CodeValueNotCarried, jsonPointer("Parameters", p.Name, "Default"), "the template "+
				l.t.file+" gives the parameter "+p.Name+" a default that is wholly a CloudFormation short form, which "+
				"CloudFormation reads as text and a catalog Recipe placing it would read as an intrinsic function, so "+
				"the default is not carried: write the value by hand where the stack deploys")
		}
		if shortFormText(p.Supplied) && l.r.sel.params != nil {
			p.Supplied = nil
			file := l.r.sel.params.name()
			d := cloudimport.Report(cloudimport.CodeValueNotCarried, "the parameters file "+file+" gives the "+
				"parameter "+p.Name+" a value that is wholly a CloudFormation short form, which CloudFormation reads as "+
				"text and a catalog Recipe placing it would read as an intrinsic function, so the value is not "+
				"carried: write the value by hand where the stack deploys")
			d.SourceRef = file
			*l.notes = append(*l.notes, d)
		}
	}
}

// shortFormText reports whether v is literal text that is wholly a short form.
func shortFormText(v cloudimport.Value) bool {
	lit, ok := v.(cloudimport.Literal)
	if !ok {
		return false
	}
	s, isText := schemadraft.JSONText(lit.JSON)
	return isText && cfntemplate.LooksLikeShortForm(s)
}

// props returns the properties in lowered, each written at the JSON pointer
// at and its member's name, or its index where named is false: a leaf the
// reader holds as long-form JSON lowered as [lowerer.prop] lowers it, an
// object's members and an array's items each lowered, and a member or an item
// lowered to AWS::NoValue left out. An object or an array left with none is
// its empty literal.
func (l *lowerer) props(in []cloudimport.Prop, at string, named bool) []cloudimport.Prop {
	var out []cloudimport.Prop
	for i, p := range in {
		seg := p.Name
		if !named {
			seg = strconv.Itoa(i)
		}
		here := at + jsonPointer(seg)
		var lowered cloudimport.Prop
		present := true
		switch lit, isLiteral := p.Value.(cloudimport.Literal); {
		case isLiteral:
			lowered, present = l.prop(decoded(lit.JSON), here)
		case p.Value != nil:
			lowered = p
		case len(p.Props) > 0:
			lowered.Props = l.props(p.Props, here, true)
			if len(lowered.Props) == 0 {
				lowered.Value = literal(json.RawMessage("{}"))
			}
		case len(p.Items) > 0:
			lowered.Items = l.props(p.Items, here, false)
			if len(lowered.Items) == 0 {
				lowered.Value = literal(json.RawMessage("[]"))
			}
		}
		if !present {
			continue
		}
		lowered.Name = p.Name
		out = append(out, lowered)
	}
	return out
}

// prop lowers the JSON value v, written at the JSON pointer at, into a
// property: an intrinsic function — an object whose one member is named Ref,
// Condition, or Fn:: and a function's name — as [lowerer.call] lowers it, any
// other object into its members and an array into its items, each lowered, an
// empty object or array, or one left with no member or item, as its empty
// literal, text as [lowerer.text] lowers it, and any other value as its
// literal. It is false where v is AWS::NoValue: the member or the item
// holding it is absent.
func (l *lowerer) prop(v *value, at string) (cloudimport.Prop, bool) {
	switch kind(v) {
	case 0:
		return cloudimport.Prop{}, true
	case '{':
		o, _ := objectOf(v)
		if len(o) == 1 && cfntemplate.IsIntrinsicKey(o[0].Key) {
			return l.call(o[0].Key, o[0].Value, v, at)
		}
		var p cloudimport.Prop
		for _, e := range o {
			m, present := l.prop(e.Value, at+jsonPointer(e.Key))
			if !present {
				continue
			}
			m.Name = e.Key
			p.Props = append(p.Props, m)
		}
		if len(p.Props) == 0 {
			return cloudimport.Prop{Value: literal(json.RawMessage("{}"))}, true
		}
		return p, true
	case '[':
		items, _ := itemsOf(v)
		var p cloudimport.Prop
		for i, item := range items {
			m, present := l.prop(item, at+jsonPointer(strconv.Itoa(i)))
			if present {
				p.Items = append(p.Items, m)
			}
		}
		if len(p.Items) == 0 {
			return cloudimport.Prop{Value: literal(json.RawMessage("[]"))}, true
		}
		return p, true
	case '"':
		s, _ := schemadraft.JSONText(v.raw)
		return cloudimport.Prop{Value: l.text(s, v.raw, at)}, true
	}
	return cloudimport.Prop{Value: literal(v.raw)}, true
}

// text lowers the string s, which raw writes, written at the JSON pointer at:
// a dynamic reference as [lowerer.dynamic] lowers it; text that is wholly a
// short form is not carried, as CloudFormation reads it as text and a catalog
// Recipe placing it would read it as the function; any other is its literal.
func (l *lowerer) text(s string, raw json.RawMessage, at string) cloudimport.Value {
	switch {
	case cfntemplate.IsDynamicReference(s):
		return l.dynamic(s, at)
	case cfntemplate.LooksLikeShortForm(s):
		l.warn(cloudimport.CodeValueNotCarried, at, "the template "+l.t.file+" writes text at "+at+" that is wholly a "+
			"CloudFormation short form, which CloudFormation reads as text and a catalog Recipe placing it would read "+
			"as an intrinsic function, so it is not carried: write the value by hand where the stack deploys")
		return nil
	}
	return literal(raw)
}

// dynamic lowers the dynamic reference s, written at the JSON pointer at: one
// reading Secrets Manager is the secret it names, located by the secret's
// name, or its ARN with its colons kept; one reading a SecureString Systems
// Manager parameter is the secret located by all it writes after
// ssm-secure:, the parameter's name and the version it names; one reading a
// plain Systems Manager parameter is deploy-time text, its text as written.
// The reference is never resolved: CloudFormation resolves it when the stack
// deploys.
func (l *lowerer) dynamic(s, at string) cloudimport.Value {
	body := strings.TrimSuffix(strings.TrimPrefix(s, "{{resolve:"), "}}")
	store, rest, _ := strings.Cut(body, ":")
	switch store {
	case "secretsmanager":
		return cloudimport.Stored{Store: store, Locator: secretID(rest), Text: s}
	case "ssm-secure":
		return cloudimport.Stored{Store: store, Locator: rest, Text: s}
	}
	if !writable(s) {
		l.unwritable("a dynamic reference", at)
		return nil
	}
	return cloudimport.Opaque{Text: s, Target: target}
}

// secretArnFields is the number of colon-separated fields of a Secrets
// Manager secret's ARN: arn, partition, service, region, account, secret and
// the secret's name.
const secretArnFields = 7

// secretID returns the secret a Secrets Manager dynamic reference names, read
// from what the reference writes after secretsmanager: its name, the text
// before the first colon, or its ARN, the ARN's fields before the reference's
// own.
func secretID(rest string) string {
	if !strings.HasPrefix(rest, "arn:") {
		id, _, _ := strings.Cut(rest, ":")
		return id
	}
	fields := strings.Split(rest, ":")
	if len(fields) <= secretArnFields {
		return rest
	}
	return strings.Join(fields[:secretArnFields], ":")
}

// call lowers the intrinsic function fn reading arg, which raw writes whole at
// the JSON pointer at, as [reader.lower] says.
func (l *lowerer) call(fn string, arg, raw *value, at string) (cloudimport.Prop, bool) {
	switch fn {
	case "Ref":
		name, ok := schemadraft.JSONText(arg.raw)
		switch {
		case !ok:
			return cloudimport.Prop{Value: l.opaque(raw, at)}, true
		case name == "AWS::NoValue":
			return cloudimport.Prop{}, false
		case isPseudo(name):
			return cloudimport.Prop{Value: l.opaque(raw, at)}, true
		}
		switch b := l.binding(name); {
		case b.param != nil:
			return cloudimport.Prop{Value: l.paramValue(b.param)}, true
		case b.declared:
			return cloudimport.Prop{Value: listed(b.decl, b.value)}, true
		}
		if v, ok := l.reference(name, nil, at); ok {
			return cloudimport.Prop{Value: v}, true
		}
	case "Fn::GetAtt":
		if v, ok := l.getAtt(arg, at); ok {
			return cloudimport.Prop{Value: v}, true
		}
	case "Fn::Sub":
		return cloudimport.Prop{Value: l.sub(arg, raw, at)}, true
	case "Fn::Join":
		return cloudimport.Prop{Value: l.join(arg, raw, at)}, true
	case "Fn::FindInMap":
		return l.findInMap(arg, raw, at)
	case "Fn::If":
		return l.branch(arg, raw, at)
	case "Fn::GetStackOutput":
		l.warn(cloudimport.CodeValueNotCarried, at, "the template "+l.t.file+" reads another stack's output with "+
			"Fn::GetStackOutput at "+at+", which takes a mapping, and a catalog Recipe placing its one-line text "+
			"writes it as text, so it is not carried: export the output and read it with Fn::ImportValue, or write "+
			"the value by hand")
		return cloudimport.Prop{}, true
	}
	return cloudimport.Prop{Value: l.opaque(raw, at)}, true
}

// paramValue returns what a Ref to the template parameter p reads: the
// dynamic reference resolving the Systems Manager parameter p's literal value
// names, for a parameter of a Systems Manager parameter type naming one
// value, which names p among its Params; p itself for any other parameter, and
// for one given no literal value, which the Release supplies.
func (l *lowerer) paramValue(p *cloudimport.Parameter) cloudimport.Value {
	name, ok := given(p)
	read := strings.TrimSuffix(strings.TrimPrefix(p.Type, ssmValuePrefix), ">")
	if !isSSMValue(p.Type) || isList(read) || p.Secure || !ok {
		return cloudimport.Param{Name: p.Name}
	}
	text := "{{resolve:ssm:" + name + "}}"
	if !cfntemplate.IsDynamicReference(text) || !writable(text) {
		return cloudimport.Param{Name: p.Name}
	}
	return cloudimport.Opaque{Text: text, Target: target, Params: []string{p.Name}}
}

// getAtt returns what Fn::GetAtt reading arg, written at the JSON pointer
// at, reads: the attribute of the resource it names, the attribute's name
// split at each dot, as [lowerer.reference] reads it; false where it names no
// resource and an attribute written as text, or reads deploy-time text.
func (l *lowerer) getAtt(arg *value, at string) (cloudimport.Value, bool) {
	var v any
	if json.Unmarshal(arg.raw, &v) != nil {
		return nil, false
	}
	id, attr, ok := cfntemplate.GetAttParts(v)
	if !ok || attr == "" || isPseudo(id) || l.declares(id) {
		return nil, false
	}
	return l.reference(id, strings.Split(attr, "."), at)
}

// sub lowers Fn::Sub reading arg, which raw writes whole at the JSON pointer
// at: composed text of its literal runs and its variables, or deploy-time text
// where a variable is no literal text, parameter or reference, or past the
// parts the lowering composes ([cloudimport.MaxExpansion]).
func (l *lowerer) sub(arg, raw *value, at string) cloudimport.Value {
	text, vars, ok := subArgs(arg)
	if !ok {
		return l.opaque(raw, at)
	}
	part, keep := l.trial()
	var parts []cloudimport.Value
	for _, piece := range cfntemplate.SubPieces(text) {
		if !piece.Variable {
			parts = append(parts, cloudimport.Text(piece.Text))
			continue
		}
		if !l.spent.Spend(1) {
			return l.opaque(raw, at)
		}
		v, ok := part.variable(piece.Text, vars, at)
		if !ok {
			return l.opaque(raw, at)
		}
		parts = append(parts, v)
	}
	keep()
	return l.composed(parts, at)
}

// subArgs returns the text of Fn::Sub reading arg and the variables its list
// form binds; false where arg is neither text nor a list of text and an
// object.
func subArgs(arg *value) (string, obj, bool) {
	if text, ok := schemadraft.JSONText(arg.raw); ok {
		return text, nil, true
	}
	items, _ := itemsOf(arg)
	if len(items) != 2 {
		return "", nil, false
	}
	text, isText := schemadraft.JSONText(items[0].raw)
	vars, isObject := objectOf(items[1])
	return text, vars, isText && isObject
}

// variable lowers the variable name of an Fn::Sub written at the JSON pointer
// at, the list form binding vars, to a part of composed text: the value vars
// binds it to, else the parameter, the resource or the resource's attribute
// the template names by it; false where that is no literal text, parameter or
// reference, or name is a pseudo parameter.
func (l *lowerer) variable(name string, vars obj, at string) (cloudimport.Value, bool) {
	if v := vars.get(name); v != nil {
		p, present := l.prop(v, at+jsonPointer("Fn::Sub", "1", name))
		if !present {
			return nil, false
		}
		return textPart(p.Value)
	}
	id, attr, dotted := strings.Cut(name, ".")
	if isPseudo(id) {
		return nil, false
	}
	switch b := l.binding(id); {
	case b.declared && dotted:
		return nil, false
	case b.param != nil:
		return textPart(l.paramValue(b.param))
	case b.declared:
		return textPart(b.value)
	}
	var path []string
	if dotted {
		path = strings.Split(attr, ".")
	}
	v, ok := l.reference(id, path, at)
	if !ok {
		return nil, false
	}
	return textPart(v)
}

// join lowers Fn::Join reading arg, which raw writes whole at the JSON
// pointer at: with a literal delimiter and a literal list, composed text of
// its items, an item that is AWS::NoValue left out; deploy-time text where
// the delimiter or the list is computed, an item is no literal text,
// parameter or reference, or past the parts the lowering composes
// ([cloudimport.MaxExpansion]).
func (l *lowerer) join(arg, raw *value, at string) cloudimport.Value {
	args, _ := itemsOf(arg)
	if len(args) != 2 {
		return l.opaque(raw, at)
	}
	delimiter, isText := schemadraft.JSONText(args[0].raw)
	list, isList := itemsOf(args[1])
	if !isText || !isList {
		return l.opaque(raw, at)
	}
	part, keep := l.trial()
	var parts []cloudimport.Value
	for i, item := range list {
		if !l.spent.Spend(1) {
			return l.opaque(raw, at)
		}
		p, present := part.prop(item, at+jsonPointer("Fn::Join", "1", strconv.Itoa(i)))
		if !present {
			continue
		}
		v, ok := textPart(p.Value)
		if !ok {
			return l.opaque(raw, at)
		}
		if len(parts) > 0 {
			parts = append(parts, cloudimport.Text(delimiter))
		}
		parts = append(parts, v)
	}
	keep()
	return l.composed(parts, at)
}

// textPart returns v as a part of composed text: literal text, a number or a
// boolean as its text, a parameter, a reference or composed text; false for
// any other.
func textPart(v cloudimport.Value) (cloudimport.Value, bool) {
	switch v := v.(type) {
	case cloudimport.Literal:
		if s, ok := draftyaml.ScalarText(v.JSON); ok {
			return cloudimport.Text(s), true
		}
	case cloudimport.Param, cloudimport.Ref, cloudimport.Compose:
		return v, true
	}
	return nil, false
}

// composed returns the concatenation of parts, written at the JSON pointer
// at: the credential it reads where it is wholly a Secrets Manager dynamic
// reference to a resource of the template; literal text, lowered as
// [lowerer.text] lowers it, where every part is literal; composed text
// otherwise. Composed text whose opening literal text is a short form's tag,
// alone or with its argument begun, is not carried: CloudFormation reads it
// as text, and a catalog Recipe placing the text it composes would read it as
// the function.
func (l *lowerer) composed(parts []cloudimport.Value, at string) cloudimport.Value {
	v := cloudimport.Concat(parts)
	if c, ok := l.credential(v); ok {
		return c
	}
	if lit, ok := v.(cloudimport.Literal); ok {
		s, _ := schemadraft.JSONText(lit.JSON)
		return l.text(s, lit.JSON, at)
	}
	if c, ok := v.(cloudimport.Compose); ok {
		open, _ := c.Parts[0].(cloudimport.Literal)
		if s, isText := schemadraft.JSONText(open.JSON); isText && cfntemplate.LooksLikeShortForm(s) {
			l.warn(cloudimport.CodeValueNotCarried, at, "the template "+l.t.file+" writes text at "+at+" that opens as "+
				"a CloudFormation short form, which CloudFormation reads as text and a catalog Recipe placing it "+
				"would read as an intrinsic function, so it is not carried: write the value by hand where the stack "+
				"deploys")
			return nil
		}
	}
	return v
}

// secretsManagerOpen opens a dynamic reference reading Secrets Manager.
const secretsManagerOpen = "{{resolve:secretsmanager:"

// credential returns the credential v reads where it is composed text wholly
// a Secrets Manager dynamic reference whose secret is the identifier of a
// resource of the template: What is what the reference reads of the secret
// after its identifier, SecretString where it names nothing more.
func (l *lowerer) credential(v cloudimport.Value) (cloudimport.Credential, bool) {
	c, ok := v.(cloudimport.Compose)
	if !ok || len(c.Parts) != 3 {
		return cloudimport.Credential{}, false
	}
	open, _ := c.Parts[0].(cloudimport.Literal)
	ref, isRef := c.Parts[1].(cloudimport.Ref)
	closing, _ := c.Parts[2].(cloudimport.Literal)
	first, _ := schemadraft.JSONText(open.JSON)
	rest, _ := schemadraft.JSONText(closing.JSON)
	if first != secretsManagerOpen || !isRef || len(ref.Attr) > 0 || !l.r.isNode(ref.Node) ||
		!strings.HasSuffix(rest, "}}") || strings.ContainsAny(strings.TrimSuffix(rest, "}}"), "{}") ||
		!(strings.HasPrefix(rest, ":") || rest == "}}") {
		return cloudimport.Credential{}, false
	}
	what := strings.TrimRight(strings.TrimPrefix(strings.TrimSuffix(rest, "}}"), ":"), ":")
	if what == "" {
		what = "SecretString"
	}
	return cloudimport.Credential{Node: ref.Node, What: what}, true
}

// findInMap lowers Fn::FindInMap reading arg, which raw writes whole at the
// JSON pointer at: with literal keys the mapping holds a value at, that value,
// lowered; deploy-time text otherwise.
func (l *lowerer) findInMap(arg, raw *value, at string) (cloudimport.Prop, bool) {
	args, _ := itemsOf(arg)
	if len(args) == 3 || len(args) == 4 {
		name, n := schemadraft.JSONText(args[0].raw)
		first, f := schemadraft.JSONText(args[1].raw)
		second, s := schemadraft.JSONText(args[2].raw)
		if n && f && s {
			mapping, _ := objectOf(l.t.mappings.get(name))
			top, _ := objectOf(mapping.get(first))
			if v := top.get(second); v != nil {
				return l.prop(v, at)
			}
		}
	}
	return cloudimport.Prop{Value: l.opaque(raw, at)}, true
}

// branch lowers Fn::If reading arg, which raw writes whole at the JSON pointer
// at: on a condition comparing one template parameter with a literal, the
// branch the parameter's value selects, lowered, a value-not-carried warning
// naming the other; deploy-time text otherwise.
func (l *lowerer) branch(arg, raw *value, at string) (cloudimport.Prop, bool) {
	args, _ := itemsOf(arg)
	if len(args) != 3 {
		return cloudimport.Prop{Value: l.opaque(raw, at)}, true
	}
	condition, _ := schemadraft.JSONText(args[0].raw)
	holds, param, ok := l.decides(condition)
	if !ok {
		return cloudimport.Prop{Value: l.opaque(raw, at)}, true
	}
	chosen, other, makes, otherwise := 1, 2, "true", "false"
	if !holds {
		chosen, other, makes, otherwise = 2, 1, "false", "true"
	}
	carried := at + jsonPointer("Fn::If", strconv.Itoa(chosen))
	l.warn(cloudimport.CodeValueNotCarried, at, "the template "+l.t.file+" writes Fn::If on the condition "+
		condition+" at "+at+", which the value the template parameter "+param+" is given makes "+makes+", so the "+
		"draft carries its branch at "+carried+" and not the one at "+at+jsonPointer("Fn::If",
		strconv.Itoa(other))+": where the stack deploys with a value of "+param+" that makes "+condition+" "+
		otherwise+", write that branch by hand")
	return l.prop(args[chosen], carried)
}

// output lowers the value of the output o as a property's value is lowered;
// an object or a list it writes is carried as no one value, and is reported
// value-not-carried.
func (l *lowerer) output(o cloudimport.Output) cloudimport.Value {
	lit, ok := o.Value.(cloudimport.Literal)
	if !ok {
		return o.Value
	}
	at := jsonPointer("Outputs", o.Name, "Value")
	inner, keep := l.trial()
	p, present := inner.prop(decoded(lit.JSON), at)
	switch {
	case !present:
		return nil
	case len(p.Props) == 0 && len(p.Items) == 0:
		keep()
		return p.Value
	}
	l.warn(cloudimport.CodeValueNotCarried, at, "the template "+l.t.file+" writes the output "+o.Name+" at "+at+
		" as an object or a list, which the draft carries as no one value, so it is not carried: write it in the "+
		"draft by hand")
	return nil
}

// opaque returns the intrinsic function raw, written at the JSON pointer at,
// as deploy-time text for the cloudformation target: its one-line short form,
// as shortForm writes it, with each template parameter it reads written in
// place as the literal value the parameter is given — a list parameter's as
// the list of its comma-separated members, a variable of an Fn::Sub as a value
// its list form binds — each named among the text's Params. A drafted stack
// declares none of the template's parameters, mappings and conditions, and
// may declare its resources under other logical IDs or not at all, as the
// classes the core gives them decide, so text reading a parameter given no
// literal value, a NoEcho parameter, a parameter of a Systems Manager
// parameter type, a resource, a mapping or a condition owes it, Owed naming
// each. A pseudo parameter and Fn::ImportValue read what the stack deploys
// beside, and owe nothing. A function with no one-line short form, or holding
// text no one line can hold where a Recipe places it, is not carried.
func (l *lowerer) opaque(raw *value, at string) cloudimport.Value {
	dec := json.NewDecoder(bytes.NewReader(raw.raw))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return nil
	}
	p := &placing{l: l}
	placed := p.walk(v)
	text, ok := shortForm(placed)
	if !ok {
		if fn, has := noShortForm(placed); has {
			l.warn(cloudimport.CodeValueNotCarried, at, "the template "+l.t.file+" writes "+fn+" at "+at+", which "+
				"has no one-line YAML short form a catalog Recipe reads back as the function, so it is not carried: "+
				"write the value by hand")
			return nil
		}
		l.unwritable("an intrinsic function", at)
		return nil
	}
	out := cloudimport.Opaque{Text: text, Target: target, Params: p.params, Reads: p.read}
	const evaluated = "it is text CloudFormation evaluates when the stack deploys, and it reads "
	if len(p.owed) > 0 {
		out.Owed = evaluated + strings.Join(p.owed, ", and ")
	}
	if len(p.beside) > 0 {
		out.OwedBesideReads = evaluated + strings.Join(p.beside, ", and ")
	}
	return out
}

// noShortForm returns the name of the function v is where it has no one-line
// short form; false where v is a function that has one.
func noShortForm(v any) (string, bool) {
	if _, _, ok := shortFormOf(v); ok {
		return "", false
	}
	if m, ok := v.(map[string]any); ok && len(m) == 1 {
		for fn := range m {
			return fn, true
		}
	}
	return "an intrinsic function", true
}

// unwritable reports the value at the JSON pointer at, what it is, not
// carried, as one line cannot hold its text wherever a Recipe places it.
func (l *lowerer) unwritable(what, at string) {
	l.warn(cloudimport.CodeValueNotCarried, at, "the template "+l.t.file+" writes at "+at+" "+what+" holding a "+
		"double quote, a backslash, a line break or another control character, or text that is wholly a short form, "+
		"which its one-line short form cannot hold where a catalog Recipe places it, so it is not carried: write the "+
		"value by hand")
}

// placing is one deploy-time text being written: the template parameters
// written in it as their values, what it reads that the draft can neither
// write in its place nor declare, each in the order the text reads it, and of
// those, the resources it reads by the ID their nodes take (read), which the
// draft may declare under that ID, and everything else (beside).
type placing struct {
	l                          *lowerer
	params, owed, read, beside []string
}

// owe records what the text reads and owes, once.
func (p *placing) owe(what string) {
	if !slices.Contains(p.owed, what) {
		p.owed = append(p.owed, what)
		p.beside = append(p.beside, what)
	}
}

// reading records the resource id the text reads by the ID its node takes,
// which it owes, what, unless the draft declares the resource under that ID.
func (p *placing) reading(id, what string) {
	if !slices.Contains(p.read, id) {
		p.read = append(p.read, id)
	}
	if !slices.Contains(p.owed, what) {
		p.owed = append(p.owed, what)
	}
}

// placed records the parameter name written in place, once.
func (p *placing) placed(name string) {
	if !slices.Contains(p.params, name) {
		p.params = append(p.params, name)
	}
}

// walk returns the long form v with each template parameter it reads written
// in place where it can be, recording what it reads and owes. The members of
// an object are walked in key order.
func (p *placing) walk(v any) any {
	switch v := v.(type) {
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = p.walk(item)
		}
		return out
	case map[string]any:
		keys := slices.Sorted(maps.Keys(v))
		if len(keys) == 1 && cfntemplate.IsIntrinsicKey(keys[0]) {
			return p.call(keys[0], v[keys[0]])
		}
		out := make(map[string]any, len(v))
		for _, k := range keys {
			out[k] = p.walk(v[k])
		}
		return out
	}
	return v
}

// call walks the intrinsic function fn reading arg.
func (p *placing) call(fn string, arg any) any {
	switch fn {
	case "Ref":
		if name, ok := arg.(string); ok {
			return p.ref(name)
		}
	case "Fn::GetAtt":
		if id, _, ok := cfntemplate.GetAttParts(arg); ok {
			p.reads(id)
		}
	case "Fn::Sub":
		return map[string]any{fn: p.sub(arg)}
	case "Fn::FindInMap":
		name := "a mapping the template names by a function"
		if args, ok := arg.([]any); ok && len(args) > 0 {
			if text, ok := args[0].(string); ok {
				name = "the mapping " + text
			}
		}
		p.owe(name + ", which no template of the draft declares")
	case "Fn::If", "Condition":
		condition := arg
		if args, ok := arg.([]any); ok && fn == "Fn::If" && len(args) > 0 {
			condition = args[0]
		}
		name := "a condition the template names by a function"
		if text, ok := condition.(string); ok {
			name = "the condition " + text
		}
		p.owe(name + ", which no template of the draft declares")
	}
	return map[string]any{fn: p.walk(arg)}
}

// ref walks a Ref to name: a template parameter written in place where it
// can be ([placing.bound]), and a pseudo parameter kept, owed where it reads
// what the draft does not deploy ([placing.pseudo]); anything else is read
// and owed.
func (p *placing) ref(name string) any {
	keep := map[string]any{"Ref": name}
	if isPseudo(name) {
		p.pseudo(name)
		return keep
	}
	b := p.l.binding(name)
	if !b.declared {
		p.reads(name)
		return keep
	}
	v, ok := p.bound(name, b)
	if !ok {
		return keep
	}
	if b.param != nil {
		p.placed(b.param.Name)
	}
	return v
}

// bound returns the literal value b, what a Ref to the template parameter
// name reads, written in place, as [placing.value] writes the parameter
// [binding.parameter] reads; false, owing it, where it can write none, which
// is a parameter of a nested template reading a value no one line can hold
// or one the draft does not carry, each with its reason.
func (p *placing) bound(name string, b binding) (any, bool) {
	if q := b.parameter(); q != nil {
		return p.value(q)
	}
	why := "whose value no one-line text can hold in its place"
	switch {
	case b.why != "":
		why = b.why
	case b.value == nil:
		why = "whose value the draft does not carry"
	}
	p.owe("the parameter " + name + " of the " + p.l.s.kind() + " " + p.l.s.at + ", " + why)
	return nil, false
}

// pseudoStack are the pseudo parameters reading the stack a template deploys
// in: in a nested template, the nested stack, which the draft does not
// deploy.
var pseudoStack = []string{"AWS::StackId", "AWS::StackName"}

// pseudo owes the pseudo parameter name where it reads the nested stack the
// template deploys in.
func (p *placing) pseudo(name string) {
	if p.l.s.up == nil || !slices.Contains(pseudoStack, name) {
		return
	}
	p.owe("the pseudo parameter " + name + ", which in the " + p.l.s.kind() + " " + p.l.s.at + " names that stack, " +
		"which the draft reads as the resources of its template and does not deploy")
}

// reads owes the resource id the text reads, or the name id where the
// template declares no resource of it; a resource whose node takes the ID the
// text names it by is one the draft may declare under that ID
// ([placing.reading]).
func (p *placing) reads(id string) {
	if child := p.l.s.nested[id]; child != nil {
		p.owe("the " + child.kind() + " " + id + ", which the draft reads as the resources of its template, and " +
			"declares no stack of")
		return
	}
	if p.l.isResource(id) {
		what := "the resource " + id + ", which the drafted stack may declare under another logical ID, or not at all"
		if node, ok := p.l.s.ids[id]; ok && node == id {
			p.reading(id, what)
			return
		}
		p.owe(what)
		return
	}
	p.owe(id + ", which the template declares no parameter or resource of")
}

// value returns the literal value the template parameter param is given,
// written in place: its text, or for a list parameter the list of its
// comma-separated members, each with the space around it trimmed, as
// CloudFormation reads a list parameter; false, owing it, where it is NoEcho,
// of a Systems Manager parameter type, given no literal value, or holding
// text no one line can hold.
func (p *placing) value(param *cloudimport.Parameter) (any, bool) {
	text, ok := given(param)
	switch {
	case param.Secure:
		p.owe("the NoEcho template parameter " + param.Name + ", whose value is never written")
		return nil, false
	case isSSMValue(param.Type):
		p.owe("the template parameter " + param.Name + ", whose value CloudFormation reads from the Systems " +
			"Manager parameter it names")
		return nil, false
	case !ok:
		p.owe("the template parameter " + param.Name + ", which neither the parameters file nor a literal default " +
			"gives a value")
		return nil, false
	}
	unwritable := "the template parameter " + param.Name + ", whose value holds text a one-line short form " +
		"cannot hold"
	if !isList(param.Type) {
		if !writable(text) {
			p.owe(unwritable)
			return nil, false
		}
		return text, true
	}
	var items []any
	for _, item := range strings.Split(text, ",") {
		item = strings.TrimSpace(item)
		if !writable(item) {
			p.owe(unwritable)
			return nil, false
		}
		items = append(items, item)
	}
	return items, true
}

// sub walks the argument of an Fn::Sub: each variable its text reads that
// names a template parameter, and its list form does not bind, is bound to
// the parameter's value in its list form, written in place; each other is
// read as [placing.ref] reads it, and each value the list form binds is
// walked.
func (p *placing) sub(arg any) any {
	text, isText := arg.(string)
	var vars map[string]any
	if !isText {
		list, ok := arg.([]any)
		if !ok || len(list) != 2 {
			return p.walk(arg)
		}
		text, isText = list[0].(string)
		vars, ok = list[1].(map[string]any)
		if !isText || !ok {
			return p.walk(arg)
		}
	}
	bound := map[string]any{}
	for _, k := range slices.Sorted(maps.Keys(vars)) {
		bound[k] = p.walk(vars[k])
	}
	for _, name := range cfntemplate.SubVariables(text) {
		if _, ok := vars[name]; ok {
			continue
		}
		id, _, dotted := strings.Cut(name, ".")
		b := p.l.binding(id)
		switch {
		case isPseudo(id):
			p.pseudo(id)
		case !b.declared || dotted:
			p.reads(id)
		default:
			v, ok := p.bound(id, b)
			if _, list := v.([]any); !ok || list {
				if list {
					p.owe("the template parameter " + id + ", a list, which Fn::Sub cannot write in text")
				}
				continue
			}
			bound[name] = v
			if b.param != nil {
				p.placed(b.param.Name)
			}
		}
	}
	if len(bound) == 0 && isText && vars == nil {
		return text
	}
	return []any{text, bound}
}
