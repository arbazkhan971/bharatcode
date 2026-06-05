package agent

import (
	"testing"
	"path/filepath"
	"strings"
)

func TestDbgFlaky(t *testing.T) {
	workdir := t.TempDir()
	empty := filepath.Join(workdir, ".bharatcode", "skills")
	restore := skillSearchDirs
	t.Cleanup(func() { skillSearchDirs = restore })
	writeSkillFixture(t, empty, "pdf", "---\nname: pdf\ndescription: Fill PDF forms\n---\nbody\n")
	skillSearchDirs = func(string) []string { return []string{empty} }
	withSkills := renderInWorkdir(t, workdir)
	skillSearchDirs = func(string) []string { return []string{filepath.Join(workdir, "no-such-dir")} }
	withoutSkills := renderInWorkdir(t, workdir)

	// Extract the Current date lines from both
	getDate := func(s string) string {
		i := strings.Index(s, "Current date: ")
		return s[i:]
	}
	t.Logf("withSkills    date: %s", getDate(withSkills))
	t.Logf("withoutSkills date: %s", getDate(withoutSkills))
	t.Logf("dates equal: %v", getDate(withSkills) == getDate(withoutSkills))
}
