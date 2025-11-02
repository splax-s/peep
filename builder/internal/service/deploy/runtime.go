package deploy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	runtimeGeneric = "generic"
	runtimeNode    = "node"
	runtimeNext    = "next"
	runtimeGo      = "go"
	runtimeJava    = "java"
	runtimeRuby    = "ruby"

	runtimeBuildScriptPath = ".peep/peep-build.sh"
)

type runtimePreparation struct {
	Name                string
	SkipHostBuild       bool
	DockerfileGenerated bool
	BuildScriptEmbedded bool
	PackageManager      string
	BuildTool           string
}

type nodePackageManager string

const (
	nodePMNPM  nodePackageManager = "npm"
	nodePMYarn nodePackageManager = "yarn"
	nodePMPNPM nodePackageManager = "pnpm"
)

func (pm nodePackageManager) String() string {
	if pm == "" {
		return string(nodePMNPM)
	}
	return string(pm)
}

type javaBuildTool string

const (
	javaBuildToolMaven  javaBuildTool = "maven"
	javaBuildToolGradle javaBuildTool = "gradle"
)

func (t javaBuildTool) String() string {
	if t == "" {
		return string(javaBuildToolMaven)
	}
	return string(t)
}

type npmManifest struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	PackageManager  string            `json:"packageManager"`
	Scripts         map[string]string `json:"scripts"`
}

func (m *npmManifest) hasDependency(name string) bool {
	if m == nil {
		return false
	}
	target := strings.ToLower(strings.TrimSpace(name))
	if target == "" {
		return false
	}
	for dep := range m.Dependencies {
		if strings.EqualFold(dep, target) {
			return true
		}
	}
	for dep := range m.DevDependencies {
		if strings.EqualFold(dep, target) {
			return true
		}
	}
	return false
}

func (s Service) prepareRuntime(req Request, workdir string) (runtimePreparation, error) {
	runtime := detectRuntime(req, workdir)
	switch runtime {
	case runtimeNode:
		generated, embedded, pm, err := ensureNodeRuntime(workdir, strings.TrimSpace(req.BuildCommand), runtimeNode)
		if err != nil {
			return runtimePreparation{}, err
		}
		return runtimePreparation{
			Name:                runtimeNode,
			SkipHostBuild:       generated,
			DockerfileGenerated: generated,
			BuildScriptEmbedded: embedded,
			PackageManager:      pm.String(),
			BuildTool:           pm.String(),
		}, nil
	case runtimeNext:
		generated, embedded, pm, err := ensureNodeRuntime(workdir, strings.TrimSpace(req.BuildCommand), runtimeNext)
		if err != nil {
			return runtimePreparation{}, err
		}
		return runtimePreparation{
			Name:                runtimeNext,
			SkipHostBuild:       generated,
			DockerfileGenerated: generated,
			BuildScriptEmbedded: embedded,
			PackageManager:      pm.String(),
			BuildTool:           "next",
		}, nil
	case runtimeGo:
		generated, embedded, err := ensureGoRuntime(workdir, strings.TrimSpace(req.BuildCommand))
		if err != nil {
			return runtimePreparation{}, err
		}
		return runtimePreparation{
			Name:                runtimeGo,
			SkipHostBuild:       generated,
			DockerfileGenerated: generated,
			BuildScriptEmbedded: embedded,
			BuildTool:           "go",
		}, nil
	case runtimeJava:
		generated, embedded, tool, err := ensureJavaRuntime(workdir, strings.TrimSpace(req.BuildCommand))
		if err != nil {
			return runtimePreparation{}, err
		}
		return runtimePreparation{
			Name:                runtimeJava,
			SkipHostBuild:       generated,
			DockerfileGenerated: generated,
			BuildScriptEmbedded: embedded,
			BuildTool:           tool.String(),
		}, nil
	case runtimeRuby:
		generated, embedded, err := ensureRubyRuntime(workdir, strings.TrimSpace(req.BuildCommand))
		if err != nil {
			return runtimePreparation{}, err
		}
		return runtimePreparation{
			Name:                runtimeRuby,
			SkipHostBuild:       generated,
			DockerfileGenerated: generated,
			BuildScriptEmbedded: embedded,
			BuildTool:           "bundler",
		}, nil
	default:
		return runtimePreparation{Name: runtimeGeneric}, nil
	}
}

func detectRuntime(req Request, workdir string) string {
	manifest, manifestOK := loadPackageManifest(workdir)
	typ := strings.ToLower(strings.TrimSpace(req.ProjectType))

	switch typ {
	case "frontend":
		if manifestOK {
			if isNextManifest(manifest) {
				return runtimeNext
			}
			return runtimeNode
		}
	case "backend":
		if fileExists(filepath.Join(workdir, "go.mod")) {
			return runtimeGo
		}
		if isJavaProject(workdir) {
			return runtimeJava
		}
		if fileExists(filepath.Join(workdir, "Gemfile")) {
			return runtimeRuby
		}
		if manifestOK {
			if isNextManifest(manifest) {
				return runtimeNext
			}
			return runtimeNode
		}
	}

	if manifestOK {
		if isNextManifest(manifest) {
			return runtimeNext
		}
		return runtimeNode
	}
	if fileExists(filepath.Join(workdir, "go.mod")) {
		return runtimeGo
	}
	if isJavaProject(workdir) {
		return runtimeJava
	}
	if fileExists(filepath.Join(workdir, "Gemfile")) {
		return runtimeRuby
	}
	return runtimeGeneric
}

func ensureNodeRuntime(workdir, buildCommand, flavor string) (bool, bool, nodePackageManager, error) {
	hasDockerfile, err := hasDockerfile(workdir)
	if err != nil {
		return false, false, nodePMNPM, err
	}
	pm := detectNodePackageManager(workdir)
	includeScript := strings.TrimSpace(buildCommand) != ""
	if hasDockerfile {
		return false, false, pm, nil
	}
	if includeScript {
		if err := writeRuntimeBuildScript(workdir, buildCommand); err != nil {
			return false, false, pm, err
		}
	}
	content := renderNodeDockerfile(flavor, pm, includeScript)
	dockerfilePath := filepath.Join(workdir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte(content), 0o644); err != nil {
		return false, includeScript, pm, fmt.Errorf("write dockerfile: %w", err)
	}
	return true, includeScript, pm, nil
}

func renderNodeDockerfile(flavor string, pm nodePackageManager, includeScript bool) string {
	var b strings.Builder
	b.WriteString("# syntax=docker/dockerfile:1\n")
	b.WriteString("FROM node:20-bullseye AS base\n")
	b.WriteString("WORKDIR /app\n\n")
	switch pm {
	case nodePMYarn:
		b.WriteString("COPY package.json ./\n")
		b.WriteString("COPY yarn.lock ./\n")
		b.WriteString("RUN corepack enable && yarn install --frozen-lockfile\n\n")
	case nodePMPNPM:
		b.WriteString("COPY package.json ./\n")
		b.WriteString("COPY pnpm-lock.yaml ./\n")
		b.WriteString("RUN corepack enable && pnpm install --frozen-lockfile\n\n")
	default:
		b.WriteString("COPY package*.json ./\n")
		b.WriteString("RUN if [ -f package-lock.json ]; then npm ci; elif [ -f npm-shrinkwrap.json ]; then npm ci; else npm install; fi\n\n")
	}
	b.WriteString("COPY . ./\n")
	if includeScript {
		b.WriteString("RUN if [ -f " + runtimeBuildScriptPath + " ]; then \\\n")
		b.WriteString("  chmod +x " + runtimeBuildScriptPath + " && bash " + runtimeBuildScriptPath + " && rm -f " + runtimeBuildScriptPath + "; fi\n")
	}
	b.WriteString("ENV NODE_ENV=production\n")
	if flavor == runtimeNext {
		b.WriteString("ENV NEXT_TELEMETRY_DISABLED=1\n")
	}
	b.WriteString("ENV PORT=3000\n")
	b.WriteString("EXPOSE 3000\n")
	return b.String()
}

func ensureGoRuntime(workdir, buildCommand string) (bool, bool, error) {
	hasDockerfile, err := hasDockerfile(workdir)
	if err != nil {
		return false, false, err
	}
	includeScript := strings.TrimSpace(buildCommand) != ""
	if hasDockerfile {
		return false, false, nil
	}
	if includeScript {
		if err := writeRuntimeBuildScript(workdir, buildCommand); err != nil {
			return false, false, err
		}
	}
	content := renderGoDockerfile(includeScript)
	dockerfilePath := filepath.Join(workdir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte(content), 0o644); err != nil {
		return false, includeScript, fmt.Errorf("write dockerfile: %w", err)
	}
	return true, includeScript, nil
}

func renderGoDockerfile(includeScript bool) string {
	var b strings.Builder
	b.WriteString("# syntax=docker/dockerfile:1\n")
	b.WriteString("FROM golang:1.24 AS builder\n")
	b.WriteString("WORKDIR /src\n\n")
	b.WriteString("COPY go.* ./\n")
	b.WriteString("RUN go mod download\n\n")
	b.WriteString("COPY . ./\n")
	if includeScript {
		b.WriteString("RUN if [ -f " + runtimeBuildScriptPath + " ]; then \\\n")
		b.WriteString("  chmod +x " + runtimeBuildScriptPath + " && bash " + runtimeBuildScriptPath + " && rm -f " + runtimeBuildScriptPath + "; fi\n")
	}
	b.WriteString("RUN CGO_ENABLED=0 GOOS=linux go build -o /out/app ./...\n\n")
	b.WriteString("FROM debian:bookworm-slim\n")
	b.WriteString("WORKDIR /app\n")
	b.WriteString("RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*\n")
	b.WriteString("COPY --from=builder /out/app ./app\n")
	b.WriteString("ENV PORT=3000\n")
	b.WriteString("EXPOSE 3000\n")
	b.WriteString("CMD [\"./app\"]\n")
	return b.String()
}

func ensureJavaRuntime(workdir, buildCommand string) (bool, bool, javaBuildTool, error) {
	hasDockerfile, err := hasDockerfile(workdir)
	if err != nil {
		return false, false, javaBuildToolMaven, err
	}
	tool := detectJavaBuildTool(workdir)
	includeScript := strings.TrimSpace(buildCommand) != ""
	if hasDockerfile {
		return false, false, tool, nil
	}
	if includeScript {
		if err := writeRuntimeBuildScript(workdir, buildCommand); err != nil {
			return false, false, tool, err
		}
	}
	content := renderJavaDockerfile(tool, includeScript)
	dockerfilePath := filepath.Join(workdir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte(content), 0o644); err != nil {
		return false, includeScript, tool, fmt.Errorf("write dockerfile: %w", err)
	}
	return true, includeScript, tool, nil
}

func renderJavaDockerfile(tool javaBuildTool, includeScript bool) string {
	var b strings.Builder
	b.WriteString("# syntax=docker/dockerfile:1\n")
	switch tool {
	case javaBuildToolGradle:
		b.WriteString("FROM gradle:8.10-jdk21 AS builder\n")
		b.WriteString("WORKDIR /workspace\n\n")
		b.WriteString("COPY . ./\n")
		if includeScript {
			b.WriteString("RUN if [ -f " + runtimeBuildScriptPath + " ]; then \\\n")
			b.WriteString("  chmod +x " + runtimeBuildScriptPath + " && bash " + runtimeBuildScriptPath + " && rm -f " + runtimeBuildScriptPath + "; fi\n")
		}
		b.WriteString("RUN gradle clean build -x test --no-daemon\n")
		b.WriteString("RUN JAR=$(find build/libs -name \"*.jar\" -type f | head -n 1) && cp \"$JAR\" /workspace/app.jar\n\n")
	default:
		b.WriteString("FROM maven:3.9-eclipse-temurin-21 AS builder\n")
		b.WriteString("WORKDIR /workspace\n\n")
		b.WriteString("COPY pom.xml ./\n")
		b.WriteString("RUN mvn -B dependency:go-offline\n\n")
		b.WriteString("COPY . ./\n")
		if includeScript {
			b.WriteString("RUN if [ -f " + runtimeBuildScriptPath + " ]; then \\\n")
			b.WriteString("  chmod +x " + runtimeBuildScriptPath + " && bash " + runtimeBuildScriptPath + " && rm -f " + runtimeBuildScriptPath + "; fi\n")
		}
		b.WriteString("RUN mvn -B package -DskipTests\n")
		b.WriteString("RUN JAR=$(ls -1 target/*.jar | head -n 1) && cp \"$JAR\" /workspace/app.jar\n\n")
	}
	b.WriteString("FROM eclipse-temurin:21-jre\n")
	b.WriteString("WORKDIR /app\n")
	b.WriteString("COPY --from=builder /workspace/app.jar /app/app.jar\n")
	b.WriteString("ENV PORT=3000\n")
	b.WriteString("EXPOSE 3000\n")
	b.WriteString("CMD [\"java\",\"-jar\",\"/app/app.jar\"]\n")
	return b.String()
}

func ensureRubyRuntime(workdir, buildCommand string) (bool, bool, error) {
	hasDockerfile, err := hasDockerfile(workdir)
	if err != nil {
		return false, false, err
	}
	includeScript := strings.TrimSpace(buildCommand) != ""
	if hasDockerfile {
		return false, false, nil
	}
	if includeScript {
		if err := writeRuntimeBuildScript(workdir, buildCommand); err != nil {
			return false, false, err
		}
	}
	content := renderRubyDockerfile(includeScript)
	dockerfilePath := filepath.Join(workdir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte(content), 0o644); err != nil {
		return false, includeScript, fmt.Errorf("write dockerfile: %w", err)
	}
	return true, includeScript, nil
}

func renderRubyDockerfile(includeScript bool) string {
	var b strings.Builder
	b.WriteString("# syntax=docker/dockerfile:1\n")
	b.WriteString("FROM ruby:3.3 AS base\n")
	b.WriteString("WORKDIR /app\n")
	b.WriteString("ENV BUNDLE_WITHOUT=development:test\n")
	b.WriteString("COPY Gemfile* ./\n")
	b.WriteString("RUN gem install bundler && bundle install --jobs 4 --retry 3\n\n")
	b.WriteString("COPY . ./\n")
	if includeScript {
		b.WriteString("RUN if [ -f " + runtimeBuildScriptPath + " ]; then \\\n")
		b.WriteString("  chmod +x " + runtimeBuildScriptPath + " && bash " + runtimeBuildScriptPath + " && rm -f " + runtimeBuildScriptPath + "; fi\n")
	}
	b.WriteString("ENV PORT=3000\n")
	b.WriteString("EXPOSE 3000\n")
	b.WriteString("CMD [\"bundle\",\"exec\",\"puma\",\"-b\",\"tcp://0.0.0.0:3000\"]\n")
	return b.String()
}

func writeRuntimeBuildScript(workdir, buildCommand string) error {
	scriptDir := filepath.Join(workdir, filepath.Dir(runtimeBuildScriptPath))
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		return fmt.Errorf("create runtime script dir: %w", err)
	}
	scriptPath := filepath.Join(workdir, runtimeBuildScriptPath)
	script := "#!/usr/bin/env bash\nset -euo pipefail\n\n" + buildCommand + "\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		return fmt.Errorf("write runtime build script: %w", err)
	}
	return nil
}

func hasDockerfile(workdir string) (bool, error) {
	entries, err := os.ReadDir(workdir)
	if err != nil {
		return false, fmt.Errorf("read workspace: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if name == "dockerfile" {
			return true, nil
		}
	}
	return false, nil
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func detectNodePackageManager(workdir string) nodePackageManager {
	if manifest, ok := loadPackageManifest(workdir); ok {
		if parsed := parseNodePackageManager(manifest.PackageManager); parsed != "" {
			return parsed
		}
	}
	switch {
	case fileExists(filepath.Join(workdir, "yarn.lock")):
		return nodePMYarn
	case fileExists(filepath.Join(workdir, "pnpm-lock.yaml")):
		return nodePMPNPM
	default:
		return nodePMNPM
	}
}

func parseNodePackageManager(value string) nodePackageManager {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "" {
		return ""
	}
	if idx := strings.Index(trimmed, "@"); idx > 0 {
		trimmed = trimmed[:idx]
	}
	switch trimmed {
	case "yarn":
		return nodePMYarn
	case "pnpm":
		return nodePMPNPM
	case "npm":
		return nodePMNPM
	default:
		return ""
	}
}

func loadPackageManifest(workdir string) (*npmManifest, bool) {
	path := filepath.Join(workdir, "package.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var manifest npmManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, false
	}
	if manifest.Dependencies == nil {
		manifest.Dependencies = map[string]string{}
	}
	if manifest.DevDependencies == nil {
		manifest.DevDependencies = map[string]string{}
	}
	if manifest.Scripts == nil {
		manifest.Scripts = map[string]string{}
	}
	return &manifest, true
}

func isNextManifest(manifest *npmManifest) bool {
	if manifest == nil {
		return false
	}
	if manifest.hasDependency("next") {
		return true
	}
	for _, script := range manifest.Scripts {
		if strings.Contains(strings.ToLower(script), "next") {
			return true
		}
	}
	return false
}

func isJavaProject(workdir string) bool {
	candidates := []string{
		"pom.xml",
		"build.gradle",
		"build.gradle.kts",
		"settings.gradle",
		"settings.gradle.kts",
		"mvnw",
		"gradlew",
	}
	for _, name := range candidates {
		if fileExists(filepath.Join(workdir, name)) {
			return true
		}
	}
	return false
}

func detectJavaBuildTool(workdir string) javaBuildTool {
	if fileExists(filepath.Join(workdir, "gradlew")) || fileExists(filepath.Join(workdir, "build.gradle")) || fileExists(filepath.Join(workdir, "build.gradle.kts")) {
		return javaBuildToolGradle
	}
	if fileExists(filepath.Join(workdir, "pom.xml")) {
		return javaBuildToolMaven
	}
	return javaBuildToolMaven
}
