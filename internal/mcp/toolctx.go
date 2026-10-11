// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
	"github.com/aggrega-ai/aggrega-cli/internal/serve"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
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
	argumentWorkspaceRoot = workspaceconfig.WorkspaceRootArgument
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

// signingKeyRefusal reports the corrective text for a call that selects a signing
// key held on the machine this server runs on, or the empty string when the call
// selects none and the tool may run.
//
// A caller reaching this server over its own streams launched it and is at this
// machine's keyboard, so a key on this disk is that caller's own key and naming it
// is naming their own file. A caller reaching a listener is somewhere else: the
// key would be the operator's, spendable by whoever connected, and a signature
// made with it cannot be recalled from anyone who has already fetched it. So the
// argument is refused for that caller rather than honoured for the one caller who
// cannot be its owner.
//
// It is decided from the arguments alone, before anything is resolved and before
// the tool runs, and it reads the names off the tool's own spec — the same list
// the listing withholds — so what a call is refused for sending and what a caller
// is offered cannot come to disagree.
//
// The refusal is loud rather than silent for the same reason the workspace one is:
// an argument quietly dropped reads to its sender exactly like one that was
// honoured, and here the sender would believe it had signed with a key it named.
func (s *server) signingKeyRefusal(spec toolSpec, args json.RawMessage) string {
	if s.transport != transportHTTP || len(spec.signingKeyArguments) == 0 || len(args) == 0 {
		return ""
	}
	var sent map[string]json.RawMessage
	if err := json.Unmarshal(args, &sent); err != nil {
		// The tool refuses a payload it cannot read, naming the field and the type
		// it wanted. That is the correction the caller needs, and a refusal composed
		// here would reach them first and say less.
		return ""
	}

	named := make([]string, 0, len(spec.signingKeyArguments))
	for _, name := range spec.signingKeyArguments {
		if argumentCarriesAValue(sent[name]) {
			named = append(named, "'"+name+"'")
		}
	}
	if len(named) == 0 {
		return ""
	}

	selected := strings.Join(named, " and ")
	return fmt.Sprintf("%s's %s selects a signing key held on the machine this server runs on, and a call "+
		"reaching this server over a listener cannot select one: the key would be this machine's rather "+
		"than yours, and a signature it made could not be recalled from anyone who fetched it. Remove %s "+
		"and send 'identity_token' instead, which signs as the identity the call itself carries.",
		spec.name, selected, selected)
}

// argumentCarriesAValue reports whether an argument a call sent selects anything.
// An absent argument, a null and a blank string select nothing, so a call that
// sent one of those has nothing to be refused for; any other value the caller
// wrote is a selection.
func argumentCarriesAValue(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text) != ""
	}
	return true
}

// notesWithheldFromAListener reports, in the order they are advertised, the
// parts of a tool's description that are true only for a caller that launched
// this process: the sentence offering the arguments that name a workspace of its
// own, and the sentence offering a signing key this machine holds.
//
// It is the ONE place those sentences are enumerated. The composition below reads
// it, and so does every assertion that holds one transport's answer against the
// other's, so a sentence is advertised and measured by being declared on the spec
// rather than by being remembered at each of those sites.
func (spec toolSpec) notesWithheldFromAListener() []string {
	notes := make([]string, 0, 2)
	for _, note := range []string{spec.workspaceSelectionNote, spec.signingKeyNote} {
		if note != "" {
			notes = append(notes, note)
		}
	}
	return notes
}

// advertisedDescription reports the description a tool is advertised under on
// the transport this session serves.
//
// A tool that says something about an argument a listener withholds says it only
// where a caller may send that argument. The description and the argument schema
// travel in the same answer, so a sentence offering an argument the schema beside
// it withholds would send a caller to compose a call that cannot succeed — the
// same defect the withholding exists to prevent, arriving in the prose instead of
// in the schema. It is composed here, beside the schema it has to agree with, so
// the two cannot be scoped in different places and come to disagree.
//
// A sentence is withheld rather than rewritten. What a caller reaching a listener
// reads is what every caller reads, minus the parts that are not true of it, so
// there is no second text to keep in step with the first.
//
// The one sentence added rather than withheld is the listener's own note, where a
// tool declares one: an answer over a listener keeps back locations a tool's
// description says it reports, and a caller reading that description would
// otherwise look for a value the answer marks as kept back. It is appended over a
// listener and nowhere else, so what a caller over the process's own streams
// reads is unchanged by it.
func (s *server) advertisedDescription(spec toolSpec) string {
	if s.transport == transportHTTP {
		if spec.listenerNote != "" {
			return spec.description + " " + spec.listenerNote
		}
		return spec.description
	}
	description := spec.description
	for _, note := range spec.notesWithheldFromAListener() {
		description += " " + note
	}
	return description
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
	return json.RawMessage(schemaWithoutProperties(spec.inputSchema, s.withheldArguments(spec)))
}

// withheldArguments names every argument this transport keeps out of what a tool
// advertises: the ones that name a workspace, which any tool here may be sent, and
// the ones that select a key this machine holds, which only a tool that signs
// declares.
//
// It composes the list the call surface refuses rather than a second one written
// beside it, so a schema cannot come to offer an argument the call surface would
// refuse. The two names it draws from are each declared once — one on this file,
// one on the tool's own spec — and neither is copied here.
func (s *server) withheldArguments(spec toolSpec) []string {
	if len(spec.signingKeyArguments) == 0 {
		return workspaceSelectionArguments
	}
	withheld := make([]string, 0, len(workspaceSelectionArguments)+len(spec.signingKeyArguments))
	withheld = append(withheld, workspaceSelectionArguments...)
	return append(withheld, spec.signingKeyArguments...)
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
// (or, absent a root, the one the process was launched with, else the process
// working directory — resolutionInputs), and from the file the call is
// about where that moves the workspace (subjectWorkspace); the server holds no
// cached workspace, so two concurrent calls never share resolution state.
type toolWorkspace struct {
	rw       *workspaceconfig.ResolvedWorkspace
	root     string
	cfg      workspaceconfig.Config
	cacheDir string

	// dirs is the workspace's own catalog search stack, which is what an
	// operation concerning no one Release reads: a whole-catalog validation, and a
	// by-name description or the workspace's entity listing that names no Release.
	// A Release-scoped read asks catalogDirsFor instead, so a Release the
	// configuration gives its own directories is answered from them.
	dirs []string

	// named is the part of dirs somebody named, as the resolver returned it beside
	// dirs, handed to every reader of dirs so the workspace's conventional catalog
	// directory is read as named or not where the stack was built.
	named []string

	// flagDirs are the call's own catalog_dirs argument, kept so the stack for a
	// Release is resolved from the same argument the workspace stack was.
	flagDirs []string

	// transport is carried here rather than read from the server at each call
	// site, so the one helper that resolves a path argument has everything its
	// answer depends on and a tool cannot reach around it to a different rule.
	transport transportMode

	// held is set on the workspace a listing resolved where that workspace's
	// configuration is held — a configuration file its cascade merges is a link
	// leading out of it — and is nil everywhere else. Such a workspace carries no
	// configuration: rw is nil, cfg is the zero value and no catalog directory is
	// read. Only resolveListingWorkspace builds one, for the tools whose operation
	// lists what a workspace holds.
	held *workspaceconfig.HeldWorkspace
}

// resolutionInputs reports what a call's workspace resolution is handed — where
// it begins, and the root it pins — from the root the call names for itself
// (rootArg) and the root this process was launched with (launchRoot).
//
// A root, from either, is the workspace root itself. It is handed as both, so
// the resolution takes it as given: nothing above it is searched for an
// enclosing workspace, and the cascade reads that root's own configuration file
// alone. That is what --workspace-root and AGGREGA_WORKSPACE_ROOT mean on every
// command, and it is the root a generated command tool's command is handed for a
// call naming none (servedRootFlag), so a server's own tools and its command
// tools answer such a call in one workspace. The call's own root wins over the
// launch's. With neither, both are empty and the resolution discovers the
// workspace from the process working directory.
//
// It is the one place that is decided. Every resolution made over the workspace
// this process serves — a call's, a listing's, and the listener's own reading
// of that workspace as it starts — takes its inputs from here, so none of them
// can come to treat a launch root as a folder to search upward from while the
// others take it as the root.
func resolutionInputs(launchRoot, rootArg string) (start, root string) {
	root = rootArg
	if root == "" {
		root = launchRoot
	}
	return root, root
}

// rootSourceOf names where the root resolutionInputs hands a call's resolution
// came from, for the refusal of a root that is not an existing folder
// (workspaceconfig.NameRootSource): the call's own argument where it sent one,
// else this server's launch. It reads the same two inputs in the same order, so
// the refusal names the input whose value was actually used.
//
// A launch root was refused at launch where it did not exist, so the second name
// is reached only by a folder removed while the server ran.
func rootSourceOf(rootArg string) string {
	if rootArg != "" {
		return "the call's '" + argumentWorkspaceRoot + "'"
	}
	return "this server's launch"
}

// resolveToolWorkspace resolves the workspace a tool call operates in: the root
// rootArg names, else the root this process was launched with, taken as the
// workspace root, else the workspace discovered from the process working
// directory (resolutionInputs). catalogDirs are the call's catalog search
// directories, prepended (absolute) to the workspace's configured stack, which
// in turn is followed by the conventional `catalog` directory at the workspace
// root when that directory exists. A workspace that declares an empty
// `catalog_dirs` resolves no local catalog at all, and a call against it reads
// only what the cache and the lockfile already carry. A call about one Release
// reads that Release's stack through catalogDirsFor, which the workspace
// configuration may give directories of its own.
//
// The command line the process was launched with is applied to the resolution,
// here and nowhere else: the global configuration file it names with --config
// is the one the cascade's global layer is read from, and every setting it
// carries — the offline posture above all — is laid on top. So that is what
// every tool, and the documentation acquisition at the start of the run, reads
// and reaches the network through, and a setting's source is reported as the
// command line reports it. It applies over a workspace a call names for itself
// as much as over the one the process was started in, because a flag is a
// property of the invocation rather than of a workspace.
func (s *server) resolveToolWorkspace(rootArg string, catalogDirs []string) (*toolWorkspace, error) {
	start, root := resolutionInputs(s.launchRoot, rootArg)
	rw, err := workspaceconfig.ResolveWithFlags(start, root, s.flags)
	if err != nil {
		return nil, toolWorkspaceRefusal(workspaceconfig.NameRootSource(err, rootSourceOf(rootArg)))
	}
	return s.toolWorkspaceOf(rw, catalogDirs), nil
}

// toolWorkspaceOf builds a call's workspace context from its resolution and its
// own catalog_dirs argument. It is the one composition of that context, so a
// listing resolving a workspace that is not held is answered from exactly the
// context every other tool is.
func (s *server) toolWorkspaceOf(rw *workspaceconfig.ResolvedWorkspace, catalogDirs []string) *toolWorkspace {
	cfg := rw.Config()
	return &toolWorkspace{
		rw:        rw,
		root:      rw.Root(),
		cfg:       cfg,
		cacheDir:  cfg.ResolvedArtifactCacheDir(),
		dirs:      rw.ResolveCatalogDirs("", catalogDirs),
		named:     rw.NamedCatalogDirs("", catalogDirs),
		flagDirs:  catalogDirs,
		transport: s.transport,
	}
}

// subjectWorkspace returns the workspace a call runs in once its subject is
// known: tw itself, except where the shared resolution finds the workspace again
// from the folder of the file the call is about
// (workspaceconfig.ResolvedWorkspace.ForSubject), in which case it is that
// workspace, built as every call's is. subject is the Release or document
// argument as the caller sent it, the same value handed to workspacePath, and
// the resolution alone decides whether it moves anything.
//
// A call resolves every path it names through tw, the workspace it resolved
// first, and asks this before that workspace is used for anything else. Where it
// moves the workspace, tw's root was the folder resolution started from, so a
// relative path among them is read there, as the command line reads a relative
// path in the folder it runs in; what the workspace decides — its root, its
// configuration, its catalog directories and its cache — is then the subject's.
//
// A held view carries no resolution to ask, and is returned as it is: the one
// operation answered over one refuses a call naming a Release before it gets
// here.
func (s *server) subjectWorkspace(tw *toolWorkspace, subject string) (*toolWorkspace, error) {
	if tw.rw == nil {
		return tw, nil
	}
	rw, err := tw.rw.ForSubject(subject)
	if err != nil {
		return nil, toolWorkspaceRefusal(err)
	}
	if rw == tw.rw {
		return tw, nil
	}
	return s.toolWorkspaceOf(rw, tw.flagDirs), nil
}

// toolWorkspaceRefusal frames a failure to resolve a call's workspace, so a call
// refused for a held configuration reads alike whichever resolution met it.
func toolWorkspaceRefusal(err error) error {
	return fmt.Errorf("resolve workspace: %w", err)
}

// resolveListingWorkspace resolves the workspace a call to the tool answering
// operation names, as resolveToolWorkspace does, with one difference that is the
// listing's own: where operation lists what a workspace holds
// (opsurface.AnswersOverHeldConfiguration) and the workspace's configuration is
// held — a configuration file its cascade merges is a link leading out of it —
// the call is answered from the held view rather than refused. That view carries
// no configuration and reads no catalog directory, and the listing names every
// held file among the files it did not read (namedUnread). For every other
// operation it is resolveToolWorkspace, held configuration refused.
//
// It is the language server's rule for the same two operations, read from the
// same declaration, so an assistant and an editor are answered alike over one
// workspace.
func (s *server) resolveListingWorkspace(operation, rootArg string, catalogDirs []string) (*toolWorkspace, error) {
	if !opsurface.AnswersOverHeldConfiguration(operation) {
		return s.resolveToolWorkspace(rootArg, catalogDirs)
	}
	start, root := resolutionInputs(s.launchRoot, rootArg)
	rw, held, err := workspaceconfig.ResolveOrHold(start, root, s.flags)
	if err != nil {
		return nil, toolWorkspaceRefusal(workspaceconfig.NameRootSource(err, rootSourceOf(rootArg)))
	}
	if held == nil {
		return s.toolWorkspaceOf(rw, catalogDirs), nil
	}
	return &toolWorkspace{root: held.Root(), flagDirs: catalogDirs, transport: s.transport, held: held}, nil
}

// heldRefusal is what a listing answers a request its held workspace cannot
// serve with — one in a Release's terms, or with catalog directories laid onto a
// stack the held configuration decides: the refusal every other tool answers the
// same workspace with.
func (tw *toolWorkspace) heldRefusal() error {
	return toolWorkspaceRefusal(tw.held.Refusal())
}

// namedUnread returns the files a listing names as not read: the links its walk
// named and, on a held workspace, every held configuration file the walk did not
// name already (workspaceconfig.HeldWorkspace.NamedWith). Never nil.
func (tw *toolWorkspace) namedUnread(walked []string) []string {
	if tw.held != nil {
		return tw.held.NamedWith(walked)
	}
	if walked == nil {
		return []string{}
	}
	return walked
}

// catalogDirsFor returns the catalog search stack for release, resolved through
// the one resolver every command and the daemon read a Release's directories
// through, from this call's own catalog_dirs argument. An empty release is the
// workspace's own stack.
func (tw *toolWorkspace) catalogDirsFor(release string) []string {
	if release == "" {
		return tw.dirs
	}
	return tw.rw.ResolveCatalogDirs(release, tw.flagDirs)
}

// namedCatalogDirsFor returns the part of catalogDirsFor's stack for release
// somebody named, asked of the resolver in the same terms. An empty release is
// the workspace's own.
func (tw *toolWorkspace) namedCatalogDirsFor(release string) []string {
	if release == "" {
		return tw.named
	}
	return tw.rw.NamedCatalogDirs(release, tw.flagDirs)
}

// absentCatalogDirLines returns one line for each directory the workspace
// configuration lists for release where nothing exists, or none where every listed
// directory exists. Those are the directories the stack catalogDirsFor returns
// leaves out, so the call reads them as holding nothing, and each line is what
// tells a caller so: the line `aggrega pull` and `aggrega build` print for that
// directory, without the indentation a terminal nests it under (diagnosticLines).
// An empty release is the workspace's own stack.
//
// The lines are the resolver's own (NoteAbsentCatalogDirs), asked for the same
// Release and this call's own catalog_dirs argument, exactly as catalogDirsFor
// asks the stack, so what they name and what the stack leaves out cannot come to
// disagree, and a line here reads exactly as the line a dependency resolution
// writes for the same directory.
func (tw *toolWorkspace) absentCatalogDirLines(release string) []string {
	var lines bytes.Buffer
	tw.rw.NoteAbsentCatalogDirs(&lines, release, tw.flagDirs)
	return diagnosticLines(lines.String())
}

// releaseReach reports which workspace documents an answer given in the terms of
// the Release at releasePath holds — for an answer that concerns no one Release by
// itself, such as an entity listing or a lookup by name — or the corrective text
// for a path that names no Release file. releasePath is the call's 'release'
// argument as workspacePath resolved it, and tool is the tool's advertised name, so
// a refusal names what to correct in the caller's own spelling.
//
// The corrective text is guidance in the shape every tool refusing a Release it
// cannot read gives it: what was refused, the reason the workspace gave, and the
// page teaching what a Release is.
//
// The reach is read with this call's own catalog_dirs argument, as newView reads
// the stack, so the documents an answer holds and the stack its names resolve
// through are one Release's.
func (tw *toolWorkspace) releaseReach(tool, releasePath string) (workspaceconfig.ReleaseReach, string) {
	reach, err := tw.rw.ReleaseReach(releasePath, tw.flagDirs)
	if err != nil {
		return workspaceconfig.ReleaseReach{}, explainToolError(
			fmt.Sprintf("%s's 'release' names no Release.", tool), err, "release")
	}
	return reach, ""
}

// newView builds the request-scoped catalog lookup for a tool call: a lazy
// lookup over the catalog search stack for release — the workspace's own where
// release is empty — with a nil puller, so the RENDER never networks — a cache
// miss is a loud halt rather than a registry fetch. When a lockfile is present
// its pins are threaded so every resolution is verified-on-read against the
// pinned digests, matching the non-daemon build front door.
//
// The nil puller bounds the render and not the call around it. A build asked to
// resolve its dependencies first does reach a registry, in a separate step that
// finishes before this view is built; the view it then renders through is this
// same non-networking one, held to the pins that step wrote. So the render is
// hermetic given a filled lockfile however the lockfile came to be filled, which
// is the same posture the command-line build holds after its own fill.
func (tw *toolWorkspace) newView(release string, lf *lockfile.Lockfile) *catalog.LazyLookup {
	v := catalog.NewLazyLookup(tw.root, tw.cacheDir, tw.catalogDirsFor(release), nil, tw.namedCatalogDirsFor(release)...)
	if lf != nil {
		v.SetPins(catalog.PinsFromLockfile(lf))
	}
	return v
}

// catalogDirsRefusal returns the refusal a whole-catalog load gives the first
// directory of the catalog search stack for release that it cannot walk — one the
// call's own catalog directories name and that does not exist, or one that exists
// and cannot be read, say — or nil where it can walk every one. A directory the
// workspace configuration lists and where nothing exists is not in the stack, so
// it is never refused here, as `aggrega build` never refuses it. release is
// resolved as newView resolves it, and the empty string is the workspace's own
// stack.
//
// A tool asks it where its command-line twin loads the whole catalog, and so
// refuses such a directory before it does anything else: the build, as `aggrega
// build` does, and the lookup of a catalog entity by name, as `aggrega inspect`
// does when it looks a name up. A lookup built by newView finds what it can and
// passes over a directory it cannot enter, so without the question a tool would
// answer over the directories that remain where its twin refuses. A tool whose
// twin reads through such a lookup too tolerates the directory as its twin does,
// and does not ask.
//
// The stack is read off a lookup built exactly as newView builds the call's own,
// so the directories checked are the directories resolved through, and the check
// is that lookup's own (CheckDirectories), which walks them by the walk the load
// runs and reads no document. Nothing here asks the filesystem itself: a second
// test of whether a directory exists would be free to answer differently from the
// load the twin runs.
func (tw *toolWorkspace) catalogDirsRefusal(release string) error {
	return tw.newView(release, nil).CheckDirectories()
}

// catalogDirsGuidance is the text a refusal catalogDirsRefusal returned is
// answered with: action, saying what the tool did not do, then the refusal with
// every cause beneath it on one line — the command line's report of the same
// directory: the directory, the load it stopped, and the reason the filesystem
// gave — and the page teaching where this server's catalog directories come from.
//
// The cause chain is rendered rather than the refusal's own text because a
// compiler error renders a single frame, which here would name the directory and
// the load and drop the reason beneath them.
func catalogDirsGuidance(action string, refusal error) string {
	return explainToolErrorAt(action, errors.New(cerrors.CauseChain(refusal)), docServerReference)
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
// serves and reaches nothing beyond it. The spelling is judged first by
// [pathscope.Resolve], the one home for whether a path written relative to a root
// stays under it; a path it accepts is then judged on disk by the judgement the
// language server holds every path a request names to, read or written
// ([serve.LeadsOutOfWorkspace]): the deepest part of the path that exists, with
// its links resolved, so a value spelled inside the root that leads out of it
// through a link is refused, and so is a directory not made yet beneath such a
// link. Nothing here re-derives either judgement, and the refusal below is this
// surface's own sentence attached to the verdict. A 'release' argument naming a
// Release the workspace configuration names is reached through the configuration
// and is let through, as the language server lets it through
// ([serve.ReleaseLeadsOutOfWorkspace]); no other argument has such an exception.
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
		leadsOut := serve.LeadsOutOfWorkspace(tw.root, resolved)
		if argument == "release" {
			leadsOut = serve.ReleaseLeadsOutOfWorkspace(tw.root, tw.cfg, resolved)
		}
		if !leadsOut {
			return resolved, ""
		}
		// The caller's own spelling is quoted back, as for a value climbing above
		// the root, and nothing is said of where the link leads.
		return "", fmt.Sprintf("%s's '%s' must name a path inside the workspace this server serves, written "+
			"relative to that workspace's root; %q leads outside that root through a link. This server serves one "+
			"workspace, fixed when it was started, and reads nothing beyond it.", tool, argument, raw)
	}
	// Neither refusal names the workspace root or any location the value resolves
	// to. Both are places on this host, and a caller reaching a listener is told
	// none of those by any answer here — a refusal included.
	//
	// A value that lands inside the root but was not written relative to it is a
	// caller spelling a location on this machine for a file it may perfectly well
	// read. It is answered with the spelling to use instead, because the
	// correction is one edit and withholding it would make a usable call read as
	// an out-of-bounds one. That spelling is the one this caller is told for the
	// location, so it is asked of the one function deciding what that is.
	if filepath.IsAbs(raw) {
		if inside, told := tw.reportedLocation(raw); told {
			return "", fmt.Sprintf("%s's '%s' names a location on the machine this server runs on. It serves one "+
				"workspace, fixed when it was started, and a call names its subject by a path relative to that "+
				"workspace's root: send %q instead.", tool, argument, inside)
		}
		return "", fmt.Sprintf("%s's '%s' must name a path inside the workspace this server serves, written "+
			"relative to that workspace's root; the value sent names a location on the machine this server runs "+
			"on, and not one inside that workspace. This server serves one workspace, fixed when it was started, "+
			"and reads nothing beyond it.", tool, argument)
	}
	// A relative value is refused only for climbing above the root, and it is the
	// caller's own spelling, so it is quoted back as sent: it names nothing on this
	// host that the caller did not write itself.
	return "", fmt.Sprintf("%s's '%s' must name a path inside the workspace this server serves, written "+
		"relative to that workspace's root; %q climbs above that root. This server serves one workspace, fixed "+
		"when it was started, and reads nothing beyond it.", tool, argument, raw)
}

// pathRefusal carries a refusal decided before an operation ran — a path this
// session may not name, or a workspace that could not be resolved, a workspace
// whose configuration is held among them — out of an operation that reports
// failures as errors rather than as corrective text.
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
// builds a view with no pins. A lockfile that is a link leading out of the root a
// write of it is held to is not unreadable but refused (lockfile.LoadIfReadable):
// it yields the refusal's guidance as the second result, and the caller answers
// with it rather than resolving without the pins the Release records.
//
// It reports the pins as they stand when it is called, and says nothing about
// how they got there. A build asked to resolve first — by its own argument or by
// the workspace's auto_pull setting — performs that resolution BEFORE calling
// this, so what it loads is the lockfile that fill just wrote. Every other
// caller here resolves against whatever pins already exist and fills nothing.
func (tw *toolWorkspace) loadReleaseLockfile(releasePath string) (*lockfile.Lockfile, string) {
	lf, err := lockfile.LoadIfReadable(tw.root, lockfile.PathFor(releasePath))
	if err != nil {
		return nil, explainToolError("Could not read the Release's lockfile.", err, "release")
	}
	return lf, ""
}
