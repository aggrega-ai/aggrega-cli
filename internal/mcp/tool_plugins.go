// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// The plugin reading answers which plugins are resolvable here and what each of
// them says about itself, as values rather than as a rendered table.
//
// It joins the configuration reading beside it, and the two answer one question
// between them. A capability that is absent and one that is merely not attached
// here look alike from every other surface: the configuration says which
// attachments exist, and this says whether those attachments reach a guest that
// resolves, describes, and serves what it claims to.
//
// The enumeration itself is not here. It is handed in from the layer that
// assembles the plugin resolution cascade, because that cascade's inputs are
// derived there — so the plugins this reports are the plugins the command line
// reports, by being the same walk rather than by two walks agreeing.

// pluginGroupEmbedded and pluginGroupConfigured name the two groups a plugin is
// reported in: the guests compiled into this binary, and the workspace's own
// bindings. They are written once because the listing's structure carries the
// grouping and this surface only has to name it.
const (
	pluginGroupEmbedded   = "embedded"
	pluginGroupConfigured = "configured"
)

// pluginEntryResult is one plugin on the wire: which group it was found in, the
// reference it was found under, where that reference was bound, and the guest's
// own self-report.
//
// URI and Error are pointers so that a value this answer is not carrying is
// distinguishable from an empty one, and each carries a mark beside it saying
// so. The reason is the same one the configuration reading's pointers carry: a
// caller that could not tell "this server is not telling you" from "there is
// nothing here" has been handed a worse answer than either.
//
// 🔴 Origin is NOT a place on the host and is reported whole on both transports.
// It is the config keys a socket binding was written at, the pinned digest an
// installed command extension carries, or nothing at all for a built-in. A
// config key is the same string on every host, and the ruling this reading
// applies names a digest as answered whole.
type pluginEntryResult struct {
	Group string `json:"group"`

	// URI is the reference this plugin was enumerated under, absent when that
	// reference is itself a place on this host's filesystem.
	URI *string `json:"uri"`

	// URIWithheld marks a reference kept back because it names a path, a
	// directory or a file on this host.
	URIWithheld bool `json:"uri_withheld,omitempty"`

	Origin       string   `json:"origin,omitempty"`
	Type         string   `json:"type,omitempty"`
	Version      string   `json:"version,omitempty"`
	Commit       string   `json:"commit,omitempty"`
	Capabilities []string `json:"capabilities"`
	InputKinds   []string `json:"input_kinds"`
	Boundary     string   `json:"boundary,omitempty"`
	Exports      []string `json:"exports"`
	Readiness    string   `json:"readiness,omitempty"`

	// Resolved says whether the reference reached module bytes at all. It is
	// reported on both transports and for every plugin, because it is the half
	// of a failure a caller can act on without being told where anything sits.
	Resolved bool `json:"resolved"`

	// Error is the resolve or describe failure this plugin raised, absent when
	// it raised none and absent when this answer keeps it back.
	Error *string `json:"error"`

	// ErrorWithheld marks a failure whose text was kept back. It is set only
	// where there was a failure, so it never says a plugin failed silently.
	ErrorWithheld bool `json:"error_withheld,omitempty"`
}

// pluginGroupCount is one group and how many plugins were found in it.
//
// It is the summary's whole content, and it is what a caller needs before it
// asks for records: whether this server reaches its built-ins at all, and
// whether this workspace binds anything of its own. The order is the order the
// groups are enumerated in, which is fixed, so the breakdown is byte-identical
// for identical input.
type pluginGroupCount struct {
	Group string `json:"group"`
	Count int    `json:"count"`
}

// pluginListResult is the plugin reading's structured payload: the workspace the
// answer was computed over, what that workspace reaches, and the plugin records
// this answer carries.
//
// Count, Offset, Returned and MoreRemaining carry what they carry on the entity
// listing, and for the same reasons; Groups is that listing's Kinds — the
// derived summary of the whole population, computed over every plugin rather
// than over the page. The answer is bounded because it grows with what an
// operator configures: the built-ins are fixed but the bindings are not, and a
// caller asking what is here cannot know how much of it there is until the
// answer arrives.
//
// HostLocationsWithheld states the rule that shaped this answer rather than
// leaving a caller to infer it from the marks, and it is always present for the
// reason its counterpart on the configuration reading is: an answer with no
// marks on it is otherwise indistinguishable from one where nothing was kept
// back.
//
// Every collection is always an array, empty rather than absent.
type pluginListResult struct {
	WorkspaceRoot         *string `json:"workspace_root"`
	WorkspaceRootWithheld bool    `json:"workspace_root_withheld,omitempty"`

	HostLocationsWithheld bool `json:"host_locations_withheld"`

	ResponseFormat string              `json:"response_format"`
	Count          int                 `json:"count"`
	Groups         []pluginGroupCount  `json:"groups"`
	Offset         int                 `json:"offset"`
	Returned       int                 `json:"returned"`
	MoreRemaining  bool                `json:"more_remaining"`
	Plugins        []pluginEntryResult `json:"plugins"`
}

var pluginListToolSpec = toolSpec{
	kinds:   []string{"plugin"},
	summary: "Reports the plugins resolvable here and what each one says about itself.",
	description: "Report the plugins this server can resolve and each one's own describe() metadata, reading " +
		"them and writing nothing. Two groups: the guests compiled into this binary, which resolve with no " +
		"filesystem, cache or network access, and the workspace's configured bindings. With no " +
		"`response_format` it answers with the summary alone — a count per group — so you learn how many " +
		"there are before asking for any. Send `response_format` detailed for the records: each plugin's " +
		"group, reference, where that reference was bound, its self-reported type, version, commit, served " +
		"capabilities, rendered input kinds, boundary assembly and exports, its readiness where it has one, " +
		"and whether it resolved, bounded to `limit` records from `offset` with `more_remaining` saying " +
		"whether to ask again. Use it with the configuration reading to tell a capability that is absent " +
		"from one that is attached but unusable: the configuration says what is attached, this says whether " +
		"the attachment reaches a guest that resolves and describes. A plugin that fails is reported with " +
		"its failure rather than dropped from the listing. A caller that did not launch this process is not " +
		"told where anything sits on the host it runs on — a reference that is itself a filesystem path is " +
		"absent and marked `uri_withheld`, and a failure's text is absent and marked `error_withheld`, " +
		"because a resolution failure names the places that were tried. `resolved` and `readiness` are " +
		"reported either way. A registry reference, a version, a commit, a digest, a capability and the " +
		"config key a binding was written at are not places on a host and are reported whole.",
	workspaceSelectionNote: "Optional `workspace_root` overrides discovery.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to the workspace root the server was launched with, or, where it was launched with none, to discovery from its working directory."
    },
    "response_format": {
      "type": "string",
      "enum": ["detailed", "concise"],
      "description": "concise returns the summary alone: a count per group. detailed returns the plugin records as well, one page of them. Defaults to concise, because how many plugins a workspace binds is not known before it is asked for."
    },
    "limit": {
      "type": "integer",
      "minimum": 1,
      "maximum": 500,
      "description": "How many plugin records one detailed answer carries. Defaults to 50. It bounds the records, so it has no effect on the concise summary, which carries none."
    },
    "offset": {
      "type": "integer",
      "minimum": 0,
      "description": "Which plugin record the detailed answer starts at, counting from 0 in the order the listing reports. Continue a listing by sending the previous answer's offset plus its returned, while its more_remaining is true."
    }
  }
}`,
	invoke: pluginListInvoke,
}

// pluginListArgs is the plugin reading's argument shape. It names no subject:
// which plugins are resolvable is a property of this binary and of the
// workspace, not of anything a caller would have to name first. The remaining
// arguments choose between the two shapes of the answer and, where it carries
// records, which of them — and they carry pointers for the reason the entity
// listing's do.
type pluginListArgs struct {
	WorkspaceRoot  string  `json:"workspace_root"`
	ResponseFormat *string `json:"response_format"`
	Limit          *int    `json:"limit"`
	Offset         *int    `json:"offset"`
}

func pluginListInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a pluginListArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments("plugin-list", err)
		}
	}
	// Both read before the workspace is resolved, so a call naming a shape or a
	// window this surface does not serve is answered about what it named rather
	// than about a workspace it never reached.
	format, refusal := parseResponseFormat("plugin-list", a.ResponseFormat, discoveryResponseFormatDefault)
	if refusal != "" {
		return errorResult(refusal)
	}
	asked, refusal := resolveListingWindow("plugin-list", a.Limit, a.Offset)
	if refusal != "" {
		return errorResult(refusal)
	}
	if s.listPlugins == nil {
		return errorResult("plugin-list: this server was started without the plugin enumeration the reading " +
			"is composed from, so there is nothing it can report. Answering with an empty listing would say " +
			"this workspace binds no plugins, which is a different statement and may not be true.")
	}
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, nil)
	if err != nil {
		return errorResult(explainToolError(
			"Could not resolve the workspace to report the plugins resolvable in it.", err, "plugin"))
	}

	payload := pluginListPayload(tw, s.listPlugins(ctx, tw.rw), format, asked)
	return tw.answer(textResult(renderPluginListText(payload), payload))
}

// pluginListPayload projects one enumeration onto this surface's shape, keeping
// back what this call keeps back and bounding the records to the window asked
// for.
//
// It is a function of the listing handed to it rather than of a workspace it
// enumerates, so the projection — which is where the whole of the keeping-back
// rule lives — can be driven over a supplied listing and checked exactly. A
// projection that had to enumerate in order to be read could only ever be
// compared against whatever plugins the machine happened to hold. tw supplies
// only the root and the transport, which is all the projection reads of it.
func pluginListPayload(tw *toolWorkspace, listing pluginops.PluginListing, format string,
	asked listingWindow) pluginListResult {

	withholdHostLocations := tw.withholdsHostLocations()
	out := pluginListResult{
		HostLocationsWithheld: withholdHostLocations,
		ResponseFormat:        format,
		Groups:                []pluginGroupCount{},
		Plugins:               []pluginEntryResult{},
	}

	// The workspace root is a directory on this host, so it answers to the rule
	// every location in every answer here answers to, and for the reason its
	// counterpart on the configuration reading does: over a listener there is
	// exactly one workspace in scope and the marker names a place the caller
	// cannot act on.
	out.WorkspaceRoot, out.WorkspaceRootWithheld = tw.reportedLocationField(tw.root)

	// One flat record list with the group on each record, and the breakdown
	// derived beside it. Two windowed lists would make a caller continue two
	// listings to read one answer, and the group is a property of each record
	// rather than of the page it arrived on.
	all := make([]pluginEntryResult, 0, len(listing.Embedded)+len(listing.Configured))
	for _, p := range listing.Embedded {
		all = append(all, pluginEntry(pluginGroupEmbedded, p, withholdHostLocations))
	}
	for _, p := range listing.Configured {
		all = append(all, pluginEntry(pluginGroupConfigured, p, withholdHostLocations))
	}
	out.Groups = append(out.Groups,
		pluginGroupCount{Group: pluginGroupEmbedded, Count: len(listing.Embedded)},
		pluginGroupCount{Group: pluginGroupConfigured, Count: len(listing.Configured)})

	from, to, more := windowFor(format == responseFormatDetailed, asked).bounds(len(all))
	out.Count = len(all)
	out.Offset = from
	out.Returned = to - from
	out.MoreRemaining = more
	out.Plugins = append(out.Plugins, all[from:to]...)
	return out
}

// pluginEntry projects one enumerated plugin onto the wire, keeping back what
// this call keeps back.
func pluginEntry(group string, p pluginops.ListedPlugin, withholdHostLocations bool) pluginEntryResult {
	entry := pluginEntryResult{
		Group:        group,
		Origin:       p.Origin,
		Readiness:    p.Readiness,
		Resolved:     p.Resolved(),
		Capabilities: []string{},
		InputKinds:   []string{},
		Exports:      []string{},
	}

	if withholdHostLocations && referenceNamesAHostLocation(p.URI) {
		entry.URIWithheld = true
	} else {
		entry.URI = stringPointer(p.URI)
	}

	if p.Err != nil {
		if withholdHostLocations {
			entry.ErrorWithheld = true
		} else {
			entry.Error = stringPointer(p.Err.Error())
		}
		return entry
	}

	entry.Type = p.Descriptor.PluginType
	if p.Descriptor.Metadata != nil {
		entry.Version = p.Descriptor.Metadata.Version
		entry.Commit = p.Descriptor.Metadata.Commit
	}
	entry.Capabilities = append(entry.Capabilities, p.Descriptor.Capabilities...)
	if p.Descriptor.Contract != nil {
		entry.InputKinds = append(entry.InputKinds, pluginInputKinds(p.Descriptor.Contract.InputKinds)...)
		entry.Boundary = p.Descriptor.Contract.BoundaryAssembly
	}
	for _, e := range p.Descriptor.Exports {
		entry.Exports = append(entry.Exports, e.Name)
	}
	return entry
}

// referenceNamesAHostLocation reports whether a plugin reference names a place
// on the host's filesystem.
//
// 🔴 The classification is read off the one function that decides it for every
// plugin socket, and never re-derived from the reference's text. That function
// calls itself a pure structural classifier and partitions every reference into
// three forms; the filesystem-path form is the one that names a place. A second
// does-this-look-like-a-path test written here would agree with it today and
// stop agreeing the moment either moved, silently, because nothing compares the
// two.
//
// 🔴 It is asked of the reference AS THE RESOLVED CONFIGURATION CARRIES IT, never
// of what an author typed, and the difference is not academic: the cascade
// anchors a relative reference on several sockets against the directory of the
// file that declared it, so a binding written as a bare name reaches this answer
// as an absolute path. Classifying what was authored would publish exactly those.
//
// The three admissible forms are not split any finer than the classifier splits
// them. A reference beginning "./" is a path on this host in the sense that
// matters here — measured, the cascade anchors it at the serving process's own
// working directory rather than at the workspace root, so it names somewhere a
// caller reaching a listener neither chose nor can see.
func referenceNamesAHostLocation(uri string) bool {
	return pluginpath.ClassifyReference(uri) == pluginpath.FormLocalPath
}

// A failure's TEXT is kept back whole over a listener, and that is a decision
// taken here rather than a classification read from somewhere.
//
// 🔴 Nothing classifies a diagnostic's content, and the measurement that settles
// it is that a resolution failure is COMPOSED from the places the cascade tried:
// a binding whose reference this answer reports as relative came back with a
// failure naming that reference's absolute anchoring, twice. So the text leaks a
// location the reference itself does not, and no test over the reference can
// predict which failures do.
//
// Splitting a resolve failure from a describe failure would keep back one class
// and publish the other, and a describe failure's text is composed by a guest
// this server did not write. Keyed on the field it cannot leak, and what it
// costs a remote caller is the reason a plugin is unusable rather than the fact
// of it: Resolved and Readiness are reported whole either way.

// pluginInputKinds renders a provider's input-kind claim in a fixed order, or
// nothing when it claims none.
func pluginInputKinds(k wasm.InputKinds) []string {
	var out []string
	if k.Sourced {
		out = append(out, "sourced")
	}
	if k.Synthesized {
		out = append(out, "synthesized")
	}
	if k.ArtifactsStandalone {
		out = append(out, "artifacts-standalone")
	}
	return out
}

// renderPluginListText renders the readable reading: a heading carrying the
// total, the breakdown of what that total is made of, then one line per plugin
// record this answer carries.
//
// The breakdown is printed in both shapes, for the reason the entity listing
// prints its own in both: it is what the summary IS, and on a page it is what
// says where that page sits in the whole.
//
// A kept-back value is written as a word rather than left blank, for the reason
// the structured half marks it: a blank cell and an absent value are the same
// characters, and a person reading this is making the same distinction a program
// reading the payload is.
func renderPluginListText(p pluginListResult) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Plugins resolvable in %s (%d):\n", workspaceRootWords(p.WorkspaceRoot), p.Count)
	for _, g := range p.Groups {
		fmt.Fprintf(&b, "  %s: %d\n", g.Group, g.Count)
	}

	if p.Returned == 0 && p.Count > 0 {
		fmt.Fprintf(&b, "\nCall again with response_format \"detailed\" for the plugin records themselves, "+
			"%d at a time unless you send a limit.", listingPageDefault)
		return strings.TrimRight(b.String(), "\n")
	}
	if p.Returned > 0 {
		fmt.Fprintf(&b, "\nRecords %d-%d of %d:\n", p.Offset+1, p.Offset+p.Returned, p.Count)
	}
	for _, e := range p.Plugins {
		fmt.Fprintf(&b, "  [%s] %s", e.Group, pluginReferenceWords(e))
		if e.Type != "" {
			fmt.Fprintf(&b, " — %s", e.Type)
		}
		if len(e.Capabilities) > 0 {
			fmt.Fprintf(&b, "; serves %s", strings.Join(e.Capabilities, ", "))
		}
		if e.Version != "" {
			fmt.Fprintf(&b, "; %s", e.Version)
		}
		if e.Readiness != "" {
			fmt.Fprintf(&b, "; %s", e.Readiness)
		}
		if !e.Resolved {
			b.WriteString("; did not resolve")
			if reason := pluginFailureWords(e); reason != "" {
				fmt.Fprintf(&b, ": %s", reason)
			}
		} else if e.Error != nil || e.ErrorWithheld {
			fmt.Fprintf(&b, "; resolved but did not describe: %s", pluginFailureWords(e))
		}
		b.WriteByte('\n')
	}
	if p.MoreRemaining {
		fmt.Fprintf(&b, "\nMore remain: call again with offset %d.", p.Offset+p.Returned)
	}
	if p.HostLocationsWithheld {
		b.WriteString("\nA reference naming a path, a directory or a file on this server's host is not " +
			"reported to a caller that did not start it, and neither is a failure's text, which names the " +
			"places that were tried. Whether each plugin resolved is reported either way.")
	}

	return strings.TrimRight(b.String(), "\n")
}

// pluginReferenceWords renders one plugin's reference for the readable listing.
func pluginReferenceWords(e pluginEntryResult) string {
	if e.URI == nil {
		return "(reference withheld)"
	}
	return *e.URI
}

// pluginFailureWords renders one plugin's failure for the readable listing: its
// text, the word saying that text was kept back, or nothing where the plugin
// raised no failure at all.
//
// "Withheld" is written only where the answer marks the failure as kept back. A
// record carrying no failure and no mark has nothing to keep back, and telling a
// reader a reason was withheld there would be a claim about this answer that is
// not true of it.
func pluginFailureWords(e pluginEntryResult) string {
	switch {
	case e.Error != nil:
		return *e.Error
	case e.ErrorWithheld:
		return "(reason withheld)"
	default:
		return ""
	}
}
