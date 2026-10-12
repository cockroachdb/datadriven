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

// ScanArg returns the value of the first argument with the given key, scanned
// into a V (which can be any type supported by ScanArgs, including slices). It
// is a fatal error if the argument doesn't exist or can't be scanned. For
// example, for
//
//	cmd n=5 names=(a, b) verbose
//
// the following would be valid:
//
//	n := datadriven.ScanArg[int](td, "n")
//	names := datadriven.ScanArg[[]string](td, "names")
//	verbose := datadriven.ScanArg[bool](td, "verbose") // true
//
// (ScanArg is a function because Go methods can't have type parameters.)
func ScanArg[V any](td *TestData, key string) V {
	td.tb().Helper()
	var v V
	td.ScanArgs(key, &v)
	return v
}

// ScanArgOr is like ScanArg, but returns def if the argument doesn't exist.
func ScanArgOr[V any](td *TestData, key string, def V) V {
	td.tb().Helper()
	if v, ok := MaybeScanArg[V](td, key); ok {
		return v
	}
	return def
}

// MaybeScanArg is like ScanArg, but returns false (and the zero value) if the
// argument doesn't exist.
func MaybeScanArg[V any](td *TestData, key string) (V, bool) {
	td.tb().Helper()
	var v V
	ok := td.MaybeScanArgs(key, &v)
	return v, ok
}
