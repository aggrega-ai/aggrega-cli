// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Go → schema alignment for the daemon's wire types.
//
// The wire shape is declared twice: once as the JSON-tagged Go structs this
// package marshals, and once as the shipped serve-wire schema. Neither is
// generated from the other, so nothing holds them together except this file.
// A field added to a struct here and not to the schema is the failure this file
// exists to catch, and the drift gate over generated code cannot see it: there
// is nothing to regenerate, because no generator produces these structs.
//
// Two mechanisms, and the second is what keeps the first honest:
//
//   - The SUBJECT SET is read from this package's own source at test time
//     rather than listed. Every non-test struct declaration carrying a json
//     tag must resolve to a position in the shipped schema, or be named in
//     wireSubjectExclusions with the reason it has none. A wire type added and
//     forgotten fails here rather than being checked by nothing.
//   - DIRECTION Go → schema: a maximally-populated instance of each subject
//     marshals and validates against that schema position. The schema declares
//     additionalProperties:false almost everywhere, so a Go field the schema
//     does not admit surfaces as a validation failure naming the key.
//
// The schema → Go direction is not here. It needs the two fields that sit on
// the wire and in no params struct, which is a separate mechanism.
//
// # What this file takes from the entity-type alignment suite, and what it does
// not
//
// The entity suite in pkg/types is the model, and two of its properties rule
// out a straight reuse: it reads YAML tags, while these structs carry only json
// tags, and its subjects are a different package. What carries over is the
// SHAPE — a contract whose subject coverage is itself asserted, with every
// exclusion carrying a reason and a staleness guard so the list cannot rot.
// Every mechanic is its own: the population is parsed from source rather than
// read off a declared vocabulary, the instances are built by reflection rather
// than hand-written, and the validation addresses a position inside one shared
// schema rather than one schema per kind.
//
// # Why the instances are reflected rather than written out
//
// The entity suite hand-writes one maximal value per kind, which is tractable
// at a dozen kinds and is not at this surface's population. It would also be
// the wrong instrument here: a hand-written maximal stops being maximal the
// moment somebody adds a field, and stops silently — which is the failure this
// file exists to prevent. A reflected instance covers a new field the moment it
// is declared.
//
// # Why the subject set is derived from json tags and not from the method table
//
// Four methods are dispatched outside the routing table, so a subject set taken
// from that table would under-cover by exactly those four and nothing would say
// so. Reading the tags instead sidesteps that by construction rather than by an
// exclusion list somebody has to remember to maintain.
package serve

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// wirePackageDir is this package's own source directory. A Go test runs with
// its package directory as the working directory, so the subject population is
// read from the same source the compiler just built.
const wirePackageDir = "."

// wireSchemaResource is the in-memory identity the shipped schema is compiled
// under. It is an addressing detail: the schema's own $id is a published URL,
// and compiling under it would invite a resolver to reach for the network.
const wireSchemaResource = "mem://serve-wire.json"

// wireSubject is one struct checked against one position in the schema.
//
// A type needing more than one row is a type whose schema position makes two
// field groups mutually exclusive — a response carries an answer or an error
// and never both — so no single instance can populate the whole Go surface and
// still validate. Each row populates one arm, and the coverage gate below
// requires the arms between them to leave no field unpopulated, which is what
// stops skip being a way to hide a field from this direction.
type wireSubject struct {
	// name is the Go type's declared name, matched against the parsed
	// population.
	name string
	// typ is the type a maximal instance is built from.
	typ reflect.Type
	// def is a JSON pointer into the shipped schema naming the position this
	// type's marshalled form must validate against.
	def string
	// skip names Go fields left at their zero value on this arm.
	skip []string
	// values fixes the JSON value of a field whose Go type carries no shape a
	// reflected value could satisfy — an interface, or raw JSON — at a schema
	// position that does constrain the shape.
	values map[string]any
	// why states the reason for skip or values, and is required whenever
	// either is set.
	why string
}

// wireSubjectExclusions names each json-tagged struct with no position of its
// own in the schema, mapped to the reason. A struct may be excluded only by
// name, so a wire type added and forgotten fails the coverage gate rather than
// disappearing into a wildcard.
var wireSubjectExclusions = map[string]string{
	"ConditionCodes": "embedded into Diagnostic without a tag, so its keys marshal onto Diagnostic itself rather than under a nested object; the schema declares them as Diagnostic properties and the Diagnostic subject covers them",
}

// wireSubjects is every json-tagged struct in this package, paired with the
// schema position its marshalled form must validate against.
//
// Most rows map a Go name to the same name under $defs. The rest are here
// because the schema spells the shape differently, or declares it inline rather
// than as a named definition — the pointer says which, and it is checked
// against the shipped schema, so a row naming a position that is not there
// fails rather than silently checking nothing.
func wireSubjects() []wireSubject {
	return []wireSubject{
		{name: "AttachCandidatesResult", typ: reflect.TypeOf(AttachCandidatesResult{}), def: "#/$defs/AttachCandidatesResult"},
		{name: "AttachmentContainerResult", typ: reflect.TypeOf(AttachmentContainerResult{}), def: "#/$defs/AttachmentContainerResult"},
		{name: "CatalogEntity", typ: reflect.TypeOf(CatalogEntity{}), def: "#/$defs/CatalogEntity"},
		{name: "ClosureEntity", typ: reflect.TypeOf(ClosureEntity{}), def: "#/$defs/ClosureEntity"},
		{name: "CodeDescription", typ: reflect.TypeOf(CodeDescription{}), def: "#/$defs/CodeDescription"},
		{name: "CompletionItem", typ: reflect.TypeOf(CompletionItem{}), def: "#/$defs/CompletionItem"},
		{name: "ConnectionAtPositionResult", typ: reflect.TypeOf(ConnectionAtPositionResult{}), def: "#/$defs/ConnectionAtPositionResult"},
		{name: "ConnectionCandidatesResult", typ: reflect.TypeOf(ConnectionCandidatesResult{}), def: "#/$defs/ConnectionCandidatesResult"},
		{name: "ConnectionCoordinate", typ: reflect.TypeOf(ConnectionCoordinate{}), def: "#/$defs/ConnectionCoordinate"},
		{name: "Diagnostic", typ: reflect.TypeOf(Diagnostic{}), def: "#/$defs/Diagnostic"},
		{name: "InsertionPointResult", typ: reflect.TypeOf(InsertionPointResult{}), def: "#/$defs/InsertionPointResult"},
		{name: "MarkupContent", typ: reflect.TypeOf(MarkupContent{}), def: "#/$defs/MarkupContent"},
		{name: "SlotCandidacy", typ: reflect.TypeOf(SlotCandidacy{}), def: "#/$defs/SlotCandidacy"},
		{name: "SocketCandidacy", typ: reflect.TypeOf(SocketCandidacy{}), def: "#/$defs/SocketCandidacy"},
		{name: "SourcedEntityRef", typ: reflect.TypeOf(SourcedEntityRef{}), def: "#/$defs/SourcedEntityRef"},
		{name: "WorkspaceEntityRef", typ: reflect.TypeOf(WorkspaceEntityRef{}), def: "#/$defs/WorkspaceEntityRef"},
		{name: "activeReleaseResult", typ: reflect.TypeOf(activeReleaseResult{}), def: "#/$defs/ActiveReleaseResult"},
		{name: "attachCandidatesParams", typ: reflect.TypeOf(attachCandidatesParams{}), def: "#/$defs/AttachCandidatesParams"},
		{name: "attachmentContainerParams", typ: reflect.TypeOf(attachmentContainerParams{}), def: "#/$defs/AttachmentContainerParams"},
		{name: "buildNotice", typ: reflect.TypeOf(buildNotice{}), def: "#/$defs/BuildNotice"},
		{name: "buildParams", typ: reflect.TypeOf(buildParams{}), def: "#/$defs/BuildParams"},
		{name: "buildPreviewParams", typ: reflect.TypeOf(buildPreviewParams{}), def: "#/$defs/BuildPreviewParams"},
		{name: "buildPreviewResult", typ: reflect.TypeOf(buildPreviewResult{}), def: "#/$defs/BuildPreviewResult"},
		{name: "buildResult", typ: reflect.TypeOf(buildResult{}), def: "#/$defs/BuildResult"},
		{name: "cacheClassResult", typ: reflect.TypeOf(cacheClassResult{}), def: "#/$defs/CacheClass"},
		{name: "cacheClearResult", typ: reflect.TypeOf(cacheClearResult{}), def: "#/$defs/CacheClearResult"},
		{name: "cacheClearedResult", typ: reflect.TypeOf(cacheClearedResult{}), def: "#/$defs/CacheCleared"},
		{name: "cacheElementResult", typ: reflect.TypeOf(cacheElementResult{}), def: "#/$defs/CacheElement"},
		{name: "cacheEvictParams", typ: reflect.TypeOf(cacheEvictParams{}), def: "#/$defs/CacheEvictParams"},
		{name: "cacheEvictResult", typ: reflect.TypeOf(cacheEvictResult{}), def: "#/$defs/CacheEvictResult"},
		{name: "cacheProvenanceResult", typ: reflect.TypeOf(cacheProvenanceResult{}), def: "#/$defs/CacheProvenance"},
		{name: "cachePruneResult", typ: reflect.TypeOf(cachePruneResult{}), def: "#/$defs/CachePruneResult"},
		{name: "cacheRemovalResult", typ: reflect.TypeOf(cacheRemovalResult{}), def: "#/$defs/CacheRemoval"},
		{name: "cacheScope", typ: reflect.TypeOf(cacheScope{}), def: "#/$defs/CacheScope"},
		{name: "cacheShortfallResult", typ: reflect.TypeOf(cacheShortfallResult{}), def: "#/$defs/CacheShortfall"},
		{name: "cacheStatusResult", typ: reflect.TypeOf(cacheStatusResult{}), def: "#/$defs/CacheStatusResult"},
		{name: "cancelParams", typ: reflect.TypeOf(cancelParams{}), def: "#/$defs/CancelRequestParams"},
		{name: "catalogIndexParams", typ: reflect.TypeOf(catalogIndexParams{}), def: "#/$defs/CatalogIndexParams"},
		{name: "catalogIndexResult", typ: reflect.TypeOf(catalogIndexResult{}), def: "#/$defs/CatalogIndexResult"},
		{name: "clientCapabilities", typ: reflect.TypeOf(clientCapabilities{}), def: "#/$defs/ClientCapabilities"},
		{name: "completionContext", typ: reflect.TypeOf(completionContext{}), def: "#/$defs/CompletionContext"},
		{name: "completionParams", typ: reflect.TypeOf(completionParams{}), def: "#/$defs/CompletionParams"},
		{name: "completionResult", typ: reflect.TypeOf(completionResult{}), def: "#/$defs/CompletionResult"},
		{name: "configCaptureParams", typ: reflect.TypeOf(configCaptureParams{}), def: "#/$defs/ConfigCaptureParams"},
		{name: "configCaptureResult", typ: reflect.TypeOf(configCaptureResult{}), def: "#/$defs/ConfigCaptureResult"},
		{name: "configCapturedAttachment", typ: reflect.TypeOf(configCapturedAttachment{}), def: "#/$defs/ConfigCapturedAttachment"},
		{name: "configCapturedSocket", typ: reflect.TypeOf(configCapturedSocket{}), def: "#/$defs/ConfigCapturedSocket"},
		{name: "configInitParams", typ: reflect.TypeOf(configInitParams{}), def: "#/$defs/ConfigInitParams"},
		{name: "configInitResult", typ: reflect.TypeOf(configInitResult{}), def: "#/$defs/ConfigInitResult"},
		{name: "configWriteScope", typ: reflect.TypeOf(configWriteScope{}), def: "#/$defs/ConfigWriteScope"},
		{name: "connectionAtPositionParams", typ: reflect.TypeOf(connectionAtPositionParams{}), def: "#/$defs/ConnectionAtPositionParams"},
		{name: "connectionCandidatesParams", typ: reflect.TypeOf(connectionCandidatesParams{}), def: "#/$defs/ConnectionCandidatesParams"},
		{name: "credentialStoreScope", typ: reflect.TypeOf(credentialStoreScope{}), def: "#/$defs/CredentialStoreScope"},
		{name: "dependencyResolutionReport", typ: reflect.TypeOf(dependencyResolutionReport{}), def: "#/$defs/DependencyResolutionReport"},
		{name: "deprecateArtifactParams", typ: reflect.TypeOf(deprecateArtifactParams{}), def: "#/$defs/DeprecateArtifactParams"},
		{name: "deprecateArtifactResult", typ: reflect.TypeOf(deprecateArtifactResult{}), def: "#/$defs/DeprecateArtifactResult"},
		{name: "deprecationMarked", typ: reflect.TypeOf(deprecationMarked{}), def: "#/$defs/DeprecationMarked"},
		{name: "diagnosticsParams", typ: reflect.TypeOf(diagnosticsParams{}), def: "#/$defs/DiagnosticsParams"},
		{name: "diagnosticsResult", typ: reflect.TypeOf(diagnosticsResult{}), def: "#/$defs/DiagnosticsResult"},
		{name: "documentEditParams", typ: reflect.TypeOf(documentEditParams{}), def: "#/$defs/EditDocumentParams"},
		{name: "documentEditResult", typ: reflect.TypeOf(documentEditResult{}), def: "#/$defs/EditDocumentResult"},
		{name: "effectiveConfigurationResult", typ: reflect.TypeOf(effectiveConfigurationResult{}), def: "#/$defs/EffectiveConfigurationResult"},
		{name: "effectiveConfigurationSetting", typ: reflect.TypeOf(effectiveConfigurationSetting{}), def: "#/$defs/EffectiveConfigurationSetting"},
		{name: "entityDefinitionParams", typ: reflect.TypeOf(entityDefinitionParams{}), def: "#/$defs/EntityDefinitionParams"},
		{name: "entityDefinitionResult", typ: reflect.TypeOf(entityDefinitionResult{}), def: "#/$defs/EntityDefinitionResult"},
		{name: "entityKindAcceptance", typ: reflect.TypeOf(entityKindAcceptance{}), def: "#/$defs/EntityKindAcceptance"},
		{name: "entityKindDescriptor", typ: reflect.TypeOf(entityKindDescriptor{}), def: "#/$defs/EntityKindDescriptor"},
		{name: "errorData", typ: reflect.TypeOf(errorData{}), def: "#/$defs/ErrorData"},
		{name: "explainErrorEntry", typ: reflect.TypeOf(explainErrorEntry{}), def: "#/$defs/ExplainErrorEntry"},
		{name: "explainErrorParams", typ: reflect.TypeOf(explainErrorParams{}), def: "#/$defs/ExplainErrorParams"},
		{name: "explainErrorResult", typ: reflect.TypeOf(explainErrorResult{}), def: "#/$defs/ExplainErrorResult"},
		{name: "findImplementationsParams", typ: reflect.TypeOf(findImplementationsParams{}), def: "#/$defs/FindImplementationsParams"},
		{name: "findImplementationsResult", typ: reflect.TypeOf(findImplementationsResult{}), def: "#/$defs/FindImplementationsResult"},
		{name: "findReferencesParams", typ: reflect.TypeOf(findReferencesParams{}), def: "#/$defs/FindReferencesParams"},
		{name: "findReferencesResult", typ: reflect.TypeOf(findReferencesResult{}), def: "#/$defs/FindReferencesResult"},
		{
			name: "generalCapabilities", typ: reflect.TypeOf(generalCapabilities{}),
			def: "#/$defs/ClientCapabilities/properties/general",
		},
		{name: "getSchemaParams", typ: reflect.TypeOf(getSchemaParams{}), def: "#/$defs/GetSchemaParams"},
		{name: "getSchemaResult", typ: reflect.TypeOf(getSchemaResult{}), def: "#/$defs/GetSchemaResult"},
		{name: "hoverParams", typ: reflect.TypeOf(hoverParams{}), def: "#/$defs/HoverParams"},
		{name: "hoverResult", typ: reflect.TypeOf(hoverResult{}), def: "#/$defs/HoverResult"},
		{name: "importAnswered", typ: reflect.TypeOf(importAnswered{}), def: "#/$defs/ImportAnswered"},
		{name: "importDiagnostic", typ: reflect.TypeOf(importDiagnostic{}), def: "#/$defs/ImportDiagnostic"},
		{name: "importDrift", typ: reflect.TypeOf(importDrift{}), def: "#/$defs/ImportDrift"},
		{name: "importOwed", typ: reflect.TypeOf(importOwed{}), def: "#/$defs/ImportOwed"},
		{name: "importParams", typ: reflect.TypeOf(importParams{}), def: "#/$defs/ImportParams"},
		{name: "importResult", typ: reflect.TypeOf(importResult{}), def: "#/$defs/ImportResult"},
		{name: "importedFile", typ: reflect.TypeOf(importedFile{}), def: "#/$defs/ImportedFile"},
		{name: "initializeParams", typ: reflect.TypeOf(initializeParams{}), def: "#/$defs/InitializeParams"},
		{name: "initializeResult", typ: reflect.TypeOf(initializeResult{}), def: "#/$defs/InitializeResult"},
		{name: "insertionPointParams", typ: reflect.TypeOf(insertionPointParams{}), def: "#/$defs/InsertionPointParams"},
		{name: "invalidateParams", typ: reflect.TypeOf(invalidateParams{}), def: "#/$defs/InvalidateParams"},
		{name: "invalidateResult", typ: reflect.TypeOf(invalidateResult{}), def: "#/$defs/InvalidateResult"},
		{name: "irGraphParams", typ: reflect.TypeOf(irGraphParams{}), def: "#/$defs/IrGraphParams"},
		{name: "irGraphResult", typ: reflect.TypeOf(irGraphResult{}), def: "#/$defs/IrGraphResult"},
		{name: "jsonRPCError", typ: reflect.TypeOf(jsonRPCError{}), def: "#/$defs/ErrorObject"},
		{
			name: "jsonRPCNotification", typ: reflect.TypeOf(jsonRPCNotification{}),
			def: "#/$defs/DaemonNotification",
			values: map[string]any{
				"Method": "aggrega/publishDiagnostics",
				"Params": map[string]any{"file": "/w/a.yaml", "diagnostics": []any{}},
			},
			why: "the notification envelope's params shape is fixed by the method it names, so the pair has to be a real one; the arm chosen is the diagnostics push, and a method or params shape the schema stops admitting fails here",
		},
		{
			name: "jsonRPCRequest", typ: reflect.TypeOf(jsonRPCRequest{}),
			def: "#/$defs/Request",
			values: map[string]any{
				"Method": "aggrega/invalidate",
				"Params": map[string]any{"file": "/w/a.yaml", "workspaceRoot": "/w"},
			},
			why: "the request envelope's params shape is fixed by the method it names, so the pair has to be a real one; the arm chosen is the invalidate request, and a method or params shape the schema stops admitting fails here",
		},
		{name: "lintFileFindings", typ: reflect.TypeOf(lintFileFindings{}), def: "#/$defs/LintFileFindings"},
		{name: "lintParams", typ: reflect.TypeOf(lintParams{}), def: "#/$defs/LintParams"},
		{name: "lintPass", typ: reflect.TypeOf(lintPass{}), def: "#/$defs/LintPass"},
		{name: "lintResult", typ: reflect.TypeOf(lintResult{}), def: "#/$defs/LintResult"},
		{name: "logoutParams", typ: reflect.TypeOf(logoutParams{}), def: "#/$defs/LogoutParams"},
		{name: "logoutResult", typ: reflect.TypeOf(logoutResult{}), def: "#/$defs/LogoutResult"},
		{name: "pinClassCount", typ: reflect.TypeOf(pinClassCount{}), def: "#/$defs/PinClassCount"},
		{name: "pinParams", typ: reflect.TypeOf(pinParams{}), def: "#/$defs/PinParams"},
		{name: "pinResult", typ: reflect.TypeOf(pinResult{}), def: "#/$defs/PinResult"},
		{name: "pluginAddParams", typ: reflect.TypeOf(pluginAddParams{}), def: "#/$defs/PluginAddParams"},
		{name: "pluginAddResult", typ: reflect.TypeOf(pluginAddResult{}), def: "#/$defs/PluginAddResult"},
		{name: "pluginGenCatalogParams", typ: reflect.TypeOf(pluginGenCatalogParams{}), def: "#/$defs/PluginGenCatalogParams"},
		{name: "pluginGenCatalogResult", typ: reflect.TypeOf(pluginGenCatalogResult{}), def: "#/$defs/PluginGenCatalogResult"},
		{name: "pluginGeneratedDocument", typ: reflect.TypeOf(pluginGeneratedDocument{}), def: "#/$defs/PluginGeneratedDocument"},
		{name: "pluginRemoveParams", typ: reflect.TypeOf(pluginRemoveParams{}), def: "#/$defs/PluginRemoveParams"},
		{name: "pluginRemoveResult", typ: reflect.TypeOf(pluginRemoveResult{}), def: "#/$defs/PluginRemoveResult"},
		{name: "pluginRetraction", typ: reflect.TypeOf(pluginRetraction{}), def: "#/$defs/PluginRetraction"},
		{name: "pluginRetractionExclusion", typ: reflect.TypeOf(pluginRetractionExclusion{}), def: "#/$defs/PluginRetractionExclusion"},
		{name: "pluginRetractionProvenance", typ: reflect.TypeOf(pluginRetractionProvenance{}), def: "#/$defs/PluginRetractionProvenance"},
		{name: "pluginSyncParams", typ: reflect.TypeOf(pluginSyncParams{}), def: "#/$defs/PluginSyncParams"},
		{name: "pluginSyncResult", typ: reflect.TypeOf(pluginSyncResult{}), def: "#/$defs/PluginSyncResult"},
		{name: "pluginWriteScope", typ: reflect.TypeOf(pluginWriteScope{}), def: "#/$defs/PluginWriteScope"},
		{name: "progressBegin", typ: reflect.TypeOf(progressBegin{}), def: "#/$defs/ProgressBegin"},
		{name: "progressEnd", typ: reflect.TypeOf(progressEnd{}), def: "#/$defs/ProgressEnd"},
		{
			name: "progressParams", typ: reflect.TypeOf(progressParams{}),
			def:    "#/$defs/ProgressParams",
			values: map[string]any{"Value": map[string]any{"kind": "end"}},
			why:    "the report a progress notification carries is one of three shapes the schema names, and the reflected value satisfies none of them; the closing report is the arm chosen because it is the one shape with no fields of its own",
		},
		{name: "progressReport", typ: reflect.TypeOf(progressReport{}), def: "#/$defs/ProgressReport"},
		{name: "publicationReach", typ: reflect.TypeOf(publicationReach{}), def: "#/$defs/PublicationReach"},
		{name: "publishArtifactParams", typ: reflect.TypeOf(publishArtifactParams{}), def: "#/$defs/PublishArtifactParams"},
		{name: "publishArtifactResult", typ: reflect.TypeOf(publishArtifactResult{}), def: "#/$defs/PublishArtifactResult"},
		{name: "publishDiagnosticsParams", typ: reflect.TypeOf(publishDiagnosticsParams{}), def: "#/$defs/PublishDiagnosticsParams"},
		{name: "publishEntityParams", typ: reflect.TypeOf(publishEntityParams{}), def: "#/$defs/PublishEntityParams"},
		{name: "publishEntityResult", typ: reflect.TypeOf(publishEntityResult{}), def: "#/$defs/PublishEntityResult"},
		{name: "publishedArtifact", typ: reflect.TypeOf(publishedArtifact{}), def: "#/$defs/PublishedArtifact"},
		{name: "registryAddParams", typ: reflect.TypeOf(registryAddParams{}), def: "#/$defs/RegistryAddParams"},
		{name: "registryAddResult", typ: reflect.TypeOf(registryAddResult{}), def: "#/$defs/RegistryAddResult"},
		{name: "registryListResult", typ: reflect.TypeOf(registryListResult{}), def: "#/$defs/RegistryListResult"},
		{name: "registryRemoveParams", typ: reflect.TypeOf(registryRemoveParams{}), def: "#/$defs/RegistryRemoveParams"},
		{name: "registryRemoveResult", typ: reflect.TypeOf(registryRemoveResult{}), def: "#/$defs/RegistryRemoveResult"},
		{name: "registrySearchIndex", typ: reflect.TypeOf(registrySearchIndex{}), def: "#/$defs/RegistrySearchIndex"},
		{name: "registrySearchParams", typ: reflect.TypeOf(registrySearchParams{}), def: "#/$defs/SearchParams"},
		{name: "registrySearchRecord", typ: reflect.TypeOf(registrySearchRecord{}), def: "#/$defs/RegistrySearchRecord"},
		{name: "registrySearchResult", typ: reflect.TypeOf(registrySearchResult{}), def: "#/$defs/SearchResult"},
		{name: "registryUpdateParams", typ: reflect.TypeOf(registryUpdateParams{}), def: "#/$defs/RegistryUpdateParams"},
		{name: "registryUpdateResult", typ: reflect.TypeOf(registryUpdateResult{}), def: "#/$defs/RegistryUpdateResult"},
		{name: "releaseCandidatesResult", typ: reflect.TypeOf(releaseCandidatesResult{}), def: "#/$defs/ReleaseCandidatesResult"},
		{name: "releaseClosureParams", typ: reflect.TypeOf(releaseClosureParams{}), def: "#/$defs/ReleaseClosureParams"},
		{name: "releaseClosureResult", typ: reflect.TypeOf(releaseClosureResult{}), def: "#/$defs/ReleaseClosureResult"},
		{name: "resolveDependenciesParams", typ: reflect.TypeOf(resolveDependenciesParams{}), def: "#/$defs/ResolveDependenciesParams"},
		{name: "resolveDependenciesResult", typ: reflect.TypeOf(resolveDependenciesResult{}), def: "#/$defs/ResolveDependenciesResult"},
		{name: "resolveParams", typ: reflect.TypeOf(resolveParams{}), def: "#/$defs/ResolveParams"},
		{name: "resolveResult", typ: reflect.TypeOf(resolveResult{}), def: "#/$defs/ResolveResult"},
		{name: "scaffoldEntityParams", typ: reflect.TypeOf(scaffoldEntityParams{}), def: "#/$defs/ScaffoldEntityParams"},
		{name: "scaffoldEntityResult", typ: reflect.TypeOf(scaffoldEntityResult{}), def: "#/$defs/ScaffoldEntityResult"},
		{name: "scaffoldExample", typ: reflect.TypeOf(scaffoldExample{}), def: "#/$defs/ScaffoldExample"},
		{name: "serverCapabilities", typ: reflect.TypeOf(serverCapabilities{}), def: "#/$defs/ServerCapabilities"},
		{name: "setActiveReleaseParams", typ: reflect.TypeOf(setActiveReleaseParams{}), def: "#/$defs/SetActiveReleaseParams"},
		{name: "signArtifactsParams", typ: reflect.TypeOf(signArtifactsParams{}), def: "#/$defs/SignArtifactsParams"},
		{name: "signArtifactsResult", typ: reflect.TypeOf(signArtifactsResult{}), def: "#/$defs/SignArtifactsResult"},
		{name: "signedArtifact", typ: reflect.TypeOf(signedArtifact{}), def: "#/$defs/SignedArtifact"},
		{name: "skillFolderScope", typ: reflect.TypeOf(skillFolderScope{}), def: "#/$defs/SkillFolderScope"},
		{name: "skillsInstallParams", typ: reflect.TypeOf(skillsInstallParams{}), def: "#/$defs/SkillsInstallParams"},
		{name: "skillsInstallResult", typ: reflect.TypeOf(skillsInstallResult{}), def: "#/$defs/SkillsInstallResult"},
		{name: "skillsInstalledFolder", typ: reflect.TypeOf(skillsInstalledFolder{}), def: "#/$defs/SkillsInstalledFolder"},
		{name: "skillsListResult", typ: reflect.TypeOf(skillsListResult{}), def: "#/$defs/SkillsListResult"},
		{name: "skillsListedAgent", typ: reflect.TypeOf(skillsListedAgent{}), def: "#/$defs/SkillsListedAgent"},
		{name: "skillsListedSkill", typ: reflect.TypeOf(skillsListedSkill{}), def: "#/$defs/SkillsListedSkill"},
		{name: "toolchainCorePluginRow", typ: reflect.TypeOf(toolchainCorePluginRow{}), def: "#/$defs/ToolchainCorePlugin"},
		{name: "toolchainReport", typ: reflect.TypeOf(toolchainReport{}), def: "#/$defs/ToolchainReport"},
		{name: "validateParams", typ: reflect.TypeOf(validateParams{}), def: "#/$defs/ValidateParams"},
		{name: "validateResult", typ: reflect.TypeOf(validateResult{}), def: "#/$defs/ValidateResult"},
		{
			name: "windowCapabilities", typ: reflect.TypeOf(windowCapabilities{}),
			def: "#/$defs/ClientCapabilities/properties/window",
		},
		{name: "workDoneProgressParams", typ: reflect.TypeOf(workDoneProgressParams{}), def: "#/$defs/WorkspaceAndProgressParams"},
		{name: "workspaceParams", typ: reflect.TypeOf(workspaceParams{}), def: "#/$defs/WorkspaceAndProgressParams"},

		// The response envelope carries an answer or a failure and never both,
		// and the schema says so by declaring two shapes rather than one with
		// optional halves. One arm each, and between them every field is
		// populated.
		{
			name: "jsonRPCResponse", typ: reflect.TypeOf(jsonRPCResponse{}),
			def: "#/$defs/SuccessResponse", skip: []string{"Error"},
			values: map[string]any{"Result": map[string]any{"invalidated": true}},
			why:    "the successful arm, which the schema forbids an error on; the answer it carries has to be one of the result shapes the schema names, and the invalidate answer is the arm chosen because it is the smallest of them",
		},
		{
			name: "jsonRPCResponse", typ: reflect.TypeOf(jsonRPCResponse{}),
			def: "#/$defs/ErrorResponse", skip: []string{"Result"},
			why: "the failing arm, which the schema forbids an answer on",
		},
	}
}

// TestWireSubjectSetAccountsForEveryTaggedStruct is the gate that keeps the
// subject set from going quiet.
//
// It reads this package's struct declarations from source and requires every
// one carrying a json tag to be either a subject or a named exclusion. That is
// what makes the table below safe to hand-maintain: a wire type added and
// forgotten is a failure here, not a silent omission, and the reverse
// directions keep the table from rotting the other way — a row or an exclusion
// naming a struct that no longer exists is reported rather than sitting there
// suppressing nothing.
//
// Test files are excluded from the population deliberately. A struct declared
// in one is a fixture: it models a wire shape for an assertion's benefit and is
// not itself part of the contract, so requiring it to resolve to a schema
// position would oblige the schema to declare test scaffolding.
func TestWireSubjectSetAccountsForEveryTaggedStruct(t *testing.T) {
	t.Parallel()

	declared := taggedStructFields(t, wirePackageDir)

	subjects := wireSubjects()
	covered := map[string]bool{}
	for _, s := range subjects {
		covered[s.name] = true
	}

	var unaccounted []string
	for name := range declared {
		_, excluded := wireSubjectExclusions[name]
		if !covered[name] && !excluded {
			unaccounted = append(unaccounted, name)
		}
		if covered[name] && excluded {
			t.Errorf("%s is claimed both by a subject row and by the exclusion set; remove the exclusion", name)
		}
	}
	sort.Strings(unaccounted)
	if len(unaccounted) > 0 {
		t.Errorf("%d json-tagged struct(s) in this package are checked against the wire schema by nothing: %v\n"+
			"each is either a wire type — add a row to wireSubjects() naming the schema position it must "+
			"validate against — or a shape that has no position of its own, in which case add it to "+
			"wireSubjectExclusions with the reason", len(unaccounted), unaccounted)
	}

	for _, s := range subjects {
		if _, ok := declared[s.name]; !ok {
			t.Errorf("wireSubjects() carries a row for %s, which this package no longer declares as a "+
				"json-tagged struct; remove the row", s.name)
		}
	}
	for name, reason := range wireSubjectExclusions {
		if _, ok := declared[name]; !ok {
			t.Errorf("stale exclusion %q — this package no longer declares such a json-tagged struct; "+
				"remove the exclusion", name)
			continue
		}
		if strings.TrimSpace(reason) == "" {
			t.Errorf("exclusion %q carries no reason; an unexplained exclusion is indistinguishable from "+
				"an oversight", name)
		}
	}

	// Every arm's skip and values must name a field that is really there, and
	// between them the arms must leave no field unpopulated. Without the second
	// half, skip would be a way to take a field out of this direction's reach
	// while the gate still reported green.
	populated := map[string]map[string]bool{}
	for _, s := range subjects {
		fields := declared[s.name]
		if fields == nil {
			continue
		}
		if (len(s.skip) > 0 || len(s.values) > 0) && strings.TrimSpace(s.why) == "" {
			t.Errorf("%s (%s) skips or fixes a field without saying why", s.name, s.def)
		}
		skipped := map[string]bool{}
		for _, f := range s.skip {
			if !fields[f] {
				t.Errorf("%s (%s) skips field %q, which is not a json-tagged field of that type", s.name, s.def, f)
			}
			skipped[f] = true
		}
		for f := range s.values {
			if !fields[f] {
				t.Errorf("%s (%s) fixes the value of field %q, which is not a json-tagged field of that type", s.name, s.def, f)
			}
		}
		if populated[s.name] == nil {
			populated[s.name] = map[string]bool{}
		}
		for f := range fields {
			if !skipped[f] {
				populated[s.name][f] = true
			}
		}
	}
	for name, fields := range declared {
		if !covered[name] {
			continue
		}
		var never []string
		for f := range fields {
			if !populated[name][f] {
				never = append(never, f)
			}
		}
		sort.Strings(never)
		if len(never) > 0 {
			t.Errorf("%s: field(s) %v are skipped by every arm, so this direction never marshals them and "+
				"a schema that stopped admitting them would not be noticed; give one arm a shape that "+
				"populates them", name, never)
		}
	}
}

// TestWireShapeGoToSchema is the Go-field-not-in-schema detector.
//
// A maximally-populated instance of each subject marshals and validates against
// the position the shipped schema declares for it. The schema sets
// additionalProperties:false on every shape that has one, so a field added to a
// struct here and not to the schema fails with the key named. That is the
// defect this file exists for, and it is the one the drift gate over generated
// code structurally cannot catch, because no generator produces these structs.
//
// The instance is maximal rather than representative on purpose: a field left
// at its zero value with an omitempty tag marshals to nothing at all, so a
// half-populated instance would validate while saying nothing about the fields
// it omitted.
//
// A field the session reads off the raw request before it routes is the one
// thing a params struct cannot carry, so where a position requires such a field
// the instance is given it before validating (supplyBoundaryFields). The rule is
// the dispatch-boundary rule the schema → Go direction derives, and it is held
// live: a schema in which no subject's position requires such a field fails,
// because a supply with no occupant is a rule that has stopped being true without
// anything saying so. That check reads the schema in this function's own body
// rather than counting supplies inside the subtests, so a -run filter selecting
// a subset of subjects cannot make it report a live rule as dead.
func TestWireShapeGoToSchema(t *testing.T) {
	t.Parallel()

	doc, compiler := compileWireSchema(t)
	boundary := wireBoundaryPropertyNames(t, doc)

	// Every position is compiled here, in one goroutine, before the subjects
	// run. The compiler keeps a resource cache that is not safe for concurrent
	// use, so compiling inside a parallel subject is a data race rather than a
	// slow start.
	subjects := wireSubjects()
	positions := map[string]map[string]any{}
	compiled := map[string]*jsonschema.Schema{}
	for _, s := range subjects {
		if _, done := compiled[s.def]; done {
			continue
		}
		position, err := resolveSchemaPointer(doc, s.def)
		if err != nil {
			t.Fatalf("%s: %v — the row names a position the shipped schema does not declare, so it "+
				"would check nothing", s.name, err)
		}
		schema, err := compiler.Compile(wireSchemaResource + s.def)
		if err != nil {
			t.Fatalf("%s: compile %s: %v", s.name, s.def, err)
		}
		positions[s.def] = position
		compiled[s.def] = schema
	}

	occupants := 0
	for _, position := range positions {
		occupants += supplyBoundaryFields(map[string]any{}, position, boundary)
	}
	if occupants == 0 {
		t.Errorf("no subject's position requires a field the session reads at the routing boundary, so " +
			"supplyBoundaryFields has no occupant. Either no request params position requires one any " +
			"longer, in which case the supply is dead and goes, or the derivation is reading the wrong " +
			"position")
	}

	for _, s := range subjects {
		name := s.name + " against " + s.def
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			instance := maximalWireValue(t, s)
			body, err := json.Marshal(instance)
			if err != nil {
				t.Fatalf("%s: marshal the maximal instance: %v", s.name, err)
			}

			var decoded any
			if err := json.Unmarshal(body, &decoded); err != nil {
				t.Fatalf("%s: decode the marshalled instance: %v (%s)", s.name, err, body)
			}
			decoded = conformConstants(decoded, positions[s.def], doc)
			supplyBoundaryFields(decoded, positions[s.def], boundary)

			if err := compiled[s.def].Validate(decoded); err != nil {
				conformed, _ := json.Marshal(decoded)
				t.Errorf("%s: a maximally-populated instance does not validate against %s — a Go field the "+
					"schema does not admit, or a shape the two sides no longer agree on:\n%v\n--- marshalled ---\n%s",
					s.name, s.def, err, conformed)
			}
		})
	}
}

// TestIrGraphParamsDeclaresTheRevealTheSchemaPublishes is the schema → Go
// direction for the one field a client sends to ask for the build graph's
// withheld values in the clear.
//
// The direction above cannot see this field go missing. It builds the Go value
// and validates it against the schema, so a request shape that stopped declaring
// the field still validates — the schema merely offers a property nobody sends.
// The daemon would then refuse that property on every request carrying it,
// because it accepts exactly the fields its request shape declares, and a client
// typed through the published binding would be told its reveal does not exist.
// So the field is read off the schema and looked for in the shape the daemon
// decodes into: declared, optional, a boolean, and accepted.
func TestIrGraphParamsDeclaresTheRevealTheSchemaPublishes(t *testing.T) {
	t.Parallel()

	const def = "#/$defs/IrGraphParams"
	name := wireOptionalProperty(t, def, "unredacted")

	position, err := resolveSchemaPointer(wireSchemaDocument(t), def+"/properties/"+name)
	if err != nil {
		t.Fatalf("%s: %v", def, err)
	}
	if position["type"] != "boolean" {
		t.Errorf("%s declares %q as %v; the reveal is a yes-or-no request and the daemon decodes a boolean", def, name, position["type"])
	}
	if !declaredParamFields(irGraphParams{})[name] {
		t.Errorf("the build-graph request shape declares no %q, so the daemon refuses the field the published contract offers its clients", name)
	}
}

// taggedStructFields parses every non-test Go file in dir and returns each
// named struct declaration carrying at least one json tag, mapped to the Go
// field names that carry one.
//
// Reading the population from source is what makes the subject set
// self-asserting: Go cannot enumerate a package's types reflectively, so the
// alternative is a list nobody is obliged to update. A parse that recognises
// nothing fails outright rather than certifying an empty population, because a
// silently empty read would make every assertion above pass by covering
// nothing.
//
// Every file in the directory is parsed, whatever build constraint it carries.
// That is deliberate rather than incidental: a wire type declared behind a tag
// is still a wire type, and a population that moved with the tags the test
// happened to be built under would cover a different set on a different build.
func taggedStructFields(t *testing.T, dir string) map[string]map[string]bool {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	out := map[string]map[string]bool{}
	parsed := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		parsed++
		ast.Inspect(file, func(n ast.Node) bool {
			spec, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := spec.Type.(*ast.StructType)
			if !ok {
				return true
			}
			fields := map[string]bool{}
			for _, field := range st.Fields.List {
				if field.Tag == nil || len(field.Names) == 0 {
					continue
				}
				tag, err := strconv.Unquote(field.Tag.Value)
				if err != nil {
					t.Fatalf("%s: field tag %s does not unquote: %v", spec.Name.Name, field.Tag.Value, err)
				}
				value := reflect.StructTag(tag).Get("json")
				if value == "" || value == "-" || strings.HasPrefix(value, "-,") {
					continue
				}
				fields[field.Names[0].Name] = true
			}
			if len(fields) > 0 {
				out[spec.Name.Name] = fields
			}
			return true
		})
	}

	if parsed == 0 || len(out) == 0 {
		t.Fatalf("%s: parsed %d file(s) and found no json-tagged struct declarations, so the wire-type "+
			"population could not be read and every assertion resting on it would certify nothing; the "+
			"read is looking at the wrong directory or the declarations changed shape", dir, parsed)
	}
	return out
}

// wireSchemaDocument returns the shipped schema as a decoded document.
//
// It is the one decode every assertion in this package shares. The wire-shape
// assertions address a position out of the same document the alignment
// directions walk, so a second decode beside it would be a second in-memory
// copy of the contract — one that agrees today and stops agreeing the moment
// somebody points one of them at a different embedded variable.
func wireSchemaDocument(t *testing.T) map[string]any {
	t.Helper()

	var doc map[string]any
	if err := json.Unmarshal([]byte(schemas.ServeWireV1beta1), &doc); err != nil {
		t.Fatalf("the shipped serve-wire schema does not decode as JSON: %v", err)
	}
	return doc
}

// compileWireSchema returns the shipped schema as a decoded document and a
// compiler holding it, so a subject can address any position inside it.
func compileWireSchema(t *testing.T) (map[string]any, *jsonschema.Compiler) {
	t.Helper()

	doc := wireSchemaDocument(t)

	resource, err := jsonschema.UnmarshalJSON(bytes.NewReader([]byte(schemas.ServeWireV1beta1)))
	if err != nil {
		t.Fatalf("the shipped serve-wire schema is not a usable schema resource: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(wireSchemaResource, resource); err != nil {
		t.Fatalf("add the serve-wire schema as a resource: %v", err)
	}
	return doc, compiler
}

// resolveSchemaPointer walks a document-local JSON pointer and returns the
// subschema at it.
func resolveSchemaPointer(doc map[string]any, pointer string) (map[string]any, error) {
	rest, ok := strings.CutPrefix(pointer, "#/")
	if !ok {
		return nil, fmt.Errorf("%q is not a document-local JSON pointer", pointer)
	}
	var current any = doc
	for _, token := range strings.Split(rest, "/") {
		token = strings.ReplaceAll(token, "~1", "/")
		token = strings.ReplaceAll(token, "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%q: %q is not reachable, the path leaves the object tree", pointer, token)
		}
		next, ok := object[token]
		if !ok {
			return nil, fmt.Errorf("%q: no member %q", pointer, token)
		}
		current = next
	}
	object, ok := current.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%q does not name a subschema object", pointer)
	}
	return object, nil
}

// maximalWireValue builds an instance of the subject's type with every field
// populated, honouring the arm's skips and fixed values.
func maximalWireValue(t *testing.T, s wireSubject) any {
	t.Helper()

	value := reflect.New(s.typ).Elem()
	skipped := map[string]bool{}
	for _, name := range s.skip {
		skipped[name] = true
	}
	fillStruct(t, value, skipped, s.values, map[reflect.Type]bool{s.typ: true}, 0)
	return value.Interface()
}

// rawMessageType is the raw-JSON field type the wire types use for values whose
// shape is decided elsewhere.
var rawMessageType = reflect.TypeOf(json.RawMessage(nil))

// wirePlaceholder is the value every reflected string position takes. It is
// deliberately one value rather than a per-field one: nothing here asserts on
// the content, and a varying placeholder would read as though something did.
const wirePlaceholder = "wire-alignment"

// fillStruct populates every exported field of a struct value.
func fillStruct(t *testing.T, value reflect.Value, skipped map[string]bool, values map[string]any, path map[reflect.Type]bool, depth int) {
	t.Helper()

	typ := value.Type()
	for i := range typ.NumField() {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		if skipped[field.Name] {
			continue
		}
		if fixed, ok := values[field.Name]; ok {
			assignFixed(t, value.Field(i), field.Name, fixed)
			continue
		}
		fillValue(t, value.Field(i), path, depth+1)
	}
}

// assignFixed writes a fixed value into a field, encoding it first where the
// field carries raw JSON.
func assignFixed(t *testing.T, field reflect.Value, name string, fixed any) {
	t.Helper()

	if field.Type() == rawMessageType {
		encoded, err := json.Marshal(fixed)
		if err != nil {
			t.Fatalf("encode the fixed value for %s: %v", name, err)
		}
		field.Set(reflect.ValueOf(json.RawMessage(encoded)))
		return
	}
	given := reflect.ValueOf(fixed)
	if !given.Type().AssignableTo(field.Type()) {
		t.Fatalf("the fixed value for %s is a %s, which does not fit a %s", name, given.Type(), field.Type())
	}
	field.Set(given)
}

// fillValue populates one value with a non-zero instance of whatever it holds.
//
// Non-zero matters: a field carrying omitempty and left at its zero value
// marshals to nothing, so a zero-filled instance would validate while covering
// none of the fields it dropped.
//
// Recursion stops when a struct type reappears on the path, which leaves the
// recurring position at its zero value. A shape that contains itself has no
// finite maximal instance, and the alternative — a bound that silently
// truncated — is worse than leaving the position empty and saying so here. Only
// the struct case marks the path: marking it from the pointer, slice and map
// cases as well would make a struct behind any of them look like a recursion
// into itself, and every such nested shape would come back empty.
func fillValue(t *testing.T, value reflect.Value, path map[reflect.Type]bool, depth int) {
	t.Helper()

	if depth > 16 {
		return
	}
	typ := value.Type()
	if typ == rawMessageType {
		value.Set(reflect.ValueOf(json.RawMessage(`"` + wirePlaceholder + `"`)))
		return
	}

	switch typ.Kind() {
	case reflect.Bool:
		value.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		value.SetUint(1)
	case reflect.Float32, reflect.Float64:
		value.SetFloat(1)
	case reflect.String:
		value.SetString(wirePlaceholder)
	case reflect.Interface:
		value.Set(reflect.ValueOf(wirePlaceholder))
	case reflect.Pointer:
		if path[typ.Elem()] {
			return
		}
		allocated := reflect.New(typ.Elem())
		fillValue(t, allocated.Elem(), path, depth+1)
		value.Set(allocated)
	case reflect.Slice:
		if path[typ.Elem()] {
			return
		}
		slice := reflect.MakeSlice(typ, 1, 1)
		fillValue(t, slice.Index(0), path, depth+1)
		value.Set(slice)
	case reflect.Array:
		for i := range value.Len() {
			fillValue(t, value.Index(i), path, depth+1)
		}
	case reflect.Map:
		if path[typ.Elem()] {
			return
		}
		m := reflect.MakeMap(typ)
		key := reflect.New(typ.Key()).Elem()
		fillValue(t, key, path, depth+1)
		element := reflect.New(typ.Elem()).Elem()
		fillValue(t, element, path, depth+1)
		m.SetMapIndex(key, element)
		value.Set(m)
	case reflect.Struct:
		if path[typ] {
			return
		}
		path[typ] = true
		defer delete(path, typ)
		fillStruct(t, value, nil, nil, path, depth)
	}
}

// supplyBoundaryFields adds to a decoded instance each field the session reads
// off the raw request at the routing boundary that the position requires and the
// instance does not carry, and reports how many it added.
//
// Such a field has no home on a params struct by construction — the session
// decodes it before it knows which params shape the request has — so a
// reflected instance of the struct never carries it, and a position requiring it
// would refuse every instance for a reason that says nothing about whether the
// struct's own fields agree with the schema. The field is supplied only where the
// position declares it BY REFERENCE to the shared declaration, the same clause
// the schema → Go direction's boundary rule turns on: a position declaring a
// field of that name in place owns it, and its Go type must carry it, so nothing
// is supplied there and a missing field still fails.
func supplyBoundaryFields(instance any, position map[string]any, boundary map[string]bool) int {
	object, ok := instance.(map[string]any)
	if !ok {
		return 0
	}
	required, _ := position["required"].([]any)
	properties, _ := position["properties"].(map[string]any)
	added := 0
	for _, entry := range required {
		name, _ := entry.(string)
		if !boundary[name] {
			continue
		}
		if _, present := object[name]; present {
			continue
		}
		declaration, _ := properties[name].(map[string]any)
		if _, byRef := declaration["$ref"].(string); !byRef {
			continue
		}
		object[name] = wirePlaceholder
		added++
	}
	return added
}

// conformConstants replaces, in a decoded instance, every value the schema
// pins to a single constant.
//
// A reflected value cannot know that a position takes one fixed string, and a
// failure on one says nothing about whether the two sides agree on the field
// SET, which is what this direction is asserting.
//
// Two shapes are substituted: a bare const, and a combinator every one of whose
// branches is a bare const — which is how the schema states a code drawn from a
// fixed list. What is NOT substituted is a combinator whose branches are shapes
// rather than constants, because that is a dispatch a generic walk cannot
// resolve; the subjects whose shape turns on one carry a fixed value in their
// row instead, with the reason.
//
// An enum is deliberately not substituted either, and the reason is the same
// one. Every enum on this schema is a method name, and a method name decides
// which shape the params beside it must take — so choosing one here, with no
// say over the params, would compose a message that names one method and
// carries another's arguments. The envelope subjects fix both together instead.
// An enum arriving anywhere else fails loudly with the position named, which is
// the right outcome: it is a position nobody has decided how to populate.
//
// The bound this draws is worth stating: this direction stops asserting WHICH
// constant a position carries, and goes on asserting which keys are there. A Go
// value drifting to a constant the schema does not list is a value contract and
// is not what this file is for.
func conformConstants(instance any, schema map[string]any, doc map[string]any) any {
	schema = derefSchema(schema, doc, 0)
	if schema == nil {
		return instance
	}
	if fixed, ok := schema["const"]; ok {
		return fixed
	}
	if fixed, ok := closedConstantSet(schema, doc); ok {
		return fixed
	}
	switch value := instance.(type) {
	case map[string]any:
		properties, _ := schema["properties"].(map[string]any)
		additional, _ := schema["additionalProperties"].(map[string]any)
		for key, member := range value {
			if sub, ok := properties[key].(map[string]any); ok {
				value[key] = conformConstants(member, sub, doc)
				continue
			}
			if additional != nil {
				value[key] = conformConstants(member, additional, doc)
			}
		}
		return value
	case []any:
		items, ok := schema["items"].(map[string]any)
		if !ok {
			return value
		}
		for i, member := range value {
			value[i] = conformConstants(member, items, doc)
		}
		return value
	default:
		return instance
	}
}

// closedConstantSet reports the first admissible value of a position the schema
// states as a fixed list of constants, and whether the position is one.
//
// A branch carrying anything beyond its constant — a shape, a further
// combinator — makes the position a dispatch rather than a list, and nothing is
// substituted for it.
func closedConstantSet(schema map[string]any, doc map[string]any) (any, bool) {
	for _, keyword := range []string{"oneOf", "anyOf"} {
		branches, ok := schema[keyword].([]any)
		if !ok || len(branches) == 0 {
			continue
		}
		var first any
		for i, branch := range branches {
			object, ok := branch.(map[string]any)
			if !ok {
				return nil, false
			}
			object = derefSchema(object, doc, 0)
			if object == nil {
				return nil, false
			}
			fixed, ok := object["const"]
			if !ok {
				return nil, false
			}
			if i == 0 {
				first = fixed
			}
		}
		return first, true
	}
	return nil, false
}

// derefSchema follows a chain of document-local $refs, giving up rather than
// looping on a reference that does not resolve.
func derefSchema(schema map[string]any, doc map[string]any, depth int) map[string]any {
	for depth < 16 {
		ref, ok := schema["$ref"].(string)
		if !ok {
			return schema
		}
		resolved, err := resolveSchemaPointer(doc, ref)
		if err != nil {
			return nil
		}
		schema = resolved
		depth++
	}
	return schema
}
