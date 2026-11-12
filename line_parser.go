// Copyright 2019 The Cockroach Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
// implied. See the License for the specific language governing
// permissions and limitations under the License.

package datadriven

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ParseLine parses a directive line into the command, the flags and the
// positional arguments. An empty line results in an empty command.
//
// The words on the line are separated by whitespace, except inside
// parentheses. After the command, words that start with "-" or "--" followed
// by a letter are flags; other words are positional arguments, as are all the
// words after a "--" word. A flag has one of the forms:
//
//	-name             # No value.
//	-name=value       # Single value.
//	-name=(v1, v2)    # List of values (possibly empty).
//
// Values can contain parentheses (including whitespace and commas inside
// them); a value is a list if it is a single parenthesized group, and only the
// top-level commas inside it separate the values. A flag can be repeated (see
// TestData.FlagValues).
//
// A positional argument or the value of a flag can be written between single
// quotes, in which case it is taken literally: it can contain whitespace and
// unbalanced parentheses, it is never a list (or a flag), and two consecutive
// quotes inside it stand for a quote. The closing quote must end the word.
// Quotes are not special anywhere else (in particular inside parentheses, or
// after the start of an argument or value), and neither are double quotes.
//
// For example:
//
//	cmd -exprs=(a + (b + c), d + f) -f=g(x, y) -msg='it''s (a, b' arg1 (an arg) 'another arg' -- -arg4
//
// has flags -exprs (with the list of values "a + (b + c)" and "d + f"), -f
// (with value "g(x, y)") and -msg (with value "it's (a, b"), and the arguments
// "arg1", "(an arg)", "another arg" and "-arg4".
func ParseLine(line string) (cmd string, flags []Flag, args []string, err error) {
	cmd, flags, args, _, err = parseLine(line)
	return cmd, flags, args, err
}

// parseLine implements ParseLine. It also returns the arguments that are
// written like flags except for the dash: without quotes, and before any "--"
// word (see processedFlags.check).
func parseLine(line string) (cmd string, flags []Flag, args, bareArgs []string, err error) {
	p := lineParser{line: strings.TrimSpace(line)}
	onlyArgs := false
	for first := true; ; first = false {
		p.skipSpace()
		if p.pos == len(p.line) {
			return cmd, flags, args, bareArgs, nil
		}
		start := p.pos
		switch rest := p.line[start:]; {
		case first && (rest[0] == '\'' || isFlag(rest)):
			return "", nil, nil, nil, p.errorf(start, "expected a command")

		case rest[0] == '\'':
			arg, err := p.quotedWord()
			if err != nil {
				return "", nil, nil, nil, err
			}
			args = append(args, arg)

		case !onlyArgs && isFlag(rest):
			f, err := p.flag()
			if err != nil {
				return "", nil, nil, nil, err
			}
			flags = append(flags, f)

		default:
			w, err := p.plainWord()
			if err != nil {
				return "", nil, nil, nil, err
			}
			switch {
			case first && w == "--":
				return "", nil, nil, nil, p.errorf(start, "expected a command")
			case first:
				cmd = w
			case w == "--" && !onlyArgs:
				onlyArgs = true
			default:
				args = append(args, w)
				if !onlyArgs {
					bareArgs = append(bareArgs, w)
				}
			}
		}
	}
}

// lineParser parses the words of a directive line.
type lineParser struct {
	line string
	// pos is the offset of the next character to parse.
	pos int
}

// errorf returns an error for the character at the given offset.
func (p *lineParser) errorf(offset int, format string, args ...any) error {
	return parseError(p.line, offset+1, fmt.Sprintf(format, args...))
}

// next returns the character at the current position and its size (0 at the
// end of the line).
func (p *lineParser) next() (rune, int) {
	if p.pos == len(p.line) {
		return 0, 0
	}
	return utf8.DecodeRuneInString(p.line[p.pos:])
}

// atWordEnd returns true if the current position is the end of a word: the end
// of the line or whitespace.
func (p *lineParser) atWordEnd() bool {
	r, n := p.next()
	return n == 0 || unicode.IsSpace(r)
}

func (p *lineParser) skipSpace() {
	for r, n := p.next(); n > 0 && unicode.IsSpace(r); r, n = p.next() {
		p.pos += n
	}
}

// plainWord parses the (unquoted) text up to the end of the word: the end of
// the line or whitespace outside parentheses.
func (p *lineParser) plainWord() (string, error) {
	start := p.pos
	// openings contains the offsets of the unclosed parentheses.
	var openings []int
	for r, n := p.next(); n > 0; r, n = p.next() {
		switch {
		case r == ')':
			if len(openings) == 0 {
				return "", p.errorf(p.pos, "unmatched ')'")
			}
			openings = openings[:len(openings)-1]
		case r == '(':
			openings = append(openings, p.pos)
		}
		if unicode.IsSpace(r) && len(openings) == 0 {
			break
		}
		p.pos += n
	}
	if len(openings) > 0 {
		return "", p.errorf(openings[0], "unclosed '('")
	}
	return p.line[start:p.pos], nil
}

// quotedWord parses a quoted value, which starts at the current position and
// must extend to the end of the word, and returns the value.
func (p *lineParser) quotedWord() (string, error) {
	start := p.pos
	p.pos++
	var val strings.Builder
	for {
		n := strings.IndexByte(p.line[p.pos:], '\'')
		if n < 0 {
			return "", p.errorf(start, "unclosed quote")
		}
		val.WriteString(p.line[p.pos : p.pos+n])
		p.pos += n + 1
		if !strings.HasPrefix(p.line[p.pos:], "'") {
			break
		}
		// Two consecutive quotes stand for a quote.
		val.WriteByte('\'')
		p.pos++
	}
	if !p.atWordEnd() {
		return "", p.errorf(p.pos, "unexpected text after the closing quote")
	}
	return val.String(), nil
}

// flag parses the flag that starts at the current position (for which isFlag
// is true).
func (p *lineParser) flag() (Flag, error) {
	start := p.pos
	p.pos += len(p.line[start:]) - len(trimDashes(p.line[start:]))
	nameStart := p.pos
	for r, n := p.next(); r != '=' && !p.atWordEnd(); r, n = p.next() {
		p.pos += n
	}
	f := Flag{Name: p.line[nameStart:p.pos]}
	if strings.ContainsAny(f.Name, "()'") {
		return Flag{}, p.errorf(start, "invalid flag name %q", f.Name)
	}
	if p.atWordEnd() {
		return f, nil
	}
	// Skip the "=".
	p.pos++
	if strings.HasPrefix(p.line[p.pos:], "'") {
		val, err := p.quotedWord()
		if err != nil {
			return Flag{}, err
		}
		f.Vals = []string{val}
		return f, nil
	}
	val, err := p.plainWord()
	if err != nil {
		return Flag{}, err
	}
	if isListValue(val) {
		f.List = true
		if inner := strings.TrimSpace(val[1 : len(val)-1]); inner != "" {
			f.Vals = splitValues(inner)
		}
	} else {
		f.Vals = []string{val}
	}
	return f, nil
}

// trimDashes removes the (one or two) dashes at the start of a flag.
func trimDashes(w string) string {
	return strings.TrimPrefix(strings.TrimPrefix(w, "-"), "-")
}

// isFlag returns true if the word is a flag: it starts with "-" or "--",
// followed by a letter.
func isFlag(w string) bool {
	name := trimDashes(w)
	if len(name) == len(w) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(name)
	return unicode.IsLetter(r)
}

// isListValue returns true if the (unquoted) value of a flag, which must have
// balanced parentheses, is a list: a single parenthesized group.
func isListValue(val string) bool {
	return len(val) > 0 && val[0] == '(' && matchingParen(val) == len(val)-1
}

// isPlainWord returns true if s can be written without quotes as an argument
// or as the value of a flag (ignoring whether it would be a flag or a list).
func isPlainWord(s string) bool {
	if s == "" || s[0] == '\'' {
		return false
	}
	p := lineParser{line: s}
	w, err := p.plainWord()
	return err == nil && w == s
}

// quote returns s as a quoted value (see ParseLine).
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// matchingParen returns the offset of the parenthesis that closes the one at
// the start of s (which must have balanced parentheses).
func matchingParen(s string) int {
	depth := 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// splitValues splits s at the top-level commas, trimming the whitespace
// around the values.
func splitValues(s string) []string {
	var vals []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				vals = append(vals, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	return append(vals, strings.TrimSpace(s[start:]))
}

// parseError returns an error for the character at the given 1-based byte
// offset; the error contains the (1-based) column of the character.
func parseError(line string, offset int, msg string) error {
	col := utf8.RuneCountInString(line[:offset-1]) + 1
	return fmt.Errorf("cannot parse directive at column %d (%s): %s", col, msg, line)
}
