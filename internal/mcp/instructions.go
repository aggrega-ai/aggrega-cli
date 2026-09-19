// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

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
// It points at the tool, prompt, and resource surfaces rather than restating
// them. Each tool already carries a rich description in its own spec and each
// entity kind's contract already lives in its schema and reference page, so
// naming a tool's job here and letting tools/list carry its argument schema
// keeps one home per statement. The same reasoning excludes a list of render
// targets and a list of library kinds: both are properties of the installed
// plugins and the schema set, which the resource surfaces enumerate directly.
const instructionsBody = `Aggrega is a deterministic ahead-of-time compiler. It reads declarative YAML describing an application and the infrastructure it needs, and emits rendered deployment artifacts — the concrete configuration files a target environment consumes. Given identical input it emits byte-identical output, so a change in the artifacts is always a change someone authored.

What the compiler does, and what it deliberately does not do. It parses YAML, walks the graph the documents describe, evaluates label matches, and pipes values from the entity that declares them into the entity that consumes them, stringified at the injection boundary regardless of what a value means. It is unopinionated about content: it does not police what you put in values, parameters, infra, or provides, and it does not guess a component's purpose from its name — a component named database-production is a component named database-production, nothing more. It is blind to the live cloud: while compiling it never authenticates with a provider, never reads a cluster's state, and never turns a secret into plaintext. A value that exists only once infrastructure is running is carried through as a deterministic pointer string — a stable DNS name, a template interpolation — which the target environment fills in later. Provider-specific rendering and policy checks live in WebAssembly plugins, not in the compiler itself.

The authoring vocabulary, and how the kinds relate. An Aggregate is the developer's blueprint: which components the application has and how they are wired together. A Component is a reusable catalog contract, declaring the values a consumer supplies and the outputs it provides. A Feature is a capability attached to a component or to a release. A Recipe implements a Component for one provider and a FeatureRecipe implements a Feature; each names what it implements. A Manifest is the mapping lockfile binding each Component contract to the Recipe chosen for a target, which is how one Aggregate deploys to several environments unchanged. A Release binds an Aggregate to a Manifest, and a Release is what you build. A Plugin pins a WebAssembly artifact by digest. Library kinds publish reusable fragments other entities import. The Aggregate stays the authoring surface for architecture: per-environment variation flows through parameters the Aggregate declares and the Release fills in, and a Release declares no values of its own — the schema does not admit them.

The authoring loop. Read the contract before writing YAML: get-schema returns a kind's exact shape, and scaffold turns that schema into a correct starter skeleton. Use describe to inspect a catalog Component before wiring it into an Aggregate, or to see every entity a Release reaches. Then validate checks correctness without rendering or writing anything, and build renders a Release in memory so you can read the artifacts it would produce before committing to them. For a diagnostic you do not understand, pass its exact text to explain-error: where the diagnostic opens with a halting condition's short code it returns that condition and the published walk-through of the authoring fix, the same expansion a terminal gets for the same code.

Choosing between a prompt and a tool. Reach for a tool to act — to check something, render something, or read a definition. Reach for a prompt to frame a task: prompts/list and prompts/get offer authoring prompts, which set up the perspective of the role whose entities you are writing and point at the resources for a kind, and workflow prompts, which lay out the ordered sequence of tool calls that reaches a larger goal — constructing a deployment from scratch, bringing an existing one into Aggrega, diagnosing one that fails or renders the wrong artifacts — with the decision points between the calls. A workflow prompt names the calls and you make them, one at a time; there is no single call that performs a whole goal, because each tool wraps one operation you can check.

Finding what is not in front of you. The default tool list is kept deliberately small, so it is not the whole surface. Call describe-capabilities to load the definitions it omits, or pass a kind to see the working set for the entity you are authoring; every tool it surfaces is invocable immediately by name. Not every server offers every capability — which ones a particular one offers can depend on how it was set up and on the credential the connection presented — so read what you can do here from the tool list and from describe-capabilities rather than assuming a tool is there because it exists elsewhere. For reading rather than acting, resources/list and resources/templates/list enumerate the material available: each kind's schema (schema://), the language reference (langref://), the authoring guide (authoring://), a catalog entity's authored YAML (catalog://), and command help (cli://).`

// emptyWorkspaceLead opens the closing paragraph and is the half of it that does
// not depend on the transport: what an empty listing means. What an assistant may
// then DO about it is the half that does, so the two are composed rather than
// written twice — a second copy of the meaning would agree on the day it was
// written and drift the first time either paragraph was edited alone.
const emptyWorkspaceLead = `What an empty workspace means, and what to do about it. An empty listing is an answer rather than a failure: when list-entities reports nothing, the workspace this server resolved holds no Aggrega documents it can read, and the catalog directories configured beside it declare nothing either. What you may do about that is not the same on both transports, so this server tells you which one you reached it on rather than leaving you to infer it.`

// emptyWorkspaceStdio closes the guidance for a client that launched this
// process. That client runs where the caller does, so the workspace and the
// configuration behind it are the caller's own to inspect and change, and the
// tools that do both are reachable here.
const emptyWorkspaceStdio = ` You reached it over standard input and output, which means the client running it is on the same machine you are: this workspace is yours to inspect and to repair. cli-config-show reports the configuration in force and where it came from, which is how you tell an unconfigured workspace from a merely empty one, and cli-config-init seeds a working configuration by registering a registry and its signature trust policy. Both are loaded through describe-capabilities rather than offered in the default list. Read the configuration before you change it, and tell the person you are working with what you changed.`

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
func (s *server) instructions() string {
	return instructionsFor(s.transport)
}
