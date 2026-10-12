// Copyright 2026 The Cockroach Authors.
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

import "testing"

func TestPrefix(t *testing.T) {
	testCases := []struct {
		pos, msg, expected string
	}{
		{"", "x", "x"},
		{"f:1", "x", "f:1: x"},
		{"f:1", "f:1: x", "f:1: x"},
		{"f:1", "f:1", "f:1"},
		{"f:1", "f:1\nx", "f:1\nx"},
		{"f:1", "  f:1: x", "  f:1: x"},
		{"f:1", "\nf:1: (+2) x", "\nf:1: (+2) x"},
		// Only an exact position match counts.
		{"f:1", "f:10: x", "f:1: f:10: x"},
		{"f:1", "f:1x", "f:1: f:1x"},
		{"f:1", "x f:1: y", "f:1: x f:1: y"},
	}
	for _, tc := range testCases {
		var p directivePos
		p.set(tc.pos)
		if res := p.prefix(tc.msg); res != tc.expected {
			t.Errorf("prefix(%q) with position %q: expected %q, got %q", tc.msg, tc.pos, tc.expected, res)
		}
	}
	// A nil directivePos has no position.
	if res := (*directivePos)(nil).prefix("x"); res != "x" {
		t.Errorf("unexpected: %q", res)
	}
}
