// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/internal/exampleops"
	"github.com/aggrega-ai/aggrega-cli/internal/scaffoldops"
)

// getSchemaArgs is the get-schema tool's argument shape.
type getSchemaArgs struct {
	Kind string `json:"kind"`
	List bool   `json:"list"`
}

// getSchemaListResult is the payload when the caller asks for the kind index.
type getSchemaListResult struct {
	Kinds []string `json:"kinds"`
}

// getSchemaResult is the payload for a single kind's schema.
type getSchemaResult struct {
	Kind   string          `json:"kind"`
	Schema json.RawMessage `json:"schema"`
}

var getSchemaToolSpec = toolSpec{
	summary: "Returns the JSON Schema for an entity or contract kind.",
	description: "Return the canonical JSON Schema for an Aggrega entity or contract kind, read from the copy built " +
		"into this binary, byte-identical to the distributed schema file, and writing nothing. Use it to learn the " +
		"exact structure, required fields, and value constraints of a " +
		"kind before authoring or validating one. Pass the lowercase-hyphenated kind token as `kind` — for example " +
		"component, aggregate, release, manifest, recipe, ir, or wasm-provider. Pass `list: true` (with no kind) to " +
		"enumerate every addressable kind first. An unknown kind returns the full valid set so you can correct a typo " +
		"without a second call. This is the authoritative shape source; scaffold derives its starter skeletons from it.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "kind": {
      "type": "string",
      "minLength": 1,
      "description": "The lowercase-hyphenated schema kind token, e.g. component, aggregate, release, recipe, ir, wasm-provider. Required unless list is true."
    },
    "list": {
      "type": "boolean",
      "description": "When true, enumerate every addressable schema kind instead of returning one schema. Ignores kind."
    }
  },
  "anyOf": [
    {"required":["kind"]},
    {"required":["list"],"properties":{"list":{"const":true}}}
  ]
}`,
	invoke: func(_ *server, _ context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
		var a getSchemaArgs
		if len(args) > 0 {
			if err := json.Unmarshal(args, &a); err != nil {
				return invalidToolArguments("get-schema", err)
			}
		}
		if a.List {
			kinds := schemas.Kinds()
			return textResult("Available schema kinds:\n"+strings.Join(kinds, "\n"), getSchemaListResult{Kinds: kinds})
		}
		kind := strings.TrimSpace(a.Kind)
		if kind == "" {
			return errorResult("get-schema requires a 'kind' argument, or 'list': true to enumerate kinds")
		}
		schema, ok := schemas.SchemaByKind(kind)
		if !ok {
			return errorResult(scaffoldops.UnknownKindGuidance(kind))
		}
		return textResult(schema, getSchemaResult{Kind: kind, Schema: json.RawMessage(schema)})
	},
}

// scaffoldArgs is the scaffold tool's argument shape: kind and name for drafting
// a starter document, example and dir for writing a runnable example, list for
// listing the examples, and the workspace the example form writes into.
type scaffoldArgs struct {
	Kind          string `json:"kind,omitempty"`
	Name          string `json:"name,omitempty"`
	Example       string `json:"example,omitempty"`
	List          bool   `json:"list,omitempty"`
	Dir           string `json:"dir,omitempty"`
	WorkspaceRoot string `json:"workspace_root,omitempty"`
}

// scaffoldExample is one runnable example in the list the scaffold tool answers
// with: its name, its level and the first sentence of its page.
type scaffoldExample struct {
	Name    string `json:"name"`
	Level   string `json:"level"`
	Summary string `json:"summary"`
}

// scaffoldResult is the scaffold tool's structured payload, one shape for the
// tool's three forms, each carrying its own fields and leaving the others out:
// the kind a skeleton was built for and the rendered YAML skeleton; the list of
// examples; or the example written, the directory it went into as the call named
// it, the files written relative to that directory, the commands its page gives
// for building it, and the version of the documentation it was read from.
type scaffoldResult struct {
	Kind     string            `json:"kind,omitempty"`
	Skeleton string            `json:"skeleton,omitempty"`
	Examples []scaffoldExample `json:"examples,omitempty"`
	Example  string            `json:"example,omitempty"`
	Dir      string            `json:"dir,omitempty"`
	Files    []string          `json:"files,omitempty"`
	Next     []string          `json:"next,omitempty"`
	Version  string            `json:"version,omitempty"`
}

var scaffoldToolSpec = toolSpec{
	summary: "Drafts a starter document for an entity kind, or writes a runnable example into a new directory.",
	description: "Generate a minimal starter skeleton for an Aggrega entity kind and return it as YAML, writing " +
		"nothing. The skeleton is derived directly from that kind's JSON Schema so it always tracks the real " +
		"contract: it carries exactly the required fields, with the fixed apiVersion and kind filled in and a " +
		"placeholder for every other required value. Pass the kind token as " +
		"`kind` (component, aggregate, release, recipe, manifest, feature, and so on) and, optionally, a `name` to seed " +
		"metadata.name. Use it to start a new entity from a correct shape rather than from memory, then flesh out the " +
		"placeholders and validate. Drafting needs no workspace. The same drafting is on the command line as " +
		"`aggrega scaffold <kind> [name]`, which prints these exact bytes to standard output. " +
		"Send `example` instead of `kind` to WRITE one of the documentation's runnable examples into a new " +
		"directory of the workspace — `dir`, relative to the workspace root, or a directory named after the " +
		"example there when `dir` is left out — and be answered with the files written, relative to that " +
		"directory, the commands the example's page gives for building it, and the documentation version it " +
		"was read from; send `list: true` alone to be answered with every example there is, each with its level " +
		"and the first sentence of its page, writing nothing. The directory must not exist yet or must be " +
		"empty: one holding anything is refused and left as it is. Over HTTP `dir` must lie inside the " +
		"workspace this server serves; over standard input and output it is taken as named, as every path " +
		"there is. The examples are read from the signed documentation bundle published for this server's " +
		"version — or, where none is, the newest published before it — kept in the cache every project on " +
		"the machine shares and verified against the workspace's signature policy before it is kept or read: " +
		"a bundle that does not verify is refused, never used, offline only a cached copy is read, and a " +
		"bundle carrying a link, or an entry that would land outside the directory, is refused with nothing " +
		"written. Fields of two forms sent together are refused. The same is on the command line as " +
		"`aggrega scaffold --example <name> [dir]` and `aggrega scaffold --example --list`.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "kind": {
      "type": "string",
      "minLength": 1,
      "description": "The entity kind to scaffold, as a lowercase-hyphenated schema token, e.g. component, aggregate, release, recipe, feature, manifest."
    },
    "name": {
      "type": "string",
      "description": "Optional value to seed metadata.name in the generated skeleton. Sent only with kind."
    },
    "example": {
      "type": "string",
      "minLength": 1,
      "description": "The runnable example to write into a new directory, by the name the list of examples gives it. Sent instead of kind."
    },
    "list": {
      "type": "boolean",
      "description": "When true, with no example, answer with every runnable example the documentation publishes, and write nothing."
    },
    "dir": {
      "type": "string",
      "minLength": 1,
      "description": "The new directory the example is written into, relative to the workspace root; it must not exist yet or must be empty. Defaults to a directory named after the example. Sent only with example."
    },
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override for writing or listing an example. Defaults to the workspace root the server was launched with, or, where it was launched with none, to discovery from its working directory."
    }
  },
  "anyOf": [
    {"required":["kind"]},
    {"required":["example"]},
    {"required":["list"],"properties":{"list":{"const":true}}}
  ]
}`,
	reachEscalation: scaffoldReach,
	invoke: func(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
		var a scaffoldArgs
		if len(args) > 0 {
			if err := json.Unmarshal(args, &a); err != nil {
				return invalidToolArguments("scaffold", err)
			}
		}
		form, err := exampleops.RequestForm(a.Kind, a.Name, a.Example, a.Dir, a.List)
		if err != nil {
			return errorResult(err.Error())
		}
		if form != exampleops.FormDraft {
			return s.scaffoldExample(ctx, form, a)
		}
		kind := strings.TrimSpace(a.Kind)
		if kind == "" {
			return errorResult("scaffold requires a 'kind' argument")
		}
		skeleton, err := scaffoldops.Skeleton(kind, a.Name)
		if err != nil {
			var unknown *scaffoldops.UnknownKindError
			if errors.As(err, &unknown) {
				return errorResult(unknown.Error())
			}
			return errorResult(explainToolError(
				fmt.Sprintf("Could not derive a skeleton for kind %q from its schema.", kind), err, kind))
		}
		return textResult(skeleton, scaffoldResult{Kind: kind, Skeleton: skeleton})
	},
}
