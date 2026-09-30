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

// These helpers are used by TestTOutput. They are in a separate file so that
// the test fails if a message is attributed to the helper (instead of to its
// caller).

func tbHelperErrorf(tb testing.TB, format string, args ...any) {
	tb.Helper()
	tb.Errorf(format, args...)
}

func tHelperErrorf(t *T, format string, args ...any) {
	t.Helper()
	t.Errorf(format, args...)
}
