// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	schemas "github.com/aggrega-ai/aggrega-schemas"

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
	description: "Return the canonical JSON Schema for an Aggrega entity or contract kind, byte-identical to the " +
		"distributed schema file. Use it to learn the exact structure, required fields, and value constraints of a " +
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

// scaffoldArgs is the scaffold tool's argument shape.
type scaffoldArgs struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// scaffoldResult is the scaffold tool's structured payload: the kind it built a
// skeleton for and the rendered YAML skeleton.
type scaffoldResult struct {
	Kind     string `json:"kind"`
	Skeleton string `json:"skeleton"`
}

var scaffoldToolSpec = toolSpec{
	description: "Generate a minimal starter skeleton for an Aggrega entity kind, derived directly from that kind's " +
		"JSON Schema so it always tracks the real contract — the skeleton carries exactly the required fields, with the " +
		"fixed apiVersion and kind filled in and a placeholder for every other required value. Pass the kind token as " +
		"`kind` (component, aggregate, release, recipe, manifest, feature, and so on) and, optionally, a `name` to seed " +
		"metadata.name. Use it to start a new entity from a correct shape rather than from memory, then flesh out the " +
		"placeholders and validate. The output is YAML ready to write to a file. " +
		"The same drafting is on the command line as `aggrega scaffold <kind> [name]`, which prints these exact bytes to " +
		"standard output, so the generated `cli-scaffold` tool reaches it too by running that command in a subprocess. " +
		"Prefer this tool: it derives the skeleton in-process and returns it as structured data as well as text, and it " +
		"needs no workspace.",
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
      "description": "Optional value to seed metadata.name in the generated skeleton."
    }
  },
  "required": ["kind"]
}`,
	invoke: func(_ *server, _ context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
		var a scaffoldArgs
		if len(args) > 0 {
			if err := json.Unmarshal(args, &a); err != nil {
				return invalidToolArguments("scaffold", err)
			}
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
