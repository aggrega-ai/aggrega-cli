// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package difyimport is the Dify importer: it reads an application Dify exports
// in its DSL — the YAML document Dify writes for an application — and drafts an
// Aggregate composing the standard library's Dify installation, core/dify, with
// the application carried in the installation's app_definition parameter, which
// the module hands its API server, core/ai-agent/dify, to import at deploy.
//
// The importer guest (plugins/dify-importer) holds the descriptor, the signature
// a Dify DSL is recognised by and the export wiring; everything it does with a
// source is here, so it compiles and is tested on the host toolchain as well.
// It is built on the plugin kit's importer package,
// github.com/aggrega-ai/aggrega-pdk-go/pkg/importer, whose platform draft, in its
// module form, writes the draft.
//
// # What a source holds
//
// A Dify DSL holds the application under app, its kind, app, and the version of
// the DSL it is written in under version; a workflow or chatflow application
// its graph under workflow, with the environment variables it reads, and a chat,
// agent chat or completion application its configuration under model_config; and
// the plugins the application needs under dependencies.
//
// # What it drafts
//
// The draft composes core/dify — the API server, its worker and scheduler, its
// web front end, its sandbox, plugin runtime, egress proxy and stores — as one
// entry of the drafted Aggregate, carrying the application as written in its
// app_definition parameter. The module wires every connection of the API server
// itself, its vector store included, so the draft wires nothing: a DSL names a
// knowledge base by an identifier of the installation it was exported from and
// never names a store. Dify configures the providers of the models and tools an
// application calls inside itself, and core/ai-agent/dify declares no socket for
// a model, so every model, tool, knowledge base and plugin the application names
// is reported, in its decision and in a warning, as configured inside Dify. The
// installation's application secret, which core/dify takes in its secret_key
// parameter with no default, is the Release's to supply: the drafted
// Aggregate declares a required secret_key parameter of its own and passes it
// down to the entry, for the Release to fulfill. The owner account the
// application is imported under is the Release's to name too, and a comment
// above the entry says what the Release supplies.
//
// # Credential values
//
// Dify marks an environment variable as a secret by its value_type, secret, and
// writes its value empty on export unless the export includes secrets. Each
// secret variable, each key an HTTP request node authenticates with, and the
// stored credential a tool or an agent's tool selects, is a value Dify marks as
// a credential; the headers and query parameters of an HTTP request node are
// inputs Dify marks as none that can carry one. Each is handed to the kit's
// credential decision, which reads the empty_credentials setting: by default
// each is carried as written and named in a credential-carried warning, never by
// its value; where the setting writes it empty, this package writes it empty in
// the DSL's own bytes and the kit carries the result only where the host's
// parser reads it back as the DSL's tree with exactly those values emptied.
//
// # The DSL version
//
// A DSL whose version is newer than DSLVersion — the version Dify EngineVersion,
// the engine core/ai-agent/dify runs by default, writes — is reported: Dify holds
// the import of such an application for confirmation, which the step importing it
// at deploy gives.
package difyimport

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
)

// Format is the one format the importer serves: a Dify application DSL.
const Format = "dify"

// Syntax is the syntactic format a source is parsed as.
const Syntax = "yaml"

// The catalog entities a draft names: the installation module it composes, the
// module's parameter carrying the application, the module's parameter carrying
// the application secret, which the draft passes down from a parameter of its
// own, and the API server running it.
const (
	Module              = "core/dify"
	DefinitionParameter = "app_definition"
	SecretKeyParameter  = "secret_key"
	Server              = "core/ai-agent/dify"
)

// secretKeyDescription describes the secret_key parameter a draft declares. It
// says only what the draft does with it and points at the module for the rest:
// what the value is, and what the module does with it, are the catalog's to say,
// in its own description of the parameter. That the module's parameter has no
// default is held to the catalog by TestTheSecretKeyIsTheModulesOwnWithNoDefault.
const secretKeyDescription = "Passed down to " + Module + "'s " + SecretKeyParameter + " parameter, which has no " +
	"default; that parameter's description in " + Module + " says what the value is."

// EngineVersion is the Dify version core/ai-agent/dify runs by default, its
// infra.engine_version, and DSLVersion the version of the DSL that engine
// writes. A test holds EngineVersion to the catalog, so a catalog moving its
// default fails it rather than leaving the comparison behind.
const (
	EngineVersion = "1.17.0"
	DSLVersion    = "0.7.0"
)

// Dify is the format as the kit drafts it: a YAML source, walked by walkTree,
// its draft named after the application's name, run by the Dify installation.
var Dify = importer.Format{
	Name:     Format,
	Syntax:   Syntax,
	Mappings: map[string]importer.Mapping{},
	Walk:     walkTree,
	Title:    appName,
	Platform: &platform,
}

// appName returns the name a Dify application's DSL gives the application, the
// one Dify lists it under: the scalar at app.name, read through readDSL, empty
// where it writes none.
func appName(tree json.RawMessage) string {
	d, ok := readDSL(tree)
	if !ok {
		return ""
	}
	var app struct {
		Name json.RawMessage `json:"name"`
	}
	if json.Unmarshal(d.App, &app) != nil {
		return ""
	}
	name, _ := draftyaml.ScalarText(app.Name)
	return name
}

// platform is the Dify installation: core/dify, running core/ai-agent/dify,
// whose app_definition carries the application.
var platform = importer.Platform{
	Component:       Server,
	DefinitionValue: DefinitionParameter,
	Module: &importer.Module{
		Aggregate: Module,
		Parameter: DefinitionParameter,
		PassedDown: []importer.PassedDownParameter{
			{Name: SecretKeyParameter, Description: secretKeyDescription},
		},
	},
	Credentials:   credentialsOf,
	Configured:    configuredOf,
	EmptyInSource: emptyInSource,
	LeftForRelease: []string{
		"infra.owner_email on the api component of " + Module + ", the email of the installation's owner account the " +
			"application is imported under, set under components.api in the Release's routing entry for this entry",
		"the owner_password slot on that api component, the owner account's password, attached in the same routing " +
			"entry with type core/referenced-secret",
	},
}

// Import turns one import request into the drafts: each source — the request's
// named sources, or its primary bytes — is parsed as YAML through parse, the
// host's parser, and drafted by the kit as the Dify installation carrying it. A
// source whose version is newer than DSLVersion is reported ahead of what the
// kit reports. A request naming a format other than Format reads nothing.
//
// Each source is parsed once however often the kit asks for it, so a request
// costs one parse per source and one per source the empty_credentials setting
// rewrites. Given one request and one parser, it returns byte-identical output.
func Import(in aggrega.ImportInput, parse func(syntax string, source []byte) (json.RawMessage, error)) aggrega.ImportOutput {
	parsed := map[string]parseResult{}
	once := func(syntax string, source []byte) (json.RawMessage, error) {
		key := syntax + "\x00" + string(source)
		if r, ok := parsed[key]; ok {
			return r.tree, r.err
		}
		tree, err := parse(syntax, source)
		parsed[key] = parseResult{tree: tree, err: err}
		return tree, err
	}
	var versions []aggrega.Diagnostic
	if in.Format == Format {
		for _, src := range importer.Sources(in) {
			tree, err := once(Syntax, src.Bytes)
			if err != nil {
				continue
			}
			if d, newer := versionNewer(importer.Named(in, src), tree); newer {
				versions = append(versions, d)
			}
		}
	}
	out := importer.Import(in, Dify, once)
	out.Diagnostics = append(versions, out.Diagnostics...)
	return out
}

// parseResult is what the host's parser answered for one source.
type parseResult struct {
	tree json.RawMessage
	err  error
}

// versionNewer reports a source whose version reads as a version newer than
// DSLVersion. A version that does not read as major.minor.patch is not compared.
func versionNewer(src aggrega.ImportSource, tree json.RawMessage) (aggrega.Diagnostic, bool) {
	var top struct {
		Version json.RawMessage `json:"version"`
	}
	if json.Unmarshal(tree, &top) != nil {
		return aggrega.Diagnostic{}, false
	}
	text, ok := draftyaml.ScalarText(top.Version)
	if !ok {
		return aggrega.Diagnostic{}, false
	}
	got, ok := versionOf(text)
	pin, _ := versionOf(DSLVersion)
	if !ok || !newerThan(got, pin) {
		return aggrega.Diagnostic{}, false
	}
	return aggrega.Diagnostic{
		Severity: aggrega.SeverityWarn,
		Code:     "dsl-version-newer",
		Message: fmt.Sprintf("source %q is written in version %s of the Dify DSL, newer than %s, the version Dify %s "+
			"writes — the engine %s runs by default: Dify holds the import of such an application for confirmation, "+
			"which the step importing it at deploy gives, so check the application against the engine the Release "+
			"runs", src.Name, strconv.Quote(text), DSLVersion, EngineVersion, Server),
		SourceRef: src.Name,
	}, true
}

// versionOf reads text as major.minor.patch, each a decimal number.
func versionOf(text string) ([3]int, bool) {
	var v [3]int
	parts := strings.Split(text, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" || p[0] == '+' || p[0] == '-' {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// newerThan reports whether a is a later version than b.
func newerThan(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}
