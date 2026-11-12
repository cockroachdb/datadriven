# Migrating from v1 to v2

v2 requires Go 1.27. v1 (v1.0.2 or later; older versions panic at startup)
and v2 can be linked in the same test binary (they share the `-rewrite` and
`-datadriven-quiet` flags), so a large repository can be migrated one package
at a time. Note that `TestData` (and the other types) of v1 and v2 are
different, so packages that pass them to each other (e.g. a shared helper
package with functions that take a `*datadriven.TestData`) must be migrated
together.

## Directive line syntax

In v1, the words after the command were all arguments of the form `key`,
`key=value` or `key=(value, ...)`. In v2, the directive line is similar to a
command line: words that start with `-` (or `--`) followed by a letter are
flags (`-key`, `-key=value`, or `-key=(value, ...)` for a list), and other words
(as well as all the words after a `--` word) are positional arguments. The test
files must be updated accordingly: the arguments that the test reads by key
(e.g. with `ScanArgs` or `HasArg`) become flags, and the words that the test
reads by position (e.g. with `d.CmdArgs[0].Key`) stay as they are.

A flag can only be repeated if the test retrieves all its values, with
`d.FlagValues[T]("x")` (one value per occurrence, in order); the other methods
fail if the flag is repeated. In v1, the first occurrence of a repeated key was
used; code that relies on this (e.g. by appending default arguments to
`d.CmdArgs`, so that they can be overridden) must be changed.
Also, `-key=(value)` is a list with one value (which can only be retrieved as a
slice), and `-key=()` is an empty list; in v1, both were a single value. A
single value with whitespace is written between single quotes:
`key=(some value)` becomes `-key='some value'`.

Words are separated by any whitespace (including tabs), except inside
parentheses and quoted values. An argument or a flag value that starts with a
single quote is a quoted value (see `ParseLine`): it extends to the closing
quote (which must end the word), it is taken literally, and the quotes are
removed. In v1 the quotes were part of the value: an argument like
`uri='nodelocal://1/foo'` now has the value `nodelocal://1/foo`, and a value
that must include the quotes is written as `'''nodelocal://1/foo'''` (two
quotes inside a quoted value stand for a quote). Parentheses must be balanced
in every word that is not quoted: a notation like `[a,b)` (e.g. a half-open
span) must be quoted (`'[a,b)'`) or changed.

All the flags must be processed by the test, otherwise the test fails (after
the rest of the file is processed); this catches misspelled flags. It applies
even if the test returns early for a directive (e.g. with an expected error),
so a flag that the test doesn't get to use must be removed from the test file.

An argument which was not converted to a flag (e.g. `sync` or `lower=a`, where
the test now looks up the flags `-sync` or `-lower`) is a positional argument,
which the test would silently ignore. The test fails for these as well, as
long as it looks up the flag while running that directive. An argument which
just happens to have the name of such a flag must be quoted (`'sync'`) or put
after a `--` word. Flags that the test retrieves by name are
processed automatically; flags that are processed by iterating over
`d.Flags` must be marked with `d.MarkFlagProcessed` (or
`d.MarkAllFlagsProcessed`).

## Mechanical changes

The following rewrites (run on the Go files that use datadriven) take care of
most of the changes. The receiver and argument names they match (`d`/`td`,
`t`/`b`/`tb`) are the common ones; adjust them as needed. Afterwards, run
`goimports` (or remove unused imports) and fix the remaining compile errors (see
below).

```bash
# Import path.
perl -pi -e 's#"github.com/cockroachdb/datadriven"#"github.com/cockroachdb/datadriven/v2"#' $FILES

# Callback types; the Any variants are replaced by the generic functions.
perl -pi -e 's/func\((\w+) \*testing\.T, (\w+) \*datadriven\.TestData\) (string|\(\w+ string\))/func($1 *datadriven.T, $2 *datadriven.TestData) $3/g; s/func\((\w+) \*testing\.B, (\w+) \*datadriven\.TestData\) (string|\(\w+ string\))/func($1 *datadriven.B, $2 *datadriven.TestData) $3/g; s/func\((\w+) testing\.TB, (\w+) \*datadriven\.TestData\) (string|\(\w+ string\))/func($1 *datadriven.TBWrapper[testing.TB], $2 *datadriven.TestData) $3/g; s/\bdatadriven\.(RunTest|RunTestFromString|Walk)Any\(/datadriven.${1}[testing.TB](/g' $FILES

# TestData methods no longer take a testing.TB, and arguments are now flags;
# TestData.Logf is removed. A MaybeScanArgs statement with one destination
# becomes FlagValueOr (with the current value as the default).
perl -pi -e 's/^(\s*)(d|td)\.MaybeScanArgs\((t|b|tb), ("[^"]*"), &([\w.]+)\)$/$1$5 = $2.FlagValueOr($4, $5)/; s/\b(d|td)\.(Fatalf|Retry|RetryFor)\((t|b|tb), /$1.$2(/g; s/\b(d|td)\.Logf\((t|b|tb), /$2.Logf(/g; s/\b(d|td)\.HasArg\(/$1.HasFlag(/g' $FILES

# Helpers that take a *testing.T along with a TestData can take a testing.TB.
perl -pi -e 's/^(func \w+\(\w+) \*testing\.T(, \w+ \*datadriven\.TestData)/$1 testing.TB$2/' $FILES

# CockroachDB: dd.ScanArg and dd.ScanArgOr (pkg/testutils/dd) are replaced by
# FlagValue and FlagValueOr.
perl -pi -e 's/\bdd\.ScanArgOr\b/dd.FlagValueOr/g; s/\bdd\.ScanArg\b/dd.FlagValue/g; s/\bdd\.(FlagValue|FlagValueOr)(\[(?:[^\[\]]|\[[^\[\]]*\])*\])?\((t|b|tb), (\w+), /$4.$1$2(/g' $FILES
```

## Other changes

- **The callback receives a wrapper.** `*datadriven.T` (or `*datadriven.B`)
  wraps the `*testing.T` (or `*testing.B`) and annotates messages with the
  position of the directive, so including `d.Pos` in messages is no longer
  necessary (it is not printed twice). The position is that of the directive
  which is running when the message is reported, so a logger used by background
  goroutines should be given `t.TB` and not the wrapper. The wrapper implements
  `testing.TB`; methods specific to `*testing.T` or `*testing.B` are available
  through the `TB` field (e.g. `t.TB.Run(...)`, `b.TB.ReportMetric(...)`). A
  helper that requires a `*testing.T` can be passed `t.TB`.
- **`TestData.T`.** The wrapper is also available as `TestData.T`, which the
  `TestData` methods use to report errors. A `TestData` constructed manually
  (e.g. `TestData{Cmd: ...}`) must set `T` before using those methods.
- **`TestData.Pos` is a struct** (`File`, `Line`). Use `d.Pos.String()` where
  a string is needed, and `d.Pos.File` / `d.Pos.Line` instead of parsing the
  string. Assigning `d.Pos` no longer affects messages: to report errors at the
  position of a line within the input, iterate with `d.InputLines()` or
  `d.InputDirectives()`.
- **Flags and arguments.** `TestData.CmdArgs` is replaced by `Flags` (of type
  `Flag`, which replaces `CmdArg`, with `Name`, `Vals` and `List`) and `Args`
  (the positional arguments, as strings). Flags are usually accessed by name:
  the value of a flag is retrieved with `d.FlagValue[T]("x")` (which replaces
  the `var x T; d.ScanArgs("x", &x)` pattern; the type can't be inferred, so
  this isn't done by the rewrites above) or `d.FlagValueOr("x", def)`; a list
  value is retrieved as a slice. Code that depends on whether an optional
  argument is present (e.g. `if d.MaybeScanArgs(...)`, or CockroachDB's
  `dd.ScanArgOpt`) becomes
  `if d.HasFlag("x") { x := d.FlagValue[T]("x"); ... }`. A `ScanArgs` with
  several destinations (`d.ScanArgs(t, "x", &a, &b)`) becomes
  `vals := d.FlagValue[[]T]("x")` and a check of `len(vals)` (use `[]string`
  and parse the values if they have different types). `d.HasFlag` replaces
  `d.HasArg`, and `d.BoolFlag` checks for a flag without a value (e.g.
  `-verbose`). The other scanning methods (`Arg`, `CmdArg.Scan`, `FirstVal`,
  etc.) are removed. Positional arguments can be retrieved with `SingleArg` and
  `TwoArgs` (for directives which take exactly one or two), `PopArg`, `PopArgs`
  and `ScanArgs`; the latter two parse values like `FlagValue` does (e.g.
  integers in base 10), including types that implement
  `encoding.TextUnmarshaler`. Note that `ScanArgs` now scans the positional
  arguments: v1 code like `d.ScanArgs(t, "x", &x)` still compiles, but fails at
  run time (with a message pointing to `FlagValue`). Some error messages have
  changed.
- **Commands in the input.** Tests that parse each line of the input with
  `ParseLine` can use `for cmd := range d.InputDirectives()` instead: each
  line is returned as a `TestData`, errors are reported at the position of the
  line, and all the flags on the line must be processed.
- **`ParseLine`** returns the flags and the arguments separately
  (`cmd, flags, args, err := datadriven.ParseLine(line)`); to use the flag API
  on them, construct a `TestData` (e.g.
  `&datadriven.TestData{Cmd: cmd, Flags: flags, Args: args, T: d.T}`). Flags
  parsed this way are not checked for being processed.
- **`FullCmd` and `TestData.String`** render the flags (with their dashes)
  before the arguments, so expected outputs that include them change.
- **Command lines.** A test which passes the directive line to another
  command-line parser (e.g. by rendering each of `d.CmdArgs`) should use
  `d.DirectiveLine()`, which returns the line as it is written (flags are
  otherwise rendered with a single dash, even if they were written with two)
  and disables the check that all the flags are processed.
- **`RunTestFromString` and `-rewrite`.** `RunTestFromString` now compares the
  outputs even with `-rewrite` (in v1, its tests passed vacuously), and
  `TestData.Rewrite` is only set when the test file is being rewritten.
- **Unterminated double separators.** An expected output that starts with a
  double `----` separator must end with one; otherwise, the test fails (in v1,
  the rest of the file was taken as the expected output, and removed by
  `-rewrite`).
- **Subtests in benchmarks.** `subtest` directives are not supported when
  running over a `*testing.B`; they now fail with an error (in v1, the repeated
  invocations of a sub-benchmark function consumed the rest of the file).
- **Unknown commands.** A `default:` case that returns an "unknown command"
  output (instead of failing) lets `-rewrite` record that output as expected;
  consider replacing it with `t.Fatalf`.
