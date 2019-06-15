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
	"bytes"
	"fmt"
	"go/token"
	"io"
	"io/ioutil"
	"reflect"
	"regexp"
	"testing"

	"gopkg.in/yaml.v3"
)

var yamlRE = regexp.MustCompile(`(?:^##|\n##)\s*(.*)\s*\n`)

// RunYAML runs the tests in the specified file against the driver map m. The test file takes
// the following form
//
//     ## cmdA
//     <yaml input>
//     ---
//     <yaml output>
//     ---
//
//     ## cmdB
//     <yaml input>
//     ---
//     <yaml output>
//     ---
//     [...]
//
// A driver is addressed by the string following the doubly-hashed comments at
// the beginning of the line, and will be used to map the input to the actual
// output. In the above example, the drivers cmdA and cmdB are used, and these
// must be specified in the DriverMap.
//
// A Driver is simply a `func(A) B` where A, B can be represented as YAML. For
// each test case in the order in which they appear in the file, the Driver will
// be called with an A corresponding to the input for the test case ("<yaml
// input>" above) and returns a B that must equal one populated from the
// expected output ("<yaml output>") to pass the test.
func RunYAML(t *testing.T, path string, m DriverMap) {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	runYAMLInternal(t, path, b, m)
}

// RunYAMLFromString is like RunYAML, but takes its input from a string instead
// of a file.
func RunYAMLFromString(t *testing.T, input string, m DriverMap) {
	runYAMLInternal(t, "<input>", []byte(input), m)
}

func runYAMLInternal(t *testing.T, name string, b []byte, m DriverMap) {
	cmdIdxPairs := yamlRE.FindAllSubmatchIndex(b, -1)

	if len(b) == 0 {
		t.Errorf("%s: no test cases found", name)
	}

	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)

	file := token.NewFileSet().AddFile(name, 1 /* base */, len(b))
	file.SetLinesForContent([]byte(b))

	for _, pair := range cmdIdxPairs {
		cmd := b[pair[2]:pair[3]]
		pos := file.Position(file.Pos(pair[2]))

		func() {
			defer func() {
				if r := recover(); r != nil {
					panic(fmt.Sprintf("%s: %v", pos, r))
				}
			}()

			in, exp, out, err := m.reflectCall(string(cmd), func(in, exp interface{}) error {
				if err := dec.Decode(in); err != nil {
					return err
				}
				return dec.Decode(exp)
			})
			if err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(exp, out) {
				// TODO(tbg): use a diffing pretty printer here.
				t.Errorf("input: %+v\nexpected: %+v\nactual: %+v", in, exp, out)
			}
		}()
	}

	// Make sure there isn't any more yaml in the file that we'd silently be
	// ignoring. Note that there may be a final empty document (if the test file
	// ends in `---`, so we allow that).
	var out interface{}
	if err := dec.Decode(&out); err != nil && err != io.EOF {
		t.Errorf("unexpected error while reading to end of file: %v", err)
	}
	if out != nil {
		t.Errorf("decoded extraneous test case %+v", out)
	}
}
