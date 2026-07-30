package render

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func StripANSI(value string) string {
	return ansi.Strip(value)
}

func DisplayWidth(value string) int {
	return lipgloss.Width(StripANSI(value))
}

func sanitizeInline(value string) string {
	var out strings.Builder
	for index := 0; index < len(value); {
		if value[index] == 0x1b {
			index = skipEscapeSequence(value, index)
			continue
		}
		r, size := utf8.DecodeRuneInString(value[index:])
		if r == utf8.RuneError && size == 1 {
			index++
			continue
		}
		index += size
		if isBidiControl(r) || unicode.IsControl(r) || r == '\u007f' {
			continue
		}
		out.WriteRune(r)
	}
	return strings.TrimSpace(out.String())
}

func skipEscapeSequence(value string, start int) int {
	index := start + 1
	if index >= len(value) {
		return index
	}
	switch value[index] {
	case ']':
		index++
		for index < len(value) {
			if value[index] == '\a' {
				return index + 1
			}
			if value[index] == 0x1b && index+1 < len(value) && value[index+1] == '\\' {
				return index + 2
			}
			index++
		}
		return index
	case '[':
		index++
		for index < len(value) {
			if value[index] >= 0x40 && value[index] <= 0x7e {
				return index + 1
			}
			index++
		}
		return index
	default:
		return index + 1
	}
}

func isBidiControl(r rune) bool {
	switch r {
	case '\u061c', '\u200e', '\u200f',
		'\u202a', '\u202b', '\u202c', '\u202d', '\u202e',
		'\u2066', '\u2067', '\u2068', '\u2069':
		return true
	default:
		return false
	}
}

func fitOutput(value string, width int) string {
	if width <= 0 {
		return ""
	}
	trailingNewline := strings.HasSuffix(value, "\n")
	lines := strings.Split(strings.TrimSuffix(value, "\n"), "\n")
	for index := range lines {
		lines[index] = truncateWidth(lines[index], width)
	}
	out := strings.Join(lines, "\n")
	if trailingNewline {
		out += "\n"
	}
	return out
}

func truncateWidth(value string, width int) string {
	if DisplayWidth(value) <= width {
		return value
	}
	var out strings.Builder
	for _, r := range value {
		candidate := out.String() + string(r)
		if DisplayWidth(candidate) > width {
			break
		}
		out.WriteRune(r)
	}
	return out.String()
}
