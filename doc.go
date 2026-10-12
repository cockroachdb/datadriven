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

// Package datadriven implements data-driven tests: an extension of table-driven
// tests where the test cases (and their expected results) are in separate text
// files, usually in a testdata directory.
//
// # File format
//
// A test file contains a sequence of directives, each with an optional input
// and the expected output:
//
//	# A comment.
//	<command> [<arg> | <arg>=<value> | <arg>=(<value>, <value>, ...)]...
//	<input>
//	----
//	<expected output>
//
// Lines starting with # (outside of inputs and outputs) are comments; blank
// lines between directives are ignored. A directive line can be continued on
// the next line by ending it with a backslash. See ParseLine for the syntax of
// the arguments.
//
// The input consists of the lines up to the ---- separator; leading and
// trailing blank lines (and whitespace) are trimmed. The expected output ends
// at the first blank line, so it can't contain blank lines. An expected output
// with blank lines can be specified using double separators:
//
//	<command>
//	<input>
//	----
//	----
//	<expected output>
//
//	<more expected output>
//	----
//	----
//
// # Running tests
//
// RunTest (or RunTestFromString) runs a function for each directive in a test
// file, passing it the directive as a TestData; Walk can be used to run a
// function (typically one that calls RunTest) for each test file in a
// directory. The function returns the actual output, which must match the
// expected output (after a trailing newline is added if it doesn't have one).
// The function can also fail the test, e.g. with t.Fatalf; the rest of the test
// file is then skipped. A typical test looks like:
//
//	func TestFoo(t *testing.T) {
//		datadriven.Walk(t, "testdata", func(t *testing.T, path string) {
//			datadriven.RunTest(t, path, func(t *datadriven.T, td *datadriven.TestData) string {
//				switch td.Cmd {
//				case "add":
//					a := datadriven.ScanArg[int](td, "a")
//					b := datadriven.ScanArgOr(td, "b", 1)
//					return fmt.Sprint(a + b)
//				default:
//					t.Fatalf("unknown command %q", td.Cmd)
//					return ""
//				}
//			})
//		})
//	}
//
// with a test file testdata/add:
//
//	add a=1 b=2
//	----
//	3
//
//	add a=5
//	----
//	6
//
// The function receives a wrapper of the testing.TB (*T for a *testing.T, *B
// for a *testing.B; see TBWrapper) which annotates messages with the position
// of the directive in the test file. The same wrapper is available as
// TestData.T and is used by the TestData methods that can fail the test (e.g.
// ScanArgs). The arguments can be retrieved by key (ScanArg, ScanArgOr,
// MaybeScanArg, ScanArgs, HasArg) or by position (PopArg).
//
// # Subtests
//
// A section of a test file can be run as a subtest (using testing.T.Run):
//
//	subtest <name>
//	...
//	subtest end
//
// Subtests can be nested; the name of a nested subtest must start with the
// name of its parent and a slash (e.g. "foo/bar"). The name can also be
// repeated after "subtest end". Subtests can't be skipped, and are not
// supported in benchmarks.
//
// # Flags
//
// The -rewrite flag (see Rewrite) causes the test files to be rewritten with
// the actual outputs instead of being checked against them; the changes
// should be carefully reviewed.
//
// With -test.v, each directive and its output are logged (except with
// -rewrite); the -datadriven-quiet flag (or the DATADRIVEN_QUIET_LOG=true
// environment variable) disables this (see Verbose). Regardless, directives
// that run for more than 10 seconds are logged periodically, to help debug
// test timeouts.
package datadriven
