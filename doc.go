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
//	<command> [-<flag> | -<flag>=<value> | -<flag>=(<value>, ...) | <arg>]...
//	<input>
//	----
//	<expected output>
//
// The directive line contains a command, followed by flags and positional
// arguments, similar to a command line: the words that start with - (or --)
// followed by a letter are flags, and the other words (as well as all the words
// after a -- word) are positional arguments. A flag can have no value, a single
// value, or a list of values, and it can't be repeated. See ParseLine for the
// details.
//
// Lines starting with # (outside of inputs and outputs) are comments; blank
// lines between directives are ignored. A directive line can be continued on
// the next line by ending it with a backslash.
//
// The input consists of the lines up to the ---- separator; leading and
// trailing blank lines (and whitespace) are trimmed. The expected output ends
// at the first blank line, so it can't contain blank lines. An expected output
// with blank lines (or which starts with a ---- line) can be specified using
// double separators:
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
// Such an output ends at the first two consecutive ---- lines, so it can't
// contain them (or end with a ---- line). The lines of a test file can end
// with LF or CRLF, so a line of an expected output can't end with a carriage
// return.
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
//					a := td.FlagValue[int]("a")
//					b := td.FlagValueOr("b", 1)
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
//	add -a=1 -b=2
//	----
//	3
//
//	add -a=5
//	----
//	6
//
// The function receives a wrapper of the testing.TB (*T for a *testing.T, *B
// for a *testing.B; see TBWrapper) which annotates messages with the position
// of the directive in the test file. The same wrapper is available as
// TestData.T and is used by the TestData methods that can fail the test (e.g.
// FlagValue).
//
// The flags are retrieved by name: HasFlag checks whether a flag is present,
// BoolFlag checks for a flag that must not have a value (e.g. -verbose), and
// FlagValue and FlagValueOr parse the value of a flag into a given type (a list
// value, e.g. -names=(a, b), is retrieved as a slice; a type defined by the
// test can implement encoding.TextUnmarshaler). The positional arguments
// are in TestData.Args (see also PopArg, PopArgs, ScanArgs). All the flags
// must be processed by the test (so that e.g. a misspelled flag doesn't go
// unnoticed): the flags that are retrieved by name are processed automatically,
// and the others (see TestData.Flags) can be marked with MarkFlagProcessed.
// Otherwise, the directive fails.
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
// repeated after "subtest end". The -run flag can be used to select subtests
// (e.g. -run 'TestFoo/foo/bar'); the directives of the other subtests are
// not run, but those outside of subtests are. A subtest can't be skipped with
// t.Skip, and subtests are not supported in benchmarks.
//
// # Flags
//
// The -rewrite flag (see Rewrite) causes the test files to be rewritten with
// the actual outputs instead of being checked against them; the changes
// should be carefully reviewed.
//
// With -test.v, each directive and its output are logged (except when the
// test file is being rewritten); the -datadriven-quiet flag (or the
// DATADRIVEN_QUIET_LOG=true environment variable) disables this (see Verbose).
// Regardless, directives that run for more than 10 seconds are logged
// periodically, to help debug test timeouts.
package datadriven
