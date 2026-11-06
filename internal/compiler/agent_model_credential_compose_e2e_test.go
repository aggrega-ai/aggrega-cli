// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end proofs that a core/secret-value credential reaches the service it
// is attached for on Docker Compose, whichever document attaches it: the
// Release alone, or the Aggregate naming the key the value is delivered under.
// The credential on a framework agent's model connection reaches the agent as
// OPENAI_API_KEY; the password on a PostgreSQL database reaches the database as
// POSTGRES_PASSWORD.
//
// These tests read catalog entities from the aggrega-hub repository checked out
// beside this one (the paths leave this repository for aggrega-hub). Without
// that checkout they fail with a catalog-load error rather than skipping, so
// check out aggrega-hub beside this repository first.

package compiler_test

import (
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// agentCredentialDir is the fixture workspace: per subject (the CrewAI agent,
// the LangGraph agent, the PostgreSQL database) an Aggregate leaving the
// credential to the Release, one attaching it under the name the service
// receives it as, and one attaching it under another name, a Manifest mapping
// its Compose Recipes, a Release per case, and the compose provider's Plugin
// entity under catalog/.
const agentCredentialDir = "testdata/agent-model-credential-compose"

// agentCredentialCatalogPaths names every catalog entity the fixture's Releases
// resolve from the sibling catalog (flowEscapeHub): the CrewAI and LangGraph
// Components with their Compose Recipes, the model and the checkpointer store
// they are wired to with theirs, the PostgreSQL Component with its Compose
// Recipe, the schema library their values reference, and the secret Feature with
// the Compose realization that delivers its value on the inject pipe. The
// provider Plugin entity is the fixture's own: the catalog keeps its Plugin
// entities under a directory named for the release version. Its guest resolves
// from the binary's embedded set.
func agentCredentialCatalogPaths() []string {
	hub := func(p string) string { return flowEscapeHub + "/" + p }
	return []string{
		agentCredentialDir + "/catalog",
		hub("component/core/ai-agent/crewai/1.0.0/component.yaml"),
		hub("component/core/ai-agent/langgraph/1.0.0/component.yaml"),
		hub("component/core/ai-model/openai/1.0.0/component.yaml"),
		hub("component/core/datastore/valkey/1.0.0/component.yaml"),
		hub("component/core/datastore/postgres/1.0.0/component.yaml"),
		hub("recipe/compose/crewai/1.0.0/recipe.yaml"),
		hub("recipe/compose/langgraph/1.0.0/recipe.yaml"),
		hub("recipe/compose/openai/1.0.0/recipe.yaml"),
		hub("recipe/compose/valkey/1.0.0/recipe.yaml"),
		hub("recipe/compose/postgres/1.0.0/recipe.yaml"),
		hub("schemalibrary/base-components/1.0.0/library.yaml"),
		hub("feature/core/secret-value/1.0.0/feature.yaml"),
		hub("feature-recipe/compose/dotenv-secret/1.0.0/recipe.yaml"),
	}
}

// credentialCase is one build of the fixture and what one service of it must
// receive.
type credentialCase struct {
	name    string
	release string
	// service is the compose service the credential is attached for.
	service string
	// variable is the name the service receives the credential under.
	variable string
	// want is the origin the Release writes on the credential.
	want string
	// attachmentKey is the key the attachment delivers the value under on the
	// inject pipe, which must not reach the service as a variable of its own;
	// empty where it is the variable itself.
	attachmentKey string
}

// assertCredentialDelivered builds tc's Release through the real pipeline and
// reads the rendered compose file: tc.service receives tc.want under
// tc.variable, and no variable named for the attachment's own key.
func assertCredentialDelivered(t *testing.T, tc credentialCase) {
	t.Helper()
	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  filepath.Join(agentCredentialDir, tc.release),
		CatalogPaths: agentCredentialCatalogPaths(),
	})
	body := findArtifactBySuffix(t, result.Artifacts, "docker-compose.yaml")

	var shape composeFileShape
	if err := yaml.Unmarshal(body, &shape); err != nil {
		t.Fatalf("the rendered compose file does not parse: %v\n%s", err, body)
	}
	svc, ok := shape.Services[tc.service]
	if !ok {
		t.Fatalf("the rendered compose file holds no %s service:\n%s", tc.service, body)
	}
	got, ok := svc.Environment[tc.variable]
	if !ok {
		t.Fatalf("the %s service receives no %s:\n%s", tc.service, tc.variable, body)
	}
	if got != tc.want {
		t.Errorf("the %s service receives %s %q, want the origin the Release wrote, %q:\n%s", tc.service, tc.variable, got, tc.want, body)
	}
	if tc.attachmentKey != "" {
		if _, leaked := svc.Environment[tc.attachmentKey]; leaked {
			t.Errorf("the %s service receives a variable named %q, the key the attachment delivers on the inject pipe:\n%s", tc.service, tc.attachmentKey, body)
		}
	}
}

// TestAModelCredentialTheReleaseAloneAttachesReachesTheAgentOnCompose builds the
// CrewAI and LangGraph agents on Docker Compose with a core/secret-value on the
// model connection and reads the variable the agent's service receives.
//
// What a failure here catches:
//   - an agent Recipe reading the credential off the inject pipe under a fixed
//     name instead of under the key its own attachment delivered it under. An
//     attachment the Release alone makes names no values.key, so its value
//     arrives under the key value, and a fixed read halts the build with a
//     template error naming the key the pipe does not hold — the Release-attached
//     rows;
//   - a Recipe reading only the key value, else the fixed name — the other-key
//     rows, whose Aggregate names MODEL_KEY: such a read finds neither key on the
//     pipe and halts the build;
//   - the key the Aggregate names no longer being the one read where it is the
//     fixed name itself — the keyed rows, which hold the form that builds today
//     in place;
//   - the value delivered under OPENAI_API_KEY being anything other than the
//     origin the Release wrote, or the attachment's own key leaking into the
//     service as a variable of its own.
func TestAModelCredentialTheReleaseAloneAttachesReachesTheAgentOnCompose(t *testing.T) {
	t.Parallel()

	cases := []credentialCase{
		{name: "crewai/release-attached", release: "release-crewai.yaml", service: "agent", variable: "OPENAI_API_KEY", want: "release-attached-key", attachmentKey: "value"},
		{name: "crewai/aggregate-keyed", release: "release-crewai-keyed.yaml", service: "agent", variable: "OPENAI_API_KEY", want: "aggregate-keyed-key"},
		{name: "crewai/aggregate-other-key", release: "release-crewai-other-key.yaml", service: "agent", variable: "OPENAI_API_KEY", want: "aggregate-other-key", attachmentKey: "MODEL_KEY"},
		{name: "langgraph/release-attached", release: "release-langgraph.yaml", service: "agent", variable: "OPENAI_API_KEY", want: "release-attached-key", attachmentKey: "value"},
		{name: "langgraph/aggregate-keyed", release: "release-langgraph-keyed.yaml", service: "agent", variable: "OPENAI_API_KEY", want: "aggregate-keyed-key"},
		{name: "langgraph/aggregate-other-key", release: "release-langgraph-other-key.yaml", service: "agent", variable: "OPENAI_API_KEY", want: "aggregate-other-key", attachmentKey: "MODEL_KEY"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertCredentialDelivered(t, tc)
		})
	}
}

// TestADatabasePasswordTheReleaseAloneAttachesReachesPostgresOnCompose builds a
// PostgreSQL database on Docker Compose with a core/secret-value on its password
// slot and reads the variable the database's service receives.
//
// What a failure here catches:
//   - the PostgreSQL Recipe reading the password off the inject pipe under the
//     fixed name POSTGRES_PASSWORD instead of under the key its own attachment
//     delivered it under: the Release-attached row then halts the build with a
//     template error naming the key the pipe does not hold;
//   - a Recipe reading only the key value, else the fixed name — the other-key
//     row, whose Aggregate names DB_PW: such a read finds neither key on the
//     pipe and halts the build;
//   - the key the Aggregate names no longer being the one read where it is the
//     fixed name itself — the keyed row, the form that builds today;
//   - the delivered value differing from the origin the Release wrote, or the
//     attachment's own key leaking into the service as a variable of its own.
func TestADatabasePasswordTheReleaseAloneAttachesReachesPostgresOnCompose(t *testing.T) {
	t.Parallel()

	cases := []credentialCase{
		{name: "postgres/release-attached", release: "release-postgres.yaml", service: "db", variable: "POSTGRES_PASSWORD", want: "release-attached-password", attachmentKey: "value"},
		{name: "postgres/aggregate-keyed", release: "release-postgres-keyed.yaml", service: "db", variable: "POSTGRES_PASSWORD", want: "aggregate-keyed-password"},
		{name: "postgres/aggregate-other-key", release: "release-postgres-other-key.yaml", service: "db", variable: "POSTGRES_PASSWORD", want: "aggregate-other-password", attachmentKey: "DB_PW"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertCredentialDelivered(t, tc)
		})
	}
}
