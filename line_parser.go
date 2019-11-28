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
	"regexp"
	"strings"

	"github.com/cockroachdb/errors"
)

// ParseLine parses a line of datadriven input language and returns
// the parsed command and CmdArgs.
func ParseLine(line string) (cmd string, cmdArgs []CmdArg, err error) {
	fields, err := splitDirectives(line)
	if err != nil {
		return "", nil, err
	}
	if len(fields) == 0 {
		return "", nil, nil
	}
	cmd = fields[0]

	for _, arg := range fields[1:] {
		key := arg
		var vals []string
		if pos := strings.IndexByte(key, '='); pos >= 0 {
			key = arg[:pos]
			val := arg[pos+1:]

			if len(val) > 2 && val[0] == '(' && val[len(val)-1] == ')' {
				vals = strings.Split(val[1:len(val)-1], ",")
				for i := range vals {
					vals[i] = strings.TrimSpace(vals[i])
				}
			} else {
				vals = []string{val}
			}
		}
		cmdArgs = append(cmdArgs, CmdArg{Key: key, Vals: vals})
	}
	return cmd, cmdArgs, nil
}

var splitDirectivesRE = regexp.MustCompile(`^ *[-a-zA-Z0-9/_,\.]+(|=[-a-zA-Z0-9_@=+/,\.]*|=\([^)]*\))( |$)`)

// splits a directive line into tokens, where each token is
// either:
//  - a,list,of,things        # this is just one argument
//  - argument
//  - argument=a,b,c,d        # this is just one value string
//  - argument=               # = empty value string
//  - argument=(values, ...)  # a comma-separated array of value strings
func splitDirectives(line string) ([]string, error) {
	var res []string

	origLine := line
	for line != "" {
		str := splitDirectivesRE.FindString(line)
		if len(str) == 0 {
			column := len(origLine) - len(line) + 1
			return nil, errors.Newf("cannot parse directive at column %d: %s", column, origLine)
		}
		res = append(res, strings.TrimSpace(line[0:len(str)]))
		line = line[len(str):]
	}
	return res, nil
}
