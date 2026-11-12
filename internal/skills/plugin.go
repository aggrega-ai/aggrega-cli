// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package skills

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/haltref"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The public skills repository is also a plugin: an assistant that installs
// plugins takes the shipped skills from its skills folder and connects the
// aggrega MCP server from a manifest at its root. Each assistant reads its own
// manifest format, so the tree carries one manifest per format. Every value a
// manifest states is declared once below and every manifest reads it from
// there; the manifests differ only in the shape their format gives those
// values.

// codeHost is the address of the code host the public skills repository and its
// owner live on, ending in a separator so a path appends directly.
const codeHost = "https://github.com/"

// repositoryOwner is the organisation that owns the public skills repository on
// its code host, the name a user types before the repository's.
const repositoryOwner = "aggrega-ai"

// repositorySlug is the public skills repository as its code host names it,
// owner and repository, which is the form several assistants' install commands
// take.
const repositorySlug = repositoryOwner + "/aggrega-skills"

// repositoryURL is the public skills repository's address.
const repositoryURL = codeHost + repositorySlug

// pluginName is the name the plugin is installed and listed under.
//
// It is the same word as the skill the plugin carries, InstalledSkill, but it
// names a different thing — the package an assistant installs rather than one
// skill inside it — and nothing requires the two to agree, so it is declared on
// its own.
const pluginName = "aggrega"

// marketplaceName is the name of the one-plugin marketplace the repository
// declares, which the assistants that install by marketplace add first and then
// name after the plugin: aggrega@aggrega.
const marketplaceName = "aggrega"

// pluginDisplayName is the plugin's name as an assistant shows it to a person,
// where its format has a field for one.
const pluginDisplayName = "Aggrega"

// pluginDescription is the one sentence every manifest and marketplace entry
// describes the plugin with.
const pluginDescription = "Gives your assistant the aggrega skill and the tools of the aggrega MCP server it " +
	"connects; the aggrega binary must already be on your PATH."

// pluginHomepagePath is the page about the plugin and the skill, as a path in
// the documentation tree.
const pluginHomepagePath = "ai-assistants/skill.md"

// pluginHomepage returns the address of the published page about the plugin
// and the skill, composed from pluginHomepagePath.
func pluginHomepage() string { return haltref.PublishedURL(pluginHomepagePath) }

// pluginLicense is the SPDX identifier of the licence the published tree
// carries, which is this module's own LICENSE file, and pluginLicenseName is
// that licence's name as a reader is told it. The two name one licence, so
// they change together.
const (
	pluginLicense     = "Apache-2.0"
	pluginLicenseName = "the Apache License, Version 2.0"
)

// pluginAuthor is who the plugin manifests name as the plugin's author: the
// company that writes Aggrega, as its copyright lines name it, with the
// product's address.
var pluginAuthor = pluginPerson{Name: "Cognitio s.r.l.", URL: "https://aggrega.ai"}

// marketplaceOwner is who a marketplace file names as the marketplace's owner:
// the organisation that publishes the repository the marketplace is read from,
// by the name a user types, with its address on the code host. It is not the
// author, which names who writes the plugin.
var marketplaceOwner = pluginPerson{Name: repositoryOwner, URL: codeHost + repositoryOwner}

// The MCP server every manifest connects: the aggrega binary started with the
// single argument mcp, speaking the protocol over its standard input and
// output. The command is the bare name, found on the PATH, because no plugin
// format installs the binary and its location differs from one machine to the
// next.
const (
	mcpServerName = "aggrega"
	mcpCommand    = "aggrega"
	mcpArgument   = "mcp"
)

// MCPServerCommand returns the command a client runs to start the aggrega MCP
// server, as every plugin manifest writes it: the executable's name, and the
// arguments that select the server among the executable's commands.
func MCPServerCommand() (name string, args []string) {
	return mcpCommand, []string{mcpArgument}
}

// mcpServerCommandLine returns MCPServerCommand as a reader types it.
func mcpServerCommandLine() string {
	name, args := MCPServerCommand()
	return strings.Join(append([]string{name}, args...), " ")
}

// geminiExtensionVersion is the version Gemini CLI requires its extension
// manifest to state. It is the version of the extension package's own shape,
// not of the Aggrega release the tree is generated from: a release number here
// would change the committed tree at every release, and an extension installed
// from the repository's address is updated by comparing commits, not by this
// value.
const geminiExtensionVersion = "1.0.0"

// The paths, from the tree's root, of the files each format reads.
const (
	agentPluginManifestPath   = "plugin.json"
	agentPluginMCPPath        = "mcp.json"
	claudePluginManifestPath  = ".claude-plugin/plugin.json"
	claudeMarketplacePath     = ".claude-plugin/marketplace.json"
	codexMarketplacePath      = ".agents/plugins/marketplace.json"
	geminiExtensionPath       = "gemini-extension.json"
	antigravityManifestPath   = ".antigravity-plugin/plugin.json"
	antigravityMCPConfigPath  = "mcp_config.json"
	agentPluginManifestSchema = "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json"
	agentPluginMCPSchema      = "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json"
	antigravityManifestSchema = "https://antigravity.google/schemas/v1/plugin.json"
)

// pluginPerson is a person or organisation as the manifests name one.
type pluginPerson struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// mcpServer is one entry of a manifest's server list. Type is written only by
// the format that requires it.
type mcpServer struct {
	Type    string   `json:"type,omitempty"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// agentPluginManifest is the root plugin.json of the Agent Plugins format.
type agentPluginManifest struct {
	Schema      string       `json:"$schema"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Author      pluginPerson `json:"author"`
	Homepage    string       `json:"homepage"`
	Repository  string       `json:"repository"`
	License     string       `json:"license"`
}

// agentPluginMCP is the root mcp.json of the Agent Plugins format.
type agentPluginMCP struct {
	Schema     string                       `json:"$schema"`
	MCPServers *types.OrderedMap[mcpServer] `json:"mcpServers"`
}

// claudePluginManifest is Claude Code's plugin manifest, which carries its
// server list inline.
type claudePluginManifest struct {
	Name        string                       `json:"name"`
	Description string                       `json:"description"`
	Author      pluginPerson                 `json:"author"`
	Homepage    string                       `json:"homepage"`
	Repository  string                       `json:"repository"`
	License     string                       `json:"license"`
	MCPServers  *types.OrderedMap[mcpServer] `json:"mcpServers"`
}

// claudeMarketplace is Claude Code's marketplace file, listing the one plugin
// found at the marketplace's own root.
type claudeMarketplace struct {
	Name        string                   `json:"name"`
	Description string                   `json:"description"`
	Owner       pluginPerson             `json:"owner"`
	Plugins     []claudeMarketplaceEntry `json:"plugins"`
}

type claudeMarketplaceEntry struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Description string `json:"description"`
}

// codexMarketplace is Codex's marketplace file, listing the one plugin found at
// the marketplace's own root.
type codexMarketplace struct {
	Name      string                  `json:"name"`
	Interface codexInterface          `json:"interface"`
	Plugins   []codexMarketplaceEntry `json:"plugins"`
}

type codexInterface struct {
	DisplayName string `json:"displayName"`
}

type codexMarketplaceEntry struct {
	Name     string      `json:"name"`
	Source   codexSource `json:"source"`
	Policy   codexPolicy `json:"policy"`
	Category string      `json:"category"`
}

type codexSource struct {
	Source string `json:"source"`
	Path   string `json:"path"`
}

type codexPolicy struct {
	Installation   string `json:"installation"`
	Authentication string `json:"authentication"`
}

// geminiExtension is Gemini CLI's extension manifest, which carries its server
// list inline.
type geminiExtension struct {
	Name        string                       `json:"name"`
	Version     string                       `json:"version"`
	Description string                       `json:"description"`
	MCPServers  *types.OrderedMap[mcpServer] `json:"mcpServers"`
}

// antigravityManifest is Antigravity's plugin manifest, which takes precedence
// there over the root plugin.json.
type antigravityManifest struct {
	Schema      string       `json:"$schema"`
	Name        string       `json:"name"`
	DisplayName string       `json:"displayName"`
	Description string       `json:"description"`
	Author      pluginPerson `json:"author"`
	Homepage    string       `json:"homepage"`
	Repository  string       `json:"repository"`
	License     string       `json:"license"`
}

// mcpServerList is a server-list file holding nothing but the list, which is
// the shape Antigravity reads its servers from.
type mcpServerList struct {
	MCPServers *types.OrderedMap[mcpServer] `json:"mcpServers"`
}

// aggregaServers returns a server list holding the aggrega MCP server alone,
// its entry carrying the transport type where transport is not empty.
func aggregaServers(transport string) *types.OrderedMap[mcpServer] {
	name, args := MCPServerCommand()
	servers := types.NewOrderedMap[mcpServer]()
	servers.Set(mcpServerName, mcpServer{Type: transport, Command: name, Args: args})
	return servers
}

// pluginFiles returns the plugin manifests of the public skills repository,
// one per format, each as JSON indented by two spaces and ending in a newline.
// The order of the keys in each file is the order of its type's fields, so the
// bytes are the same on every run.
func pluginFiles() ([]File, error) {
	homepage := pluginHomepage()
	manifests := []struct {
		path string
		v    any
	}{
		{agentPluginManifestPath, agentPluginManifest{
			Schema:      agentPluginManifestSchema,
			Name:        pluginName,
			Description: pluginDescription,
			Author:      pluginAuthor,
			Homepage:    homepage,
			Repository:  repositoryURL,
			License:     pluginLicense,
		}},
		{agentPluginMCPPath, agentPluginMCP{
			Schema: agentPluginMCPSchema,
			// The Agent Plugins server schema requires a local server to state
			// its transport.
			MCPServers: aggregaServers("stdio"),
		}},
		{claudePluginManifestPath, claudePluginManifest{
			Name:        pluginName,
			Description: pluginDescription,
			Author:      pluginAuthor,
			Homepage:    homepage,
			Repository:  repositoryURL,
			License:     pluginLicense,
			MCPServers:  aggregaServers(""),
		}},
		{claudeMarketplacePath, claudeMarketplace{
			Name:        marketplaceName,
			Description: pluginDescription,
			Owner:       marketplaceOwner,
			Plugins:     []claudeMarketplaceEntry{{Name: pluginName, Source: "./", Description: pluginDescription}},
		}},
		{codexMarketplacePath, codexMarketplace{
			Name:      marketplaceName,
			Interface: codexInterface{DisplayName: pluginDisplayName},
			Plugins: []codexMarketplaceEntry{{
				Name:     pluginName,
				Source:   codexSource{Source: "local", Path: "./"},
				Policy:   codexPolicy{Installation: "AVAILABLE", Authentication: "ON_INSTALL"},
				Category: "Developer Tools",
			}},
		}},
		{geminiExtensionPath, geminiExtension{
			Name:        pluginName,
			Version:     geminiExtensionVersion,
			Description: pluginDescription,
			MCPServers:  aggregaServers(""),
		}},
		{antigravityManifestPath, antigravityManifest{
			Schema:      antigravityManifestSchema,
			Name:        pluginName,
			DisplayName: pluginDisplayName,
			Description: pluginDescription,
			Author:      pluginAuthor,
			Homepage:    homepage,
			Repository:  repositoryURL,
			License:     pluginLicense,
		}},
		{antigravityMCPConfigPath, mcpServerList{MCPServers: aggregaServers("")}},
	}
	out := make([]File, 0, len(manifests))
	for _, m := range manifests {
		content, err := json.MarshalIndent(m.v, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("encode the plugin manifest %s: %w", m.path, err)
		}
		out = append(out, File{Path: m.path, Content: append(content, '\n')})
	}
	return out, nil
}
