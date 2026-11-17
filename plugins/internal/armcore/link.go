// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore/armexpr"
)

// The fields of the identity LinkPublish returns and LinkResolve reads:
// deploymentName is the name the producing template is deployed under, and an
// outputName entry per provides key is the output that template exposes the
// key's value under. Each is a deterministic pointer string the compiler pipes
// and never resolves against Azure.
const (
	handleDeploymentName   = "deploymentName"
	handleOutputNamePrefix = "outputName."
)

// deploymentIDFormat is the format() string that builds the resource ID of a
// deployment to a resource group from its subscription, its resource group and
// its name.
const deploymentIDFormat = "/subscriptions/{0}/resourceGroups/{1}/providers/Microsoft.Resources/deployments/{2}"

// LinkPublish produces the producer side of a uses edge between two arm
// boundaries, each deployed as its own template: the outputs the consumer reads,
// and the name the producing template is deployed under.
//
// The consumer reads a provides key through an output when the edge injects it,
// or when the consumer's templates read it off the edge and its text is an ARM
// expression, which names what only the producing template declares (linkKeys).
// For each such key, in sorted order, the output OutputName(edge.Target.Node,
// key) is added at the top level of the producing template: a string output whose
// value is the expression passed through string(), so every value crosses as
// text, or the literal text as written. A template that writes no outputs gains
// the outputs member. An output already there with that body is reused, so many
// consumers read one output; with another body it halts wrapping
// ErrPluginOutputNameCollision. An expression that reads a secret (a list
// function, or a securestring or secureObject parameter of the producing template,
// IsSecretExpression) halts wrapping ErrPluginValueNotRepresentable: another
// deployment's read of it would store it in that deployment's history. A key
// whose provides text is literal text splicing a complete template call
// (refuseSplicedCalls) halts wrapping ErrPluginValueNotRepresentable too: across
// the link that text would arrive as text, never evaluated. The
// template with the outputs added is checked against the limits ARM sets
// (CheckLimits), which halts past 64 outputs.
//
// A producer whose boundary deploys at a scope wider than a resource group halts
// wrapping ErrPluginCannotExpressInput (refuseWiderProducer).
//
// The producing template records DeploymentName(edge.Target.Release,
// edge.Target.Boundary) in its metadata under DeploymentNameMetadataKey, gaining
// the metadata member where it writes none; a different name recorded there
// halts wrapping ErrPluginOutputNameCollision. The returned handle carries the
// deployment name and the output name of each key read through an output.
func LinkPublish(edge rendercore.LinkEdge, producerFiles []rendercore.RenderedFile) (rendercore.LinkPublishOutput, error) {
	if err := refuseWiderProducer(edge); err != nil {
		return rendercore.LinkPublishOutput{}, err
	}
	keys, err := linkKeysOf(edge)
	if err != nil {
		return rendercore.LinkPublishOutput{}, err
	}
	template, err := linkedTemplate(edge, producerFiles, "producer_files")
	if err != nil {
		return rendercore.LinkPublishOutput{}, err
	}
	name, err := DeploymentName(edge.Target.Release, edge.Target.Boundary)
	if err != nil {
		return rendercore.LinkPublishOutput{}, err
	}
	outputs, hasOutputs, err := sectionObject(template, sectionOutputs, edge.Target.Boundary)
	if err != nil {
		return rendercore.LinkPublishOutput{}, err
	}
	metadata, hasMetadata, err := sectionObject(template, sectionMetadata, edge.Target.Boundary)
	if err != nil {
		return rendercore.LinkPublishOutput{}, err
	}
	if err := refuseSplicedCalls(edge, keys, template); err != nil {
		return rendercore.LinkPublishOutput{}, err
	}
	secure := secureParameters(template)

	handle := map[string]string{handleDeploymentName: name}
	added := map[string]any{}
	for _, key := range keys.referenced {
		text, _ := edge.Provides.Get(key)
		value, err := outputValue(edge, key, text, secure)
		if err != nil {
			return rendercore.LinkPublishOutput{}, err
		}
		output := map[string]any{"type": "string", "value": value}
		outputName := OutputName(edge.Target.Node, key)
		handle[handleOutputNamePrefix+key] = outputName
		existing, present := outputs[outputName]
		if !present {
			added[outputName] = output
			continue
		}
		if !reflect.DeepEqual(existing, output) {
			return rendercore.LinkPublishOutput{}, refuse(plugincondition.ErrPluginOutputNameCollision,
				fmt.Sprintf("in the template of boundary %q, the output %q holds a different body than the one a link writes there for provides key %q of component %q; the fragment of %q writes an output %q, which lands under that name, so rename that output, or remove it and let the link write the output every reader of the key uses",
					edge.Target.Boundary, outputName, key, edge.Target.Node, edge.Target.Node, key))
		}
	}
	recorded, recordedName := metadata[DeploymentNameMetadataKey]
	if recordedName && recorded != name {
		return rendercore.LinkPublishOutput{}, refuse(plugincondition.ErrPluginOutputNameCollision,
			fmt.Sprintf("the metadata of the template of boundary %q records %q with another value than the name a link records there, the one the template is deployed under for Release %q; remove that metadata entry from the fragment that writes it",
				edge.Target.Boundary, DeploymentNameMetadataKey, edge.Target.Release))
	}

	patched := clone(template)
	var ops []frp.JSONPatchOp
	if len(added) > 0 {
		merged := clone(outputs)
		for _, outputName := range sortedKeys(added) {
			merged[outputName] = added[outputName]
			if hasOutputs {
				ops = append(ops, addOp("/"+sectionOutputs+"/"+escapePointer(outputName), added[outputName]))
			}
		}
		if !hasOutputs {
			ops = append(ops, addOp("/"+sectionOutputs, added))
		}
		patched[sectionOutputs] = merged
	}
	if !recordedName {
		merged := clone(metadata)
		merged[DeploymentNameMetadataKey] = name
		if hasMetadata {
			ops = append(ops, addOp("/"+sectionMetadata+"/"+escapePointer(DeploymentNameMetadataKey), name))
		} else {
			ops = append(ops, addOp("/"+sectionMetadata, map[string]any{DeploymentNameMetadataKey: name}))
		}
		patched[sectionMetadata] = merged
	}

	out := rendercore.LinkPublishOutput{Handle: handle}
	if len(ops) == 0 {
		return out, nil
	}
	encoded, err := encodeTemplate(patched)
	if err != nil {
		return rendercore.LinkPublishOutput{}, err
	}
	if err := CheckLimits(patched, encoded); err != nil {
		return rendercore.LinkPublishOutput{}, err
	}
	out.Patches = []rendercore.LinkPatch{{File: TemplateFileName, Ops: ops}}
	return out, nil
}

// LinkResolve produces the consumer side of a uses edge between two arm
// boundaries: it replaces each placeholder the host stamped into the consuming
// template for the edge's provides keys.
//
// A key read through an output (linkKeys) is read from the producing template's
// deployment by its resource ID: reference(<ID>, DeploymentsAPIVersion).outputs.
// <output>.value, where the ID is the deployments resource ID built with format()
// from the producer's subscription and resource group (placementOf, over the
// producer's annotations) and the deployment name the handle carries, and the
// output is the one the handle names for the key. A placeholder that is the whole
// string becomes that read as an expression; a placeholder that is a string
// argument of an expression, which is where the assembly writes one held in
// literal text, a format() argument, becomes the read itself, and the expression
// is written again canonically. ARM evaluates reference() only inside a
// resource's properties, an output's value or the input of an output's copy
// loop, so a read anywhere else halts wrapping ErrPluginValueNotRepresentable,
// naming the JSON pointer. So does a read inside literal text that is not an
// expression, such as text that opens with [[, which ARM keeps as written.
//
// The consuming template's top level is read at the scope its $schema names.
// At subscription scope the template's resource group level, the template of
// the nested deployment LevelResourceGroupSymbol, is read too, as a level
// deployed to a resource group: the pointers of its strings run through that
// deployment. ARM evaluates resourceGroup() only in a template deployed to a
// resource group, so a read at subscription scope from a producer whose boundary
// names no resource group halts wrapping ErrPluginRequiredInputUnresolved,
// naming AnnotationResourceGroup. A producer whose boundary deploys at a scope
// wider than a resource group halts as LinkPublish does.
//
// A key read off the edge whose text is literal is written back in place of its
// placeholder: a whole string as ArmLiteral writes the text ARM reads from it, a
// string argument as an ARM string literal of that text, and inside literal text
// as that text. Literal text may sit anywhere in a template.
//
// Each string holding a placeholder is replaced once, with every placeholder of
// the edge in it resolved; strings are visited in key order, so the operations
// are a function of the input alone. A placeholder of another edge is left for
// that edge's resolve, and a key whose placeholder the consumer did not write
// contributes nothing.
func LinkResolve(edge rendercore.LinkEdge, handle map[string]string, consumerFiles []rendercore.RenderedFile) (rendercore.LinkResolveOutput, error) {
	if err := refuseWiderProducer(edge); err != nil {
		return rendercore.LinkResolveOutput{}, err
	}
	keys, err := linkKeysOf(edge)
	if err != nil {
		return rendercore.LinkResolveOutput{}, err
	}
	template, err := linkedTemplate(edge, consumerFiles, "consumer_files")
	if err != nil {
		return rendercore.LinkResolveOutput{}, err
	}
	scope, err := scopeOfSchema(template[sectionSchema])
	if err != nil {
		return rendercore.LinkResolveOutput{}, err
	}
	values, order, err := resolvedValues(edge, handle, keys)
	if err != nil {
		return rendercore.LinkResolveOutput{}, err
	}
	var ops []frp.JSONPatchOp
	visit := func(at Scope) visitFunc {
		return func(s site, text string) (string, error) {
			resolved, err := resolveString(edge, s, text, values, order, at)
			if err != nil {
				return "", err
			}
			if resolved != text {
				ops = append(ops, frp.JSONPatchOp{Scope: frp.DocumentScopeOwn, Key: TemplateFileName, Op: "replace", Path: s.pointer(), Value: resolved})
			}
			return text, nil
		}
	}
	if err := templateLevel(template, "").eachString(visit(scope)); err != nil {
		return rendercore.LinkResolveOutput{}, err
	}
	if inner, prefix, nested := resourceGroupLevelOf(template, scope); nested {
		if err := templateLevel(inner, prefix).eachString(visit(ScopeResourceGroup)); err != nil {
			return rendercore.LinkResolveOutput{}, err
		}
	}
	out := rendercore.LinkResolveOutput{}
	if len(ops) > 0 {
		out.Patches = []rendercore.LinkPatch{{File: TemplateFileName, Ops: ops}}
	}
	return out, nil
}

// linkKeys is how a link wires the producer's provides keys.
type linkKeys struct {
	// referenced are the keys the consumer reads through an output of the
	// producing template, sorted.
	referenced []string
	// literal are the keys the consumer's templates read off the edge whose text
	// is literal, in the order the edge lists them.
	literal []literalKey
}

// literalKey is one provides key whose text is literal, with the text ARM reads
// from it.
type literalKey struct {
	key, value string
}

// linkKeysOf splits the edge's provides keys by how the link wires them. Every
// key the edge injects is read through an output. A key the consumer's templates
// read off the edge, and the edge does not inject, is read through an output when
// its text is an ARM expression, whose names only the producing template
// declares, and is literal otherwise, meaning the same in any template. A key
// the edge carries that the producer does not provide halts wrapping
// ErrPluginRequiredInputUnresolved.
func linkKeysOf(edge rendercore.LinkEdge) (linkKeys, error) {
	var keys linkKeys
	carried := map[string]bool{}
	absent := func(key string) error {
		return refuse(plugincondition.ErrPluginRequiredInputUnresolved,
			fmt.Sprintf("component %q reads provides key %q of component %q over the link on socket %q, and %q provides no such key; add the key to the provides of the Recipe of %q, or stop reading it",
				edge.Source.Node, key, edge.Target.Node, edge.Socket, edge.Target.Node, edge.Target.Node))
	}
	for _, key := range rendercore.LinkInjectedKeys(edge) {
		if _, ok := edge.Provides.Get(key); !ok {
			return linkKeys{}, absent(key)
		}
		carried[key] = true
		keys.referenced = append(keys.referenced, key)
	}
	for _, key := range edge.ReadProvidesKeys {
		if carried[key] {
			continue
		}
		carried[key] = true
		text, ok := edge.Provides.Get(key)
		if !ok {
			return linkKeys{}, absent(key)
		}
		if armexpr.IsExpression(text) {
			keys.referenced = append(keys.referenced, key)
			continue
		}
		keys.literal = append(keys.literal, literalKey{key: key, value: literalText(text)})
	}
	slices.Sort(keys.referenced)
	return keys, nil
}

// literalText returns the text ARM reads from a string that is not an
// expression. A string that opens with a doubled bracket and closes with a
// bracket loses its first bracket, the escape ArmLiteral writes; any other string
// is read as written.
func literalText(s string) string {
	if strings.HasPrefix(s, "[[") && strings.HasSuffix(s, "]") {
		return s[1:]
	}
	return s
}

// refuseSplicedCalls halts wrapping ErrPluginValueNotRepresentable on the first
// key the link carries, read through an output in sorted order and then each
// literal key, whose provides text is literal text holding a complete bracketed
// call (callAt, over the template functions and the functions the producing
// template declares). The assembly writes such text as a format() expression
// within one template, but across a link it would reach the consumer as text,
// never evaluated. Text that opens with [[ is kept literal, as the assembly
// keeps it.
func refuseSplicedCalls(edge rendercore.LinkEdge, keys linkKeys, template map[string]any) error {
	declared, err := readFunctions(template[sectionFunctions])
	if err != nil {
		return err
	}
	functions := functionNames(declared)
	carried := slices.Clone(keys.referenced)
	for _, lit := range keys.literal {
		carried = append(carried, lit.key)
	}
	for _, key := range carried {
		text, _ := edge.Provides.Get(key)
		if armexpr.IsExpression(text) || strings.HasPrefix(text, "[[") {
			continue
		}
		for i := 0; i < len(text); i++ {
			if _, _, ok := callAt(text, i, functions); ok {
				return refuse(plugincondition.ErrPluginValueNotRepresentable,
					fmt.Sprintf("provides key %q of component %q, which the link on socket %q carries to another deployment, splices a template call into literal text, which that deployment would receive unevaluated, as text; write the provides value as one expression, such as [format('postgres://{0}:5432', reference('Db').outputs.fqdn.value)]",
						key, edge.Target.Node, edge.Socket))
			}
		}
	}
	return nil
}

// outputValue returns the value of the output a provides key is read through:
// an expression passed through string(), written canonically, or the literal
// text as written. An expression that does not parse, or that reads a secret,
// halts wrapping ErrPluginValueNotRepresentable.
func outputValue(edge rendercore.LinkEdge, key, text string, secure map[string]bool) (string, error) {
	if !armexpr.IsExpression(text) {
		return text, nil
	}
	n, err := armexpr.Parse(text)
	if err != nil {
		reason, offset := err.Error(), 0
		var se *armexpr.SyntaxError
		if errors.As(err, &se) {
			reason, offset = se.Reason, se.Offset
		}
		return "", refuse(plugincondition.ErrPluginValueNotRepresentable,
			fmt.Sprintf("provides key %q of component %q is read by ARM as an expression but does not parse (offset %d: %s); correct the expression in the provides of its Recipe, or write [[ at its start to keep it literal text",
				key, edge.Target.Node, offset, reason))
	}
	if IsSecretExpression(n, secure) {
		return "", refuse(plugincondition.ErrPluginValueNotRepresentable,
			fmt.Sprintf("provides key %q of component %q reads a secret, and a link would write it as an output of the template of boundary %q: a secret read by another deployment lands in its history; store it in Key Vault and attach core/referenced-secret on both ends",
				key, edge.Target.Node, edge.Target.Boundary))
	}
	return armexpr.Expression(armexpr.Call{Name: "string", Args: []armexpr.Node{n}}), nil
}

// secureParameters returns, for each parameter a template declares, whether its
// type is secure (isSecureType), following a $ref into the template's
// definitions as parameterType does.
func secureParameters(template map[string]any) map[string]bool {
	declared, _ := template[sectionParameters].(map[string]any)
	definitions, _ := template[sectionDefinitions].(map[string]any)
	secure := map[string]bool{}
	for _, name := range sortedKeys(declared) {
		decl, _ := declared[name].(map[string]any)
		typ, _ := parameterType(decl, definitions)
		secure[name] = isSecureType(typ)
	}
	return secure
}

// linkedValue is what one placeholder of a link resolves to: the read of an
// output of the producing template's deployment, or the text of a literal key.
type linkedValue struct {
	key string
	// read is the reference() expression for a key read through an output, nil
	// for a literal key.
	read armexpr.Node
	// literal is the text ARM reads from a literal key's provides text.
	literal string
}

// resolvedValues returns what each placeholder of the link resolves to, keyed
// by the placeholder, and the placeholders in the order the keys are listed: the
// keys read through an output, sorted, then the literal keys. A field the
// handle lacks for a key read through an output halts wrapping
// ErrPluginRequiredInputUnresolved.
func resolvedValues(edge rendercore.LinkEdge, handle map[string]string, keys linkKeys) (map[string]linkedValue, []string, error) {
	values := map[string]linkedValue{}
	var order []string
	missing := func(field string) error {
		return refuse(plugincondition.ErrPluginRequiredInputUnresolved,
			fmt.Sprintf("the identity published for the link from component %q to component %q on socket %q carries no %s; the link is resolved against the identity its publish returns",
				edge.Source.Node, edge.Target.Node, edge.Socket, field))
	}
	at := placementOf(edge.TargetAnnotations)
	for _, key := range keys.referenced {
		deployment, ok := handle[handleDeploymentName]
		if !ok || deployment == "" {
			return nil, nil, missing(handleDeploymentName)
		}
		output, ok := handle[handleOutputNamePrefix+key]
		if !ok || output == "" {
			return nil, nil, missing(handleOutputNamePrefix + key)
		}
		read, err := armexpr.Parse(fmt.Sprintf("[reference(format(%s, %s, %s, %s), %s).outputs.%s.value]",
			StringLiteral(deploymentIDFormat), at.subscription, at.resourceGroup, StringLiteral(deployment),
			StringLiteral(DeploymentsAPIVersion), output))
		if err != nil {
			return nil, nil, refuse(plugincondition.ErrPluginValueNotRepresentable,
				fmt.Sprintf("the identity published for the link from component %q to component %q on socket %q gives under %q an output name ARM cannot read after .outputs; the link is resolved against the identity its publish returns, whose output names are lowercase letters, digits and underscores",
					edge.Source.Node, edge.Target.Node, edge.Socket, handleOutputNamePrefix+key))
		}
		placeholder := rendercore.CrossBoundaryLinkSentinel(edge.Source.Node, edge.Target.Node, edge.Socket, key)
		values[placeholder] = linkedValue{key: key, read: read}
		order = append(order, placeholder)
	}
	for _, lit := range keys.literal {
		placeholder := rendercore.CrossBoundaryLinkSentinel(edge.Source.Node, edge.Target.Node, edge.Socket, lit.key)
		values[placeholder] = linkedValue{key: lit.key, literal: lit.value}
		order = append(order, placeholder)
	}
	return values, order, nil
}

// resolveString returns text with every placeholder of the link in it resolved,
// or text unchanged where it holds none. at is the scope of the level text sits
// in.
func resolveString(edge rendercore.LinkEdge, s site, text string, values map[string]linkedValue, order []string, at Scope) (string, error) {
	if !strings.Contains(text, rendercore.CrossBoundaryLinkSentinelPrefix) {
		return text, nil
	}
	if v, ok := values[text]; ok {
		if v.read == nil {
			return ArmLiteral(v.literal), nil
		}
		if err := readableAt(edge, s, v.key, at); err != nil {
			return "", err
		}
		return armexpr.Expression(v.read), nil
	}
	if armexpr.IsExpression(text) {
		n, err := armexpr.Parse(text)
		if err != nil {
			// checkStrings holds the one sentence for an expression that does not
			// parse; it reads text again and refuses it.
			return "", checkStrings(text, fmt.Sprintf("%s in the template of boundary %q", s.pointer(), edge.Source.Boundary))
		}
		readKey := ""
		resolved, changed := substitute(n, func(str armexpr.String) (armexpr.Node, bool) {
			v, ok := values[str.Value]
			switch {
			case !ok:
				return nil, false
			case v.read == nil:
				return armexpr.String{Value: v.literal}, true
			}
			if readKey == "" {
				readKey = v.key
			}
			return v.read, true
		})
		if !changed {
			return text, nil
		}
		if readKey != "" {
			if err := readableAt(edge, s, readKey, at); err != nil {
				return "", err
			}
		}
		return armexpr.Expression(resolved), nil
	}
	for _, placeholder := range order {
		if !strings.Contains(text, placeholder) {
			continue
		}
		v := values[placeholder]
		if v.read != nil {
			where := "inside literal text the assembly did not write as a format() expression, which ARM keeps as written"
			remedy := "write the value whole as the string, or inside the literal text of a fragment, which the assembly writes as a format() expression"
			if strings.HasPrefix(text, "[[") {
				where = "inside text that opens with [[, which ARM keeps literal"
				remedy = "write the value whole, or inside literal text that does not open with [[, which the assembly writes as a format() expression"
			}
			return "", refuse(plugincondition.ErrPluginValueNotRepresentable,
				fmt.Sprintf("at JSON pointer %s in the template of boundary %q, the value of provides key %q of component %q is read from another deployment, and it is written %s; %s",
					s.pointer(), edge.Source.Boundary, v.key, edge.Target.Node, where, remedy))
		}
		text = strings.ReplaceAll(text, placeholder, v.literal)
	}
	return text, nil
}

// readableAt halts where a value read from another deployment cannot be read at
// site s of a level at scope at: outside the places ARM evaluates reference(),
// and at a scope wider than a resource group when the read names the
// consumer's own resource group.
func readableAt(edge rendercore.LinkEdge, s site, key string, at Scope) error {
	if !s.readsRuntimeState() {
		return readOutsideRuntimeState(edge, s, key)
	}
	if at != ScopeResourceGroup && placementOf(edge.TargetAnnotations).readsOwnResourceGroup() {
		return refuse(plugincondition.ErrPluginRequiredInputUnresolved,
			fmt.Sprintf("at JSON pointer %s in the template of boundary %q, the value of provides key %q of component %q is read from another deployment at %s scope, where the template has no resource group of its own to take as the producer's; annotate the boundary of %q with %s",
				s.pointer(), edge.Source.Boundary, key, edge.Target.Node, scopeWords(at), edge.Target.Node, AnnotationResourceGroup))
	}
	return nil
}

// readOutsideRuntimeState is the halt for a value read from another deployment
// at a site where ARM does not evaluate reference().
func readOutsideRuntimeState(edge rendercore.LinkEdge, s site, key string) error {
	return refuse(plugincondition.ErrPluginValueNotRepresentable,
		fmt.Sprintf("at JSON pointer %s in the template of boundary %q, the value of provides key %q of component %q is read from another deployment with reference(), which ARM evaluates only inside a resource's properties, an output's value or the input of an output's copy loop; move the value into one of those",
			s.pointer(), edge.Source.Boundary, key, edge.Target.Node))
}

// substitute returns n with each string literal replace maps replaced by the node
// it returns, and whether any was replaced. n itself is not changed.
func substitute(n armexpr.Node, replace func(armexpr.String) (armexpr.Node, bool)) (armexpr.Node, bool) {
	switch v := n.(type) {
	case armexpr.String:
		if out, ok := replace(v); ok {
			return out, true
		}
	case armexpr.Call:
		var args []armexpr.Node
		for i, a := range v.Args {
			if out, ok := substitute(a, replace); ok {
				if args == nil {
					args = slices.Clone(v.Args)
				}
				args[i] = out
			}
		}
		if args != nil {
			v.Args = args
			return v, true
		}
	case armexpr.Member:
		if out, ok := substitute(v.Target, replace); ok {
			v.Target = out
			return v, true
		}
	case armexpr.Index:
		target, targetChanged := substitute(v.Target, replace)
		index, indexChanged := substitute(v.Index, replace)
		if targetChanged || indexChanged {
			v.Target, v.Index = target, index
			return v, true
		}
	}
	return n, false
}

// linkedTemplate returns the assembled template among a link endpoint's files.
// Files holding no template halt wrapping ErrPluginRequiredInputUnresolved,
// naming the payload field that was empty; a template that is not one JSON
// object halts wrapping ErrPluginValueNotRepresentable.
func linkedTemplate(edge rendercore.LinkEdge, files []rendercore.RenderedFile, field string) (map[string]any, error) {
	for _, f := range files {
		if f.Path != TemplateFileName {
			continue
		}
		doc, err := readTemplate(f.Content)
		template, ok := doc.(map[string]any)
		if err == nil && ok {
			return template, nil
		}
		fault := "is not one JSON object"
		if err != nil {
			fault = "does not parse as one JSON object"
			var syntax *json.SyntaxError
			if errors.As(err, &syntax) {
				fault += fmt.Sprintf(" (at byte %d)", syntax.Offset)
			}
		}
		return nil, refuse(plugincondition.ErrPluginValueNotRepresentable,
			fmt.Sprintf("the %s handed to the link from component %q to component %q on socket %q in its %s %s, where a link reads the template its boundary assembled; make sure nothing rewrites that file after the boundary is assembled",
				TemplateFileName, edge.Source.Node, edge.Target.Node, edge.Socket, field, fault))
	}
	return nil, refuse(plugincondition.ErrPluginRequiredInputUnresolved,
		fmt.Sprintf("the link from component %q to component %q on socket %q was handed no %s in its %s, so there is no template to wire",
			edge.Source.Node, edge.Target.Node, edge.Socket, TemplateFileName, field))
}

// sectionObject returns a top-level section of the template of boundary and
// whether the template writes it. A section written as anything but an object
// halts wrapping ErrPluginValueNotRepresentable.
func sectionObject(template map[string]any, section, boundary string) (map[string]any, bool, error) {
	v, present := template[section]
	if !present {
		return nil, false, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, true, refuse(plugincondition.ErrPluginValueNotRepresentable,
			fmt.Sprintf("the template of boundary %q writes %q as something other than an object, so a link cannot add its entries to it; write %s as an object of named entries",
				boundary, section, section))
	}
	return m, true, nil
}

// addOp is a JSON patch operation adding value at path in the template file.
func addOp(path string, value any) frp.JSONPatchOp {
	return frp.JSONPatchOp{Scope: frp.DocumentScopeOwn, Key: TemplateFileName, Op: "add", Path: path, Value: value}
}

// templateLevel returns an assembled template as a level, so its strings are
// visited with the site each sits at: each member of a top-level object is an
// entry of that section, and a top-level value that is not an object is one entry
// of its own section. Every entry's pointer is its JSON pointer in the template,
// after prefix, the pointer of the template in the file that holds it.
func templateLevel(template map[string]any, prefix string) *level {
	l := newLevel()
	for _, section := range sortedKeys(template) {
		entries, ok := template[section].(map[string]any)
		if !ok {
			l.put(section, "", &levelEntry{body: template[section], pointer: prefix + "/" + escapePointer(section)})
			continue
		}
		for _, name := range sortedKeys(entries) {
			l.put(section, name, &levelEntry{body: entries[name], pointer: prefix + "/" + escapePointer(section) + "/" + escapePointer(name)})
		}
	}
	return l
}

// resourceGroupLevelOf returns the resource group level of an assembled template
// at subscription scope, the template its nested deployment
// LevelResourceGroupSymbol carries, with that template's JSON pointer, and
// whether the template holds one.
func resourceGroupLevelOf(template map[string]any, scope Scope) (map[string]any, string, bool) {
	if scope != ScopeSubscription {
		return nil, "", false
	}
	resources, _ := template[sectionResources].(map[string]any)
	deployment, _ := resources[LevelResourceGroupSymbol].(map[string]any)
	if !isDeployment(deployment) {
		return nil, "", false
	}
	properties, _ := deployment["properties"].(map[string]any)
	inner, ok := properties["template"].(map[string]any)
	return inner, "/" + sectionResources + "/" + LevelResourceGroupSymbol + "/properties/template", ok
}

// refuseWiderProducer halts wrapping ErrPluginCannotExpressInput when the
// producer's boundary deploys at a scope wider than a resource group, as its
// AnnotationDeploymentScope names: a link reads the producer's deployment by the
// resource ID of a deployment to a resource group. A value that names no scope
// halts as BoundaryScope does.
func refuseWiderProducer(edge rendercore.LinkEdge) error {
	scope, err := scopeAnnotation(edge.TargetAnnotations, edge.Target.Node)
	if err != nil || scope == ScopeResourceGroup {
		return err
	}
	return refuse(plugincondition.ErrPluginCannotExpressInput,
		fmt.Sprintf("arm links to a producer deployed at resource-group scope; this producer's boundary deploys at %s scope, so the link from component %q to component %q on socket %q, whose producer is in boundary %q, cannot be wired; place component %q in a boundary deployed at resource-group scope",
			scopeWords(scope), edge.Source.Node, edge.Target.Node, edge.Socket, edge.Target.Boundary, edge.Target.Node))
}
