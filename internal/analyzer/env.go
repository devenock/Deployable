package analyzer

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

var envVarPatterns = []*regexp.Regexp{
	// Go: os.Getenv("X"), os.LookupEnv("X")
	regexp.MustCompile(`os\.(?:Getenv|LookupEnv)\(\s*"([A-Za-z0-9_]+)"\s*\)`),
	// Node: process.env.X, process.env["X"], process.env['X']
	regexp.MustCompile(`process\.env\.([A-Za-z0-9_]+)`),
	regexp.MustCompile(`process\.env\[\s*['"]([A-Za-z0-9_]+)['"]\s*\]`),
	// Python: os.environ["X"], os.environ.get("X"), os.getenv("X")
	regexp.MustCompile(`os\.environ\[\s*['"]([A-Za-z0-9_]+)['"]\s*\]`),
	regexp.MustCompile(`os\.environ\.get\(\s*['"]([A-Za-z0-9_]+)['"]`),
	regexp.MustCompile(`os\.getenv\(\s*['"]([A-Za-z0-9_]+)['"]`),
	// Ruby: ENV["X"], ENV.fetch("X")
	regexp.MustCompile(`ENV\[\s*['"]([A-Za-z0-9_]+)['"]\s*\]`),
	regexp.MustCompile(`ENV\.fetch\(\s*['"]([A-Za-z0-9_]+)['"]`),
}

var envScanExts = map[string]bool{
	".go": true, ".js": true, ".ts": true, ".jsx": true, ".tsx": true,
	".py": true, ".rb": true,
}

const envScanByteBudget = 8 * 1024 * 1024 // 8MB total across all files scanned
const envScanFileCap = 2000

// ExtractEnvVars scans source files for references to environment variables
// (os.Getenv, process.env.X, os.environ[...], ENV[...]) and returns the
// sorted, de-duplicated list of variable names found.
func ExtractEnvVars(manifest FileManifest, dir string) []string {
	found := map[string]bool{}
	var scanned, budget int

	for _, f := range manifest.Files {
		if !envScanExts[f.Ext] {
			continue
		}
		if scanned >= envScanFileCap || budget >= envScanByteBudget {
			break
		}

		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if err != nil {
			continue
		}
		scanned++
		budget += len(data)

		content := string(data)
		for _, pattern := range envVarPatterns {
			for _, m := range pattern.FindAllStringSubmatch(content, -1) {
				found[m[1]] = true
			}
		}
	}

	vars := make([]string, 0, len(found))
	for name := range found {
		vars = append(vars, name)
	}
	sort.Strings(vars)
	return vars
}
