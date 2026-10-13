// Package cliargs rebuilds process.argv the way Node's Windows entry point
// sees it: wmain receives the command line split by the MSVC UCRT, and each
// UTF-16 argument is converted with WideCharToMultiByte(CP_UTF8), which turns
// every unpaired surrogate into one U+FFFD.
package cliargs

import "unicode/utf16"

// SplitUCRT splits a Windows command line with the UCRT rules
// (argv_parsing.cpp parse_command_line) and returns the arguments after the
// program name. The program name ends at the first space or tab outside
// quotes, and quotes only toggle there. In arguments, 2N backslashes before
// a quote give N backslashes and toggle quoting, 2N+1 give N and a literal
// quote, and `""` inside quotes is one literal quote that keeps quoting on.
func SplitUCRT(cmd []uint16) [][]uint16 {
	i := 0
	inQuotes := false
	for i < len(cmd) {
		c := cmd[i]
		if c == '"' {
			inQuotes = !inQuotes
			i++
			continue
		}
		if !inQuotes && (c == ' ' || c == '\t') {
			break
		}
		i++
	}
	var args [][]uint16
	inQuotes = false
	for {
		for i < len(cmd) && (cmd[i] == ' ' || cmd[i] == '\t') {
			i++
		}
		if i >= len(cmd) {
			return args
		}
		arg := []uint16{}
		for {
			copyChar := true
			backslashes := 0
			for i < len(cmd) && cmd[i] == '\\' {
				i++
				backslashes++
			}
			if i < len(cmd) && cmd[i] == '"' {
				if backslashes%2 == 0 {
					if inQuotes && i+1 < len(cmd) && cmd[i+1] == '"' {
						i++ // "" inside quotes: one literal quote
					} else {
						copyChar = false
						inQuotes = !inQuotes
					}
				}
				backslashes /= 2
			}
			for ; backslashes > 0; backslashes-- {
				arg = append(arg, '\\')
			}
			if i >= len(cmd) || (!inQuotes && (cmd[i] == ' ' || cmd[i] == '\t')) {
				break
			}
			if copyChar {
				arg = append(arg, cmd[i])
			}
			i++
		}
		args = append(args, arg)
	}
}

// FromWide is WideCharToMultiByte(CP_UTF8, 0, ...): UTF-16 to UTF-8 with
// each unpaired surrogate replaced by U+FFFD.
func FromWide(units []uint16) string { return string(utf16.Decode(units)) }
