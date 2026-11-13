// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cbororacle

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
)

// The shared vector suite lives beside the codec in the aggrega-abi module, one
// tree consumed by all three languages. Each vector carries a JSON wire document
// (<Name>.golden.json) and its canonical-CBOR bytes (<Name>.cbor.hex). This test
// anchors the CBOR hex: it decodes the JSON document through the generated Go
// codec, encodes to canonical CBOR, and asserts the bytes equal the fxamacker
// reference (the independent oracle) and the committed hex. The Rust and TS
// harnesses assert their own encoders match the same hex, so a three-way hex
// match is a cross-language byte-identity proof anchored on the fxamacker oracle.
//
// Regenerate the hex goldens with AGGREGA_UPDATE_GOLDEN=1.
const testdataDir = "../../../../aggrega-abi/testdata"

// cborRT is the generated per-type CBOR codec surface over a value type T.
type cborRT[T any] interface {
	*T
	EncodeCBOR() ([]byte, error)
	DecodeCBOR([]byte) error
}

// runVec decodes a vector's JSON golden through the generated codec, encodes to
// canonical CBOR, checks the fxamacker reference (unless carved out) and the
// committed hex, and asserts a decode round-trip is byte-stable.
func runVec[T any, PT cborRT[T]](t *testing.T, file string, skipRef bool) {
	t.Helper()
	jsonBytes, err := os.ReadFile(filepath.Join(testdataDir, file+".golden.json"))
	if err != nil {
		t.Fatalf("read %s.golden.json: %v", file, err)
	}
	var v T
	if err := json.Unmarshal(bytes.TrimSpace(jsonBytes), PT(&v)); err != nil {
		t.Fatalf("%s: decode JSON golden: %v", file, err)
	}
	ours, err := PT(&v).EncodeCBOR()
	if err != nil {
		t.Fatalf("%s: EncodeCBOR: %v", file, err)
	}
	checkOrUpdateHex(t, file, ours)

	// Decode our bytes back to the canonical value, then re-encode: the round-trip
	// must be byte-stable, and this canonical value is what the fxamacker oracle
	// marshals. The generated codec canonicalizes a nil and an empty container to
	// the same empty CBOR container (a cross-language necessity — a Rust Vec or a
	// TS array cannot express nil, so all three emit the empty container), whereas
	// fxamacker preserves Go's nil-slice-as-null distinction. The JSON golden's
	// empty [] / {} decode to nil in Go's JSON codec; the CBOR decode yields the
	// non-nil empty container, so comparing fxamacker against the round-tripped
	// value keeps the oracle faithful to the encoding rules without asserting
	// fxamacker's nil-vs-empty opinion the canonical wire deliberately erases.
	var v2 T
	if err := PT(&v2).DecodeCBOR(ours); err != nil {
		t.Fatalf("%s: DecodeCBOR: %v", file, err)
	}
	rt, err := PT(&v2).EncodeCBOR()
	if err != nil {
		t.Fatalf("%s: re-encode: %v", file, err)
	}
	if !bytes.Equal(ours, rt) {
		t.Errorf("%s: round-trip not byte-stable\n in: %s\nout: %s",
			file, hex.EncodeToString(ours), hex.EncodeToString(rt))
	}
	if !skipRef {
		em := refEncoder(t)
		// Through the view that presents each order-keeping container as its list
		// of entries, which the reference encoder writes natively; handed the
		// container itself it would see no field and write an empty map.
		ref, err := em.Marshal(referenceView(v2))
		if err != nil {
			t.Fatalf("%s: reference marshal: %v", file, err)
		}
		if !bytes.Equal(ours, ref) {
			t.Fatalf("%s: generated CBOR != fxamacker reference\n generated: %s\n reference: %s",
				file, hex.EncodeToString(ours), hex.EncodeToString(ref))
		}
	}
}

// registeredVectors holds the name of every vector the suite registers. The
// registration path records it itself so the completeness check below compares
// the corpus on disk against what the suite actually covers, rather than against
// a list of names maintained beside the registrations: such a list is a second
// copy of the same information, and the copy is what goes stale.
var registeredVectors = map[string]bool{}

// vec registers one vector as its own subtest. The wrapping is load-bearing
// rather than cosmetic: runVec's reference-encoder comparison ends the test on
// failure, so registering vectors directly in one test body lets the first
// failing vector mask every vector after it. Per-vector subtests make each
// vector's verdict independent, which is what lets the suite report which
// vectors anchor rather than only whether the corpus as a whole does.
func vec[T any, PT cborRT[T]](t *testing.T, file string, skipRef bool) {
	t.Helper()
	// Recorded here rather than inside the subtest closure, and regardless of
	// skipRef. Outside the closure, because a -run filter selecting a subset of
	// subtests never executes the ones it filters out, so recording inside would
	// under-report the coverage and fail the completeness check on a legitimate
	// scoped run; it also keeps this map written only from the parent's serial
	// body. Regardless of skipRef, because the check asserts that a vector is
	// registered, not that it is anchored against the reference encoder — a
	// vector deliberately carved out of that comparison is still covered here.
	registeredVectors[file] = true
	t.Run(file, func(t *testing.T) {
		runVec[T, PT](t, file, skipRef)
	})
}

// assertCorpusFullyRegistered compares the vectors present in the shared suite
// directory against the vectors this suite registers, and names any the suite
// does not cover. It guards against a vector being added to the shared tree and
// wired into no assertion here: without it, a new vector would simply never be
// anchored against the reference encoder and nothing would say so. That is not a
// hypothetical failure mode — it is how the unanchored part of this corpus grew,
// held only by the three codecs agreeing with each other while every gate stayed
// green.
//
// Both sides are derived rather than written down. The disk side is read from the
// directory; the registered side is recorded by the registration path itself. A
// check comparing a census against a constant a maintainer keeps in step with it
// proves only that two hand-written numbers agree.
//
// The directory is read rather than pattern-matched because a pattern match
// reports no error for a directory that is not there: a moved or mistyped path
// would match nothing, find nothing missing, and pass. An empty corpus and a
// fully covered one must never be indistinguishable, so finding no vectors at all
// is its own failure. Reading the directory also yields entries sorted by name,
// so the reported set is deterministic without sorting it.
//
// Only the disk-to-registered direction is asserted. The opposite direction — a
// registration naming a vector that has no file — is already fatal in runVec,
// which reads the hex golden and cannot proceed without it, so asserting it again
// here would duplicate a guarantee that already holds.
func assertCorpusFullyRegistered(t *testing.T) {
	t.Helper()

	const ext = ".cbor.hex"
	entries, err := os.ReadDir(testdataDir)
	if err != nil {
		t.Fatalf("read the vector directory %s: %v", testdataDir, err)
	}
	onDisk := 0
	var unregistered []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ext) {
			continue
		}
		onDisk++
		if name := strings.TrimSuffix(e.Name(), ext); !registeredVectors[name] {
			unregistered = append(unregistered, name)
		}
	}
	if onDisk == 0 {
		t.Fatalf("found no %s vectors under %s, so this check certifies nothing: an empty "+
			"discovery is a broken instrument, not a covered corpus", ext, testdataDir)
	}
	if len(unregistered) > 0 {
		t.Errorf("%d of %d vectors in the shared suite are registered by no assertion here: %s.\n"+
			"Register every vector, so none is left held only by the codecs agreeing with each "+
			"other: all three are emitted by one generator, so a generator-level mistake appears "+
			"identically in each and their agreement stays green.",
			len(unregistered), onDisk, strings.Join(unregistered, ", "))
	}
}

// checkOrUpdateHex writes the canonical hex under AGGREGA_UPDATE_GOLDEN, else
// asserts the committed hex matches.
func checkOrUpdateHex(t *testing.T, file string, cborBytes []byte) {
	t.Helper()
	p := filepath.Join(testdataDir, file+".cbor.hex")
	got := hex.EncodeToString(cborBytes)
	if os.Getenv("AGGREGA_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(p, []byte(got+"\n"), 0o644); err != nil {
			t.Fatalf("write %s.cbor.hex: %v", file, err)
		}
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s.cbor.hex (run AGGREGA_UPDATE_GOLDEN=1 to create): %v", file, err)
	}
	if strings.TrimSpace(string(want)) != got {
		t.Errorf("%s: CBOR hex not byte-identical to golden\n golden: %s\n    got: %s",
			file, strings.TrimSpace(string(want)), got)
	}
}

// TestSuite_CBORVectors iterates the shared vector suite. Every vector on disk is
// registered here, so the oracle covers the whole corpus rather than the subset
// that happened to be wired: a vector held only by the three codecs agreeing with
// each other is not independently attested at all, because all three are emitted
// by one generator and a generator-level mistake appears identically in each while
// the three-way hex match stays green. That coverage is enforced rather than
// merely intended — assertCorpusFullyRegistered, called at the end of this
// function, fails if the directory holds a vector no registration here names.
//
// FRPStepOutput is the one vector carved out of the fxamacker assertion: its patch
// operand is a bare opaque JSON value (any), which the R10 rule carries as a CBOR
// byte-string of the verbatim JSON bytes, whereas fxamacker encodes a Go any as
// its native CBOR type — a deliberate representation difference, so FRPStepOutput
// is proven by the cross-language hex match plus its round-trip, not by the
// fxamacker byte oracle. The carve-out is specific to that vector rather than to
// its type: the other two FRPStepOutput vectors leave the patch list absent, never
// reach the opaque path, and so agree with the reference encoder exactly.
func TestSuite_CBORVectors(t *testing.T) {
	vec[abi.CLIRunInput](t, "CLIRunInput", false)
	vec[abi.FRPStepInput](t, "FRPStepInput", false)
	vec[abi.FRPStepOutput](t, "FRPStepOutput", true)
	vec[abi.MutatorInput](t, "MutatorInput", false)
	vec[abi.MutatorOutput](t, "MutatorOutput", false)
	vec[abi.LinterInput](t, "LinterInput", false)
	// The input of a rule that reads a whole Release, its Release and Component map
	// populated. Each crosses as a byte string of the document's JSON bytes — a
	// raw-message field, which the reference encoder writes as a byte string too —
	// so the vector stays anchored, and the map's two keys, which the map holds in
	// an order that is not the length-first one, pin the order-preserving array form
	// against an encoder this schema did not generate.
	vec[abi.LinterInput](t, "LinterInputRelease", false)
	vec[abi.LinterOutput](t, "LinterOutput", false)
	vec[abi.IRGraph](t, "IRGraph", false)
	vec[abi.PolicyInput](t, "PolicyInput", false)
	vec[abi.PolicyOutput](t, "PolicyOutput", false)
	vec[abi.RenderNodeInput](t, "RenderNodeInput", false)
	vec[abi.RenderNodeOutput](t, "RenderNodeOutput", false)
	// A render output carrying a populated fact list. Each value crosses as a byte
	// string of the JSON bytes the guest wrote — a raw-message field, which the
	// reference encoder writes as a byte string too — so it stays anchored.
	vec[abi.RenderNodeOutput](t, "RenderNodeOutputRenderFacts", false)
	// A source render input carrying omit entries, and an output carrying one count
	// per entry. The members they add are lists of text-string maps and of
	// integers, which the reference encoder writes the same way, so both stay
	// anchored.
	vec[abi.RenderNodeInput](t, "RenderNodeInputOmits", false)
	vec[abi.RenderNodeOutput](t, "RenderNodeOutputOmitted", false)
	// An omit entry carrying the sensitivity mark beside one that does not. The
	// member it adds is a boolean, which the reference encoder writes as the same
	// simple value and leaves out when false, so it stays anchored.
	vec[abi.RenderNodeInput](t, "RenderNodeInputOmitsSensitive", false)
	// An omit entry naming the subchart its unit came from beside one that does not.
	// The member it adds is a text string, which the reference encoder writes the
	// same way and leaves out when empty, so it stays anchored.
	vec[abi.RenderNodeInput](t, "RenderNodeInputOmitsSubchart", false)
	vec[abi.IRNodeProjection](t, "IRNodeProjectionSensitiveRenderedKeys", false)
	// A node projection whose values a values mapping produced. The member it adds
	// is a boolean, which the reference encoder writes as the same simple value and
	// leaves out when false, so it stays anchored.
	vec[abi.IRNodeProjection](t, "IRNodeProjectionValuesMapped", false)
	// A rendered node of the assembly input carrying the same mark, a boolean the
	// reference encoder writes as the same simple value and leaves out when false, so
	// it stays anchored.
	vec[abi.RenderedNode](t, "RenderedNodeValuesMapped", false)
	vec[abi.IRFeatureEntry](t, "IRFeatureEntrySensitivePayloadLeaves", false)
	vec[abi.IRSpec](t, "IRSpecConnectionAttachment", false)
	vec[abi.IRSpec](t, "IRSpecAttachmentRealization", false)
	vec[abi.IRNode](t, "IRNodeSensitivePayloadLeaves", false)
	vec[abi.IRBoundaryFeature](t, "IRBoundaryFeatureSensitivePayloadLeaves", false)
	// A connection's own marking. The member it adds is a list of text strings,
	// which the reference encoder writes the same way and leaves out when empty, so
	// it stays anchored.
	vec[abi.IREdge](t, "IREdgeSensitivePayloadLeaves", false)
	vec[abi.AssembleBoundaryInput](t, "AssembleBoundaryInput", false)
	vec[abi.AssembleBoundaryOutput](t, "AssembleBoundaryOutput", false)
	vec[abi.LinkPublishInput](t, "LinkPublishInput", false)
	vec[abi.LinkPublishOutput](t, "LinkPublishOutput", false)
	vec[abi.LinkResolveInput](t, "LinkResolveInput", false)
	vec[abi.LinkResolveOutput](t, "LinkResolveOutput", false)
	// The populated payloads of the render family's remaining exports. Each
	// carries its members populated — diagnostics, file bytes, units and patched
	// units, verdicts with and without a bound, and identities. The members they
	// add are text and byte strings, integers, booleans, lists, and raw-message
	// documents the reference encoder writes as byte strings too, so all of them
	// stay anchored.
	vec[abi.ValidateNodeOutput](t, "ValidateNodeOutput", false)
	// A diagnostic whose location names the instance it concerns beside the
	// document it points into. The member it adds is a text string, which the
	// reference encoder writes the same way and leaves out when empty, so it stays
	// anchored.
	vec[abi.ValidateNodeOutput](t, "ValidateNodeOutputInstanceLocation", false)
	vec[abi.CarveInput](t, "CarveInput", false)
	vec[abi.CarveOutput](t, "CarveOutput", false)
	vec[abi.ReassembleInput](t, "ReassembleInput", false)
	vec[abi.ReassembleOutput](t, "ReassembleOutput", false)
	vec[abi.NameBoundInput](t, "NameBoundInput", false)
	// A second name-bound question, carrying the facts of a node's resolved
	// workload — its form and its Service keys in declaration order. The members it
	// adds are a text string and a list of text strings, which the reference
	// encoder writes the same way and leaves out when empty, so it stays anchored.
	vec[abi.NameBoundInput](t, "NameBoundInputWorkload", false)
	vec[abi.NameBoundOutput](t, "NameBoundOutput", false)
	vec[abi.RenderedNameInput](t, "RenderedNameInput", false)
	vec[abi.RenderedNameOutput](t, "RenderedNameOutput", false)
	vec[abi.PluginDescriptor](t, "PluginDescriptorCLI", false)
	vec[abi.PluginDescriptor](t, "PluginDescriptorProvider", false)
	vec[abi.PluginDescriptor](t, "PluginDescriptorProviderOmits", false)
	// A provider contract claiming to accept the files a Recipe contributes to its
	// boundary's shared output. The member it adds is a boolean, which the
	// reference encoder writes the same way, so it stays anchored.
	vec[abi.PluginDescriptor](t, "PluginDescriptorProviderBoundaryArtifacts", false)
	// An importer's recognition contract populated in every field. The members it
	// adds are text strings, lists of text strings and lists of nested maps of
	// text strings, which the reference encoder writes the same way, so it stays
	// anchored.
	vec[abi.PluginDescriptor](t, "PluginDescriptorImporter", false)
	vec[abi.PluginDescriptor](t, "PluginDescriptorMinimal", false)
	vec[abi.PluginDescriptor](t, "PluginDescriptorMoo", false)
	vec[abi.ParseInput](t, "ParseInput", false)
	// A parse input whose source bytes are not valid UTF-8 and which omits the
	// options document. The source is a byte field, which the reference encoder
	// writes as a byte string natively, so it stays anchored: a generated codec that
	// carried those bytes as text, or as base64 text, would disagree with it.
	vec[abi.ParseInput](t, "ParseInputNonUTF8Source", false)
	vec[abi.ParseOutput](t, "ParseOutput", false)
	vec[abi.ParseOutput](t, "ParseOutputBare", false)
	vec[abi.ImportInput](t, "ImportInput", false)
	// An import input carrying a populated catalog view: nested maps of text
	// strings, lists of text strings and lists of nested maps, which the reference
	// encoder writes the same way, so it stays anchored. The label keys differ in
	// length, so the length-first CBOR key order is pinned against an encoder this
	// schema did not generate.
	vec[abi.ImportInput](t, "ImportInputCatalog", false)
	// A file imported on its own: a text string naming the file beside the byte
	// string of its bytes, which the reference encoder writes the same way, so it
	// stays anchored.
	vec[abi.ImportInput](t, "ImportInputNamed", false)
	vec[abi.ImportOutput](t, "ImportOutput", false)
	vec[abi.ParseSourceResponse](t, "ParseSourceResponse", false)
	// The parse-source reply's SUCCESS shape, which its failure envelope above
	// cannot stand in for: the two are the two directions of one omission
	// property, and an encoder emitting an error key unconditionally passes any
	// test built only from success fixtures while one dropping it unconditionally
	// passes any test built only from failure fixtures. The tree it carries is an
	// opaque JSON document crossing as a byte string of its verbatim bytes, and
	// the failure envelope reaches no opaque field at all, so the tree member's
	// bytes are pinned here. Its tree is long enough that the byte-string length
	// needs a two-byte head, which is where the reference encoder earns its keep:
	// all three generated codecs write that head through one shared head writer,
	// so a width mistake would appear identically in each and their agreement
	// would stay green.
	vec[abi.ParseSourceResponse](t, "ParseSourceResponseTree", false)
	// The fetch reply in its three shapes: the failure envelope, the fetched
	// bytes, and a zero-length payload. The content is a byte field, which the
	// reference encoder writes as a byte string natively, so a generated codec
	// that carried it as text, or as base64 text, disagrees with it. The
	// zero-length shape is the empty map: the content field is omitted when
	// empty, so a codec that emitted an empty byte string, or counted the omitted
	// key in the map head, disagrees with the reference too.
	vec[abi.FetchExternalInputResponse](t, "FetchExternalInputResponse", false)
	vec[abi.FetchExternalInputResponse](t, "FetchExternalInputResponseContent", false)
	vec[abi.FetchExternalInputResponse](t, "FetchExternalInputResponseEmptyContent", false)

	// The pack-source and read-source-file request and reply payloads. Each reply
	// vector is the SUCCESS shape, which is what its failure envelope below cannot
	// be: the two shapes are the two directions of the same omission property, and
	// either alone proves nothing. A reply carrying its success member and omitting
	// both error members passes just as well against an encoder that emits an error
	// key unconditionally as against one that omits it correctly, and the failure
	// envelope is the fixture that separates them — so the pair is the assertion and
	// neither half is redundant.
	//
	// The two request payloads declare no omittable member at all, so their map head
	// is a constant and there is no omission direction to assert. They are here for
	// the head-and-order property instead: a request is the payload the guest builds
	// and the host decodes, so a key order or a head count that disagrees between the
	// generated encoder and the reference is exactly the desynchronisation a
	// same-generator round-trip cannot see.
	//
	// The packed-source reply carries three files whose paths are in the host's byte
	// order — Chart.yaml before charts/ before templates/, which is byte order rather
	// than case-insensitive order — and one of them carries content that is not valid
	// UTF-8, so the file bytes are proven to cross as an opaque byte string rather
	// than as text. Its base64 also exercises both non-alphanumeric alphabet
	// characters and all three padding widths across the four vectors, which the
	// hand-written base64 codecs in the other two languages reach on no other vector.
	vec[abi.SourcePackRequest](t, "SourcePackRequest", false)
	vec[abi.SourcePackResponse](t, "SourcePackResponse", false)
	vec[abi.SourceFileRequest](t, "SourceFileRequest", false)
	vec[abi.SourceFileResponse](t, "SourceFileResponse", false)

	// The socket payloads whose governance and label-transform vectors the suite
	// carried without an oracle anchor.
	vec[abi.RenderPolicyInput](t, "RenderPolicyInput", false)
	vec[abi.AnnotationTransformInput](t, "AnnotationTransformInput", false)
	vec[abi.AnnotationTransformOutput](t, "AnnotationTransformOutput", false)
	vec[abi.MutatorInput](t, "MutatorInputSources", false)

	// The failure-envelope vectors: one per socket output type that declares an
	// error member, each with that member populated and the success members left
	// at their zero value.
	vec[abi.AnnotationTransformOutput](t, "ErrorEnvelopeAnnotationTransformOutput", false)
	vec[abi.AssembleBoundaryOutput](t, "ErrorEnvelopeAssembleBoundaryOutput", false)
	vec[abi.FRPStepOutput](t, "ErrorEnvelopeFRPStepOutput", false)
	vec[abi.ImportOutput](t, "ErrorEnvelopeImportOutput", false)
	vec[abi.LinkPublishOutput](t, "ErrorEnvelopeLinkPublishOutput", false)
	vec[abi.LinkResolveOutput](t, "ErrorEnvelopeLinkResolveOutput", false)
	vec[abi.LinterOutput](t, "ErrorEnvelopeLinterOutput", false)
	vec[abi.MutatorOutput](t, "ErrorEnvelopeMutatorOutput", false)
	vec[abi.NameBoundOutput](t, "ErrorEnvelopeNameBoundOutput", false)
	vec[abi.ParseOutput](t, "ErrorEnvelopeParseOutput", false)
	vec[abi.PolicyOutput](t, "ErrorEnvelopePolicyOutput", false)
	vec[abi.RenderNodeOutput](t, "ErrorEnvelopeRenderNodeOutput", false)
	vec[abi.RenderedNameOutput](t, "ErrorEnvelopeRenderedNameOutput", false)
	vec[abi.SourceFileResponse](t, "ErrorEnvelopeSourceFileResponse", false)
	vec[abi.SourcePackResponse](t, "ErrorEnvelopeSourcePackResponse", false)
	vec[abi.ValidateNodeOutput](t, "ErrorEnvelopeValidateNodeOutput", false)

	// The map-order probe vectors: one per payload declaring a map field, with
	// every such map populated by its probe key set. Their whole purpose is
	// pinning map-key order — a plain map's sort, and an order-preserving map's
	// own order, set as neither sorted order — so they are the vectors an oracle
	// anchor is worth most to: an order attested only by codecs that all order the
	// same way because one generator emitted them is attested by its own
	// assumption.
	vec[abi.AnnotationTarget](t, "MapOrderAnnotationTarget", false)
	vec[abi.ArtifactProjectionEntry](t, "MapOrderArtifactProjectionEntry", false)
	vec[abi.DocumentSet](t, "MapOrderDocumentSet", false)
	vec[abi.EmittedEntity](t, "MapOrderEmittedEntity", false)
	vec[abi.FRPStepInput](t, "MapOrderFRPStepInput", false)
	vec[abi.FRPStepOutput](t, "MapOrderFRPStepOutput", false)
	vec[abi.IRFeatureEntry](t, "MapOrderIRFeatureEntry", false)
	vec[abi.IRNode](t, "MapOrderIRNode", false)
	vec[abi.IRNodeProjection](t, "MapOrderIRNodeProjection", false)
	vec[abi.IRSpec](t, "MapOrderIRSpec", false)
	vec[abi.LinkEdge](t, "MapOrderLinkEdge", false)
	// The same payload with its endpoints, socket and provides values populated and
	// read_provides_keys carried, which the vector above leaves absent.
	vec[abi.LinkEdge](t, "MapOrderLinkEdgeReadProvidesKeys", false)
	vec[abi.LinkPublishOutput](t, "MapOrderLinkPublishOutput", false)
	vec[abi.LinkResolveInput](t, "MapOrderLinkResolveInput", false)
	vec[abi.LinterInput](t, "MapOrderLinterInput", false)
	vec[abi.MutatorGraph](t, "MapOrderMutatorGraph", false)
	vec[abi.MutatorNode](t, "MapOrderMutatorNode", false)
	// The policy payload declares no map of its own; it carries the graph, whose
	// nodes and a node's labels are order-preserving, held here in a populated payload.
	vec[abi.PolicyInput](t, "MapOrderPolicyInput", false)
	vec[abi.RenderNodeOutput](t, "MapOrderRenderNodeOutput", false)
	vec[abi.RenderedNode](t, "MapOrderRenderedNode", false)
	vec[abi.SubChart](t, "MapOrderSubChart", false)
	vec[abi.WorkloadProjection](t, "MapOrderWorkloadProjection", false)

	// Last, after every registration above: the registrations are what this
	// checks, so it cannot be a test of its own. Nothing guarantees the order in
	// which two top-level tests run, so a separate test could run before the
	// registrations had recorded anything and pass on an empty comparison — the
	// vacuous green this check exists to make impossible.
	assertCorpusFullyRegistered(t)
}

// floatVector is one entry of the shared float-boundary suite: a label, the f64's
// raw bits (hex, so NaN / ±Inf / −0.0 survive a JSON carrier that has no literal
// for them), and the canonical CBOR of a CandidateSignals whose five float fields
// all hold that value.
type floatVector struct {
	Label string `json:"label"`
	Bits  string `json:"bits"`
	Cbor  string `json:"cbor"`
}

// floatBitsTable is the boundary set the float suite pins, keyed to the raw f64
// bits so the special values round-trip through the JSON carrier exactly.
var floatBitsTable = []struct {
	label string
	value float64
}{
	{"neg_zero", math.Copysign(0, -1)},
	{"pos_zero", 0.0},
	{"one", 1.0},
	{"hundred", 100.0},
	{"f16_max", 65504.0},
	{"f16_exact_frac", 0.15625},
	{"needs_f64", 0.1},
	{"score_typical", 0.87},
	{"beyond_f32", 1e39},
	{"pos_inf", math.Inf(1)},
	{"neg_inf", math.Inf(-1)},
	{"nan", math.NaN()},
}

// TestSuite_FloatVectors anchors the shared float-boundary vectors: each value's
// CandidateSignals encoding matches the fxamacker reference and the committed
// hex, and round-trips byte-stably. The Rust and TS harnesses reconstruct the
// same f64 from the bits and assert their encoders reproduce the same hex, so the
// non-JSON-representable float cases (NaN, ±Inf, −0.0) are proven cross-language.
func TestSuite_FloatVectors(t *testing.T) {
	p := filepath.Join(testdataDir, "float_vectors.json")
	if os.Getenv("AGGREGA_UPDATE_GOLDEN") == "1" {
		var vecs []floatVector
		for _, e := range floatBitsTable {
			v := abi.CandidateSignals{Table: e.value, Category: e.value, DomainLabels: e.value, Shape: e.value, Name: e.value}
			b, err := v.EncodeCBOR()
			if err != nil {
				t.Fatalf("%s: EncodeCBOR: %v", e.label, err)
			}
			vecs = append(vecs, floatVector{
				Label: e.label,
				Bits:  hex.EncodeToString(u64be(math.Float64bits(e.value))),
				Cbor:  hex.EncodeToString(b),
			})
		}
		out, err := json.MarshalIndent(vecs, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, append(out, '\n'), 0o644); err != nil {
			t.Fatalf("write float_vectors.json: %v", err)
		}
		return
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read float_vectors.json (run AGGREGA_UPDATE_GOLDEN=1 to create): %v", err)
	}
	var vecs []floatVector
	if err := json.Unmarshal(raw, &vecs); err != nil {
		t.Fatalf("decode float_vectors.json: %v", err)
	}
	em := refEncoder(t)
	for _, fv := range vecs {
		bitsBytes, err := hex.DecodeString(fv.Bits)
		if err != nil || len(bitsBytes) != 8 {
			t.Fatalf("%s: bad bits %q", fv.Label, fv.Bits)
		}
		f := math.Float64frombits(be64(bitsBytes))
		v := abi.CandidateSignals{Table: f, Category: f, DomainLabels: f, Shape: f, Name: f}
		ours, err := v.EncodeCBOR()
		if err != nil {
			t.Fatalf("%s: EncodeCBOR: %v", fv.Label, err)
		}
		if got := hex.EncodeToString(ours); got != fv.Cbor {
			t.Errorf("%s: CBOR hex mismatch\n golden: %s\n    got: %s", fv.Label, fv.Cbor, got)
		}
		ref, err := em.Marshal(v)
		if err != nil {
			t.Fatalf("%s: reference marshal: %v", fv.Label, err)
		}
		if !bytes.Equal(ours, ref) {
			t.Fatalf("%s: generated CBOR != fxamacker reference\n generated: %s\n reference: %s",
				fv.Label, hex.EncodeToString(ours), hex.EncodeToString(ref))
		}
		var v2 abi.CandidateSignals
		if err := v2.DecodeCBOR(ours); err != nil {
			t.Fatalf("%s: DecodeCBOR: %v", fv.Label, err)
		}
		rt, _ := v2.EncodeCBOR()
		if !bytes.Equal(ours, rt) {
			t.Errorf("%s: round-trip not byte-stable", fv.Label)
		}
	}
}

func u64be(v uint64) []byte {
	return []byte{
		byte(v >> 56), byte(v >> 48), byte(v >> 40), byte(v >> 32),
		byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v),
	}
}

func be64(b []byte) uint64 {
	var v uint64
	for _, x := range b {
		v = v<<8 | uint64(x)
	}
	return v
}
