// Copyright 2019 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License included
// in the file licenses/BSL.txt and at www.mariadb.com/bsl11.
//
// Change Date: 2022-10-01
//
// On the date above, in accordance with the Business Source License, use
// of this software will be governed by the Apache License, Version 2.0,
// included in the file licenses/APL.txt and at
// http://www.apache.org/licenses/LICENSE-2.0

package datadriven

import (
	"errors"
	"fmt"
	"testing"
)

type A struct {
	N int
}
type B struct {
	S string
}

func TestReflectCall(t *testing.T) {
	isEven := func(i int) bool { return i%2 == 0 }
	popEven := func(in, exp interface{}) error {
		*in.(*int)++
		*exp.(*bool) = false

		return nil
	}

	aToB := func(a A) B { return B{fmt.Sprintf("%03d", a.N)} }
	popAToB := func(in, exp interface{}) error {
		in.(*A).N = 12
		exp.(*B).S = "foo" // not actually what aToB will produce
		return nil
	}

	panicsOnZero := func(i int) int {
		if i == 0 {
			panic("boom")
		}
		return i
	}

	m := DriverMap{
		"even": isEven,
		"atob": aToB,
		"errs": panicsOnZero,
	}

	// Two standard examples to sanity check that things "work".
	{
		in, exp, out, err := m.reflectCall("even", popEven)
		if err != nil {
			t.Fatal(err)
		}
		if i, a, e := in.(int), out.(bool), exp.(bool); i != 1 || a || e {
			t.Fatalf("even: unexpected result (%v,%v,%v)", i, a, e)
		}
	}

	{
		in, exp, out, err := m.reflectCall("atob", popAToB)

		if err != nil {
			t.Fatal(err)
		}
		if i, a, e := in.(A), out.(B), exp.(B); i != (A{N: 12}) || a != (B{S: "012"}) || e != (B{S: "foo"}) {
			t.Fatalf("even: unexpected result (%v,%v,%v)", i, a, e)
		}
	}

	// Test function panics.
	{
		_, _, _, err := m.reflectCall("errs", func(in, exp interface{}) error {
			return nil
		})

		if err == nil || err.Error() != "boom" {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	// Populate panics.
	{
		_, _, _, err := m.reflectCall("errs", func(in, exp interface{}) error {
			panic("fail")
		})

		if err == nil || err.Error() != "fail" {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	// Populate errors.
	{
		_, _, _, err := m.reflectCall("errs", func(in, exp interface{}) error {
			return errors.New("broken")
		})

		if err == nil || err.Error() != "broken" {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}
