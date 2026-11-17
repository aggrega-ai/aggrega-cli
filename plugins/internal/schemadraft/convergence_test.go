// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft/cloudformation"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft/terraform"
	"github.com/aggrega-ai/aggrega-cli/plugins/parsercore"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// parseJSON reads JSON source as the host's json parser does.
func parseJSON(syntax string, source []byte) (json.RawMessage, error) {
	if syntax != "json" {
		return nil, fmt.Errorf("no parser for %s", syntax)
	}
	out := parsercore.ParseJSON(source)
	for _, d := range out.Diagnostics {
		return nil, errors.New(d.Message)
	}
	return out.Tree, nil
}

// headerRead matches a header line of a drafted Recipe naming a key it reads:
// its pillar and the key.
var headerRead = regexp.MustCompile(`(?m)^  # (values|infra\.overrides|infra|features)\.([A-Za-z0-9_]+): `)

// pillarKeys drafts the fixture path of format f and returns, for each pillar
// its Recipe reads from, the snake_case keys among keep it reads there, sorted.
func pillarKeys(t *testing.T, f schemadraft.Format, path string, keep []string) map[string][]string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := schemadraft.Import(aggrega.ImportInput{Format: f.Name, Name: filepath.Base(path), Bytes: body}, f, parseJSON)
	var recipe string
	for _, d := range out.Diagnostics {
		if d.Severity == "halt" {
			t.Fatalf("drafting %s halts: %+v", path, d)
		}
	}
	for _, e := range out.Entities {
		if e.Kind == "Recipe" {
			recipe = e.Body
		}
	}
	keys := map[string][]string{}
	for _, m := range headerRead.FindAllStringSubmatch(recipe, -1) {
		if key := schemadraft.SnakeKey(m[2]); slices.Contains(keep, key) {
			keys[m[1]] = append(keys[m[1]], key)
		}
	}
	for _, k := range keys {
		slices.Sort(k)
	}
	return keys
}

// TestCreateOnlyPlacementConverges drafts four properties CloudFormation states
// alike and Terraform does not — the identifier and the subnet group required
// and create-only, the key id optional and create-only, the name optional —
// from each format, and finds each in the same pillar under the same
// snake_case key: whether a property is replaced on change decides nothing of
// where a draft places it.
func TestCreateOnlyPlacementConverges(t *testing.T) {
	keep := []string{"identifier", "name", "subnet_group", "kms_key_id"}
	cfn := pillarKeys(t, cloudformation.Format, filepath.Join("cloudformation", "testdata", "ledger.json"), keep)
	tf := pillarKeys(t, terraform.Format, filepath.Join("terraform", "testdata", "aws-example-ledger.json"), keep)
	want := map[string][]string{"values": {"identifier", "name"}, "infra": {"subnet_group"},
		"infra.overrides": {"kms_key_id"}}
	for _, drafted := range []struct {
		format string
		keys   map[string][]string
	}{{"cloudformation", cfn}, {"terraform", tf}} {
		if fmt.Sprint(drafted.keys) != fmt.Sprint(want) {
			t.Errorf("the %s draft places %v, want %v", drafted.format, drafted.keys, want)
		}
	}
}
