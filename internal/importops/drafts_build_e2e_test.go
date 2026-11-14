// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// End-to-end proofs that what `aggrega import` drafts from each documentation
// example builds as the import wrote it. For every importer, the example's own
// source is imported for a target through the real Import, over a workspace
// whose catalog is the standard library's as the hub publishes it — the
// reference importers built from this repository's sources standing in for the
// published ones — and the drafted Release is built, with the Aggregate and the
// Manifest it names, through the build-faithful harness.
//
// Nothing the import wrote is edited, and no line it commented is uncommented.
// Where the import leaves values empty, because no source can hold them, the
// case does what the import asks of its user: it fills in the answers file the
// import wrote, and imports again with it, and the second import's drafts are the
// ones built. The values left empty are held to exactly the ones each case
// expects — by the import's own report and by a reading of the drafts written
// apart from it — so a draft that starts or stops owing a value fails here.
//
// These tests read the import examples of the aggrega-docs repository and the
// catalog of the aggrega-hub repository, each checked out beside this one.
// Without those checkouts they fail rather than skip.

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The answers the cases give a value the drafts leave for the user, by what the
// value is. None is a credential: a secret is answered with the name of the place
// its value is read from, never with a value.
const (
	// answerImage is the image an application built from code runs from.
	answerImage = "ghcr.io/example/agent:1.0.0"
	// answerSecret is the name a credential attached by reference is read from:
	// a Kubernetes Secret on helm, and the referenced name on compose.
	answerSecret = "example-secret"
	// answerModel is the model a model instance serves where no source names one.
	answerModel = "gpt-4o-mini"
	// answerOwner is the email address of a platform's owner account.
	answerOwner = "owner@example.com"
	// answerSecretKey is the name a Dify installation reads its signing key from.
	answerSecretKey = "example-credentials"
)

// asDraftedRelease prefixes a place in the drafted Release.
const asDraftedRelease = "release.yaml:spec."

// asDraftedCase is one documentation example imported for one target: the
// example's format, the source its walkthrough names to the import, and the
// values the drafts leave for the user, each with its answer, keyed as
// draftedScalars keys a place. check, where set, holds what the build rendered to
// what the source states.
type asDraftedCase struct {
	format, argument, target string
	owed                     map[string]string
	check                    func(t *testing.T, artifacts map[string][]byte)
}

// asDraftedCases are the ten documentation examples, each imported for the
// compose target and for the helm target, with the source argument each
// example's walkthrough runs the import with.
//
// On helm a credential is attached by reference to a Kubernetes Secret, whose
// name no source holds, so each is owed there; on compose it is read from a
// variable the source names or the provider's conventional one, so it is not.
var asDraftedCases = []asDraftedCase{
	{format: "compose", argument: "compose.yaml", target: "compose", owed: map[string]string{},
		check: assertTheNotesStackOnCompose},
	{format: "compose", argument: "compose.yaml", target: "helm", owed: map[string]string{
		asDraftedRelease + "components.db.features.password.infra.origin": answerSecret,
	}, check: assertTheNotesStackOnHelm},

	{format: "langflow", argument: "basic-prompting.json", target: "compose", owed: map[string]string{}},
	{format: "langflow", argument: "basic-prompting.json", target: "helm", owed: map[string]string{
		asDraftedRelease + "components.langflow.features.admin_password.infra.origin":                                     answerSecret,
		asDraftedRelease + "components.langflow.uses.model.languagemodelcomponent-kboja.features.credential.infra.origin": answerSecret,
	}},

	{format: "flowise", argument: "support-chat.json", target: "compose", owed: map[string]string{
		asDraftedRelease + "components.flowise.infra.owner_email": answerOwner,
	}},
	{format: "flowise", argument: "support-chat.json", target: "helm", owed: map[string]string{
		asDraftedRelease + "components.flowise.infra.owner_email":                                        answerOwner,
		asDraftedRelease + "components.flowise.features.owner_password.infra.origin":                     answerSecret,
		asDraftedRelease + "components.flowise.uses.model.chatopenai-0.features.credential.infra.origin": answerSecret,
	}},

	{format: "n8n", argument: "support-agent.json", target: "compose", owed: map[string]string{}},
	{format: "n8n", argument: "support-agent.json", target: "helm", owed: map[string]string{
		asDraftedRelease + "components.n8n.uses.model.openai-chat-model.features.credential.infra.origin": answerSecret,
	}},

	{format: "dify", argument: "support-assistant.yml", target: "compose", owed: map[string]string{
		asDraftedRelease + "boundaries.platform.aggregates.dify.components.api.infra.owner_email": answerOwner,
	}},
	{format: "dify", argument: "support-assistant.yml", target: "helm", owed: difyOwedOnHelm},

	{format: "agent-framework", argument: "support-agent.yaml", target: "compose", owed: map[string]string{
		asDraftedRelease + "components.supportagent.infra.image": answerImage,
	}},
	{format: "agent-framework", argument: "support-agent.yaml", target: "helm", owed: map[string]string{
		asDraftedRelease + "components.supportagent.infra.image":                                              answerImage,
		asDraftedRelease + "components.supportagent.uses.model.gpt-4-1-mini.features.credential.infra.origin": answerSecret,
	}},

	{format: "crewai", argument: "src/support_brief", target: "compose", owed: map[string]string{
		asDraftedRelease + "components.researcher.infra.image": answerImage,
		asDraftedRelease + "components.writer.infra.image":     answerImage,
	}},
	{format: "crewai", argument: "src/support_brief", target: "helm", owed: map[string]string{
		asDraftedRelease + "components.researcher.infra.image":                                             answerImage,
		asDraftedRelease + "components.researcher.uses.model.gpt-4o-mini.features.credential.infra.origin": answerSecret,
		asDraftedRelease + "components.writer.infra.image":                                                 answerImage,
		asDraftedRelease + "components.writer.uses.model.gpt-4o-mini.features.credential.infra.origin":     answerSecret,
	}},

	{format: "google-adk", argument: "support_agent", target: "compose", owed: map[string]string{
		asDraftedRelease + "components.support-agent.infra.image": answerImage,
	}},
	{format: "google-adk", argument: "support_agent", target: "helm", owed: map[string]string{
		asDraftedRelease + "components.support-agent.infra.image":                                                  answerImage,
		asDraftedRelease + "components.support-agent.uses.model.gemini-2-5-flash.features.credential.infra.origin": answerSecret,
	}},

	{format: "langgraph", argument: ".", target: "compose", owed: map[string]string{
		"aggregate.yaml:spec.components.model.values.model": answerModel,
		asDraftedRelease + "components.support.infra.image": answerImage,
	}},
	{format: "langgraph", argument: ".", target: "helm", owed: map[string]string{
		"aggregate.yaml:spec.components.model.values.model":                                                    answerModel,
		asDraftedRelease + "components.support.infra.image":                                                    answerImage,
		asDraftedRelease + "components.support.uses.checkpointer_db.postgres.features.credential.infra.origin": answerSecret,
		asDraftedRelease + "components.support.uses.model.model.features.credential.infra.origin":              answerSecret,
		asDraftedRelease + "components.postgres.features.password.infra.origin":                                answerSecret,
	}},

	{format: "mcp", argument: ".mcp.json", target: "compose", owed: map[string]string{}},
	{format: "mcp", argument: ".mcp.json", target: "helm", owed: map[string]string{
		asDraftedRelease + "components.github.features.server_env.infra.origin": answerSecret,
	}},
}

// difyOwedOnHelm are the values a Dify draft leaves for the user on helm: the
// name of the Secret the installation reads its signing key from, passed down as
// the Release's parameter, and the owner account's address and the name of the
// Secret holding its password. On compose the draft reads both secrets from
// variables, so only the owner's address is owed there.
var difyOwedOnHelm = map[string]string{
	asDraftedRelease + "parameters.secret_key":                                                                   answerSecretKey,
	asDraftedRelease + "boundaries.platform.aggregates.dify.components.api.infra.owner_email":                    answerOwner,
	asDraftedRelease + "boundaries.platform.aggregates.dify.components.api.features.owner_password.infra.origin": answerSecret,
}

// TestEveryImportExampleBuildsAsTheImportDraftsIt imports each documentation
// example's source for each target, as its walkthrough runs the import, and
// requires:
//
//   - the source read as the example's format, the import not halted, and
//     exactly an Aggregate, a Manifest and a Release drafted;
//   - the example's source imported with the answers file it ships left out, as
//     a first import meets it: the import reporting as still owed exactly the
//     values the case expects, and its drafts leaving exactly those empty, read
//     apart from the import;
//   - the answers file the example ships sitting where an import of its source
//     looks for one, answering every value this target owes, and answering
//     exactly the values the example owes on one target or the other;
//   - where any is owed, the answers file the import wrote beside the source
//     listing exactly those, each unanswered; once each is answered there, a
//     second import of the same source into a fresh directory, naming that file,
//     owing nothing and drafting what the first drafted with each answer in its
//     place and every other value unchanged;
//   - the Release the import that owes nothing drafted building, with the
//     Aggregate and the Manifest drafted beside it, over the hub's catalog, every
//     answer reaching what the build rendered;
//   - for the Compose example, the stack the source describes: the API given the
//     database's and the cache's addresses, the database's user, name and password
//     reference, PostgreSQL 16 with the source's user and database and that
//     password, and Redis 7.
//
// 🔴 PLANTED DEFECTS, each one the test is written to catch:
//
//   - in answerOwed (internal/importops/answers.go), skip writing an answer into
//     the drafts while still counting it answered: the second import owes nothing
//     and its drafts still leave the value empty, so every row owing a value
//     fails on the answered drafts before any build;
//   - delete one place from a case's owed map (flowise compose's owner_email): the
//     first import fails naming it as reported owed and not expected — and with
//     the answers file left unanswered, the second import still owes it and the
//     case fails before any build;
//   - in the answers file's reader, drop the answers to one drafted file: the
//     second import still owes those values and the case fails;
//   - delete a key from an example's shipped answers file, or have an importer
//     start owing a value the file does not answer: every row of that example
//     fails, naming the key as expected and absent;
//   - in the Compose importer, stop translating an environment variable naming a
//     service into an edge (the measured wrong stack: the API with no DATABASE_* or
//     CACHE_HOST): the compose row fails on the API's missing variables;
//   - in the Compose importer, stop translating the image tag into
//     engine_version: the compose row fails, Redis rendered at the catalog's
//     default rather than 7;
//   - in the kit's Release writer, write an owed infra.image as a comment rather
//     than an empty value: the agent rows fail, the place expected and neither
//     reported nor left empty.
func TestEveryImportExampleBuildsAsTheImportDraftsIt(t *testing.T) {
	t.Parallel()
	drafted := []string{"aggregate.yaml", ManifestFileName, "release.yaml"}
	for _, tc := range asDraftedCases {
		t.Run(tc.format+"/"+tc.target, func(t *testing.T) {
			t.Parallel()
			rw := wholeHubWorkspace(t)
			source, shipped := exampleSource(t, tc.format)
			besideRel, err := filepath.Rel(source, DefaultAnswersPath(filepath.Join(source, filepath.FromSlash(tc.argument))))
			if err != nil || besideRel != shipped {
				t.Fatalf("the example ships its answers file at %s, where an import of %s does not look for it (%s, %v)",
					shipped, tc.argument, besideRel, err)
			}
			res, dir := importAsTheExample(t, rw, source, tc.argument, tc.target, "drafts", "")
			if res.Format != tc.format {
				t.Fatalf("the source was read as the %q format, want %s", res.Format, tc.format)
			}
			if res.Halted() {
				t.Fatalf("the import halted: %+v", res.Diagnostics)
			}
			if got := draftPaths(res); !slices.Equal(got, drafted) {
				t.Fatalf("the import drafted %v, want %v", got, drafted)
			}
			holdPlaces(t, "the import reports as still owed", owedPlaces(res), tc.owed)
			holdPlaces(t, "the drafts leave empty", emptyPlaceholders(t, dir), tc.owed)

			// The example's own answers file serves both targets: it answers every
			// value either target owes, and nothing neither owes, so a value the
			// import stops or starts owing on either target fails here too.
			shippedAnswers := exampleAnswers(t, tc.format, shipped)
			holdPlaces(t, "the example's own answers file answers", placesOf(shippedAnswers), owedOnEveryTarget(tc.format))
			for place := range tc.owed {
				if shippedAnswers[place] == "" {
					t.Errorf("the example's own answers file leaves %s unanswered, which the %s target owes", place,
						tc.target)
				}
			}

			if len(tc.owed) > 0 {
				beside, err := filepath.Abs(DefaultAnswersPath(filepath.Join(source, filepath.FromSlash(tc.argument))))
				if err != nil {
					t.Fatalf("resolve the answers file beside the source: %v", err)
				}
				if res.Answers != beside || !res.AnswersWritten {
					t.Fatalf("the import wrote the answers file %q (written %v), want it beside the source at %s",
						res.Answers, res.AnswersWritten, beside)
				}
				fillAnswersFile(t, res.Answers, tc.owed)
				again, answeredDir := importAsTheExample(t, rw, source, tc.argument, tc.target, "answered", res.Answers)
				if again.Halted() {
					t.Fatalf("the answered import halted: %+v", again.Diagnostics)
				}
				if got := draftPaths(again); !slices.Equal(got, drafted) {
					t.Fatalf("the answered import drafted %v, want %v", got, drafted)
				}
				if owed := owedPlaces(again); len(owed) > 0 {
					t.Fatalf("the import given every answer still reports %q as owed", owed)
				}
				assertAnsweredAsDrafted(t, dir, answeredDir, tc.owed)
				dir = answeredDir
			} else if res.Answers != "" || res.AnswersWritten {
				t.Errorf("an import owing nothing wrote the answers file %q (written %v)", res.Answers,
					res.AnswersWritten)
			}

			artifacts := buildAsDrafted(t, dir)

			for _, answer := range distinctAnswers(tc.owed) {
				reached := false
				for _, body := range artifacts {
					reached = reached || bytes.Contains(body, []byte(answer))
				}
				if !reached {
					t.Errorf("the answer %q reaches nothing the build rendered", answer)
				}
			}
			if tc.check != nil {
				tc.check(t, artifacts)
			}
		})
	}
}

// owedOnEveryTarget returns every value the cases of format expect owed on any
// target, each with the answer its case gives it: what the example's own answers
// file answers.
func owedOnEveryTarget(format string) map[string]string {
	owed := map[string]string{}
	for _, tc := range asDraftedCases {
		if tc.format == format {
			for place, answer := range tc.owed {
				owed[place] = answer
			}
		}
	}
	return owed
}

// placesOf returns the places answers names, sorted.
func placesOf(answers map[string]string) []string {
	places := make([]string, 0, len(answers))
	for place := range answers {
		places = append(places, place)
	}
	slices.Sort(places)
	return places
}

// distinctAnswers returns the answers of owed, each once, sorted.
func distinctAnswers(owed map[string]string) []string {
	var out []string
	for _, answer := range owed {
		if !slices.Contains(out, answer) {
			out = append(out, answer)
		}
	}
	slices.Sort(out)
	return out
}

// The values the Compose example's source states for the notes service.
const (
	notesPasswordReference = "${POSTGRES_PASSWORD}"
	notesAccount           = "notes"
)

// assertTheNotesStackOnCompose holds the Docker Compose file the Compose
// example renders to what its source states: the API given the database's and
// the cache's addresses — the services' own names — and the database's user,
// name and password reference; the database PostgreSQL 16 with the source's
// user, database and password reference rather than any default of the
// catalog's; the cache Redis 7.
func assertTheNotesStackOnCompose(t *testing.T, artifacts map[string][]byte) {
	t.Helper()
	stack := renderedComposeStack(t, artifacts)
	for service, want := range map[string]struct {
		image string
		env   map[string]string
	}{
		"api": {image: "ghcr.io/example/notes-api:2.3.0", env: map[string]string{
			"DATABASE_HOST": "db", "DATABASE_USER": notesAccount, "DATABASE_NAME": notesAccount,
			"DATABASE_PASSWORD": notesPasswordReference, "CACHE_HOST": "cache",
		}},
		"db": {image: "postgres:16", env: map[string]string{
			"POSTGRES_USER": notesAccount, "POSTGRES_DB": notesAccount, "POSTGRES_PASSWORD": notesPasswordReference,
		}},
		"cache": {image: "redis:7"},
	} {
		got, ok := stack.Services[service]
		if !ok {
			t.Errorf("the rendered stack has no service %s", service)
			continue
		}
		if got.Image != want.image {
			t.Errorf("the service %s runs %q, want %q", service, got.Image, want.image)
		}
		for name, value := range want.env {
			if got.Environment[name] != value {
				t.Errorf("the service %s is given %s=%q, want %q", service, name, got.Environment[name], value)
			}
		}
	}
}

// assertTheNotesStackOnHelm holds what the Compose example renders on helm to
// what its source states: the API given the database's and the cache's
// addresses — each Service the provider names after its instance — and the
// database's user and name; the database PostgreSQL 16 with the source's user
// and database, reading its password from the Secret the answer names; the cache
// Redis 7.
func assertTheNotesStackOnHelm(t *testing.T, artifacts map[string][]byte) {
	t.Helper()
	api := renderedFirstContainer(t, artifacts, "helm/api-deployment.yaml")
	for name, want := range map[string]string{
		"DATABASE_HOST": "db-default", "DATABASE_USER": notesAccount, "DATABASE_NAME": notesAccount,
		"CACHE_HOST": "cache-default",
	} {
		if got := api.literal(t, name); got != want {
			t.Errorf("the API is given %s=%q, want %q", name, got, want)
		}
	}
	db := renderedFirstContainer(t, artifacts, "helm/db-statefulset.yaml")
	if db.Image != "postgres:16" {
		t.Errorf("the database runs %q, want postgres:16", db.Image)
	}
	for _, name := range []string{"POSTGRES_USER", "POSTGRES_DB"} {
		if got := db.literal(t, name); got != notesAccount {
			t.Errorf("the database is given %s=%q, want %q", name, got, notesAccount)
		}
	}
	if got := db.secretOf(t, "POSTGRES_PASSWORD"); got != answerSecret {
		t.Errorf("the database reads its password from the Secret %q, want %q", got, answerSecret)
	}
	if cache := renderedFirstContainer(t, artifacts, "helm/cache-deployment.yaml"); cache.Image != "redis:7" {
		t.Errorf("the cache runs %q, want redis:7", cache.Image)
	}
	if strings.Contains(string(artifacts["helm/db-statefulset.yaml"]), "apppass") {
		t.Error("the database is rendered with the catalog's default password")
	}
}
