// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// xAggregaFinding represents one normalized finding for the
// equivalence comparison: entity URI, the offending key, and the
// RFC 6901 JSON Pointer locating it within the entity schema.
// Severity field intentionally omitted — the Go walker emits at
// warning severity (via log.Printf); the Wasm rule emits at error
// severity (via LinterFinding.Severity); the divergence is by
// design and is normalised out of the equivalence comparison.
type xAggregaFinding struct {
	Entity  string
	Key     string
	Pointer string
}

// parseGoWalkerOutput extracts xAggregaFinding tuples from
// captured DiscourageDirectXAggregaInSchema log output. Log
// format (placeholders in ${...}, literal angle brackets around
// the pointer):
//
//	warning: entity "${name}" authors ${key} at <${pointer}> in schema content; ...
//
// Pointer is bracketed (literal `<...>`) so the regex can capture
// pointers containing spaces. Key is captured by `\S+`: spaces in
// `x-aggrega-*` key names would violate the platform's naming
// conventions (vendor-extension keys follow domain naming rules,
// not free-form labels), so non-whitespace is sufficient.
//
// One tuple per matching log line. Lines not matching the regex
// are ignored (other log.Printf callers don't pollute the result).
func parseGoWalkerOutput(captured string) []xAggregaFinding {
	re := regexp.MustCompile(`warning: entity "([^"]+)" authors (x-aggrega-\S+) at <([^>]+)> in schema content`)
	matches := re.FindAllStringSubmatch(captured, -1)
	result := make([]xAggregaFinding, 0, len(matches))
	for _, m := range matches {
		result = append(result, xAggregaFinding{Entity: m[1], Key: m[2], Pointer: m[3]})
	}
	return result
}

// parseWasmFindings extracts xAggregaFinding tuples from the
// publishing-linter Wasm plugin's findings. Message format is
// identical to the Go walker's log line except for the leading
// "warning: " prefix (severity carried separately in
// LinterFinding.Severity). The RuleID filter is defensive against
// the dispatcher returning findings from other rules; harmless
// here since DispatchPublishingLinter only returns findings for
// the requested rule.
func parseWasmFindings(findings []LinterFinding) []xAggregaFinding {
	re := regexp.MustCompile(`entity "([^"]+)" authors (x-aggrega-\S+) at <([^>]+)> in schema content`)
	result := make([]xAggregaFinding, 0, len(findings))
	for _, f := range findings {
		if f.RuleID != "x-aggrega-discouragement" {
			continue
		}
		m := re.FindStringSubmatch(f.Message)
		if m == nil {
			continue
		}
		result = append(result, xAggregaFinding{Entity: m[1], Key: m[2], Pointer: m[3]})
	}
	return result
}

// sortFindings sorts a finding slice in place by (Entity, Pointer,
// Key) so the equivalence comparison is order-independent. The
// Go walker emits in YAML authoring order; the Rust rule emits in
// serde_json::Map's BTreeMap-backed alphabetical order; the sort
// resolves the divergence before comparison.
func sortFindings(findings []xAggregaFinding) {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Entity != findings[j].Entity {
			return findings[i].Entity < findings[j].Entity
		}
		if findings[i].Pointer != findings[j].Pointer {
			return findings[i].Pointer < findings[j].Pointer
		}
		return findings[i].Key < findings[j].Key
	})
}

// loadEntityAndSchemaJSON parses a fixture YAML and returns:
//   - the entity's metadata.name
//   - a *yaml.Node of spec.schema (for the Go walker)
//   - a json.RawMessage of the same spec.schema (for the Wasm rule)
//
// The Wasm side receives JSON because the host converts YAML to
// JSON before invoking the plugin (per the linter plugin's
// canonical wire shape).
func loadEntityAndSchemaJSON(t *testing.T, yamlBytes []byte) (string, *yaml.Node, json.RawMessage) {
	t.Helper()
	// Unmarshal into a typed envelope: name as string; schema as a
	// *yaml.Node so we can hand it to the Go walker directly AND
	// re-decode it to a generic value for JSON marshalling.
	var envelope struct {
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
		Spec struct {
			Schema yaml.Node `yaml:"schema"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(yamlBytes, &envelope); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}

	var schemaNode *yaml.Node
	var schemaAny interface{}
	if envelope.Spec.Schema.Kind != 0 {
		schemaNode = &envelope.Spec.Schema
		if err := schemaNode.Decode(&schemaAny); err != nil {
			t.Fatalf("schema decode: %v", err)
		}
	}
	cleaned := convertYAMLMapsForJSON(schemaAny)
	schemaJSON, err := json.Marshal(cleaned)
	if err != nil {
		t.Fatalf("schema marshal: %v", err)
	}
	return envelope.Metadata.Name, schemaNode, schemaJSON
}

// convertYAMLMapsForJSON recursively converts map[interface{}]interface{}
// (yaml.v3 default for nested maps when decoded into interface{}) to
// map[string]interface{} so json.Marshal accepts the value.
func convertYAMLMapsForJSON(v interface{}) interface{} {
	switch x := v.(type) {
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(x))
		for k, val := range x {
			out[asString(k)] = convertYAMLMapsForJSON(val)
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, len(x))
		for k, val := range x {
			out[k] = convertYAMLMapsForJSON(val)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(x))
		for i, val := range x {
			out[i] = convertYAMLMapsForJSON(val)
		}
		return out
	default:
		return v
	}
}

func asString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// buildProposedJSON constructs the `proposed` envelope the Wasm
// plugin's LinterInput consumes.
func buildProposedJSON(entityName string, schemaJSON json.RawMessage) json.RawMessage {
	proposed := map[string]interface{}{
		"kind":     "Component",
		"metadata": map[string]string{"name": entityName},
		"spec":     map[string]json.RawMessage{"schema": schemaJSON},
	}
	out, _ := json.Marshal(proposed)
	return out
}

// TestXAggregaWalkerAndRuleProduceEquivalentFindings drives BOTH
// implementations against shared YAML fixtures and asserts the
// finding sets are equivalent (same entity, same key, same JSON
// Pointer; severity differs by design and is normalised out of the
// comparison).
//
// NO t.Parallel() — log.SetOutput is process-wide state; subtests
// share the test binary's global logger.
func TestXAggregaWalkerAndRuleProduceEquivalentFindings(t *testing.T) {
	pluginBytes := wasmtest.EnsureFixtureBytes(t, "publishing-linter.wasm")
	fixtures, err := filepath.Glob("testdata/x_aggrega_equivalence/*.yaml")
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatalf("no fixtures found")
	}
	for _, fxPath := range fixtures {
		t.Run(filepath.Base(fxPath), func(t *testing.T) {
			yamlBytes, err := os.ReadFile(fxPath)
			if err != nil {
				t.Fatalf("read %s: %v", fxPath, err)
			}
			entityName, schemaNode, schemaJSON := loadEntityAndSchemaJSON(t, yamlBytes)

			// Drive Go walker; capture log output. Use t.Cleanup
			// to restore on panic. Strip timestamp flags so the
			// captured output is parser-friendly.
			var logBuf bytes.Buffer
			prevWriter := log.Writer()
			prevFlags := log.Flags()
			log.SetOutput(&logBuf)
			log.SetFlags(0)
			t.Cleanup(func() {
				log.SetOutput(prevWriter)
				log.SetFlags(prevFlags)
			})
			DiscourageDirectXAggregaInSchema(entityName, schemaNode)
			goFindings := parseGoWalkerOutput(logBuf.String())

			// Drive Wasm rule via Extism through DispatchPublishingLinter.
			proposed := buildProposedJSON(entityName, schemaJSON)
			wasmRaw, err := DispatchPublishingLinter(pluginBytes, LinterInput{
				RuleID:     "x-aggrega-discouragement",
				EntityKind: "Component",
				Proposed:   proposed,
			})
			if err != nil {
				t.Fatalf("DispatchPublishingLinter: %v", err)
			}
			wasmFindings := parseWasmFindings(wasmRaw)

			sortFindings(goFindings)
			sortFindings(wasmFindings)
			if !reflect.DeepEqual(goFindings, wasmFindings) {
				t.Errorf("equivalence broken on %s:\n  Go:   %+v\n  Wasm: %+v",
					filepath.Base(fxPath), goFindings, wasmFindings)
			}
		})
	}
}
