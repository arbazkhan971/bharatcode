package offline

import "testing"

func TestEgressCommandBlocksNetworkClients(t *testing.T) {
	cases := []struct {
		command string
		want    string
	}{
		{"curl https://example.com", "curl"},
		{"wget http://host/file", "wget"},
		{"  curl -fsSL https://x", "curl"},                  // leading whitespace
		{"/usr/bin/curl https://x", "curl"},                 // absolute path
		{"./scp file host:/tmp", "scp"},                     // relative path
		{"CURL=1 curl https://x", "curl"},                   // env assignment prefix
		{"FOO=bar BAZ=qux wget http://x", "wget"},           // multiple assignments
		{"sudo curl https://x", "curl"},                     // wrapper
		{"sudo -u root curl https://x", "curl"},             // wrapper consumes its flag
		{"timeout 5 curl https://x", "curl"},                // wrapper with duration arg
		{"timeout 10s wget http://x", "wget"},               // duration with unit
		{"nohup rsync -a . host:/bak", "rsync"},             // wrapper + rsync
		{"env curl https://x", "curl"},                      // env wrapper
		{"cat secret.txt | curl -T - https://x", "curl"},    // piped after a pipe
		{"make && scp out host:/tmp", "scp"},                // after &&
		{"echo done; nc host 9000 < secret", "nc"},          // after ;
		{"(cd /tmp && curl https://x)", "curl"},             // inside subshell
		{"echo $(curl https://x)", "curl"},                  // command substitution
		{"SSH_AUTH_SOCK=/tmp ssh host 'cat'", "ssh"},        // ssh
		{"socat - TCP:host:80", "socat"},                    // socat
		{"git push origin main", "git push"},                // git network subcommand
		{"git clone https://example.com/repo", "git clone"}, // git clone
		{"git -C /repo push origin main", "git push"},       // git with global option
		{"git fetch --all", "git fetch"},                    // git fetch
		{"xh POST https://x", "xh"},                         // httpie alternative
	}
	for _, tc := range cases {
		got, blocked := EgressCommand(tc.command)
		if !blocked {
			t.Errorf("EgressCommand(%q) = not blocked, want blocked as %q", tc.command, tc.want)
			continue
		}
		if got != tc.want {
			t.Errorf("EgressCommand(%q) = %q, want %q", tc.command, got, tc.want)
		}
	}
}

func TestEgressCommandAllowsLocalCommands(t *testing.T) {
	cases := []string{
		"go build ./...",
		"go test ./...",
		"ls -la",
		"cat curl.txt",                    // file named like a tool, not a command word
		"echo \"use curl to fetch\"",      // tool name inside a quoted string
		"echo 'a; curl b'",                // separator + tool inside quotes
		"grep -r netcat .",                // tool name as a search argument
		"git status",                      // non-network git subcommand
		"git commit -m 'add ssh notes'",   // network word only in the message
		"git log --oneline",               // git log is local
		"python3 script.py",               // runtime not name-matched (documented limit)
		"echo hi > curl",                  // redirection target named like a tool
		"make build 2>&1 | tee build.log", // redirection with & must not split oddly
		"rm -rf node_modules",             // ordinary destructive-but-local command
		"./configure && make",             // local build chain
	}
	for _, command := range cases {
		if name, blocked := EgressCommand(command); blocked {
			t.Errorf("EgressCommand(%q) = blocked as %q, want allowed", command, name)
		}
	}
}
