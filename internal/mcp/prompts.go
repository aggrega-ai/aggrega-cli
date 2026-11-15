// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"encoding/json"
	"strings"

	schemas "github.com/aggrega-ai/aggrega-schemas"
)

// promptSpec is one prompt the server can render: its name, the description the
// client shows, the arguments it accepts, and the renderer that builds its
// messages. Every renderer emits pointers into the resource and tool surfaces —
// schema://, langref://, authoring://, and the tool names — rather than restating
// schema or documentation prose, so a prompt never duplicates content that lives
// in a resource.
type promptSpec struct {
	name        string
	description string
	arguments   []promptArgument
	render      func(args map[string]string) (getPromptResult, *rpcError)
}

// promptRegistry is the fixed prompt set, in advertised order: the authoring
// prompts first, then the workflow prompts. The three personas frame the
// collaborating authoring roles and the per-kind template focuses the same
// authoring loop on one entity kind, so all four answer "what am I writing"; the
// workflow prompts answer "which calls, in what order" for a goal that spans
// several operations. The order is fixed so the advertised list is byte-stable
// across runs.
//
// The workflow group is appended from its own slice rather than restated member
// by member, so the group has exactly one home.
//
// It is assembled in init rather than a package-level literal so each renderer
// can reference shared helpers in this file without an initializer-order
// constraint.
var promptRegistry []promptSpec

func init() {
	promptRegistry = append([]promptSpec{
		developerPromptSpec,
		architectPromptSpec,
		opsPromptSpec,
		authorEntityPromptSpec,
	}, workflowPromptSpecs...)
}

// fourDomainFraming states the collaborating-roles separation as guidance, not
// as a rule the compiler enforces. Every persona opens with it so an assistant
// keeps concerns clear without treating the split as a hard constraint.
const fourDomainFraming = "Aggrega separates authoring into four collaborating roles — developer intent, the platform catalog, infrastructure recipes, and deployment wiring — so each concern stays clear. The separation is guidance, not a rule the compiler enforces: you are free to collapse roles when a situation calls for it."

// kindArgument is the optional focus argument the personas accept and the
// required focus argument the per-kind template accepts.
func kindArgument(required bool) promptArgument {
	return promptArgument{
		Name:        "kind",
		Description: "The entity kind to focus on, as a lowercase-hyphenated token (for example component, feature, recipe, aggregate).",
		Required:    required,
	}
}

// resolveKind reads the kind argument, applying fallback when it is absent and
// validating it against the addressable schema set when present, so a persona
// or template always points at a schema:// resource that resolves. An unknown
// kind is an invalid-params error naming the valid set.
func resolveKind(args map[string]string, fallback string) (string, *rpcError) {
	kind := strings.TrimSpace(args["kind"])
	if kind == "" {
		return fallback, nil
	}
	if _, ok := schemas.SchemaByKind(kind); !ok {
		return "", &rpcError{Code: codeInvalidParams, Message: "unknown kind: " + kind + "; valid kinds: " + strings.Join(schemas.Kinds(), ", ")}
	}
	return kind, nil
}

// authoringPointers renders the shared closing block that points at the
// resources and tools an author consults for a kind. It names resource URIs and
// tool names; it never inlines their content.
func authoringPointers(b *strings.Builder, kind string) {
	b.WriteString("\n\nRead these resources before proposing YAML:\n")
	b.WriteString("- schema://" + kind + " — the exact structure, required fields, and value constraints.\n")
	b.WriteString("- langref://entities/" + kind + " — the field-by-field reference.\n")
	b.WriteString("- authoring://getting-started — the end-to-end authoring walkthrough.\n\n")
	b.WriteString("Then use the draft tool to start from a skeleton for " + kind + " carrying the fields it requires, fill in the fields your role owns, and run the validate tool before finishing: it names every placeholder still owed.")
}

// userPrompt wraps a rendered body as a single user-role message.
func userPrompt(description, body string) getPromptResult {
	return getPromptResult{
		Description: description,
		Messages: []promptMessage{
			{Role: "user", Content: promptContent{Type: "text", Text: body}},
		},
	}
}

var developerPromptSpec = promptSpec{
	name:        "developer-authoring",
	description: "Persona and method for a developer authoring application intent — an Aggregate. Points at the schema, reference, and guide resources rather than restating them.",
	arguments:   []promptArgument{kindArgument(false)},
	render: func(args map[string]string) (getPromptResult, *rpcError) {
		kind, rerr := resolveKind(args, "aggregate")
		if rerr != nil {
			return getPromptResult{}, rerr
		}
		var b strings.Builder
		b.WriteString("You are helping a developer capture application intent in Aggrega. A developer authors an Aggregate: what the application is and what it needs, expressed as intent, while physical infrastructure is left to the catalog and to operations.\n\n")
		b.WriteString(fourDomainFraming)
		authoringPointers(&b, kind)
		return userPrompt("Developer authoring persona for "+kind+".", b.String()), nil
	},
}

var architectPromptSpec = promptSpec{
	name:        "architect-authoring",
	description: "Persona and method for a platform architect authoring catalog entities — Components and Features. Points at the schema, reference, and guide resources rather than restating them.",
	arguments:   []promptArgument{kindArgument(false)},
	render: func(args map[string]string) (getPromptResult, *rpcError) {
		kind, rerr := resolveKind(args, "component")
		if rerr != nil {
			return getPromptResult{}, rerr
		}
		var b strings.Builder
		b.WriteString("You are helping a platform architect author catalog entities in Aggrega. An architect authors Components and Features: the reusable, versioned contracts developers consume, each declaring the values a developer supplies, the infrastructure operations sizes, and the outputs it provides to consumers.\n\n")
		b.WriteString(fourDomainFraming)
		authoringPointers(&b, kind)
		return userPrompt("Platform-architect authoring persona for "+kind+".", b.String()), nil
	},
}

var opsPromptSpec = promptSpec{
	name:        "ops-authoring",
	description: "Persona and method for platform operations authoring infrastructure recipes — Recipes and FeatureRecipes. Points at the schema, reference, and guide resources rather than restating them.",
	arguments:   []promptArgument{kindArgument(false)},
	render: func(args map[string]string) (getPromptResult, *rpcError) {
		kind, rerr := resolveKind(args, "recipe")
		if rerr != nil {
			return getPromptResult{}, rerr
		}
		var b strings.Builder
		b.WriteString("You are helping platform operations author a Recipe in Aggrega. Operations author Recipes and FeatureRecipes: the provider-specific translations that turn a catalog contract into rendered infrastructure, without changing the developer-facing values a Component declares.\n\n")
		b.WriteString(fourDomainFraming)
		authoringPointers(&b, kind)
		return userPrompt("Platform-operations authoring persona for "+kind+".", b.String()), nil
	},
}

var authorEntityPromptSpec = promptSpec{
	name:        "author-entity",
	description: "Focused authoring template for a single entity kind. Requires a kind argument and points at that kind's schema, reference, and the authoring guide, plus the draft and validate tools.",
	arguments:   []promptArgument{kindArgument(true)},
	render: func(args map[string]string) (getPromptResult, *rpcError) {
		if strings.TrimSpace(args["kind"]) == "" {
			return getPromptResult{}, &rpcError{Code: codeInvalidParams, Message: "author-entity requires a 'kind' argument; valid kinds: " + strings.Join(schemas.Kinds(), ", ")}
		}
		kind, rerr := resolveKind(args, "")
		if rerr != nil {
			return getPromptResult{}, rerr
		}
		var b strings.Builder
		b.WriteString("You are authoring an Aggrega " + kind + ".\n\n")
		b.WriteString(fourDomainFraming + " Keep each value in the pillar the " + kind + " owns and leave the others to their roles.")
		authoringPointers(&b, kind)
		return userPrompt("Authoring template for "+kind+".", b.String()), nil
	},
}

// handlePromptsList answers prompts/list with the fixed prompt set: each
// prompt's name, description, and arguments, in registry order.
func handlePromptsList() (any, *rpcError) {
	ps := make([]prompt, 0, len(promptRegistry))
	for i := range promptRegistry {
		ps = append(ps, prompt{
			Name:        promptRegistry[i].name,
			Description: promptRegistry[i].description,
			Arguments:   promptRegistry[i].arguments,
		})
	}
	return listPromptsResult{Prompts: ps}, nil
}

// handlePromptsGet renders a named prompt with its arguments. A missing or
// unknown name is an invalid-params error naming the valid set; a renderer's own
// argument validation surfaces its error.
func handlePromptsGet(params json.RawMessage) (any, *rpcError) {
	var p getPromptParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidRequestParams("prompts/get", err)
	}
	if p.Name == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "prompts/get requires a prompt name; available prompts: " + promptNames()}
	}
	spec, ok := lookupPrompt(p.Name)
	if !ok {
		return nil, &rpcError{Code: codeInvalidParams, Message: "unknown prompt: " + p.Name + "; available prompts: " + promptNames()}
	}
	res, rerr := spec.render(p.Arguments)
	if rerr != nil {
		return nil, rerr
	}
	return res, nil
}

// lookupPrompt resolves a prompt by name in the registry.
func lookupPrompt(name string) (*promptSpec, bool) {
	for i := range promptRegistry {
		if promptRegistry[i].name == name {
			return &promptRegistry[i], true
		}
	}
	return nil, false
}

// promptNames renders the prompt names as a comma-separated list for an error
// that must point the caller at the valid set.
func promptNames() string {
	names := make([]string, 0, len(promptRegistry))
	for i := range promptRegistry {
		names = append(names, promptRegistry[i].name)
	}
	return strings.Join(names, ", ")
}
