// Package generator produces deployment files (Dockerfile, compose,
// .env.example, CI workflow, platform config, deployment guide) from a
// completed analyzer.Result. It depends on internal/analyzer's types, so
// analyzer must never import generator (that would cycle) — callers run
// generation as a step after the analysis pipeline returns.
package generator

import (
	"fmt"

	"deployable/internal/analyzer"
)

// GenerateDockerfile returns a production-ready multi-stage Dockerfile
// tailored to the detected language, falling back to a generic annotated
// template for languages without a specific one yet.
func GenerateDockerfile(result *analyzer.Result) string {
	port := result.StackInfo.AppPort
	if port == "" {
		port = "8080"
	}

	switch result.StackInfo.Language {
	case "Go":
		return goDockerfile(result.StackInfo.LanguageVersion, port)
	case "Node.js":
		return nodeDockerfile(result.StackInfo.LanguageVersion, port, result.StackInfo.EntryPoint)
	case "Python":
		return pythonDockerfile(result.StackInfo.LanguageVersion, port, result.StackInfo.EntryPoint, result.StackInfo.Framework)
	case "Ruby":
		return rubyDockerfile(result.StackInfo.LanguageVersion, port, result.StackInfo.Framework)
	default:
		return genericDockerfile(port)
	}
}

func goDockerfile(version, port string) string {
	if version == "" {
		version = "1.22"
	}
	return fmt.Sprintf(`# syntax=docker/dockerfile:1
FROM golang:%s-alpine AS builder
RUN apk add --no-cache git ca-certificates
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o server .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/server .
EXPOSE %s
HEALTHCHECK --interval=30s --timeout=10s --start-period=15s --retries=3 \
    CMD wget -qO- http://localhost:%s/health || exit 1
CMD ["./server"]
`, version, port, port)
}

func nodeDockerfile(version, port, entryPoint string) string {
	if version == "" {
		version = "20"
	}
	if entryPoint == "" {
		entryPoint = "index.js"
	}
	return fmt.Sprintf(`# syntax=docker/dockerfile:1
FROM node:%s-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN npm ci --omit=dev
COPY . .

FROM node:%s-alpine
WORKDIR /app
ENV NODE_ENV=production
COPY --from=builder /app .
EXPOSE %s
HEALTHCHECK --interval=30s --timeout=10s --start-period=15s --retries=3 \
    CMD wget -qO- http://localhost:%s/health || exit 1
CMD ["node", "%s"]
`, version, version, port, port, entryPoint)
}

func pythonDockerfile(version, port, entryPoint, framework string) string {
	if version == "" {
		version = "3.12"
	}
	if entryPoint == "" {
		entryPoint = "app.py"
	}
	cmd := fmt.Sprintf(`CMD ["python", "%s"]`, entryPoint)
	note := ""
	switch framework {
	case "FastAPI":
		note = "# For production, prefer: CMD [\"uvicorn\", \"main:app\", \"--host\", \"0.0.0.0\", \"--port\", \"" + port + "\"]\n"
	case "Django":
		note = "# For production, prefer: CMD [\"gunicorn\", \"myproject.wsgi\", \"--bind\", \"0.0.0.0:" + port + "\"]\n"
	case "Flask":
		note = "# For production, prefer: CMD [\"gunicorn\", \"app:app\", \"--bind\", \"0.0.0.0:" + port + "\"]\n"
	}
	return fmt.Sprintf(`# syntax=docker/dockerfile:1
FROM python:%s-slim
WORKDIR /app
COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt
COPY . .
EXPOSE %s
HEALTHCHECK --interval=30s --timeout=10s --start-period=15s --retries=3 \
    CMD python -c "import urllib.request; urllib.request.urlopen('http://localhost:%s/health')" || exit 1
%s%s
`, version, port, port, note, cmd)
}

func rubyDockerfile(version, port, framework string) string {
	if version == "" {
		version = "3.3"
	}
	cmd := `CMD ["ruby", "app.rb"]`
	if framework == "Rails" {
		cmd = fmt.Sprintf(`CMD ["bundle", "exec", "rails", "server", "-b", "0.0.0.0", "-p", "%s"]`, port)
	}
	return fmt.Sprintf(`# syntax=docker/dockerfile:1
FROM ruby:%s-slim
RUN apt-get update -qq && apt-get install -y build-essential && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY Gemfile Gemfile.lock ./
RUN bundle install
COPY . .
EXPOSE %s
HEALTHCHECK --interval=30s --timeout=10s --start-period=15s --retries=3 \
    CMD curl -f http://localhost:%s/health || exit 1
%s
`, version, port, port, cmd)
}

func genericDockerfile(port string) string {
	return fmt.Sprintf(`# Generic Dockerfile — customize for your stack.
# Deployable couldn't match this project to a language-specific template yet.
FROM debian:bookworm-slim
WORKDIR /app
COPY . .
# TODO: install your runtime/dependencies and set the correct start command.
EXPOSE %s
CMD ["echo", "Replace this CMD with your app's start command"]
`, port)
}
