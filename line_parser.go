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
	"slices"
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
// top-level commas inside it separate the values. A flag can't be repeated. For
// example:
//
//	cmd -exprs=(a + (b + c), d + f) -f=g(x, y) arg1 (an arg) -- -arg3
//
// has flags -exprs (with the list of values "a + (b + c)" and "d + f") and -f
// (with value "g(x, y)"), and the arguments "arg1", "(an arg)" and "-arg3".
func ParseLine(line string) (cmd string, flags []Flag, args []string, err error) {
	line = strings.TrimSpace(line)
	words, err := splitWords(line)
	if err != nil {
		return "", nil, nil, err
	}
	if len(words) == 0 {
		return "", nil, nil, nil
	}
	if cmd = words[0].s; isFlag(cmd) || cmd == "--" {
		return "", nil, nil, parseError(line, words[0].col, "expected a command")
	}
	onlyArgs := false
	for _, w := range words[1:] {
		if !onlyArgs {
			if w.s == "--" {
				onlyArgs = true
				continue
			}
			if isFlag(w.s) {
				f, err := parseFlag(w.s)
				if err != nil {
					return "", nil, nil, parseError(line, w.col, err.Error())
				}
				if slices.ContainsFunc(flags, func(g Flag) bool { return g.Name == f.Name }) {
					return "", nil, nil, parseError(line, w.col, fmt.Sprintf("flag -%s is repeated", f.Name))
				}
				flags = append(flags, f)
				continue
			}
		}
		args = append(args, w.s)
	}
	return cmd, flags, args, nil
}

type word struct {
	s string
	// col is the 1-based byte offset of the start of the word.
	col int
}

// splitWords splits the line into words separated by whitespace, except inside
// parentheses.
func splitWords(line string) ([]word, error) {
	var words []word
	start := -1
	// openings contains the offsets of the unclosed parentheses.
	var openings []int
	for i, r := range line {
		switch {
		case r == ')':
			if len(openings) == 0 {
				return nil, parseError(line, i+1, "unmatched ')'")
			}
			openings = openings[:len(openings)-1]
		case r == '(':
			openings = append(openings, i)
		case unicode.IsSpace(r) && len(openings) == 0:
			if start != -1 {
				words = append(words, word{s: line[start:i], col: start + 1})
				start = -1
			}
			continue
		}
		if start == -1 {
			start = i
		}
	}
	if len(openings) > 0 {
		return nil, parseError(line, openings[0]+1, "unclosed '('")
	}
	if start != -1 {
		words = append(words, word{s: line[start:], col: start + 1})
	}
	return words, nil
}

// isFlag returns true if the word is a flag: it starts with "-" or "--",
// followed by a letter.
func isFlag(w string) bool {
	name := strings.TrimPrefix(w, "-")
	if len(name) == len(w) {
		return false
	}
	name = strings.TrimPrefix(name, "-")
	r, _ := utf8.DecodeRuneInString(name)
	return unicode.IsLetter(r)
}

// parseFlag parses a word for which isFlag is true.
func parseFlag(w string) (Flag, error) {
	w = strings.TrimPrefix(strings.TrimPrefix(w, "-"), "-")
	name, val, hasVal := strings.Cut(w, "=")
	if strings.ContainsAny(name, "()") {
		return Flag{}, fmt.Errorf("invalid flag name %q", name)
	}
	f := Flag{Name: name}
	switch {
	case !hasVal:
	case len(val) > 0 && val[0] == '(' && matchingParen(val) == len(val)-1:
		f.List = true
		if inner := strings.TrimSpace(val[1 : len(val)-1]); inner != "" {
			f.Vals = splitValues(inner)
		}
	default:
		f.Vals = []string{val}
	}
	return f, nil
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
