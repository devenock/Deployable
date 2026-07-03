package analyzer

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

var knownLockFiles = []string{
	"go.sum", "package-lock.json", "yarn.lock", "pnpm-lock.yaml",
	"poetry.lock", "Pipfile.lock", "requirements.txt",
	"Gemfile.lock", "Cargo.lock", "composer.lock",
}

var sourceExtsForHealthScan = map[string]bool{
	".go": true, ".js": true, ".ts": true, ".py": true, ".rb": true, ".java": true, ".php": true,
}

const healthScanByteBudget = 5 * 1024 * 1024 // 5MB total across all files scanned
const healthScanFileCap = 500

// CheckInfra assesses deployment-infrastructure files: Dockerfile quality,
// compose config, .gitignore coverage, CI config, and a best-effort health
// check endpoint scan. It does not cross-reference env vars — that's done
// in the pipeline once both InfraChecks and ExtractEnvVars are available.
func CheckInfra(manifest FileManifest, dir string) InfraChecks {
	set := newFileSet(manifest)
	checks := InfraChecks{
		DockerfileExists: set.has("Dockerfile"),
		ComposeExists:    set.hasAny("docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"),
		EnvExampleExists: set.hasAny(".env.example", ".env.sample", ".env.dist"),
		GitignoreExists:  set.has(".gitignore"),
		CIConfigExists:   set.hasMatch(ciConfigPattern) || set.has(".gitlab-ci.yml"),
	}

	if checks.DockerfileExists {
		if content, ok := readRoot(dir, "Dockerfile"); ok {
			fromCount := strings.Count(strings.ToUpper(content), "\nFROM ") + boolToInt(strings.HasPrefix(strings.ToUpper(content), "FROM "))
			checks.DockerfileMultiStage = fromCount >= 2
			checks.DockerfileHasExpose = strings.Contains(strings.ToUpper(content), "EXPOSE")
			upper := strings.ToUpper(content)
			checks.DockerfileHasCMD = strings.Contains(upper, "\nCMD ") || strings.Contains(upper, "\nENTRYPOINT ") ||
				strings.HasPrefix(upper, "CMD ") || strings.HasPrefix(upper, "ENTRYPOINT ")
		}
	}

	if checks.ComposeExists {
		name := firstExisting(set, "docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml")
		if content, ok := readRoot(dir, name); ok {
			checks.ComposeHasServices = strings.Contains(content, "services:")
		}
	}

	if checks.GitignoreExists {
		if content, ok := readRoot(dir, ".gitignore"); ok {
			checks.GitignoreCoversEnv = gitignoreCoversEnv(content)
		}
	}

	for _, lock := range knownLockFiles {
		if set.has(lock) {
			checks.LockFileFound = true
			break
		}
	}
	checks.LockFileMissing = !checks.LockFileFound

	checks.HealthCheckFound = scanForHealthCheck(manifest, dir)

	return checks
}

func gitignoreCoversEnv(content string) bool {
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == ".env" || line == ".env*" || line == "*.env" || strings.HasPrefix(line, ".env") {
			return true
		}
	}
	return false
}

func scanForHealthCheck(manifest FileManifest, dir string) bool {
	var scanned, budget int
	for _, f := range manifest.Files {
		if !sourceExtsForHealthScan[f.Ext] {
			continue
		}
		if scanned >= healthScanFileCap || budget >= healthScanByteBudget {
			break
		}
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if err != nil {
			continue
		}
		scanned++
		budget += len(data)
		if strings.Contains(string(data), "/health") || strings.Contains(string(data), "/healthz") {
			return true
		}
	}
	return false
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ParseEnvExampleKeys extracts variable names (left of "=") from a .env-style file.
func ParseEnvExampleKeys(content string) []string {
	var keys []string
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if idx := strings.Index(line, "="); idx > 0 {
			keys = append(keys, strings.TrimSpace(line[:idx]))
		}
	}
	return keys
}
