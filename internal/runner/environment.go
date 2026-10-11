package runner

import "path/filepath"

const jobHomeDir = "home"
const jobTempDir = "tmp"
var jobDirectories = []struct {
	path string
	keys []string
}{
	{jobHomeDir, []string{"HOME"}},
	{jobTempDir, []string{"TMPDIR", "TMP", "TEMP"}},
	{"home/.config", []string{"XDG_CONFIG_HOME"}},
	{"home/.cache", []string{"XDG_CACHE_HOME"}},
	{"home/.local", nil},
	{"home/.local/share", []string{"XDG_DATA_HOME"}},
	{"home/.local/state", []string{"XDG_STATE_HOME"}},
	{"tmp/xdg-runtime", []string{"XDG_RUNTIME_DIR"}},
}

func jobEnvironment(workspace string) map[string]string {
	env := make(map[string]string)
	for _, dir := range jobDirectories {
		for _, key := range dir.keys {
			env[key] = filepath.Join(workspace, filepath.FromSlash(dir.path))
		}
	}
	return env
}
