// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package difyimport

import (
	"encoding/json"
	"strconv"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// This file locates the values a Dify DSL holds that can carry a credential,
// each of the class the kit's credential decision reads: the value of each
// environment variable Dify marks as a secret, the key an HTTP request node
// authenticates with, and the stored credential a tool node, or a tool of an
// agent node, selects, which Dify marks as credentials; and the headers and the
// query parameters of an HTTP request node, which Dify marks as none and which
// can carry one all the same.

// secretValueType is the value_type Dify gives an environment variable it keeps
// secret.
const secretValueType = "secret"

// workflowNode names the environment variables in a credential report: they
// belong to the workflow rather than to one node of it.
const workflowNode = "workflow"

// The reasons a request's headers and query parameters are written empty whole.
const (
	headersDetail = "a request's headers can carry a key, though Dify marks them as no credential"
	paramsDetail  = "a request's query parameters can carry a key, though Dify marks them as no credential"
)

// credentialsOf locates every value of tree that can carry a credential, in the
// order the DSL writes them: the environment variables first, then each node of
// the workflow's graph.
func credentialsOf(tree json.RawMessage) []importer.Credential {
	var d struct {
		Workflow *struct {
			Graph struct {
				Nodes []struct {
					ID   json.RawMessage            `json:"id"`
					Data map[string]json.RawMessage `json:"data"`
				} `json:"nodes"`
			} `json:"graph"`
			EnvironmentVariables []struct {
				Name      json.RawMessage `json:"name"`
				ValueType json.RawMessage `json:"value_type"`
			} `json:"environment_variables"`
		} `json:"workflow"`
	}
	if json.Unmarshal(tree, &d) != nil || d.Workflow == nil {
		return nil
	}
	var out []importer.Credential
	for i, v := range d.Workflow.EnvironmentVariables {
		if kind, _ := draftyaml.ScalarText(v.ValueType); kind != secretValueType {
			continue
		}
		name, _ := draftyaml.ScalarText(v.Name)
		out = append(out, importer.Credential{Node: workflowNode, Field: "environment_variables." + name,
			Pointer: importer.Pointer("workflow", "environment_variables", strconv.Itoa(i), "value"),
			Class:   importer.CredentialMarked})
	}
	for i, n := range d.Workflow.Graph.Nodes {
		id, _ := draftyaml.ScalarText(n.ID)
		typ, _ := draftyaml.ScalarText(n.Data["type"])
		at := func(tokens ...string) string {
			return importer.Pointer(append([]string{"workflow", "graph", "nodes", strconv.Itoa(i), "data"}, tokens...)...)
		}
		switch typ {
		case "http-request":
			var auth struct {
				Type   json.RawMessage            `json:"type"`
				Config map[string]json.RawMessage `json:"config"`
			}
			_ = json.Unmarshal(n.Data["authorization"], &auth)
			if _, ok := auth.Config["api_key"]; ok {
				out = append(out, importer.Credential{Node: id, Field: "authorization.config.api_key",
					Pointer: at("authorization", "config", "api_key"), Class: importer.CredentialMarked})
			}
			for _, f := range []struct{ field, detail string }{{"headers", headersDetail}, {"params", paramsDetail}} {
				if _, ok := n.Data[f.field]; ok {
					out = append(out, importer.Credential{Node: id, Field: f.field, Pointer: at(f.field),
						Detail: f.detail, Class: importer.CredentialUnmarked})
				}
			}
		case "tool":
			if _, ok := n.Data["credential_id"]; ok {
				out = append(out, importer.Credential{Node: id, Field: "credential_id", Pointer: at("credential_id"),
					Class: importer.CredentialMarked})
			}
		case "agent":
			var params struct {
				Tools struct {
					Value []map[string]json.RawMessage `json:"value"`
				} `json:"tools"`
			}
			_ = json.Unmarshal(n.Data["agent_parameters"], &params)
			for j, tool := range params.Tools.Value {
				if _, ok := tool["credential_id"]; ok {
					out = append(out, importer.Credential{Node: id,
						Field:   "agent_parameters.tools.value." + strconv.Itoa(j) + ".credential_id",
						Pointer: at("agent_parameters", "tools", "value", strconv.Itoa(j), "credential_id"),
						Class:   importer.CredentialMarked})
				}
			}
		}
	}
	return out
}
