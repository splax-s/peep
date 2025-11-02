package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectRuntimeHeuristics(t *testing.T) {
	t.Run("node defaults", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "package.json", `{"name":"app","dependencies":{"react":"18.0.0"}}`)
		runtime := detectRuntime(Request{ProjectType: "frontend"}, dir)
		if runtime != runtimeNode {
			t.Fatalf("expected runtime %q got %q", runtimeNode, runtime)
		}
	})

	t.Run("node backend", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "package.json", `{"name":"api","dependencies":{"express":"4"}}`)
		runtime := detectRuntime(Request{ProjectType: "backend"}, dir)
		if runtime != runtimeNode {
			t.Fatalf("expected runtime %q got %q", runtimeNode, runtime)
		}
	})

	t.Run("next detected", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "package.json", `{"name":"nextapp","dependencies":{"next":"14.0.0"}}`)
		runtime := detectRuntime(Request{ProjectType: "frontend"}, dir)
		if runtime != runtimeNext {
			t.Fatalf("expected runtime %q got %q", runtimeNext, runtime)
		}
	})

	t.Run("go backend", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "go.mod", "module example.com/app\n")
		runtime := detectRuntime(Request{ProjectType: "backend"}, dir)
		if runtime != runtimeGo {
			t.Fatalf("expected runtime %q got %q", runtimeGo, runtime)
		}
	})

	t.Run("java project", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "pom.xml", "<project></project>")
		runtime := detectRuntime(Request{}, dir)
		if runtime != runtimeJava {
			t.Fatalf("expected runtime %q got %q", runtimeJava, runtime)
		}
	})

	t.Run("ruby project", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "Gemfile", "source 'https://rubygems.org'\n")
		runtime := detectRuntime(Request{}, dir)
		if runtime != runtimeRuby {
			t.Fatalf("expected runtime %q got %q", runtimeRuby, runtime)
		}
	})
}

func TestDetectNodePackageManager(t *testing.T) {
	t.Run("package manager field", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "package.json", `{"packageManager":"pnpm@8.6.0"}`)
		pm := detectNodePackageManager(dir)
		if pm != nodePMPNPM {
			t.Fatalf("expected pnpm, got %s", pm)
		}
	})

	t.Run("lock files", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "yarn.lock", "\n")
		pm := detectNodePackageManager(dir)
		if pm != nodePMYarn {
			t.Fatalf("expected yarn, got %s", pm)
		}
	})
}

func TestEnsureNodeRuntimeGeneratesArtifacts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"name":"app"}`)

	generated, embedded, pm, err := ensureNodeRuntime(dir, "npm run build", runtimeNode)
	if err != nil {
		t.Fatalf("ensureNodeRuntime error: %v", err)
	}
	if !generated {
		t.Fatalf("expected dockerfile generation")
	}
	if !embedded {
		t.Fatalf("expected build script embedding")
	}
	if pm != nodePMNPM {
		t.Fatalf("expected npm package manager, got %s", pm)
	}

	dockerfile := readFile(t, filepath.Join(dir, "Dockerfile"))
	if !strings.Contains(dockerfile, "FROM node:20-bullseye") {
		t.Fatalf("expected node base image in dockerfile: %s", dockerfile)
	}
	if !strings.Contains(dockerfile, runtimeBuildScriptPath) {
		t.Fatalf("expected build script reference in dockerfile")
	}

	script := readFile(t, filepath.Join(dir, runtimeBuildScriptPath))
	if !strings.Contains(script, "npm run build") {
		t.Fatalf("expected build command in embedded script")
	}
}

func TestEnsureNodeRuntimeHonorsExistingDockerfile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"name":"app"}`)
	writeFile(t, dir, "Dockerfile", "FROM node:18")

	generated, embedded, pm, err := ensureNodeRuntime(dir, "npm run build", runtimeNode)
	if err != nil {
		t.Fatalf("ensureNodeRuntime error: %v", err)
	}
	if generated || embedded {
		t.Fatalf("expected existing dockerfile to be preserved")
	}
	if pm != nodePMNPM {
		t.Fatalf("expected npm package manager, got %s", pm)
	}
	if fileExists(filepath.Join(dir, runtimeBuildScriptPath)) {
		t.Fatalf("unexpected build script creation when dockerfile exists")
	}
}

func TestEnsureGoRuntimeGeneratesDockerfile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/app\n")

	generated, embedded, err := ensureGoRuntime(dir, "go generate ./...")
	if err != nil {
		t.Fatalf("ensureGoRuntime error: %v", err)
	}
	if !generated || !embedded {
		t.Fatalf("expected dockerfile and script generation for go runtime")
	}

	dockerfile := readFile(t, filepath.Join(dir, "Dockerfile"))
	if !strings.Contains(dockerfile, "FROM golang:1.24") {
		t.Fatalf("expected golang base image in dockerfile: %s", dockerfile)
	}
	if !strings.Contains(dockerfile, "CGO_ENABLED=0") {
		t.Fatalf("expected go build command in dockerfile")
	}

	script := readFile(t, filepath.Join(dir, runtimeBuildScriptPath))
	if !strings.Contains(script, "go generate ./...") {
		t.Fatalf("expected go build command in script")
	}
}

func TestEnsureJavaRuntimeMaven(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pom.xml", "<project></project>")

	generated, embedded, tool, err := ensureJavaRuntime(dir, "mvn -B verify")
	if err != nil {
		t.Fatalf("ensureJavaRuntime error: %v", err)
	}
	if !generated || !embedded {
		t.Fatalf("expected dockerfile and script generation for java runtime")
	}
	if tool != javaBuildToolMaven {
		t.Fatalf("expected maven build tool, got %s", tool)
	}

	dockerfile := readFile(t, filepath.Join(dir, "Dockerfile"))
	if !strings.Contains(dockerfile, "FROM maven:3.9") {
		t.Fatalf("expected maven base image in dockerfile: %s", dockerfile)
	}
	if !strings.Contains(dockerfile, "app.jar") {
		t.Fatalf("expected jar packaging step in dockerfile")
	}

	script := readFile(t, filepath.Join(dir, runtimeBuildScriptPath))
	if !strings.Contains(script, "mvn -B verify") {
		t.Fatalf("expected java build command in script")
	}
}

func TestEnsureJavaRuntimeGradle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "build.gradle", "tasks.register('build') { }")

	generated, embedded, tool, err := ensureJavaRuntime(dir, "gradle test")
	if err != nil {
		t.Fatalf("ensureJavaRuntime error: %v", err)
	}
	if !generated || !embedded {
		t.Fatalf("expected dockerfile and script generation for gradle runtime")
	}
	if tool != javaBuildToolGradle {
		t.Fatalf("expected gradle build tool, got %s", tool)
	}

	dockerfile := readFile(t, filepath.Join(dir, "Dockerfile"))
	if !strings.Contains(dockerfile, "FROM gradle:8.10-jdk21") {
		t.Fatalf("expected gradle base image in dockerfile: %s", dockerfile)
	}
}

func TestEnsureRubyRuntimeGeneratesDockerfile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Gemfile", "source 'https://rubygems.org'")

	generated, embedded, err := ensureRubyRuntime(dir, "bundle exec rake assets:precompile")
	if err != nil {
		t.Fatalf("ensureRubyRuntime error: %v", err)
	}
	if !generated || !embedded {
		t.Fatalf("expected dockerfile and script generation for ruby runtime")
	}

	dockerfile := readFile(t, filepath.Join(dir, "Dockerfile"))
	if !strings.Contains(dockerfile, "FROM ruby:3.3") {
		t.Fatalf("expected ruby base image in dockerfile: %s", dockerfile)
	}
	if !strings.Contains(dockerfile, "bundle install") {
		t.Fatalf("expected bundler install command in dockerfile")
	}

	script := readFile(t, filepath.Join(dir, runtimeBuildScriptPath))
	if !strings.Contains(script, "bundle exec rake assets:precompile") {
		t.Fatalf("expected ruby build command in script")
	}
}

func writeFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write file %s: %v", name, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file %s: %v", path, err)
	}
	return string(data)
}
