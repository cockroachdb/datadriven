// Copyright 2018 The Cockroach Authors.
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
	"io"
	"strings"
	"testing"
)

type testDataReader struct {
	sourceName string
	reader     io.Reader
	scanner    *lineScanner
	data       TestData
	rewrite    *bytes.Buffer
}

func newTestDataReader(
	t *testing.T, sourceName string, file io.Reader, record bool,
) *testDataReader {
	t.Helper()

	var rewrite *bytes.Buffer
	if record {
		rewrite = &bytes.Buffer{}
	}
	return &testDataReader{
		sourceName: sourceName,
		reader:     file,
		scanner:    newLineScanner(file),
		rewrite:    rewrite,
	}
}

func (r *testDataReader) Next(t *testing.T) bool {
	t.Helper()

	r.data = TestData{}
	for r.scanner.Scan() {
		line := r.scanner.Text()
		r.emit(line)

		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			// Skip comment lines.
			continue
		}
		// Support wrapping directive lines using \, for example:
		//   build-scalar \
		//   vars(int)
		for strings.HasSuffix(line, `\`) && r.scanner.Scan() {
			nextLine := r.scanner.Text()
			r.emit(nextLine)
			line = strings.TrimSuffix(line, `\`) + " " + strings.TrimSpace(nextLine)
		}

		pos := fmt.Sprintf("%s:%d", r.sourceName, r.scanner.line)
		cmd, args, err := ParseLine(pos, line)
		if err != nil {
			t.Fatalf("%s: %v", pos, err)
		}
		if cmd == "" {
			// Nothing to do here.
			continue
		}
		r.data.Pos = pos
		r.data.Cmd = cmd
		r.data.CmdArgs = args

		var buf bytes.Buffer
		var separator bool
		for r.scanner.Scan() {
			line := r.scanner.Text()
			if line == "----" {
				separator = true
				break
			}

			r.emit(line)
			fmt.Fprintln(&buf, line)
		}

		r.data.Input = strings.TrimSpace(buf.String())

		if separator {
			r.readExpected()
		}
		return true
	}
	return false
}

func (r *testDataReader) readExpected() {
	var buf bytes.Buffer
	var line string
	var allowBlankLines bool

	if r.scanner.Scan() {
		line = r.scanner.Text()
		if line == "----" {
			allowBlankLines = true
		}
	}

	if allowBlankLines {
		// Look for two successive lines of "----" before terminating.
		for r.scanner.Scan() {
			line = r.scanner.Text()

			if line == "----" {
				if r.scanner.Scan() {
					line2 := r.scanner.Text()
					if line2 == "----" {
						break
					}

					fmt.Fprintln(&buf, line)
					fmt.Fprintln(&buf, line2)
					continue
				}
			}

			fmt.Fprintln(&buf, line)
		}
	} else {
		// Terminate on first blank line.
		for {
			if strings.TrimSpace(line) == "" {
				break
			}

			fmt.Fprintln(&buf, line)

			if !r.scanner.Scan() {
				break
			}

			line = r.scanner.Text()
		}
	}

	r.data.Expected = buf.String()
}

func (r *testDataReader) emit(s string) {
	if r.rewrite != nil {
		r.rewrite.WriteString(s)
		r.rewrite.WriteString("\n")
	}
}
