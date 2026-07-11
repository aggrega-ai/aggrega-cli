// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// genCatalogRunner runs the catalog-generation operation with the supplied
// argument vector, writing its output to stdout and stderr. It is the seam the
// gen-catalog tool drives: the production value re-invokes the running binary's
// catalog-generation subcommand so the generation logic has a single home, and
// a test injects a fake that records the arguments.
type genCatalogRunner func(ctx context.Context, args []string, stdout, stderr io.Writer) error

// genCatalogArgs is the gen-catalog tool's argument shape, mirroring the
// catalog-generation subcommand's inputs.
type genCatalogArgs struct {
	FromEmbedded bool   `json:"from_embedded"`
	Plugin       string `json:"plugin"`
	Output       string `json:"output"`
	Overlay      string `json:"overlay"`
}

// genCatalogResultPayload is the gen-catalog tool's structured payload: the
// generation subcommand's stdout, which lists the entities it wrote.
type genCatalogResultPayload struct {
	Output string `json:"output"`
}

var genCatalogToolSpec = toolSpec{
	name:  "gen-catalog",
	kinds: []string{"plugin"},
	description: "Generate Plugin catalog entities from Wasm guests, driving the compiler's own catalog-generation " +
		"operation. Each entity's contract fields are derived from the guest's self-report, and its identity and runtime " +
		"limits come from an authored overlay, so a generated entity can never promise a capability the binary does not " +
		"provide. Two modes: `from_embedded: true` generates an entity for every guest embedded in the compiler (the " +
		"`overlay` is a directory of per-plugin overlays); or `plugin` names a single .wasm file to generate one entity " +
		"for (the `overlay` is a single file). `output` is the directory the entities are written under. Regenerating over " +
		"unchanged inputs yields byte-identical files. Use it to keep Plugin catalog entities in sync with the guests they " +
		"describe.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "from_embedded": {
      "type": "boolean",
      "description": "Generate an entity for every guest embedded in the compiler. The overlay is then a directory of per-plugin overlays. Mutually exclusive with plugin."
    },
    "plugin": {
      "type": "string",
      "description": "Path to a single .wasm guest to generate one entity for. The overlay is then a single overlay file. Mutually exclusive with from_embedded."
    },
    "output": {
      "type": "string",
      "description": "Directory the generated Plugin entities are written under."
    },
    "overlay": {
      "type": "string",
      "description": "Authored overlay: a directory (with from_embedded) or a single file (with plugin)."
    }
  },
  "required": ["output", "overlay"]
}`,
	invoke: genCatalogInvoke,
}

func genCatalogInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a genCatalogArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: "invalid gen-catalog arguments: " + err.Error()}
		}
	}
	switch {
	case a.FromEmbedded && strings.TrimSpace(a.Plugin) != "":
		return nil, &rpcError{Code: codeInvalidParams, Message: "gen-catalog takes either 'from_embedded' or 'plugin', not both"}
	case !a.FromEmbedded && strings.TrimSpace(a.Plugin) == "":
		return nil, &rpcError{Code: codeInvalidParams, Message: "gen-catalog requires one of 'from_embedded': true or a 'plugin' path"}
	}
	if strings.TrimSpace(a.Output) == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "gen-catalog requires an 'output' directory"}
	}
	if strings.TrimSpace(a.Overlay) == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "gen-catalog requires an 'overlay' path (a directory with from_embedded, a file with plugin)"}
	}

	argv := []string{"plugin", "gen-catalog", "--output", a.Output, "--overlay", a.Overlay}
	if a.FromEmbedded {
		argv = append(argv, "--from-embedded")
	} else {
		argv = append(argv, "--plugin", a.Plugin)
	}

	run := s.runGenCatalog
	if run == nil {
		run = selfExecGenCatalog
	}
	var stdout, stderr bytes.Buffer
	if err := run(ctx, argv, &stdout, &stderr); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return errorResult(explainToolError("Catalog generation failed.", fmt.Errorf("%s", detail), "plugin"))
	}
	out := stdout.String()
	return textResult(strings.TrimRight(out, "\n"), genCatalogResultPayload{Output: out})
}

// selfExecGenCatalog runs the catalog-generation operation by re-invoking the
// running binary's catalog-generation subcommand as a subprocess, so the
// generation logic keeps its single home in the command layer rather than being
// duplicated here. The subprocess inherits the current working directory, which
// grounds the embedded-guest and overlay resolution the subcommand performs.
func selfExecGenCatalog(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate running binary for catalog generation: %w", err)
	}
	cmd := exec.CommandContext(ctx, self, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}
