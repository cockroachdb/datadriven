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
	"strings"

	"github.com/cockroachdb/errors"
)

// ParseLine parses a datadriven directive line and returns the parsed command
// and CmdArgs.
//
// An input directive line is a command optionally followed by a list of
// arguments. Arguments may or may not have values and are specified with one of
// the forms:
//  - <argname>                            # No values.
//  - <argname>=<value>                    # Single value.
//  - <argname>=(<value1>, <value2>, ...)  # Multiple values.
//
// Note that in the last case, we allow the values to contain parens; the
// parsing will take nesting into account. For example:
//   cmd exprs=(a + (b + c), d + f)
// is valid and produces the expected values for the argument.
//
func ParseLine(line string) (cmd string, cmdArgs []CmdArg, err error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", nil, nil
	}
	origLine := line

	defer func() {
		if r := recover(); r != nil {
			if r == (parseError{}) {
				column := len(origLine) - len(line) + 1
				cmd = ""
				cmdArgs = nil
				err = errors.Errorf("cannot parse directive at column %d: %s", column, origLine)
				// Note: to debug an unexpected parsing error, this is a good place to
				// add a debug.PrintStack().
			} else {
				panic(r)
			}
		}
	}()

	// until removes the prefix up to one of the given characters from line and
	// returns the prefix.
	until := func(chars string) string {
		idx := strings.IndexAny(line, chars)
		if idx == -1 {
			idx = len(line)
		}
		res := line[:idx]
		line = line[idx:]
		return res
	}

	cmd = until(" ")
	if cmd == "" {
		panic(parseError{})
	}
	line = strings.TrimSpace(line)

	for line != "" {
		var arg CmdArg
		arg.Key = until(" =")
		if arg.Key == "" {
			panic(parseError{})
		}
		if line != "" && line[0] == '=' {
			// Skip the '='.
			line = line[1:]

			if line == "" || line[0] == ' ' {
				// Empty value.
				arg.Vals = []string{""}
			} else if line[0] != '(' {
				// Single value.
				val := until(" ")
				arg.Vals = []string{val}
			} else {
				// Skip the '('.
				line = line[1:]

				// To allow for nested parens, we implement a recursive descent parser
				// which operates on a slice of runes and keeps track of the current
				// position.
				var runes []rune
				var pos int

				// untilMatchedParen advances pos until a ')' is reached that does not
				// match an earlier '('. After the call, pos will point right after ')'.
				//
				// For example, if runes[pos:] is "x+(a+(b+c)))y" before the call, it
				// will be "y" after the call.
				var untilMatchedParen func()
				untilMatchedParen = func() {
					for pos < len(runes) {
						pos++
						if runes[pos-1] == ')' {
							// Done.
							return
						}
						if runes[pos-1] == '(' {
							untilMatchedParen()
						}
					}
					// Did not find the matching paren.
					panic(parseError{})
				}

				for done := false; !done; {
					pos = 0
					runes = []rune(line)
					for pos < len(runes) && runes[pos] != ')' && runes[pos] != ',' {
						pos++
						if runes[pos-1] == '(' {
							untilMatchedParen()
						}
					}
					if pos == len(runes) {
						// Did not find the matching paren.
						panic(parseError{})
					}
					done = runes[pos] == ')'
					val := string(runes[:pos])
					arg.Vals = append(arg.Vals, val)
					line = strings.TrimSpace(string(runes[pos+1:]))
				}
			}
		}
		cmdArgs = append(cmdArgs, arg)
		line = strings.TrimSpace(line)
	}
	return cmd, cmdArgs, nil
}

type parseError struct{}
