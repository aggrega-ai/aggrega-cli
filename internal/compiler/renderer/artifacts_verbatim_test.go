// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// stubVerbatimEngine mirrors the terraform provider's host capabilities: it
// handles artifacts through the external path (ExternalArtifacts) and emits
// them verbatim (VerbatimArtifacts) — byte-for-byte after Go-template
// evaluation, with no YAML parse and no provenance splice. Renderer-package
// tests exercise the production dispatch path against this holder without
// crossing into the terraform package (which would create a circular import).
type stubVerbatimEngine struct{ stubEngine }

func (stubVerbatimEngine) HostCapabilities() HostCapabilities {
	return HostCapabilities{
		ExternalArtifacts: true,
		VerbatimArtifacts: true,
	}
}

// stubExternalNonVerbatimEngine is an external-handler holder with
// VerbatimArtifacts left false — the gate that keeps inline entries rejected
// for a non-verbatim external provider. Same shape as helm-chart's holder for
// the inline-rejection assertion, without the delimiter/placement companions.
type stubExternalNonVerbatimEngine struct{ stubEngine }

func (stubExternalNonVerbatimEngine) HostCapabilities() HostCapabilities {
	return HostCapabilities{ExternalArtifacts: true}
}

// newTerraformArtifactsCtx returns a template context populated with the
// pillars a terraform mariadb-rds artifact body reads: .locals.db_identifier,
// .infra.* sizing, and .values.* developer intent. Mirrors the shape the real
// Phase B pipeline assembles before artifacts evaluation.
func newTerraformArtifactsCtx() (*types.TemplateContext, *PhaseBState, NodeRenderContext) {
	tmplCtx := &types.TemplateContext{
		Context: types.ContextBlock{InstanceID: "db"},
		Values:  types.OrderedMap[any]{},
		Infra:   types.OrderedMap[any]{},
		Locals:  types.OrderedMap[string]{},
	}
	tmplCtx.Locals.Set("db_identifier", "prod-db")
	tmplCtx.Values.Set("port", 3306)
	tmplCtx.Values.Set("database", "appdb")
	tmplCtx.Infra.Set("engine_version", "10.6")
	tmplCtx.Infra.Set("storage", map[string]any{"size_gb": 100, "type": "gp3"})

	r := &types.Recipe{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Recipe",
		Metadata:   types.RecipeMetadata{Name: "mariadb-rds", Version: "1.0.0"},
		Spec:       types.RecipeSpec{Provider: "terraform", Implements: "core/datastore/mariadb:1.0.0"},
	}
	state := NewPhaseBState(r, "db", "production", "recipe.yaml")
	nodeCtx := NodeRenderContext{
		NodeID:          "db",
		BoundaryPath:    "production",
		ProviderName:    "terraform",
		ProviderVersion: "1.0.0",
		Provenance: ProvenanceContext{
			CompilerVersion: "0.1.0",
			AggregateName:   "shop",
			Environment:     "production",
		},
	}
	return tmplCtx, state, nodeCtx
}

// wantVerbatimProvenanceComment is the exact HCL provenance comment block the
// verbatim artifacts path prepends for newTerraformArtifactsCtx's node. It is
// written out literally (not produced by the formatter) so this test
// independently pins the marker, the aggrega.ai/* key set, the deterministic
// alphabetical line order, and the exclusion of the Kubernetes-only
// app.kubernetes.io/name key.
const wantVerbatimProvenanceComment = `# Aggrega build provenance
# aggrega.ai/aggregate-name: shop
# aggrega.ai/boundary-path: production
# aggrega.ai/compiler-version: 0.1.0
# aggrega.ai/component-id: db
# aggrega.ai/environment: production
# aggrega.ai/provider: terraform
# aggrega.ai/provider-version: 1.0.0
# aggrega.ai/recipe-uri: mariadb-rds:1.0.0
`

// verbatimHCLFixture is a compact terraform HCL body exercising the three
// pillar reads a real recipe uses ({{ .locals }}, {{ .infra }}, {{ .values }},
// including a nested map field) plus a Terraform interpolation probe ${...}
// that must survive untouched while the Go-template inside it resolves. This is
// a dedicated fixture, not the shipped mariadb-rds body, so the probe line can
// assert ${...} survival without editing the catalog recipe.
const verbatimHCLFixture = `resource "aws_db_instance" "{{ .locals.db_identifier }}" {
  identifier        = "{{ .locals.db_identifier }}"
  engine            = "mariadb"
  engine_version    = "{{ .infra.engine_version }}"
  port              = {{ .values.port }}
  allocated_storage = {{ .infra.storage.size_gb }}
  storage_type      = "{{ .infra.storage.type }}"
  db_name           = "{{ .values.database }}"
  endpoint_probe    = "${aws_db_instance.{{ .locals.db_identifier }}.endpoint}"
}
`

// verbatimHCLExpected is verbatimHCLFixture with the Go-template actions
// resolved and every other byte preserved — the byte-identity golden.
const verbatimHCLExpected = `resource "aws_db_instance" "prod-db" {
  identifier        = "prod-db"
  engine            = "mariadb"
  engine_version    = "10.6"
  port              = 3306
  allocated_storage = 100
  storage_type      = "gp3"
  db_name           = "appdb"
  endpoint_probe    = "${aws_db_instance.prod-db.endpoint}"
}
`

// TestEvaluateArtifacts_VerbatimEmitsMainTfByteFaithfully drives an inline
// terraform artifact named main.tf through the real host artifacts path and
// asserts the verbatim contract: the file is named main.tf; the Go-template is
// resolved; there is NO YAML round-trip mangling and NO metadata/labels block
// spliced in; the ${...} Terraform interpolation survives untouched; and two
// renders are byte-identical (Deterministic Output Invariant).
func TestEvaluateArtifacts_VerbatimEmitsMainTfByteFaithfully(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newTerraformArtifactsCtx()

	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state.Recipe.Spec.Artifacts.Set("main.tf", types.ArtifactEntry{Inline: verbatimHCLFixture})

	files, err := evaluateArtifacts(context.Background(), tmplCtx, state, stubVerbatimEngine{}, nodeCtx)
	if err != nil {
		t.Fatalf("evaluateArtifacts: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 emitted file, got %d", len(files))
	}
	got := files[0]

	// (a) named main.tf (the author-declared inline map key).
	if got.Name != "main.tf" {
		t.Errorf("Name = %q, want main.tf", got.Name)
	}
	if got.Origin != EmitOriginArtifactsInline {
		t.Errorf("Origin = %v, want EmitOriginArtifactsInline", got.Origin)
	}
	if got.Intermediate {
		t.Errorf("Intermediate = true, want false for a flat verbatim provider")
	}

	// (b) byte-identity golden: the emitted body is exactly the HCL provenance
	// comment block followed by the template-resolved HCL. The comment is
	// prepended above; the HCL below is byte-identical to the pre-provenance
	// golden — no YAML round-trip, no metadata/labels splice.
	wantFull := wantVerbatimProvenanceComment + verbatimHCLExpected
	if string(got.Body) != wantFull {
		t.Errorf("body not emitted as provenance-comment + verbatim HCL.\n--- got ---\n%s\n--- want ---\n%s", string(got.Body), wantFull)
	}
	// The emitted file STARTS with the provenance comment block, showing the
	// provider and version plus the cross-provider identity fields.
	head := string(got.Body)
	if !strings.HasPrefix(head, wantVerbatimProvenanceComment) {
		t.Errorf("body must start with the provenance comment block; head:\n%s", head)
	}
	// The HCL body below the comment is byte-identical to the pre-provenance golden.
	if strings.TrimPrefix(head, wantVerbatimProvenanceComment) != verbatimHCLExpected {
		t.Errorf("HCL below the provenance comment is not byte-identical to the pre-provenance output; got:\n%s", strings.TrimPrefix(head, wantVerbatimProvenanceComment))
	}
	// The Kubernetes-runtime-specific keys are excluded from Terraform output,
	// and there is no YAML metadata/labels splice.
	for _, banned := range []string{"app.kubernetes.io/name", "argocd.argoproj.io", "metadata:", "\nlabels:"} {
		if strings.Contains(head, banned) {
			t.Errorf("verbatim body contains %q — it must not appear in Terraform output; body:\n%s", banned, head)
		}
	}
	// (c) Terraform interpolation survived untouched.
	if !strings.Contains(string(got.Body), `endpoint_probe    = "${aws_db_instance.prod-db.endpoint}"`) {
		t.Errorf("${...} Terraform interpolation should survive verbatim; body:\n%s", string(got.Body))
	}

	// Determinism (Invariant 8): a second render is byte-identical.
	tmplCtx2, state2, nodeCtx2 := newTerraformArtifactsCtx()
	state2.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state2.Recipe.Spec.Artifacts.Set("main.tf", types.ArtifactEntry{Inline: verbatimHCLFixture})
	files2, err := evaluateArtifacts(context.Background(), tmplCtx2, state2, stubVerbatimEngine{}, nodeCtx2)
	if err != nil {
		t.Fatalf("evaluateArtifacts (second render): %v", err)
	}
	if string(files2[0].Body) != string(got.Body) {
		t.Errorf("non-deterministic output across two renders.\nfirst:\n%s\nsecond:\n%s", string(got.Body), string(files2[0].Body))
	}

	// No provenance-skip warning path for a verbatim provider — it never
	// attempts a YAML round-trip.
	if len(state.Warnings) != 0 {
		t.Errorf("verbatim path should emit no warnings, got %v", state.Warnings)
	}
}

// shippedMariadbRdsBody is a verbatim copy of the main.tf artifact body from
// the shipped mariadb-rds terraform recipe. It is embedded (rather than read
// from the catalog) so this renderer-package unit test stays self-contained and
// does not depend on the catalog tree being present at test time. It exercises
// the catalog template functions the tracker previously could not parse
// (trimSuffix, toJson, until, int, default) so the field-access pre-check and
// the verbatim emission are proven against a real-consumer body, not a
// simplified stand-in. Keep byte-identical to the recipe's main.tf value.
const shippedMariadbRdsBody = `resource "aws_db_instance" "{{ .locals.db_identifier }}" {
  identifier     = "{{ .locals.db_identifier }}"
  engine         = "mariadb"
  engine_version = "{{ .infra.engine_version }}"
  port           = {{ .values.port }}

  ## ── Compute ──────────────────────────────────────────────
  {{- if .infra.overrides }}
  {{- if .infra.overrides.instance_class }}
  instance_class = "{{ .infra.overrides.instance_class }}"
  {{- else }}
  instance_class = "db.t3.small"
  {{- end }}
  {{- else }}
  instance_class = "db.t3.small"
  {{- end }}

  ## ── Storage ──────────────────────────────────────────────
  allocated_storage = {{ trimSuffix "Gi" .infra.storage.size }}
  storage_type      = "{{ .infra.storage.type }}"

  ## ── Database ─────────────────────────────────────────────
  db_name  = "{{ .values.database }}"
  username = "admin"

  ## Black-Box: RDS manages the password via Secrets Manager.
  manage_master_user_password = true

  ## ── HA ───────────────────────────────────────────────────
  {{- if .infra.overrides }}
  multi_az = {{ .infra.overrides.multi_az | default .infra.ha.enabled }}
  {{- else }}
  multi_az = {{ .infra.ha.enabled }}
  {{- end }}

  ## ── Backup ───────────────────────────────────────────────
  backup_retention_period = {{ .infra.backup.retention_days }}
  {{- if not .infra.backup.enabled }}
  skip_final_snapshot     = true
  {{- end }}

  ## ── Provider-specific overrides ──────────────────────────
  {{- if .infra.overrides }}
  {{- if .infra.overrides.subnet_group }}
  db_subnet_group_name = "{{ .infra.overrides.subnet_group }}"
  {{- end }}
  {{- if .infra.overrides.parameter_group }}
  parameter_group_name = "{{ .infra.overrides.parameter_group }}"
  {{- end }}
  {{- if .infra.overrides.vpc_security_groups }}
  vpc_security_group_ids = {{ toJson .infra.overrides.vpc_security_groups }}
  {{- end }}
  {{- if .infra.overrides.kms_key_id }}
  storage_encrypted = true
  kms_key_id        = "{{ .infra.overrides.kms_key_id }}"
  {{- end }}
  {{- if .infra.overrides.deletion_protection }}
  deletion_protection = {{ .infra.overrides.deletion_protection }}
  {{- end }}
  {{- if .infra.overrides.tags }}
  tags = {{ toJson .infra.overrides.tags }}
  {{- end }}
  {{- end }}
}

{{- if gt (int .infra.ha.read_replicas) 0 }}
{{- range $i := until (int .infra.ha.read_replicas) }}
resource "aws_db_instance" "{{ $.locals.db_identifier }}_replica_{{ $i }}" {
  identifier          = "{{ $.locals.db_identifier }}-replica-{{ $i }}"
  replicate_source_db = aws_db_instance.{{ $.locals.db_identifier }}.identifier
  instance_class      = aws_db_instance.{{ $.locals.db_identifier }}.instance_class
}
{{- end }}
{{- end }}
`

// newMariadbRdsRealCtx populates a template context with realistic pillar
// values for every slot the shipped mariadb-rds main.tf body reads: locals
// identity, infra sizing / HA / backup / overrides, and developer values. The
// overrides carry multi_az, vpc_security_groups, and tags so the default,
// toJson, and truthiness branches all resolve.
func newMariadbRdsRealCtx() (*types.TemplateContext, *PhaseBState, NodeRenderContext) {
	tmplCtx := &types.TemplateContext{
		Context: types.ContextBlock{InstanceID: "prod-mariadb"},
		Values:  types.OrderedMap[any]{},
		Infra:   types.OrderedMap[any]{},
		Locals:  types.OrderedMap[string]{},
	}
	tmplCtx.Locals.Set("db_identifier", "prod-mariadb")
	tmplCtx.Values.Set("port", 3306)
	tmplCtx.Values.Set("database", "appdb")
	tmplCtx.Infra.Set("engine_version", "10.6")
	tmplCtx.Infra.Set("storage", map[string]any{"size": "100Gi", "type": "gp3"})
	tmplCtx.Infra.Set("ha", map[string]any{"enabled": true, "read_replicas": 2})
	tmplCtx.Infra.Set("backup", map[string]any{"retention_days": 7, "enabled": true})
	tmplCtx.Infra.Set("overrides", map[string]any{
		"multi_az":            true,
		"vpc_security_groups": []string{"sg-abc", "sg-def"},
		"tags":                map[string]any{"Environment": "production"},
	})

	r := &types.Recipe{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Recipe",
		Metadata:   types.RecipeMetadata{Name: "mariadb-rds", Version: "1.0.0"},
		Spec:       types.RecipeSpec{Provider: "terraform", Implements: "core/datastore/mariadb:1.0.0"},
	}
	state := NewPhaseBState(r, "prod-mariadb", "production", "recipe.yaml")
	nodeCtx := NodeRenderContext{
		NodeID:          "prod-mariadb",
		BoundaryPath:    "production",
		ProviderName:    "terraform",
		ProviderVersion: "1.0.0",
		Provenance: ProvenanceContext{
			CompilerVersion: "0.1.0",
			AggregateName:   "shop",
			Environment:     "production",
		},
	}
	return tmplCtx, state, nodeCtx
}

// wantShippedProvenanceComment is the exact provenance comment block prepended
// to the shipped mariadb-rds body for newMariadbRdsRealCtx's node.
const wantShippedProvenanceComment = `# Aggrega build provenance
# aggrega.ai/aggregate-name: shop
# aggrega.ai/boundary-path: production
# aggrega.ai/compiler-version: 0.1.0
# aggrega.ai/component-id: prod-mariadb
# aggrega.ai/environment: production
# aggrega.ai/provider: terraform
# aggrega.ai/provider-version: 1.0.0
# aggrega.ai/recipe-uri: mariadb-rds:1.0.0
`

// TestEvaluateArtifacts_ShippedMariadbRdsBodyEmitsVerbatim drives the REAL
// shipped mariadb-rds main.tf body — including its trimSuffix/toJson/until/int/
// default catalog-function expressions — through the host artifacts path. It is
// the real-consumer acceptance for the verbatim terraform path: the body both
// passes the field-access pre-check (which parses with the evaluator's funcmap)
// and emits template-resolved HCL byte-for-byte, with no YAML round-trip and no
// provenance splice.
func TestEvaluateArtifacts_ShippedMariadbRdsBodyEmitsVerbatim(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newMariadbRdsRealCtx()

	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state.Recipe.Spec.Artifacts.Set("main.tf", types.ArtifactEntry{Inline: shippedMariadbRdsBody})

	files, err := evaluateArtifacts(context.Background(), tmplCtx, state, stubVerbatimEngine{}, nodeCtx)
	if err != nil {
		t.Fatalf("evaluateArtifacts on shipped mariadb-rds body: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 emitted file, got %d", len(files))
	}
	got := files[0]
	body := string(got.Body)

	// (a) named main.tf, inline origin, standalone file.
	if got.Name != "main.tf" {
		t.Errorf("Name = %q, want main.tf", got.Name)
	}
	if got.Origin != EmitOriginArtifactsInline {
		t.Errorf("Origin = %v, want EmitOriginArtifactsInline", got.Origin)
	}
	if got.Intermediate {
		t.Errorf("Intermediate = true, want false for a flat verbatim provider")
	}

	// The emitted file STARTS with the provenance comment block; the shipped HCL
	// below it is unchanged by provenance stamping.
	if !strings.HasPrefix(body, wantShippedProvenanceComment) {
		t.Errorf("body must start with the provenance comment block; head:\n%s", body)
	}
	hcl := strings.TrimPrefix(body, wantShippedProvenanceComment)
	if !strings.HasPrefix(hcl, `resource "aws_db_instance" "prod-mariadb"`) {
		t.Errorf("HCL below the provenance comment should begin with the aws_db_instance resource; got:\n%s", hcl)
	}

	// (b) template fully resolved — no residual Go-template delimiters.
	if strings.Contains(body, "{{") || strings.Contains(body, "}}") {
		t.Errorf("body still contains unresolved template delimiters:\n%s", body)
	}
	// Catalog functions resolved to concrete HCL:
	//   trimSuffix "Gi" "100Gi" -> bare number 100
	//   .values.port / .infra.backup.retention_days -> scalars
	//   .infra.overrides.multi_az | default ... -> true
	//   toJson .infra.overrides.vpc_security_groups -> JSON array
	//   toJson .infra.overrides.tags -> JSON object
	//   until (int .infra.ha.read_replicas) -> two replica resources
	wantSubstrings := []string{
		"allocated_storage = 100",
		`storage_type      = "gp3"`,
		"port           = 3306",
		`db_name  = "appdb"`,
		"instance_class = \"db.t3.small\"",
		"multi_az = true",
		"backup_retention_period = 7",
		`vpc_security_group_ids = ["sg-abc","sg-def"]`,
		`tags = {"Environment":"production"}`,
		`resource "aws_db_instance" "prod-mariadb_replica_0"`,
		`resource "aws_db_instance" "prod-mariadb_replica_1"`,
		"replicate_source_db = aws_db_instance.prod-mariadb.identifier",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(body, want) {
			t.Errorf("resolved body missing %q; full body:\n%s", want, body)
		}
	}
	// Branches whose guards are false must be absent (self-containment: only
	// declared, populated overrides render).
	for _, absent := range []string{
		"skip_final_snapshot",  // backup.enabled true
		"storage_encrypted",    // no kms_key_id
		"deletion_protection",  // unset
		"db_subnet_group_name", // unset
		"parameter_group_name", // unset
	} {
		if strings.Contains(body, absent) {
			t.Errorf("body unexpectedly contains %q (its guard is false):\n%s", absent, body)
		}
	}

	// No YAML round-trip splice, and the Kubernetes-runtime-specific keys never
	// appear in Terraform output. (aggrega.ai/* provenance rides in the leading
	// comment block, asserted above; the HCL below carries no metadata/labels.)
	for _, banned := range []string{"app.kubernetes.io/name", "argocd.argoproj.io", "metadata:", "\nlabels:"} {
		if strings.Contains(hcl, banned) {
			t.Errorf("HCL body contains %q — it must not appear in Terraform output:\n%s", banned, hcl)
		}
	}
	if len(state.Warnings) != 0 {
		t.Errorf("verbatim path should emit no warnings, got %v", state.Warnings)
	}

	// (c) Determinism (Invariant 8): a second independent render is byte-identical.
	tmplCtx2, state2, nodeCtx2 := newMariadbRdsRealCtx()
	state2.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state2.Recipe.Spec.Artifacts.Set("main.tf", types.ArtifactEntry{Inline: shippedMariadbRdsBody})
	files2, err := evaluateArtifacts(context.Background(), tmplCtx2, state2, stubVerbatimEngine{}, nodeCtx2)
	if err != nil {
		t.Fatalf("evaluateArtifacts (second render): %v", err)
	}
	if string(files2[0].Body) != body {
		t.Errorf("non-deterministic output across two renders.\nfirst:\n%s\nsecond:\n%s", body, string(files2[0].Body))
	}
}

// TestEvaluateArtifacts_VerbatimAcceptsImport confirms a verbatim provider
// accepts a $import entry too, emitting at the bare basename with the import
// origin — the inline-rejection that a non-verbatim external provider applies
// does not fire here.
func TestEvaluateArtifacts_VerbatimAcceptsImport(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newTerraformArtifactsCtx()

	dir := t.TempDir()
	p := filepath.Join(dir, "network.tf")
	if err := os.WriteFile(p, []byte(`resource "aws_security_group" "{{ .locals.db_identifier }}" {}`+"\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state.Recipe.Spec.Artifacts.Set("network.tf", types.ArtifactEntry{Import: p})

	files, err := evaluateArtifacts(context.Background(), tmplCtx, state, stubVerbatimEngine{}, nodeCtx)
	if err != nil {
		t.Fatalf("evaluateArtifacts: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 emitted file, got %d", len(files))
	}
	got := files[0]
	if got.Name != "network.tf" {
		t.Errorf("Name = %q, want network.tf (bare basename)", got.Name)
	}
	if got.Origin != EmitOriginArtifactsImport {
		t.Errorf("Origin = %v, want EmitOriginArtifactsImport", got.Origin)
	}
	// A $import verbatim body also carries the prepended provenance comment; the
	// imported HCL below it is emitted byte-for-byte.
	wantBody := wantVerbatimProvenanceComment + `resource "aws_security_group" "prod-db" {}` + "\n"
	if string(got.Body) != wantBody {
		t.Errorf("import body not emitted as provenance-comment + verbatim HCL; got %q", string(got.Body))
	}
}

// TestEvaluateArtifacts_ExternalNonVerbatimStillRejectsInline is the negative
// guard: an external-handler provider with VerbatimArtifacts left false still
// rejects inline entries. This pins that VerbatimArtifacts — not merely
// ExternalArtifacts — is what unlocks inline acceptance.
func TestEvaluateArtifacts_ExternalNonVerbatimStillRejectsInline(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newTerraformArtifactsCtx()

	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state.Recipe.Spec.Artifacts.Set("any.yaml", types.ArtifactEntry{Inline: "kind: ConfigMap\n"})

	_, err := evaluateArtifacts(context.Background(), tmplCtx, state, stubExternalNonVerbatimEngine{}, nodeCtx)
	if err == nil {
		t.Fatal("expected CATALOG_ERROR: an external non-verbatim provider must still reject inline artifacts")
	}
	if !strings.Contains(err.Error(), "does not support inline artifacts") {
		t.Errorf("expected inline-rejection error, got %q", err.Error())
	}
}

// verbatimJSONFixture is a compact terraform JSON config body (the .tf.json
// form Terraform accepts alongside HCL) exercising a Go-template read plus a
// Terraform interpolation probe that must survive untouched. It is authored as
// valid JSON so the emitted body can be parsed back to prove the compiler
// never corrupts it with a leading comment.
const verbatimJSONFixture = `{
  "resource": {
    "aws_db_instance": {
      "{{ .locals.db_identifier }}": {
        "identifier": "{{ .locals.db_identifier }}",
        "engine": "mariadb",
        "port": {{ .values.port }},
        "endpoint_probe": "${aws_db_instance.{{ .locals.db_identifier }}.endpoint}"
      }
    }
  }
}
`

// verbatimJSONExpected is verbatimJSONFixture with the Go-template actions
// resolved and every other byte preserved — the byte-identity golden.
const verbatimJSONExpected = `{
  "resource": {
    "aws_db_instance": {
      "prod-db": {
        "identifier": "prod-db",
        "engine": "mariadb",
        "port": 3306,
        "endpoint_probe": "${aws_db_instance.prod-db.endpoint}"
      }
    }
  }
}
`

// TestEvaluateArtifacts_VerbatimJSONBodyByteFaithfulWithCompanion drives an
// inline terraform artifact named main.tf.json (a JSON config body Terraform
// accepts) through the real host artifacts path and asserts the format-aware
// provenance contract for JSON: the body is emitted byte-for-byte with NO `#`
// provenance prefix (a prefix would make the JSON invalid), it parses as valid
// JSON, and a single companion aggrega.provenance.tf comment-only file carries
// the provenance block instead. Two renders are byte-identical.
func TestEvaluateArtifacts_VerbatimJSONBodyByteFaithfulWithCompanion(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newTerraformArtifactsCtx()

	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state.Recipe.Spec.Artifacts.Set("main.tf.json", types.ArtifactEntry{Inline: verbatimJSONFixture})

	files, err := evaluateArtifacts(context.Background(), tmplCtx, state, stubVerbatimEngine{}, nodeCtx)
	if err != nil {
		t.Fatalf("evaluateArtifacts: %v", err)
	}
	// Exactly two files: the JSON body and the provenance companion.
	if len(files) != 2 {
		t.Fatalf("expected 2 emitted files (JSON body + companion), got %d: %v", len(files), fileNames(files))
	}

	json := fileByName(t, files, "main.tf.json")
	companion := fileByName(t, files, "aggrega.provenance.tf")

	// (a) the JSON body is byte-for-byte the template-resolved JSON — NO `#`
	// prefix, no leading provenance comment, no trailing companion content.
	if string(json.Body) != verbatimJSONExpected {
		t.Errorf("JSON body not emitted byte-for-byte verbatim.\n--- got ---\n%s\n--- want ---\n%s", string(json.Body), verbatimJSONExpected)
	}
	if strings.HasPrefix(string(json.Body), "#") || strings.Contains(string(json.Body), "# Aggrega build provenance") {
		t.Errorf("JSON body must not carry an HCL `#` provenance comment; got:\n%s", string(json.Body))
	}

	// (b) the JSON body actually parses as valid JSON — the corruption the
	// unconditional prepend would cause is proven absent.
	var parsed any
	if err := jsonUnmarshal(json.Body, &parsed); err != nil {
		t.Fatalf("emitted main.tf.json is not valid JSON (provenance prefix would corrupt it): %v\nbody:\n%s", err, string(json.Body))
	}

	// (c) the ${...} Terraform interpolation survived untouched inside the JSON.
	if !strings.Contains(string(json.Body), `"endpoint_probe": "${aws_db_instance.prod-db.endpoint}"`) {
		t.Errorf("${...} Terraform interpolation should survive verbatim in the JSON body; body:\n%s", string(json.Body))
	}

	// (d) the companion carries exactly the shared provenance block.
	if string(companion.Body) != wantVerbatimProvenanceComment {
		t.Errorf("companion provenance mismatch.\n--- got ---\n%s\n--- want ---\n%s", string(companion.Body), wantVerbatimProvenanceComment)
	}

	// No YAML round-trip warnings on the verbatim path.
	if len(state.Warnings) != 0 {
		t.Errorf("verbatim path should emit no warnings, got %v", state.Warnings)
	}

	// (e) Determinism (Invariant 8): a second independent render is byte-identical
	// for both files.
	tmplCtx2, state2, nodeCtx2 := newTerraformArtifactsCtx()
	state2.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state2.Recipe.Spec.Artifacts.Set("main.tf.json", types.ArtifactEntry{Inline: verbatimJSONFixture})
	files2, err := evaluateArtifacts(context.Background(), tmplCtx2, state2, stubVerbatimEngine{}, nodeCtx2)
	if err != nil {
		t.Fatalf("evaluateArtifacts (second render): %v", err)
	}
	if string(fileByName(t, files2, "main.tf.json").Body) != string(json.Body) {
		t.Errorf("non-deterministic JSON body across two renders")
	}
	if string(fileByName(t, files2, "aggrega.provenance.tf").Body) != string(companion.Body) {
		t.Errorf("non-deterministic companion across two renders")
	}
}

// TestEvaluateArtifacts_VerbatimMixedHCLAndJSON drives a node with BOTH an HCL
// body (main.tf) and a JSON body (extra.tf.json). The chosen mixed-node rule:
// when any JSON body is present, the single companion aggrega.provenance.tf is
// the sole provenance carrier and NO body is prefixed — so the block is never
// duplicated across surfaces and the JSON is never corrupted. Both bodies emit
// byte-for-byte.
func TestEvaluateArtifacts_VerbatimMixedHCLAndJSON(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newTerraformArtifactsCtx()

	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state.Recipe.Spec.Artifacts.Set("main.tf", types.ArtifactEntry{Inline: verbatimHCLFixture})
	state.Recipe.Spec.Artifacts.Set("extra.tf.json", types.ArtifactEntry{Inline: verbatimJSONFixture})

	files, err := evaluateArtifacts(context.Background(), tmplCtx, state, stubVerbatimEngine{}, nodeCtx)
	if err != nil {
		t.Fatalf("evaluateArtifacts: %v", err)
	}
	// Two bodies + exactly one companion.
	if len(files) != 3 {
		t.Fatalf("expected 3 emitted files (HCL + JSON + one companion), got %d: %v", len(files), fileNames(files))
	}

	hcl := fileByName(t, files, "main.tf")
	jsonFile := fileByName(t, files, "extra.tf.json")
	companion := fileByName(t, files, "aggrega.provenance.tf")

	// In the mixed case the HCL body is NOT prefixed — the companion is the
	// sole carrier, so provenance is never duplicated.
	if string(hcl.Body) != verbatimHCLExpected {
		t.Errorf("mixed-node HCL body should emit verbatim (no prefix); got:\n%s", string(hcl.Body))
	}
	if strings.Contains(string(hcl.Body), "# Aggrega build provenance") {
		t.Errorf("mixed-node HCL body must not carry a prepended provenance comment (companion is the sole carrier); got:\n%s", string(hcl.Body))
	}

	// The JSON body emits verbatim and stays valid JSON.
	if string(jsonFile.Body) != verbatimJSONExpected {
		t.Errorf("mixed-node JSON body should emit verbatim; got:\n%s", string(jsonFile.Body))
	}
	var parsed any
	if err := jsonUnmarshal(jsonFile.Body, &parsed); err != nil {
		t.Fatalf("mixed-node main JSON body is not valid JSON: %v", err)
	}

	// Exactly one companion, carrying the shared provenance block.
	if string(companion.Body) != wantVerbatimProvenanceComment {
		t.Errorf("companion provenance mismatch.\n--- got ---\n%s\n--- want ---\n%s", string(companion.Body), wantVerbatimProvenanceComment)
	}
	companionCount := 0
	for _, f := range files {
		if f.Name == "aggrega.provenance.tf" {
			companionCount++
		}
	}
	if companionCount != 1 {
		t.Errorf("expected exactly one companion provenance file, got %d", companionCount)
	}
}

// fileByName returns the emitted file with the given name, failing the test
// when absent.
func fileByName(t *testing.T, files []EmittedFile, name string) EmittedFile {
	t.Helper()
	for _, f := range files {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no emitted file named %q; got %v", name, fileNames(files))
	return EmittedFile{}
}

// fileNames lists the names of emitted files for diagnostic messages.
func fileNames(files []EmittedFile) []string {
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Name)
	}
	return names
}

// jsonUnmarshal is a thin wrapper so the JSON-validity assertions read
// clearly; it proves the emitted verbatim body parses as JSON.
func jsonUnmarshal(b []byte, v any) error {
	return json.Unmarshal(b, v)
}
