// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/aggrega-ai/aggrega-cli/internal/cliplugin"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/machinedirs"
	"github.com/aggrega-ai/aggrega-cli/pkg/ociref"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// cliPluginDispatcher runs a configured CLI plugin identified by its artifact
// URI, streaming the guest's output and returning its exit code and the content
// digest of the bytes it resolved and ran. command is the resolved subcommand
// name the operator invoked, handed to the guest so a single guest backing
// several commands can route on which command was called. It is the seam a
// plugin command's RunE calls; the production value is a cliplugin.Runner, and a
// test substitutes a fake that records the command and argument vector and
// returns a chosen exit code and digest without a real guest.
type cliPluginDispatcher interface {
	Run(ctx context.Context, uri, command string, argv []string) (int, string, error)
}

// installedPluginRunnerFor builds the dispatcher that runs a configured CLI
// plugin command for a resolved workspace. The production value resolves the
// plugin URI through the same workspace-derived cascade the install path uses —
// the catalog entity view and the shared compilation cache — so any URI an
// install accepts is resolvable at run time. It is a
// package-level seam so a test can substitute a fake dispatcher that records the
// argument vector without a real guest or workspace.
var installedPluginRunnerFor = func(rw *workspaceconfig.ResolvedWorkspace) cliPluginDispatcher {
	return cliplugin.Runner{Resolve: workspacePluginResolver(rw), Digest: digest.ContentDigest}
}

// resolveCLIPluginDigest resolves the bytes uri names through the workspace
// cascade and returns their content digest, so the run gate can compare them to
// the pinned digest before executing the guest. It is a package-level seam so a
// test can force a drift outcome without a real guest.
var resolveCLIPluginDigest = func(rw *workspaceconfig.ResolvedWorkspace, uri string) (string, error) {
	resolved, err := workspacePluginResolver(rw)(uri)
	if err != nil {
		return "", err
	}
	return digest.ContentDigest(resolved.Bytes), nil
}

// cliPluginRegistrations yields the CLI-extension commands to register for this
// invocation, derived from the resolved config cascade and the describe cache.
//
// It defaults to contributing nothing, and the process entry point wires the
// live implementation — the same shape the unknown-command hint's workspace uses.
// The default matters because command-tree construction happens before any
// command runs, so the live implementation has no resolved workspace to be
// handed and resolves one from the working directory. Defaulting to the live
// implementation would make every caller of the tree constructor read whatever
// configuration sits above its working directory: a test would take the
// developer's own configuration as input, and the generated command reference
// would vary with whichever CLI plugins the machine building it happens to have
// configured. A test that wants registrations substitutes a fixture, so tree
// construction touches neither disk nor a guest.
var cliPluginRegistrations = func() []cliCommandReg { return nil }

// pluginRegistrationWarnWriter receives the diagnostics emitted when a configured
// plugin cannot be registered as a command. It defaults to standard error and is
// redirected by tests to observe the diagnostics.
var pluginRegistrationWarnWriter io.Writer = os.Stderr

// unknownCommandWorkspace resolves the workspace whose local registry index the
// unknown-command hint reads, or nil for none.
//
// It defaults to resolving none, and the process entry point wires the live
// resolution, for the reason the command registrations above default to nothing:
// the unknown-command boundary is reached after dispatch has already failed and
// carries no resolved workspace of its own, so the live form resolves one from the
// working directory. Defaulting to it would make a test, or the generated command
// reference, read whatever configuration and index sit above the directory it was
// started in. With none resolved the hint reads no index, and says how to build
// one.
var unknownCommandWorkspace = func() *workspaceconfig.ResolvedWorkspace { return nil }

// reservedCommandNames are command names the CLI reserves for anticipated
// built-ins, so a configured CLI plugin can never squat a name the CLI intends
// to claim later. The names present in a freshly built tree are the
// authoritative gate and are checked separately against root.Commands() —
// built-ins always win — so this set deliberately lists reserved-but-not-yet-
// built-in names rather than re-enumerating the live built-ins (which would be a
// second copy that could drift from the tree).
var reservedCommandNames = map[string]bool{
	"build":    true,
	"deploy":   true,
	"render":   true,
	"apply":    true,
	"validate": true,
	"plugin":   true,
	"cache":    true,
}

// cliCommandReg is one top-level command a configured CLI plugin contributes:
// the command verb and its short-help line (from the plugin's describe
// metadata), the flags the command declares (also from describe, in declared
// order), the plugin artifact URI the runner resolves and runs, and the digest
// the config pinned that URI to. Registration builds one per command; the flags
// are declared on the cobra command so help renders them like a core command's,
// while the guest remains the parser; the run gate compares the freshly resolved
// bytes' digest to that pin before running.
type cliCommandReg struct {
	name   string
	short  string
	flags  []wasm.PluginCommandFlag
	uri    string
	digest string
}

// cliPluginIdentity names the configured plugin a registration came from: the
// artifact reference and the digest the config pinned it to. Every command one
// plugin contributes carries the same identity, which is what lets registration
// skip a plugin whole rather than command by command.
type cliPluginIdentity struct {
	uri    string
	digest string
}

func (r cliCommandReg) plugin() cliPluginIdentity {
	return cliPluginIdentity{uri: r.uri, digest: r.digest}
}

// The value types a CLI plugin may declare for a flag. Each names the kind of
// flag help renders it as; a flag declaring no type renders as a string flag.
//
// The Plugin entity schema's command mirror states the same set as an enum on
// its flag type, and the shorthand shape isPluginFlagShorthand accepts as a
// pattern. The two sides are held together by a test driving one corpus of
// declarations through both, since the schema and this check sit on either side
// of a language boundary and neither can read the other.
const (
	pluginFlagTypeString = "string"
	pluginFlagTypeBool   = "bool"
	pluginFlagTypeInt    = "int"
)

// productionCLIPluginRegistrations is the seam-facing form of
// productionCLIPluginRegistrationsContext, for the command-tree construction
// that reaches it.
//
// The tree is built before any command body runs, so there is no command context
// to take, and taking one would reshape the root-command constructor that every
// test in this package builds. The Context form below is where the work lives,
// so a caller that does hold a context resolves and describes under it.
func productionCLIPluginRegistrations() []cliCommandReg {
	return productionCLIPluginRegistrationsContext(context.Background())
}

// productionCLIPluginRegistrationsContext resolves the workspace and projects
// its plugins.cli list into the commands to register. A resolution failure is
// reported as a diagnostic and treated as no configured CLI plugins rather than
// failing the tree, mirroring how a malformed config surfaces elsewhere.
//
// ctx reaches the cold-cache describe below, which loads and runs each
// configured guest; a guest call whose context ends unwinds rather than being
// noticed only once the guest next returns.
func productionCLIPluginRegistrationsContext(ctx context.Context) []cliCommandReg {
	return cliRegistrationsResolving(ctx, "", machinedirs.Host(), envLookupOrGetenv(nil))
}

// cliRegistrationsResolving resolves the workspace from start, on platform and
// reading the environment through lookup, and projects its plugins.cli list
// into the commands to register.
//
// A global configuration file named for this run — by AGGREGA_CONFIG — that
// does not exist yet is not a failure here. It declares no command extension,
// because it does not exist, while the project's own files may declare some, so
// the workspace is resolved again without the global layer and theirs are
// registered. Nothing is said about the missing file, and that is deliberate:
// this runs while the command tree is built, before the command is known, so a
// warning here would be printed for every command — including the seeding
// command, which is about to create the file and says so, and every command that
// reads its configuration, which halts on the missing file in its own words.
// Every other failure to resolve is reported once and leaves no command
// registered.
func cliRegistrationsResolving(ctx context.Context, start string, platform machinedirs.Platform, lookup func(string) string) []cliCommandReg {
	rw, err := workspaceconfig.ResolveOn(platform, start, "", lookup)
	var missing *workspaceconfig.GlobalConfigNotFoundError
	if errors.As(err, &missing) {
		rw, err = workspaceconfig.ResolveWithoutGlobalOn(platform, start, "", lookup)
	}
	if err != nil {
		fmt.Fprintf(pluginRegistrationWarnWriter, "warning: ignoring configured CLI plugins: %v\n", err)
		return nil
	}
	return cliRegistrationsFromWorkspace(ctx, rw)
}

// cliRegistrationsFromWorkspace projects a resolved workspace's plugins.cli list
// into the commands to register, in the list's author order (the deterministic
// registration order). Each entry's command names and help come from the
// describe cache keyed by the pinned digest; a cold cache falls back to resolving
// and describing the guest and warms the cache, so the cache is an optimization
// and never a requirement. An entry whose bytes cannot be resolved or described
// on a cold cache is skipped with a diagnostic rather than failing the tree.
func cliRegistrationsFromWorkspace(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace) []cliCommandReg {
	resolve := workspacePluginResolver(rw)
	var regs []cliCommandReg
	for _, ref := range rw.Config().Plugins.CLI {
		desc, ok := readDescribeCache(ref.Digest)
		if !ok {
			d, derr := describeConfiguredCLI(ctx, ref, resolve)
			if derr != nil {
				fmt.Fprintf(pluginRegistrationWarnWriter,
					"warning: CLI plugin %q was not added as a command: %v\n", ref.URI, missingCLIPluginRemedy(ref.URI, derr))
				continue
			}
			desc = d
		}
		for _, command := range desc.Commands {
			regs = append(regs, cliCommandReg{name: command.Name, short: command.Description, flags: command.Flags, uri: ref.URI, digest: ref.Digest})
		}
	}
	return regs
}

// describeConfiguredCLI is the cold-cache fallback for cliRegistrationsFromWorkspace:
// it resolves ref's bytes and describes the guest under the describe-only inert
// profile, caching the descriptor under the resolved bytes' content digest so a
// later run reads it without loading the guest again.
func describeConfiguredCLI(ctx context.Context, ref workspaceconfig.CLIPluginRef, resolve pluginpath.Lookup) (wasm.PluginDescriptor, error) {
	resolved, err := resolve(ref.URI)
	if err != nil {
		return wasm.PluginDescriptor{}, err
	}
	spec := resolver.PluginSpec(pluginDiagName(ref.URI), resolved)
	if spec.MaxMemoryPages == 0 {
		spec.MaxMemoryPages = wasm.DescribeMaxMemoryPages
	}
	loader := wasm.NewCachingLoaderFromContext(ctx, nil)
	plugin, desc, err := loader.LoadForDescribe(ctx, spec)
	if err != nil {
		return wasm.PluginDescriptor{}, err
	}
	_ = plugin.Close(ctx)
	writeDescribeCache(digest.ContentDigest(resolved.Bytes), desc)
	return desc, nil
}

// registerInstalledCLIPlugins adds one top-level command per configured CLI
// plugin command to root, so a freshly built tree exposes the configured plugins
// as commands. It runs after the static commands are added, so the live tree is
// the authoritative built-in set. A name that collides with a built-in already
// in the tree, that is on the reserved denylist, or that another CLI plugin
// already contributed this invocation is skipped with a diagnostic — built-ins
// always win, reserved names stay reserved, and one command name registers once.
// Each registered command's help comes from the describe cache; no guest is
// loaded to list or help while that cache is warm.
//
// Every command a plugin contributes is built, and its flags declared, before
// any command is attached. A plugin one of whose commands declares a flag the
// host cannot declare for help is skipped whole — none of its commands is added
// — with one diagnostic naming the plugin, the command, the flag and what is
// wrong with it. The flags are declared here, while the tree is built and before
// any command runs, so a declaration the flag library refuses would otherwise
// stop every command in the workspace, built-ins included, rather than only the
// plugin that made it. The registrations read here are the same whether the
// describe cache or a fresh describe of the guest supplied them, so the check
// covers a plugin either way.
func registerInstalledCLIPlugins(root *cobra.Command) {
	regs := cliPluginRegistrations()
	if len(regs) == 0 {
		return
	}

	carried := flagsEveryCommandCarries(root)
	built := make([]*cobra.Command, len(regs))
	refused := map[cliPluginIdentity]bool{}
	for i, reg := range regs {
		if refused[reg.plugin()] {
			continue
		}
		cmd, err := newCLIPluginCmd(reg, carried)
		if err != nil {
			refused[reg.plugin()] = true
			fmt.Fprintf(pluginRegistrationWarnWriter,
				"warning: CLI plugin %q was not added as a command: %v\n", reg.uri, err)
			continue
		}
		built[i] = cmd
	}

	builtin := map[string]bool{}
	for _, c := range root.Commands() {
		builtin[c.Name()] = true
	}

	added := map[string]bool{}
	for i, reg := range regs {
		if refused[reg.plugin()] {
			continue
		}
		switch {
		case builtin[reg.name]:
			fmt.Fprintf(pluginRegistrationWarnWriter,
				"warning: CLI plugin %q was not added as a command: a built-in command with that name already exists\n", reg.name)
			continue
		case reservedCommandNames[reg.name]:
			fmt.Fprintf(pluginRegistrationWarnWriter,
				"warning: CLI plugin %q was not added as a command: %q is a reserved command name\n", reg.name, reg.name)
			continue
		case added[reg.name]:
			fmt.Fprintf(pluginRegistrationWarnWriter,
				"warning: CLI plugin %q was not added as a command: the command %q is already contributed by another CLI plugin\n", reg.name, reg.name)
			continue
		}
		added[reg.name] = true
		mustAttachContributed(root, built[i])
	}
}

// flagsEveryCommandCarries returns the flags a command attached under root holds
// besides the ones it declares: the root's persistent flags, which every command
// inherits, and the help flag the command library gives every command. Both are
// read from the library rather than listed here, so a global flag added to the
// root is covered without a second list to keep in step with it.
func flagsEveryCommandCarries(root *cobra.Command) *pflag.FlagSet {
	carried := pflag.NewFlagSet(root.Name(), pflag.ContinueOnError)
	carried.AddFlagSet(root.PersistentFlags())
	probe := &cobra.Command{Use: "probe"}
	probe.InitDefaultHelpFlag()
	carried.AddFlagSet(probe.Flags())
	return carried
}

// newCLIPluginCmd builds the cobra command that dispatches a configured CLI
// plugin. Its help comes from the describe cache, so building the tree never
// loads a guest while that cache is warm. The command's declared flags are
// registered on it so that 'aggrega help <command>' renders them in a Flags
// section like a core command's flags; this is a help-rendering surface only.
// Flag parsing stays disabled so the whole argument vector after the command
// name — flag tokens included — is forwarded verbatim to the guest, which is the
// sole parser of its own flags. Before running, the command re-resolves the
// plugin's bytes and compares their content digest to the digest pinned in the
// config: a mismatch halts the command loudly with a re-pin instruction, so bytes
// that drifted from the pin never execute. On a match it resolves and executes
// the plugin through the shared runner, threading the command's own name to the
// guest as the invoked-command discriminator; a non-zero exit is returned as an
// exitCodeError so the process mirrors the plugin's status without treating it as
// a host error.
//
// carried is the set of flags every command already holds, which a declared flag
// may not reuse. A declaration the host cannot declare for help returns an error
// naming the command, the flag and the fault, and no command.
func newCLIPluginCmd(reg cliCommandReg, carried *pflag.FlagSet) (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:                reg.name,
		Short:              reg.short,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			current, err := resolveCLIPluginDigest(rw, reg.uri)
			if err != nil {
				return missingCLIPluginRemedy(reg.uri, err)
			}
			if current != reg.digest {
				return fmt.Errorf(
					"the %q plugin's resolved bytes no longer match the pinned digest "+
						"(pinned %s, resolved %s); re-pin it with 'aggrega plugin add cli %s' before running it",
					reg.name, shortDigest(reg.digest), shortDigest(current), reg.name)
			}
			code, _, err := installedPluginRunnerFor(rw).Run(cmd.Context(), reg.uri, reg.name, args)
			if err != nil {
				return err
			}
			if code != 0 {
				return exitCodeError{code: code}
			}
			return nil
		},
	}
	if err := declarePluginCommandFlags(cmd, reg.flags, carried); err != nil {
		return nil, err
	}
	return cmd, nil
}

// declarePluginCommandFlags registers a configured CLI plugin command's declared
// flags on cmd so help renders them like a core command's flags, and checks every
// declaration before it declares any. It declares nothing unless every
// declaration passes, and otherwise returns the first that does not:
//
//   - a flag with no name, or a name the command declares twice;
//   - a shorthand other than a single ASCII letter or digit, or one the command
//     declares for two flags;
//   - a name or shorthand already held by a flag in carried, the set every
//     command carries;
//   - a type other than string, bool or int;
//   - a default that does not parse as the declared type.
//
// A declaration the flag library refuses would otherwise panic while the command
// tree is built, and one it accepts could still misreport in help: an unknown
// type shown as a string flag, a default that does not parse shown as the type's
// zero value.
//
// A bool flag declares a bool, an int flag an int, and a flag of type string or
// of no type a string; a declared default is parsed by the flag library itself,
// so help shows the default the library holds. The declaration is a
// help-rendering surface only: because the command keeps flag parsing disabled,
// cobra never parses these flags and the guest remains the sole parser of the
// forwarded argument vector. So the check judges whether the host can declare a
// flag as written — whether the guest reads the flags it declares is outside
// what the host can see.
func declarePluginCommandFlags(cmd *cobra.Command, flags []wasm.PluginCommandFlag, carried *pflag.FlagSet) error {
	command := cmd.Name()
	declared := pflag.NewFlagSet(command, pflag.ContinueOnError)
	declared.SortFlags = false
	for _, f := range flags {
		if err := checkPluginFlag(command, f, declared, carried); err != nil {
			return err
		}
		if err := declarePluginFlag(declared, f); err != nil {
			return fmt.Errorf("its command %q declares the flag %q with the default %q, which is not a valid %s value",
				command, "--"+f.Name, f.Default, f.Type)
		}
	}
	cmd.Flags().AddFlagSet(declared)
	return nil
}

// checkPluginFlag reports what, if anything, stops f from being declared on the
// command named command alongside the flags already in declared. Every check
// runs before the flag library sees the declaration, because the library answers
// a malformed or repeated shorthand, and a repeated name, by panicking.
func checkPluginFlag(command string, f wasm.PluginCommandFlag, declared, carried *pflag.FlagSet) error {
	long := "--" + f.Name
	switch {
	case f.Name == "":
		return fmt.Errorf("its command %q declares a flag with no name", command)
	case declared.Lookup(f.Name) != nil:
		return fmt.Errorf("its command %q declares the flag %q twice", command, long)
	case carried.Lookup(f.Name) != nil:
		return fmt.Errorf("its command %q declares the flag %q, which every aggrega command already has", command, long)
	}
	if f.Shorthand != "" {
		if !isPluginFlagShorthand(f.Shorthand) {
			return fmt.Errorf("its command %q declares the flag %q with the shorthand %q; a shorthand is a single ASCII letter or digit",
				command, long, f.Shorthand)
		}
		if other := declared.ShorthandLookup(f.Shorthand); other != nil {
			return fmt.Errorf("its command %q declares the shorthand %q for both %q and %q",
				command, f.Shorthand, "--"+other.Name, long)
		}
		if other := carried.ShorthandLookup(f.Shorthand); other != nil {
			return fmt.Errorf("its command %q declares the shorthand %q for %q, which every aggrega command already uses for %q",
				command, f.Shorthand, long, "--"+other.Name)
		}
	}
	switch f.Type {
	case "", pluginFlagTypeString, pluginFlagTypeBool, pluginFlagTypeInt:
		return nil
	default:
		return fmt.Errorf("its command %q declares the flag %q with the type %q; a flag's type is %s, %s or %s",
			command, long, f.Type, pluginFlagTypeString, pluginFlagTypeBool, pluginFlagTypeInt)
	}
}

// isPluginFlagShorthand reports whether s is a shorthand a CLI plugin may
// declare: exactly one ASCII letter or digit. The flag library measures a
// shorthand in bytes, so a single non-ASCII character is already more than it
// takes; within ASCII, a letter or a digit is what reads as a short option, where
// a hyphen, a space or punctuation would render in help as something other than
// the -x form it stands for.
func isPluginFlagShorthand(s string) bool {
	if len(s) != 1 {
		return false
	}
	c := s[0]
	return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')
}

// declarePluginFlag declares f on fs as the kind of flag its type names, holding
// its declared default. The default is handed to the flag's own value, so it is
// parsed exactly as the flag library parses a value of that kind; an error means
// the default is not a value of the declared type.
func declarePluginFlag(fs *pflag.FlagSet, f wasm.PluginCommandFlag) error {
	switch f.Type {
	case pluginFlagTypeBool:
		fs.BoolP(f.Name, f.Shorthand, false, f.Description)
	case pluginFlagTypeInt:
		fs.IntP(f.Name, f.Shorthand, 0, f.Description)
	default:
		fs.StringP(f.Name, f.Shorthand, "", f.Description)
	}
	if f.Default == "" {
		return nil
	}
	flag := fs.Lookup(f.Name)
	if err := flag.Value.Set(f.Default); err != nil {
		return err
	}
	flag.DefValue = flag.Value.String()
	return nil
}

// shortDigest renders a content digest in an abbreviated form for a diagnostic,
// keeping the leading hex that distinguishes it while sparing the full 64-char
// string. A digest shorter than the abbreviation is returned unchanged.
func shortDigest(d string) string {
	const keep = 12
	if len(d) <= keep {
		return d
	}
	return d[:keep] + "..."
}

// exitCodeError carries a non-zero exit status that is not an error — a configured
// CLI plugin's own, or import's drift status — up to the single process-exit
// boundary. A RunE reports it without calling os.Exit itself, so Execute stays
// the one place the process exits and the status still reaches the shell
// verbatim.
type exitCodeError struct {
	code int
}

func (e exitCodeError) Error() string {
	return fmt.Sprintf("plugin command exited with status %d", e.code)
}

// installSuggestion is the single home for the wording that points a user at
// installing a plugin for a name the CLI does not recognize as a command, so the
// unknown-command hint and the plugin-install command stay in lockstep.
func installSuggestion(name string) string {
	return fmt.Sprintf("%q is not a command — install the %q plugin with 'aggrega plugin add cli %s'", name, name, name)
}

// searchSuggestion is the single home for the wording that tells a user how to
// look for a CLI plugin named after a command the CLI does not recognize, where
// the local index was read and holds no CLI plugin of that name. The command it
// names is the one cliPluginSearchCommand composes, so the printed search asks
// exactly what the hint itself asked of the index.
func searchSuggestion(name string) string {
	return fmt.Sprintf("%q is not a command — to look for a CLI plugin of that name, run: %s",
		name, cliPluginSearchCommand(name))
}

// addSuggestion is the single home for the wording that points a user at adding
// a CLI plugin named after a command the CLI does not recognize, where no local
// index says whether one is published. The command it names is the one
// cliPluginAddCommand composes: it looks the plugin up by that name at the
// registry and refuses where none is published, so it is the direct route
// whether or not such a plugin exists, and needs no index built first.
func addSuggestion(name string) string {
	return fmt.Sprintf("%q is not a command — to add a CLI plugin of that name, run: %s",
		name, cliPluginAddCommand(name))
}

// missingCLIPluginRemedy returns err restated for a configured cli plugin whose
// bytes no source on this machine holds, and err unchanged otherwise.
//
// The cascade's own refusal names a pull as the way to cache a registry plugin's
// bytes, which is the remedy for a plugin a Release uses; a cli plugin's bytes are
// cached by adding it, so the restated refusal names the command that adds it
// again, at the version its configuration pins, so the bytes it fetches are the
// ones that pin was recorded for. It still matches the not-found condition, so a
// caller testing for that condition reads it as before.
func missingCLIPluginRemedy(uri string, err error) error {
	if !errors.Is(err, pluginpath.ErrPluginArtifactNotFound()) || !workspaceconfig.LooksLikeOCIURI(uri) {
		return err
	}
	base, version, pinned := ociref.SplitVersionTag(strings.TrimPrefix(uri, "oci://"))
	query := path.Base(base)
	if pinned {
		query += ":" + version
	}
	return fmt.Errorf("%w; add it again with: %s", pluginpath.ErrPluginArtifactNotFound(), cliPluginAddCommand(query))
}

// cliPluginAddCommand is the command line that adds the CLI plugin named name,
// with the name written as one shell word so the line can be copied as printed
// whatever the user typed.
func cliPluginAddCommand(name string) string {
	return "aggrega plugin add " + string(wasm.PluginTypeCLI) + " " + shellWord(name)
}

// cliPluginSearchCommand is the search command line that looks for a CLI plugin
// named name: the name as the free-text query, narrowed to the plugin kind and the
// CLI plugin type. The name is written as one shell word, so the line can be
// copied as printed whatever the user typed.
func cliPluginSearchCommand(name string) string {
	return "aggrega search " + shellWord(name) + " --kind " + cliPluginSearchKind + " --plugin-type " + string(wasm.PluginTypeCLI)
}

// shellWord returns s as a single word a POSIX shell reads back as s: unchanged
// where every character is one a shell gives no meaning to, and otherwise wrapped
// in single quotes, with each single quote inside written as a closing quote, an
// escaped quote and an opening quote.
func shellWord(s string) string {
	plain := s != ""
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:@%+=,", r)) {
			plain = false
			break
		}
	}
	if plain {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// unknownCommandSuggestion returns formatted with a plugin hint appended when err
// is cobra's unknown-command error. For any other error it returns formatted
// unchanged. Cobra's own message, with its suggestions of similar commands, is
// always kept; the hint follows it.
//
// Which hint follows is decided by the default registry's local index alone, so
// the answer costs what reading a few files costs and a mistyped command is never
// sent to a registry: an index holding a CLI plugin of that name gets the install
// hint, an index holding none gets the search hint, and no index gets the command
// that adds a CLI plugin of that name directly.
func unknownCommandSuggestion(ctx context.Context, formatted string, err error) string {
	name, ok := unknownCommandName(err)
	if !ok {
		return formatted
	}
	switch cliPluginInLocalIndex(ctx, unknownCommandWorkspace(), name) {
	case indexHoldsCLIPlugin:
		return formatted + "\n" + installSuggestion(name)
	case indexHoldsNoCLIPlugin:
		return formatted + "\n" + searchSuggestion(name)
	default:
		return formatted + "\n" + addSuggestion(name)
	}
}

// unknownCommandName extracts the offending command name from cobra's
// unknown-command error and reports whether err is that error. Cobra formats the
// error as: unknown command "NAME" for "aggrega" (optionally followed by
// suggestions). The name is the first double-quoted token, matched precisely so
// only the unknown-command case is enriched.
func unknownCommandName(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	const prefix = `unknown command "`
	msg := err.Error()
	if !strings.HasPrefix(msg, prefix) {
		return "", false
	}
	rest := msg[len(prefix):]
	end := strings.IndexByte(rest, '"')
	if end <= 0 {
		return "", false
	}
	return rest[:end], true
}
