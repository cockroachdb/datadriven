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
flags (`-key`, `-key=value`, `-key=(value, ...)`), and other words (as well as
all the words after a `--` word) are positional arguments. The test files must
be updated accordingly: the arguments that the test reads by key (e.g. with
`ScanArgs` or `HasArg`) become flags, and the words that the test reads by
position (e.g. with `d.CmdArgs[0].Key`) stay as they are.

Words are separated by any whitespace (including tabs), except inside
parentheses. Parentheses must be balanced in every word, and there is no
escaping: a notation like `[a,b)` (e.g. a half-open span) must be changed, along
with the test code that parses it.

All the flags must be processed by the test, otherwise the directive fails;
this catches misspelled flags. Flags that the test retrieves by name are
processed automatically; flags that are processed by iterating over `d.Flags`
must be marked with `d.MarkFlagProcessed` (or `d.MarkAllFlagsProcessed`).

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
# TestData.Logf is removed.
perl -pi -e 's/\b(d|td)\.ScanArgs\((t|b|tb), /$1.ScanFlagVals(/g; s/\b(d|td)\.MaybeScanArgs\((t|b|tb), /$1.MaybeScanFlagVals(/g; s/\b(d|td)\.(Fatalf|Retry|RetryFor)\((t|b|tb), /$1.$2(/g; s/\b(d|td)\.Logf\((t|b|tb), /$2.Logf(/g; s/\b(d|td)\.HasArg\(/$1.HasFlag(/g; s/\b(d|td)\.Arg\(/$1.Flag(/g' $FILES

# Helpers that take a *testing.T along with a TestData can take a testing.TB.
perl -pi -e 's/^(func \w+\(\w+) \*testing\.T(, \w+ \*datadriven\.TestData)/$1 testing.TB$2/' $FILES

# CockroachDB: pkg/testutils/dd is replaced by the generic ScanFlag functions.
perl -pi -e 's/\bdd\.ScanArgOpt\b/dd.MaybeScanFlag/g; s/\bdd\.ScanArgOr\b/dd.ScanFlagOr/g; s/\bdd\.ScanArg\b/dd.ScanFlag/g; s/\bdd\.(ScanFlag|ScanFlagOr|MaybeScanFlag)(\[(?:[^\[\]]|\[[^\[\]]*\])*\])?\((t|b|tb), /datadriven.$1$2(/g' $FILES
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
- **Flags and arguments.** `TestData.CmdArgs` is replaced by `Flags` (of type
  `Flag`, which replaces `CmdArg`; `Key` is now `Name`) and `Args` (the
  positional arguments, as strings). `ScanFlag`, `ScanFlagOr` and
  `MaybeScanFlag` replace the `var x T; d.ScanArgs("x", &x)` pattern; repeated
  flags (e.g. `-x=1 -x=2`) are retrieved with `FlagValues` or
  `ScanFlagValues`. Note that in v1, `ScanArgs`, `Arg` and `HasArg` used the
  first occurrence of a repeated key; in v2, `Flag`, `ScanFlagVals` and the
  `ScanFlag` functions fail if the flag is repeated, so code that appends
  default flags to override them must remove the duplicates. A flag without values (e.g. `-verbose`) can be scanned
  into a bool, as true. Positional arguments can be retrieved with `PopArg`.
  `CmdArg.ExpectNumVals` and `ExpectNumValsGE` are removed (check
  `len(f.Vals)`). Some error messages have changed.
- **`ParseLine`** returns the flags and the arguments separately (e.g.
  `d.Cmd, d.Flags, d.Args, err = datadriven.ParseLine(line)`). Only the flags
  on the directive line itself must be processed; flags parsed this way are not
  checked.
- **`FullCmd` and `TestData.String`** render the flags (with their dashes)
  before the arguments, so expected outputs that include them change.
- **Subtests in benchmarks.** `subtest` directives are not supported when
  running over a `*testing.B`; they now fail with an error (in v1, the repeated
  invocations of a sub-benchmark function consumed the rest of the file).
- **Unknown commands.** A `default:` case that returns an "unknown command"
  output (instead of failing) lets `-rewrite` record that output as expected;
  consider replacing it with `t.Fatalf`.
