package offline

import (
	"path"
	"strings"
)

// egressBinaries are the well-known command-line tools whose sole purpose is to
// move bytes across the network. In offline mode the bash tool refuses any
// command that invokes one, since each is a channel through which source code
// could leave the machine — the very thing offline mode promises cannot happen.
// The set is deliberately limited to dedicated network clients; broad tools that
// merely *can* reach the network (language runtimes, package managers, cloud
// CLIs) are not listed, because matching them by name would block far more
// legitimate local work than it would stop. EgressCommand documents that limit.
var egressBinaries = map[string]struct{}{
	"curl":   {},
	"wget":   {},
	"scp":    {},
	"sftp":   {},
	"rsync":  {},
	"ssh":    {},
	"telnet": {},
	"ftp":    {},
	"tftp":   {},
	"nc":     {},
	"ncat":   {},
	"netcat": {},
	"socat":  {},
	"lftp":   {},
	"aria2":  {},
	"aria2c": {},
	"wput":   {},
	"httpie": {},
	"xh":     {},
	"http":   {},
	"https":  {},
}

// commandWrappers prefix another command without changing what it does; the real
// command word follows once their own flags and arguments are consumed. Tracking
// them lets EgressCommand see the "curl" in "sudo curl ..." or "timeout 5 curl
// ...". Their flag/duration arguments are skipped while still in command
// position (see the scan in EgressCommand).
var commandWrappers = map[string]struct{}{
	"sudo":    {},
	"doas":    {},
	"env":     {},
	"nohup":   {},
	"setsid":  {},
	"stdbuf":  {},
	"nice":    {},
	"ionice":  {},
	"time":    {},
	"timeout": {},
	"command": {},
	"builtin": {},
	"exec":    {},
	"xargs":   {},
}

// gitNetworkSubcommands are the git verbs that contact a remote, and so can
// push source off the machine. "git push" / "git clone" are the obvious exfil
// paths; fetch/pull also open a remote connection.
var gitNetworkSubcommands = map[string]struct{}{
	"push":  {},
	"pull":  {},
	"fetch": {},
	"clone": {},
}

// EgressCommand reports whether command invokes a known network-egress tool, and
// names the offending invocation when it does. It recognizes a dedicated network
// client (curl, wget, scp, ssh, …) used as a command word — at the start of the
// command, after a pipe/`&&`/`;`/`(`, or behind a wrapper such as sudo/timeout —
// and the network-contacting git subcommands (git push/pull/fetch/clone). The
// scan is quote-aware so a separator or tool name inside a quoted string (e.g.
// `echo "a; curl b"`) is not mistaken for a real invocation.
//
// It is intentionally not exhaustive: a determined caller can still reach the
// network through a language runtime ("python3 -c …") or a cloud CLI, which name
// matching cannot distinguish from ordinary local use. The airtight parts of
// offline mode are the provider, MCP-server, and web-tool checks; this guard
// closes the common, direct shell-egress hole on top of them.
func EgressCommand(command string) (string, bool) {
	tokens := tokenize(command)
	inCmdPos := true
	afterWrapper := false
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if tok.sep {
			inCmdPos = true
			afterWrapper = false
			continue
		}
		if !inCmdPos {
			continue
		}
		word := tok.text
		// An environment assignment (VAR=value) prefixing a command keeps the next
		// word in command position: "FOO=bar curl ..." still runs curl.
		if isAssignment(word) {
			continue
		}
		// A wrapper's own flags and numeric/duration arguments (timeout's "5",
		// stdbuf's "-oL") precede the wrapped command; skip them while staying in
		// command position so the wrapped command word is still examined.
		if strings.HasPrefix(word, "-") {
			// A bare short option behind a wrapper may take the next token as its
			// value ("sudo -u root curl"); skip that value so it is not read as the
			// command word, which would otherwise hide the real command behind it.
			if afterWrapper && len(word) == 2 && i+1 < len(tokens) && !tokens[i+1].sep {
				i++
			}
			continue
		}
		if isNumericArg(word) {
			continue
		}
		name := commandName(word)
		if _, ok := commandWrappers[name]; ok {
			// The wrapped command follows; remain in command position.
			afterWrapper = true
			continue
		}
		if name == "git" {
			if sub, ok := gitNetworkSubcommand(tokens[i+1:]); ok {
				return "git " + sub, true
			}
		}
		if _, ok := egressBinaries[name]; ok {
			return name, true
		}
		// A real, non-wrapper command word: its remaining tokens are arguments, not
		// commands, until the next separator.
		inCmdPos = false
		afterWrapper = false
	}
	return "", false
}

// gitNetworkSubcommand scans a git invocation's tokens (those after the "git"
// word, up to the next separator) for a network subcommand, skipping git's
// pre-subcommand global options ("-C dir", "-c k=v", "--no-pager", …). It
// returns the subcommand and true on the first network verb found.
func gitNetworkSubcommand(rest []token) (string, bool) {
	for i := 0; i < len(rest); i++ {
		tok := rest[i]
		if tok.sep {
			return "", false
		}
		w := tok.text
		// "-C <path>" and "-c <name=value>" take their value in the next token;
		// skip it so the path/config is not mistaken for the subcommand.
		if w == "-C" || w == "-c" {
			i++
			continue
		}
		if strings.HasPrefix(w, "-") || isAssignment(w) {
			continue
		}
		if _, ok := gitNetworkSubcommands[w]; ok {
			return w, true
		}
		// The first non-flag word is the subcommand; if it is not a network verb,
		// this git invocation does not reach a remote.
		return "", false
	}
	return "", false
}

// token is one lexical unit of a shell command: either an operator that
// separates commands (sep == true) or an ordinary word.
type token struct {
	text string
	sep  bool
}

// tokenize splits command into words and command-separating operators, honoring
// single and double quotes so that operators or tool names inside a quoted
// string are kept as ordinary word content rather than treated as structure.
// Only the control operators that begin a new command — | || && ; & (with
// surrounding space) ( ) and a `$(` subshell opener — are emitted as separators;
// redirections (> >> < 2>) are left as word content so a file named like a tool
// ("echo hi > curl") does not read as an invocation.
func tokenize(command string) []token {
	var tokens []token
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, token{text: cur.String()})
			cur.Reset()
		}
	}
	addSep := func() {
		flush()
		tokens = append(tokens, token{sep: true})
	}

	runes := []rune(command)
	var quote rune // 0, '\'' or '"'
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			} else {
				cur.WriteRune(c)
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '\\':
			// A backslash escapes the next character; keep it as word content so an
			// escaped separator does not split the command.
			if i+1 < len(runes) {
				cur.WriteRune(runes[i+1])
				i++
			}
		case '|', ';', '\n', '(', ')', '`':
			addSep()
			// Collapse "||" into a single separator.
			if c == '|' && i+1 < len(runes) && runes[i+1] == '|' {
				i++
			}
		case '&':
			// "&&" and a standalone background "&" separate commands; ">&", "2>&1"
			// and similar redirections do not. Treat "&" as a separator only when it
			// doubles or stands alone (followed by whitespace or end of input).
			if i+1 < len(runes) && runes[i+1] == '&' {
				addSep()
				i++
			} else if i+1 >= len(runes) || isSpace(runes[i+1]) {
				addSep()
			} else {
				cur.WriteRune(c)
			}
		case '$':
			// "$(" opens a command substitution whose body starts a fresh command.
			if i+1 < len(runes) && runes[i+1] == '(' {
				addSep()
				i++
			} else {
				cur.WriteRune(c)
			}
		default:
			if isSpace(c) {
				flush()
			} else {
				cur.WriteRune(c)
			}
		}
	}
	flush()
	return tokens
}

func isSpace(c rune) bool {
	return c == ' ' || c == '\t' || c == '\r'
}

// commandName reduces a command word to the bare program name: it strips any
// directory prefix ("/usr/bin/curl" -> "curl", "./curl" -> "curl") and lowercases
// the result so matching is case-insensitive.
func commandName(word string) string {
	word = strings.Trim(word, `"'`)
	if word == "" {
		return ""
	}
	base := path.Base(word)
	return strings.ToLower(base)
}

// isAssignment reports whether word has the shell VAR=value form of an inline
// environment assignment.
func isAssignment(word string) bool {
	eq := strings.IndexByte(word, '=')
	if eq <= 0 {
		return false
	}
	for i := 0; i < eq; i++ {
		c := word[i]
		if c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (i > 0 && c >= '0' && c <= '9') {
			continue
		}
		return false
	}
	return true
}

// isNumericArg reports whether word is a bare number or a duration like "5s" or
// "10m" — the shape of a wrapper argument (timeout's interval) that should be
// skipped while still looking for the wrapped command word.
func isNumericArg(word string) bool {
	if word == "" {
		return false
	}
	digits := 0
	for i, c := range word {
		if c >= '0' && c <= '9' {
			digits++
			continue
		}
		// A trailing single unit letter (s/m/h/d) is allowed for durations.
		if i == len(word)-1 && digits > 0 && strings.ContainsRune("smhd", c) {
			continue
		}
		return false
	}
	return digits > 0
}
