// Copyright 2024 The Cockroach Authors.
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

package diagram

import (
	"math"
	"strings"
)

// Whiteboard is a data structure that allows writing text at arbitrary
// locations on an unbounded area filled with spaces.
//
// THe memory usage is at most O((maxLineIdx-minLineIdx) * (maxColIdx-minColIdx)).
type Whiteboard struct {
	firstLineIdx int
	lines        []whiteboardLine
}

// whiteboardLine contains a single line of text, starting at an arbitrary column.
type whiteboardLine struct {
	firstColIdx int
	buf         []rune
}

// Write a string at the given line and column index. The indexes are arbitrary
// and can be negative.
func (wb *Whiteboard) Write(lineIdx, colIdx int, s string) {
	runes := []rune(s)
	l := wb.getLine(lineIdx)
	if l.buf == nil {
		l.firstColIdx = colIdx
		l.buf = runes
		return
	}

	if colIdx < l.firstColIdx {
		// Extend the buffer to the left.
		newLength := l.firstColIdx + len(l.buf) - colIdx
		// Make it at least 50% larger to amortize repeated extensions.
		if newLength < len(l.buf)*3/2 {
			newLength = len(l.buf) * 3 / 2
		}
		extra := newLength - len(l.buf)
		newBuf := make([]rune, extra, newLength)
		for i := range newBuf {
			newBuf[i] = ' '
		}
		newBuf = append(newBuf, l.buf...)
		l.buf = newBuf
		l.firstColIdx -= extra
	}

	for l.firstColIdx+len(l.buf) < colIdx+len(runes) {
		l.buf = append(l.buf, ' ')
	}

	// p is the position inside l.buf where we should start writing s.
	copy(l.buf[colIdx-l.firstColIdx:], runes)
}

func (wb *Whiteboard) getLine(lineIdx int) *whiteboardLine {
	switch {
	case wb.lines == nil:
		wb.lines = make([]whiteboardLine, 1)
		wb.firstLineIdx = lineIdx

	case lineIdx < wb.firstLineIdx:
		extra := wb.firstLineIdx - lineIdx
		wb.lines = append(make([]whiteboardLine, extra, extra+len(wb.lines)), wb.lines...)
		wb.firstLineIdx = lineIdx

	case lineIdx >= wb.firstLineIdx+len(wb.lines):
		extra := lineIdx - (wb.firstLineIdx + len(wb.lines)) + 1
		wb.lines = append(wb.lines, make([]whiteboardLine, extra)...)
	}
	return &wb.lines[lineIdx-wb.firstLineIdx]
}

// String turns the whiteboard into a multi-line string.
func (wb *Whiteboard) String() string {
	return wb.Indented(0)
}

// Indented turns the whiteboard into a multi-line string and prepends an
// indentation on each line.
func (wb *Whiteboard) Indented(indent int) string {
	if len(wb.lines) == 0 {
		return ""
	}
	firstCol := math.MaxInt
	for _, l := range wb.lines {
		if l.firstColIdx < firstCol {
			i := 0
			for ; i < len(l.buf) && l.buf[i] == ' '; i++ {
			}
			if l.firstColIdx+i < firstCol {
				firstCol = l.firstColIdx + i
			}
		}
	}
	var buf strings.Builder
	for _, l := range wb.lines {
		buf.WriteString(strings.Repeat(" ", indent))
		if l.firstColIdx > firstCol {
			buf.WriteString(strings.Repeat(" ", l.firstColIdx-firstCol))
			buf.WriteString(string(l.buf))
		} else {
			// We may need to skip over some spaces.
			buf.WriteString(string(l.buf[firstCol-l.firstColIdx:]))
		}
		buf.WriteString("\n")
	}
	return buf.String()
}
