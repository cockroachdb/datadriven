// Copyright 2018 The Cockroach Authors.
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
	"errors"
	"fmt"
	"reflect"
)

// A DriverMap maps commands to test functions (represented via interface{}). A
// test function must accept a single parameter (the input for a test case) and
// output (the output corresponding to the input). Test functions do not have
// to be pure (a test function could in principle take a SQL statement and run
// it against a database). The input and output types should be kept simple
// enough to be encoded/decoded to/from common formats such as JSON or YAML.
type DriverMap map[string]interface{}

func functionAndType(f interface{}) (v reflect.Value, t reflect.Type, ok bool) {
	v = reflect.ValueOf(f)
	ok = v.Kind() == reflect.Func
	if !ok {
		return
	}
	t = v.Type()
	return
}

// reflectCall looks up a function from the map (verifying that it takes and
// returns one arg). It then synthesizes (via reflection) variables of the
// correct input and output type, calls populate with pointers to these
// variables, and invokes the test function. Finally, it returns the value
// passed to the test function, the expected value (as set by populate()), and
// the actual return value. The caller will want to verify that exp == out to
// check whether the test passed, and populate() will usually unmarshal into
// its arguments from some testdata format (e.g. YAML).
func (m DriverMap) reflectCall(name string, populate func(in, exp interface{}) error) (in, exp, out interface{}, _ error) {
	f, ok := m[name]
	if !ok {
		return nil, nil, nil, fmt.Errorf("driver %q not found", name)
	}
	fVal, fType, ok := functionAndType(f)
	if !ok {
		return nil, nil, nil, errors.New("argument is not a function")
	}

	if fType.NumOut() != 1 || fType.NumIn() != 1 {
		return nil, nil, nil, errors.New("function does not take and return one value")
	}

	// If f is of type func(A) B, then this is (mod reflection)
	// vIn, vExp := &A{}, &B{}
	vIn := reflect.New(fType.In(0))
	vExp := reflect.New(fType.Out(0))

	if err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("%+v", r)
			}
		}()

		// This corresponds to populate(vIn, vExp).
		if err := populate(vIn.Interface(), vExp.Interface()); err != nil {
			return err
		}
		// Corresponds to:
		// in, exp := interface{}(*vIn), interface{}(*vExp).
		in = vIn.Elem().Interface()
		exp = vExp.Elem().Interface()
		out = fVal.Call([]reflect.Value{vIn.Elem()})[0].Interface()
		return nil
	}(); err != nil {
		return nil, nil, nil, err
	}

	return in, exp, out, nil
}
