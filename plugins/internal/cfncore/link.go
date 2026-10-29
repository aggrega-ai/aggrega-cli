// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// Handle field names the producer's link_publish exposes for the consumer's
// link_resolve. stackName carries the producer stack a cross-account read
// targets; an exportName entry per output key carries the cross-stack export a
// same-account read imports. All are deterministic pointer strings the compiler
// pipes and never resolves against AWS state.
const (
	handleStackNameKey     = "stackName"
	handleExportNamePrefix = "exportName."
)

// LinkPublish produces the producer-side wiring for one cross-boundary link on
// the consumed (target) endpoint's side: for each key the link carries across as
// a cross-stack reference ([linkedKeys] — every output key the consuming edge
// injects, and every key the consuming templates read off the edge whose value is
// a CloudFormation intrinsic), it adds an Export to that key's stack Output and
// records the export name — plus the producer stack name — in the published
// handle the consumer resolves against. A key read off the edge whose value is
// plain text gets no Export: the resolve writes its value back in place. The
// Output itself already exists from the provides-to-Outputs projection at
// assembly; the publish adds only the Export block that makes the Output
// importable across stacks. It is addressed by the same node-and-entry name the
// projection gave it ([OutputLogicalID]), which the consumed node's id and the
// output key together determine — so the publish reaches the right producer's
// output even where another node in the same stack exports the same key. A key
// whose producer's fragment projects no Output for it gets the same Export
// operation, and applying it halts naming the Output it could not find, whether
// the key was injected or read.
//
// The export and stack names carry the consumed endpoint's Release as their
// leading scope segment, so two distinct Releases sharing an account and region —
// each with the same boundary path and component — produce distinct export and
// stack names. Every name is a deterministic pointer string the compiler pipes
// and never resolves against AWS state.
//
// A produced value several cross-boundary edges consume is published once. The
// host applies each publish's wiring to the consumed endpoint's files before it
// publishes the next edge, so a later edge onto a key an earlier edge already
// exported finds that key's own Output carrying an Export named exactly the
// derived name. The publish then returns that name in its handle and emits no
// operation for the key, and every consumer imports the one name. A key no
// earlier edge injected still gets its own Export.
//
// The consumed endpoint's assembled files are read to check each derived export
// name against the export names the stack's other Outputs entries already
// declare, and a match halts: one name, two declarations, only one of which
// appears in any file the author wrote. The key's own Output is checked too: an
// Export already there under any name other than the derived one — a different
// string, a name given as an intrinsic, an Export block naming nothing, or an
// Export that is not a block at all — halts, because publishing the link's export would overwrite what the author
// wrote there, and keeping the author's would leave the consumers importing a
// name the link did not publish. The checks are scoped
// to this build: what a name meets in an account and region it is deployed into
// is a property of the deployment, which nothing here can observe. Files
// carrying no stack template yield no comparison, which is the answer whenever
// the host has no assembled producer output to hand the publish.
func LinkPublish(edge rendercore.LinkEdge, producerFiles []rendercore.RenderedFile) (rendercore.LinkPublishOutput, error) {
	linked, err := linkedKeys(edge)
	if err != nil {
		return rendercore.LinkPublishOutput{}, err
	}
	keys := linked.referenced
	var authored []authoredExport
	if stack, ok := findStackFile(producerFiles); ok {
		authored = authoredExportNames(stack)
	}
	handle := map[string]string{
		handleStackNameKey: StackName(edge.Target.Release, edge.Target.Boundary),
	}
	ops := make([]frp.JSONPatchOp, 0, len(keys))
	var shadowed, overwritten []string
	for _, outputKey := range keys {
		exportName := ExportName(edge.Target.Release, edge.Target.Boundary, edge.Target.Node, outputKey)
		ownOutputName := OutputLogicalID(edge.Target.Node, outputKey)
		alreadyExported := false
		for _, a := range authored {
			if a.outputName == ownOutputName {
				// The entry this publish writes to is not compared against the
				// derived name as a shadowing declaration: it holds the
				// provides-to-Outputs projection this Export attaches to, and,
				// once an earlier edge sharing the consumed endpoint has been
				// published, that edge's Export. An Export there naming exactly
				// the derived name is the one this publish would write, so it is
				// reused; any other Export there is the author's, and it halts.
				switch {
				case a.named && a.name == exportName:
					alreadyExported = true
				case !a.block:
					overwritten = append(overwritten, ownExportClause(edge, outputKey, exportName,
						"already carries an Export that is not a block, where CloudFormation expects one carrying a Name"))
				case a.named:
					overwritten = append(overwritten, ownExportClause(edge, outputKey, exportName,
						fmt.Sprintf("already declares the export name %q", a.name)))
				case a.nameGiven:
					overwritten = append(overwritten, ownExportClause(edge, outputKey, exportName,
						"already declares an export name that is not a string — one given as an intrinsic, which resolves at deploy time, so this provider cannot read what it spells"))
				default:
					overwritten = append(overwritten, ownExportClause(edge, outputKey, exportName,
						"already carries an Export block declaring no export name"))
				}
				continue
			}
			if !a.named || a.name != exportName {
				continue
			}
			shadowed = append(shadowed, fmt.Sprintf(
				"the link derives the export name %q for provides key %q of component %q in boundary %q of Release %q, and another Outputs entry of the same stack already declares that same export name — that declaration sits under the stack output %q, a name this provider assembles rather than one a Recipe wrote",
				exportName, outputKey, edge.Target.Node, edge.Target.Boundary, edge.Target.Release, a.outputName))
		}
		handle[handleExportNamePrefix+outputKey] = exportName
		if alreadyExported {
			continue
		}
		ops = append(ops, frp.JSONPatchOp{
			Scope: frp.DocumentScopeOwn,
			Key:   StackTemplateName,
			Op:    "add",
			Path:  "/Outputs/" + rfc6901Escape(ownOutputName) + "/Export",
			Value: map[string]any{"Name": exportName},
		})
	}
	var refusals []string
	if len(overwritten) > 0 {
		refusals = append(refusals, fmt.Sprintf("cloudformation-provider: the stack output a cross-boundary link publishes its export on already carries an Export the link did not write: %s; remove that entry's Export block and let the link publish the export every consumer imports",
			strings.Join(overwritten, "; ")))
	}
	if len(shadowed) > 0 {
		refusals = append(refusals, fmt.Sprintf("cloudformation-provider: an export name a cross-boundary link derives is already declared by another Outputs entry in the same stack: %s; each shadowing Outputs entry spells its export name exactly as the halt does, so searching your Recipes for that name reaches the entry — rename the export name the entry declares, or remove that entry's Export block and let the link publish the export",
			strings.Join(shadowed, "; ")))
	}
	if len(refusals) > 0 {
		return rendercore.LinkPublishOutput{}, cerrors.Wrap(plugincondition.ErrPluginOutputNameCollision(),
			cerrors.TranslationError, cerrors.SourceLocation{}, strings.Join(refusals, "; and "))
	}
	out := rendercore.LinkPublishOutput{Handle: handle}
	if len(ops) > 0 {
		out.Patches = []rendercore.LinkPatch{{File: StackTemplateName, Ops: ops}}
	}
	return out, nil
}

// LinkResolve produces the consumer-side wiring for one cross-boundary link on
// the consuming (source) endpoint's side. For each key the link carries across as
// a cross-stack reference ([linkedKeys] — every output key the edge injects, and
// every key the consuming templates read off the edge whose value is a
// CloudFormation intrinsic), it locates the deterministic placeholder the host
// stamped into the consuming stack and replaces it with a CloudFormation
// cross-stack reference. Both ends' placement annotations select the reference
// shape: only when both ends declare a placement (account or region) and those
// placements differ is the value read with Fn::GetStackOutput against the
// producer stack, output, region, and assume-role — the producer (target) side
// supplies the region and assume-role, and the output is named as the producer
// stack named it ([OutputLogicalID]); otherwise the value is imported with
// Fn::ImportValue against the published export name. A placement declared on
// only one end, or matching placements on both, resolves to the same-placement
// import. Every reference field is a deterministic pointer string the compiler
// pipes and the runtime resolves.
//
// A placeholder of such a key that survives only inside a larger string halts
// loudly: a cross-reference is an intrinsic object and can replace only a whole
// scalar. A placeholder absent from the consumer stack contributes no operation —
// the consumer did not render that value, so there is nothing to wire.
//
// For each key the consuming templates read off the edge whose value is plain
// text, and which the edge does not also inject, the value is written back in
// place of its placeholder: a whole-scalar placeholder becomes the value as the
// provides-to-Outputs projection reads it, so a port written as a number lands as
// a number, and a placeholder inside a larger string becomes the text the
// producer wrote. The consumer then carries the producer's value itself, which
// means the same in its stack as in the producer's, and the publish exports
// nothing for such a key. A key both injected and read follows the injected rule
// at every occurrence.
func LinkResolve(edge rendercore.LinkEdge, handle map[string]string, consumerFiles []rendercore.RenderedFile) (rendercore.LinkResolveOutput, error) {
	linked, err := linkedKeys(edge)
	if err != nil {
		return rendercore.LinkResolveOutput{}, err
	}
	stack, ok := findStackFile(consumerFiles)
	if !ok {
		return rendercore.LinkResolveOutput{}, cerrors.Wrap(plugincondition.ErrPluginRequiredInputUnresolved(),
			cerrors.TranslationError, cerrors.SourceLocation{},
			"cloudformation-provider: cross-boundary link resolve received no consumer stack template")
	}
	var doc any
	if err := json.Unmarshal(stack, &doc); err != nil {
		return rendercore.LinkResolveOutput{}, fmt.Errorf("cloudformation-provider: parse consumer stack template: %w", err)
	}

	targetAnn := ReadCloudTargetAnnotationsFromMap(edge.TargetAnnotations)
	sourceAnn := ReadCloudTargetAnnotationsFromMap(edge.SourceAnnotations)
	targetDeclared := targetAnn.AWSAccount != "" || targetAnn.AWSRegion != ""
	sourceDeclared := sourceAnn.AWSAccount != "" || sourceAnn.AWSRegion != ""
	differ := targetAnn.AWSAccount != sourceAnn.AWSAccount || targetAnn.AWSRegion != sourceAnn.AWSRegion
	crossAccountOrRegion := targetDeclared && sourceDeclared && differ

	ops := make([]frp.JSONPatchOp, 0)
	for _, outputKey := range linked.referenced {
		sentinel := rendercore.CrossBoundaryLinkSentinel(edge.Source.Node, edge.Target.Node, edge.Socket, outputKey)
		paths, interpolated := findSentinelPaths(doc, sentinel)
		if len(interpolated) > 0 {
			return rendercore.LinkResolveOutput{}, cerrors.Wrap(plugincondition.ErrPluginValueNotRepresentable(),
				cerrors.TranslationError, cerrors.SourceLocation{},
				fmt.Sprintf("cloudformation-provider: cross-boundary link value is interpolated into a larger string; only a whole-scalar injection can carry a cross-stack reference: output key %q on socket %q", outputKey, edge.Socket))
		}
		if len(paths) == 0 {
			// The consumer did not render this value; nothing to wire.
			continue
		}
		var value any
		if crossAccountOrRegion {
			value = map[string]any{"Fn::GetStackOutput": map[string]any{
				"StackName":  handle[handleStackNameKey],
				"OutputName": OutputLogicalID(edge.Target.Node, outputKey),
				"Region":     targetAnn.AWSRegion,
				"RoleArn":    targetAnn.AWSAssumeRoleARN,
			}}
		} else {
			value = map[string]any{"Fn::ImportValue": handle[handleExportNamePrefix+outputKey]}
		}
		for _, path := range paths {
			ops = append(ops, frp.JSONPatchOp{
				Scope: frp.DocumentScopeOwn,
				Key:   StackTemplateName,
				Op:    "replace",
				Path:  path,
				Value: value,
			})
		}
	}
	// A string carrying several plain placeholders is rewritten once, with every
	// one written back, so no operation replaces a string an earlier one already
	// replaced. Strings are kept in the order they are first met, which the walk
	// order and the read order together fix.
	var rewrittenPaths []string
	rewritten := map[string]string{}
	for _, read := range linked.plain {
		sentinel := rendercore.CrossBoundaryLinkSentinel(edge.Source.Node, edge.Target.Node, edge.Socket, read.key)
		paths, interpolated := findSentinelPaths(doc, sentinel)
		for _, path := range paths {
			ops = append(ops, frp.JSONPatchOp{
				Scope: frp.DocumentScopeOwn,
				Key:   StackTemplateName,
				Op:    "replace",
				Path:  path,
				Value: read.value,
			})
		}
		for _, s := range interpolated {
			current, seen := rewritten[s.path]
			if !seen {
				current = s.text
				rewrittenPaths = append(rewrittenPaths, s.path)
			}
			rewritten[s.path] = strings.ReplaceAll(current, sentinel, read.text)
		}
	}
	for _, path := range rewrittenPaths {
		ops = append(ops, frp.JSONPatchOp{
			Scope: frp.DocumentScopeOwn,
			Key:   StackTemplateName,
			Op:    "replace",
			Path:  path,
			Value: rewritten[path],
		})
	}
	out := rendercore.LinkResolveOutput{}
	if len(ops) > 0 {
		out.Patches = []rendercore.LinkPatch{{File: StackTemplateName, Ops: ops}}
	}
	return out, nil
}

// edgeLinkKeys is how one cross-boundary link wires the consumed endpoint's
// provides keys.
type edgeLinkKeys struct {
	// referenced are the keys carried across as a cross-stack reference: the
	// publish exports each and the resolve replaces each placeholder with a
	// reference to that export. Sorted, so the publish operations and handle
	// entries are byte-identical across runs of identical input.
	referenced []string
	// plain are the keys read off the edge, not injected, whose value is plain
	// text, in the order the edge lists them.
	plain []plainReadKey
}

// plainReadKey is one key a consuming template read off the edge whose value is
// plain text: the text the producer wrote, which is what a placeholder inside a
// larger string is written back as, and that text as the provides-to-Outputs
// projection reads it, which is what a whole-scalar placeholder is written back
// as.
type plainReadKey struct {
	key   string
	text  string
	value any
}

// linkedKeys splits an edge's provides keys into the two ways the link wires
// them. Every key the edge injects is referenced, as it always is. Every key the
// consuming templates read directly off the edge — the edge's read-keys list —
// and which the edge does not also inject is referenced when its value is a
// CloudFormation intrinsic and plain otherwise. A key both injected and read is
// referenced once, by the injected rule.
//
// The intrinsic-or-plain decision is made here, in the provider, rather than by
// the host: the host hands every provides value as the text the producer's
// Recipe wrote and reads none of it, and only CloudFormation's own reading tells a
// value naming a resource in the producer's stack — which the consumer's stack
// cannot resolve — from a value that means the same thing in any stack. The
// reading is the one the provides-to-Outputs projection applies
// ([readProvidesValue]), so a key is referenced exactly when the producer's stack
// Output for it carries an intrinsic.
//
// A read key the edge's provides does not carry halts: it names a value the host
// never delivered, so there is nothing to wire it to.
func linkedKeys(edge rendercore.LinkEdge) (edgeLinkKeys, error) {
	injected := injectedOutputKeys(edge)
	carried := make(map[string]bool, len(injected)+len(edge.ReadProvidesKeys))
	for _, key := range injected {
		carried[key] = true
	}
	referenced := append([]string(nil), injected...)
	var plain []plainReadKey
	for _, key := range edge.ReadProvidesKeys {
		if carried[key] {
			continue
		}
		carried[key] = true
		text, ok := edge.Provides.Get(key)
		if !ok {
			return edgeLinkKeys{}, cerrors.Wrap(plugincondition.ErrPluginRequiredInputUnresolved(),
				cerrors.TranslationError, cerrors.SourceLocation{},
				fmt.Sprintf("cloudformation-provider: cross-boundary link lists provides key %q as read by component %q off its edge on socket %q, but component %q provides no such key",
					key, edge.Source.Node, edge.Socket, edge.Target.Node))
		}
		value, intrinsic := readProvidesValue(text)
		if intrinsic {
			referenced = append(referenced, key)
			continue
		}
		plain = append(plain, plainReadKey{key: key, text: text, value: value})
	}
	sort.Strings(referenced)
	return edgeLinkKeys{referenced: referenced, plain: plain}, nil
}

// injectedOutputKeys returns the sorted, de-duplicated set of consumed output
// keys the edge injects across its env, file, and arg mechanisms — the keys the
// producer must export and the consumer must resolve whatever their value.
// Sorting makes the publish operations and handle entries byte-identical across
// runs of identical input.
func injectedOutputKeys(edge rendercore.LinkEdge) []string {
	seen := map[string]struct{}{}
	for _, mech := range []string{"env", "file", "arg"} {
		keymap, ok := edge.Inject.Get(mech)
		if !ok {
			continue
		}
		for _, outputKey := range keymap.All() {
			seen[outputKey] = struct{}{}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// findStackFile returns the assembled CloudFormation stack template's bytes from
// a consuming endpoint's files, and whether it was present.
func findStackFile(files []rendercore.RenderedFile) ([]byte, bool) {
	for _, f := range files {
		if f.Path == StackTemplateName {
			return f.Content, true
		}
	}
	return nil, false
}

// authoredExport is one Export block an assembled stack's Outputs carry, paired
// with the name of the stack Output carrying it.
//
// Only the export name is authored. The output name is this core's own, built at
// assembly from the component and the entry an author wrote ([OutputLogicalID]),
// which encodes every character the logical-id alphabet refuses rather than
// dropping it — so entries as different as db_endpoint, db-endpoint and
// dbEndpoint reach three distinct output names. Distinct is not the same as
// authored: the encoded name is nothing the author typed or would recognise,
// which is why a diagnostic built from this pair leans on the export name and
// never presents the output name as something an author wrote.
//
// block reports that the Export is a mapping; an Export written as any other
// value is carried with block unset and nothing else. named reports that the
// block's Name is a string, which name then holds. nameGiven reports that a Name
// is present but is not a string — an intrinsic, which resolves at deploy time
// rather than here, so this core cannot know what it spells and does not guess.
// A block with neither set names nothing. Only a named entry can declare a name
// another one shadows; every other shape matters only at the Output a publish
// writes to, where any Export but the one it would write is the author's.
type authoredExport struct {
	outputName string
	name       string
	block      bool
	named      bool
	nameGiven  bool
}

// authoredExportNames returns the Exports an assembled stack's Outputs entries
// carry, ordered by the stack's own Output names so a diagnostic built from them
// reads the same across runs of identical input.
//
// Every reading that carries no Export yields no entry rather than an error: a
// stack with no Outputs section carries none, and an Output with no Export key
// carries none. A template that does not parse likewise yields none — the
// publish needs no document to author its own wiring, and a stack this core
// cannot read is reported where it is read to be written, when the publish's
// wiring is applied to it.
func authoredExportNames(stack []byte) []authoredExport {
	var doc any
	if err := json.Unmarshal(stack, &doc); err != nil {
		return nil
	}
	root, ok := doc.(map[string]any)
	if !ok {
		return nil
	}
	outputs, ok := root["Outputs"].(map[string]any)
	if !ok {
		return nil
	}
	found := make([]authoredExport, 0, len(outputs))
	for _, outputName := range sortedKeys(outputs) {
		body, ok := outputs[outputName].(map[string]any)
		if !ok {
			continue
		}
		rawExport, present := body["Export"]
		if !present {
			continue
		}
		entry := authoredExport{outputName: outputName}
		export, ok := rawExport.(map[string]any)
		if !ok {
			found = append(found, entry)
			continue
		}
		entry.block = true
		if raw, present := export["Name"]; present {
			entry.name, entry.named = raw.(string)
			entry.nameGiven = !entry.named
		}
		found = append(found, entry)
	}
	return found
}

// ownExportClause describes, for the halt, an Export the key's own stack Output
// already carries that the link did not write. The Output is named by the
// component and the provides key it was assembled from, which are strings the
// author wrote, rather than by its assembled name.
func ownExportClause(edge rendercore.LinkEdge, outputKey, exportName, found string) string {
	return fmt.Sprintf(
		"the link derives the export name %q for provides key %q of component %q in boundary %q of Release %q, and the stack output this provider assembles for that key and component %s",
		exportName, outputKey, edge.Target.Node, edge.Target.Boundary, edge.Target.Release, found)
}

// interpolatedString is one string in a decoded document that carries a
// placeholder inside larger text: its RFC 6901 pointer and the string as it
// stands.
type interpolatedString struct {
	path string
	text string
}

// findSentinelPaths walks a decoded JSON document for the placeholder token and
// returns the RFC 6901 pointer to every whole-scalar occurrence, and every string
// carrying the token inside larger text, each in walk order (map keys walked in
// sorted order, list items in their own, so the result is deterministic). A
// whole-scalar occurrence is a replaceable target, and every one is replaced: a
// value a sourced template's parameter was bound to lands wherever the template
// referenced the parameter, which can be several places. An interpolated
// occurrence cannot carry an intrinsic, so the caller halts on one for a key it
// references, and writes plain text into one for a key it writes back.
func findSentinelPaths(doc any, sentinel string) (paths []string, interpolated []interpolatedString) {
	var walk func(node any, ptr string)
	walk = func(node any, ptr string) {
		switch v := node.(type) {
		case string:
			if v == sentinel {
				paths = append(paths, ptr)
				return
			}
			if strings.Contains(v, sentinel) {
				interpolated = append(interpolated, interpolatedString{path: ptr, text: v})
			}
		case map[string]any:
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(v[k], ptr+"/"+rfc6901Escape(k))
			}
		case []any:
			for i, item := range v {
				walk(item, ptr+"/"+strconv.Itoa(i))
			}
		}
	}
	walk(doc, "")
	return paths, interpolated
}

// rfc6901Escape escapes a JSON Pointer reference token per RFC 6901: '~' becomes
// "~0" and '/' becomes "~1", so a key carrying either character addresses the
// intended location.
func rfc6901Escape(token string) string {
	token = strings.ReplaceAll(token, "~", "~0")
	token = strings.ReplaceAll(token, "/", "~1")
	return token
}
