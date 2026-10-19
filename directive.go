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
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"
)

// Flag is a flag on a directive line (see ParseLine): -name, -name=value or
// -name=(value, ...).
//
// The methods which take a testing.TB report errors through it; pass TestData.T
// so that the errors are annotated with the directive position.
type Flag struct {
	// Name is the name of the flag (without the dashes).
	Name string
	// Vals contains the values of the flag: none for -name, one for -name=value
	// (an empty value for -name=), and the list of values for -name=(v1, v2).
	Vals []string
}

// String renders the flag in the directive line syntax.
func (f Flag) String() string {
	switch len(f.Vals) {
	case 0:
		return "-" + f.Name
	case 1:
		v := f.Vals[0]
		if (strings.HasPrefix(v, "(") && matchingParen(v) == len(v)-1) || hasTopLevelSpace(v) {
			// Without the parentheses, the value would be parsed as a list or as
			// multiple words.
			v = "(" + v + ")"
		}
		return "-" + f.Name + "=" + v
	default:
		return "-" + f.Name + "=(" + strings.Join(f.Vals, ", ") + ")"
	}
}

// hasTopLevelSpace returns true if s contains whitespace outside of
// parentheses.
func hasTopLevelSpace(s string) bool {
	depth := 0
	for _, r := range s {
		switch {
		case r == '(':
			depth++
		case r == ')':
			depth--
		case depth == 0 && unicode.IsSpace(r):
			return true
		}
	}
	return false
}

// FirstVal asserts that the flag has at least one value and returns the first
// one.
func (f Flag) FirstVal(t testing.TB) string {
	t.Helper()
	if len(f.Vals) == 0 {
		t.Fatalf("flag -%s requires at least 1 value, has 0", f.Name)
	}
	return f.Vals[0]
}

// SingleVal asserts that the flag has exactly one value and returns it.
func (f Flag) SingleVal(t testing.TB) string {
	t.Helper()
	f.expectNumVals(t, 1)
	return f.Vals[0]
}

// TwoVals asserts that the flag has exactly two values and returns them.
func (f Flag) TwoVals(t testing.TB) (string, string) {
	t.Helper()
	f.expectNumVals(t, 2)
	return f.Vals[0], f.Vals[1]
}

func (f Flag) expectNumVals(t testing.TB, num int) {
	t.Helper()
	if len(f.Vals) != num {
		t.Fatalf("flag -%s requires %d values, has %d", f.Name, num, len(f.Vals))
	}
}

// Scan parses the value at index i into dest.
func (f Flag) Scan(t testing.TB, i int, dest any) {
	t.Helper()
	if err := f.scanScalarErr(i, dest); err != nil {
		t.Fatalf("flag -%s: value %d: %v", f.Name, i, err)
	}
}

// HasFlag returns true if the flag is present, and marks it as processed.
func (td *TestData) HasFlag(name string) bool {
	td.MarkFlagProcessed(name)
	return slices.ContainsFunc(td.Flags, func(f Flag) bool { return f.Name == name })
}

// Flag returns the flag with the given name, and marks it as processed. The
// second return value indicates whether the flag exists. It is a fatal error if
// the flag is repeated (see FlagValues).
func (td *TestData) Flag(name string) (Flag, bool) {
	td.MarkFlagProcessed(name)
	var res Flag
	found := false
	for _, f := range td.Flags {
		if f.Name == name {
			if found {
				t := td.tb()
				t.Helper()
				t.Fatalf("flag -%s is repeated (see FlagValues)", name)
			}
			res, found = f, true
		}
	}
	return res, found
}

// FlagValues returns the values of all the occurrences of a flag, in order, and
// marks the flag as processed. It is used for flags that can be repeated, e.g.
// "-x=1 -x=2". See also ScanFlagValues.
func (td *TestData) FlagValues(name string) [][]string {
	td.MarkFlagProcessed(name)
	var res [][]string
	for _, f := range td.Flags {
		if f.Name == name {
			res = append(res, f.Vals)
		}
	}
	return res
}

// MarkFlagProcessed marks a flag as processed. It is used when the flags are
// processed without looking them up by name (e.g. when iterating over Flags).
//
// All the flags on the directive line (regardless of any changes to Flags) must
// be processed by the time the test function returns; otherwise, the directive
// fails. Flags that are looked up by name (e.g. with HasFlag, ScanFlag) are
// marked as processed automatically.
func (td *TestData) MarkFlagProcessed(name string) {
	if td.processed == nil {
		// This is a TestData constructed manually.
		td.processed = &processedFlags{}
	}
	td.processed.add(name)
}

// MarkAllFlagsProcessed marks all the flags as processed (see
// MarkFlagProcessed).
func (td *TestData) MarkAllFlagsProcessed() {
	for _, f := range td.Flags {
		td.MarkFlagProcessed(f.Name)
	}
}

// processedFlags is the set of the names of the flags that were processed. It
// is shared by all the copies of a TestData, and it is safe for concurrent use.
type processedFlags struct {
	mu    sync.Mutex
	names map[string]struct{}
}

func (p *processedFlags) add(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.names == nil {
		p.names = make(map[string]struct{})
	}
	p.names[name] = struct{}{}
}

// unprocessed returns the flags (rendered as strings) that were not processed.
func (p *processedFlags) unprocessed(flags []Flag) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var res []string
	for _, f := range flags {
		if _, ok := p.names[f.Name]; !ok {
			res = append(res, f.String())
		}
	}
	return res
}

// MaybeScanFlagVals is like ScanFlagVals, except that if the flag does not
// exist it leaves the destinations unmodified and returns false.
func (td *TestData) MaybeScanFlagVals(name string, dests ...any) bool {
	t := td.tb()
	t.Helper()
	f, ok := td.Flag(name)
	if ok {
		f.scan(t, dests...)
	}
	return ok
}

// ScanFlagVals scans the values of a flag into the given destinations, and
// marks the flag as processed. It is a fatal error if the flag does not exist
// or is repeated, if the number of destinations does not match that of the
// values, or if a value can't be parsed. For example, for
//
//	cmd -f1=50 -f2=yoruba -f3=(50, 50, 50) -f4
//
// the following would be valid:
//
//	var i1, i2, i3, i4 int
//	var s string
//	var l []int
//	var b bool
//	td.ScanFlagVals("f1", &i1)
//	td.ScanFlagVals("f2", &s)
//	td.ScanFlagVals("f3", &i2, &i3, &i4)
//	td.ScanFlagVals("f3", &l)
//	td.ScanFlagVals("f4", &b) // true
//
// The destinations must be pointers to one of: string, bool, integer and
// floating point types, named types with one of these as the underlying type,
// time.Duration (but not named types defined from it, which are parsed as
// integers), or slices of these (which receive all the values). Integers are
// parsed in base 10. A flag without values (e.g. -f4 above) can be scanned
// into a bool, as true.
//
// See also ScanFlag, ScanFlagOr and MaybeScanFlag.
func (td *TestData) ScanFlagVals(name string, dests ...any) {
	t := td.tb()
	t.Helper()
	f, ok := td.Flag(name)
	if !ok {
		t.Fatalf("missing flag: -%s", name)
	}
	f.scan(t, dests...)
}

// ScanFlag returns the value of a flag scanned into a V (which can be any type
// supported by ScanFlagVals, including slices), and marks the flag as
// processed. It is a fatal error if the flag doesn't exist or is repeated, or
// if it can't be scanned. For example, for
//
//	cmd -n=5 -names=(a, b) -verbose
//
// the following would be valid:
//
//	n := datadriven.ScanFlag[int](td, "n")
//	names := datadriven.ScanFlag[[]string](td, "names")
//	verbose := datadriven.ScanFlag[bool](td, "verbose") // true
//
// (ScanFlag is a function because Go methods can't have type parameters.)
func ScanFlag[V any](td *TestData, name string) V {
	td.tb().Helper()
	var v V
	td.ScanFlagVals(name, &v)
	return v
}

// ScanFlagOr is like ScanFlag, but returns def if the flag doesn't exist.
func ScanFlagOr[V any](td *TestData, name string, def V) V {
	td.tb().Helper()
	if v, ok := MaybeScanFlag[V](td, name); ok {
		return v
	}
	return def
}

// MaybeScanFlag is like ScanFlag, but returns false (and the zero value) if the
// flag doesn't exist.
func MaybeScanFlag[V any](td *TestData, name string) (V, bool) {
	td.tb().Helper()
	var v V
	ok := td.MaybeScanFlagVals(name, &v)
	return v, ok
}

// ScanFlagValues is like ScanFlag, but returns the scanned values of all the
// occurrences of a flag, in order (see FlagValues). It returns nil if the flag
// doesn't exist.
func ScanFlagValues[V any](td *TestData, name string) []V {
	t := td.tb()
	t.Helper()
	td.MarkFlagProcessed(name)
	var res []V
	for _, f := range td.Flags {
		if f.Name == name {
			var v V
			f.scan(t, &v)
			res = append(res, v)
		}
	}
	return res
}

// PopArg removes the first positional argument from Args and returns it. It is
// a fatal error if there are no arguments left.
func (td *TestData) PopArg() string {
	t := td.tb()
	t.Helper()
	if len(td.Args) == 0 {
		t.Fatalf("PopArg: no arguments left (directive: %s)", td.directiveLine())
	}
	arg := td.Args[0]
	td.Args = td.Args[1:]
	return arg
}

// FullCmd renders the command, the flags and the positional arguments (which
// reflect any changes to Flags and Args, e.g. by PopArg). The flags are
// rendered before the arguments.
func (td *TestData) FullCmd() string {
	fields := make([]string, 0, 2+len(td.Flags)+len(td.Args))
	fields = append(fields, td.Cmd)
	for _, f := range td.Flags {
		fields = append(fields, f.String())
	}
	if slices.ContainsFunc(td.Args, func(a string) bool { return a == "--" || isFlag(a) }) {
		fields = append(fields, "--")
	}
	return strings.Join(append(fields, td.Args...), " ")
}

func (f Flag) scan(t testing.TB, dests ...any) {
	t.Helper()
	// If only one destination is provided, use scanAllErr which supports
	// scanning multiple values into a slice destination type.
	if len(dests) == 1 {
		if err := f.scanAllErr(dests[0]); err != nil {
			t.Fatalf("flag -%s: %v", f.Name, err)
		}
		return
	}

	// Multiple destinations provided; update each corresponding destination to
	// support invocations of the form:
	//
	//   td.ScanFlagVals("f3", &i2, &i3, &i4)
	//
	if len(dests) != len(f.Vals) {
		t.Fatalf("flag -%s: %d destinations, but %d values", f.Name, len(dests), len(f.Vals))
	}

	for i := range dests {
		if err := f.scanScalarErr(i, dests[i]); err != nil {
			t.Fatalf("flag -%s: value %d: %v", f.Name, i, err)
		}
	}
}

// scanAllErr scans the flag into dest, which is a pointer to either a slice
// (which receives all the values) or a scalar (which receives the single
// value; a bool also accepts no values, as true).
func (f Flag) scanAllErr(dest any) error {
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return fmt.Errorf("destination must be a non-nil pointer, got %T", dest)
	}

	if sliceV := rv.Elem(); sliceV.Kind() == reflect.Slice {
		slice := reflect.MakeSlice(sliceV.Type(), len(f.Vals), len(f.Vals))
		for i := range f.Vals {
			if err := f.scanScalarErr(i, slice.Index(i).Addr().Interface()); err != nil {
				return fmt.Errorf("value %d: %w", i, err)
			}
		}
		sliceV.Set(slice)
		return nil
	}

	switch len(f.Vals) {
	case 0:
		if rv.Elem().Kind() == reflect.Bool {
			// A flag without values (e.g. "cmd -verbose") is a true bool.
			rv.Elem().SetBool(true)
			return nil
		}
		return fmt.Errorf("no value")
	case 1:
		return f.scanScalarErr(0, dest)
	default:
		return fmt.Errorf("%d values; scan into a slice, or pass %d destinations", len(f.Vals), len(f.Vals))
	}
}

// scanScalarErr is like Scan but returns an error rather than taking a testing.T to fatal.
func (f Flag) scanScalarErr(i int, dest any) error {
	if i < 0 || i >= len(f.Vals) {
		return fmt.Errorf("index out of range (%d values)", len(f.Vals))
	}
	val := f.Vals[i]

	// Special cases.
	switch dest := dest.(type) {
	case *time.Duration:
		t, err := time.ParseDuration(val)
		if err != nil {
			return err
		}
		*dest = t
		return nil
	}

	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return fmt.Errorf("out must be a non-nil pointer")
	}
	return parseArgVal(val, rv.Elem())
}
