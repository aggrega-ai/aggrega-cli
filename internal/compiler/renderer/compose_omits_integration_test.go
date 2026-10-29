// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// These tests drive a compose Recipe's omits key end to end through RenderAll: a
// compose file fetched as a compose-https-fragment source, seeded in the cache as a
// pull leaves it, rendered through the embedded compose provider guest and assembled
// into the docker-compose.yaml a build writes. The guest decides what an entry
// leaves out and whether what remains still names it; the host evaluates the entries,
// hands them over, refuses a provider that does not claim to honour them and halts on
// an entry that left nothing out.

// composeOmitsURL is where the fixture's fragment is published.
const composeOmitsURL = "https://example.com/omits/compose.yaml"

// composeOmitsBundle is a fetched compose file bundling a database beside the
// service the Release wants, where web names nothing.
const composeOmitsBundle = `services:
  web:
    image: nginx:1.27
    ports:
      - "8080:8080"
  db:
    image: postgres:16
`

// renderComposeOmits renders one component whose compose Recipe sources a fetched
// fragment holding source and declares omits, returning what the build assembled.
func renderComposeOmits(t *testing.T, source string, omits []types.OmitEntry) (map[string]renderer.AssembledFile, error) {
	t.Helper()
	root := t.TempDir()
	blob := buildIntegrationTGZ(t, map[string]string{"compose.yaml": source})
	digest := integrationDigest(blob)
	seedIntegrationCacheSlot(t, root, "compose-https-fragment", composeOmitsURL, "1.0.0", blob, digest)

	input := integrationInputCompose(t, root, "core/compute/api-server", "api", digest, composeOmitsURL, "1.0.0")
	base := structuredSourceRecipeLookup("compose", "compose-https-fragment", composeOmitsURL, "1.0.0")
	lookup := renderer.RecipeLookup{
		Recipe: func(namespace, componentType string) (*types.Recipe, bool) {
			recipe, ok := base.Recipe(namespace, componentType)
			if ok {
				recipe.Spec.Omits = omits
			}
			return recipe, ok
		},
		FeatureRecipe: base.FeatureRecipe,
	}
	result, err := renderer.RenderAll(context.Background(), input, lookup, wasmEngineFactory(t))
	if err != nil {
		return nil, err
	}
	return result.Files, nil
}

// assembledServices returns the services the assembled docker-compose.yaml declares,
// sorted, or fails when the build assembled no such file.
func assembledServices(t *testing.T, files map[string]renderer.AssembledFile) []string {
	t.Helper()
	for _, path := range slices.Sorted(maps.Keys(files)) {
		if !strings.HasSuffix(path, "docker-compose.yaml") {
			continue
		}
		var doc struct {
			Services map[string]any `yaml:"services"`
		}
		if err := yaml.Unmarshal(files[path].Content, &doc); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		return slices.Sorted(maps.Keys(doc.Services))
	}
	t.Fatalf("the build assembled no docker-compose.yaml; files: %v", slices.Sorted(maps.Keys(files)))
	return nil
}

// requireNotRefusedForTheContract fails the test with the reason when the build was
// refused because the compose provider's declared contract does not claim to honour
// omits — the refusal every compose Recipe declaring the key met before the provider
// claimed it, and the one a stale embedded guest still produces.
func requireNotRefusedForTheContract(t *testing.T, err error) {
	t.Helper()
	if errors.Is(err, renderer.ErrUnsupportedOmits()) {
		t.Fatalf("the compose Recipe declaring omits was refused before it rendered, because the provider does "+
			"not claim to honour the key — is the embedded compose guest built from this tree?\n%s", cerrors.CauseChain(err))
	}
}

// TestRenderAll_ComposeOmitsLeaveAServiceOutOfAFetchedSource is the key's purpose
// read through the build: a fetched compose file bundles a database the Release
// must not run. Without the key the assembled file carries both services, which is
// what shows the build can see the difference; with an entry leaving db out it
// carries web alone, and two builds assemble the same bytes.
func TestRenderAll_ComposeOmitsLeaveAServiceOutOfAFetchedSource(t *testing.T) {
	t.Parallel()

	control, err := renderComposeOmits(t, composeOmitsBundle, nil)
	if err != nil {
		t.Fatalf("the build without the key: %s", cerrors.CauseChain(err))
	}
	if got, want := assembledServices(t, control), []string{"db", "web"}; !slices.Equal(got, want) {
		t.Fatalf("the build without the key assembles %v, want %v — the rows below read a file the source no longer renders", got, want)
	}

	omits := []types.OmitEntry{{Kind: "service", Name: "db"}}
	once, err := renderComposeOmits(t, composeOmitsBundle, omits)
	requireNotRefusedForTheContract(t, err)
	if err != nil {
		t.Fatalf("the build leaving db out: %s", cerrors.CauseChain(err))
	}
	if got, want := assembledServices(t, once), []string{"web"}; !slices.Equal(got, want) {
		t.Errorf("the build leaving db out assembles %v, want %v", got, want)
	}
	twice, err := renderComposeOmits(t, composeOmitsBundle, omits)
	if err != nil {
		t.Fatalf("the second build leaving db out: %s", cerrors.CauseChain(err))
	}
	if !reflect.DeepEqual(once, twice) {
		t.Errorf("two builds leaving db out assembled different files")
	}
}

// TestRenderAll_ComposeOmitsEntryThatLeftNothingOutHalts holds the host's halt on an
// entry the compose provider reported as having left nothing out, naming the entry's
// position and the kind and name it resolved to.
func TestRenderAll_ComposeOmitsEntryThatLeftNothingOutHalts(t *testing.T) {
	t.Parallel()

	_, err := renderComposeOmits(t, composeOmitsBundle, []types.OmitEntry{
		{Kind: "service", Name: "db"},
		{Kind: "service", Name: "cache"},
	})
	requireNotRefusedForTheContract(t, err)
	if !errors.Is(err, renderer.ErrOmitMatchedNothing()) {
		t.Fatalf("an entry naming a service the file does not declare: got %v, want the halt on an entry that left nothing out", err)
	}
	chain := cerrors.CauseChain(err)
	for _, want := range []string{`"api-recipe" omits[1]`, `kind "service"`, `name "cache"`} {
		if !strings.Contains(chain, want) {
			t.Errorf("the halt does not carry %q:\n%s", want, chain)
		}
	}
	if strings.Contains(chain, "omits[0]") {
		t.Errorf("the halt names the entry that left its service out:\n%s", chain)
	}
}

// TestRenderAll_ComposeOmitsRemainingDependsOnHalts holds the provider's halt on a
// file in which a remaining service still names the one an entry left out: web
// depends on db, so leaving db out would assemble a file compose refuses, and the
// build stops with the provider's own sentence naming web, the reference and db.
func TestRenderAll_ComposeOmitsRemainingDependsOnHalts(t *testing.T) {
	t.Parallel()

	source := strings.Replace(composeOmitsBundle, "    image: nginx:1.27\n", "    image: nginx:1.27\n    depends_on: [db]\n", 1)
	if _, err := renderComposeOmits(t, source, nil); err != nil {
		t.Fatalf("the file builds without the key, so the halt below is the reference's: %s", cerrors.CauseChain(err))
	}

	_, err := renderComposeOmits(t, source, []types.OmitEntry{{Kind: "service", Name: "db"}})
	requireNotRefusedForTheContract(t, err)
	if !errors.Is(err, plugincondition.ErrPluginRequiredInputUnresolved()) {
		t.Fatalf("leaving out a service another still depends on: got %v, want the provider's halt on the remaining reference", err)
	}
	chain := cerrors.CauseChain(err)
	for _, want := range []string{
		plugincondition.ErrPluginRequiredInputUnresolved().Error(),
		`compose: what an omits entry left out is still named by the file: service "web" names service "db" at depends_on[0]`,
	} {
		if !strings.Contains(chain, want) {
			t.Errorf("the halt does not carry %q:\n%s", want, chain)
		}
	}
}
