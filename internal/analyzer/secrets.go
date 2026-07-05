package analyzer

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type secretPattern struct {
	Name     string
	Pattern  *regexp.Regexp
	Severity string
}

// secretPatterns are regex signatures for commonly-leaked credentials.
// Patterns intentionally err toward the specific (fewer false positives)
// over exhaustive coverage.
var secretPatterns = []secretPattern{
	{"Anthropic API Key", regexp.MustCompile(`sk-ant-[a-zA-Z0-9\-_]{40,}`), "critical"},
	{"OpenAI API Key", regexp.MustCompile(`sk-[a-zA-Z0-9]{48}`), "critical"},
	{"GitHub Token", regexp.MustCompile(`ghp_[a-zA-Z0-9]{36}`), "critical"},
	{"GitHub OAuth Token", regexp.MustCompile(`gho_[a-zA-Z0-9]{36}`), "critical"},
	{"AWS Access Key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`), "critical"},
	{"Postgres URL with credentials", regexp.MustCompile(`postgres://[^:\s]+:[^@\s]+@`), "high"},
	{"MySQL URL with credentials", regexp.MustCompile(`mysql://[^:\s]+:[^@\s]+@`), "high"},
	{"Redis URL with password", regexp.MustCompile(`redis://:[^@\s]+@`), "high"},
	{"Private Key", regexp.MustCompile(`-----BEGIN (RSA |EC )?PRIVATE KEY`), "critical"},
	{"Bearer Token", regexp.MustCompile(`Bearer [a-zA-Z0-9\-_.]{32,}`), "medium"},
	{"Basic Auth Header", regexp.MustCompile(`Authorization: Basic [a-zA-Z0-9+/=]{20,}`), "medium"},
	{"Hardcoded JWT Secret", regexp.MustCompile(`(?i)jwt[_-]?secret\s*[=:]\s*["'][^"']{8,}`), "high"},
	{"Hardcoded Password", regexp.MustCompile(`(?i)password\s*[=:]\s*["'][^"']{6,}["']`), "high"},
}

// secretSkipSuffixes are file suffixes too noisy to be worth scanning
// (directories like node_modules/vendor/.git are already excluded at walk
// time, see walker.go's excludedDirs).
var secretSkipSuffixes = []string{".min.js", ".map", ".lock"}

var binaryExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".ico": true, ".webp": true,
	".woff": true, ".woff2": true, ".ttf": true, ".eot": true,
	".zip": true, ".tar": true, ".gz": true, ".pdf": true, ".exe": true, ".bin": true,
	".so": true, ".dylib": true, ".dll": true, ".wasm": true,
}

const secretScanFileCap = 3000

// ScanSecrets regex-scans manifest files for hardcoded credentials and
// flags a committed .env not covered by .gitignore. Matched secret values
// are never stored — findings carry a masked excerpt only. onProgress, if
// non-nil, is invoked periodically (see progressInterval) with
// (done, total) as files are scanned; pass nil where progress doesn't
// matter.
func ScanSecrets(manifest FileManifest, dir string, onProgress func(done, total int)) []SecretFinding {
	var findings []SecretFinding
	scanned := 0
	total := len(manifest.Files)
	interval := progressInterval(total)

	report := func(done int) {
		if onProgress != nil && (done == total || done%interval == 0) {
			onProgress(done, total)
		}
	}

	for i, f := range manifest.Files {
		if scanned >= secretScanFileCap {
			report(total)
			break
		}
		if binaryExts[f.Ext] || f.Size == 0 || f.Size > maxWalkFileBytes || hasAnySuffix(f.Path, secretSkipSuffixes) {
			report(i + 1)
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if err != nil {
			report(i + 1)
			continue
		}
		scanned++

		findings = append(findings, scanFileForSecrets(f.Path, data)...)
		report(i + 1)
	}

	if set := newFileSet(manifest); set.has(".env") {
		covered := false
		if content, ok := readRoot(dir, ".gitignore"); ok {
			covered = gitignoreCoversEnv(content)
		}
		if !covered {
			findings = append(findings, SecretFinding{
				Name:     "Committed .env file",
				Severity: "critical",
				File:     ".env",
				Line:     0,
				Excerpt:  ".env is committed to the repository and not covered by .gitignore",
			})
		}
	}

	return findings
}

func scanFileForSecrets(path string, data []byte) []SecretFinding {
	var findings []SecretFinding
	lineNum := 0
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		for _, p := range secretPatterns {
			loc := p.Pattern.FindStringIndex(line)
			if loc == nil {
				continue
			}
			findings = append(findings, SecretFinding{
				Name:     p.Name,
				Severity: p.Severity,
				File:     path,
				Line:     lineNum,
				Excerpt:  redact(line, loc[0], loc[1]),
			})
		}
	}

	return findings
}

// redact returns the line with the matched span replaced by "[REDACTED]",
// truncated to a safe length, so raw secret values are never persisted.
func redact(line string, start, end int) string {
	redacted := line[:start] + "[REDACTED]" + line[end:]
	redacted = strings.TrimSpace(redacted)
	const maxLen = 120
	if len(redacted) > maxLen {
		redacted = redacted[:maxLen] + "..."
	}
	return redacted
}

func hasAnySuffix(path string, suffixes []string) bool {
	for _, s := range suffixes {
		if strings.HasSuffix(path, s) {
			return true
		}
	}
	return false
}
