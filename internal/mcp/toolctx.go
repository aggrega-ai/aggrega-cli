// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The two arguments a call uses to say which workspace it means: the root
// itself, and the catalog directories read beside it. They are spelled here as
// the caller spells them, and this is the one place they are, so the refusal
// below names what was sent rather than a second spelling of it.
const (
	argumentWorkspaceRoot = "workspace_root"
	argumentCatalogDirs   = "catalog_dirs"
)

// workspaceSelection is the slice of a tool's arguments that names a workspace,
// read before the tool reads its own arguments.
//
// It is deliberately not a second declaration of any tool's argument shape: it
// carries these two fields and no others, so a tool may add, rename or drop
// anything else without this reading changing, and a tool that takes neither
// argument simply decodes to the zero value.
type workspaceSelection struct {
	WorkspaceRoot string   `json:"workspace_root"`
	CatalogDirs   []string `json:"catalog_dirs"`
}

// workspaceSelectionRefusal reports the corrective text for a call that names a
// workspace this session does not let it name, or the empty string when the
// call names none and the tool may run.
//
// A caller reaching this server over its own streams launched it and is at this
// machine's keyboard, so it names the workspace it means and every one of these
// arguments is honoured. A caller reaching a listener is not, and the process
// serves the one workspace it was started over; naming another would let a
// caller read a directory the operator never offered.
//
// The refusal is loud rather than silent because an argument that is quietly
// dropped reads to its sender exactly like one that was honoured — the answer
// comes back computed somewhere else, with nothing in it saying so.
func (s *server) workspaceSelectionRefusal(args json.RawMessage) string {
	if s.transport != transportHTTP || len(args) == 0 {
		return ""
	}
	var sent workspaceSelection
	if err := json.Unmarshal(args, &sent); err != nil {
		// The tool refuses a payload it cannot read, naming the field and the type
		// it wanted. That is the correction the caller needs, and a refusal composed
		// here would reach them first and say less.
		return ""
	}

	named := make([]string, 0, 2)
	if sent.WorkspaceRoot != "" {
		named = append(named, argumentWorkspaceRoot)
	}
	if len(sent.CatalogDirs) > 0 {
		named = append(named, argumentCatalogDirs)
	}
	if len(named) == 0 {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "This server serves one workspace, fixed when it was started, so a call cannot name "+
		"another: remove %s from the arguments and call again. The answer is computed in the workspace "+
		"the server was started over.", strings.Join(named, " and "))
	if len(sent.CatalogDirs) > 0 {
		b.WriteString(" Catalog directories to search beside that workspace are its own configuration, " +
			"under workspace.catalog_dirs.")
	}
	return b.String()
}

// workspaceSelectionArguments is the set of arguments that name a workspace, as
// one list. The transport that refuses them also withholds them from what it
// advertises, and the two read the same names.
var workspaceSelectionArguments = []string{argumentWorkspaceRoot, argumentCatalogDirs}

// advertisedDescription reports the description a tool is advertised under on
// the transport this session serves.
//
// A tool that says something about naming a workspace says it only where a
// caller may name one. The description and the argument schema travel in the
// same answer, so a sentence offering an argument the schema beside it withholds
// would send a caller to compose a call that cannot succeed — the same defect
// the withholding exists to prevent, arriving in the prose instead of in the
// schema. It is composed here, beside the schema it has to agree with, so the
// two cannot be scoped in different places and come to disagree.
func (s *server) advertisedDescription(spec toolSpec) string {
	if spec.workspaceSelectionNote == "" || s.transport == transportHTTP {
		return spec.description
	}
	return spec.description + " " + spec.workspaceSelectionNote
}

// advertisedSchema reports the argument schema a tool is advertised under on the
// transport this session serves.
//
// A caller that may not name a workspace is not offered the arguments that name
// one. The schema is what a caller reads before it composes anything, so a
// schema offering a property whose call is guaranteed to be refused is the same
// defect the refusal exists to prevent, arriving one step earlier: it sends the
// caller to compose a call that cannot succeed.
func (s *server) advertisedSchema(spec toolSpec) json.RawMessage {
	if s.transport != transportHTTP {
		return json.RawMessage(spec.inputSchema)
	}
	return json.RawMessage(schemaWithoutProperties(spec.inputSchema, workspaceSelectionArguments))
}

// schemaWithoutProperties reports schema with the named properties removed from
// what it declares and from what it requires, so what comes back neither offers
// one of them nor demands one.
//
// It keeps the order of everything it does not remove, and hands back the
// schema's own bytes when it removes nothing, so a schema declaring none of the
// names is advertised exactly as authored. Order is kept rather than re-derived
// because the advertised bytes are a determinism contract: one session
// advertises the same bytes on every run, and an object rebuilt through an
// unordered map would not.
//
// It reads the object the schema itself is and does not descend into
// subschemas. A name a subschema buries is not one the arguments object
// declares, and rewriting a subschema would change which calls a tool accepts
// rather than which arguments it advertises.
//
// A schema it cannot read comes back unchanged. This removes what it can read,
// and rewriting a document it could not parse would be composing one.
func schemaWithoutProperties(schema string, names []string) string {
	var doc types.OrderedMap[json.RawMessage]
	if err := json.Unmarshal([]byte(schema), &doc); err != nil {
		return schema
	}

	removed := false
	if raw, ok := doc.Get("properties"); ok {
		var declared types.OrderedMap[json.RawMessage]
		if err := json.Unmarshal(raw, &declared); err != nil {
			return schema
		}
		dropped := false
		for _, name := range names {
			if declared.Delete(name) {
				dropped = true
			}
		}
		if dropped {
			encoded, err := json.Marshal(declared)
			if err != nil {
				return schema
			}
			doc.Set("properties", encoded)
			removed = true
		}
	}

	if raw, ok := doc.Get("required"); ok {
		var demanded []string
		if err := json.Unmarshal(raw, &demanded); err != nil {
			return schema
		}
		kept := make([]string, 0, len(demanded))
		for _, name := range demanded {
			if slices.Contains(names, name) {
				continue
			}
			kept = append(kept, name)
		}
		if len(kept) != len(demanded) {
			// An emptied list is dropped rather than advertised as an empty one:
			// requiring nothing and saying nothing about what is required are the
			// same constraint, and the shorter of the two is the one a reader can
			// act on.
			if len(kept) == 0 {
				doc.Delete("required")
			} else {
				encoded, err := json.Marshal(kept)
				if err != nil {
					return schema
				}
				doc.Set("required", encoded)
			}
			removed = true
		}
	}

	if !removed {
		return schema
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return schema
	}
	return string(out)
}

// toolWorkspace is the per-call resolved workspace context the closure-driven
// tools build their compiler-operations inputs from. Every field is derived
// once per tool call from the call's workspace_root and catalog_dirs arguments
// (or the process working directory when absent); the server holds no cached
// workspace, so two concurrent calls never share resolution state.
type toolWorkspace struct {
	rw       *workspaceconfig.ResolvedWorkspace
	root     string
	cfg      workspaceconfig.Config
	cacheDir string
	dirs     []string

	// transport is carried here rather than read from the server at each call
	// site, so the one helper that resolves a path argument has everything its
	// answer depends on and a tool cannot reach around it to a different rule.
	transport transportMode
}

// resolveToolWorkspace resolves the workspace a tool call operates in. An
// explicit rootArg pins the workspace root; otherwise resolution starts from
// the server's configured start directory, which is the process working
// directory in production. catalogDirs are the call's catalog search
// directories, prepended (absolute) to the workspace's configured stack; an
// empty result falls back to the workspace root so a catalog beside the release
// still resolves.
func (s *server) resolveToolWorkspace(rootArg string, catalogDirs []string) (*toolWorkspace, error) {
	start := rootArg
	flagRoot := rootArg
	if start == "" {
		start = s.startDir
	}
	rw, err := workspaceconfig.Resolve(start, flagRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	cfg := rw.Config()
	root := rw.Root()
	tw := &toolWorkspace{
		rw:        rw,
		root:      root,
		cfg:       cfg,
		cacheDir:  cfg.ResolvedArtifactCacheDir(),
		dirs:      workspaceconfig.ResolveCatalogDirs(catalogDirs, cfg, root),
		transport: s.transport,
	}
	return tw, nil
}

// newView builds the request-scoped catalog lookup for a tool call: a lazy
// lookup over the workspace's catalog search stack with a nil puller, so the
// RENDER never networks — a cache miss is a loud halt rather than a registry
// fetch. When a lockfile is present its pins are threaded so every resolution
// is verified-on-read against the pinned digests, matching the non-daemon build
// front door.
//
// The nil puller bounds the render and not the call around it. A build asked to
// resolve its dependencies first does reach a registry, in a separate step that
// finishes before this view is built; the view it then renders through is this
// same non-networking one, held to the pins that step wrote. So the render is
// hermetic given a filled lockfile however the lockfile came to be filled, which
// is the same posture the command-line build holds after its own fill.
func (tw *toolWorkspace) newView(lf *lockfile.Lockfile) *catalog.LazyLookup {
	v := catalog.NewLazyLookup(tw.root, tw.cacheDir, tw.dirs, nil)
	if lf != nil {
		v.SetPins(catalog.PinsFromLockfile(lf))
	}
	return v
}

// workspacePath resolves a path a tool call named, in the way the transport this
// session speaks allows, and reports the corrective text for a path that
// transport does not let the call name. An empty refusal means the resolved path
// may be used; a non-empty one means nothing was resolved and the refusal is the
// answer.
//
// It is the ONE home of that contract for every path argument on this surface —
// a Release, an entity document, a plugin executable, an artifact file, a batch
// file, a generated entity's output directory. Two rules meet here, and which one
// applies is a property of who is calling rather than of which argument is being
// resolved:
//
// A client that launched this process reaches it over the process's own streams
// and is at this machine's keyboard, so it names the workspace it means on the
// call itself. A path outside that root is something such a caller can address
// rather than something it has strayed into, and a rule that admitted an absolute
// Release while refusing an absolute entity would be one no caller could predict
// from the rest of this surface. So a path is taken as it is named, relative
// values against the workspace root.
//
// A caller reaching a listener is somewhere else entirely and does not own this
// machine, so it names its subject relative to the one workspace the process
// serves and reaches nothing beyond it. The containment judgement is
// [pathscope.Resolve]'s, which is the one home for whether a path stays inside a
// root; nothing here re-derives it, and the refusal below is this surface's own
// sentence attached to that verdict.
//
// tool and argument are the caller's own words for what is being resolved — the
// tool's advertised name and the argument's — so the refusal names what to
// correct in the spelling a caller sent, and so the guard over this package can
// hold the set of arguments that resolve a path to a hand-written inventory.
//
// The empty value resolves to the workspace root and is never refused, which is
// what a caller that omits an optional path argument has always been answered
// with. [pathscope.Resolve] refuses an empty relative path and leaves that
// meaning to the site holding it, which is here.
func (tw *toolWorkspace) workspacePath(tool, argument, raw string) (string, string) {
	rel := raw
	if rel == "" {
		rel = "."
	}
	if tw.transport != transportHTTP {
		if filepath.IsAbs(rel) {
			return filepath.Clean(rel), ""
		}
		return filepath.Join(tw.root, rel), ""
	}
	if resolved, ok := pathscope.Resolve(tw.root, rel); ok {
		return resolved, ""
	}
	// A value that lands inside the root but was not written relative to it is a
	// caller spelling a location on this machine for a file it may perfectly well
	// read. It is answered with the spelling to use instead, because the
	// correction is one edit and withholding it would make a usable call read as
	// an out-of-bounds one.
	if inside, ok := pathscope.Relative(tw.root, raw); ok {
		return "", fmt.Sprintf("%s's '%s' names a location on the machine this server runs on. It serves one "+
			"workspace, fixed when it was started (%s), and a call names its subject by a path relative to that "+
			"root: send %q instead.", tool, argument, tw.root, inside)
	}
	return "", fmt.Sprintf("%s's '%s' must name a path inside the workspace this server serves (%s), written "+
		"relative to that root; %s lies outside it. This server serves one workspace, fixed when it was started, "+
		"and reads nothing beyond it.", tool, argument, tw.root, pathscope.ReportedName(tw.root, raw))
}

// pathRefusal carries a path refusal out of an operation that reports failures as
// errors rather than as corrective text.
//
// It exists because the answer a caller gets must be the refusal's own sentence.
// An operation's error return is labelled at its call site with what that
// operation's failure means — a lint run's errors are reported as findings that
// halt publication — and a containment refusal wearing that label would tell the
// caller something untrue about what happened.
type pathRefusal struct{ text string }

func (e *pathRefusal) Error() string { return e.text }

// loadReleaseLockfile loads the per-Release lockfile beside releasePath,
// best-effort: an absent or unreadable lockfile yields nil, and the caller
// builds a view with no pins.
//
// It reports the pins as they stand when it is called, and says nothing about
// how they got there. A build asked to resolve first — by its own argument or by
// the workspace's auto_pull setting — performs that resolution BEFORE calling
// this, so what it loads is the lockfile that fill just wrote. Every other
// caller here resolves against whatever pins already exist and fills nothing.
func loadReleaseLockfile(releasePath string) *lockfile.Lockfile {
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		return nil
	}
	return lf
}
