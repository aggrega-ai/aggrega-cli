// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
	schemas "github.com/aggrega-ai/aggrega-schemas"
)

// denyReachEnum reads the values mcp.deny_reach admits out of a configuration
// schema, in the order the schema lists them.
//
// Every step of the descent is reported rather than skipped. A path that has
// moved would otherwise yield an empty list, and an empty list compares equal to
// nothing at all — which is the shape in which this whole check would go quiet
// while the two sides drifted.
func denyReachEnum(schema string) ([]string, error) {
	var node map[string]any
	if err := json.Unmarshal([]byte(schema), &node); err != nil {
		return nil, fmt.Errorf("unmarshal the configuration schema: %w", err)
	}
	for _, step := range []string{"properties", "mcp", "properties", "deny_reach", "items"} {
		next, ok := node[step].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("the configuration schema carries no %q under the path to the "+
				"deny_reach entries; the setting has moved or been renamed", step)
		}
		node = next
	}
	raw, ok := node["enum"].([]any)
	if !ok {
		return nil, fmt.Errorf("the deny_reach entries declare no enum, so the schema admits any string " +
			"and nothing refuses a value the operation vocabulary does not hold")
	}
	out := make([]string, 0, len(raw))
	for i, v := range raw {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("entry %d of the deny_reach enum is not a string: %v", i, v)
		}
		out = append(out, s)
	}
	return out, nil
}

// TestTheDenyReachEnumAgreesWithTheOperationVocabulary holds the configuration
// schema's list of reach kinds in step with the one the operation set declares.
//
// The two are independent statements of one closed set: the schema's enum is
// what refuses a value when a configuration loads, and the operation set's
// classes are what every operation declares itself against. A kind added to the
// operation set and not to the schema cannot be named by a deployment; one added
// to the schema and not to the set is a value the schema accepts and nothing can
// act on. Neither failure is visible from the side it was edited on.
//
// The order is compared too, and deliberately. The operation set returns its
// classes widening, so a reader meeting the schema's list meets the same scale
// in the same direction rather than an alphabet.
//
// The comparison is written across the two representations rather than by
// generating one from the other. That is the near-duplicate the cross-check
// needs: a schema emitted from the Go constants could not disagree with them,
// and disagreement is the entire signal.
func TestTheDenyReachEnumAgreesWithTheOperationVocabulary(t *testing.T) {
	t.Parallel()

	declared, err := denyReachEnum(schemas.AggregaConfigV1alpha1)
	if err != nil {
		t.Fatalf("read the deny_reach enum: %v", err)
	}
	// A control before the comparison: an empty list on either side satisfies a
	// length-and-member check while asserting nothing about either vocabulary.
	if len(declared) == 0 {
		t.Fatal("the deny_reach enum is empty, so the comparison below asserts nothing")
	}
	classes := opsurface.Classes()
	if len(classes) == 0 {
		t.Fatal("the operation set declares no reach classes, so the comparison below asserts nothing")
	}

	if len(declared) != len(classes) {
		t.Fatalf("the configuration schema admits %d reach kinds and the operation set declares %d: "+
			"schema %v, operations %v", len(declared), len(classes), declared, classes)
	}
	for i := range classes {
		if declared[i] != string(classes[i]) {
			t.Errorf("at position %d the configuration schema admits %q and the operation set declares %q; "+
				"a deployment naming a kind here and an operation declaring one must mean the same word",
				i, declared[i], string(classes[i]))
		}
	}
}

// TestTheDenyReachEnumReaderRefusesWhatItCannotRead is the negative control for
// the descent above: a schema whose path has moved, or whose entries carry no
// enum, must be reported rather than read as an empty vocabulary.
func TestTheDenyReachEnumReaderRefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		schema  string
		wantSub string
	}{
		{
			name:    "the setting has moved",
			schema:  `{"properties":{"mcp":{"properties":{"allowed_origins":{"items":{"enum":["a"]}}}}}}`,
			wantSub: `no "deny_reach"`,
		},
		{
			name:    "the entries carry no enum",
			schema:  `{"properties":{"mcp":{"properties":{"deny_reach":{"items":{"type":"string"}}}}}}`,
			wantSub: "declare no enum",
		},
		{
			name:    "an entry is not a string",
			schema:  `{"properties":{"mcp":{"properties":{"deny_reach":{"items":{"enum":[1]}}}}}}`,
			wantSub: "is not a string",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := denyReachEnum(tc.schema)
			if err == nil {
				t.Fatalf("the reader accepted a schema it cannot read and returned %v", got)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("the refusal does not report %q: %v", tc.wantSub, err)
			}
		})
	}
}
