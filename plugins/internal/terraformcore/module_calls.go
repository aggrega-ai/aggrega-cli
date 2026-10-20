// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package terraformcore

import (
	"fmt"
	"path"
	"strings"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	hcljson "github.com/hashicorp/hcl/v2/json"
	"github.com/zclconf/go-cty/cty"
)

// ModuleFetchedAtInitCode is the stable, machine-matchable class code carried by
// the warning raised for a `module` block the engine fetches when it initialises
// the configuration, rather than reading it from the files the build placed. It
// names a class of finding rather than a registered condition: the warning
// refuses nothing and the rendered bytes are the same whether or not it surfaces.
const ModuleFetchedAtInitCode = "module-fetched-at-init"

// ConfigFile is one file of a Terraform module tree: its path relative to the
// tree's root, slash-separated, and its bytes.
type ConfigFile struct {
	Path    string
	Content []byte
}

// ParseConfigFile parses one Terraform configuration file in the syntax its name
// declares — a name ending in .tf.json as the JSON variant, one ending in .tf as
// native syntax — and reports false for any other name, which is not a
// configuration file the engine loads. The parse resolves nothing and evaluates
// nothing; the returned file may be nil when the bytes do not parse at all.
func ParseConfigFile(name string, content []byte) (*hcl.File, hcl.Diagnostics, bool) {
	jsonSyntax, ok := configSyntax(name)
	switch {
	case !ok:
		return nil, nil, false
	case jsonSyntax:
		f, d := hcljson.Parse(content, name)
		return f, d, true
	default:
		f, d := hclsyntax.ParseConfig(content, name, hcl.Pos{Line: 1, Column: 1})
		return f, d, true
	}
}

// configSyntax reports whether a file name is one of the configuration files the
// engine loads and, when it is, whether it is written in the JSON variant.
func configSyntax(name string) (jsonSyntax, ok bool) {
	switch {
	case strings.HasSuffix(name, ".tf.json"):
		return true, true
	case strings.HasSuffix(name, ".tf"):
		return false, true
	default:
		return false, false
	}
}

// FetchedModuleCall is one `module` block the engine fetches when it initialises
// the configuration rather than reading it from the module tree it was handed.
type FetchedModuleCall struct {
	// File is the configuration file declaring the block, relative to the tree's root.
	File string
	// Line is the line of the block's `source` argument in that file.
	Line int
	// Name is the block's label.
	Name string
	// Source is the `source` argument's text exactly as it is written in the file,
	// quotes included where the file quotes it.
	Source string
	// OutsideTree is true when the source is a local path that resolves outside the
	// tree, and false when it is a module address the engine downloads.
	OutsideTree bool
}

// FetchedModuleCalls returns every `module` block in a module tree whose source
// the engine fetches at initialisation rather than reads from the tree: a source
// that is not a local path — a registry address, a git, Mercurial or archive URL,
// a bucket, an absolute path — and a local path that climbs out of the tree, since
// it names files the tree does not hold.
//
// What is read is what the engine itself loads when the tree's root is called:
// the configuration files directly in the root directory, then those in each
// directory a local source reaches from there, followed transitively, each
// directory read once so a cycle stops. A directory nothing reaches — the examples
// or tests a module commonly ships — is not read, because the engine never loads
// it when the module is called and a call written there is never fetched.
//
// Only what a source says is read. A local path is a `./`, `../`, `.\` or `..\`
// prefix, which is how the engine tells one apart; anything else is an address it
// fetches. A source the engine evaluates rather than reads as written — a
// template with an interpolation in it, or a reference — is judged by its leading
// literal text: one that opens with an address is reported as written, while one
// opening with a local prefix, or with no literal text at all, is neither reported
// nor followed, because where it leads is not in the text. A file that does not
// parse contributes whatever blocks it parsed; its syntax is reported elsewhere.
//
// The result is in walk order — directories in the order they are first reached,
// files in the order the tree lists them, blocks in the order each file declares
// them — so identical input yields an identical result.
func FetchedModuleCalls(files []ConfigFile) []FetchedModuleCall {
	byDir := make(map[string][]int, len(files))
	for i, f := range files {
		if _, ok := configSyntax(f.Path); !ok {
			continue
		}
		dir := path.Dir(f.Path)
		byDir[dir] = append(byDir[dir], i)
	}

	var calls []FetchedModuleCall
	reached := map[string]bool{".": true}
	queue := []string{"."}
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		for _, i := range byDir[dir] {
			for _, mc := range moduleCallsIn(files[i]) {
				call := FetchedModuleCall{File: files[i].Path, Line: mc.line, Name: mc.name, Source: mc.written}
				if mc.remote {
					calls = append(calls, call)
					continue
				}
				if mc.local == "" {
					continue
				}
				// The engine reads a backslash in a local source as a separator.
				target := path.Join(dir, strings.ReplaceAll(mc.local, `\`, "/"))
				if target == ".." || strings.HasPrefix(target, "../") {
					call.OutsideTree = true
					calls = append(calls, call)
					continue
				}
				if !reached[target] {
					reached[target] = true
					queue = append(queue, target)
				}
			}
		}
	}
	return calls
}

// Diagnostic is the warning the call raises: warn severity, the
// [ModuleFetchedAtInitCode] class, and a message naming the file, the line, the
// block and its source as written.
//
// It carries no location. The host attributes a finding without one to the node
// and the provider that rendered it, and the file and line sit in the message
// instead, because a location here would replace the node the reader needs in
// order to find the Recipe concerned.
func (c FetchedModuleCall) Diagnostic() renderctx.Diagnostic {
	var msg string
	if c.OutsideTree {
		msg = fmt.Sprintf("module %q (%s, line %d) has the local source %s, which resolves outside the module this build placed, so Terraform reads whatever sits at that path when the configuration is initialised rather than files this build vendored", c.Name, c.File, c.Line, c.Source)
	} else {
		msg = fmt.Sprintf("module %q (%s, line %d) has the source %s, which Terraform downloads when the configuration is initialised, at whatever version it then selects, so no commit or digest this build records covers that module", c.Name, c.File, c.Line, c.Source)
	}
	return renderctx.Diagnostic{
		Severity: abi.SeverityWarn,
		Code:     ModuleFetchedAtInitCode,
		Message:  msg,
	}
}

// ModuleFetchedAtInitDiagnostics returns the warning for each call
// [FetchedModuleCalls] finds in the tree, in its order.
func ModuleFetchedAtInitDiagnostics(files []ConfigFile) []renderctx.Diagnostic {
	calls := FetchedModuleCalls(files)
	if len(calls) == 0 {
		return nil
	}
	out := make([]renderctx.Diagnostic, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.Diagnostic())
	}
	return out
}

// moduleCall is one `module` block's source, classified. local holds a local
// path the walk follows; remote is true for a source the engine fetches; both
// empty means the source says nothing the walk can act on.
type moduleCall struct {
	name    string
	line    int
	written string
	local   string
	remote  bool
}

var (
	moduleBlockSchema = &hcl.BodySchema{Blocks: []hcl.BlockHeaderSchema{{Type: "module", LabelNames: []string{"name"}}}}
	sourceAttrSchema  = &hcl.BodySchema{Attributes: []hcl.AttributeSchema{{Name: "source"}}}
)

// moduleCallsIn reads the `module` blocks one configuration file declares, in
// declaration order, and classifies each block's source.
func moduleCallsIn(f ConfigFile) []moduleCall {
	file, _, _ := ParseConfigFile(f.Path, f.Content)
	if file == nil || file.Body == nil {
		return nil
	}
	content, _, _ := file.Body.PartialContent(moduleBlockSchema)
	if content == nil {
		return nil
	}
	var out []moduleCall
	for _, b := range content.Blocks {
		if len(b.Labels) == 0 || b.Body == nil {
			continue
		}
		attrs, _, _ := b.Body.PartialContent(sourceAttrSchema)
		if attrs == nil {
			continue
		}
		attr, ok := attrs.Attributes["source"]
		if !ok || attr.Expr == nil {
			continue
		}
		rng := attr.Expr.Range()
		mc := moduleCall{name: b.Labels[0], line: rng.Start.Line, written: writtenText(f.Content, rng)}
		jsonSyntax, _ := configSyntax(f.Path)
		classifySource(&mc, attr.Expr, jsonSyntax)
		out = append(out, mc)
	}
	return out
}

// classifySource sets the call's local path or marks it remote, from the value a
// literal source has or, for a source the engine evaluates, from its leading
// literal text.
//
// The JSON syntax evaluates a string with no evaluation context as its literal
// text, so a JSON template would otherwise read as a literal source holding the
// interpolation's own characters; such a string is judged by its leading literal
// text instead, as a native template is.
func classifySource(mc *moduleCall, expr hcl.Expression, jsonSyntax bool) {
	if v, diags := expr.Value(nil); !diags.HasErrors() && v.IsWhollyKnown() && !v.IsNull() && v.Type() == cty.String {
		s := v.AsString()
		if !jsonSyntax || !(strings.Contains(s, "${") || strings.Contains(s, "%{")) {
			if isLocalSource(s) {
				mc.local = s
			} else {
				mc.remote = true
			}
			return
		}
	}
	lead, quoted := strings.CutPrefix(mc.written, `"`)
	if !quoted || lead == "" || strings.HasPrefix(lead, "${") || strings.HasPrefix(lead, "%{") || strings.HasPrefix(lead, `"`) {
		return
	}
	if !isLocalSource(lead) {
		mc.remote = true
	}
}

// isLocalSource reports whether a source names a local path, which the engine
// decides from the prefix alone.
func isLocalSource(s string) bool {
	for _, p := range []string{"./", "../", `.\`, `..\`} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// writtenText is the text a range covers in the file, or empty when the range does
// not sit inside the bytes.
func writtenText(content []byte, rng hcl.Range) string {
	if rng.Start.Byte < 0 || rng.End.Byte > len(content) || rng.Start.Byte > rng.End.Byte {
		return ""
	}
	return string(content[rng.Start.Byte:rng.End.Byte])
}
