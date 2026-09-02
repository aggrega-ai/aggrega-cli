// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/clisurface"
)

// CommandSurface is one command the server may expose as a generated tool: the
// descriptor the command-surface projection records, plus the one fact that
// projection deliberately does not carry — whether the command executes an
// operation or only groups subcommands.
//
// Runnable is supplied rather than inferred here. Whether a command runs is the
// command tree's own answer, and the caller holding the tree is the only place
// that can read it. Inferring it from a child count would be a second
// derivation of the same fact: it agrees with the tree today, and it stops
// agreeing the first time a grouping command gains an operation of its own — at
// which point that operation would drop out of the tool surface with nothing
// reporting the loss.
type CommandSurface struct {
	// Descriptor is the projected command: its invocation path, its help text,
	// and the flags it declares and inherits.
	Descriptor clisurface.Descriptor

	// Runnable reports whether the command executes an operation. A grouping
	// command that only holds subcommands yields no tool.
	Runnable bool
}

// CommandExemption records one command deliberately absent from the generated
// command-tool surface, together with the reason it is absent.
//
// The exemptions are data rather than branches in the derivation so that a
// reader, and a completeness check, can enumerate what is missing and why
// instead of reconstructing it from control flow. An omission is therefore
// declared rather than silent — which is only true while the list is readable
// from outside this package, so it is exported alongside the derivation that
// consumes it.
type CommandExemption struct {
	// Path is the command's full invocation path, matching Descriptor.Path. It
	// covers that command and every subcommand beneath it, so a command group
	// whose whole subtree is unusable here is named once rather than enumerated
	// — and a subcommand added to that group later is covered without an edit.
	Path string

	// Reason states, in one line, why the command is not exposed.
	Reason string
}

// CommandExemptions returns the commands deliberately absent from the generated
// command-tool surface, each with its reason, in a fixed order.
//
// It returns a copy, so a caller enumerating the exemptions cannot alter the
// set the derivation reads.
func CommandExemptions() []CommandExemption {
	return append([]CommandExemption(nil), commandExemptions...)
}

// commandExemptions are the commands that stay off the tool surface.
//
// Two distinct grounds appear here and they are deliberately not merged. A
// command may be unusable through a tool protocol because of what it does to
// the stream or to the machine it runs on; or it may have no non-interactive
// form at all, meaning there is no supplied-or-ambient path it could take even
// in principle. Only `auth token` is on the second ground, and recording it as
// such is what keeps the signing verbs off this list: push, sign and deprecate
// all resolve an identity from supplied or ambient inputs and refuse loudly
// when none is available, so they are usable here and are exposed.
var commandExemptions = []CommandExemption{
	{
		Path:   "aggrega serve",
		Reason: "a long-running daemon: it holds its stream until stopped, so a tool call would never return a result",
	},
	{
		Path:   "aggrega mcp",
		Reason: "this server: running it from inside itself would nest one protocol stream inside another",
	},
	{
		Path:   "aggrega login",
		Reason: "reads a registry password from the terminal, which a tool protocol has no way to answer",
	},
	{
		Path:   "aggrega logout",
		Reason: "removes a credential from the shared machine-wide store, outside any workspace the caller named",
	},
	{
		Path:   "aggrega auth token",
		Reason: "no non-interactive form: acquiring a token interactively is the whole verb, so no supplied or ambient input can satisfy it",
	},
	{
		Path:   "aggrega completion",
		Reason: "writes a shell completion script, and a caller reaching this surface over a protocol has no shell to install one into",
	},
	{
		Path:   "aggrega help",
		Reason: "returns help text rather than doing work; the cli:// resource family already serves every command's help",
	},
}

// CommandExempted reports the exemption covering a command path: an exact
// match, or an ancestor whose whole subtree is exempt.
//
// It is exported alongside the list because enumerating the exemptions and
// deciding whether a given command is exempt are different questions, and a
// caller that can only do the first has to reimplement the second — at which
// point the subtree rule exists in two places and the copy is what goes stale.
func CommandExempted(path string) (CommandExemption, bool) {
	for _, e := range commandExemptions {
		if e.Path == path || strings.HasPrefix(path, e.Path+" ") {
			return e, true
		}
	}
	return CommandExemption{}, false
}

// commandToolPrefix distinguishes a generated command tool from a curated one.
//
// The curated tools are named for the goal they serve — validate, build, lint,
// get-schema — and commands of those same names exist, so an unprefixed
// derivation would collide with the curated tool it must not replace. The
// prefix names the surface the tool reaches: the same command-line surface the
// cli:// resource family serves help for.
const commandToolPrefix = "cli-"

// commandPositionalProperty carries a command's positional arguments, in the
// order the command reads them.
const commandPositionalProperty = "args"

// commandArgument binds one generated tool property to the command flag it
// fills. The binding is recorded when the tool is derived, so building an
// argument vector never has to invert a property-name spelling back into a flag
// name — the two spellings differ, and inverting a lossy transform is how they
// would drift apart.
type commandArgument struct {
	// property is the wire key an MCP client sends.
	property string

	// flag is the command's own long flag name, without leading dashes.
	flag string

	// jsonType is the JSON Schema type the property accepts.
	jsonType string

	// repeated reports whether the flag takes a list, in which case the flag is
	// emitted once per element.
	repeated bool

	// usage is the flag's help text, carried into the property description.
	usage string

	// def is the flag's default value as the command records it.
	def string
}

// commandTool is one generated tool's derived shape: the argument vector prefix
// that selects the command, the flag-backed arguments it accepts, and the
// positional summary it declares.
type commandTool struct {
	// name is the tool's advertised name.
	name string

	// path is the argument vector that selects the command, without the binary
	// name.
	path []string

	// arguments are the flag-backed properties, in a fixed order.
	arguments []commandArgument

	// positional is the command's positional-argument summary as it declares
	// it, empty when the command takes none.
	positional string
}

// deriveCommandTools projects the command surface into the generated tool set,
// in catalog order, skipping grouping commands and exempt commands.
//
// The order is the catalog's own, which is a property of the command tree
// alone, so the derived set and every listing built from it are byte-stable
// across runs.
func deriveCommandTools(surface []CommandSurface) []toolSpec {
	out := make([]toolSpec, 0, len(surface))
	for _, cs := range surface {
		if !cs.Runnable {
			continue
		}
		if _, exempt := CommandExempted(cs.Descriptor.Path); exempt {
			continue
		}
		if cs.Descriptor.Hidden {
			continue
		}
		out = append(out, deriveCommandTool(cs.Descriptor))
	}
	return out
}

// deriveCommandTool builds one generated tool from a command descriptor: its
// name from the invocation path, its description from the command's own one-line
// help, and its arguments from the flags the command declares and inherits.
//
// Nothing about the command is restated here. Every field is read from the
// descriptor, so a renamed flag or a reworded help line reaches the tool surface
// without a second place to edit.
func deriveCommandTool(d clisurface.Descriptor) toolSpec {
	ct := commandTool{
		name:       commandToolName(d.Path),
		path:       commandArgvPath(d.Path),
		arguments:  commandArguments(d),
		positional: commandPositionalSummary(d.Use),
	}
	return toolSpec{
		name:        ct.name,
		description: commandToolDescription(d, ct),
		inputSchema: commandInputSchema(ct),
		invoke:      ct.invoke,
	}
}

// commandToolName renders a command's invocation path as its tool name: the
// path below the binary, joined by dashes, behind the command-tool prefix.
func commandToolName(path string) string {
	return commandToolPrefix + strings.Join(commandArgvPath(path), "-")
}

// commandArgvPath splits a command's invocation path into the argument vector
// that selects it, dropping the binary name the path starts with.
func commandArgvPath(path string) []string {
	fields := strings.Fields(path)
	if len(fields) == 0 {
		return nil
	}
	return fields[1:]
}

// commandPositionalSummary returns the positional-argument summary a command
// declares in its usage line: everything after the command's own name. It is
// empty for a command that takes no positional arguments.
func commandPositionalSummary(use string) string {
	fields := strings.Fields(use)
	if len(fields) < 2 {
		return ""
	}
	return strings.Join(fields[1:], " ")
}

// commandArguments derives the flag-backed properties a generated tool accepts:
// the command's own flags followed by the flags it inherits from its ancestors.
//
// A hidden flag is omitted — it is not part of the documented surface — and a
// name declared locally shadows the inherited flag of the same name, which is
// the same precedence the command line itself applies. The help flag is skipped
// because a tool result carrying help text instead of doing the work is not an
// outcome any caller asked for; the cli:// resource family serves help.
func commandArguments(d clisurface.Descriptor) []commandArgument {
	var out []commandArgument
	seen := map[string]bool{}
	add := func(flags []clisurface.Flag) {
		for _, f := range flags {
			if f.Hidden || f.Name == "help" || seen[f.Name] {
				continue
			}
			seen[f.Name] = true
			out = append(out, commandArgument{
				property: commandArgumentName(f.Name),
				flag:     f.Name,
				jsonType: jsonSchemaTypeFor(f.Type),
				repeated: isRepeatedFlagType(f.Type),
				usage:    f.Usage,
				def:      f.Default,
			})
		}
	}
	add(d.LocalFlags)
	add(d.InheritedFlags)
	return out
}

// commandArgumentName renders a flag's long name as the wire property an MCP
// client sends. A JSON-RPC input-schema property is a wire payload key, so it
// carries the flag's own words in snake_case: --output-dir is sent as
// output_dir. The words are the flag's; only the separator changes.
func commandArgumentName(flag string) string {
	return strings.ReplaceAll(flag, "-", "_")
}

// isRepeatedFlagType reports whether a flag type accepts a list of values, in
// which case the flag is emitted once per element rather than once with a
// joined value.
func isRepeatedFlagType(flagType string) bool {
	return strings.HasSuffix(flagType, "Slice") || strings.HasSuffix(flagType, "Array")
}

// jsonSchemaTypeFor maps a command flag's value type to the JSON Schema type
// the generated property accepts. An unrecognized type is described as a
// string, which is how the command line itself receives every value, so an
// unmapped type stays usable rather than becoming unreachable.
func jsonSchemaTypeFor(flagType string) string {
	if isRepeatedFlagType(flagType) {
		return "array"
	}
	switch flagType {
	case "bool":
		return "boolean"
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "count":
		return "integer"
	case "float32", "float64":
		return "number"
	default:
		return "string"
	}
}

// commandToolDescription renders the description a client shows an assistant:
// what the command does, in the command's own words, plus how to reach its full
// help and what the tool returns.
//
// Only the one-line help is carried. The long-form help stays in the command
// layer and is served by the cli:// resource family, so a client that wants it
// reads it there rather than receiving every command's full help in one
// capability listing.
func commandToolDescription(d clisurface.Descriptor, ct commandTool) string {
	var b strings.Builder
	short := strings.TrimSpace(d.Short)
	if short != "" {
		b.WriteString(short)
		if !strings.HasSuffix(short, ".") {
			b.WriteString(".")
		}
		b.WriteString(" ")
	}
	fmt.Fprintf(&b, "Runs the `%s` command and returns its output. ", d.Path)
	if ct.positional != "" {
		fmt.Fprintf(&b, "Positional arguments go in `%s`, in the order the command reads them: %s. ",
			commandPositionalProperty, ct.positional)
	}
	fmt.Fprintf(&b, "Every other property is one of the command's flags, sent under the flag's own words in snake_case. "+
		"Read `cli://%s` for the command's full help.", strings.Join(ct.path, "/"))
	return b.String()
}

// commandInputSchema renders the generated tool's JSON Schema. The properties
// are written in the derived argument order rather than collected into a map,
// so the schema bytes are stable across runs.
func commandInputSchema(ct commandTool) string {
	var b strings.Builder
	b.WriteString("{\n  \"type\": \"object\",\n  \"properties\": {")
	first := true
	writeProperty := func(name, body string) {
		if !first {
			b.WriteString(",")
		}
		first = false
		fmt.Fprintf(&b, "\n    %s: %s", jsonQuote(name), body)
	}
	if ct.positional != "" {
		writeProperty(commandPositionalProperty, fmt.Sprintf(
			"{\n      \"type\": \"array\",\n      \"items\": {\"type\": \"string\"},\n      \"description\": %s\n    }",
			jsonQuote("The command's positional arguments, in the order it reads them: "+ct.positional+".")))
	}
	for _, a := range ct.arguments {
		writeProperty(a.property, commandArgumentSchema(a))
	}
	b.WriteString("\n  }\n}")
	return b.String()
}

// commandArgumentSchema renders one flag as its JSON Schema fragment, carrying
// the flag's own help text as the description and naming its default where the
// command records a meaningful one.
func commandArgumentSchema(a commandArgument) string {
	desc := strings.TrimSpace(a.usage)
	if desc == "" {
		desc = "The --" + a.flag + " flag."
	}
	if d := strings.TrimSpace(a.def); d != "" && d != "false" && d != "[]" && d != "0" {
		desc += " Defaults to " + d + "."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "{\n      \"type\": %s", jsonQuote(a.jsonType))
	if a.repeated {
		b.WriteString(",\n      \"items\": {\"type\": \"string\"}")
	}
	fmt.Fprintf(&b, ",\n      \"description\": %s\n    }", jsonQuote(desc))
	return b.String()
}

// jsonQuote renders s as a JSON string literal.
func jsonQuote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		// A Go string always marshals; the branch exists so the helper has no
		// error return to thread through every schema-building call site.
		return `""`
	}
	return string(b)
}

// invoke runs the generated tool's command and returns its output as the tool
// result.
//
// The command runs through the command runner seam, whose production value
// re-invokes the binary as a subprocess. Its streams are captured buffers, so
// the command's output becomes tool data and cannot reach this server's own
// stdout, where it would corrupt the protocol stream.
//
// A non-zero exit is a tool-level failure rather than a protocol error: the call
// was well-formed and the operation reported something the caller can act on, so
// the command's own diagnostics are returned as guidance.
func (ct commandTool) invoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	supplied := map[string]json.RawMessage{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &supplied); err != nil {
			return nil, invalidToolArguments(ct.name, err)
		}
	}
	argv, rpcErr := ct.argv(supplied)
	if rpcErr != nil {
		return nil, rpcErr
	}

	run := s.runCommand
	if run == nil {
		run = selfExecCommand
	}
	var stdout, stderr bytes.Buffer
	runErr := run(ctx, argv, &stdout, &stderr)

	out := strings.TrimRight(stdout.String(), "\n")
	diag := strings.TrimSpace(stderr.String())
	if runErr != nil {
		detail := diag
		if detail == "" {
			detail = runErr.Error()
		}
		guidance := fmt.Sprintf("`%s` did not succeed.\n\n%s", strings.Join(append([]string{"aggrega"}, argv...), " "), detail)
		if out != "" {
			guidance += "\n\nOutput before the failure:\n" + out
		}
		return errorResult(guidance)
	}
	text := out
	if diag != "" {
		if text != "" {
			text += "\n\n"
		}
		text += "Diagnostics:\n" + diag
	}
	return textResult(text, commandToolResultPayload{
		Command: strings.Join(append([]string{"aggrega"}, argv...), " "),
		Output:  stdout.String(),
		Diags:   stderr.String(),
	})
}

// commandToolResultPayload is a generated tool's structured payload: the
// command line that ran and the two streams it produced, kept apart so a caller
// reading the structured form can tell an operation's output from its
// diagnostics.
type commandToolResultPayload struct {
	Command string `json:"command"`
	Output  string `json:"output"`
	Diags   string `json:"diagnostics,omitempty"`
}

// argv builds the argument vector for a call: the command path, then one entry
// per supplied flag in the derived argument order, then the positional
// arguments behind a bare double dash.
//
// Flags are emitted in the derived order rather than in the order the caller's
// object happened to serialize them, so one set of arguments always produces one
// argument vector. A property the command does not declare is refused, naming
// what the tool accepts, rather than being dropped — a silently ignored argument
// reads to a caller as an argument that was honored.
func (ct commandTool) argv(supplied map[string]json.RawMessage) ([]string, *rpcError) {
	known := map[string]bool{commandPositionalProperty: ct.positional != ""}
	for _, a := range ct.arguments {
		known[a.property] = true
	}
	var unknown []string
	for name := range supplied {
		if !known[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, &rpcError{Code: codeInvalidParams, Message: fmt.Sprintf(
			"%s does not accept %s; it accepts: %s",
			ct.name, strings.Join(quoteAll(unknown), ", "), strings.Join(ct.acceptedProperties(), ", "))}
	}

	argv := append([]string(nil), ct.path...)
	for _, a := range ct.arguments {
		raw, ok := supplied[a.property]
		if !ok {
			continue
		}
		values, err := flagValues(a, raw)
		if err != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: fmt.Sprintf(
				"%s: the %q property %s", ct.name, a.property, err.Error())}
		}
		for _, v := range values {
			argv = append(argv, "--"+a.flag+"="+v)
		}
	}
	if raw, ok := supplied[commandPositionalProperty]; ok {
		positionals, err := jsonStringList(raw)
		if err != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: fmt.Sprintf(
				"%s: the %q property %s", ct.name, commandPositionalProperty, err.Error())}
		}
		if len(positionals) > 0 {
			// The bare double dash ends flag parsing, so a positional value that
			// begins with a dash is read as a value rather than as a flag.
			argv = append(argv, "--")
			argv = append(argv, positionals...)
		}
	}
	return argv, nil
}

// acceptedProperties names every property the tool accepts, for the refusal a
// caller reads when it sends one the tool does not.
func (ct commandTool) acceptedProperties() []string {
	out := make([]string, 0, len(ct.arguments)+1)
	if ct.positional != "" {
		out = append(out, commandPositionalProperty)
	}
	for _, a := range ct.arguments {
		out = append(out, a.property)
	}
	if len(out) == 0 {
		return []string{"(no arguments)"}
	}
	return out
}

// quoteAll renders each name as a quoted string for a diagnostic listing.
func quoteAll(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, fmt.Sprintf("%q", n))
	}
	return out
}

// flagValues renders one supplied property as the flag values it contributes: a
// single value for a scalar flag, one per element for a repeated flag.
func flagValues(a commandArgument, raw json.RawMessage) ([]string, error) {
	if a.repeated {
		return jsonStringList(raw)
	}
	switch a.jsonType {
	case "boolean":
		var v bool
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("must be a boolean")
		}
		return []string{fmt.Sprintf("%t", v)}, nil
	case "integer", "number":
		var v json.Number
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("must be a number")
		}
		return []string{v.String()}, nil
	default:
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("must be a string")
		}
		return []string{v}, nil
	}
}

// jsonStringList reads a JSON array of scalars as a list of strings. A single
// scalar is accepted as a one-element list, because a caller passing one value
// where a list is accepted is unambiguous and refusing it would buy nothing.
func jsonStringList(raw json.RawMessage) ([]string, error) {
	var list []any
	if err := json.Unmarshal(raw, &list); err == nil {
		out := make([]string, 0, len(list))
		for _, v := range list {
			s, ok := scalarString(v)
			if !ok {
				return nil, fmt.Errorf("must be an array of strings")
			}
			out = append(out, s)
		}
		return out, nil
	}
	var single any
	if err := json.Unmarshal(raw, &single); err != nil {
		return nil, fmt.Errorf("must be an array of strings")
	}
	s, ok := scalarString(single)
	if !ok {
		return nil, fmt.Errorf("must be an array of strings")
	}
	return []string{s}, nil
}

// scalarString renders a decoded JSON scalar as the string the command line
// receives. It reports false for a composite value, which has no command-line
// spelling.
func scalarString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case bool:
		return fmt.Sprintf("%t", t), true
	case float64:
		return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%f", t), "0"), "."), true
	case json.Number:
		return t.String(), true
	default:
		return "", false
	}
}
