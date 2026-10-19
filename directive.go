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
)

// Flag is a flag on a directive line (see ParseLine).
type Flag struct {
	// Name is the name of the flag (without the dashes).
	Name string
	// Vals contains the value of the flag: nil for -name, the single value for
	// -name=value (an empty string for -name=), or the values for
	// -name=(v1, v2, ...) (if List is set).
	Vals []string
	// List is set if the value is a list (possibly empty or with one value).
	List bool
}

// hasValue returns true if the flag has a value (possibly an empty list).
func (f Flag) hasValue() bool {
	return len(f.Vals) > 0 || f.List
}

// String renders the flag in the directive line syntax.
func (f Flag) String() string {
	switch {
	case f.List:
		return "-" + f.Name + "=(" + strings.Join(f.Vals, ", ") + ")"
	case len(f.Vals) > 0:
		return "-" + f.Name + "=" + f.Vals[0]
	default:
		return "-" + f.Name
	}
}

func (td *TestData) lookupFlag(name string) (Flag, bool) {
	for _, f := range td.Flags {
		if f.Name == name {
			return f, true
		}
	}
	return Flag{}, false
}

// HasFlag returns true if the flag is present (with or without a value), and
// marks it as processed.
func (td *TestData) HasFlag(name string) bool {
	td.MarkFlagProcessed(name)
	_, ok := td.lookupFlag(name)
	return ok
}

// BoolFlag returns true if a flag without a value (e.g. -verbose) is present,
// and marks it as processed. It is a fatal error if the flag has a value.
func (td *TestData) BoolFlag(name string) bool {
	td.MarkFlagProcessed(name)
	f, ok := td.lookupFlag(name)
	if ok && f.hasValue() {
		t := td.tb()
		t.Helper()
		t.Fatalf("flag -%s must not have a value: %s", name, f)
	}
	return ok
}

// FlagValue returns the value of a flag, parsed as a V, and marks the flag as
// processed. For example, for
//
//	cmd -n=5 -names=(a, b) -name=c -d=1s
//
// the following would be valid:
//
//	n := td.FlagValue[int]("n")
//	names := td.FlagValue[[]string]("names") // [a b]
//	name := td.FlagValue[[]string]("name") // [c]
//	d := td.FlagValue[time.Duration]("d")
//
// V must be one of: string, bool, integer and floating point types, named types
// with one of these as the underlying type (e.g. type X int), time.Duration
// (but not named types defined from it, which are parsed as integers), or
// slices of these, including named slice types (e.g. type Elems []X). A list
// value (e.g. -names=(a, b)) can only be retrieved as a slice; a single value
// can be retrieved either as a scalar or as a slice with one element. Integers
// are parsed in base 10.
//
// It is a fatal error if the flag doesn't exist, if it doesn't have a value
// (see BoolFlag), or if the value can't be parsed as a V.
func (td *TestData) FlagValue[V any](name string) V {
	t := td.tb()
	t.Helper()
	td.MarkFlagProcessed(name)
	f, ok := td.lookupFlag(name)
	if !ok {
		t.Fatalf("missing flag: -%s", name)
	}
	v, err := flagValue[V](f)
	if err != nil {
		t.Fatalf("flag -%s: %v", name, err)
	}
	return v
}

// FlagValueOr is like FlagValue, but returns def if the flag doesn't exist.
func (td *TestData) FlagValueOr[V any](name string, def V) V {
	t := td.tb()
	t.Helper()
	td.MarkFlagProcessed(name)
	f, ok := td.lookupFlag(name)
	if !ok {
		return def
	}
	v, err := flagValue[V](f)
	if err != nil {
		t.Fatalf("flag -%s: %v", name, err)
	}
	return v
}

// flagValue returns the value of the flag, parsed as a V.
func flagValue[V any](f Flag) (V, error) {
	var v V
	if !f.hasValue() {
		return v, fmt.Errorf("no value (see BoolFlag)")
	}
	rv := reflect.ValueOf(&v).Elem()
	if rv.Kind() == reflect.Slice {
		slice := reflect.MakeSlice(rv.Type(), len(f.Vals), len(f.Vals))
		for i, s := range f.Vals {
			if err := parseValue(s, slice.Index(i)); err != nil {
				if f.List {
					return v, fmt.Errorf("value %d: %w", i, err)
				}
				return v, err
			}
		}
		rv.Set(slice)
		return v, nil
	}
	if f.List {
		return v, fmt.Errorf("the value is a list; use a slice type")
	}
	return v, parseValue(f.Vals[0], rv)
}

// parseValue parses s and stores it into dest.
func parseValue(s string, dest reflect.Value) error {
	if dest.Type() == reflect.TypeOf(time.Duration(0)) {
		d, err := time.ParseDuration(strings.TrimSpace(s))
		if err != nil {
			return err
		}
		dest.SetInt(int64(d))
		return nil
	}
	return parseArgVal(s, dest)
}

// MarkFlagProcessed marks a flag as processed. It is used when the flags are
// processed without looking them up by name (e.g. when iterating over Flags).
//
// All the flags on the directive line (regardless of any changes to Flags) must
// be processed by the time the test function returns; otherwise, the directive
// fails. Flags that are looked up by name (e.g. with HasFlag, FlagValue) are
// marked as processed automatically.
func (td *TestData) MarkFlagProcessed(name string) {
	// The processed flags are only tracked (and checked) for the TestData passed
	// to the test function (not for a TestData constructed manually).
	if td.processed != nil {
		td.processed.add(name)
	}
}

// MarkAllFlagsProcessed marks all the flags as processed (see
// MarkFlagProcessed).
func (td *TestData) MarkAllFlagsProcessed() {
	for _, f := range td.Flags {
		td.MarkFlagProcessed(f.Name)
	}
}

// processedFlags is the set of the names of the flags that were processed. It
// is shared by all the copies of the TestData passed to the test function, and
// it is safe for concurrent use.
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

// PopArgs parses the first len(values) positional arguments into the given
// values, one argument per value; it then removes these arguments from Args.
// For example, for
//
//	cmd 12 foo bar
//
// the following would be valid:
//
//	var i int
//	var s string
//	td.PopArgs(&i, &s) // td.Args is now ["bar"]
//
// The values must be non-nil pointers. Values of the (non-slice) types
// supported by FlagValue are parsed in the same way: e.g. integers are parsed
// in base 10 (a leading 0 does not indicate an octal number) and a
// time.Duration is parsed with time.ParseDuration. Values of other types (e.g.
// implementations of fmt.Scanner) are scanned like fmt.Sscan does.
//
// It is a fatal error if there are not enough arguments, or if an argument
// can't be parsed in its entirety (e.g. an int with an argument "12abc").
func (td *TestData) PopArgs(values ...any) {
	td.tb().Helper()
	td.scanEachArg("PopArgs", values)
	td.Args = td.Args[len(values):]
}

// ScanArgs is like PopArgs, except that it is a fatal error if there are more
// arguments than values. Args is not modified.
func (td *TestData) ScanArgs(values ...any) {
	td.tb().Helper()
	td.scanEachArg("ScanArgs", values)
	td.checkNoArgsLeft("ScanArgs", len(values))
}

// scanEachArg implements PopArgs and ScanArgs: it parses each of the first
// len(values) arguments into the corresponding value.
func (td *TestData) scanEachArg(desc string, values []any) {
	t := td.tb()
	t.Helper()
	td.checkPointers(desc, values)
	if len(values) > len(td.Args) {
		t.Fatalf("%s: not enough arguments (directive: %s)", desc, td.directiveLine())
	}
	for i, v := range values {
		if err := parseArg(td.Args[i], v); err != nil {
			t.Fatalf("%s: argument %q: %v (directive: %s)", desc, td.Args[i], err, td.directiveLine())
		}
	}
}

// parseArg parses a positional argument into the value that v (a non-nil
// pointer) points to. The types supported by FlagValue are parsed in the same
// way; other types are scanned with fmt.Fscan, which must consume the entire
// argument.
func parseArg(arg string, v any) error {
	if _, ok := v.(fmt.Scanner); !ok {
		if dest := reflect.ValueOf(v).Elem(); isParseableKind(dest.Kind()) {
			return parseValue(arg, dest)
		}
	}
	r := strings.NewReader(arg)
	if _, err := fmt.Fscan(r, v); err != nil {
		return err
	}
	if r.Len() > 0 {
		return fmt.Errorf("unexpected %q after the scanned value", arg[len(arg)-r.Len():])
	}
	return nil
}

// checkPointers fails the test if any of the values is not a non-nil pointer.
func (td *TestData) checkPointers(desc string, values []any) {
	t := td.tb()
	t.Helper()
	if len(values) > 0 && desc == "ScanArgs" {
		if _, ok := values[0].(testing.TB); ok {
			// This is v1 code: ScanArgs(t, key, dests...).
			t.Fatalf("ScanArgs: value 0 is a %T; ScanArgs scans the positional arguments "+
				"(use FlagValue to retrieve the value of a flag)", values[0])
		}
	}
	for i, v := range values {
		if rv := reflect.ValueOf(v); rv.Kind() != reflect.Pointer || rv.IsNil() {
			t.Fatalf("%s: value %d must be a non-nil pointer, got %T", desc, i, v)
		}
	}
}

// checkNoArgsLeft fails the test if there are arguments after the first n.
func (td *TestData) checkNoArgsLeft(desc string, n int) {
	if n < len(td.Args) {
		t := td.tb()
		t.Helper()
		t.Fatalf("%s: unexpected arguments: %s (directive: %s)",
			desc, strings.Join(td.Args[n:], " "), td.directiveLine())
	}
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
