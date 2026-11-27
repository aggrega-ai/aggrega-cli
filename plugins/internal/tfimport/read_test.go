// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package tfimport

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
)

// parseFailed is the importer kit's parse-failed diagnostic, its code and
// severity read off the kit's own constructor.
var parseFailed = importer.ParseFailed(aggrega.ImportSource{}, formatName, errors.New(""))

// render writes blocks as text, each with its kind, its labels and its
// members, so that a configuration read from either syntax compares alike;
// placed writes each block's file and range too.
func render(blocks []block, placed bool) string {
	var b strings.Builder
	for _, blk := range blocks {
		b.WriteString(strings.Join(append([]string{blk.Kind}, blk.Labels...), " "))
		if placed {
			fmt.Fprintf(&b, " (%s %s)", blk.File, blk.Range)
		}
		b.WriteString("\n")
		ms, diags := members(blk.Body)
		for _, m := range ms {
			writeMember(&b, m, 1, placed)
		}
		if len(diags) > 0 {
			fmt.Fprintf(&b, "  diagnostics: %s\n", diags.Error())
		}
	}
	return b.String()
}

// writeMember writes m at the depth given, an item of a list as "-".
func writeMember(b *strings.Builder, m member, depth int, placed bool) {
	indent := strings.Repeat("  ", depth)
	name := m.Name
	if name == "" {
		name = "-"
	}
	if placed {
		name += " (" + m.Range.String() + ")"
	}
	switch m.Shape {
	case objectShape:
		fmt.Fprintf(b, "%s%s {\n", indent, name)
		for _, c := range m.Members {
			writeMember(b, c, depth+1, placed)
		}
		fmt.Fprintf(b, "%s}\n", indent)
	case listShape:
		fmt.Fprintf(b, "%s%s [\n", indent, name)
		for _, c := range m.Items {
			writeMember(b, c, depth+1, placed)
		}
		fmt.Fprintf(b, "%s]\n", indent)
	default:
		if m.Name == "" {
			fmt.Fprintf(b, "%s%s %s\n", indent, name, leafText(m.Expr))
		} else {
			fmt.Fprintf(b, "%s%s = %s\n", indent, name, leafText(m.Expr))
		}
	}
}

// leafText writes an expression as either syntax writes it alike: a literal
// string quoted, the references of one reading any, and otherwise its value.
func leafText(e hcl.Expression) string {
	if s, ok := literalString(e); ok {
		return strconv.Quote(s)
	}
	var refs []string
	for _, tr := range e.Variables() {
		refs = append(refs, traversalText(tr))
	}
	if len(refs) > 0 {
		return "refs(" + strings.Join(refs, ", ") + ")"
	}
	v, diags := e.Value(nil)
	switch {
	case diags.HasErrors() || !v.IsWhollyKnown():
		return "expression"
	case v.IsNull():
		return "null"
	case v.Type() == cty.Number:
		return v.AsBigFloat().Text('f', -1)
	case v.Type() == cty.Bool:
		return strconv.FormatBool(v.True())
	}
	return v.GoString()
}

// traversalText writes a traversal's root and attribute names.
func traversalText(tr hcl.Traversal) string {
	var b strings.Builder
	for _, step := range tr {
		switch s := step.(type) {
		case hcl.TraverseRoot:
			b.WriteString(s.Name)
		case hcl.TraverseAttr:
			b.WriteString("." + s.Name)
		default:
			b.WriteString("[…]")
		}
	}
	return b.String()
}

// readFixture reads the imported directory of the testdata directory dir,
// failing t on any diagnostic.
func readFixture(t *testing.T, dir string) *config {
	t.Helper()
	cfg, diags := readDir(".", fixture(t, dir))
	if len(diags) != 0 || cfg == nil {
		t.Fatalf("readDir(%s) = %v, %+v", dir, cfg != nil, diags)
	}
	return cfg
}

// placeOf names each block by its kind, labels, file and line.
func placeOf(blocks []block) []string {
	out := make([]string, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, fmt.Sprintf("%s %s at %s:%d", b.Kind, strings.Join(b.Labels, "."), b.File,
			b.Range.Start.Line))
	}
	return out
}

func TestReadDirBlocksInSourceOrder(t *testing.T) {
	cfg := readFixture(t, "order")
	want := []string{
		"output jobs_url at a.tf.json:3",
		"locals  at a.tf.json:7",
		"resource aws_sqs_queue.jobs at b.tf:1",
		"variable retention at b.tf:6",
	}
	if got := placeOf(cfg.Blocks); !reflect.DeepEqual(got, want) {
		t.Fatalf("blocks = %q, want %q", got, want)
	}
	if cfg.Dir != "." || len(cfg.NotRead) != 0 {
		t.Fatalf("dir = %q, not read = %q", cfg.Dir, cfg.NotRead)
	}
}

// TestReadDirReadsOnlyItsOwnDirectory reads the files directly in the
// directory named, its path written with or without a closing slash.
func TestReadDirReadsOnlyItsOwnDirectory(t *testing.T) {
	files := fixture(t, "layout")
	for _, dir := range []string{"modules/db", "modules/db/"} {
		cfg, diags := readDir(dir, files)
		if len(diags) != 0 || cfg == nil {
			t.Fatalf("readDir(%q) = %v, %+v", dir, cfg != nil, diags)
		}
		want := []string{"variable name at modules/db/main.tf:1", "resource aws_db_instance.this at modules/db/main.tf:3"}
		if got := placeOf(cfg.Blocks); cfg.Dir != "modules/db" || !reflect.DeepEqual(got, want) {
			t.Fatalf("readDir(%q): dir %q, blocks %q, want %q", dir, cfg.Dir, got, want)
		}
	}
}

func TestReadDirParseFailedNamesFileLineAndColumn(t *testing.T) {
	cfg, diags := readDir(".", fixture(t, "broken"))
	if cfg != nil || len(diags) != 1 {
		t.Fatalf("readDir = %v, %+v; want one halt and no configuration", cfg != nil, diags)
	}
	d := diags[0]
	if d.Code != parseFailed.Code || d.Severity != parseFailed.Severity || d.SourceRef != "main.tf" ||
		!strings.Contains(d.Message, "main.tf:3,31") || !strings.Contains(d.Message, "Invalid expression") {
		t.Fatalf("diagnostic = %+v, want parse-failed naming main.tf at line 3, column 31", d)
	}
}

func TestReadDirNotRead(t *testing.T) {
	cfg := readFixture(t, "notread/native")
	wantBlocks := []string{"terraform  at main.tf:1", "resource aws_sqs_queue.jobs at main.tf:8"}
	wantNotRead := []string{"backend at main.tf:2", "moved at main.tf:12", "import at main.tf:17",
		"removed at main.tf:22", "check at main.tf:26", "ephemeral at main.tf:33"}
	if got := placeOf(cfg.Blocks); !reflect.DeepEqual(got, wantBlocks) || !reflect.DeepEqual(cfg.NotRead, wantNotRead) {
		t.Fatalf("native: blocks %q, not read %q; want %q, %q", got, cfg.NotRead, wantBlocks, wantNotRead)
	}

	cfg = readFixture(t, "notread/json")
	wantBlocks = []string{"terraform  at main.tf.json:2", "resource aws_sqs_queue.jobs at main.tf.json:12"}
	wantNotRead = []string{"cloud at main.tf.json:3", "moved at main.tf.json:17", "ephemeral at main.tf.json:23"}
	if got := placeOf(cfg.Blocks); !reflect.DeepEqual(got, wantBlocks) || !reflect.DeepEqual(cfg.NotRead, wantNotRead) {
		t.Fatalf("JSON: blocks %q, not read %q; want %q, %q", got, cfg.NotRead, wantBlocks, wantNotRead)
	}
}

// sameConfig is the configuration of testdata/same, as render writes it: a
// nested block written once is an object, the blocks of one type written
// several times a list, an object expression an object and a list
// expression a list.
const sameConfig = `terraform
  required_version = ">= 1.6"
  required_providers {
    aws {
      source = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
provider aws
  region = refs(var.region)
variable region
  description = "where the queue runs"
  default = "eu-west-1"
locals
  name = refs(var.region)
resource aws_security_group web
  name = refs(local.name)
  description = "web tier"
  ingress [
    - {
      from_port = 443
      to_port = 443
      protocol = "tcp"
      cidr_blocks [
        - "10.0.0.0/8"
        - "172.16.0.0/12"
      ]
    }
    - {
      from_port = 80
      to_port = 80
      protocol = "tcp"
      cidr_blocks [
        - "10.0.0.0/8"
      ]
    }
  ]
  egress {
    from_port = 0
    to_port = 0
    protocol = "-1"
  }
  tags {
    Name = "web"
    Tier = "edge"
  }
data aws_vpc main
  default = true
module queue
  source = "./modules/queue"
  name = refs(local.name)
output group_id
  value = refs(aws_security_group.web.id)
`

func TestTfAndTfJsonReadTheSameConfig(t *testing.T) {
	native := render(readFixture(t, "same/native").Blocks, false)
	json := render(readFixture(t, "same/json").Blocks, false)
	if native != sameConfig {
		t.Fatalf(".tf reads\n%s\nwant\n%s", native, sameConfig)
	}
	if json != sameConfig {
		t.Fatalf(".tf.json reads\n%s\nwant\n%s", json, sameConfig)
	}
}

func TestInitialisedDirectorySkipsDotTerraform(t *testing.T) {
	files := fixture(t, "initialised")
	if got := unitPaths(units(files)); !reflect.DeepEqual(got, []string{"./"}) {
		t.Fatalf("units = %q, want the imported directory alone", got)
	}
	installed := 0
	for _, f := range files {
		if classify(f.Name) == kindInstalled {
			installed++
			if f.Name != ".terraform/modules/modules.json" {
				t.Fatalf("%s classified as the installed modules' record", f.Name)
			}
		}
	}
	if installed != 1 {
		t.Fatalf("the root's modules.json is not kept among %d files", len(files))
	}
	cfg := readFixture(t, "initialised")
	wantBlocks := []string{"module db at main.tf:1", "resource aws_sqs_queue.jobs at main.tf:8"}
	if got := placeOf(cfg.Blocks); !reflect.DeepEqual(got, wantBlocks) {
		t.Fatalf("blocks = %q, want %q", got, wantBlocks)
	}
	if !reflect.DeepEqual(cfg.NotRead, []string{".terraform/"}) {
		t.Fatalf("not read = %q, want .terraform/ once", cfg.NotRead)
	}
	if cfg, diags := readDir(".terraform/modules/db", files); len(diags) != 0 || cfg == nil || len(cfg.Blocks) != 0 {
		t.Fatalf("readDir of the installed module read %+v, %+v; want nothing", cfg, diags)
	}
}

// cdktfConfig is testdata/cdktf/main.tf.json as render writes it: no
// member named "//" at any level.
const cdktfConfig = `terraform
  required_providers {
    aws {
      source = "aws"
      version = "5.0.0"
    }
  }
provider aws
  region = "eu-west-1"
variable retention
  default = 86400
locals
  team = "orders"
resource aws_sqs_queue jobs
  name = "jobs"
  message_retention_seconds = refs(var.retention)
  lifecycle {
    create_before_destroy = true
  }
resource aws_security_group web
  name = "web"
  ingress [
    - {
      from_port = 443
      to_port = 443
      protocol = "tcp"
    }
    - {
      from_port = 80
      to_port = 80
      protocol = "tcp"
    }
  ]
module queue
  source = "./modules/queue"
output jobs_url
  value = refs(aws_sqs_queue.jobs.url)
`

func TestCdktfCommentKeysIgnored(t *testing.T) {
	cfg := readFixture(t, "cdktf")
	if got := render(cfg.Blocks, false); got != cdktfConfig {
		t.Fatalf("reads\n%s\nwant\n%s", got, cdktfConfig)
	}
	if len(cfg.NotRead) != 0 {
		t.Fatalf("not read = %q, want none", cfg.NotRead)
	}
}

func TestByteOrderMarkAndCRLFReadAsTofuReadsThem(t *testing.T) {
	mark := []byte{0xEF, 0xBB, 0xBF}
	plain := fixture(t, "same/native")[0].Bytes
	windows := append(append([]byte(nil), mark...), bytes.ReplaceAll(plain, []byte("\n"), []byte("\r\n"))...)
	cfg, diags := readDir(".", []file{{Name: "main.tf", Bytes: windows}})
	if len(diags) != 0 || cfg == nil {
		t.Fatalf("readDir = %v, %+v", cfg != nil, diags)
	}
	if got := render(cfg.Blocks, false); got != sameConfig {
		t.Fatalf("a .tf with a byte-order mark and CRLF reads\n%s\nwant\n%s", got, sameConfig)
	}

	marked := append(append([]byte(nil), mark...), fixture(t, "same/json")[0].Bytes...)
	cfg, diags = readDir(".", []file{{Name: "main.tf.json", Bytes: marked}})
	if cfg != nil || len(diags) != 1 {
		t.Fatalf("readDir = %v, %+v; want one halt and no configuration", cfg != nil, diags)
	}
	d := diags[0]
	if d.Code != parseFailed.Code || d.Severity != parseFailed.Severity || d.SourceRef != "main.tf.json" ||
		!strings.Contains(d.Message, "main.tf.json:1,1-") {
		t.Fatalf("diagnostic = %+v, want parse-failed naming main.tf.json at line 1, column 1", d)
	}
}
