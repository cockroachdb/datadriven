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
	"encoding"
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

// String renders the flag in the directive line syntax (see ParseLine); a
// single value is quoted if necessary.
func (f Flag) String() string {
	switch {
	case f.List:
		return "-" + f.Name + "=(" + strings.Join(f.Vals, ", ") + ")"
	case len(f.Vals) > 0:
		val := f.Vals[0]
		if val != "" && (!isPlainWord(val) || isListValue(val)) {
			val = quote(val)
		}
		return "-" + f.Name + "=" + val
	default:
		return "-" + f.Name
	}
}

// uniqueFlag returns the flag with the given name, if it is present. It is a
// fatal error if the flag is repeated.
func (td *TestData) uniqueFlag(name string) (Flag, bool) {
	hasName := func(f Flag) bool { return f.Name == name }
	i := slices.IndexFunc(td.Flags, hasName)
	if i == -1 {
		return Flag{}, false
	}
	if slices.ContainsFunc(td.Flags[i+1:], hasName) {
		t := td.tb()
		t.Helper()
		t.Fatalf("flag -%s is repeated (see FlagValues)", name)
	}
	return td.Flags[i], true
}

// HasFlag returns true if the flag is present (with or without a value, and
// possibly repeated), and marks it as processed.
func (td *TestData) HasFlag(name string) bool {
	td.MarkFlagProcessed(name)
	return slices.ContainsFunc(td.Flags, func(f Flag) bool { return f.Name == name })
}

// BoolFlag returns true if a flag without a value (e.g. -verbose) is present,
// and marks it as processed. It is a fatal error if the flag has a value, or if
// it is repeated.
func (td *TestData) BoolFlag(name string) bool {
	t := td.tb()
	t.Helper()
	td.MarkFlagProcessed(name)
	f, ok := td.uniqueFlag(name)
	if ok && f.hasValue() {
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
// V must be one of:
//   - string, bool, an integer or floating point type, or a named type with one
//     of these as the underlying type (e.g. type X int); integers are parsed in
//     base 10;
//   - time.Duration, which is parsed with time.ParseDuration (a named type
//     defined from it is parsed as an integer);
//   - a type that implements encoding.TextUnmarshaler, which is used to parse
//     the value (regardless of the underlying type): either a type T for which
//     *T implements it (e.g. netip.Addr, time.Time, or a type defined by the
//     test), or a pointer type that implements it (e.g. *big.Int);
//   - a slice of one of the above, including a named slice type (e.g.
//     type Elems []X).
//
// A list value (e.g. -names=(a, b)) can only be retrieved as a slice; a single
// value can be retrieved either as a scalar or as a slice with one element. A
// single value with whitespace must be quoted (e.g. -name='a b'; see
// ParseLine): -name=(a b) is a list.
//
// It is a fatal error if V is not one of these types, if the flag doesn't
// exist, if it doesn't have a value (see BoolFlag), if the value can't be
// parsed as a V, or if the flag is repeated (see FlagValues).
func (td *TestData) FlagValue[V any](name string) V {
	t := td.tb()
	t.Helper()
	td.MarkFlagProcessed(name)
	td.checkFlagType(name, reflect.TypeFor[V]())
	f, ok := td.uniqueFlag(name)
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
	td.checkFlagType(name, reflect.TypeFor[V]())
	f, ok := td.uniqueFlag(name)
	if !ok {
		return def
	}
	v, err := flagValue[V](f)
	if err != nil {
		t.Fatalf("flag -%s: %v", name, err)
	}
	return v
}

// FlagValues returns the values of a flag which can be repeated, in order, each
// parsed as a V (see FlagValue), and marks the flag as processed. It returns
// nil if the flag doesn't exist. For example, for
//
//	cmd -probe=(a, 1) -probe=(b, 2) -level=3
//
// the following would be valid:
//
//	probes := td.FlagValues[[]string]("probe") // [[a 1] [b 2]]
//	levels := td.FlagValues[int]("level") // [3]
//
// A repeated flag can only be retrieved with FlagValues: BoolFlag, FlagValue
// and FlagValueOr fail instead of using one of the occurrences.
//
// It is a fatal error if V is not a type supported by FlagValue, or if an
// occurrence of the flag doesn't have a value or its value can't be parsed as a
// V.
func (td *TestData) FlagValues[V any](name string) []V {
	t := td.tb()
	t.Helper()
	td.MarkFlagProcessed(name)
	td.checkFlagType(name, reflect.TypeFor[V]())
	var vals []V
	for _, f := range td.Flags {
		if f.Name != name {
			continue
		}
		v, err := flagValue[V](f)
		if err != nil {
			t.Fatalf("flag %s: %v", f, err)
		}
		vals = append(vals, v)
	}
	return vals
}

// checkFlagType fails the test if typ is not a type supported by FlagValue.
// This is checked regardless of the flag (which can be missing, or an empty
// list), so that the mistake is not found only when a test file uses the flag.
func (td *TestData) checkFlagType(name string, typ reflect.Type) {
	if !isValueType(typ) && !(isSliceType(typ) && isValueType(typ.Elem())) {
		t := td.tb()
		t.Helper()
		t.Fatalf("flag -%s: unsupported type %s (see FlagValue)", name, typ)
	}
}

// flagValue returns the value of the flag, parsed as a V (which must be a
// supported type).
func flagValue[V any](f Flag) (V, error) {
	var v V
	if !f.hasValue() {
		return v, fmt.Errorf("no value (see BoolFlag)")
	}
	rv := reflect.ValueOf(&v).Elem()
	if isSliceType(rv.Type()) {
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
		return v, fmt.Errorf("the value is a list; use a slice type (or quote the value)")
	}
	return v, parseValue(f.Vals[0], rv)
}

var (
	durationType        = reflect.TypeFor[time.Duration]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// unmarshalsText returns true if a value of type t is parsed using
// encoding.TextUnmarshaler: either *t implements it, or t is a pointer type
// that implements it (e.g. *big.Int).
func unmarshalsText(t reflect.Type) bool {
	return reflect.PointerTo(t).Implements(textUnmarshalerType) ||
		(t.Kind() == reflect.Pointer && t.Implements(textUnmarshalerType))
}

// isSliceType returns true if a value of type t is parsed as a slice of values
// (as opposed to a single value). A slice type which implements
// encoding.TextUnmarshaler (e.g. net.IP) is a single value.
func isSliceType(t reflect.Type) bool {
	return t.Kind() == reflect.Slice && !unmarshalsText(t)
}

// isValueType returns true if parseValue supports values of type t.
func isValueType(t reflect.Type) bool {
	if unmarshalsText(t) || t == durationType {
		return true
	}
	switch t.Kind() {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

// parseValue parses s as a single value and stores it into dest, which must be
// addressable and of a supported type (see isValueType).
func parseValue(s string, dest reflect.Value) error {
	t := dest.Type()
	switch {
	case reflect.PointerTo(t).Implements(textUnmarshalerType):
		u := dest.Addr().Interface().(encoding.TextUnmarshaler)
		if err := u.UnmarshalText([]byte(s)); err != nil {
			return fmt.Errorf("parse %q as %s: %w", s, t, err)
		}
		return nil

	case t.Kind() == reflect.Pointer && t.Implements(textUnmarshalerType):
		p := reflect.New(t.Elem())
		if err := p.Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(s)); err != nil {
			return fmt.Errorf("parse %q as %s: %w", s, t, err)
		}
		dest.Set(p)
		return nil

	case t == durationType:
		d, err := time.ParseDuration(strings.TrimSpace(s))
		if err != nil {
			return err
		}
		dest.SetInt(int64(d))
		return nil

	default:
		return parseArgVal(s, dest)
	}
}

// MarkFlagProcessed marks a flag as processed. It is used when the flags are
// processed without looking them up by name (e.g. when iterating over Flags).
//
// All the flags on the directive line (regardless of any changes to Flags) must
// be processed by the time the test function returns; otherwise, the directive
// fails. The same goes for the flags of a directive returned by
// InputDirectives, by the end of the iteration. Flags that are looked up by
// name (e.g. with HasFlag, FlagValue) are marked as processed automatically.
func (td *TestData) MarkFlagProcessed(name string) {
	// The processed flags are only tracked (and checked) for the TestData passed
	// to the test function and those returned by InputDirectives (not for a
	// TestData constructed manually).
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

// SingleArg returns the positional argument of a directive which takes exactly
// one. It is a fatal error if Args doesn't have exactly one argument.
func (td *TestData) SingleArg() string {
	td.tb().Helper()
	td.checkNumArgs("SingleArg", 1)
	return td.Args[0]
}

// TwoArgs returns the positional arguments of a directive which takes exactly
// two. It is a fatal error if Args doesn't have exactly two arguments.
func (td *TestData) TwoArgs() (string, string) {
	td.tb().Helper()
	td.checkNumArgs("TwoArgs", 2)
	return td.Args[0], td.Args[1]
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
// The values must be non-nil pointers to the (non-slice) types supported by
// FlagValue, and are parsed in the same way: e.g. integers are parsed in base
// 10 (a leading 0 does not indicate an octal number), a time.Duration is parsed
// with time.ParseDuration, and a type that implements encoding.TextUnmarshaler
// is parsed using it.
//
// It is a fatal error if a value is not such a pointer, if there are not enough
// arguments, or if an argument can't be parsed (in its entirety; e.g. an int
// with an argument "12abc").
func (td *TestData) PopArgs(values ...any) {
	td.tb().Helper()
	td.scanEachArg("PopArgs", values)
	td.Args = td.Args[len(values):]
}

// ScanArgs is like PopArgs, except that it is a fatal error if there are more
// arguments than values. Args is not modified. See SingleArg and TwoArgs for
// directives which take one or two arguments that don't need to be parsed.
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
	td.checkEnoughArgs(desc, len(values))
	for i, v := range values {
		if err := parseValue(td.Args[i], reflect.ValueOf(v).Elem()); err != nil {
			t.Fatalf("%s: argument %q: %v (directive: %s)", desc, td.Args[i], err, td.directiveLine())
		}
	}
}

// checkPointers fails the test if any of the values is not a non-nil pointer to
// a supported type.
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
		rv := reflect.ValueOf(v)
		if rv.Kind() != reflect.Pointer || rv.IsNil() {
			t.Fatalf("%s: value %d must be a non-nil pointer, got %T", desc, i, v)
		}
		if typ := rv.Type().Elem(); !isValueType(typ) {
			t.Fatalf("%s: value %d: unsupported type %s (see FlagValue)", desc, i, typ)
		}
	}
}

// checkEnoughArgs fails the test if there are fewer than n arguments.
func (td *TestData) checkEnoughArgs(desc string, n int) {
	if n > len(td.Args) {
		t := td.tb()
		t.Helper()
		t.Fatalf("%s: not enough arguments (directive: %s)", desc, td.directiveLine())
	}
}

// checkNumArgs fails the test if the number of arguments is not n.
func (td *TestData) checkNumArgs(desc string, n int) {
	td.tb().Helper()
	td.checkEnoughArgs(desc, n)
	td.checkNoArgsLeft(desc, n)
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
// reflect any changes to Flags and Args, e.g. by PopArg) in the directive line
// syntax (see ParseLine). The flags are rendered before the arguments, and
// values and arguments are quoted if necessary.
func (td *TestData) FullCmd() string {
	fields := make([]string, 0, 2+len(td.Flags)+len(td.Args))
	fields = append(fields, td.Cmd)
	for _, f := range td.Flags {
		fields = append(fields, f.String())
	}
	if slices.ContainsFunc(td.Args, func(a string) bool { return a == "--" || isFlag(a) }) {
		fields = append(fields, "--")
	}
	for _, a := range td.Args {
		if !isPlainWord(a) {
			a = quote(a)
		}
		fields = append(fields, a)
	}
	return strings.Join(fields, " ")
}
