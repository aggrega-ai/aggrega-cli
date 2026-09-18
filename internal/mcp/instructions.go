// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// serverInstructions is the guidance the initialize handshake hands a connecting
// client, which the client may surface to the model before its first call. It
// teaches the mental model an assistant cannot recover from tool schemas alone:
// what the compiler does and deliberately does not do, how the authoring kinds
// relate, the order the authoring loop runs in, and which surface answers which
// kind of question.
//
// It points at the tool, prompt, and resource surfaces rather than restating
// them. Each tool already carries a rich description in its own spec and each
// entity kind's contract already lives in its schema and reference page, so
// naming a tool's job here and letting tools/list carry its argument schema
// keeps one home per statement. The same reasoning excludes a list of render
// targets and a list of library kinds: both are properties of the installed
// plugins and the schema set, which the resource surfaces enumerate directly.
const serverInstructions = `Aggrega is a deterministic ahead-of-time compiler. It reads declarative YAML describing an application and the infrastructure it needs, and emits rendered deployment artifacts — the concrete configuration files a target environment consumes. Given identical input it emits byte-identical output, so a change in the artifacts is always a change someone authored.

What the compiler does, and what it deliberately does not do. It parses YAML, walks the graph the documents describe, evaluates label matches, and pipes values from the entity that declares them into the entity that consumes them, stringified at the injection boundary regardless of what a value means. It is unopinionated about content: it does not police what you put in values, parameters, infra, or provides, and it does not guess a component's purpose from its name — a component named database-production is a component named database-production, nothing more. It is blind to the live cloud: while compiling it never authenticates with a provider, never reads a cluster's state, and never turns a secret into plaintext. A value that exists only once infrastructure is running is carried through as a deterministic pointer string — a stable DNS name, a template interpolation — which the target environment fills in later. Provider-specific rendering and policy checks live in WebAssembly plugins, not in the compiler itself.

The authoring vocabulary, and how the kinds relate. An Aggregate is the developer's blueprint: which components the application has and how they are wired together. A Component is a reusable catalog contract, declaring the values a consumer supplies and the outputs it provides. A Feature is a capability attached to a component or to a release. A Recipe implements a Component for one provider and a FeatureRecipe implements a Feature; each names what it implements. A Manifest is the mapping lockfile binding each Component contract to the Recipe chosen for a target, which is how one Aggregate deploys to several environments unchanged. A Release binds an Aggregate to a Manifest, and a Release is what you build. A Plugin pins a WebAssembly artifact by digest. Library kinds publish reusable fragments other entities import. The Aggregate stays the authoring surface for architecture: per-environment variation flows through parameters the Aggregate declares and the Release fills in, and a Release declares no values of its own — the schema does not admit them.

The authoring loop. Read the contract before writing YAML: get-schema returns a kind's exact shape, and scaffold turns that schema into a correct starter skeleton. Use describe to inspect a catalog Component before wiring it into an Aggregate, or to see every entity a Release reaches. Then validate checks correctness without rendering or writing anything, and build renders a Release in memory so you can read the artifacts it would produce before committing to them. For a diagnostic you do not understand, pass its exact text to explain-error: where the diagnostic opens with a halting condition's short code it returns that condition and the published walk-through of the authoring fix, the same expansion a terminal gets for the same code.

Choosing between a prompt and a tool. Reach for a tool to act — to check something, render something, or read a definition. Reach for a prompt to frame a task: prompts/list and prompts/get offer authoring prompts, which set up the perspective of the role whose entities you are writing and point at the resources for a kind, and workflow prompts, which lay out the ordered sequence of tool calls that reaches a larger goal — constructing a deployment from scratch, bringing an existing one into Aggrega, diagnosing one that fails or renders the wrong artifacts — with the decision points between the calls. A workflow prompt names the calls and you make them, one at a time; there is no single call that performs a whole goal, because each tool wraps one operation you can check.

Finding what is not in front of you. The default tool list is kept deliberately small, so it is not the whole surface. Call describe-capabilities to load the definitions it omits, or pass a kind to see the working set for the entity you are authoring; every tool it surfaces is invocable immediately by name. Not every server offers every capability — which ones a particular one offers can depend on how it was set up and on the credential the connection presented — so read what you can do here from the tool list and from describe-capabilities rather than assuming a tool is there because it exists elsewhere. For reading rather than acting, resources/list and resources/templates/list enumerate the material available: each kind's schema (schema://), the language reference (langref://), the authoring guide (authoring://), a catalog entity's authored YAML (catalog://), and command help (cli://).`
