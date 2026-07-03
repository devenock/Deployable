package generator

import "deployable/internal/analyzer"

// GenerateFiles produces every deployment file Deployable can generate for
// this analysis result, keyed by filename.
func GenerateFiles(result *analyzer.Result) map[string]string {
	files := map[string]string{
		"Dockerfile":               GenerateDockerfile(result),
		"docker-compose.yml":       GenerateCompose(result),
		".env.example":             GenerateEnvExample(result),
		".github/workflows/ci.yml": GenerateCI(result),
		"DEPLOYMENT.md":            GenerateDeploymentGuide(result),
	}

	if name, content := GeneratePlatformConfig(result); name != "" {
		files[name] = content
	}

	return files
}
