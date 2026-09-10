// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// Workflow prompts teach a high-level goal as the ordered sequence of real tool
// calls that reaches it. They are prompts rather than tools deliberately: each
// tool wraps one verifiable operation against the shared compiler-operations
// layer, so a goal that spans several operations is taught as a sequence the
// model executes rather than hidden inside a single call whose result no other
// surface could reproduce.
//
// Each body names tools and resource URIs and restates neither their argument
// schemas nor the documentation they point at, the same discipline the authoring
// prompts follow. The tool names are literal prose here because they sit inside
// sentences; a test holds every name in these bodies to the tool registry in both
// directions, so a rename cannot leave a prompt teaching a call a client cannot
// make.

// Workflow prompt names. They are referenced where a test holds each body's
// named tools to the registry and where it asserts that no tool answers to a
// goal name.
const (
	constructAggregatePromptName = "construct-aggregate"
	migrateDeploymentPromptName  = "migrate-existing-deployment"
	diagnoseBuildPromptName      = "diagnose-build-failure"
)

// workflowPromptSpecs is the workflow prompt set, in advertised order. The
// registry composes from this slice rather than repeating its members, so the
// group has one home; the order within it is fixed for the same reason the
// registry's is — the advertised list stays byte-stable across runs.
var workflowPromptSpecs = []promptSpec{
	constructAggregatePromptSpec,
	migrateDeploymentPromptSpec,
	diagnoseBuildPromptSpec,
}

// sequenceFraming opens every workflow prompt. It sets the expectation that the
// prompt teaches a sequence the model executes call by call, and that no single
// call performs the whole goal.
const sequenceFraming = "Work through the sequence below one tool call at a time, reading each result before making the next call. No single call does the whole job: each tool performs one operation you can check, and it is the order you make them in that gets you to the goal."

// workflowPointers is the shared closing block naming the resources a goal
// sequence draws on. It points at URI families rather than a per-kind list,
// because a goal sequence spans several kinds.
const workflowPointers = "For each kind you author, read schema://{kind} for its exact structure, langref://entities/{kind} for the field-by-field reference, and authoring://getting-started for the end-to-end walkthrough. The braces are placeholders: substitute the kind before reading."

var constructAggregatePromptSpec = promptSpec{
	name: constructAggregatePromptName,
	description: "Calling sequence for building a new Aggrega deployment from scratch, from reading a kind's contract " +
		"to reading the artifacts it renders. Names the tools to call in order and the decision points between them.",
	render: func(_ map[string]string) (getPromptResult, *rpcError) {
		return userPrompt(
			"Calling sequence for constructing a new deployment.",
			constructAggregateBody,
		), nil
	},
}

const constructAggregateBody = `You are constructing a new Aggrega deployment from scratch. ` + sequenceFraming + `

` + fourDomainFraming + ` Building a deployment from scratch usually means writing in more than one of them.

1. Learn what already exists. Call describe with a candidate component's type URI to read its full contract — the values a consumer supplies, the outputs it provides, and its labels — with inheritance applied. Once you have an entity's name, catalog://{kind}/{name} reads its authored YAML. Reuse a catalog Component wherever one fits and author a new one only where nothing does.
2. Read the contract before writing YAML. Call get-schema for each kind you are about to write, then scaffold to turn that schema into a starter skeleton carrying the required fields. Filling a skeleton beats writing from memory, because the skeleton is derived from the schema and so tracks the real contract.
3. Author the Aggregate. Declare which components the application has and how they are wired to each other. Expose every value that differs between environments as a parameter here: that declaration is what a Release is later able to fill in.
4. Author or select a Recipe per component, per target. A Recipe implements a Component's contract for one provider. Where a catalog Recipe already implements the component you chose, select it rather than writing one.
5. Author the Manifest. It binds each Component contract to the Recipe chosen for a target, which is how one Aggregate deploys to several environments unchanged.
6. Author the Release. It binds the Aggregate to a Manifest and fills in the parameters the Aggregate declared. A Release carries no values block of its own — the schema does not admit one — and it declares no components. Where a value has to differ per environment, the fix belongs in the Aggregate: expose it as a parameter there and fill it in from the Release.
7. Check each file as you finish it. Call validate with entity set to that file's path. Every kind is schema- and shape-checked, a Component or Feature has its pillar structure checked too, and a Recipe is driven through the real compilation pipeline around a synthesized minimal deployment so you see its isolated render summary.
8. Check the whole deployment. Call validate with release set to the Release path. It reads the Manifest and Aggregate the Release names, walks the Release's dependency closure, and runs the closure-scoped gates — without rendering or writing anything.
9. Read what it would emit. Call build with the Release path. It renders in memory and returns the artifacts by output-relative path, writing nothing to disk, so you can read the output before committing to it.

Decision points along the way.
- A diagnostic you do not understand: pass its exact text to explain-error, with the kind it concerns. It returns the diagnostic verbatim plus a pointer to the authoring contract for that kind, and where the diagnostic opens with a halting condition's short code, that condition and the published walk-through of the authoring fix as well. It does not guess at causes the diagnostic does not state.
- validate rejects a file you just wrote: fix that file and call validate on it alone before returning to the release-scoped check. The narrower scope names the problem more precisely.
- You do not know which entities a Release reaches: call describe with release set to the Release path for the closure summary, including which version of each entity it reaches.
- build renders, but an artifact is not what you expected: read the Recipe that produced it. Values arrive at a Recipe as strings, piped from the entity that declares them into the entity that consumes them without being interpreted, so the Recipe is where the shape of the output is decided.
- A value only exists once infrastructure is running — an address, a generated name, a credential. The compiler never reads live infrastructure while compiling, so carry it through as a deterministic pointer string, such as a stable DNS name or a template interpolation the target environment fills in later.

` + workflowPointers

var migrateDeploymentPromptSpec = promptSpec{
	name: migrateDeploymentPromptName,
	description: "Calling sequence for bringing an existing deployment description into Aggrega — a chart, an " +
		"infrastructure module, a container-compose file. States plainly which part no tool performs, and names the " +
		"tools that check the result and render it for comparison against the original.",
	render: func(_ map[string]string) (getPromptResult, *rpcError) {
		return userPrompt(
			"Calling sequence for bringing an existing deployment into Aggrega.",
			migrateDeploymentBody,
		), nil
	},
}

const migrateDeploymentBody = `You are bringing an existing deployment description — a chart, an infrastructure module, a container-compose file — into Aggrega.

Read this before you start: no tool on this surface translates one for you. The translation is yours to perform, by reading the source and authoring Aggrega entities from what you find. The tools below check your work at each point and show you what it renders to, which is what makes the translation verifiable rather than a guess.

Aggrega does ship a separate import path, on the command line rather than among these tools. It drafts authoring files from a source description, for the source formats an importer plugin attached to the workspace covers, and what it produces is draft files to review and complete — not a mapping, and not a score you can rank alternatives by. Read cli://import for what it accepts and what it needs attached before assuming it covers your source. For everything it does not cover, the sequence below is the path that exists.

` + fourDomainFraming + `

1. Read the source and write down what it actually deploys: each unit of compute, the configuration each one takes, and which units talk to which. This is the part no tool does, and everything after it rests on getting it right.
2. Look for catalog components that already cover those units. Call describe with a candidate's type URI to read its contract, and judge the fit against what you wrote down. A component that covers the unit is worth more than a faithful transcription of the original.
3. Decide per unit between reusing a catalog Component and authoring a new one. Reuse where the contract covers the unit's real inputs and outputs; author a new Component where the unit is specific to this application.
4. Author the Aggregate over the units you settled on, wiring them the way the source wires them. Every value that differed between the source's environments becomes a parameter the Aggregate declares.
5. Author or select a Recipe per component for the target you are deploying to, then the Manifest binding each Component contract to its chosen Recipe, then the Release binding the Aggregate to that Manifest and filling in the parameters. A Release carries no values block of its own; per-environment variation flows through those parameters.
6. Check as you go. Call validate with entity set to each file as you write it, then validate with release set to the Release path once the set is complete.
7. Compare against the original. Call build with the Release path and read the rendered artifacts against the source you started from. Every difference is either something the source did that you have not expressed yet, or something you changed on purpose. Decide which, one difference at a time.

Decision points along the way.
- Something in the source has no equivalent you can find: say so rather than approximating it. A requirement you have named as unexpressed is easier to deal with than a wrong one buried inside a value.
- The source obtained a value at deploy time — an address, a generated name, a credential. The compiler never authenticates with a provider, reads a cluster's state, or turns a secret into plaintext while compiling. Carry the value through as a deterministic pointer string, such as a stable DNS name or a template interpolation, which the target environment fills in later.
- The rendered output differs from the original in a way you did not intend: read the Recipe for the component that produced it. Values reach a Recipe as strings and are piped through without being interpreted, so the Recipe decides the shape of what is rendered.
- The source relies on a name meaning something: names carry no meaning here. The compiler does not read a purpose out of a component's name — a component named database-production is a component named database-production, nothing more — so express the intent in the wiring and the values, not in the naming.
- A diagnostic you do not understand: pass its exact text to explain-error, with the kind it concerns.

` + workflowPointers

var diagnoseBuildPromptSpec = promptSpec{
	name: diagnoseBuildPromptName,
	description: "Calling sequence for diagnosing a deployment that does not compile, or that compiles into the wrong " +
		"artifacts. Narrows the surface from the whole Release down to a single file, and says what the compiler does " +
		"and does not report.",
	render: func(_ map[string]string) (getPromptResult, *rpcError) {
		return userPrompt(
			"Calling sequence for diagnosing a failing or wrong build.",
			diagnoseBuildBody,
		), nil
	},
}

const diagnoseBuildBody = `You are diagnosing an Aggrega deployment that does not compile, or that compiles into artifacts that are not what was wanted. ` + sequenceFraming + ` Each call below narrows the surface the problem can be on, which converges faster than guessing at it.

1. Reproduce it in one call. Call validate with release set to the Release path. It checks correctness over the Release's dependency closure without rendering or writing anything, and returns each blocking diagnostic verbatim.
2. Start at the first diagnostic. The diagnostics come back in the order the checks produced them, so the first one is the one to read. Pass its exact text to explain-error, with the kind it concerns, for a pointer to the authoring contract behind it — and, where the diagnostic opens with a halting condition's short code, for that condition and the published walk-through of what to change.
3. Narrow to a file. Call validate with entity set to the path of the file the diagnostic names. A single entity is checked in isolation, so a failure there is in that file; a pass there puts the problem in how the file composes with the rest rather than in the file.
4. If the file checks out alone, look at the composition. Call describe with release set to the Release path to see the Aggregate and Manifest the Release binds and every catalog entity it reaches, with the version each one is at. An unexpected version, or a Recipe bound for a target you did not mean, is visible there.
5. Read a contract you assumed. Call describe with a component's type URI and compare the values it declares and the outputs it provides against what the Aggregate supplies and consumes. A value the Aggregate never supplies, or one the component never declared, shows up in that comparison.
6. If every check passes and the artifacts are still wrong, render and read. Call build with the Release path and read the returned files. build renders in memory and writes nothing, so it is safe to repeat as you change things.
7. Confirm the fix at the scope you started from. Call validate with release again, then build again, and read the artifacts against what you expected this time.

What the compiler reports, and what it leaves to you.
- It checks structure and composition. It does not police what a value means: a host name, a port number and an arbitrary token are all scalars, piped from the entity that declares them into the entity that consumes them and stringified at the injection boundary. A value that is well-formed and wrong produces no diagnostic.
- It does not read a purpose out of a name. A component named database-production is a component named database-production, nothing more, so a naming mistake surfaces as wiring that does the wrong thing rather than as an error.
- It never reads live infrastructure while compiling. A value that exists only once something is running travels as a deterministic pointer string, so a diagnostic about such a value concerns the pointer you wrote, not the running system.
- Given identical input it emits byte-identical output. So if the artifacts changed and you believe no input did, the useful question is which input changed.
- Provider-specific rendering and policy checks live in WebAssembly plugins rather than in the compiler, so a diagnostic about the shape of a rendered artifact points at the plugin that rendered it.

For the publishing rules over a single catalog entity — naming shape, category-matches-directory, annotation-prefix discipline — the lint tool reports over local content at warn severity. It is not in the default tool list: call describe-capabilities to load its definition, then call it by name.

` + workflowPointers
