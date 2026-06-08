package selfupdate

import "testing"

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name       string
		current    string
		latest     string
		wantUpdate bool
	}{
		{"patch newer", "v0.2.0", "v0.2.1", true},
		{"equal", "v0.2.1", "v0.2.1", false},
		{"minor newer", "v0.2.9", "v0.3.0", true},
		{"major newer", "v1.0.0", "v2.0.0", true},
		{"current newer (downgrade)", "v0.3.0", "v0.2.9", false},
		{"no v prefix", "0.2.0", "0.2.1", true},
		{"mixed prefix", "v0.2.0", "0.2.1", true},
		{"prerelease suffix on latest", "v0.2.0", "v0.2.1-rc1", true},
		{"two vs three components", "v0.2", "v0.2.1", true},
		{"two components equal", "v0.2", "v0.2.0", false},
		{"unknown current", "unknown", "v0.2.1", false},
		{"empty current", "", "v0.2.1", false},
		{"dev placeholder", "v0.0.0", "v0.2.1", false},
		{"malformed latest", "v0.2.0", "nightly", false},
		{"malformed current", "garbage", "v0.2.1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompareVersions(tt.current, tt.latest)
			if got.UpdateAvailable != tt.wantUpdate {
				t.Fatalf("CompareVersions(%q,%q).UpdateAvailable = %v, want %v",
					tt.current, tt.latest, got.UpdateAvailable, tt.wantUpdate)
			}
		})
	}
}

func TestClassifyExePath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want InstallMethod
	}{
		{"npm global on homebrew node", "/opt/homebrew/lib/node_modules/bharatcode-cli/bin/bharatcode", InstallNpm},
		{"npm global unix", "/usr/local/lib/node_modules/bharatcode-cli/bin/bharatcode", InstallNpm},
		{"homebrew cellar", "/opt/homebrew/Cellar/bharatcode/0.2.1/bin/bharatcode", InstallBrew},
		{"linuxbrew", "/home/linuxbrew/.linuxbrew/bin/bharatcode", InstallBrew},
		{"curl local bin", "/Users/arbaz/.local/bin/bharatcode", InstallBinary},
		{"system bin", "/usr/local/bin/bharatcode", InstallBinary},
		{"windows npm", "C:\\Users\\a\\AppData\\Roaming\\npm\\node_modules\\bharatcode-cli\\bin\\bharatcode.exe", InstallNpm},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyExePath(tt.path); got != tt.want {
				t.Fatalf("classifyExePath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestUpgradeCommand(t *testing.T) {
	cases := map[InstallMethod]string{
		InstallNpm:     "npm install -g bharatcode-cli@latest",
		InstallBrew:    "brew upgrade bharatcode",
		InstallBinary:  "bharatcode update",
		InstallUnknown: "bharatcode update",
	}
	for method, want := range cases {
		if got := UpgradeCommand(method); got != want {
			t.Errorf("UpgradeCommand(%q) = %q, want %q", method, got, want)
		}
	}
}

func TestAdviceFor(t *testing.T) {
	noUpdate := ReleaseStatus{Current: "v0.2.1", Latest: "v0.2.1", UpdateAvailable: false}
	if msg := noUpdate.AdviceFor(InstallNpm); msg != "" {
		t.Fatalf("AdviceFor with no update = %q, want empty", msg)
	}
	upd := ReleaseStatus{Current: "v0.2.0", Latest: "v0.2.1", UpdateAvailable: true}
	got := upd.AdviceFor(InstallNpm)
	want := "A new BharatCode is available (v0.2.0 -> v0.2.1). Update: npm install -g bharatcode-cli@latest"
	if got != want {
		t.Fatalf("AdviceFor(npm) = %q, want %q", got, want)
	}
	if brew := upd.AdviceFor(InstallBrew); brew == "" || brew == got {
		t.Fatalf("AdviceFor(brew) should differ from npm and be non-empty, got %q", brew)
	}
}
