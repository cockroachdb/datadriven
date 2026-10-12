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

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

type namedInt int

func TestScanArg(t *testing.T) {
	RunTestFromString(t, `
scan n=5 names=(a, b) verbose d=1m nm=7 b=false
----
n=5 names=[]string{"a", "b"} verbose=true d=1m0s nm=7 b=false

scan-or n=3
----
n=3 m=10 verbose=false

maybe n=3
----
n=3 ok=true; m=0 ok=false
`, func(t *T, d *TestData) string {
		switch d.Cmd {
		case "scan":
			return fmt.Sprintf("n=%d names=%#v verbose=%t d=%s nm=%d b=%t",
				ScanArg[int](d, "n"), ScanArg[[]string](d, "names"), ScanArg[bool](d, "verbose"),
				ScanArg[time.Duration](d, "d"), ScanArg[namedInt](d, "nm"), ScanArg[bool](d, "b"))
		case "scan-or":
			return fmt.Sprintf("n=%d m=%d verbose=%t",
				ScanArgOr(d, "n", 1), ScanArgOr(d, "m", 10), ScanArgOr(d, "verbose", false))
		case "maybe":
			n, nOK := MaybeScanArg[int](d, "n")
			m, mOK := MaybeScanArg[int](d, "m")
			return fmt.Sprintf("n=%d ok=%t; m=%d ok=%t", n, nOK, m, mOK)
		}
		t.Fatalf("unknown command %s", d.Cmd)
		return ""
	})
}

func TestScanErrors(t *testing.T) {
	testCases := []struct {
		directive string
		f         func(d *TestData)
		expected  string
	}{
		{"cmd", func(d *TestData) { ScanArg[int](d, "n") }, "<string>:1: missing argument: n"},
		{
			"cmd n=x", func(d *TestData) { ScanArg[int](d, "n") },
			`<string>:1: argument "n": parse "x" as int: strconv.ParseInt: parsing "x": invalid syntax`,
		},
		{"cmd n", func(d *TestData) { ScanArg[int](d, "n") }, `<string>:1: argument "n": no value`},
		{
			"cmd n=(1,2)", func(d *TestData) { ScanArg[int](d, "n") },
			`<string>:1: argument "n": 2 values; scan into a slice, or pass 2 destinations`,
		},
		{
			"cmd n=(1,x)", func(d *TestData) { ScanArg[[]int](d, "n") },
			`<string>:1: argument "n": value 1: parse "x" as int: strconv.ParseInt: parsing "x": invalid syntax`,
		},
		{
			"cmd n=(1,x)", func(d *TestData) { var a, b int; d.ScanArgs("n", &a, &b) },
			`<string>:1: argument "n": value 1: parse "x" as int: strconv.ParseInt: parsing "x": invalid syntax`,
		},
		{
			"cmd n=(1,2)", func(d *TestData) { var a, b, c int; d.ScanArgs("n", &a, &b, &c) },
			`<string>:1: argument "n": 3 destinations, but 2 values`,
		},
		{
			"cmd n=1", func(d *TestData) { ScanArg[struct{}](d, "n") },
			`<string>:1: argument "n": unsupported destination type struct {}`,
		},
		{
			// A bool with a value must be a valid bool.
			"cmd b=yes", func(d *TestData) { ScanArg[bool](d, "b") },
			`<string>:1: argument "b": strconv.ParseBool: parsing "yes": invalid syntax`,
		},
		{
			"cmd n=x", func(d *TestData) { ScanArgOr(d, "n", 1) },
			`<string>:1: argument "n": parse "x" as int: strconv.ParseInt: parsing "x": invalid syntax`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.directive, func(t *testing.T) {
			rec := &recorderTB{TB: t}
			runAndWait(func() {
				runFromString(rec, tc.directive+"\n----\n", func(t *TBWrapper[*recorderTB], d *TestData) string {
					tc.f(d)
					return ""
				})
			})
			if expected := []string{tc.expected}; !reflect.DeepEqual(rec.msgs, expected) {
				t.Fatalf("expected:\n  %s\ngot:\n  %s", tc.expected, strings.Join(rec.msgs, "\n  "))
			}
		})
	}
}
