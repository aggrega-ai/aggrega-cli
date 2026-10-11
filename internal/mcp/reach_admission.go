// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// The scope that admits each reach class, and the whole of the mapping between
// the two vocabularies.
//
// It is a fixed mapping from a class to a scope rather than from a scope to a
// list of tools. A list of tool names beside the tool registry would be a second
// copy of what the operation set already declares, and it would drift the first
// time a tool was renamed or added; a class is declared once per operation —
// and, where a command saves what its operation only renders, once per command
// in the command line's own records — and every surface reads it from there.
//
// Machine reach appears here as the class no scope admits. That is not an
// omission to be filled in later: a caller reaching this server over HTTP does
// not own the host, and the operations of that class are carried only by the
// generated command tools, which a session reached over HTTP is composed
// without. The floor is therefore structural and this mapping restates it
// rather than enforcing it.
//
// It names the scope a refusal asks for. Which classes some scope admits at all
// — the question a listing over a listener asks — is read from the implications
// below rather than from here, because that question is the call check's own
// one asked of every published scope at once, and a second route to its answer
// would agree with the call check only until one of the two was edited.
func scopeAdmitting(class opsurface.Reach) string {
	switch class {
	case opsurface.ReachNone:
		return scopeRead
	case opsurface.ReachWorkspace:
		return scopeWorkspaceWrite
	case opsurface.ReachRegistry:
		return scopeRegistryWrite
	default:
		return ""
	}
}

// scopeImplications is what carrying one scope says about the others.
//
// The protocol's authorization design asks a server to account for scope
// hierarchies, where a broader scope implies a narrower one. Both write scopes
// imply the read scope, so a caller granted the right to write inside the
// workspace need not also be granted the right to read: a credential that could
// write but not read would be one no authorization server administrator would
// think to issue, and the refusal it earned would be unreadable.
//
// It is written as what each scope admits rather than as what each scope
// implies, because the admission is the question every reader here is asking
// and an implication would have to be resolved into one anyway.
var scopeImplications = map[string][]opsurface.Reach{
	scopeRead:           {opsurface.ReachNone},
	scopeWorkspaceWrite: {opsurface.ReachNone, opsurface.ReachWorkspace},
	scopeRegistryWrite:  {opsurface.ReachNone, opsurface.ReachRegistry},
}

// reachAdmission is which reach classes one request may run, and which tools a
// listing answered to that request names.
//
// It is a property of the request rather than of the session: this server holds
// nothing between requests, a caller reaching a listener presents its credential
// on every one of them, and two requests arriving on one connection may carry
// different ones. So it is computed where a request is answered and never stored
// beside the server.
//
// The zero value admits nothing and lists nothing, which is the value a request
// that asserted nothing gets. That direction is deliberate: a credential check
// that admitted a request while attaching nothing to it would otherwise hand that
// request the whole surface, and a mapping whose zero value is "everything" fails
// open on exactly the path nobody is looking at.
type reachAdmission struct {
	// classes is what the request's own credential admits, and it is read by
	// the call check alone.
	classes map[opsurface.Reach]bool

	// reachable is every class some credential on this transport could be
	// admitted to, whatever this request's own credential holds: every class
	// for a caller at this machine's keyboard, and over a listener exactly the
	// classes some scope this server publishes admits, for a request that
	// presented a credential at all. A request over a listener that presented
	// none is given no class here, as it is given none above.
	//
	// It is what a listing reads where the call check reads the credential. A
	// client acting for a user offers a model only the tools it listed, so a
	// tool withheld from the list for want of a scope is one no call ever
	// names, and the refusal that would tell the client which scope to obtain
	// never arrives. A class no scope admits is a different matter: no
	// credential changes it, so a tool of that class stays out of the list as a
	// tool of a withdrawn class does.
	reachable map[opsurface.Reach]bool

	// denied is what this deployment does not carry, whatever the request's
	// credential asserted. It is kept beside the scope answer rather than
	// subtracted from it, because the two produce different refusals: a class
	// a scope would admit and the deployment withdrew is not reachable with a
	// better token, and telling its caller to obtain one would send it round a
	// loop it cannot finish. A listing reads it where it reads no scope answer,
	// which is the second reason the two are kept apart.
	denied map[opsurface.Reach]bool

	// announced names the tools this server announces, and nil announces every
	// tool it has. It is a property of the server rather than of the request —
	// no credential widens or narrows it — and it is carried here anyway, on
	// the ground the withdrawal above is carried here: this is the one
	// predicate every listing and the call check already pass through, and a
	// second place a tool can be hidden is a second answer that can come to
	// disagree with the first about one request.
	//
	// It is asked apart from the scope answer and the withdrawal because a
	// caller can act on none of the three in the same way: a scope is obtained,
	// a withdrawn class is not reachable at all, and a tool this server does not
	// announce is one the person running it chose not to offer. A refusal that
	// conflated them would tell a caller to do something that would change
	// nothing.
	announced map[string]bool
}

// deniedClasses reads the reach kinds a deployment declared it does not carry.
//
// Every value is held to the operation set's own vocabulary, and one outside it
// is refused rather than ignored. Ignoring it is the direction that fails
// quietly in the wrong way: an operator who misspells a kind has asked for a
// bound and would be served without one, and nothing about the running server
// would say so.
//
// A nil or empty declaration yields nil, so a deployment that named nothing
// carries no withdrawal and the predicate that reads this does no lookup at all.
func deniedClasses(declared []string) (map[opsurface.Reach]bool, error) {
	if len(declared) == 0 {
		return nil, nil
	}
	out := make(map[opsurface.Reach]bool, len(declared))
	for _, value := range declared {
		class := opsurface.Reach(value)
		if !slices.Contains(opsurface.Classes(), class) {
			return nil, fmt.Errorf("mcp.deny_reach names %q, which is not a kind of reach; the kinds are %s",
				value, strings.Join(reachClassNames(), ", "))
		}
		out[class] = true
	}
	return out, nil
}

// reachClassNames renders the closed set for a refusal, read from the operation
// set rather than written here, so a kind added there reaches this sentence with
// no edit.
func reachClassNames() []string {
	classes := opsurface.Classes()
	out := make([]string, 0, len(classes))
	for _, class := range classes {
		out = append(out, strconv.Quote(string(class)))
	}
	return out
}

// admitEveryClass is the admission a caller at this machine's keyboard holds.
//
// A client that launched this process reaches it over the process's own streams
// and runs as the person who started it, so there is no credential to read and
// nothing a class could bound that the caller does not already have. The scopes
// exist to bound a caller who is somewhere else.
func admitEveryClass() reachAdmission {
	classes := make(map[opsurface.Reach]bool, len(opsurface.Classes()))
	for _, class := range opsurface.Classes() {
		classes[class] = true
	}
	return reachAdmission{classes: classes, reachable: classes}
}

// admissionFromScopes is what the scopes a credential asserted admit.
//
// A credential asserting no scope at all admits no call. There is no anonymous
// read of any surface here, and a token carrying no scope is a token whose
// authorization server granted its bearer none of what this deployment
// publishes — which is a different thing from a token nobody presented, and each
// call it makes is answered the way a call outside the admitted set is. What it
// is listed is not narrowed by that, because no listing reads a credential's
// scopes: it is told what this server carries and refused at each call, naming
// the scope that would admit it.
//
// A scope this deployment does not publish is ignored rather than refused. An
// authorization server may issue a token carrying scopes for several resource
// servers, so a scope naming another one is not a fault in the credential.
func admissionFromScopes(scopes []string) reachAdmission {
	classes := make(map[opsurface.Reach]bool, len(opsurface.Classes()))
	for _, scope := range scopes {
		for _, class := range scopeImplications[scope] {
			classes[class] = true
		}
	}
	return reachAdmission{classes: classes}
}

// reachableOverAListener is every class some scope this server publishes admits:
// what a credential carrying all of them would be admitted to.
//
// It is the call check's own mapping asked once for every published scope, so a
// class a scope is taught to admit reaches the listing with no edit here, and a
// class no scope admits stays out of it for the same reason a call to it is
// refused with no challenge.
func reachableOverAListener() map[opsurface.Reach]bool {
	return admissionFromScopes(publishedScopes()).classes
}

// admittedReach is what this request may run, and what a listing answered to it
// names, on the transport that composed this session.
//
// The deployment's own withdrawal is applied here rather than beside any reader,
// because this is the one predicate every listing and the call check already
// pass through: a second filter wrapping one of them would be a second place a
// tool can be hidden, and two places that hide tools are two answers that can
// come to disagree about one request.
//
// A session speaking over the process's own streams carries no withdrawal at
// all. The set is installed where an HTTP handler is built and nowhere else, so
// the absence is structural rather than a branch here: the block it is written
// in configures a listener, and a caller at this machine's keyboard is bounded
// by owning the machine rather than by what this deployment chose to serve.
//
// What this server announces is attached last and on every path, which is what
// makes it independent of the transport without anything here asking which
// transport it is on. The client a narrowing exists for is usually the one that
// launched this process, so a bound reached only through the branch above would
// miss the case it was written for.
func (s *server) admittedReach(ctx context.Context) reachAdmission {
	a := s.classAdmission(ctx)
	a.announced = s.announcedTools
	return a
}

// classAdmission is which reach classes this request may run, before what this
// server announces is read.
//
// It is separated from the composition above so that the announcement is
// attached at one point rather than at each path the class answer takes. An
// attachment per path would be a place per path to forget one.
//
// A listing is answered only to a caller who presented a credential, and that
// holds here as well as at the authenticator. What a listing over a listener may
// carry is attached to a request carrying a principal, whatever scopes that
// principal holds and none included, and to no other request. A path that
// reached a listing without passing the credential check is therefore answered
// with nothing rather than with the whole surface: the zero value is what it
// gets, with what mcp.deny_reach withdraws still attached, because that bound
// holds whatever a request asserted.
func (s *server) classAdmission(ctx context.Context) reachAdmission {
	if s.transport != transportHTTP {
		return admitEveryClass()
	}
	p := principalFrom(ctx)
	if p == nil {
		return reachAdmission{denied: s.deniedReach}
	}
	a := admissionFromScopes(p.Scopes)
	a.reachable = reachableOverAListener()
	a.denied = s.deniedReach
	return a
}

// admits reports whether a call reaching this far may run.
func (a reachAdmission) admits(class opsurface.Reach) bool {
	return !a.denied[class] && a.classes[class]
}

// announces reports whether this server announces the named tool. A server told
// to announce nothing in particular announces every tool it has.
func (a reachAdmission) announces(tool string) bool {
	return a.announced == nil || a.announced[tool]
}

// lists reports whether a listing answered to this request names the tool: one
// this server announces, of a class it carries and some credential on this
// transport could be admitted to.
//
// It reads nothing the request's credential asserted, which is what separates it
// from the call check. The two answer different questions — what this server
// carries, and whether this call may run — so a tool the
// credential does not admit is listed and refused at the call, naming the scope
// that would admit it, which is the refusal a client acting for a user answers
// by signing in again for that scope. Past whether a credential was presented at
// all, what it reads is only what no credential can change, so a tool it leaves
// out of a credentialed caller's list is one no better token would put back.
//
// It is read at one site, the filter below, so every listing applies the same
// bounds in the same order and a tool cannot be offered by one surface and
// withheld by another.
func (a reachAdmission) lists(spec toolSpec) bool {
	class := declaredReach(spec)
	return a.announces(spec.name) && !a.denied[class] && a.reachable[class]
}

// deniedByDeployment reports whether this deployment withdrew the class,
// separately from whether a scope would have admitted it.
//
// The two are asked apart because a caller can act on one of them and not the
// other, and a refusal that conflated them would tell a caller to obtain a
// credential that would change nothing.
func (a reachAdmission) deniedByDeployment(class opsurface.Reach) bool {
	return a.denied[class]
}

// boundsAnything reports whether this admission refuses any class at all.
//
// It exists for the one reader that would otherwise pay to compute something
// that could not change its answer: a call whose own arguments can widen the
// class it reaches has to resolve a workspace to find out, and a caller nothing
// bounds is admitted whatever the widening produces.
func (a reachAdmission) boundsAnything() bool {
	for _, class := range opsurface.Classes() {
		if !a.admits(class) {
			return true
		}
	}
	return false
}

// listed reports the specs from the given set a listing answered to this request
// names, in the order they were given.
//
// It is the one filter every listing on this server passes through, which is
// what makes the tool list and the capability tool's answer agree for one
// request: they read disjoint registries and one predicate, rather than two
// predicates that agree today.
//
// A listing reads the class each tool DECLARES — its operation's, or for a
// generated tool its command's — and never a widening the tool's own arguments
// would produce, because a listing is composed before any arguments exist. So a
// tool that widens is listed under the class it declares, and a call whose
// widening the credential does not admit is refused naming the scope that admits
// the widening.
func (a reachAdmission) listed(specs []toolSpec) []toolSpec {
	out := make([]toolSpec, 0, len(specs))
	for i := range specs {
		if a.lists(specs[i]) {
			out = append(out, specs[i])
		}
	}
	return out
}

// declaredReach is the class a tool reaches, as its spec recorded it when the
// tool entered a registry.
//
// A spec carrying no class is taken to reach the machine — the widest class,
// which no scope admits — rather than nothing. The empty value is not a class,
// so something has to say what it means, and this says it once: a tool nobody
// declared a bound for is bounded by the widest one. The curated and deferred
// registries cannot produce such a spec, because registration refuses an
// identifier the operation set does not hold; a generated command tool can, for
// a command the availability records account for no operation at, and those exist
// only on the transport that admits every class. So the value is unreachable
// today and is chosen for the direction it fails in.
func declaredReach(spec toolSpec) opsurface.Reach {
	if !slices.Contains(opsurface.Classes(), spec.reach) {
		return opsurface.ReachMachine
	}
	return spec.reach
}

// admissionRefusal reports what a call is answered with when this request may
// not run it, and nil when it may.
//
// It reads the same admission every listing is composed from, and asks what this
// server announces first, as a listing does. Reading the announcement first is
// what keeps the two answers agreeing about one request — a tool this server does
// not announce is absent from every listing whatever its class, so a refusal
// naming a scope would send a caller after a credential that would not put the
// tool back. Past the announcement it asks what the credential admits, which no
// listing asks: a tool listed to a caller whose credential does not admit it is
// refused here, naming the one scope that would.
//
// The class is read twice and in one order: the class the tool declares, then
// the widening the tool's own arguments produce where that tool has one.
// Reading the declared class first is what keeps the refusal specific — a call
// refused for its declared class is told the scope that admits the tool at all,
// rather than the scope that admits the widening it also happened to ask for.
//
// It is called after the workspace-selection refusal and before the tool runs,
// so a call naming a workspace this session does not let it name is answered
// about that rather than about a scope, and nothing a refused call asked for has
// run by the time it is refused.
//
// Each class it settles is recorded on the request as it is settled, rather than
// being returned for somebody to record. This is the only point at which both
// are known — the declared class needs the resolved tool, and the escalation
// needs the arguments that tool was sent — so a record written anywhere else
// would have to derive them a second time, and a second derivation agrees on the
// day it is written and stops agreeing the first time one of the two learns about
// an input the other did not. A request nothing is recording takes no branch
// here: the recording is a no-op where there is no recorder.
func (s *server) admissionRefusal(ctx context.Context, spec *toolSpec, args json.RawMessage) *rpcError {
	admission := s.admittedReach(ctx)
	if !admission.announces(spec.name) {
		return toolNotAnnounced(spec.name)
	}
	declared := declaredReach(*spec)
	if !admission.admits(declared) {
		noteReach(ctx, declared, declared)
		return admission.refuse(spec.name, declared)
	}
	if spec.reachEscalation == nil || !admission.boundsAnything() {
		noteReach(ctx, declared, declared)
		return nil
	}
	widened := spec.reachEscalation(s, args)
	noteReach(ctx, declared, widened)
	if !admission.admits(widened) {
		return admission.refuse(spec.name, widened)
	}
	return nil
}

// refuse composes what a caller is told when a call reaches a class this
// request may not run, reading which of the two bounds turned it away.
//
// A deployment's withdrawal is reported before a scope, because it is the one a
// caller cannot lift: where the deployment does not carry a class, no token
// admits it and no challenge would help, so naming a scope there would be an
// instruction that cannot be followed.
func (a reachAdmission) refuse(tool string, class opsurface.Reach) *rpcError {
	if a.deniedByDeployment(class) {
		return reachWithdrawnByDeployment(tool, class)
	}
	return insufficientScope(tool, class)
}

// reachWithdrawnByDeployment is what a caller is told when the class a call
// reaches is one this deployment chose not to carry.
//
// It names the configuration setting, because the operator reading a refused
// call is the one who can lift it and the setting is the only thing they can
// act on. It carries no challenge: a challenge invites a caller to obtain a
// scope, and no scope reaches a class the deployment withdrew.
func reachWithdrawnByDeployment(tool string, class opsurface.Reach) *rpcError {
	return &rpcError{
		Code:       codeInvalidRequest,
		httpStatus: statusInsufficientScope,
		Message: tool + " " + reachInCallerWords(class) + ", and this deployment does not carry that: " +
			"its mcp.deny_reach setting names " + string(class) + ". No credential lifts it, because the bound " +
			"is the deployment's rather than yours. Whoever runs this server can change that setting; until " +
			"they do, the tool list this server answers you with leaves this tool out.",
	}
}

// What each class reaches, in the words a caller reads. They are the classes'
// own definitions written for somebody deciding what to ask their authorization
// server for, rather than the vocabulary the set declares them in.
func reachInCallerWords(class opsurface.Reach) string {
	switch class {
	case opsurface.ReachWorkspace:
		return "writes inside the workspace this server serves"
	case opsurface.ReachRegistry:
		return "changes what a registry holds"
	case opsurface.ReachMachine:
		return "changes what every workspace on this server's host shares"
	default:
		return "only reads and leaves nothing behind"
	}
}

// insufficientScope is what a caller is told when what it presented does not
// admit what it asked for: what the call would have reached, and the one scope
// that would admit it.
//
// It names the scope to obtain rather than listing what the credential carried.
// A caller cannot act on being told what it already has, and reporting the
// scopes back would put a credential's contents in an answer for no gain.
//
// A class no scope admits earns a refusal that says so and carries no challenge.
// Inviting a caller to obtain a scope that does not exist would send it round a
// loop it cannot finish, and the honest answer is that this is not reachable
// from where it is calling rather than that it needs a better token.
func insufficientScope(tool string, class opsurface.Reach) *rpcError {
	scope := scopeAdmitting(class)
	if scope == "" {
		return &rpcError{
			Code:       codeInvalidRequest,
			httpStatus: statusInsufficientScope,
			Message: tool + " " + reachInCallerWords(class) + ", which no credential admits to a caller " +
				"reaching this server over HTTP. An operation of that reach is run at a terminal on the host " +
				"instead, which is why the tool list this server answers you with carries none.",
		}
	}
	return &rpcError{
		Code:              codeInvalidRequest,
		httpStatus:        statusInsufficientScope,
		insufficientScope: scope,
		Message: tool + " " + reachInCallerWords(class) + ", and the credential this call presented does " +
			"not carry a scope that admits it. Obtain a token carrying " + scope + " and call again. The tool " +
			"list this server answers you with names every tool it carries whatever your credential holds; " +
			"the credential decides which of them a call may run.",
	}
}
