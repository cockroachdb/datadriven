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
	"math/big"
	"net"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"
)

// level is an integer type which implements encoding.TextUnmarshaler.
type level int

func (l *level) UnmarshalText(text []byte) error {
	switch string(text) {
	case "low":
		*l = 1
	case "high":
		*l = 2
	default:
		return fmt.Errorf("unknown level")
	}
	return nil
}

// timeout is a type defined from time.Duration which implements
// encoding.TextUnmarshaler.
type timeout time.Duration

func (d *timeout) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(string(text))
	*d = timeout(v)
	return err
}

// TestFlagValueTextUnmarshaler verifies that FlagValue and FlagValueOr parse
// types which implement encoding.TextUnmarshaler using it.
func TestFlagValueTextUnmarshaler(t *testing.T) {
	RunTestFromString(t, `
cmd -addr=10.0.0.1 -addrs=(10.0.0.1, ::1) -big=123456789012345678901234567890 -bigs=(1, 2) \
    -time=2026-01-02T03:04:05Z -ip=10.0.0.2 -ips=(10.0.0.2, 10.0.0.3) \
    -level=high -levels=(low, high) -timeout=1m
----
ok
`, func(t *T, d *TestData) string {
		check := func(name string, got, expected any) {
			t.Helper()
			if !reflect.DeepEqual(got, expected) {
				t.Errorf("%s: expected %#v, got %#v", name, expected, got)
			}
		}
		addr1, addr2 := netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("::1")
		// A type T for which *T implements the interface.
		check("addr", d.FlagValue[netip.Addr]("addr"), addr1)
		check("addr-slice", d.FlagValue[[]netip.Addr]("addr"), []netip.Addr{addr1})
		check("addrs", d.FlagValue[[]netip.Addr]("addrs"), []netip.Addr{addr1, addr2})
		check("time", d.FlagValue[time.Time]("time"), time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
		// A pointer type which implements the interface.
		check("big", d.FlagValue[*big.Int]("big").String(), "123456789012345678901234567890")
		check("bigs", fmt.Sprint(d.FlagValue[[]*big.Int]("bigs")), "[1 2]")
		// A slice type which implements the interface is a single value.
		check("ip", d.FlagValue[net.IP]("ip").String(), "10.0.0.2")
		check("ips", fmt.Sprint(d.FlagValue[[]net.IP]("ips")), "[10.0.0.2 10.0.0.3]")
		// The interface takes precedence over the underlying type.
		check("level", d.FlagValue[level]("level"), level(2))
		check("levels", d.FlagValue[[]level]("levels"), []level{1, 2})
		check("timeout", d.FlagValue[timeout]("timeout"), timeout(time.Minute))
		check("or-level", d.FlagValueOr("level", level(0)), level(2))
		check("or-missing", d.FlagValueOr("missing", level(1)), level(1))
		check("or-big", d.FlagValueOr("big", big.NewInt(1)).String(), "123456789012345678901234567890")
		check("or-missing-big", d.FlagValueOr("missing-big", big.NewInt(1)).String(), "1")
		return "ok"
	})
}

func TestFlagValueTextUnmarshalerErrors(t *testing.T) {
	testCases := []struct {
		directive string
		f         func(d *TestData)
		expected  string
	}{
		{
			"cmd -l=medium", func(d *TestData) { d.FlagValue[level]("l") },
			`<string>:1: flag -l: parse "medium" as datadriven.level: unknown level`,
		},
		{
			"cmd -l=(low, 1)", func(d *TestData) { d.FlagValue[[]level]("l") },
			`<string>:1: flag -l: value 1: parse "1" as datadriven.level: unknown level`,
		},
		{
			"cmd -b=x", func(d *TestData) { d.FlagValue[*big.Int]("b") },
			`<string>:1: flag -b: parse "x" as *big.Int: math/big: cannot unmarshal "x" into a *big.Int`,
		},
		{
			"cmd -ip=(10.0.0.2, 10.0.0.3)", func(d *TestData) { d.FlagValue[net.IP]("ip") },
			`<string>:1: flag -ip: the value is a list; use a slice type`,
		},
		{"cmd -l", func(d *TestData) { d.FlagValue[level]("l") }, `<string>:1: flag -l: no value (see BoolFlag)`},
	}
	for _, tc := range testCases {
		t.Run(tc.directive, func(t *testing.T) {
			rec := &recorderTB{TB: t}
			runAndWait(func() {
				RunTestFromString(rec, tc.directive+"\n----\n", func(t *TBWrapper[*recorderTB], d *TestData) string {
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
