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
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
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
// the consumed (target) endpoint's side: for each output key the consuming edge
// injects, it adds an Export to that key's stack Output and records the export
// name — plus the producer stack name — in the published handle the consumer
// resolves against. The Output itself already exists from the provides-to-Outputs
// projection at assembly; the publish adds only the Export block that makes the
// Output importable across stacks. It is addressed by the same node-and-entry
// name the projection gave it ([OutputLogicalID]), which the consumed node's id
// and the output key together determine — so the publish reaches the right
// producer's output even where another node in the same stack exports the same
// key.
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
	keys := injectedOutputKeys(edge)
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
		return rendercore.LinkPublishOutput{}, cerrors.Wrap(wasm.ErrPluginOutputNameCollision(),
			cerrors.TranslationError, cerrors.SourceLocation{}, strings.Join(refusals, "; and "))
	}
	out := rendercore.LinkPublishOutput{Handle: handle}
	if len(ops) > 0 {
		out.Patches = []rendercore.LinkPatch{{File: StackTemplateName, Ops: ops}}
	}
	return out, nil
}

// LinkResolve produces the consumer-side wiring for one cross-boundary link on
// the consuming (source) endpoint's side: for each output key the edge injects,
// it locates the deterministic placeholder the host stamped into the consuming
// stack and replaces it with a CloudFormation cross-stack reference. Both ends'
// placement annotations select the reference shape: only when both ends declare
// a placement (account or region) and those placements differ is the value read
// with Fn::GetStackOutput against the producer stack, output, region, and
// assume-role — the producer (target) side supplies the region and assume-role,
// and the output is named as the producer stack named it ([OutputLogicalID]);
// otherwise the value is imported with Fn::ImportValue against the published
// export name. A placement declared on only one end, or matching placements on
// both, resolves to the same-placement import. Every reference field is a
// deterministic pointer string the compiler pipes and the runtime resolves.
//
// A placeholder that survives only inside a larger string halts loudly: a
// cross-reference is an intrinsic object and can replace only a whole scalar. A
// placeholder absent from the consumer stack contributes no operation — the
// consumer did not render that injected value, so there is nothing to wire.
func LinkResolve(edge rendercore.LinkEdge, handle map[string]string, consumerFiles []rendercore.RenderedFile) (rendercore.LinkResolveOutput, error) {
	stack, ok := findStackFile(consumerFiles)
	if !ok {
		return rendercore.LinkResolveOutput{}, cerrors.Wrap(wasm.ErrPluginRequiredInputUnresolved(),
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
	for _, outputKey := range injectedOutputKeys(edge) {
		sentinel := rendercore.CrossBoundaryLinkSentinel(edge.Source.Node, edge.Target.Node, edge.Socket, outputKey)
		paths, interpolated := findSentinelPaths(doc, sentinel)
		if interpolated {
			return rendercore.LinkResolveOutput{}, cerrors.Wrap(wasm.ErrPluginValueNotRepresentable(),
				cerrors.TranslationError, cerrors.SourceLocation{},
				fmt.Sprintf("cloudformation-provider: cross-boundary link value is interpolated into a larger string; only a whole-scalar injection can carry a cross-stack reference: output key %q on socket %q", outputKey, edge.Socket))
		}
		if len(paths) == 0 {
			// The consumer did not render this injected value; nothing to wire.
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
	out := rendercore.LinkResolveOutput{}
	if len(ops) > 0 {
		out.Patches = []rendercore.LinkPatch{{File: StackTemplateName, Ops: ops}}
	}
	return out, nil
}

// injectedOutputKeys returns the sorted, de-duplicated set of consumed output
// keys the edge injects across its env, file, and arg mechanisms — the keys the
// producer must export and the consumer must resolve. Sorting makes the publish
// operations and handle entries byte-identical across runs of identical input.
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

// findSentinelPaths walks a decoded JSON document for the placeholder token and
// returns the RFC 6901 pointer to every whole-scalar occurrence, in walk order (map
// keys walked in sorted order, list items in their own, so the result is
// deterministic), and whether the token appears inside a larger string anywhere. A
// whole-scalar occurrence is a replaceable target, and every one is replaced: a
// value a sourced template's parameter was bound to lands wherever the template
// referenced the parameter, which can be several places. An interpolated
// occurrence cannot carry an intrinsic, and the caller halts on it.
func findSentinelPaths(doc any, sentinel string) (paths []string, interpolated bool) {
	var walk func(node any, ptr string)
	walk = func(node any, ptr string) {
		switch v := node.(type) {
		case string:
			if v == sentinel {
				paths = append(paths, ptr)
				return
			}
			if strings.Contains(v, sentinel) {
				interpolated = true
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
