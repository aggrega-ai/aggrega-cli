// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
)

// writtenNullPair is one key of an emitted mapping as the tests read it: the key,
// the value's resolved tag, and the value's text.
type writtenNullPair struct {
	key, tag, value string
}

// renderSourcedAndAssemble renders source with values laid over it through the
// sourced core, assembles the one fragment it emits the way the merge does, and
// returns the assembled bytes and the mapping of the named service in them.
func renderSourcedAndAssemble(t *testing.T, source string, values map[string]any, service string) ([]byte, *yaml.Node) {
	t.Helper()
	nodeCtx := nodeCtxFor("app")
	rendered, err := RenderSourcedCore([]byte(source), "docker-compose.base.yaml", values, nodeCtx)
	if err != nil {
		t.Fatalf("RenderSourcedCore: %v", err)
	}
	var fragments []rendercore.EmittedFile
	for name, body := range rendered.Files {
		fragments = append(fragments, rendercore.EmittedFile{Name: name, Body: body, Intermediate: true})
	}
	files, _, err := AssembleBoundaryCore(
		map[string][]rendercore.EmittedFile{"app": fragments},
		map[string]map[string]string{"app": {}},
		nil, nil, "default", 2,
	)
	if err != nil {
		t.Fatalf("AssembleBoundaryCore: %v", err)
	}
	body, ok := files[AssembledOutputFile]
	if !ok {
		t.Fatalf("no %s among the assembled files", AssembledOutputFile)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse the assembled file: %v\n%s", err, body)
	}
	svc := mappingValue(mappingValue(doc.Content[0], "services"), service)
	if svc == nil || svc.Kind != yaml.MappingNode {
		t.Fatalf("no service %q in the assembled file:\n%s", service, body)
	}
	return body, svc
}

// mappingValue returns the value written for key in mapping, or nil.
func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// mappingPairs lists a mapping's keys in the order emitted, each with its value's
// tag and text; a value that is not a scalar is listed with its tag alone.
func mappingPairs(mapping *yaml.Node) []writtenNullPair {
	var out []writtenNullPair
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		v := mapping.Content[i+1]
		p := writtenNullPair{key: mapping.Content[i].Value, tag: v.ShortTag()}
		if v.Kind == yaml.ScalarNode {
			p.value = v.Value
		}
		out = append(out, p)
	}
	return out
}

// mappingKeys lists a mapping's keys in the order emitted.
func mappingKeys(mapping *yaml.Node) []string {
	var out []string
	for _, p := range mappingPairs(mapping) {
		out = append(out, p.key)
	}
	return out
}

// TestWrittenNull_OverlayEnvironmentEntryCarriedAsNull lays an overlay writing an
// environment entry as null over a source that gives it a value. The entry is
// emitted as `C: null` — not an empty string, not removed — while the entries
// beside it keep their values, the quoted string included.
func TestWrittenNull_OverlayEnvironmentEntryCarriedAsNull(t *testing.T) {
	t.Parallel()
	source := `services:
  api:
    image: base:1
    environment:
      A: "1"
      C: "3"
`
	values := map[string]any{"services": map[string]any{"api": map[string]any{
		"environment": map[string]any{"B": "2", "C": nil},
	}}}
	body, svc := renderSourcedAndAssemble(t, source, values, "api")

	got := mappingPairs(mappingValue(svc, "environment"))
	want := []writtenNullPair{{"A", "!!str", "1"}, {"B", "!!str", "2"}, {"C", "!!null", "null"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("environment = %v, want %v\n%s", got, want, body)
	}
	for _, line := range []string{"      A: \"1\"\n", "      C: null\n"} {
		if !strings.Contains(string(body), line) {
			t.Errorf("the assembled file does not carry %q:\n%s", line, body)
		}
	}
}

// TestWrittenNull_OverlayServiceKeyCarriedAsNull lays an overlay writing a
// service's own key as null over a source that gives it a value. The key is
// emitted as `user: null` at its place among the service's keys, where the decode
// alone would have left it out.
func TestWrittenNull_OverlayServiceKeyCarriedAsNull(t *testing.T) {
	t.Parallel()
	source := `services:
  api:
    image: base:1
    user: "1000"
    working_dir: /srv
`
	values := map[string]any{"services": map[string]any{"api": map[string]any{"user": nil}}}
	body, svc := renderSourcedAndAssemble(t, source, values, "api")

	if got, want := mappingKeys(svc), []string{"image", "labels", "networks", "user", "working_dir"}; !reflect.DeepEqual(got, want) {
		t.Errorf("service keys = %v, want %v\n%s", got, want, body)
	}
	user := mappingValue(svc, "user")
	if user == nil || user.ShortTag() != "!!null" {
		t.Errorf("user = %+v, want a null\n%s", user, body)
	}
	if !strings.Contains(string(body), "    user: null\n") {
		t.Errorf("the assembled file does not carry `user: null`:\n%s", body)
	}
}

// TestWrittenNull_SourceFileNoValueEntryCarriedAsNull renders a source whose own
// environment entry is written with no value, under an overlay that does not touch
// it. The entry is emitted as `FROM_SHELL: null`, so Compose takes the variable
// from the environment it runs in, and the entry beside it keeps its value.
func TestWrittenNull_SourceFileNoValueEntryCarriedAsNull(t *testing.T) {
	t.Parallel()
	source := `services:
  api:
    image: base:1
    environment:
      FROM_SHELL:
      KEPT: "x"
`
	values := map[string]any{"services": map[string]any{"api": map[string]any{"image": "overlay:2"}}}
	body, svc := renderSourcedAndAssemble(t, source, values, "api")

	got := mappingPairs(mappingValue(svc, "environment"))
	want := []writtenNullPair{{"FROM_SHELL", "!!null", "null"}, {"KEPT", "!!str", "x"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("environment = %v, want %v\n%s", got, want, body)
	}
	if !strings.Contains(string(body), "      FROM_SHELL: null\n") {
		t.Errorf("the assembled file does not carry `FROM_SHELL: null`:\n%s", body)
	}
}

// TestWrittenNull_ValueSetByTheMergeIsEmitted writes `networks: null` on a
// service. The merge attaches the service to its network, which is a value of
// the service's own, so the key is emitted once, with that value, and not as
// null.
func TestWrittenNull_ValueSetByTheMergeIsEmitted(t *testing.T) {
	t.Parallel()
	source := `services:
  api:
    image: base:1
    networks: null
`
	body, svc := renderSourcedAndAssemble(t, source, nil, "api")

	if got, want := mappingKeys(svc), []string{"image", "labels", "networks"}; !reflect.DeepEqual(got, want) {
		t.Errorf("service keys = %v, want %v\n%s", got, want, body)
	}
	networks := mappingValue(svc, "networks")
	if networks == nil || networks.Kind != yaml.SequenceNode || len(networks.Content) != 1 || networks.Content[0].ShortTag() != "!!str" {
		t.Errorf("networks = %+v, want the one network the merge attaches\n%s", networks, body)
	}
}

// TestWrittenNull_ServiceKeyRecordFollowsTheDecoder decodes services whose keys a
// YAML merge key (`<<`) brings in. A key is recorded as written null exactly when
// the decoder gives its field nothing: a value written in place wins over a merged
// null, a merged null with nothing in place is a null, and the earlier of two
// merged mappings wins. Each row also reads the user the decoder gave the field,
// so a row whose record and decode disagree fails. An extension member written as
// null is carried as its own value rather than recorded.
func TestWrittenNull_ServiceKeyRecordFollowsTheDecoder(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, service string
		want          []string
		wantUser      string
	}{
		{"null written in place", `user: null
image: a:1`, []string{"user"}, ""},
		{"value in place wins over a merged null", `<<: {user: null}
user: "1000"`, nil, "1000"},
		{"merged null with nothing in place", `<<: {user: null}
image: a:1`, []string{"user"}, ""},
		{"earlier merged mapping wins", `<<: [{user: null}, {user: "1000"}]`, []string{"user"}, ""},
		{"earlier merged value wins over a later null", `<<: [{user: "1000"}, {user: null}]`, nil, "1000"},
		{"tilde and no value are null", `user: ~
working_dir:`, []string{"user", "working_dir"}, ""},
		{"extension member is not recorded", `x-note: null
image: a:1`, nil, ""},
		{"null environment entry is not a service key", `environment:
  A: null`, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := "services:\n  api:\n" + indentLines(tc.service, "    ")
			frag, err := decodeFragment([]byte(body), "app", `fragment "probe"`)
			if err != nil {
				t.Fatalf("decodeFragment: %v\n%s", err, body)
			}
			svc := frag.Services["api"]
			if !reflect.DeepEqual(svc.writtenNull, tc.want) {
				t.Errorf("written null = %v, want %v\n%s", svc.writtenNull, tc.want, body)
			}
			if svc.User != tc.wantUser {
				t.Errorf("decoded user = %q, want %q\n%s", svc.User, tc.wantUser, body)
			}
		})
	}
}

// indentLines prefixes every line of s with prefix and ends it with a newline.
func indentLines(s, prefix string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		b.WriteString(prefix + line + "\n")
	}
	return b.String()
}
