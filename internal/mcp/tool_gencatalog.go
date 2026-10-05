// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/gencatalog"
)

// genCatalogRunner runs the catalog-generation core with the supplied options,
// writing its per-entity listing to out. It is the seam the gen-catalog tool
// drives: the production value is the in-process core so the generation logic
// has a single home, and a test injects a fake that records the options.
type genCatalogRunner func(ctx context.Context, opts gencatalog.Options, out io.Writer) error

// genCatalogArgs is the gen-catalog tool's argument shape, mirroring the
// catalog-generation subcommand's inputs.
//
// It carries no delivery selector, and that is a budget decision rather than an
// omission. A generated entity is content a catalog author owns, so the
// capability does carry a reporting delivery — and this protocol reaches it
// through the tool generated from that command, which derives its arguments from
// the command's documented flags and sits in the deferred set, where it costs a
// session's up-front definition budget nothing. Spelling the selector again here
// would be a second hand-written copy of a derived argument, paid for out of the
// default budget whose ceiling deliberately leaves almost no headroom.
type genCatalogArgs struct {
	FromEmbedded bool   `json:"from_embedded"`
	Plugin       string `json:"plugin"`
	Output       string `json:"output_dir"`
	Overlay      string `json:"overlay"`
}

// genCatalogResultPayload is the gen-catalog tool's structured payload: the
// generation subcommand's stdout, which lists the entities it wrote.
//
// That listing is prose the generation composed and it names the files it wrote,
// so it is told the way every answer here tells a location: over a listener each
// file is named relative to the workspace root.
type genCatalogResultPayload struct {
	Output string `json:"output"`
}

var genCatalogToolSpec = toolSpec{
	kinds:   []string{"plugin"},
	summary: "Generates Plugin catalog entities from WebAssembly guests.",
	description: "Generate Plugin catalog entities from Wasm guests, driving the compiler's own catalog-generation " +
		"operation and writing the entities to disk under the directory you name. Each entity's contract fields are " +
		"derived from the guest's self-report, and its identity and runtime " +
		"limits come from an authored overlay, so a generated entity can never promise a capability the binary does not " +
		"provide. Two modes: `from_embedded: true` generates an entity for every guest embedded in the compiler (the " +
		"`overlay` is a directory of per-plugin overlays); or `plugin` names a single .wasm file to generate one entity " +
		"for (the `overlay` is a single file). `output_dir` is the directory the entities are written under. Regenerating " +
		"over unchanged inputs yields byte-identical files. Use it to keep Plugin catalog entities in sync with the guests " +
		"they describe.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "from_embedded": {
      "type": "boolean",
      "description": "Generate an entity for every guest embedded in the compiler. The overlay is then a directory of per-plugin overlays. Mutually exclusive with plugin."
    },
    "plugin": {
      "type": "string",
      "minLength": 1,
      "description": "Path to a single .wasm guest to generate one entity for. The overlay is then a single overlay file. Mutually exclusive with from_embedded."
    },
    "output_dir": {
      "type": "string",
      "minLength": 1,
      "description": "Directory the generated Plugin entities are written under."
    },
    "overlay": {
      "type": "string",
      "minLength": 1,
      "description": "Authored overlay: a directory (with from_embedded) or a single file (with plugin)."
    }
  },
  "required": ["output_dir", "overlay"],
  "anyOf": [
    {"required":["from_embedded"],"properties":{"from_embedded":{"const":true}},"not":{"required":["plugin"]}},
    {"required":["plugin"],"properties":{"from_embedded":{"const":false}}}
  ]
}`,
	invoke: genCatalogInvoke,
}

func genCatalogInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a genCatalogArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments("gen-catalog", err)
		}
	}
	switch {
	case a.FromEmbedded && strings.TrimSpace(a.Plugin) != "":
		return errorResult("gen-catalog takes either 'from_embedded' or 'plugin', not both")
	case !a.FromEmbedded && strings.TrimSpace(a.Plugin) == "":
		return errorResult("gen-catalog requires one of 'from_embedded': true or a 'plugin' path")
	}
	if strings.TrimSpace(a.Output) == "" {
		return errorResult("gen-catalog requires an 'output_dir' directory")
	}
	if strings.TrimSpace(a.Overlay) == "" {
		return errorResult("gen-catalog requires an 'overlay' path (a directory with from_embedded, a file with plugin)")
	}

	// The three paths resolve through the same helper every other path argument on
	// this surface goes through, so they are named relative to the workspace and,
	// over a listener, stay inside it. The output directory is the one argument here
	// that is WRITTEN to rather than read, which is why it is resolved rather than
	// passed on as it arrived.
	tw, err := s.resolveToolWorkspace("", nil)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace to generate entities in.", err, "plugin"))
	}
	pluginPath := ""
	if trimmed := strings.TrimSpace(a.Plugin); trimmed != "" {
		resolved, refusal := tw.workspacePath("gen-catalog", "plugin", trimmed)
		if refusal != "" {
			return errorResult(refusal)
		}
		pluginPath = resolved
	}
	outputDir, refusal := tw.workspacePath("gen-catalog", "output_dir", strings.TrimSpace(a.Output))
	if refusal != "" {
		return errorResult(refusal)
	}
	overlayPath, refusal := tw.workspacePath("gen-catalog", "overlay", strings.TrimSpace(a.Overlay))
	if refusal != "" {
		return errorResult(refusal)
	}

	// The generation holds every entity it writes to the root the output directory
	// picks against this call's workspace, so a link beneath that directory cannot
	// carry an entity out of it.
	opts := gencatalog.Options{
		FromEmbedded:  a.FromEmbedded,
		PluginPath:    pluginPath,
		OutputDir:     outputDir,
		OverlayPath:   overlayPath,
		WorkspaceRoot: tw.root,
	}
	run := s.runGenCatalog
	if run == nil {
		run = gencatalog.Run
	}
	var stdout bytes.Buffer
	if err := run(ctx, opts, &stdout); err != nil {
		return tw.answer(errorResult(explainToolError("Catalog generation failed.", fmt.Errorf("%s", err.Error()), "plugin")))
	}
	out := tw.reportedText(stdout.String())
	return tw.answer(textResult(strings.TrimRight(out, "\n"), genCatalogResultPayload{Output: out}))
}
