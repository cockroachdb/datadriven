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

import "strings"

// lineScanner iterates over the lines of a test file. Unlike bufio.Scanner, it
// has no limit on the length of a line.
type lineScanner struct {
	// rest is the part of the file that follows the current line.
	rest string
	// text is the current line, without the end-of-line marker.
	text string
	// line is the 1-based number of the current line.
	line int
}

func newLineScanner(data []byte) *lineScanner {
	return &lineScanner{rest: string(data)}
}

// Scan advances to the next line, which is then available through Text; it
// returns false if there are no more lines. The end-of-line marker is a newline
// optionally preceded by a carriage return; the last line doesn't need one.
func (l *lineScanner) Scan() bool {
	if l.rest == "" {
		l.text = ""
		return false
	}
	l.text, l.rest, _ = strings.Cut(l.rest, "\n")
	l.text = strings.TrimSuffix(l.text, "\r")
	l.line++
	return true
}

// Text returns the current line, without the end-of-line marker.
func (l *lineScanner) Text() string {
	return l.text
}
