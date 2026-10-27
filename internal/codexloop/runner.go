package codexloop

import (
	"errors"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Mizore66/faultline/internal/jsexc"
	"github.com/Mizore66/faultline/internal/jsstr"
	"github.com/Mizore66/faultline/internal/nodefs"
	"github.com/Mizore66/faultline/internal/nodeproc"
)

// codexBinEnv ports src/runners/codex-runner.ts:5 FAULTLINE_CODEX_BIN_ENV.
const codexBinEnv = "FAULTLINE_CODEX_BIN"

// Resolution ports src/runners/codex-runner.ts:7 CodexRunnerResolution.
// Source is "ENV" or "PATH".
type Resolution struct {
	Bin, Version, Source string
}

// pathEntries ports src/runners/codex-runner.ts:20 pathEntries:
// (process.env.PATH ?? "").split(delimiter).filter(Boolean).
func pathEntries() []string {
	path, _ := nodeproc.Getenv("PATH")
	delimiter := ":"
	if runtime.GOOS == "windows" {
		delimiter = ";"
	}
	var entries []string
	for _, entry := range strings.Split(path, delimiter) {
		if entry != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

// candidateNames ports src/runners/codex-runner.ts:24 candidateNames.
func candidateNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"codex.cmd", "codex.exe", "codex"}
	}
	return []string{"codex"}
}

// trimTrailingSeparators is directory.replace(/[\\/]+$/, ""): the regex
// has no m flag, so $ is the end of the string, and the one match is the
// trailing run of slashes and backslashes.
func trimTrailingSeparators(directory string) string {
	return strings.TrimRight(directory, `\/`)
}

// resolveOnPath ports src/runners/codex-runner.ts:28 resolveOnPath. The
// candidate is checked with existsSync, which follows symlinks and accepts
// a directory. It never returns undefined: the last resort is the bare
// name, which spawnSync resolves against PATH (and fails with ENOENT).
func resolveOnPath() string {
	for _, directory := range pathEntries() {
		for _, name := range candidateNames() {
			separator := "/"
			if strings.Contains(directory, `\`) {
				separator = `\`
			}
			candidate := trimTrailingSeparators(directory) + separator + name
			if nodefs.Exists(candidate) {
				return candidate
			}
		}
	}
	// Last resort: bare name (spawn may still resolve via PATH / PATHEXT).
	if runtime.GOOS == "windows" {
		return "codex.cmd"
	}
	return "codex"
}

// codexEnv is the env object both spawnSync calls build (codex-runner.ts:44
// and :100): { PATH: process.env.PATH ?? "", ComSpec and SystemRoot on
// Windows when set and not empty }, in that key order. The CLI passes no
// options.env, so the exec's spread adds nothing. On Windows nodeproc sorts
// the pairs as normalizeSpawnArguments does.
func codexEnv() []string {
	path, _ := nodeproc.Getenv("PATH")
	env := []string{"PATH=" + path}
	if runtime.GOOS == "windows" {
		for _, name := range []string{"ComSpec", "SystemRoot"} {
			if v, _ := nodeproc.Getenv(name); v != "" {
				env = append(env, name+"="+v)
			}
		}
	} else {
		// Node encodes the env object's JS strings as UTF-8.
		for i := range env {
			env[i] = jsstr.ToUTF8(env[i])
		}
	}
	return env
}

// firstLine is .split(/\r?\n/, 1)[0] ?? "": the text before the first
// "\n", less one "\r" just before it (the match's optional \r). split
// always yields at least one element, so ?? "" never applies.
func firstLine(s string) string {
	i := strings.IndexByte(s, '\n')
	if i < 0 {
		return s
	}
	return strings.TrimSuffix(s[:i], "\r")
}

// probeVersion ports src/runners/codex-runner.ts:39 probeVersion:
// spawnSync(bin, ["--version"], { encoding: "utf8", shell: false,
// windowsHide: true, env }) with fl's cwd, no timeout, no input (stdin is a
// pipe, shut down at once) and the default maxBuffer, so an output over
// 1 MiB is the ENOBUFS error.
func probeVersion(bin string) (string, error) {
	result := nodeproc.SpawnSyncWith(bin, []string{"--version"}, nodeproc.SpawnSyncOptions{Env: codexEnv()})
	if result.Err != nil {
		return "", errors.New("Codex CLI probe failed for " + bin + ": " + result.Err.Error() +
			". Set " + codexBinEnv + " or install Codex on PATH.")
	}
	if result.Status == nil || *result.Status != 0 {
		detail := jsstr.Trim(jsstr.DecodeUTF8(result.Stderr) + jsstr.DecodeUTF8(result.Stdout))
		if detail != "" {
			detail = ": " + detail
		}
		return "", errors.New("Codex CLI --version failed for " + bin + detail +
			". Set " + codexBinEnv + " or install Codex on PATH.")
	}
	version := firstLine(jsstr.Trim(jsstr.DecodeUTF8(result.Stdout)))
	if version == "" {
		return "", errors.New("Codex CLI --version returned empty output for " + bin + ".")
	}
	return version, nil
}

// ResolveRunner ports src/runners/codex-runner.ts:73 resolveCodexRunner.
// The error is the CodexRunnerError TS throws. The override is
// process.env.FAULTLINE_CODEX_BIN?.trim(); an empty result means none. Only
// an absolute override is checked for existence: a relative one is probed
// as given, and spawnSync resolves it against PATH.
func ResolveRunner() (Resolution, error) {
	override, _ := nodeproc.Getenv(codexBinEnv)
	override = jsstr.Trim(override)
	if override != "" {
		if nodefs.IsAbsolute(override) && !nodefs.Exists(override) {
			return Resolution{}, errors.New(codexBinEnv + " points to a missing binary: " + override)
		}
		version, err := probeVersion(override)
		if err != nil {
			return Resolution{}, err
		}
		return Resolution{Bin: override, Version: version, Source: "ENV"}, nil
	}
	bin := resolveOnPath()
	// codex-runner.ts:83-87 throws "Codex CLI was not found on PATH" when
	// resolveOnPath returns undefined, which it never does (it falls back
	// to the bare name), so that branch is left out.
	version, err := probeVersion(bin)
	if err != nil {
		return Resolution{}, err
	}
	return Resolution{Bin: bin, Version: version, Source: "PATH"}, nil
}

// SpawnCodex ports src/runners/codex-runner.ts:92 spawnCodex with the
// options the CLI passes (cwd, input, timeout): it resolves the runner on
// every call and throws (jsexc.Throw) a resolution error, then runs
// spawnSync(bin, args, { encoding: "utf8", shell: false, windowsHide: true,
// cwd, input, timeout, env }) with the default maxBuffer. The input is
// written as UTF-8; spawnSync ignores a falsy input, so "" is none.
//
// normalizeSpawnArguments then throws ERR_INVALID_ARG_VALUE for an argument
// holding a NUL, which the witness implement prompt can (the incident's
// symptom comes from JSON). The bin cannot (it comes from the environment or
// PATH), nor can the cwd (fl has just created that directory).
func SpawnCodex(args []string, cwd, input string, timeout time.Duration) nodeproc.Result {
	resolved, err := ResolveRunner()
	if err != nil {
		jsexc.Throw(err)
	}
	for i, arg := range args {
		if strings.IndexByte(arg, 0) >= 0 {
			jsexc.Throw(nodeproc.InvalidArgValue("The argument 'args["+strconv.Itoa(i)+"]'", arg, "must be a string without null bytes"))
		}
	}
	return nodeproc.SpawnSyncWith(resolved.Bin, args, nodeproc.SpawnSyncOptions{
		Env:      codexEnv(),
		Cwd:      cwd,
		Input:    []byte(jsstr.ToUTF8(input)),
		HasInput: input != "",
		Timeout:  timeout,
	})
}

// CLIRunner ports the runner objects src/cli-app.ts builds for --with-codex
// (witness implement, :1181-1196, timeout 120 s; repair, :2704-2719,
// timeout 180 s). Without --allow-codex-full-auto (allowFlag) it throws
// refusal before any probe or spawn. A resolution error is the runner's
// throw. A spawn error does not throw: TS reads only status, stdout and
// stderr, so it becomes a null exit code (with whatever output there was).
func CLIRunner(allowFlag bool, refusal string, timeout time.Duration) Runner {
	return func(args []string, cwd, input string) (RunResult, error) {
		if !allowFlag {
			return RunResult{}, errors.New(refusal)
		}
		var executed nodeproc.Result
		if err := jsexc.Try(func() { executed = SpawnCodex(args, cwd, input, timeout) }); err != nil {
			return RunResult{}, err
		}
		return RunResult{
			ExitCode: executed.Status,
			Stdout:   jsstr.DecodeUTF8(executed.Stdout),
			Stderr:   jsstr.DecodeUTF8(executed.Stderr),
		}, nil
	}
}
