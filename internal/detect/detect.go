// Package detect identifies the framework of a local project directory.
package detect

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Framework is the kind of project Shipyard knows how to describe.
type Framework string

const (
	FrameworkNextJS  Framework = "nextjs"
	FrameworkVite    Framework = "vite"
	FrameworkPython  Framework = "python"
	FrameworkNode    Framework = "node"
	FrameworkUnknown Framework = "unknown"
)

// Result is what Detect found, including the files that decided it.
type Result struct {
	Framework Framework
	Signals   []string
}

type packageJSON struct {
	Dependencies    map[string]any `json:"dependencies"`
	DevDependencies map[string]any `json:"devDependencies"`
}

// Detect inspects dir. Priority is Next.js, then Vite, then Python, then
// a generic Node project. A directory with both a Python project file and a
// generic package.json is reported as Python.
func Detect(dir string) (Result, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return Result{}, fmt.Errorf("detect: %w", err)
	}
	if !info.IsDir() {
		return Result{}, fmt.Errorf("detect: %s is not a directory", dir)
	}

	var signals []string
	pkg, pkgErr := readPackageJSON(filepath.Join(dir, "package.json"))
	if pkgErr == nil {
		signals = append(signals, "package.json")
	} else if !errors.Is(pkgErr, os.ErrNotExist) {
		return Result{}, pkgErr
	}

	nextConfig := existing(dir, "next.config.js", "next.config.mjs", "next.config.ts", "next.config.cjs")
	if len(nextConfig) > 0 || hasDep(pkg, "next") {
		found := append([]string{}, nextConfig...)
		if hasDep(pkg, "next") {
			found = append(found, "package.json dependency next")
		}
		return Result{Framework: FrameworkNextJS, Signals: found}, nil
	}

	viteConfig := existing(dir, "vite.config.js", "vite.config.mjs", "vite.config.ts", "vite.config.cjs")
	if len(viteConfig) > 0 || hasDep(pkg, "vite") {
		found := append([]string{}, viteConfig...)
		if hasDep(pkg, "vite") {
			found = append(found, "package.json dependency vite")
		}
		return Result{Framework: FrameworkVite, Signals: found}, nil
	}

	// setup.py and manage.py are picked up with the other root-level Python files.
	pythonFiles := existing(dir, "pyproject.toml", "requirements.txt", "Pipfile")
	pythonFiles = append(pythonFiles, rootPythonFiles(dir)...)
	if len(pythonFiles) > 0 {
		return Result{Framework: FrameworkPython, Signals: pythonFiles}, nil
	}

	if pkgErr == nil {
		return Result{Framework: FrameworkNode, Signals: signals}, nil
	}
	return Result{Framework: FrameworkUnknown, Signals: nil}, nil
}

func readPackageJSON(path string) (packageJSON, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return packageJSON{}, err
	}
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return packageJSON{}, fmt.Errorf("detect: parse %s: %w", path, err)
	}
	return pkg, nil
}

func hasDep(pkg packageJSON, name string) bool {
	if pkg.Dependencies == nil && pkg.DevDependencies == nil {
		return false
	}
	if _, ok := pkg.Dependencies[name]; ok {
		return true
	}
	_, ok := pkg.DevDependencies[name]
	return ok
}

func existing(dir string, names ...string) []string {
	var found []string
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			found = append(found, name)
		}
	}
	return found
}

func rootPythonFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var found []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".py") {
			found = append(found, entry.Name())
		}
	}
	return found
}
