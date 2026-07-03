package generator

import (
	"fmt"

	"deployable/internal/analyzer"
)

// GeneratePlatformConfig returns platform-specific config for whichever
// platform Claude ranked #1, plus the filename it should be saved as.
// Returns ("", "") when there's no semantic report to rank from, or we don't
// have a template for that platform yet.
func GeneratePlatformConfig(result *analyzer.Result) (filename, content string) {
	if result.Semantic == nil || len(result.Semantic.Platforms) == 0 {
		return "", ""
	}

	port := result.StackInfo.AppPort
	if port == "" {
		port = "8080"
	}

	switch result.Semantic.Platforms[0].Name {
	case "Fly.io":
		return "fly.toml", fmt.Sprintf(`app = "your-app-name"
primary_region = "iad"

[build]

[http_service]
  internal_port = %s
  force_https = true
  auto_stop_machines = true
  auto_start_machines = true
  min_machines_running = 0

[[vm]]
  cpu_kind = "shared"
  cpus = 1
  memory_mb = 512
`, port)
	case "Render":
		return "render.yaml", fmt.Sprintf(`services:
  - type: web
    name: your-app-name
    env: docker
    plan: starter
    healthCheckPath: /health
    envVars:
      - key: PORT
        value: "%s"
`, port)
	default:
		return "", ""
	}
}
