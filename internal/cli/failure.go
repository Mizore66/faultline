package cli

import (
	"strings"

	"github.com/Mizore66/faultline/internal/jsstr"
)

// formatFailure ports formatCliFailure in src/cli.ts.
func formatFailure(message string) string {
	windowsHint := strings.Join([]string{
		"",
		"Windows tip: If PowerShell blocked pnpm due to ExecutionPolicy restrictions, run:",
		"  Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope Process",
		"Or invoke the command proxy directly:",
		"  pnpm.cmd fl <command> (e.g., pnpm.cmd fl judge-demo)",
	}, "\n")
	if unknownDashDash(message) || strings.HasPrefix(message, "Unknown command: --") {
		return "FaultLine error: " + message + "\nNote: Do not place '--' between 'fl' and your subcommand. Use 'pnpm fl <command>'.\nNext: pnpm fl help"
	}
	if strings.HasPrefix(message, "Unknown command:") {
		return "FaultLine error: " + message + "\nNext: pnpm fl help"
	}
	lower := asciiLower(message)
	for _, needle := range []string{"executionpolicy", "running scripts is disabled", "pssecurityexception", "unauthorizedaccess"} {
		if strings.Contains(lower, needle) {
			return strings.Join([]string{"FaultLine error: " + message, windowsHint, "Next: pnpm fl help"}, "\n")
		}
	}
	return "FaultLine error: " + message + "\nNext: pnpm fl help"
}

// unknownDashDash is /^Unknown command:\s*--\b/: after optional JS
// whitespace, "--" must be followed by an ASCII word character.
func unknownDashDash(message string) bool {
	rest, ok := strings.CutPrefix(message, "Unknown command:")
	if !ok {
		return false
	}
	units := jsstr.ToUTF16(rest)
	i := 0
	for i < len(units) && jsstr.IsWhitespace(units[i]) {
		i++
	}
	if i+2 >= len(units) || units[i] != '-' || units[i+1] != '-' {
		return false
	}
	c := units[i+2]
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// asciiLower folds only ASCII letters, matching JS /i on these ASCII needles.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
