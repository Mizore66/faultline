package projinit

import (
	"errors"
	"io/fs"
	"runtime"
	"slices"
	"strings"

	"github.com/Mizore66/faultline/internal/jscase"
	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsjson"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/safedir"
	"github.com/Mizore66/faultline/internal/sidecar"
)

// HookCommand ports src/cli-app.ts:1607 SidecarHookCommand; HasWindows
// says whether commandWindows is present.
type HookCommand struct {
	Command, CommandWindows string
	HasWindows              bool
}

// controlChars is /[\0\r\n]/.
func controlChars(value string) bool { return strings.ContainsAny(value, "\x00\r\n") }

// assertSidecarCommandText ports src/cli-app.ts:1612.
func assertSidecarCommandText(value, label string) (string, error) {
	command := jsstr.Trim(value)
	if command == "" {
		return "", errors.New(jsexc.Concat(label, " must not be empty."))
	}
	if controlChars(command) {
		return "", errors.New(jsexc.Concat(label, " cannot contain a NUL byte or a line break."))
	}
	return command, nil
}

// errCliPathControl is the refusal of src/cli-app.ts:1620 and 1626.
var errCliPathControl = errors.New("The sidecar CLI path cannot contain a NUL byte or a line break.")

// quotePosixShellArgument ports src/cli-app.ts:1619.
func quotePosixShellArgument(value string) (string, error) {
	if controlChars(value) {
		return "", errCliPathControl
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'", nil
}

// quoteWindowsCommandArgument ports src/cli-app.ts:1625: one argv value
// quoted for CommandLineToArgvW. Backslash and '"' are ASCII, so walking
// bytes is walking code points here.
func quoteWindowsCommandArgument(value string) (string, error) {
	if controlChars(value) {
		return "", errCliPathControl
	}
	var b strings.Builder
	b.WriteByte('"')
	backslashes := 0
	for i := 0; i < len(value); i++ {
		switch c := value[i]; c {
		case '\\':
			backslashes++
		case '"':
			b.WriteString(strings.Repeat(`\`, backslashes*2+1))
			b.WriteByte('"')
			backslashes = 0
		default:
			b.WriteString(strings.Repeat(`\`, backslashes))
			b.WriteByte(c)
			backslashes = 0
		}
	}
	b.WriteString(strings.Repeat(`\`, backslashes*2))
	b.WriteByte('"')
	return b.String(), nil
}

// lstatIfPresent ports src/cli-app.ts:1648: nil for ENOENT, any other
// failure rewrapped.
func lstatIfPresent(path string) (fs.FileInfo, error) {
	info, err := nodefs.Lstat(path)
	if err != nil {
		if isENOENT(err) {
			return nil, nil
		}
		return nil, errors.New(jsexc.Concat("FaultLine could not inspect ", path, ": ", jsexc.Message(err)))
	}
	return info, nil
}

// isENOENT is filesystemErrorCode(error) === "ENOENT".
func isENOENT(err error) bool {
	var e *nodefs.Error
	return errors.As(err, &e) && e.Code == "ENOENT"
}

// option and requiredOption are src/cli-app.ts:144 and 149.
func option(args []string, flag string) (string, bool) {
	i := slices.Index(args, flag)
	if i < 0 || i+1 >= len(args) {
		return "", false
	}
	return args[i+1], true
}

func requiredOption(args []string, flag string) (string, error) {
	value, ok := option(args, flag)
	if !ok || value == "" || strings.HasPrefix(value, "--") {
		return "", errors.New("Missing required option: " + flag)
	}
	return value, nil
}

// extname is path.extname of a resolved path (no trailing separator).
func extname(p string) string {
	base := p[strings.LastIndexByte(p, '/')+1:]
	if runtime.GOOS == "windows" {
		if len(p) >= 2 && p[1] == ':' {
			p = p[2:]
		}
		base = p[strings.LastIndexAny(p, `/\`)+1:]
	}
	i := strings.LastIndexByte(base, '.')
	if i <= 0 || base == ".." {
		return ""
	}
	return base[i:]
}

// SidecarCliCommand ports src/cli-app.ts:1656 sidecarCliCommand: exactly
// one of --cli (a real, non-symlink .js file, quoted for both platforms)
// or --command (with an optional --command-windows).
func SidecarCliCommand(args []string) (HookCommand, error) {
	_, hasCommand := option(args, "--command")
	_, hasCli := option(args, "--cli")
	if hasCommand == hasCli {
		return HookCommand{}, errors.New("Provide exactly one of --cli <built-cli.js> or --command <stable-hook-command>.")
	}
	commandWindows, hasWindows := option(args, "--command-windows")
	if !hasCli {
		raw, err := requiredOption(args, "--command")
		if err != nil {
			return HookCommand{}, err
		}
		command, err := assertSidecarCommandText(raw, "--command")
		if err != nil {
			return HookCommand{}, err
		}
		if !hasWindows {
			return HookCommand{Command: command}, nil
		}
		windows, err := assertSidecarCommandText(commandWindows, "--command-windows")
		if err != nil {
			return HookCommand{}, err
		}
		return HookCommand{Command: command, CommandWindows: windows, HasWindows: true}, nil
	}
	if hasWindows {
		return HookCommand{}, errors.New("--command-windows is only valid with an explicit --command; --cli generates both platform commands safely.")
	}
	raw, err := requiredOption(args, "--cli")
	if err != nil {
		return HookCommand{}, err
	}
	cliPath := nodefs.Resolve(raw)
	if jscase.Lower(extname(cliPath)) != ".js" {
		return HookCommand{}, errors.New(jsexc.Concat("--cli must point to the built JavaScript entry point (normally dist/cli.js), not ", cliPath, "."))
	}
	info, err := nodefs.Lstat(cliPath)
	if err == nil && (!info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0) {
		err = errors.New("not a real regular file")
	}
	if err != nil {
		return HookCommand{}, errors.New(jsexc.Concat("--cli must point to an existing built, non-symlink CLI file: ", cliPath, " (", jsexc.Message(err), ")"))
	}
	return cliCommands(cliPath)
}

// cliCommands is the --cli result of sidecarCliCommand.
func cliCommands(cliPath string) (HookCommand, error) {
	posix, err := quotePosixShellArgument(cliPath)
	if err != nil {
		return HookCommand{}, err
	}
	windows, err := quoteWindowsCommandArgument(cliPath)
	if err != nil {
		return HookCommand{}, err
	}
	return HookCommand{
		Command:        jsexc.Concat("node ", posix, " codex sidecar hook --quiet"),
		CommandWindows: jsexc.Concat("node ", windows, " codex sidecar hook --quiet"),
		HasWindows:     true,
	}, nil
}

// HookConfig is codexSidecarHookConfig for the commands `--cli cliAbs`
// generates; cliAbs must hold no NUL byte or line break.
func HookConfig(cliAbs string) jsjson.Value {
	command, err := cliCommands(cliAbs)
	if err != nil {
		panic(err)
	}
	return HookConfigFor(command)
}

// HookConfigFor ports src/cli-app.ts:1689 codexSidecarHookConfig.
func HookConfigFor(command HookCommand) jsjson.Value {
	hook := func(statusMessage string) jsjson.Value {
		o := jsjson.NewObj()
		o.Set("type", str("command"))
		o.Set("command", str(command.Command))
		if command.HasWindows {
			o.Set("commandWindows", str(command.CommandWindows))
		}
		o.Set("timeout", jsjson.MakeNumber(30))
		o.Set("statusMessage", str(statusMessage))
		return jsjson.MakeObject(o)
	}
	group := func(statusMessage string) jsjson.Value {
		return jsjson.MakeArray([]jsjson.Value{jsjson.MustObject("hooks", jsjson.MakeArray([]jsjson.Value{hook(statusMessage)}))})
	}
	return jsjson.MustObject("hooks", jsjson.MustObject(
		"SessionStart", jsjson.MakeArray([]jsjson.Value{jsjson.MustObject(
			"matcher", str("startup|resume|clear|compact"),
			"hooks", jsjson.MakeArray([]jsjson.Value{hook("FaultLine observes this Codex session")}))}),
		"UserPromptSubmit", group("FaultLine records an observed Codex turn"),
		"PreToolUse", group("FaultLine records tool-use attribution (digests only)"),
		"PostToolUse", group("FaultLine records tool-use completion (digests only)"),
		"Stop", group("FaultLine captures a clean observed checkpoint when available")))
}

// realDirectory is resolveSafeDirectorySegment(path) !== null; its lstat
// error is thrown, as in TS.
func realDirectory(path string) bool {
	_, ok, err := safedir.ResolveSafeDirectorySegment(path)
	if err != nil {
		jsexc.Throw(err)
	}
	return ok
}

// HookTarget ports src/cli-app.ts:1719 sidecarProjectHookTarget: the
// repository must be a real directory and a usable Git worktree, .codex a
// real directory when present, and .codex/hooks.json absent. It returns the
// hook document's path and whether .codex already exists.
func HookTarget(repo string) (file string, codexPresent bool, err error) {
	defer jsexc.Catch(func(e error) { file, codexPresent, err = "", false, e })
	resolved := nodefs.Resolve(repo)
	info, err := lstatIfPresent(resolved)
	if err != nil {
		return "", false, err
	}
	if info == nil || !realDirectory(resolved) {
		return "", false, errors.New(jsexc.Concat("--repo must be a real directory, not a symbolic link or file: ", resolved))
	}
	if _, err := CodexSidecarLedgerPath(resolved, "faultline-install-check"); err != nil {
		return "", false, errors.New(jsexc.Concat("--repo must be a usable local Git worktree for FaultLine sidecar capture: ", jsexc.Message(err)))
	}
	directory := nodefs.Join(resolved, ".codex")
	dirInfo, err := lstatIfPresent(directory)
	if err != nil {
		return "", false, err
	}
	if dirInfo != nil && !realDirectory(directory) {
		return "", false, errors.New(jsexc.Concat("Project .codex directory must be a real directory, not a symbolic link or file: ", directory))
	}
	file = nodefs.Join(directory, "hooks.json")
	present, err := lstatIfPresent(file)
	if err != nil {
		return "", false, err
	}
	if present != nil {
		return "", false, errors.New(jsexc.Concat("FaultLine will not replace an existing project hook document: ", file, ". Run sidecar config and merge the reviewed hooks yourself."))
	}
	return file, dirInfo != nil, nil
}

// WriteHookConfig ports src/cli-app.ts:1744 writeSidecarProjectHookConfig
// for the target HookTarget(repo) accepted: mkdir .codex (0700) when
// absent, then an exclusive 0600 write of the indented JSON.
func WriteHookConfig(repo string, cfg jsjson.Value) (err error) {
	defer jsexc.Catch(func(e error) { err = e })
	directory := nodefs.Join(nodefs.Resolve(repo), ".codex")
	file := nodefs.Join(directory, "hooks.json")
	info, err := lstatIfPresent(directory)
	if err != nil {
		return err
	}
	if info == nil {
		if err := nodefs.Mkdir(directory, 0o700); err != nil {
			return err
		}
	}
	if !realDirectory(directory) {
		return errors.New(jsexc.Concat("Project .codex directory must be a real directory, not a symbolic link or file: ", directory)) // coverage gate: race only (HookTarget refused a non-directory .codex just before)
	}
	if err := nodefs.WriteFileUTF8(file, jsexc.Concat(jsjson.StringifyIndent(cfg, "  "), "\n"), 0o600, true); err != nil {
		return errors.New(jsexc.Concat("FaultLine could not create the project hook document without replacing anything: ", jsexc.Message(err)))
	}
	return nil
}

// CodexSidecarLedgerPath ports src/codex-sidecar.ts:334
// codexSidecarLedgerPath: the opaque ledger file name of a session under
// the Git common directory's recordings. The storage code lives in
// internal/sidecar; this forwards to it.
func CodexSidecarLedgerPath(cwd, sessionID string) (string, error) {
	return sidecar.LedgerPath(cwd, sessionID)
}
