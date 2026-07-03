package analyzer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// fileSet indexes a manifest by relative path for O(1) existence checks.
type fileSet map[string]bool

func newFileSet(m FileManifest) fileSet {
	set := make(fileSet, len(m.Files))
	for _, f := range m.Files {
		set[f.Path] = true
	}
	return set
}

func (s fileSet) has(name string) bool { return s[name] }

func (s fileSet) hasAny(names ...string) bool {
	for _, n := range names {
		if s[n] {
			return true
		}
	}
	return false
}

func (s fileSet) hasSuffix(suffix string) bool {
	for path := range s {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

func (s fileSet) hasMatch(re *regexp.Regexp) bool {
	for path := range s {
		if re.MatchString(path) {
			return true
		}
	}
	return false
}

func readRoot(root, relPath string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath)))
	if err != nil {
		return "", false
	}
	return string(data), true
}

var testFilePattern = regexp.MustCompile(`(?i)(_test\.go$|\.test\.[jt]sx?$|\.spec\.[jt]sx?$|test_.*\.py$|.*_test\.py$|_spec\.rb$)`)
var goVersionPattern = regexp.MustCompile(`(?m)^go\s+(\d+\.\d+)`)
var ciConfigPattern = regexp.MustCompile(`^\.github/workflows/.+\.ya?ml$`)

// DetectStack inspects the manifest (and, for the winning language, reads a
// handful of root manifest files) to determine language, framework,
// databases, and basic project conventions.
func DetectStack(manifest FileManifest) StackInfo {
	set := newFileSet(manifest)
	info := StackInfo{
		HasDocker:        set.has("Dockerfile"),
		HasDockerCompose: set.hasAny("docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"),
		HasGitignore:     set.has(".gitignore"),
		HasEnvExample:    set.hasAny(".env.example", ".env.sample", ".env.dist"),
		HasCIConfig:      set.hasMatch(ciConfigPattern) || set.has(".gitlab-ci.yml"),
		HasTests:         set.hasMatch(testFilePattern),
	}

	switch {
	case set.has("go.mod"):
		detectGo(manifest.Root, set, &info)
	case set.has("package.json"):
		detectNode(manifest.Root, set, &info)
	case set.hasAny("requirements.txt", "pyproject.toml", "Pipfile"):
		detectPython(manifest.Root, set, &info)
	case set.has("Gemfile"):
		detectRuby(manifest.Root, set, &info)
	case set.has("Cargo.toml"):
		detectRust(manifest.Root, set, &info)
	case set.hasAny("pom.xml", "build.gradle", "build.gradle.kts"):
		detectJava(set, &info)
	case set.has("composer.json"):
		detectPHP(manifest.Root, set, &info)
	case set.hasSuffix(".csproj"):
		info.Language = ".NET"
		info.HasLockFile = set.hasSuffix(".sln")
	default:
		info.Language = "Unknown"
	}

	return info
}

func detectGo(root string, set fileSet, info *StackInfo) {
	info.Language = "Go"
	info.HasLockFile = set.has("go.sum")
	info.EntryPoint = firstExisting(set, "main.go", "cmd/server/main.go", "cmd/api/main.go")

	if content, ok := readRoot(root, "go.mod"); ok {
		if m := goVersionPattern.FindStringSubmatch(content); len(m) == 2 {
			info.LanguageVersion = m[1]
		}
		lower := strings.ToLower(content)
		info.Framework = firstMatch(lower, map[string]string{
			"go-chi/chi":    "Chi",
			"gin-gonic/gin": "Gin",
			"labstack/echo": "Echo",
			"gofiber/fiber": "Fiber",
			"gorilla/mux":   "Gorilla Mux",
			"beego":         "Beego",
		})
		info.Databases = detectDatabases(lower, map[string]string{
			"jackc/pgx":           "PostgreSQL",
			"lib/pq":              "PostgreSQL",
			"go-redis":            "Redis",
			"redis/go-redis":      "Redis",
			"mongo-driver":        "MongoDB",
			"go-sql-driver/mysql": "MySQL",
			"mattn/go-sqlite3":    "SQLite",
			"gorm.io":             "GORM (ORM)",
		})
	}
}

func detectNode(root string, set fileSet, info *StackInfo) {
	info.Language = "Node.js"
	info.HasLockFile = set.hasAny("package-lock.json", "yarn.lock", "pnpm-lock.yaml")
	info.EntryPoint = firstExisting(set, "index.js", "server.js", "app.js", "src/index.ts", "src/index.js")

	content, ok := readRoot(root, "package.json")
	if !ok {
		return
	}
	var pkg struct {
		Engines         map[string]string `json:"engines"`
		Main            string            `json:"main"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal([]byte(content), &pkg) != nil {
		return
	}
	if pkg.Main != "" {
		info.EntryPoint = pkg.Main
	}
	if v, ok := pkg.Engines["node"]; ok {
		info.LanguageVersion = v
	}

	deps := make(map[string]bool, len(pkg.Dependencies)+len(pkg.DevDependencies))
	for name := range pkg.Dependencies {
		deps[name] = true
	}
	for name := range pkg.DevDependencies {
		deps[name] = true
	}

	info.Framework = firstDep(deps, map[string]string{
		"next":         "Next.js",
		"express":      "Express",
		"fastify":      "Fastify",
		"koa":          "Koa",
		"@nestjs/core": "NestJS",
		"hapi":         "Hapi",
	})
	info.Databases = detectDatabasesFromDeps(deps, map[string]string{
		"pg":        "PostgreSQL",
		"mysql2":    "MySQL",
		"mysql":     "MySQL",
		"mongoose":  "MongoDB",
		"mongodb":   "MongoDB",
		"ioredis":   "Redis",
		"redis":     "Redis",
		"sqlite3":   "SQLite",
		"prisma":    "Prisma (ORM)",
		"sequelize": "Sequelize (ORM)",
		"typeorm":   "TypeORM",
	})
}

func detectPython(root string, set fileSet, info *StackInfo) {
	info.Language = "Python"
	info.HasLockFile = set.hasAny("poetry.lock", "Pipfile.lock", "requirements.txt")
	info.EntryPoint = firstExisting(set, "main.py", "app.py", "manage.py", "wsgi.py", "asgi.py")

	var blob strings.Builder
	for _, name := range []string{"requirements.txt", "pyproject.toml", "Pipfile"} {
		if content, ok := readRoot(root, name); ok {
			blob.WriteString(strings.ToLower(content))
			blob.WriteString("\n")
		}
	}
	content := blob.String()

	info.Framework = firstMatch(content, map[string]string{
		"fastapi": "FastAPI",
		"django":  "Django",
		"flask":   "Flask",
		"tornado": "Tornado",
	})
	info.Databases = detectDatabases(content, map[string]string{
		"psycopg":    "PostgreSQL",
		"asyncpg":    "PostgreSQL",
		"pymongo":    "MongoDB",
		"redis":      "Redis",
		"pymysql":    "MySQL",
		"sqlalchemy": "SQLAlchemy (ORM)",
	})
}

func detectRuby(root string, set fileSet, info *StackInfo) {
	info.Language = "Ruby"
	info.HasLockFile = set.has("Gemfile.lock")
	info.EntryPoint = firstExisting(set, "config.ru", "app.rb")

	if content, ok := readRoot(root, "Gemfile"); ok {
		lower := strings.ToLower(content)
		info.Framework = firstMatch(lower, map[string]string{
			"rails":   "Rails",
			"sinatra": "Sinatra",
		})
		info.Databases = detectDatabases(lower, map[string]string{
			"pg":      "PostgreSQL",
			"mysql2":  "MySQL",
			"redis":   "Redis",
			"mongoid": "MongoDB",
		})
	}
}

func detectRust(root string, set fileSet, info *StackInfo) {
	info.Language = "Rust"
	info.HasLockFile = set.has("Cargo.lock")
	info.EntryPoint = firstExisting(set, "src/main.rs")

	if content, ok := readRoot(root, "Cargo.toml"); ok {
		lower := strings.ToLower(content)
		info.Framework = firstMatch(lower, map[string]string{
			"actix-web": "Actix Web",
			"axum":      "Axum",
			"rocket":    "Rocket",
			"warp":      "Warp",
		})
		info.Databases = detectDatabases(lower, map[string]string{
			"sqlx":    "SQL (sqlx)",
			"diesel":  "SQL (Diesel)",
			"redis":   "Redis",
			"mongodb": "MongoDB",
		})
	}
}

func detectJava(set fileSet, info *StackInfo) {
	info.Language = "Java/Kotlin"
	info.HasLockFile = set.hasAny("pom.xml", "build.gradle", "build.gradle.kts")
	if set.has("pom.xml") {
		info.Framework = "Maven"
	} else {
		info.Framework = "Gradle"
	}
}

func detectPHP(root string, set fileSet, info *StackInfo) {
	info.Language = "PHP"
	info.HasLockFile = set.has("composer.lock")

	if content, ok := readRoot(root, "composer.json"); ok {
		lower := strings.ToLower(content)
		info.Framework = firstMatch(lower, map[string]string{
			"laravel/framework": "Laravel",
			"symfony/symfony":   "Symfony",
		})
	}
}

func firstExisting(set fileSet, candidates ...string) string {
	for _, c := range candidates {
		if set.has(c) {
			return c
		}
	}
	return ""
}

func firstMatch(haystack string, patterns map[string]string) string {
	for needle, label := range patterns {
		if strings.Contains(haystack, needle) {
			return label
		}
	}
	return ""
}

func firstDep(deps map[string]bool, patterns map[string]string) string {
	for needle, label := range patterns {
		if deps[needle] {
			return label
		}
	}
	return ""
}

func detectDatabases(haystack string, patterns map[string]string) []string {
	found := map[string]bool{}
	var out []string
	for needle, label := range patterns {
		if strings.Contains(haystack, needle) && !found[label] {
			found[label] = true
			out = append(out, label)
		}
	}
	return out
}

func detectDatabasesFromDeps(deps map[string]bool, patterns map[string]string) []string {
	found := map[string]bool{}
	var out []string
	for needle, label := range patterns {
		if deps[needle] && !found[label] {
			found[label] = true
			out = append(out, label)
		}
	}
	return out
}
