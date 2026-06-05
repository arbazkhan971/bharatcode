package outputfilter

import "regexp"

// builtinFilters is the registry of built-in filters embedded in the binary.
// Lookup is first-match-wins; order here determines priority among builtins.
// Each filter strips noise while preserving the look of real command output —
// no reformatting, no summaries, just noise removal and length caps.
var builtinFilters = []*Filter{
	filterGoBuild,
	filterGoTest,
	filterGoVet,
	filterGoMod,
	filterGofmt,
	filterMake,
	filterCargo,
	filterMaven,
	filterGradle,
	filterNpmInstall,
	filterNpmTest,
	filterPnpmInstall,
	filterYarnInstall,
	filterPipInstall,
	filterPytest,
	filterTerraformPlan,
	filterGitStatus,
	filterGitDiff,
}

// ---- Go toolchain -------------------------------------------------------

var filterGoBuild = &Filter{
	Name:         "go-build",
	Description:  "Compact go build/run output — strip unchanged package lines, keep errors",
	MatchCommand: re(`^go\s+(build|run|install)\b`),
	StripLinesMatching: []*regexp.Regexp{
		re(`^\s*$`),
		// A cold build resolves the module cache first, flooding the output with
		// one "go: downloading ..." line per transitive dependency. They are pure
		// progress noise; real build errors are kept.
		re(`^go: downloading\s+`),
		re(`^go: extracting\s+`),
		re(`^go: finding\s+`),
	},
	MaxLines: 60,
	OnEmpty:  "go build: ok",
}

var filterGoTest = &Filter{
	Name:         "go-test",
	Description:  "Compact go test output — strip ok/cached lines, keep failures and summary",
	MatchCommand: re(`^go\s+test\b`),
	StripLinesMatching: []*regexp.Regexp{
		re(`^\s*$`),
		re(`^ok\s+\S+\s+\(cached\)$`),
		re(`^=== RUN\s+`),
		re(`^--- PASS:`),
		re(`^\s+--- PASS:`),
		// Cold-cache module download progress (see filterGoBuild).
		re(`^go: downloading\s+`),
		re(`^go: extracting\s+`),
		re(`^go: finding\s+`),
	},
	MaxLines: 80,
	OnEmpty:  "go test: all pass",
}

var filterGoVet = &Filter{
	Name:         "go-vet",
	Description:  "Compact go vet output — strip blank lines, keep findings",
	MatchCommand: re(`^go\s+vet\b`),
	StripLinesMatching: []*regexp.Regexp{
		re(`^\s*$`),
		// Cold-cache module download progress (see filterGoBuild).
		re(`^go: downloading\s+`),
		re(`^go: extracting\s+`),
		re(`^go: finding\s+`),
	},
	MaxLines: 50,
	OnEmpty:  "go vet: ok",
}

// filterGoMod tames module-management commands (go mod tidy/download/verify,
// go get, go work). On a cold cache these emit one "go: downloading ..." line
// per dependency — sometimes dozens — which is pure progress noise. The
// meaningful lines (go: added/upgraded/downgraded/removed dependency changes,
// and any error) do not match the strip patterns and are preserved.
var filterGoMod = &Filter{
	Name:         "go-mod",
	Description:  "Compact go mod/get/work output — strip downloading noise, keep dependency changes and errors",
	MatchCommand: re(`^go\s+(mod|get|work)\b`),
	StripLinesMatching: []*regexp.Regexp{
		re(`^\s*$`),
		re(`^go: downloading\s+`),
		re(`^go: extracting\s+`),
		re(`^go: finding\s+`),
	},
	MaxLines: 60,
	OnEmpty:  "go mod: ok",
}

var filterGofmt = &Filter{
	Name:         "gofmt",
	Description:  "Compact gofmt -l output — strip blank lines, keep unformatted file paths",
	MatchCommand: re(`^gofmt\b`),
	StripLinesMatching: []*regexp.Regexp{
		re(`^\s*$`),
	},
	MaxLines: 30,
	OnEmpty:  "gofmt: all files formatted",
}

// ---- make ---------------------------------------------------------------

var filterMake = &Filter{
	Name:         "make",
	Description:  "Compact make output — strip entering/leaving directory and blank lines",
	MatchCommand: re(`^make\b`),
	StripLinesMatching: []*regexp.Regexp{
		re(`^make\[\d+\]:`),
		re(`^\s*$`),
		re(`^Nothing to be done`),
	},
	MaxLines: 50,
	OnEmpty:  "make: ok",
}

// ---- Rust/Cargo ---------------------------------------------------------

var filterCargo = &Filter{
	Name:         "cargo",
	Description:  "Compact cargo build/test output — strip Compiling/Downloading noise, keep errors and summary",
	MatchCommand: re(`^cargo\s+(build|test|check|clippy|run)\b`),
	StripANSI:    true,
	StripLinesMatching: []*regexp.Regexp{
		re(`^\s*$`),
		re(`^\s*Compiling\s+`),
		re(`^\s*Downloading\s+`),
		re(`^\s*Downloaded\s+`),
		re(`^\s*Updating\s+`),
		re(`^\s*Locking\s+`),
		re(`^\s*Blocking\s+`),
		re(`^\s*Fresh\s+`),
	},
	MaxLines: 80,
	OnEmpty:  "cargo: ok",
}

// ---- JVM (Maven / Gradle) -----------------------------------------------

// filterMaven tames Maven build output. Against a cold local repository Maven
// prints one "Downloading from"/"Downloaded from" line per artifact plus
// interleaved "Progress (N): ..." byte-count trickle — often hundreds of lines
// that dwarf the actual build result. It also emits decorative "[INFO] ----"
// rules and a "Scanning for projects" banner. All of that is stripped; the
// signal ([ERROR]/[WARNING] lines, BUILD SUCCESS/FAILURE, and the "Tests run:
// ..." summary) does not match the strip patterns and is preserved. Both the
// modern "[INFO] "-prefixed and the older bare forms of the download lines are
// covered.
var filterMaven = &Filter{
	Name:         "maven",
	Description:  "Compact mvn output — strip artifact download/progress noise and decorative rules, keep errors, warnings and the build result",
	MatchCommand: re(`^(\./)?mvnw?\b`),
	StripANSI:    true,
	StripLinesMatching: []*regexp.Regexp{
		re(`^\s*$`),
		re(`^(\[INFO\]\s+)?Downloading from\b`),
		re(`^(\[INFO\]\s+)?Downloaded from\b`),
		re(`^(\[INFO\]\s+)?Progress\s*\(\d`),
		re(`^\[INFO\]\s+-{10,}\s*$`),
		re(`^\[INFO\] Scanning for projects`),
	},
	MaxLines: 80,
	OnEmpty:  "mvn: ok",
}

// filterGradle tames Gradle build output. Gradle streams a live progress bar
// ("<====---> 75% EXECUTING"), per-task lines for work that is up to date or
// skipped, "Download https://..." lines for plugin/dependency resolution, and
// daemon/welcome chatter. The meaningful lines — "BUILD SUCCESSFUL"/"BUILD
// FAILED", "FAILURE:", a failing "> Task :test FAILED", and compiler errors —
// carry a status outside the stripped set (or no "> Task" prefix at all) and so
// survive.
var filterGradle = &Filter{
	Name:         "gradle",
	Description:  "Compact gradle output — strip progress bar, no-op task lines, download and daemon noise, keep failures and the build result",
	MatchCommand: re(`^(\./)?gradlew?\b`),
	StripANSI:    true,
	StripLinesMatching: []*regexp.Regexp{
		re(`^\s*$`),
		re(`^Download https?://`),
		// Live progress bar lines, e.g. "<=======------> 60% EXECUTING [3s]".
		re(`%\s+(INITIALIZING|CONFIGURING|EXECUTING|WAITING)\b`),
		// Per-task lines for work that produced nothing of interest. A real
		// failure ("> Task :test FAILED") keeps a status outside this set and so
		// is preserved.
		re(`^> Task :\S+(\s+(UP-TO-DATE|NO-SOURCE|SKIPPED|FROM-CACHE))?$`),
		re(`^> Configure project :`),
		re(`^Starting a Gradle Daemon`),
		re(`^Welcome to Gradle\b`),
		re(`^Daemon will be stopped`),
	},
	MaxLines: 80,
	OnEmpty:  "gradle: ok",
}

// ---- Node.js ecosystem --------------------------------------------------

var filterNpmInstall = &Filter{
	Name:         "npm-install",
	Description:  "Compact npm install/ci output — strip progress, keep added/removed summary",
	MatchCommand: re(`^npm\s+(install|ci|i)\b`),
	StripANSI:    true,
	StripLinesMatching: []*regexp.Regexp{
		re(`^\s*$`),
		re(`^npm warn\s+`),
		re(`^npm notice\s+`),
		re(`^added \d+ package`), // keep the "added N packages" line from warn; keep signal lines differently
	},
	MatchOutput: []MatchOutputRule{
		{Pattern: re(`up to date`), Message: "npm install: ok (up to date)"},
	},
	MaxLines: 30,
}

var filterNpmTest = &Filter{
	Name:         "npm-test",
	Description:  "Compact npm test / jest output — strip passing test lines, keep failures and summary",
	MatchCommand: re(`^npm\s+(test|run\s+test|run\s+build)\b`),
	StripANSI:    true,
	StripLinesMatching: []*regexp.Regexp{
		re(`^\s*$`),
		re(`^\s+✓\s+`),
		re(`^\s+✔\s+`),
		re(`^\s+PASS\s+`),
		re(`^\s+√\s+`),
	},
	MaxLines: 80,
	OnEmpty:  "npm test: all pass",
}

var filterPnpmInstall = &Filter{
	Name:         "pnpm-install",
	Description:  "Compact pnpm install output — strip progress, keep summary",
	MatchCommand: re(`^pnpm\s+(install|i|add)\b`),
	StripANSI:    true,
	StripLinesMatching: []*regexp.Regexp{
		re(`^\s*$`),
		re(`^Packages:\s+\+`),
		re(`^Progress:\s+`),
		re(`^\s+WARN\s+`),
	},
	MatchOutput: []MatchOutputRule{
		{Pattern: re(`Already up to date`), Message: "pnpm install: ok (up to date)"},
	},
	MaxLines: 30,
}

var filterYarnInstall = &Filter{
	Name:         "yarn-install",
	Description:  "Compact yarn install output — strip progress and info noise, keep warnings and errors",
	MatchCommand: re(`^yarn\s*(install|add)?\b`),
	StripANSI:    true,
	StripLinesMatching: []*regexp.Regexp{
		re(`^\s*$`),
		re(`^yarn info\s+`),
		re(`^\[1\]\s+`), // yarn v1 step markers like "[1/4] Resolving packages..."
		re(`^\[2\]\s+`),
		re(`^\[3\]\s+`),
		re(`^\[4\]\s+`),
		re(`^success\s+`),
	},
	MaxLines: 30,
	OnEmpty:  "yarn: ok",
}

// ---- Python -------------------------------------------------------------

var filterPipInstall = &Filter{
	Name:         "pip-install",
	Description:  "Compact pip install output — strip already-satisfied/collecting/download progress, keep the installed summary and errors",
	MatchCommand: re(`^(pip[23]?|python[23]?\s+-m\s+pip|uv\s+pip)\s+install\b`),
	StripANSI:    true,
	StripLinesMatching: []*regexp.Regexp{
		re(`^\s*$`),
		re(`^Requirement already satisfied:`),
		re(`^Collecting\s+`),
		re(`^\s*Using cached\s+`),
		re(`^\s*Downloading\s+`),
		re(`^\s*Preparing metadata`),
		re(`^\s*Building wheels?\b`),
		re(`^\s*Created wheel for`),
		re(`^\s*Stored in directory`),
		re(`^\s*Installing collected packages:`),
		re(`^\s*━`),          // download progress bar (modern pip, box-drawing)
		re(`\beta \d`),       // download progress trailer "… eta 0:00:00"
		re(`^\s*\[notice\]`), // "A new release of pip is available" upgrade notice
	},
	MaxLines: 40,
	OnEmpty:  "pip install: requirements already satisfied",
}

var filterPytest = &Filter{
	Name:         "pytest",
	Description:  "Compact pytest output — strip the session header and per-test progress lines, keep failures and the final summary",
	MatchCommand: re(`^(pytest|py\.test|python[23]?\s+-m\s+pytest)\b`),
	StripANSI:    true,
	StripLinesMatching: []*regexp.Regexp{
		re(`^\s*$`),
		re(`^=+ test session starts =+$`), // decorative session banner
		re(`^platform \S+ -- Python`),     // platform/interpreter/plugin versions
		re(`^(rootdir|plugins|cachedir|configfile|testpaths):`),
		re(`^collecting\b`),   // transient "collecting ..." line pytest overwrites
		re(`\[\s*\d+%\]\s*$`), // per-test/per-file progress lines ("... [ 42%]")
	},
	MaxLines: 80,
	OnEmpty:  "pytest: all pass",
}

// ---- Terraform ----------------------------------------------------------

var filterTerraformPlan = &Filter{
	Name:         "terraform-plan",
	Description:  "Compact terraform plan output — strip refresh noise, keep resource changes and summary",
	MatchCommand: re(`^terraform\s+(plan|apply)\b`),
	StripANSI:    true,
	StripLinesMatching: []*regexp.Regexp{
		re(`^Refreshing state`),
		re(`^\s*#.*unchanged`),
		re(`^\s*$`),
		re(`^Acquiring state lock`),
		re(`^Releasing state lock`),
	},
	MaxLines: 80,
	OnEmpty:  "terraform plan: no changes detected",
}

// ---- Git ----------------------------------------------------------------

var filterGitStatus = &Filter{
	Name:         "git-status",
	Description:  "Compact git status — strip blank lines and branch tracking noise",
	MatchCommand: re(`^git\s+status\b`),
	StripLinesMatching: []*regexp.Regexp{
		re(`^\s*$`),
		re(`^On branch\s+`),
		re(`^Your branch is up to date`),
		re(`^nothing to commit`),
		re(`^\s*\(use "git`),
	},
	MaxLines: 60,
	OnEmpty:  "git status: clean",
}

var filterGitDiff = &Filter{
	Name:         "git-diff",
	Description:  "Compact git diff — strip index lines and mode changes, keep hunks",
	MatchCommand: re(`^git\s+(diff|show)\b`),
	StripLinesMatching: []*regexp.Regexp{
		re(`^\s*$`),
		re(`^index [0-9a-f]+\.\.[0-9a-f]+`),
		re(`^old mode `),
		re(`^new mode `),
		re(`^similarity index`),
		re(`^rename from `),
		re(`^rename to `),
	},
	TruncateLinesAt: 200,
	MaxLines:        200,
}

// re compiles a regex and panics at init time on invalid patterns.
// All patterns are literals from this file, so a panic here is a programming error.
func re(pattern string) *regexp.Regexp {
	return regexp.MustCompile(pattern)
}
