// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"encoding/json"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// A workload crosses two decode surfaces — a yaml decode at recipe parse and an
// encoding/json round-trip on the render payload — so every property below is
// asserted on both. A shape accepted on one surface and not the other would be
// dropped on the crossing, which is the failure the custom codecs exist to
// prevent.

func TestPortSpec_AcceptsBothAuthoredForms(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		yamlDoc  string
		jsonDoc  string
		wantPort int
		wantProt string
	}{
		{
			name:     "the bare number declares a port and no transport",
			yamlDoc:  "web: 8080\n",
			jsonDoc:  `{"web":8080}`,
			wantPort: 8080,
			wantProt: "",
		},
		{
			name:     "the mapping form declares a port and its transport",
			yamlDoc:  "dns:\n  port: 53\n  protocol: UDP\n",
			jsonDoc:  `{"dns":{"port":53,"protocol":"UDP"}}`,
			wantPort: 53,
			wantProt: "UDP",
		},
		{
			name:     "the mapping form without a transport means what the bare number means",
			yamlDoc:  "web:\n  port: 8080\n",
			jsonDoc:  `{"web":{"port":8080}}`,
			wantPort: 8080,
			wantProt: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var fromYAML OrderedMap[PortSpec]
			if err := yaml.Unmarshal([]byte(tc.yamlDoc), &fromYAML); err != nil {
				t.Fatalf("yaml decode: %v", err)
			}
			var fromJSON OrderedMap[PortSpec]
			if err := json.Unmarshal([]byte(tc.jsonDoc), &fromJSON); err != nil {
				t.Fatalf("json decode: %v", err)
			}
			for surface, m := range map[string]OrderedMap[PortSpec]{"yaml": fromYAML, "json": fromJSON} {
				if m.Len() != 1 {
					t.Fatalf("%s: expected one entry, got %d", surface, m.Len())
				}
				got, _ := m.Get(m.Keys()[0])
				if got.Port != tc.wantPort {
					t.Errorf("%s: port = %d, want %d", surface, got.Port, tc.wantPort)
				}
				if got.Protocol != tc.wantProt {
					t.Errorf("%s: protocol = %q, want %q", surface, got.Protocol, tc.wantProt)
				}
			}
		})
	}
}

// TestPortSpec_NoTransportMarshalsAsTheBareNumber is the additivity property,
// and it is the reason a workload authored before the transport existed
// produces the same bytes on both surfaces afterwards. It is asserted on the
// emitted bytes rather than on a re-decoded value, because a value that
// round-trips still moves the payload if it round-trips through a different
// shape.
func TestPortSpec_NoTransportMarshalsAsTheBareNumber(t *testing.T) {
	t.Parallel()
	var m OrderedMap[PortSpec]
	m.Set("web", PortSpec{Port: 8080})

	gotJSON, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("json encode: %v", err)
	}
	if string(gotJSON) != `{"web":8080}` {
		t.Errorf("json = %s, want {\"web\":8080}", gotJSON)
	}

	gotYAML, err := yaml.Marshal(m)
	if err != nil {
		t.Fatalf("yaml encode: %v", err)
	}
	if strings.TrimSpace(string(gotYAML)) != "web: 8080" {
		t.Errorf("yaml = %q, want \"web: 8080\"", strings.TrimSpace(string(gotYAML)))
	}
}

func TestPortSpec_TransportMarshalsAsTheMappingForm(t *testing.T) {
	t.Parallel()
	var m OrderedMap[PortSpec]
	m.Set("dns", PortSpec{Port: 53, Protocol: "UDP"})

	gotJSON, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("json encode: %v", err)
	}
	if string(gotJSON) != `{"dns":{"port":53,"protocol":"UDP"}}` {
		t.Errorf("json = %s", gotJSON)
	}

	gotYAML, err := yaml.Marshal(m)
	if err != nil {
		t.Fatalf("yaml encode: %v", err)
	}
	var back OrderedMap[PortSpec]
	if err := yaml.Unmarshal(gotYAML, &back); err != nil {
		t.Fatalf("yaml re-decode: %v", err)
	}
	got, _ := back.Get("dns")
	if got.Port != 53 || got.Protocol != "UDP" {
		t.Errorf("yaml round-trip lost the declaration: %+v (encoded: %s)", got, gotYAML)
	}
}

// TestPortSpec_RefusesWhatTheSchemaRefuses keeps the typed entry point from
// accepting a document the authored surface rejects. Each row carries one
// fault, so a row cannot start passing because some other refusal fired first.
func TestPortSpec_RefusesWhatTheSchemaRefuses(t *testing.T) {
	t.Parallel()
	// The YAML refusal is read by the author of the document, so it names what
	// they wrote; the JSON one is read on the render-payload crossing only, so it
	// is held to the refusal alone.
	cases := []struct {
		name     string
		yamlDoc  string
		jsonDoc  string
		wantYAML string
		wantJSON string
	}{
		{
			name:     "a key the mapping form does not declare",
			yamlDoc:  "dns:\n  port: 53\n  transport: UDP\n",
			jsonDoc:  `{"dns":{"port":53,"transport":"UDP"}}`,
			wantYAML: `line 3: unexpected key "transport"; a port is a number, or a map of port and protocol`,
			wantJSON: "unexpected key",
		},
		{
			name:     "a sequence is neither a number nor a mapping",
			yamlDoc:  "dns:\n  - 53\n",
			jsonDoc:  `{"dns":[53]}`,
			wantYAML: "line 2: a list is written where a number or a map is expected; a port is a number, or a map of port and protocol",
			wantJSON: "PortSpec",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var fromYAML OrderedMap[PortSpec]
			err := yaml.Unmarshal([]byte(tc.yamlDoc), &fromYAML)
			if err == nil {
				t.Errorf("yaml: accepted %q", tc.yamlDoc)
			} else if !strings.Contains(err.Error(), tc.wantYAML) {
				t.Errorf("yaml: error %q does not mention %q", err, tc.wantYAML)
			} else if strings.Contains(err.Error(), "PortSpec") {
				t.Errorf("yaml: error %q names the program's type, which the author never wrote", err)
			}
			var fromJSON OrderedMap[PortSpec]
			err = json.Unmarshal([]byte(tc.jsonDoc), &fromJSON)
			if err == nil {
				t.Errorf("json: accepted %q", tc.jsonDoc)
			} else if !strings.Contains(err.Error(), tc.wantJSON) {
				t.Errorf("json: error %q does not mention %q", err, tc.wantJSON)
			}
		})
	}
}

// TestPortAndResourceRefusalsNameWhatTheAuthorWrote holds every refusal a port or
// a resource quantity raises while a Workload is read to the author's words: the
// key, the line, the shape written and the shape the position takes, and neither
// the program's own type names nor the decoder's text. One fault per row.
func TestPortAndResourceRefusalsNameWhatTheAuthorWrote(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		doc  string
		into func() any
		want string
	}{
		{
			name: "a port written as text",
			doc:  "http: abc\n",
			into: func() any { return &OrderedMap[PortSpec]{} },
			want: `under key "http" (line 1): line 1: text ` + "`abc`" + ` is written where a number is expected; a port is a number, or a map of port and protocol`,
		},
		{
			name: "a port number written as text",
			doc:  "http:\n  port: abc\n",
			into: func() any { return &OrderedMap[PortSpec]{} },
			want: `under key "http" (line 2): under key "port" (line 2): line 2: text ` + "`abc`" + ` is written where a number is expected`,
		},
		{
			name: "a protocol written as a list",
			doc:  "http:\n  port: 80\n  protocol: [TCP]\n",
			into: func() any { return &OrderedMap[PortSpec]{} },
			want: `under key "http" (line 2): under key "protocol" (line 3): line 3: a list is written where text is expected`,
		},
		{
			name: "a port mapping key written as a list",
			doc:  "http:\n  ? [port]\n  : 80\n",
			into: func() any { return &OrderedMap[PortSpec]{} },
			want: `under key "http" (line 2): line 2: reading a key: line 2: a list is written where text is expected`,
		},
		{
			name: "a resource quantity written as a list",
			doc:  "cpu: [1]\n",
			into: func() any { return &ResourceList{} },
			want: `under key "cpu" (line 1): line 1: a list is written where text is expected`,
		},
		{
			name: "a resource key written as a list",
			doc:  "? [cpu]\n: 1\n",
			into: func() any { return &ResourceList{} },
			want: `line 1: reading a key: line 1: a list is written where text is expected`,
		},
		{
			name: "resources written as a list",
			doc:  "- 1\n",
			into: func() any { return &ResourceList{} },
			want: `line 1: a list is written where a map is expected`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("decoding panicked: %v", r)
					}
				}()
				err = yaml.Unmarshal([]byte(tc.doc), tc.into())
			}()
			if err == nil {
				t.Fatalf("accepted %q", tc.doc)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not say %q", err, tc.want)
			}
			for _, leak := range []string{"PortSpec", "ResourceList", "cannot unmarshal", "!!"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("error %q carries %q, which names nothing the author wrote", err, leak)
				}
			}
		})
	}
}
