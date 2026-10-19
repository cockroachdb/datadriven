# Migrating from v1 to v2

v2 requires Go 1.24. v1 (v1.0.2 or later; older versions panic at startup)
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

A flag can't be repeated. In v1, a repeated key was allowed, and the first
occurrence was used; code that relies on this (e.g. by appending default
arguments to `d.CmdArgs`, so that they can be overridden) must be changed.
Also, `-key=(value)` is a list with one value (which can only be retrieved as a
slice), and `-key=()` is an empty list; in v1, both were a single value.

Words are separated by any whitespace (including tabs), except inside
parentheses. Parentheses must be balanced in every word, and there is no
escaping: a notation like `[a,b)` (e.g. a half-open span) must be changed, along
with the test code that parses it.

All the flags must be processed by the test, otherwise the directive fails;
this catches misspelled flags. Flags that the test retrieves by name are
processed automatically; flags that are processed by iterating over
`d.FlagNames()` must be marked with `d.MarkFlagProcessed` (or
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
perl -pi -e 's/^(\s*)(d|td)\.MaybeScanArgs\((t|b|tb), ("[^"]*"), &([\w.]+)\)$/$1$5 = datadriven.FlagValueOr($2, $4, $5)/; s/\b(d|td)\.(Fatalf|Retry|RetryFor)\((t|b|tb), /$1.$2(/g; s/\b(d|td)\.Logf\((t|b|tb), /$2.Logf(/g; s/\b(d|td)\.HasArg\(/$1.HasFlag(/g' $FILES

# Helpers that take a *testing.T along with a TestData can take a testing.TB.
perl -pi -e 's/^(func \w+\(\w+) \*testing\.T(, \w+ \*datadriven\.TestData)/$1 testing.TB$2/' $FILES

# CockroachDB: dd.ScanArg and dd.ScanArgOr (pkg/testutils/dd) are replaced by
# FlagValue and FlagValueOr.
perl -pi -e 's/\bdd\.ScanArgOr\b/dd.FlagValueOr/g; s/\bdd\.ScanArg\b/dd.FlagValue/g; s/\bdd\.(FlagValue|FlagValueOr)(\[(?:[^\[\]]|\[[^\[\]]*\])*\])?\((t|b|tb), /datadriven.$1$2(/g' $FILES
```

## Other changes

- **The callback receives a wrapper.** `*datadriven.T` (or `*datadriven.B`)
  wraps the `*testing.T` (or `*testing.B`) and annotates messages with the
  position of the directive, so including `d.Pos` in messages is no longer
  necessary (it is not printed twice). The wrapper implements `testing.TB`;
  methods specific to `*testing.T` or `*testing.B` are available through the
  `TB` field (e.g. `t.TB.Run(...)`, `b.TB.ReportMetric(...)`). A helper that
  requires a `*testing.T` can be passed `t.TB`.
- **`TestData.T`.** The wrapper is also available as `TestData.T`, which the
  `TestData` methods use to report errors. A `TestData` constructed manually
  (e.g. `TestData{Cmd: ...}`) must set `T` before using those methods.
- **`TestData.Pos` is a struct** (`File`, `Line`). Use `d.Pos.String()` where
  a string is needed, and `d.Pos.File` / `d.Pos.Line` instead of parsing the
  string. Assigning `d.Pos` no longer affects messages: to report errors at the
  position of a line within the input, iterate with `d.InputLines()`.
- **Flags and arguments.** `TestData.CmdArgs` and the `CmdArg` type are
  replaced by the flags, which are accessed by name, and `Args` (the positional
  arguments, as strings). The value of a flag is retrieved with
  `datadriven.FlagValue[T](d, "x")` (which replaces the
  `var x T; d.ScanArgs("x", &x)` pattern; the type can't be inferred, so this
  isn't done by the rewrites above) or `datadriven.FlagValueOr(d, "x", def)`; a
  list value is retrieved as a slice. `d.HasFlag` replaces `d.HasArg`, and
  `d.BoolFlag` checks for a flag without a value (e.g. `-verbose`). The other
  scanning methods (`Arg`, multiple-destination `ScanArgs`, `CmdArg.Scan`,
  `FirstVal`, etc.) are removed. Positional arguments can be retrieved with
  `PopArg`. Some error messages have changed.
- **`ParseLine`** returns a `*TestData` (with `Cmd`, `Args` and the flags), on
  which the same API can be used once `T` is set (e.g. `sub.T = d.T`). It also
  replaces `TestData` values constructed manually. Only the flags on the
  directive line itself must be processed; flags parsed this way are not
  checked.
- **`FullCmd` and `TestData.String`** render the flags (with their dashes)
  before the arguments, so expected outputs that include them change.
- **Subtests in benchmarks.** `subtest` directives are not supported when
  running over a `*testing.B`; they now fail with an error (in v1, the repeated
  invocations of a sub-benchmark function consumed the rest of the file).
- **Unknown commands.** A `default:` case that returns an "unknown command"
  output (instead of failing) lets `-rewrite` record that output as expected;
  consider replacing it with `t.Fatalf`.
