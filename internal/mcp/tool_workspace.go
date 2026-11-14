// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/serve"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The bound on a listing that returns records, and the page a call that names no
// bound is given.
//
// Neither of these tools takes a subject, so the caller cannot know how much a
// workspace holds until the answer arrives — which is what makes an unbounded
// listing different from a large one. A caller asking a tool about a subject it
// named has some idea of the size of the answer; a caller asking what is here has
// none, and a workspace holding a few thousand documents answers with megabytes
// before it has learned anything. The default is what a caller that asked for
// records and named no bound receives, and the maximum is what a caller that names
// one may ask for, so no call at all returns a listing the workspace's own size
// decides.
const (
	listingPageDefault = 50
	listingPageMax     = 500
)

// listingWindow is the span of records one call asks for: where in the listing to
// start, and how many to return from there.
//
// Both listings resolve and apply one, so the bound is one rule rather than a
// pair that agree today. A window with a limit of zero is the summary shape, which
// is how the summary comes out of the same arithmetic as a page rather than out of
// a second branch that could disagree with it about what remains.
type listingWindow struct {
	offset int
	limit  int
}

// resolveListingWindow reads the window a call asked for, and reports the
// correction to hand back when the call named one this surface does not serve.
//
// An absent limit is the default page rather than everything: a caller that asked
// for records without saying how many has not asked for the whole workspace, and
// answering as though it had is the unbounded answer this bound exists to remove.
// An out-of-range value is refused rather than clamped, because an argument
// silently adjusted reads to its sender exactly like one that was honoured.
func resolveListingWindow(tool string, limit, offset *int) (listingWindow, string) {
	window := listingWindow{limit: listingPageDefault}
	if limit != nil {
		if *limit < 1 || *limit > listingPageMax {
			return window, fmt.Sprintf("%s: `limit` is how many records one answer carries, and it must be "+
				"between 1 and %d; %d was sent. Send a value inside that range, or leave `limit` out for %d.",
				tool, listingPageMax, *limit, listingPageDefault)
		}
		window.limit = *limit
	}
	if offset != nil {
		if *offset < 0 {
			return window, fmt.Sprintf("%s: `offset` counts records from the start of the listing, so it cannot "+
				"be negative; %d was sent. Send 0 to start at the beginning, or the previous answer's `offset` "+
				"plus its `returned` to continue from where that answer stopped.", tool, *offset)
		}
		window.offset = *offset
	}
	return window, ""
}

// bounds reports the half-open span of a listing of the given size this window
// selects, and whether records remain after it.
//
// The remainder is computed from the span against the whole listing and is
// therefore not something a caller or a handler can set: an answer saying nothing
// remains while records do is the failure a bound introduces, and the only way to
// keep it out is to derive the signal rather than to assign it. An offset past the
// end yields an empty span and no remainder, which says what it means — there is
// nothing there — rather than refusing a caller that walked one page too far.
func (w listingWindow) bounds(total int) (from, to int, more bool) {
	from = w.offset
	if from > total {
		from = total
	}
	to = from + w.limit
	if to > total {
		to = total
	}
	return from, to, to < total
}

// windowFor returns the window a call's own arguments and response format select:
// the page the caller asked for where it asked for records, and the empty window
// where it asked for the summary.
//
// The summary shape carries no records, so it is the window of no records rather
// than a separate case. Reporting what remains then falls out of the same
// arithmetic every page uses, which is what makes the summary's own "there are
// records to ask for" signal the same fact a page reports.
func windowFor(detail bool, asked listingWindow) listingWindow {
	if !detail {
		return listingWindow{}
	}
	return asked
}

// entityKindCount is one kind a workspace holds and how many of its entities
// declare that kind.
//
// It is the answer to the question a caller arriving cold actually has — what is
// in here, and how much of it — which a first page of full records does not
// answer. A page says "here are some entities"; this says what the workspace is
// made of, which is what decides whether the caller wants any records at all and
// which of them.
type entityKindCount struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// entityKindCounts derives the per-kind breakdown from the entities themselves.
//
// Nothing here declares which kinds exist. A list of kinds written beside this
// would have to be remembered when a kind is added, and the breakdown would
// silently stop reporting the new one — a wrong answer that looks exactly like a
// right one, since every kind it does report is still correct. Reading the kinds
// out of the documents is the only form of this that cannot fall behind the
// documents.
//
// The order is the order the kinds first appear in the listing, which the listing
// itself fixes: it arrives ordered by kind, then name, then file, so the breakdown
// reads in the same order as the records it summarises and is byte-identical for
// identical input. The map is an index into the accumulating result and is never
// ranged over, so no iteration order reaches the answer.
func entityKindCounts(entities []serve.CatalogEntity) []entityKindCount {
	out := []entityKindCount{}
	at := map[string]int{}
	for _, e := range entities {
		if i, seen := at[e.Kind]; seen {
			out[i].Count++
			continue
		}
		at[e.Kind] = len(out)
		out = append(out, entityKindCount{Kind: e.Kind, Count: 1})
	}
	return out
}

// listEntitiesArgs is the list-entities tool's argument shape.
//
// There is no argument that selects a subject, and that is the point of the
// tool: it answers what the workspace holds, which is a property of the
// workspace rather than of anything a caller would have to name first. Two of the
// arguments only say which workspace, and both have defaults; one says in whose
// terms the answer is given, and is absent for the workspace's own; the rest
// choose between the two shapes of the answer and, where the answer carries
// records, which of them.
//
// Release, ResponseFormat, Limit and Offset are pointers so that a value a caller
// sent is distinguishable from one it left out. Zero is a limit this surface
// refuses and an offset it accepts, every response format outside the declared
// set is refused, the blank string among them, and a blank Release names none
// while an absent one asks for the workspace's own listing — so a plain value
// would make a refused one and an absent one the same bytes at exactly the point
// the two have to be told apart.
type listEntitiesArgs struct {
	WorkspaceRoot  string   `json:"workspace_root"`
	CatalogDirs    []string `json:"catalog_dirs"`
	Release        *string  `json:"release"`
	ResponseFormat *string  `json:"response_format"`
	Limit          *int     `json:"limit"`
	Offset         *int     `json:"offset"`
}

// listEntitiesResult is the list-entities tool's structured payload: the
// workspace the answer was computed over, what that workspace is made of, and the
// entity records this answer carries.
//
// The entities carry the shape a workspace entity is reported under, rather
// than a second description of one assembled here, so the identity a caller
// reads from this tool is the identity every other surface reports the same
// entity under.
//
// Count is the whole workspace and Returned is this answer, and they are separate
// fields because a bounded listing makes them different numbers. A single count
// would have to mean one or the other, and either reading leaves a caller unable to
// tell a workspace of fifty entities from the first fifty of two thousand.
//
// Kinds is derived from the whole listing rather than from the records this answer
// carries, so the breakdown and the total describe the same population however
// narrow the page is.
//
// MoreRemaining is computed from the window against that total. Together with
// Offset and Returned it is the whole of the continuation contract: while it is
// true, call again with Offset plus Returned. ResponseFormat echoes which shape
// came back, so a client reads the shape off the answer rather than off what it
// believes it asked for.
//
// Entities is always an array, empty when the workspace declares none and empty in
// the summary shape, so a client iterates without a null check.
//
// WorkspaceRoot is absent, and WorkspaceRootWithheld set, where the caller is not
// told where the workspace sits on this host; the mark is omitted otherwise, so an
// answer that keeps nothing back carries no trace of the rule.
//
// UnreadLinkedDirs and UnreadLinkedFiles name the links leading out of the
// workspace that the walk reading its documents met and did not read — to a
// directory outside the root, and named like a YAML file to a file outside it —
// the same lists the editor's entity index answer carries, from the same walk. No
// document behind such a link is read, so an entity whose document an editor's
// file tree shows under one is absent from Count and Entities as the workspace's
// own; these are what let a caller say why. Like Kinds they describe the whole
// workspace rather than the page, so both shapes carry them whole. Each name is
// the link's own place, relative to the root, and never the place it leads to, so
// it reads the same to a caller at this machine and to one reaching a listener.
// Each is always an array, empty where there is no such link.
type listEntitiesResult struct {
	WorkspaceRoot         *string `json:"workspace_root"`
	WorkspaceRootWithheld bool    `json:"workspace_root_withheld,omitempty"`

	ResponseFormat    string                `json:"response_format"`
	Count             int                   `json:"count"`
	Kinds             []entityKindCount     `json:"kinds"`
	Offset            int                   `json:"offset"`
	Returned          int                   `json:"returned"`
	MoreRemaining     bool                  `json:"more_remaining"`
	Entities          []serve.CatalogEntity `json:"entities"`
	UnreadLinkedDirs  []string              `json:"unread_linked_dirs"`
	UnreadLinkedFiles []string              `json:"unread_linked_files"`
}

var listEntitiesToolSpec = toolSpec{
	summary: "Summarises what a workspace holds, and reads its entities a page at a time.",
	description: "Summarise what a workspace holds, and read its entities a page at a time, reading the files and " +
		"writing nothing. With no `response_format` it answers with the summary alone — the total and a count per " +
		"kind, one row per kind, so it grows with how many kinds a workspace declares and not with how many " +
		"documents it holds. Send `response_format` detailed for the entity records themselves: kind, name, " +
		"apiVersion, description, labels and source file, read verbatim from the document that declares it, " +
		"bounded to `limit` records from `offset`, with `more_remaining` saying " +
		"whether to ask again. Call this first in an unfamiliar workspace: it takes no subject, so it is how you " +
		"obtain the names the other tools accept — describe, validate and build refuse a name they cannot resolve " +
		"rather than answering empty — and the summary tells you how much there is before you ask for any of it. A " +
		"document counts as an entity when it declares both a kind and a name and declares no apiVersion outside " +
		"Aggrega's own, so YAML declaring no kind — a chart's values file, a configuration file — is skipped, and " +
		"so is a document belonging to another project, such as a rendered Kubernetes manifest. Pair it with " +
		"list-releases and join the two on kind, name and file. Send `release` (a Release path, as list-releases " +
		"returns one) for the listing in that Release's terms: the Features the catalog directories the workspace " +
		"configuration gives that Release declare, and no document under a catalog directory only other Releases " +
		"read — so a workspace keeping variants of one entity side by side, one per Release, is listed with the " +
		"variant that Release builds with. `unread_linked_dirs` and `unread_linked_files`, in both shapes, name each " +
		"link the listing meets that leads out of the workspace — to a directory, which is not searched, and to a " +
		"YAML file other than a Release the workspace configuration names, which is not read — so a document an " +
		"editor shows behind one is not listed as the workspace's own. Where the workspace's own configuration " +
		"file, or the CI-owned overlay beside it, is a link leading out of the workspace, the configuration is not " +
		"read: `unread_linked_files` names that file, the listing is the workspace's own read with nothing " +
		"configured, `release` is refused, and so is every other tool over that workspace.",
	// The refusal of `catalog_dirs` over a held configuration is said here, not in
	// the description: it names an argument a listener does not offer, so it is
	// advertised only where that argument is.
	workspaceSelectionNote: "Optional `workspace_root` and `catalog_dirs` override discovery; `catalog_dirs` is " +
		"refused where the workspace's configuration is not read, as `release` is.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. ` + workspaceRootArgumentRule + `"
    },
    "catalog_dirs": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Optional catalog directories (absolute, or relative to the workspace root), prepended (first-match-wins) to the workspace catalog search stack. Features they declare are reported alongside the workspace's own."
    },
    "release": {
      "type": "string",
      "minLength": 1,
      "description": "Optional Release path. The listing is then given in that Release's terms: the Features its catalog directories declare, and every workspace document except those under a catalog directory only other Releases read. Absent, the listing is the workspace's own."
    },
    "response_format": {
      "type": "string",
      "enum": ["detailed", "concise"],
      "description": "concise returns the summary alone: the total and a count per kind. detailed returns the entity records as well, one page of them. Defaults to concise, because what a workspace holds is not known to be small before it is asked for."
    },
    "limit": {
      "type": "integer",
      "minimum": 1,
      "maximum": 500,
      "description": "How many entity records one detailed answer carries. Defaults to 50. It bounds the records, so it has no effect on the concise summary, which carries none."
    },
    "offset": {
      "type": "integer",
      "minimum": 0,
      "description": "Which entity record the detailed answer starts at, counting from 0 in the order the listing reports. Continue a listing by sending the previous answer's offset plus its returned, while its more_remaining is true."
    }
  }
}`,
	invoke: listEntitiesInvoke,
}

func listEntitiesInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a listEntitiesArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments("list-entities", err)
		}
	}
	// Both read before the workspace is resolved, so a call naming a shape or a
	// window this surface does not serve is answered about what it named rather
	// than about a workspace it never reached. The shape comes first because it
	// decides whether the answer carries records at all, and the window bounds
	// records.
	format, refusal := parseResponseFormat("list-entities", a.ResponseFormat, discoveryResponseFormatDefault)
	if refusal != "" {
		return errorResult(refusal)
	}
	asked, refusal := resolveListingWindow("list-entities", a.Limit, a.Offset)
	if refusal != "" {
		return errorResult(refusal)
	}
	if a.Release != nil && strings.TrimSpace(*a.Release) == "" {
		return errorResult("list-entities: `release` names the Release whose terms the listing is given in, and a " +
			"blank value names none. Send a Release path, as list-releases returns one, or leave `release` out for " +
			"the workspace's own listing.")
	}
	tw, err := s.resolveListingWorkspace("workspace-entity-index", a.WorkspaceRoot, a.CatalogDirs)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace to list its entities.", err, "component"))
	}
	// A workspace whose configuration is held is listed in its own terms with
	// nothing configured. A listing in a Release's terms, or with catalog
	// directories laid onto the configured stack, is decided by the configuration
	// the hold keeps unread, so it is refused as every other tool is refused on that
	// workspace — before anything is walked.
	if tw.held != nil && (a.Release != nil || len(a.CatalogDirs) > 0) {
		return tw.answer(errorResult(explainToolError("Could not resolve the workspace to list its entities.",
			tw.heldRefusal(), "component")))
	}

	// With no Release named, the listing is the workspace's, so it reads the
	// workspace's own stack and holds every document — the index the editor's
	// Catalog view reads when it names no Release either. With one named, the
	// stack and the documents held are that Release's, read through the same
	// projection the editor's view reads, so the two report one Release alike.
	var releasePath string
	var reach workspaceconfig.ReleaseReach
	if a.Release != nil {
		releasePath, refusal = tw.workspacePath("list-entities", "release", strings.TrimSpace(*a.Release))
		if refusal != "" {
			return tw.answer(errorResult(refusal))
		}
		if tw, err = s.subjectWorkspace(tw, strings.TrimSpace(*a.Release)); err != nil {
			return errorResult(explainToolError("Could not resolve the workspace to list its entities.", err, "component"))
		}
		reach, refusal = tw.releaseReach("list-entities", releasePath)
		if refusal != "" {
			return tw.answer(errorResult(refusal))
		}
	}
	index := serve.WorkspaceEntityIndex(ctx, tw.root, tw.cfg, tw.newView(releasePath, nil), reach)
	entities := index.Entities
	from, to, more := windowFor(format == responseFormatDetailed, asked).bounds(len(entities))
	payload := listEntitiesResult{
		ResponseFormat:    format,
		Count:             len(entities),
		Kinds:             entityKindCounts(entities),
		Offset:            from,
		Returned:          to - from,
		MoreRemaining:     more,
		Entities:          entities[from:to],
		UnreadLinkedDirs:  index.UnreadLinkedDirs,
		UnreadLinkedFiles: tw.namedUnread(index.UnreadLinkedFiles),
	}
	payload.WorkspaceRoot, payload.WorkspaceRootWithheld = tw.reportedLocationField(tw.root)
	return tw.answer(textResult(renderListEntitiesText(payload), payload))
}

// renderListEntitiesText renders the human-readable entity listing, closing with
// one line naming the links to directories outside the workspace the walk did not
// enter, and one naming the links to files outside it the walk did not read, each
// where there are any — in every shape, the empty listing included, since a
// workspace whose entities sit behind such links is exactly the one whose reader
// needs to be told why they are not listed.
func renderListEntitiesText(p listEntitiesResult) string {
	text := renderEntityRecordsText(p)
	if len(p.UnreadLinkedDirs) > 0 {
		text += "\n\nNo document inside these links is listed as the workspace's own, because each leads to a " +
			"directory outside the workspace and none is searched: " + strings.Join(p.UnreadLinkedDirs, ", ")
	}
	if len(p.UnreadLinkedFiles) > 0 {
		text += "\n\nNo document behind these links is listed as the workspace's own, because each leads to a file " +
			"outside the workspace and none is read: " + strings.Join(p.UnreadLinkedFiles, ", ")
	}
	return text
}

// renderEntityRecordsText renders the listing itself: a heading carrying the
// total, the breakdown of what that total is made of, then one line per entity
// record this answer carries giving the coordinates a caller addresses it by.
//
// The breakdown is printed in both shapes. It is what the summary is, and on a
// page it is what says where that page sits in the whole — a reader handed fifty
// records out of two thousand otherwise has to infer the shape of the rest from
// the fifty.
func renderEntityRecordsText(p listEntitiesResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Entities in %s (%d):\n", workspaceRootWords(p.WorkspaceRoot), p.Count)
	for _, k := range p.Kinds {
		fmt.Fprintf(&b, "  %s: %d\n", k.Kind, k.Count)
	}
	if p.Count == 0 {
		b.WriteString("  none — no Aggrega document under this root declares both a kind and a name\n")
	}
	if p.Returned == 0 && p.Count > 0 {
		fmt.Fprintf(&b, "\nCall again with response_format \"detailed\" for the entity records themselves, "+
			"%d at a time unless you send a limit.", listingPageDefault)
		return strings.TrimRight(b.String(), "\n")
	}
	if p.Returned > 0 {
		fmt.Fprintf(&b, "\nRecords %d-%d of %d:\n", p.Offset+1, p.Offset+p.Returned, p.Count)
	}
	for _, e := range p.Entities {
		fmt.Fprintf(&b, "  %s %s", e.Kind, e.Name)
		if e.File != "" {
			fmt.Fprintf(&b, " [%s]", e.File)
		} else {
			fmt.Fprintf(&b, " [%s]", e.Origin)
		}
		if strings.TrimSpace(e.Description) != "" {
			fmt.Fprintf(&b, ": %s", e.Description)
		}
		b.WriteByte('\n')
	}
	if len(p.Entities) > 0 {
		b.WriteString("\nLabels and apiVersion for each entity are in the structured result.")
	}
	if p.MoreRemaining {
		fmt.Fprintf(&b, "\nMore remain: call again with offset %d.", p.Offset+p.Returned)
	}
	return strings.TrimRight(b.String(), "\n")
}

// releaseDirectoryCount is one directory at the top of a workspace and how many
// of that workspace's Releases sit under it.
//
// It is the Release listing's answer to what is here and how much of it. A
// Release's only coordinate is its path, so where the Releases sit is the one thing
// about them that can be summarised without reading any of them — and it is the
// thing a caller needs, because a workspace's Releases cluster by the part of the
// tree they belong to. The directory is the first path segment rather than the
// whole directory: the whole one has as many values as there are trees holding a
// Release, which would make the summary as long as the listing it stands in for.
type releaseDirectoryCount struct {
	Directory string `json:"directory"`
	Count     int    `json:"count"`
}

// releaseDirectoryCounts derives the per-directory breakdown from the Release
// paths themselves. Nothing is declared about which directories a workspace has,
// so a workspace laid out in a way nobody anticipated is summarised as accurately
// as one that is not.
//
// A Release sitting at the workspace root is reported under ".", which is the
// spelling a reader already has for that directory — an empty string there would
// be indistinguishable from a value nothing filled in.
//
// The order is the order the directories first appear, and the paths arrive
// sorted, so the breakdown is in path order and is byte-identical for identical
// input. The map indexes the accumulating result and is never ranged over.
func releaseDirectoryCounts(releases []string) []releaseDirectoryCount {
	out := []releaseDirectoryCount{}
	at := map[string]int{}
	for _, r := range releases {
		dir := "."
		if i := strings.IndexByte(r, '/'); i >= 0 {
			dir = r[:i]
		}
		if i, seen := at[dir]; seen {
			out[i].Count++
			continue
		}
		at[dir] = len(out)
		out = append(out, releaseDirectoryCount{Directory: dir, Count: 1})
	}
	return out
}

// listReleasesArgs is the list-releases tool's argument shape. Like the entity
// listing it names no subject: which Releases a workspace holds is a property of
// the workspace. The remaining arguments choose between the two shapes of the
// answer and, where it carries paths, which of them — and they carry pointers for
// the reason the entity listing's do.
type listReleasesArgs struct {
	WorkspaceRoot  string  `json:"workspace_root"`
	ResponseFormat *string `json:"response_format"`
	Limit          *int    `json:"limit"`
	Offset         *int    `json:"offset"`
}

// listReleasesResult is the list-releases tool's structured payload: the
// workspace the answer was computed over, where its Releases sit, and the paths
// this answer carries, each reported under the same workspace-relative path the
// entity listing reports a file under so the two answers line up.
//
// Count, Offset, Returned and MoreRemaining carry what they carry on the entity
// listing, and for the same reasons; Directories is that listing's Kinds — the
// derived summary of the whole population, computed over every Release rather than
// over the page.
//
// Releases is always an array, empty when the workspace declares none and empty in
// the summary shape.
//
// Lockfiles maps each path Releases carries to the path of that Release's
// lockfile, in the same workspace-relative coordinate, computed by the rule the
// language server's Release answers use, so every surface names the same file.
// It covers the page rather than the whole listing: it describes the records
// this answer carries, so the summary shape carries an empty object and a
// page's map grows with the page and not with the workspace.
//
// UnreadLinkedDirs names each link the listing's walk met that leads to a
// directory outside the workspace root, where the link itself sits and in the
// order the walk met them — the walk that found Releases, so the two cannot
// disagree about what was read. The walk does not enter such a directory, so a
// Release inside one is absent from Count and Releases even though an editor's
// file tree shows it under the link; this is what lets a caller say why. Like
// Directories it describes the whole workspace rather than the page, so both
// shapes carry it whole, and it grows with the links leading out of the
// workspace rather than with the documents in it. It is always an array, empty
// where there is no such link.
//
// UnreadLinkedFiles names, beside it and the same way, each link the same walk met
// that leads to a YAML file outside the workspace root which the workspace's
// configuration does not name as a Release. The walk does not read such a file, so
// a Release behind one is absent from Count and Releases too; a link to a file the
// configuration names — by build.authoring_release, or as a key of
// workspace.releases — is read and listed instead, and is not named. It describes
// the whole workspace, as UnreadLinkedDirs does, and is always an array, empty
// where there is no such link.
//
// Each name is the link's own place, relative to the root, and never the place
// it leads to — the coordinate Releases is reported in, so it reads the same to a
// caller at this machine and to one reaching a listener, and nothing about the
// directory or file outside the workspace is told to either.
//
// WorkspaceRoot and its mark carry what they carry on the entity listing, for
// the same reason.
type listReleasesResult struct {
	WorkspaceRoot         *string `json:"workspace_root"`
	WorkspaceRootWithheld bool    `json:"workspace_root_withheld,omitempty"`

	ResponseFormat    string                  `json:"response_format"`
	Count             int                     `json:"count"`
	Directories       []releaseDirectoryCount `json:"directories"`
	Offset            int                     `json:"offset"`
	Returned          int                     `json:"returned"`
	MoreRemaining     bool                    `json:"more_remaining"`
	Releases          []string                `json:"releases"`
	Lockfiles         map[string]string       `json:"lockfiles"`
	UnreadLinkedDirs  []string                `json:"unread_linked_dirs"`
	UnreadLinkedFiles []string                `json:"unread_linked_files"`
}

var listReleasesToolSpec = toolSpec{
	kinds:   []string{"release"},
	summary: "Summarises the Releases a workspace holds, and reads their paths a page at a time.",
	description: "Summarise the Releases a workspace holds, and read their paths a page at a time, reading the " +
		"files and writing nothing. With no `response_format` it answers with the summary alone — the total and a " +
		"count per top-level directory — so you learn how many there are and where they sit before asking for any. " +
		"Send `response_format` detailed for the paths themselves, each relative to the workspace root — the " +
		"coordinate list-entities reports a file under, so the two answers line up — bounded to `limit` paths from " +
		"`offset`, with `more_remaining` saying whether to ask again. `lockfiles` maps each returned path to the " +
		"path of that Release's lockfile, in the same coordinate: the file pull writes and build reads, so you never " +
		"work out where it lives yourself. `unread_linked_dirs`, in both shapes, names each link the listing meets " +
		"that leads to a directory outside the workspace: no such directory is searched, so a Release inside one is " +
		"not listed even where an editor shows it under the link. `unread_linked_files`, in both shapes, names each " +
		"link the listing meets that leads to a YAML file outside the workspace which the workspace configuration " +
		"does not name as a Release: no such file is read, so a Release behind one is not listed; one the " +
		"configuration names is read and listed. Where the workspace's own configuration file, or the CI-owned " +
		"overlay beside it, is a link leading out of the workspace, the configuration is not read: " +
		"`unread_linked_files` names that file, the listing is read with nothing configured, and every tool " +
		"other than this one and list-entities is refused over that workspace. Membership is decided by the file's own kind " +
		"header, never by its name: a Release named without a conventional prefix is included, and a file merely " +
		"named like one is not. Call this when you need to name a Release and do not know which exist — build, " +
		"describe and validate each require one named and none can enumerate for you. Feed a returned path straight " +
		"to the `release` argument of describe or build.",
	workspaceSelectionNote: "Optional `workspace_root` overrides discovery.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. ` + workspaceRootArgumentRule + `"
    },
    "response_format": {
      "type": "string",
      "enum": ["detailed", "concise"],
      "description": "concise returns the summary alone: the total and a count per top-level directory. detailed returns the Release paths as well, one page of them. Defaults to concise, because how many Releases a workspace holds is not known before it is asked for."
    },
    "limit": {
      "type": "integer",
      "minimum": 1,
      "maximum": 500,
      "description": "How many Release paths one detailed answer carries. Defaults to 50. It bounds the paths, so it has no effect on the concise summary, which carries none."
    },
    "offset": {
      "type": "integer",
      "minimum": 0,
      "description": "Which Release path the detailed answer starts at, counting from 0 in the order the listing reports. Continue a listing by sending the previous answer's offset plus its returned, while its more_remaining is true."
    }
  }
}`,
	invoke: listReleasesInvoke,
}

func listReleasesInvoke(s *server, _ context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a listReleasesArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments("list-releases", err)
		}
	}
	format, refusal := parseResponseFormat("list-releases", a.ResponseFormat, discoveryResponseFormatDefault)
	if refusal != "" {
		return errorResult(refusal)
	}
	asked, refusal := resolveListingWindow("list-releases", a.Limit, a.Offset)
	if refusal != "" {
		return errorResult(refusal)
	}
	tw, err := s.resolveListingWorkspace("release-candidates", a.WorkspaceRoot, nil)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace to list its Releases.", err, "release"))
	}

	walked := serve.WalkReleaseCandidates(tw.root, tw.cfg)
	releases := walked.Releases
	from, to, more := windowFor(format == responseFormatDetailed, asked).bounds(len(releases))
	page := releases[from:to]
	payload := listReleasesResult{
		ResponseFormat:    format,
		Count:             len(releases),
		Directories:       releaseDirectoryCounts(releases),
		Offset:            from,
		Returned:          to - from,
		MoreRemaining:     more,
		Releases:          page,
		Lockfiles:         serve.LockfilesFor(page),
		UnreadLinkedDirs:  walked.UnreadLinkedDirs,
		UnreadLinkedFiles: tw.namedUnread(walked.UnreadLinkedFiles),
	}
	payload.WorkspaceRoot, payload.WorkspaceRootWithheld = tw.reportedLocationField(tw.root)
	return tw.answer(textResult(renderListReleasesText(payload), payload))
}

// renderListReleasesText renders the human-readable Release listing, closing
// with one line naming the links to directories outside the workspace the walk
// did not enter, and one naming the links to files outside it the walk did not
// read, each where there are any — in every shape, the empty listing included,
// since a workspace whose only Release sits behind such a link is exactly the one
// whose reader needs to be told why none is listed.
func renderListReleasesText(p listReleasesResult) string {
	text := renderReleasePathsText(p)
	if len(p.UnreadLinkedDirs) > 0 {
		text += "\n\nNo Release inside these links is listed, because each leads to a directory outside the " +
			"workspace and none is searched: " + strings.Join(p.UnreadLinkedDirs, ", ")
	}
	if len(p.UnreadLinkedFiles) > 0 {
		text += "\n\nNo Release behind these links is listed, because each leads to a file outside the workspace " +
			"that the workspace configuration does not name as a Release, and none is read: " +
			strings.Join(p.UnreadLinkedFiles, ", ")
	}
	return text
}

// renderReleasePathsText renders the listing itself, and says what an empty one
// means: the walk ran and found none, rather than the question having been
// refused. Each path is printed with its lockfile, read off the answer's own
// map, so the text and the structured result name the same file.
func renderReleasePathsText(p listReleasesResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Releases in %s (%d):\n", workspaceRootWords(p.WorkspaceRoot), p.Count)
	if p.Count == 0 {
		b.WriteString("  none — no document under this root declares kind: Release")
		return b.String()
	}
	for _, d := range p.Directories {
		fmt.Fprintf(&b, "  %s: %d\n", d.Directory, d.Count)
	}
	if p.Returned == 0 {
		fmt.Fprintf(&b, "\nCall again with response_format \"detailed\" for the paths themselves, %d at a time "+
			"unless you send a limit.", listingPageDefault)
		return strings.TrimRight(b.String(), "\n")
	}
	fmt.Fprintf(&b, "\nPaths %d-%d of %d:\n", p.Offset+1, p.Offset+p.Returned, p.Count)
	for _, r := range p.Releases {
		fmt.Fprintf(&b, "  %s (lockfile %s)\n", r, p.Lockfiles[r])
	}
	if p.MoreRemaining {
		fmt.Fprintf(&b, "\nMore remain: call again with offset %d.", p.Offset+p.Returned)
	}
	return strings.TrimRight(b.String(), "\n")
}
