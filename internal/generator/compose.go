package generator

import (
	"fmt"
	"strings"

	"deployable/internal/analyzer"
)

type composeDB struct {
	service string
	block   string
	volume  string
}

var composeDBs = map[string]composeDB{
	"PostgreSQL": {
		service: "postgres",
		volume:  "pgdata",
		block: `  postgres:
    image: postgres:16-alpine
    restart: unless-stopped
    environment:
      POSTGRES_DB: ${POSTGRES_DB}
      POSTGRES_USER: ${POSTGRES_USER}
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${POSTGRES_USER}"]
      interval: 10s
      timeout: 5s
      retries: 5`,
	},
	"Redis": {
		service: "redis",
		volume:  "redisdata",
		block: `  redis:
    image: redis:7-alpine
    restart: unless-stopped
    command: redis-server --appendonly yes
    volumes:
      - redisdata:/data
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 10s
      timeout: 5s
      retries: 5`,
	},
	"MongoDB": {
		service: "mongo",
		volume:  "mongodata",
		block: `  mongo:
    image: mongo:7
    restart: unless-stopped
    volumes:
      - mongodata:/data/db`,
	},
	"MySQL": {
		service: "mysql",
		volume:  "mysqldata",
		block: `  mysql:
    image: mysql:8
    restart: unless-stopped
    environment:
      MYSQL_DATABASE: ${MYSQL_DATABASE}
      MYSQL_USER: ${MYSQL_USER}
      MYSQL_PASSWORD: ${MYSQL_PASSWORD}
      MYSQL_ROOT_PASSWORD: ${MYSQL_ROOT_PASSWORD}
    volumes:
      - mysqldata:/var/lib/mysql
    healthcheck:
      test: ["CMD", "mysqladmin", "ping", "-h", "localhost"]
      interval: 10s
      timeout: 5s
      retries: 5`,
	},
}

// GenerateCompose returns a docker-compose.yml with an app service plus one
// service (with its own volume and healthcheck-gated depends_on) per
// detected database.
func GenerateCompose(result *analyzer.Result) string {
	port := result.StackInfo.AppPort
	if port == "" {
		port = "8080"
	}

	var blocks, dependsOn, volumes []string
	seen := map[string]bool{}
	for _, name := range result.StackInfo.Databases {
		db, ok := composeDBs[name]
		if !ok || seen[db.service] {
			continue
		}
		seen[db.service] = true
		blocks = append(blocks, db.block)
		dependsOn = append(dependsOn, db.service)
		volumes = append(volumes, db.volume)
	}

	var b strings.Builder
	b.WriteString("services:\n  app:\n    build: .\n    restart: unless-stopped\n    env_file: .env\n    ports:\n")
	fmt.Fprintf(&b, "      - \"%s:%s\"\n", port, port)

	if len(dependsOn) > 0 {
		b.WriteString("    depends_on:\n")
		for _, dep := range dependsOn {
			fmt.Fprintf(&b, "      %s:\n        condition: service_healthy\n", dep)
		}
	}

	if len(blocks) > 0 {
		b.WriteString("\n")
		b.WriteString(strings.Join(blocks, "\n\n"))
		b.WriteString("\n")
	}

	if len(volumes) > 0 {
		b.WriteString("\nvolumes:\n")
		for _, v := range volumes {
			fmt.Fprintf(&b, "  %s:\n", v)
		}
	}

	return b.String()
}
