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
		rw:       rw,
		root:     root,
		cfg:      cfg,
		cacheDir: cfg.ResolvedArtifactCacheDir(),
		dirs:     workspaceconfig.ResolveCatalogDirs(catalogDirs, cfg, root),
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

// absWorkspacePath resolves a path a tool call named to an absolute path. A
// relative path is taken against the workspace root, so a call can name its
// subject by a workspace-relative path or an absolute one interchangeably.
//
// It is the one home of that contract for every path argument on this surface —
// a Release, an entity document, a plugin executable, an artifact file, a batch
// file. The contract is deliberately the same for all of them: a caller here
// names the workspace it means on the call itself, so a path outside that root is
// something it can address rather than something it has strayed into, and a rule
// that admitted an absolute Release while refusing an absolute entity would be
// one no caller could predict from the rest of this surface.
func (tw *toolWorkspace) absWorkspacePath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(tw.root, p)
}

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
