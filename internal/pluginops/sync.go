// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pluginops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredshape"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/yamlsplice"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// SyncOptions is what a record alignment decides, independently of who asked for
// it: the Plugin entity file whose machine-owned records are aligned, and the
// module whose imports and self-report determine what they are aligned to.
type SyncOptions struct {
	EntityPath string
	PluginPath string
}

// SyncResult is what a record alignment did: the entity file it read, the
// host-function set derived from the module's import section, the commands read
// from the module's self-report, and whether the file changed.
//
// Changed is what separates a write from an alignment that found the records
// already correct. The write is idempotent, so a caller that runs this twice sees
// the second run report no change and leave the file byte-identical.
//
// Commands carries the described commands rather than a rendered line, so each
// surface renders what it needs from one value: a terminal formats the mirror, a
// protocol serializes the names and flags. A result carrying both a value and a
// rendering of it would be two representations of one fact.
type SyncResult struct {
	EntityPath    string                  `json:"entityPath"`
	HostFunctions []string                `json:"hostFunctions"`
	Commands      []wasm.PluginCLICommand `json:"commands"`
	Changed       bool                    `json:"changed"`
}

// Sync records a plugin's machine-owned records into the entity file from the
// module: the host-function set derived from the binary's import section, and the
// command mirror read from the binary's self-report.
//
// Neither record is authored by hand — both are ground truth in the binary — so
// this reads the module, derives the host-function set in canonical form and the
// command mirror in declared order, and aligns the entity's
// spec.runtime.hostFunctions and spec.commands with them, creating the runtime
// block when the file has none. Both are written via the surgical splice, so only
// those two blocks change and every other byte — comments, blank lines,
// indentation, and the trailing-newline shape — carries through verbatim.
//
// The write is atomic (temp file plus rename) and idempotent: when the file
// already records both the derived host functions and the described commands, the
// file is left byte-identical and the result reports no change.
func Sync(ctx context.Context, opts SyncOptions) (SyncResult, error) {
	const command = "aggrega plugin sync"

	var res SyncResult
	entityPath, pluginPath := opts.EntityPath, opts.PluginPath

	data, err := os.ReadFile(entityPath)
	if err != nil {
		return res, fmt.Errorf("%s: read entity %s: %w", command, entityPath, err)
	}
	wasmBytes, err := os.ReadFile(pluginPath)
	if err != nil {
		return res, fmt.Errorf("%s: read .wasm %s: %w", command, pluginPath, err)
	}

	var entity types.Plugin
	if err := yaml.Unmarshal(data, &entity); err != nil {
		return res, fmt.Errorf("%s: parse entity %s: %w", command, entityPath, authoredshape.DescribeYAMLDecodeErrorIn(err, data))
	}

	derived, err := wasm.DeriveHostFunctions(entityPath, wasmBytes)
	if err != nil {
		return res, fmt.Errorf("%s: %w", command, err)
	}

	// The command mirror is read from the plugin's self-report, which requires
	// loading the guest. Load with the describe-time headroom as a floor, raised to
	// the entity's declared cap when it is larger, so the self-report always has
	// enough memory to instantiate even a large guest. Recording is not the place to
	// enforce the entity's cap — a too-small authored cap surfaces at publish and
	// load, not here — so the cap is raised, never lowered.
	maxPages := wasm.DescribeMaxMemoryPages
	if entity.Spec.Runtime != nil && entity.Spec.Runtime.MaxMemoryPages > maxPages {
		maxPages = entity.Spec.Runtime.MaxMemoryPages
	}
	desc, err := wasm.DescribePluginBytes(ctx, entityPath, wasmBytes, maxPages, entity.Metadata.PluginType)
	if err != nil {
		return res, fmt.Errorf("%s: %s: %w", command, entityPath, err)
	}

	updated, changedHF, err := SpliceHostFunctions(data, derived)
	if err != nil {
		return res, fmt.Errorf("%s: %s: %w", command, entityPath, err)
	}
	updated, changedCmd, err := SpliceCommands(updated, desc.Commands)
	if err != nil {
		return res, fmt.Errorf("%s: %s: %w", command, entityPath, err)
	}

	res = SyncResult{
		EntityPath:    entityPath,
		HostFunctions: derived,
		Commands:      desc.Commands,
	}
	if !changedHF && !changedCmd {
		return res, nil
	}
	if err := writeFileAtomic(entityPath, updated, 0o644); err != nil {
		return res, fmt.Errorf("%s: write %s: %w", command, entityPath, err)
	}
	res.Changed = true
	return res, nil
}

// SpliceHostFunctions records derived into the entity bytes'
// spec.runtime.hostFunctions and returns the result together with whether it
// changed. Only that one block is rewritten; every other byte — comments, blank
// lines, indentation, and the trailing-newline shape — carries through verbatim.
// The runtime block is created when absent, the hostFunctions block is added under
// an existing runtime when absent, or replaced in place when present. An empty
// derived set is written as an explicit empty list so the machine-owned record is
// always present. The write is skipped — and the bytes returned unchanged — when
// the file already records the derived set (compared as a set).
func SpliceHostFunctions(data []byte, derived []string) ([]byte, bool, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, false, fmt.Errorf("parse entity for host-function recording: %w", err)
	}
	specKey, spec := topLevelChild(&root, "spec")
	if spec == nil || spec.Kind != yaml.MappingNode {
		return nil, false, fmt.Errorf("Plugin entity has no spec mapping to record the derived host functions into")
	}

	runtimeKey, runtime := childEntry(spec, "runtime")

	// Skip only when the record is already present and equal as a set; an absent
	// record is still materialized (even for an empty binary) so the machine-owned
	// field is always present and diff-reviewable.
	if runtime != nil {
		if _, hf := childEntry(runtime, "hostFunctions"); hf != nil {
			if CanonicalHostFunctionsEqual(scalarSeqValues(hf), derived) {
				return data, false, nil
			}
		}
	}
	if runtime != nil && !isBlockMappingWithChildren(runtime) {
		return nil, false, fmt.Errorf("spec.runtime is present but is not a block mapping; rewrite it as a block mapping so the derived host functions can be recorded")
	}

	lines := strings.Split(string(data), "\n")

	specIndent := specKey.Column - 1
	step := 2
	childIndent := specIndent + step
	if len(spec.Content) > 0 {
		childIndent = spec.Content[0].Column - 1
		if s := childIndent - specIndent; s > 0 {
			step = s
		}
	}

	// An empty set must round-trip to an explicit "[]" rather than an absent value.
	value := derived
	if len(value) == 0 {
		value = []string{}
	}

	var result []string
	switch {
	case runtime == nil:
		block, err := renderRuntimeWithHostFunctions(value, childIndent, step)
		if err != nil {
			return nil, false, err
		}
		insertAt := yamlsplice.EndOfMappingContent(lines, specKey.Line, spec, childIndent, nil)
		result = spliceInsert(lines, insertAt, block)

	default:
		runtimeChildIndent := childIndent + step
		if len(runtime.Content) > 0 {
			runtimeChildIndent = runtime.Content[0].Column - 1
		}
		block, err := yamlsplice.RenderBlock("hostFunctions", value, runtimeChildIndent, step)
		if err != nil {
			return nil, false, err
		}
		if hfKey, _ := childEntry(runtime, "hostFunctions"); hfKey == nil {
			insertAt := yamlsplice.EndOfMappingContent(lines, runtimeKey.Line, runtime, runtimeChildIndent, nil)
			result = spliceInsert(lines, insertAt, block)
		} else {
			start, end := yamlsplice.BlockRange(lines, hfKey.Line, hfKey.Column-1)
			result = spliceReplace(lines, start, end, block)
		}
	}

	return []byte(strings.Join(result, "\n")), true, nil
}

// stampCommand is the on-disk shape of one spec.commands entry the splice
// renders: the command name and its optional short-help description. It exists so
// the rendered block carries name first and omits an empty description, matching
// the machine-owned record's canonical shape.
type stampCommand struct {
	Name        string             `yaml:"name"`
	Description string             `yaml:"description,omitempty"`
	Flags       []stampCommandFlag `yaml:"flags,omitempty"`
}

// stampCommandFlag is the byte-splice render shape of one command flag in the
// machine-owned command mirror: the flag's long name and the optional fields that
// carry a value, in field-declaration order.
type stampCommandFlag struct {
	Name        string `yaml:"name"`
	Shorthand   string `yaml:"shorthand,omitempty"`
	Description string `yaml:"description,omitempty"`
	Type        string `yaml:"type,omitempty"`
	Default     string `yaml:"default,omitempty"`
}

// stampCommandFlags projects a described command's flags into the byte-splice
// render shape, preserving declared order. A command with no flags yields a nil
// slice, which renders no flags block.
func stampCommandFlags(flags []wasm.PluginCommandFlag) []stampCommandFlag {
	if len(flags) == 0 {
		return nil
	}
	out := make([]stampCommandFlag, len(flags))
	for i, f := range flags {
		out[i] = stampCommandFlag{
			Name:        f.Name,
			Shorthand:   f.Shorthand,
			Description: f.Description,
			Type:        f.Type,
			Default:     f.Default,
		}
	}
	return out
}

// SpliceCommands records commands into the entity bytes' spec.commands and
// returns the result together with whether it changed. Only that one block is
// rewritten; every other byte — comments, blank lines, indentation, and the
// trailing-newline shape — carries through verbatim. The block is created under
// spec when absent or replaced in place when present. Commands are rendered in
// declared order (the order the caller supplies), and an empty set is written as
// an explicit empty list so the machine-owned record is always present. The write
// is skipped — and the bytes returned unchanged — only when the file already
// records the commands present and equal (name, short help, and order); an absent
// record is still materialized so the field is always diff-reviewable.
func SpliceCommands(data []byte, commands []wasm.PluginCLICommand) ([]byte, bool, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, false, fmt.Errorf("parse entity for command recording: %w", err)
	}
	specKey, spec := topLevelChild(&root, "spec")
	if spec == nil || spec.Kind != yaml.MappingNode {
		return nil, false, fmt.Errorf("Plugin entity has no spec mapping to record the described commands into")
	}

	// Skip only when the record is already present and equal (order-sensitive); an
	// absent record is still materialized (even for a plugin with no commands) so
	// the machine-owned field is always present and diff-reviewable.
	if cmdKey, _ := childEntry(spec, "commands"); cmdKey != nil {
		var existing types.Plugin
		if err := yaml.Unmarshal(data, &existing); err != nil {
			return nil, false, fmt.Errorf("parse entity for command comparison: %w", err)
		}
		if wasm.CommandMirrorsEqual(EntityCommandMirror(existing), commands) {
			return data, false, nil
		}
	}

	lines := strings.Split(string(data), "\n")

	specIndent := specKey.Column - 1
	step := 2
	childIndent := specIndent + step
	if len(spec.Content) > 0 {
		childIndent = spec.Content[0].Column - 1
		if s := childIndent - specIndent; s > 0 {
			step = s
		}
	}

	value := make([]stampCommand, len(commands))
	for i, c := range commands {
		value[i] = stampCommand{
			Name:        c.Name,
			Description: c.Description,
			Flags:       stampCommandFlags(c.Flags),
		}
	}

	block, err := yamlsplice.RenderBlock("commands", value, childIndent, step)
	if err != nil {
		return nil, false, err
	}

	var result []string
	if cmdKey, _ := childEntry(spec, "commands"); cmdKey == nil {
		insertAt := yamlsplice.EndOfMappingContent(lines, specKey.Line, spec, childIndent, nil)
		result = spliceInsert(lines, insertAt, block)
	} else {
		start, end := yamlsplice.BlockRange(lines, cmdKey.Line, cmdKey.Column-1)
		result = spliceReplace(lines, start, end, block)
	}

	return []byte(strings.Join(result, "\n")), true, nil
}

// renderRuntimeWithHostFunctions renders a fresh spec.runtime block carrying only
// the hostFunctions record, indented to sit under spec.
func renderRuntimeWithHostFunctions(value []string, runtimeIndent, step int) ([]string, error) {
	hf, err := yamlsplice.RenderBlock("hostFunctions", value, runtimeIndent+step, step)
	if err != nil {
		return nil, err
	}
	return append([]string{strings.Repeat(" ", runtimeIndent) + "runtime:"}, hf...), nil
}

// spliceInsert returns lines with block inserted at the 0-indexed position at.
func spliceInsert(lines []string, at int, block []string) []string {
	result := make([]string, 0, len(lines)+len(block))
	result = append(result, lines[:at]...)
	result = append(result, block...)
	result = append(result, lines[at:]...)
	return result
}

// spliceReplace returns lines with the range [start, end) 0-indexed replaced by block.
func spliceReplace(lines []string, start, end int, block []string) []string {
	result := make([]string, 0, len(lines)-(end-start)+len(block))
	result = append(result, lines[:start]...)
	result = append(result, block...)
	result = append(result, lines[end:]...)
	return result
}

// topLevelChild returns the (key, value) nodes for the named key at the document's
// top-level mapping, or (nil, nil) when absent.
func topLevelChild(root *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	n := root
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil, nil
		}
		n = n.Content[0]
	}
	return childEntry(n, key)
}

// childEntry returns the (key, value) nodes for key within a mapping node, or
// (nil, nil) when the node is not a mapping or has no such key.
func childEntry(m *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i], m.Content[i+1]
		}
	}
	return nil, nil
}

// scalarSeqValues returns the scalar values of a YAML sequence node, or nil when
// the node is not a sequence.
func scalarSeqValues(node *yaml.Node) []string {
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]string, 0, len(node.Content))
	for _, c := range node.Content {
		out = append(out, c.Value)
	}
	return out
}

// isBlockMappingWithChildren reports whether a node is a block-style mapping
// carrying at least one child, the only shape the single-key splice can add or
// replace a nested child under.
func isBlockMappingWithChildren(node *yaml.Node) bool {
	return node != nil && node.Kind == yaml.MappingNode && node.Style != yaml.FlowStyle && len(node.Content) > 0
}

// writeFileAtomic writes data to path atomically: it writes a temp file in the
// same directory and renames it over path, so a reader never observes a partial
// entity file and an interrupted write leaves the original intact.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// EntityCommandMirror maps a Plugin entity's machine-owned command record into
// the neutral command shape the mirror comparison reads. An entity that records
// no commands field yields an empty slice, which the comparison treats as equal
// to a plugin that describes no commands.
func EntityCommandMirror(entity types.Plugin) []wasm.PluginCLICommand {
	mirror := make([]wasm.PluginCLICommand, len(entity.Spec.Commands))
	for i, c := range entity.Spec.Commands {
		mirror[i] = wasm.PluginCLICommand{
			Name:        c.Name,
			Description: c.Description,
			Flags:       entityCommandFlags(c.Flags),
		}
	}
	return mirror
}

// entityCommandFlags projects an entity command's authored flag mirror into the
// neutral flag shape the command-mirror comparison reads, preserving declared
// order. A command that records no flags yields a nil slice, which the comparison
// treats as equal to a described command that declares no flags.
func entityCommandFlags(flags []types.PluginCommandFlag) []wasm.PluginCommandFlag {
	if len(flags) == 0 {
		return nil
	}
	out := make([]wasm.PluginCommandFlag, len(flags))
	for i, f := range flags {
		out[i] = wasm.PluginCommandFlag{
			Name:        f.Name,
			Shorthand:   f.Shorthand,
			Description: f.Description,
			Type:        f.Type,
			Default:     f.Default,
		}
	}
	return out
}

// CanonicalHostFunctionsEqual reports whether two host-function sets are equal as
// sets (order and duplicates ignored), treating an absent record (nil) and an
// explicit empty set as equal. It delegates to the single set-equality home in the
// wasm package so the record-sync tooling and the publication validation door
// compare a host-function record by exactly the same rule.
func CanonicalHostFunctionsEqual(a, b []string) bool {
	return wasm.HostFunctionsEqual(a, b)
}
