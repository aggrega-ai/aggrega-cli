// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// instructionsBody is the part of the connect-time guidance that is the same
// whichever transport composed the session. It is a body rather than the whole
// answer: what a client receives is instructionsFor, which closes it with the
// paragraph that transport earns.
//
// It teaches the mental model an assistant cannot recover from tool schemas
// alone: what the compiler does and deliberately does not do, how the authoring
// kinds relate, the order the authoring loop runs in, and which surface answers
// which kind of question.
//
// It carries no index of the tools themselves, and that is the point rather than
// an omission: a client holds every tool's definition from tools/list before it
// reads a word of this, so a line per tool here would be a second copy of the
// purposes it already has — paid for in every session, by every client, and free
// to disagree with the registry the moment either was edited.
const instructionsBody = instructionsPrologue

// instructionsPrologue is the body's own text.
//
// It points at the tool, prompt, and resource surfaces rather than restating
// them. Each tool already carries a rich description in its own spec and each
// entity kind's contract already lives in its schema and reference page, so
// naming a tool's job here and letting tools/list carry its argument schema
// keeps one home per statement. The same reasoning excludes a list of render
// targets and a list of library kinds: both are properties of the installed
// plugins and the schema set, which the resource surfaces enumerate directly.
const instructionsPrologue = `Aggrega is a deterministic ahead-of-time compiler. It reads declarative YAML describing an application and the infrastructure it needs, and emits rendered deployment artifacts — the concrete configuration files a target environment consumes. Given identical input it emits byte-identical output, so a change in the artifacts is always a change someone authored.

What the compiler does, and what it deliberately does not do. It parses YAML, walks the graph the documents describe, evaluates label matches, and pipes values from the entity that declares them into the entity that consumes them, stringified at the injection boundary regardless of what a value means. It is unopinionated about content: it does not police what you put in values, parameters, infra, or provides, and it does not guess a component's purpose from its name — a component named database-production is a component named database-production, nothing more. It is blind to the live cloud: while compiling it never authenticates with a provider, never reads a cluster's state, and never turns a secret into plaintext. A value that exists only once infrastructure is running is carried through as a deterministic pointer string — a stable DNS name, a template interpolation — which the target environment fills in later. Provider-specific rendering and policy checks live in WebAssembly plugins, not in the compiler itself.

The authoring vocabulary, and how the kinds relate. An Aggregate is the developer's blueprint: which components the application has and how they are wired together. A Component is a reusable catalog contract, declaring the values a consumer supplies and the outputs it provides. A Feature is a capability attached to a component or to a release. A Recipe implements a Component for one provider and a FeatureRecipe implements a Feature; each names what it implements. A Manifest is the mapping lockfile binding each Component contract to the Recipe chosen for a target, which is how one Aggregate deploys to several environments unchanged. A Release binds an Aggregate to a Manifest, and a Release is what you build. A Plugin pins a WebAssembly artifact by digest. Library kinds publish reusable fragments other entities import. The Aggregate stays the authoring surface for architecture: per-environment variation flows through parameters the Aggregate declares and the Release fills in, and a Release declares no values of its own — the schema does not admit them.

The authoring loop. Read the contract before writing YAML: get-schema returns a kind's exact shape, and scaffold turns that schema into a starter skeleton carrying the fields the kind requires, with a placeholder wherever a value is still owed. Use describe to inspect a catalog Component before wiring it into an Aggregate, or to see every entity a Release reaches. Then validate checks correctness without rendering or writing anything, and build renders a Release in memory so you can read the artifacts it would produce before committing to them; sent write, build also saves them where aggrega build writes them, to dist/ at the workspace root. For a diagnostic you do not understand, pass its exact text to explain-error: where the diagnostic opens with a halting condition's short code it returns that condition and the published walk-through of the authoring fix, the same expansion a terminal gets for the same code.

Choosing between a prompt and a tool. Reach for a tool to act — to check something, render something, or read a definition. Reach for a prompt to frame a task: prompts/list and prompts/get offer authoring prompts, which set up the perspective of the role whose entities you are writing and point at the resources for a kind, and workflow prompts, which lay out the ordered sequence of tool calls that reaches a larger goal — constructing a composition from scratch, bringing an existing chart or infrastructure module into Aggrega, diagnosing a build that fails or renders the wrong artifacts — with the decision points between the calls. A workflow prompt names the calls and you make them, one at a time; there is no single call that performs a whole goal, because each tool wraps one operation you can check.

Finding what is not in front of you. The tool list is the whole surface: every tool this server offers is in it, which on a server you launched yourself includes one tool per aggrega command. Calling describe-capabilities with no arguments returns an index of every tool it offers — each one's name, a one-line purpose, the entity kinds it serves and whether it writes — and naming tools in that call returns their full definitions and the argument schemas you need in order to call them. What that tool is for is the narrowing: pass a kind for the working set for authoring that kind, which is read from the kinds each tool declares rather than from the words in its description. Every tool the index names is one this server has. Not every server offers every capability — which ones a particular one offers depends on how it was set up — so read what is here from the tool list and from describe-capabilities rather than assuming a tool is there because it exists elsewhere. Where your connection presented a credential, that credential decides which of those tools a call may run and never which of them are listed: a call it does not admit is refused naming the scope that would admit it, and the client you are working through can sign in again for that scope. For reading rather than acting, resources/list and resources/templates/list enumerate the material available: each kind's schema (schema://), the language reference (langref://), the authoring guide (authoring://), a catalog entity's authored YAML (catalog://), and command help (cli://).`

// emptyWorkspaceLead opens the closing paragraph and is the half of it that does
// not depend on the transport: what an empty listing means, and where the
// entities that would have filled it are read from. What an assistant may then DO
// about it is the half that does, so the two are composed rather than written
// twice — a second copy of the meaning would agree on the day it was written and
// drift the first time either paragraph was edited alone.
//
// Where entities are read from belongs in this half and not in either closing
// paragraph: it is the same on both transports, and a caller that may only report
// needs it as much as one that may repair — it is what turns "the listing was
// empty" into a report the person running the server can act on.
//
// The directory and the settings key are taken from the resolver's own exported
// spellings rather than written out here. They are the two things this paragraph
// asks an assistant to act on, so a second spelling would go on reading as
// correct after the resolver's had moved, and this surface would then send a
// caller to a directory nothing searches. It stops there deliberately: the
// precedence between the two, the command-line flag and the file cascade above
// them are the reference's to teach, and an assistant facing an empty workspace
// has none of those decisions in front of it.
var emptyWorkspaceLead = fmt.Sprintf(`What an empty workspace means, and what to do about it. An empty listing is an answer rather than a failure: when list-entities reports nothing, the workspace this server resolved holds no Aggrega documents it can read, and the catalog directories it searches declare nothing either. Those directories are the %[1]s directory at the workspace root, which is searched without being listed anywhere, and every directory listed under %[2]s in the workspace configuration — so an entity written under %[1]s is read with no configuration at all, while a workspace whose %[2]s is written as an empty list has declared that it holds no local catalog, which is a decision to respect rather than a gap to fill. What you may do about that is not the same on both transports, so this server tells you which one you reached it on rather than leaving you to infer it.`,
	workspaceconfig.ConventionalCatalogDir, workspaceconfig.CatalogDirsSetting)

// emptyWorkspaceStdio closes the guidance for a client that launched this
// process. That client runs where the caller does, so the workspace and the
// configuration behind it are the caller's own to inspect and change, and the
// tools that do both are reachable here.
//
// It says where a call naming no workspace is answered, because over these
// streams a call may name another and an empty answer is most often a call
// answered in the wrong folder. The order it states is resolutionInputs', and the
// last case is the one an answer says of itself (withNoWorkspaceNotice). The
// argument is read from its one spelling, so the sentence names what a call
// sends.
var emptyWorkspaceStdio = fmt.Sprintf(` You reached it over standard input and output, which means the client running it is on the same machine you are: this workspace is yours to inspect and to repair. A call that names no workspace is answered in the folder this server was launched with, where it was launched with one, and otherwise in the workspace found from the folder it was started in — that folder itself where it lies in no workspace, which such an answer then says; send %[1]s on a call to name another workspace. config-show reports the configuration in force and which layer decided each setting, which is how you tell an unconfigured workspace from a merely empty one, and cli-config-init seeds a working configuration by registering a registry and its signature trust policy. Read the configuration before you change it, and tell the person you are working with what you changed.`,
	argumentWorkspaceRoot)

// emptyWorkspaceHTTP closes the guidance for a caller that reached a listener.
// Such a caller is somewhere else, the workspace was chosen by whoever started
// the process, and the arguments that would point the server elsewhere are
// refused — so the honest instruction is to report rather than to configure.
const emptyWorkspaceHTTP = ` You reached it over HTTP, which means the workspace was chosen by whoever started this server and is fixed for the life of the process. You cannot point it somewhere else: a call naming a workspace or a catalog directory of its own is refused. Nor can you configure it — the tools that write configuration are offered on this transport to no caller, whatever its credential carries. So an empty listing here is yours to report rather than to repair: say which listing came back empty, and leave the workspace to the person who runs this server.`

// instructionsFor is the connect-time guidance a client reaches on the given
// transport, and the single home both protocol revisions answer from.
//
// Composing it here rather than at each result is what keeps the two revisions
// answering identically. They read one function, so a client learns the same
// things about this server whichever revision it speaks, and neither answer can
// drift from the other by one of them being taught something the other was not.
func instructionsFor(transport transportMode) string {
	if transport == transportHTTP {
		return instructionsBody + "\n\n" + emptyWorkspaceLead + emptyWorkspaceHTTP
	}
	return instructionsBody + "\n\n" + emptyWorkspaceLead + emptyWorkspaceStdio
}

// instructions is the guidance this session hands a connecting client, composed
// for the transport that session speaks.
//
// Where the desktop bundle that launched this process comes from a different
// release than the executable it launched, the guidance opens with the sentence
// saying so (releaseDifference), so the difference is in front of a model from
// its first exchange rather than only once it asks. Where there is no such
// difference — no bundle launched the server, or the two versions are equal —
// the guidance is exactly instructionsFor's. The launch fixes both versions, so
// every client reaching one process still reads one text.
func (s *server) instructions() string {
	guidance := instructionsFor(s.transport)
	if difference := releaseDifference(s.reportedVersions()); difference != "" {
		return difference + "\n\n" + guidance
	}
	return guidance
}
