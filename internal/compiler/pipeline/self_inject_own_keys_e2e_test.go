// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

const selfInjectOwnKeysDir = "testdata/self-inject-own-keys/"

// TestARecipeReadsWhichOwnKeyEachSelfInjectEntryDelivers builds a component whose
// own injection block carries one entry delivering an own provides key, one
// delivering one own value, a literal, one reading two own values, and one in a
// custom mechanism, through the real pipeline, with a Recipe that renders
// .context.self_inject whole. The rendered map must equal, mechanism by
// mechanism, the own key each entry delivers: the provides key and the single
// value are named, the literal and the two-value entry are empty.
func TestARecipeReadsWhichOwnKeyEachSelfInjectEntryDelivers(t *testing.T) {
	t.Parallel()

	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  selfInjectOwnKeysDir + "release.yaml",
		CatalogPaths: []string{selfInjectOwnKeysDir + "catalog/base"},
	})

	var rendered string
	found := 0
	for path, body := range result.Artifacts {
		if !strings.HasSuffix(path, "docker-compose.yaml") {
			continue
		}
		var compose struct {
			Services map[string]struct {
				Environment map[string]string `yaml:"environment"`
			} `yaml:"services"`
		}
		if err := yaml.Unmarshal(body, &compose); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if v, ok := compose.Services["app"].Environment["OWN_KEYS"]; ok {
			rendered = v
			found++
		}
	}
	if found != 1 {
		t.Fatalf("want exactly one rendered OWN_KEYS variable, found %d among %d artifacts", found, len(result.Artifacts))
	}

	var got map[string]map[string]string
	if err := json.Unmarshal([]byte(rendered), &got); err != nil {
		t.Fatalf("decode the rendered map %q: %v", rendered, err)
	}
	want := map[string]map[string]string{
		"env": {
			"SERVICE_HOST":  "provides.host",
			"AGENT_ROLE":    "values.role",
			"MODE":          "",
			"ROLE_AND_GOAL": "",
		},
		"ssm_param": {
			"/app/goal": "values.goal",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf(".context.self_inject = %v; want %v (rendered %q)", got, want, rendered)
	}
}
