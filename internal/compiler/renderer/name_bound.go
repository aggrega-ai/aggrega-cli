// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"fmt"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/labels"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// NameBounder is the optional capability a provider engine implements to answer
// whether a proposed component identity yields names its target accepts.
//
// It carries a QUESTION rather than a table, and the answer carries a CONSTRAINT
// rather than a name. What a target accepts differs by the kind of resource a
// name lands on, spans several incompatible grammars, is sometimes compositional
// and is sometimes absent entirely, so no fixed structure declares it correctly
// for long. A guest that answers a question about one proposal needs no such
// structure, and a host that reads a constraint learns nothing about the target
// beyond how short to make its next attempt.
//
// An engine that does not implement it is never asked, and the identity the
// compiler composed is used as composed. That is the honest position rather than
// a gap: with no answer from the target, the compiler holds no limit of its own
// to apply, and inventing one would be one target's rule imposed on every
// target.
type NameBounder interface {
	NameBound(ctx context.Context, in NameBoundInput) (NameBoundOutput, error)
}

// nameBoundMaxPasses is how many times the compiler will compose an identity and
// ask the guest about it before it stops and reports the guest.
//
// More than one pass is needed because the two sides know different things:
// shortening is host-generic and the grammar is target-specific, so shortening to
// a length the guest named can land a character in a position that target's
// grammar refuses, and the guest answers again with a smaller bound. That
// exchange is how the two agree without the host learning the grammar, and it is
// why one pass is not enough.
//
// A bound is what makes the exchange terminate rather than merely usually
// terminate. Each pass the guest is free to return any bound, so nothing in the
// protocol forces progress; a guest that returns the same bound it just refused
// would otherwise loop forever, inside a build, with no output and no report.
// The figure is small because a converging exchange takes very few passes — each
// refusal after the first is a grammar edge rather than a length — and a figure
// large enough to hide a non-converging guest would defeat the purpose of having
// one.
const nameBoundMaxPasses = 8

// nameVerdict is the compiler's reading of a guest's answer about one proposed
// identity: whether the guest accepted it, and — where it did not — the
// constraint the compiler acts on and the words it reports.
//
// The bound is carried beside a flag rather than as a sentinel value, because
// absent and zero are different answers. Absent says length is not what the
// target objected to, so no shorter identity would help and there is nothing to
// try. Zero says the target accepts no name of this kind at any length. Folding
// them together would turn the first into an endless narrowing toward a bound
// that was never the obstacle.
type nameVerdict struct {
	usable  bool
	bounded bool
	bound   int
	kind    string
	reason  string
}

// askNameBound puts one proposed identity to a provider engine and reads the
// answer into a single verdict.
//
// A proposal is usable when every name kind the guest reports is usable — the
// guest reports every kind it derives, so a silent kind is not a passing one.
// Where kinds refuse, the compiler takes the SMALLEST bound any of them carries,
// because an identity has to satisfy all of them at once and the smallest is the
// only one that can. Ties keep the earlier entry, so a guest reporting its kinds
// in a fixed order gets the same reason reported twice for the same input.
//
// A refusal carrying no bound at all wins over one that carries a bound, and
// that ordering is deliberate: no shortening repairs it, so reporting the
// bounded kind instead would send a reader off to shorten a name that would
// still be refused for the other reason.
//
// An engine that does not implement the capability is reported usable without
// being asked.
func askNameBound(ctx context.Context, engine any, in NameBoundInput) (nameVerdict, error) {
	bounder, ok := engine.(NameBounder)
	if !ok {
		return nameVerdict{usable: true}, nil
	}
	out, err := bounder.NameBound(ctx, in)
	if err != nil {
		return nameVerdict{}, err
	}

	verdict := nameVerdict{usable: true}
	for _, name := range out.Names {
		if name.Usable {
			continue
		}
		candidate := nameVerdict{kind: name.Kind, reason: name.Reason}
		if name.Bound != nil {
			candidate.bounded = true
			candidate.bound = *name.Bound
		}
		if verdict.usable {
			verdict = candidate
			continue
		}
		// An unbounded refusal is the one no shortening repairs, so it is reported
		// whatever else refused; between two bounded refusals the smaller bound is
		// the binding one.
		if verdict.bounded && (!candidate.bounded || candidate.bound < verdict.bound) {
			verdict = candidate
		}
	}
	return verdict, nil
}

// nameBoundQuestion is the question put to a guest about one proposal: the
// proposal, whether a person wrote it, and the facts of the node's resolved
// workload that some of the target's names are built from.
//
// The workload facts are carried as the workload states them — its form as the
// template produced it, its Service keys in the order it declares them — and
// read by nothing here. Which names they lengthen or bring into being is the
// target's knowledge, held in its guest; the compiler only hands over what it
// resolved. A nil workload carries neither, which is the question a guest has
// always been asked, so a guest that reads neither answers exactly as before.
func nameBoundQuestion(proposed string, authored bool, workload *types.Workload) NameBoundInput {
	in := NameBoundInput{Proposed: proposed, Authored: authored}
	if workload != nil {
		in.WorkloadForm = workload.Form
		if keys := workload.Services.Keys(); len(keys) > 0 {
			in.ServiceKeys = keys
		}
	}
	return in
}

// SettleAuthoredComponentIdentity refuses an identity a person wrote that the
// node's target will not accept.
//
// It shortens nothing. An authored identity is the deployed name of the
// workload, so writing it into the target's alphabet would rename every resource
// in every existing output for no gain — and the person able to change it is the
// person who wrote it, which is why the refusal names the field they wrote
// rather than a coordinate no document contains.
//
// The target's own words are carried through: the kind of name that refused and
// why, exactly as the guest reported them. Where the guest also said how short
// the name would have to be, that number is reported too — not as an instruction
// to truncate, but because an author choosing a new name is better off knowing
// the budget than discovering it a second time.
//
// workload is the node's resolved workload, or nil for a node declaring none. It
// is asked here, after that workload is resolved, so a name the target derives
// from it — a Service named after one of its keys, the job a scheduled workload
// emits — is judged as the node will render it, and the refusal still names the
// one field the developer wrote. Where the name the target refuses is built from
// a part the Recipe's author wrote as well, the target's reason says so.
//
// A usable identity, and an engine that answers no such question, both return
// nil.
func SettleAuthoredComponentIdentity(ctx context.Context, engine any, nodeID, authoredField string, workload *types.Workload) error {
	verdict, err := askNameBound(ctx, engine, nameBoundQuestion(nodeID, true, workload))
	if err != nil {
		return cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
			fmt.Sprintf("node %q name bound", nodeID))
	}
	if verdict.usable {
		return nil
	}

	detail := []string{
		fmt.Sprintf("component %q", nodeID),
		fmt.Sprintf("written at %s", authoredField),
	}
	if verdict.kind != "" {
		detail = append(detail, fmt.Sprintf("which the target cannot use as a %s", verdict.kind))
	}
	if verdict.reason != "" {
		detail = append(detail, verdict.reason)
	}
	// A bound of zero says no name of any length is accepted, which is a budget no
	// author can choose within, so it is not offered as one; the target's reason
	// above says what has to change instead.
	remedy := "choose a name the target accepts"
	if verdict.bounded && verdict.bound > 0 {
		remedy = fmt.Sprintf("choose a name of at most %d characters that the target accepts", verdict.bound)
	}
	return cerrors.Wrap(ErrComponentIdentityTargetRefuses(), cerrors.CatalogError,
		cerrors.SourceLocation{File: authoredField},
		cerrors.ComposeMessage(ErrComponentIdentityTargetRefuses(), detail, remedy))
}

// SettleComposedComponentIdentity composes the identity a derived node's
// resources are stamped with, shortening it until the node's target accepts it.
//
// The loop is the whole mechanism. The compiler composes with the bound it
// currently holds — none, on the first pass — and asks the guest. A refusal
// carrying a bound is a shorter composition and another question, because
// shortening to a length can land a character where that target's grammar
// refuses it and only the guest knows that. A refusal carrying no bound is a
// name no length repairs, and the build stops rather than stamping one the
// target never agreed to.
//
// It is deterministic in the sense the contract states: identical parts and an
// identical guest yield an identical number of passes and an identical result,
// because every step is a pure function of the previous answer.
//
// The uniqueness of a shortened identity is PROBABILISTIC and rests on the
// duplicate check every derived name is declared into, not on the derivation. No
// injective map exists from unbounded parts into a bounded name, so no bound and
// no digest width can make shortening injective; what the digest buys is odds,
// and what the duplicate check buys is that two identities meeting is reported
// rather than deployed.
//
// workload is the derived node's resolved workload, carried on every pass so the
// guest judges each shorter proposal against the same names; nil carries none,
// for a node declaring no workload or an identity settled before its workload is
// resolved.
func SettleComposedComponentIdentity(
	ctx context.Context,
	engine any,
	scope labels.ComponentIdentityScope,
	guestName string,
	workload *types.Workload,
	parts ...string,
) (string, error) {
	bound := labels.Unbounded
	proposed := ""
	for pass := 0; pass < nameBoundMaxPasses; pass++ {
		proposed = labels.ComposeComponentIdentity(scope, bound, parts...)
		verdict, err := askNameBound(ctx, engine, nameBoundQuestion(proposed, false, workload))
		if err != nil {
			return "", cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: proposed},
				fmt.Sprintf("composed identity %q name bound", proposed))
		}
		if verdict.usable {
			return proposed, nil
		}
		// A bound of zero or less is a length statement, not an absent one: it says
		// no identity of any length reaches an acceptable name of that kind. It is
		// read here rather than composed against, because composing against it would
		// re-enter the unbounded composition and put the exchange in a loop that
		// ends only at the pass limit, reporting a guest defect where the guest was
		// answering plainly.
		if !verdict.bounded || verdict.bound <= 0 {
			detail := []string{fmt.Sprintf("composed identity %q", proposed)}
			if verdict.kind != "" {
				detail = append(detail, fmt.Sprintf("which the target cannot use as a %s", verdict.kind))
			}
			if verdict.reason != "" {
				detail = append(detail, verdict.reason)
			}
			return "", cerrors.Wrap(ErrComponentIdentityTargetRefuses(), cerrors.CatalogError,
				cerrors.SourceLocation{File: proposed},
				cerrors.ComposeMessage(ErrComponentIdentityTargetRefuses(), detail,
					"rename the parts the identity is composed from so the target accepts the name they reach"))
		}
		// A bound the composition cannot reach is the target's answer, not a guest
		// defect, and telling the two apart is what keeps the report honest. The
		// scheme reserves a run for the last part and a digest before it measures
		// anything, so there is a shortest name it can produce from a given set of
		// parts; a bound below that is unreachable however many passes are spent.
		// Detected by MEASURING the next composition rather than by computing the
		// floor, because the floor depends on the parts — a short last part reserves
		// less — and a computed one would be a second copy of the scheme's own
		// arithmetic, agreeing with it today and drifting when a reserve moves.
		next := labels.ComposeComponentIdentity(scope, verdict.bound, parts...)
		if len(next) > verdict.bound {
			detail := []string{
				fmt.Sprintf("composed identity %q", next),
				fmt.Sprintf("which the target bounds to %d characters as a %s", verdict.bound, verdict.kind),
				fmt.Sprintf("and %d is shorter than any identity the compiler can compose from these parts", verdict.bound),
			}
			// The target's own reason is carried here as on every other refusal, because
			// the name it bounds can be built from more than the identity — a Service
			// named after a key the Recipe declares — and only the reason says which part
			// left too little room.
			if verdict.reason != "" {
				detail = append(detail, verdict.reason)
			}
			return "", cerrors.Wrap(ErrComponentIdentityTargetRefuses(), cerrors.CatalogError,
				cerrors.SourceLocation{File: next},
				cerrors.ComposeMessage(ErrComponentIdentityTargetRefuses(), detail,
					"shorten the parts the identity is composed from"))
		}
		bound = verdict.bound
	}
	return "", cerrors.Wrap(ErrNameBoundNotConverging(), cerrors.TranslationError,
		cerrors.SourceLocation{File: proposed},
		cerrors.ComposeMessage(ErrNameBoundNotConverging(), []string{
			fmt.Sprintf("provider guest %q", guestName),
			fmt.Sprintf("refused %d proposals, the last of them %q", nameBoundMaxPasses, proposed),
		}, "report the defect to whoever maintains that provider plugin"))
}
