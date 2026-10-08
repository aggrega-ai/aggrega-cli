// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The configuration reading answers what is in force here and which layer
// decided each setting, as values rather than as a rendered table.
//
// It joins the inventories beside it for the reason those exist. The same
// capability is reachable, for a caller at this machine's keyboard, as the tool
// generated from its command, which returns that command's rendered output;
// that is the right answer for a person reading it and the wrong one for a
// program, which would have to parse a column layout back into the settings it
// was rendered from. This carries the settings, and renders the text from them.
//
// What it answers is also what decides whether a capability exists at all. A
// caller that cannot read the configuration cannot tell a provider, an importer
// or a governance check that is absent from one that is merely not attached in
// this workspace, and those two call for opposite next steps.

// settingsNamingAHostLocation names every setting whose VALUE names a place on
// the host's filesystem, against the reason it does.
//
// 🔴 It is hand-written because it IS the decision rather than a fact about the
// code. Nothing in the configuration package classifies a setting by whether its
// value is a location, so there is nothing to derive this from; deriving it from
// the values one server happens to hold would make it agree with whatever
// those values look like, which is the one thing it must not do. Adding an entry
// states that a setting's value names somewhere on the host and that keeping it
// from a remote caller was decided.
//
// 🔴 The unit of the decision is the SETTING, never the value, and the reason is
// that a setting's admissible forms are declared where a value's are not.
// Several of the entries below accept a location among other forms — a serving
// address is a host and port or a socket file; a plugin reference is a published
// identity, a capability, or a file on this disk. Telling those apart from the
// bytes would re-derive, badly, a judgement the resolution cascade already makes
// elsewhere: such a test would agree on most inputs, and the inputs it disagreed
// on would be exactly the ones that leak. Keyed on the setting, it cannot leak,
// and what it costs a remote caller is an address that caller already reached
// and a reference it is not being asked to resolve.
//
// It is read by lookup only and never ranged over, so no iteration order reaches
// an answer.
var settingsNamingAHostLocation = map[string]string{
	"build.authoring_release":     "a Release document, taken as written when it is absolute and resolved against the workspace root when it is not",
	"mcp.listen":                  "an address, which is either a host and port or a socket file on this disk",
	"mcp.tls.cert_file":           "the certificate chain file a listener presents",
	"mcp.tls.key_file":            "the private-key file for that certificate",
	"mcp.authorization.jwks_file": "the file a key set is read from",
	"mcp.health.listen":           "an address, in the two forms the serving address takes",
	"signing.key_path":            "a held private-key file",
	"workspace.catalog_dirs":      "the directories a workspace's catalog is searched in",
	"workspace.releases":          "each entry's key, the Release file it gives directories of its own, and those directories, all resolved to absolute paths on this disk",
	"plugins.governance":          "each entry's reference, which the configuration schema declares may be a file on this disk",
	"plugins.linter":              "each entry's reference, which the configuration schema declares may be a file on this disk",
	"plugins.importer":            "each entry's reference, which the configuration schema declares may be a file on this disk",
	"plugins.providers":           "each entry's reference, which the configuration schema declares may be a file on this disk",
}

// Three settings are deliberately absent from the set above, and each would
// keep back something different that is not a place on this host.
//
// The artifact cache directory reads like the plainest location of the lot and
// is not one: the configuration refuses an absolute value for it when the
// configuration loads, because every reader of that setting resolves it against
// the workspace root. So it is always relative to a root that is itself kept
// back, which leaves it naming nowhere — and it is the same kind of value as the
// workspace-relative paths the entity and Release listings already report on
// this transport. Keeping it back would withhold, from a caller that may read
// those listings, less than those listings already give it.
//
// The command-extension attachments carry a resolved, version-pinned artifact
// reference and the digest of the bytes it resolved to — the configuration
// declares no local-path form for them at all, unlike the four sockets above.
// A published reference is not a place on this host, and withholding one would
// keep back a fact that is the same wherever the artifact is read from.
//
// The configured registries carry hosts. A host is somewhere to reach, not
// somewhere on this disk, and a caller that cannot read which registries a
// workspace trusts cannot tell an entity that does not exist from one it is not
// configured to see.

// configSettingResult is one setting on the wire: its dotted name, its effective
// value, and the layer that determined it.
//
// Value and Source are pointers so that a value this answer is not carrying is
// distinguishable from an empty one. A setting nobody set carries the empty
// value its shape renders as; a value kept back carries none at all and the mark
// beside it says so. A caller that could not tell those two apart would have
// been handed a worse answer than either — "nobody configured a signing key" and
// "this server is not telling you where it is" call for opposite next steps.
//
// The marks are the answer's own statement rather than something a reader has to
// look up. They are omitted where nothing was kept back, so an answer over the
// process's own streams carries none of them at all and is the plain shape it
// has always been.
type configSettingResult struct {
	Field  string  `json:"field"`
	Value  *string `json:"value"`
	Source *string `json:"source"`

	// ValueWithheld marks a value kept back because it names a place on this
	// host's filesystem.
	ValueWithheld bool `json:"value_withheld,omitempty"`

	// SourceWithheld marks a layer kept back because that layer is a config
	// file and such a layer is named by that file's own path.
	SourceWithheld bool `json:"source_withheld,omitempty"`
}

// configLayerResult is one contributing layer: its name, or the mark saying that
// name was kept back.
//
// The entry survives even where its name does not, so the number of layers and
// their precedence order are the same answer on both transports. A kept-back
// layer dropped from the list instead would leave a caller unable to tell a
// workspace running on the built-in defaults from one whose whole cascade is
// invisible to it.
type configLayerResult struct {
	Name     *string `json:"name"`
	Withheld bool    `json:"withheld,omitempty"`
}

// configShowResult is the configuration reading's structured payload: the
// workspace the answer was computed over, the layers that contributed in
// precedence order lowest first, and every attributed setting.
//
// A Source on a setting is spelled exactly as the matching entry in Layers, so
// the two join directly; a setting the built-in defaults determined names no
// layer in Layers, because the defaults are not a layer that contributed. Where
// one side of that join is kept back so is the other, because the two are the
// same label read twice.
//
// HostLocationsWithheld states the rule that shaped this answer rather than
// leaving a caller to infer it from the marks. It is always present: a caller
// reading an answer with no marks on it needs to know whether that is because
// nothing was kept back or because nothing this server is configured with names a
// place on its host.
//
// Both collections are always arrays, empty rather than absent, so a caller
// iterates unconditionally.
type configShowResult struct {
	WorkspaceRoot         *string `json:"workspace_root"`
	WorkspaceRootWithheld bool    `json:"workspace_root_withheld,omitempty"`

	HostLocationsWithheld bool                  `json:"host_locations_withheld"`
	Layers                []configLayerResult   `json:"layers"`
	Settings              []configSettingResult `json:"settings"`
}

var configShowToolSpec = toolSpec{
	summary: "Reports the configuration in force and which layer decided each setting.",
	description: "Report the configuration in force for a workspace and which layer decided each setting, " +
		"reading configuration and writing nothing. Returns one entry per setting with its effective value and " +
		"the layer that determined it — a config file, an environment variable, a command-line flag, or the " +
		"built-in defaults — plus the layers that contributed in precedence order, lowest first. A setting's " +
		"`source` is spelled exactly as the matching entry in `layers`, so the two join directly, and a setting " +
		"the defaults decided names no layer at all because the defaults are not a layer that contributed. Use " +
		"it to tell a capability that is absent from one that is merely not configured here: whether a provider, " +
		"an importer or a governance check is attached is a setting, and the two cases call for opposite next " +
		"steps. A caller that did not launch this process is not told where anything sits on the host it runs " +
		"on — every value naming a path, a directory or a file there is absent and marked `value_withheld`, and " +
		"a layer that is a config file is marked `withheld` in place of its path. An absent value is always " +
		"marked, so it reads as kept back rather than as nothing configured; a setting nobody set carries the " +
		"empty value its shape renders as and no mark. `host_locations_withheld` says which of the two answers " +
		"you are reading. A registry reference, a version, a digest, a capability and the name of an " +
		"environment variable or a flag are not places on a host and are reported whole.",
	workspaceSelectionNote: "Optional `workspace_root` overrides discovery.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to discovery from the server's working directory."
    }
  }
}`,
	invoke: configShowInvoke,
}

// configShowArgs is the configuration reading's argument shape. It names no
// subject: which settings are in force is a property of the workspace and of the
// machine, not of anything a caller would have to name first.
//
// It carries no window either, and that is a measured property rather than an
// omission. A bounded listing exists where a caller cannot know how large an
// answer is before it arrives; here it can, because the settings are the fixed
// set this binary accounts for and every resolution reports every one of them.
// The answer's size is therefore a property of the binary rather than of
// anything an operator writes, and the layers beside them are bounded by the
// resolution cascade's own shape — the machine-wide layer, the walk up the
// workspace tree, the additive overlay, the environment table and the flag
// table. Neither grows with what an operator configures, so neither is what a
// window is for.
type configShowArgs struct {
	WorkspaceRoot string `json:"workspace_root"`
}

func configShowInvoke(s *server, _ context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a configShowArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments("config-show", err)
		}
	}
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, nil)
	if err != nil {
		return errorResult(explainToolError(
			"Could not resolve the workspace to report the configuration in force for it.", err, "component"))
	}

	origins, err := tw.rw.ValueOrigins()
	if err != nil {
		return errorResult(explainToolError(
			"Could not read which layer decided each setting of the configuration in force.", err, "component"))
	}

	payload := configShowPayload(tw, tw.rw.ContributingLayers(), origins)
	return tw.answer(textResult(renderConfigShowText(payload), payload))
}

// withholdsHostLocations reports whether this call's answer keeps back the values
// that name a place on this host's filesystem.
//
// A client that launched this process reaches it over the process's own streams
// and runs as the person who started it, so every location this answer could
// name is one that caller already owns and can go and look at. A caller reaching
// a listener is somewhere else: it did not choose where anything on this host
// sits, it cannot act on knowing, and a server that has published those
// locations to it cannot un-publish them.
//
// It is asked of the resolved workspace rather than of the server because that
// is where the transport this session speaks already travels, and one more
// reader of the server's own field would be a second place the same question is
// answered.
func (tw *toolWorkspace) withholdsHostLocations() bool {
	return tw.transport == transportHTTP
}

// configShowPayload projects a resolved workspace's provenance onto this
// surface's shape, keeping back what this call keeps back.
//
// It is a function of the layers and origins handed to it rather than of a
// workspace it resolves, so the projection — which is where the whole of the
// keeping-back rule lives — can be driven over a supplied cascade and checked
// exactly. A projection that had to resolve a workspace in order to be read
// could only ever be compared against whatever the machine happened to hold. tw
// supplies only the root and the transport, which is all the projection reads of
// it.
//
// Every collection is materialised as an empty slice rather than left nil, so a
// caller iterates unconditionally.
func configShowPayload(tw *toolWorkspace, layers []string, origins []workspaceconfig.ValueOrigin) configShowResult {
	withholdHostLocations := tw.withholdsHostLocations()
	out := configShowResult{
		HostLocationsWithheld: withholdHostLocations,
		Layers:                make([]configLayerResult, 0, len(layers)),
		Settings:              make([]configSettingResult, 0, len(origins)),
	}

	// The workspace root is a directory on this host, so it answers to the rule
	// every location in every answer here answers to: over a listener the root
	// itself is kept back, because there is exactly one workspace to be in scope —
	// the one the process was started over — and what the marker would say is a
	// location the caller cannot act on and does not need.
	out.WorkspaceRoot, out.WorkspaceRootWithheld = tw.reportedLocationField(tw.root)

	for _, layer := range layers {
		if withholdHostLocations && sourceNamesAHostLocation(layer) {
			out.Layers = append(out.Layers, configLayerResult{Withheld: true})
			continue
		}
		out.Layers = append(out.Layers, configLayerResult{Name: stringPointer(layer)})
	}

	for _, o := range origins {
		entry := configSettingResult{Field: o.Field}
		if withholdHostLocations && valueNamesAHostLocation(o) {
			entry.ValueWithheld = true
		} else {
			entry.Value = stringPointer(o.Value)
		}
		if withholdHostLocations && sourceNamesAHostLocation(o.Source) {
			entry.SourceWithheld = true
		} else {
			entry.Source = stringPointer(o.Source)
		}
		out.Settings = append(out.Settings, entry)
	}

	return out
}

// valueNamesAHostLocation reports whether this setting's effective value names a
// place on the host's filesystem.
//
// Two conditions, and the second is as load-bearing as the first. The setting has
// to be one whose value is a location, which the declared set above answers. And
// some layer of this server's own cascade has to have SET it: a setting no layer
// sets holds what this binary ships, so its value is the same on every host
// running this version and discloses nothing about this one.
//
// 🔴 That second condition is what keeps an unset setting distinguishable from a
// kept-back one. A reading that marked every location-bearing setting whatever it
// held would answer identically for a workspace that names a signing key and one
// that names none, which tells a caller nothing it can act on — and it would keep
// back the paths this binary itself defaults to, which are not this host's.
//
// It is answered through the classifier that produces the layer labels rather
// than by testing the value's own text, so it moves with that classification
// rather than agreeing with it until one of the two is edited.
func valueNamesAHostLocation(o workspaceconfig.ValueOrigin) bool {
	if _, declared := settingsNamingAHostLocation[o.Field]; !declared {
		return false
	}
	kind, _ := o.Layer()
	return kind != workspaceconfig.OriginBuiltInDefault
}

// sourceNamesAHostLocation reports whether a layer label names a place on the
// host's filesystem.
//
// A config-file layer is named by that file's absolute path; an environment
// override is named by the variable, a command-line flag by the flag as a reader
// types it, and the built-in defaults by nothing at all. None of those three is a
// place.
//
// 🔴 The classification is read off the one function that produces those labels
// and never re-derived from a label's text. A does-this-look-like-a-path test
// would agree with it today and stop agreeing the moment either spelling moved,
// silently, because nothing compares the two.
//
// It serves the label in both the places it appears. A setting's own source and
// an entry in the contributing-layer list are the same string produced by the
// same labelling, which is what lets a caller join the two — so a rule applied to
// one of them and not the other would publish, in the list, the very paths it
// kept out of the settings.
func sourceNamesAHostLocation(label string) bool {
	kind, _ := workspaceconfig.ValueOrigin{Source: label}.Layer()
	return kind == workspaceconfig.OriginConfigFile
}

// stringPointer returns a pointer to a copy of s, so each entry carries its own
// value rather than the loop variable's address.
func stringPointer(s string) *string { return &s }

// renderConfigShowText renders the readable reading: the layers that contributed,
// then one line per setting giving its value and where it came from.
//
// A kept-back value is written as a word rather than left blank, for the reason
// the structured half marks it: a blank column and an empty setting are the same
// characters, and a person reading this is making the same distinction a program
// reading the payload is.
func renderConfigShowText(p configShowResult) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Configuration in force for %s\n", workspaceRootWords(p.WorkspaceRoot))

	if len(p.Layers) == 0 {
		b.WriteString("\nLoaded from: the built-in defaults alone — no config file, environment override or " +
			"flag contributed.\n")
	} else {
		b.WriteString("\nLoaded from (lowest precedence first):\n")
		for i, layer := range p.Layers {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, layerWords(layer))
		}
	}

	b.WriteString("\nSettings:\n")
	for _, setting := range p.Settings {
		fmt.Fprintf(&b, "  %s = %s (from %s)\n",
			setting.Field, settingValueWords(setting), settingSourceWords(setting))
	}

	if p.HostLocationsWithheld {
		b.WriteString("\nValues naming a path, a directory or a file on this server's host are not reported " +
			"to a caller that did not start it. A setting shown as withheld has one; a setting shown with an " +
			"empty value has none.")
	}

	return strings.TrimRight(b.String(), "\n")
}

// layerWords renders one contributing layer for the readable listing.
func layerWords(layer configLayerResult) string {
	if layer.Name == nil {
		return "a config file (withheld)"
	}
	return *layer.Name
}

// settingValueWords renders one setting's value for the readable listing.
func settingValueWords(setting configSettingResult) string {
	if setting.Value == nil {
		return "(withheld)"
	}
	if *setting.Value == "" {
		return `""`
	}
	return *setting.Value
}

// settingSourceWords renders one setting's originating layer for the readable
// listing.
func settingSourceWords(setting configSettingResult) string {
	if setting.Source == nil {
		return "a config file (withheld)"
	}
	return *setting.Source
}
