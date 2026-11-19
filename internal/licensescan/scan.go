package licensescan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// LicenseFile is an upstream license or notice file reproduced in the notices.
type LicenseFile struct {
	// Path is the file location relative to the module root, slash-separated.
	Path string
	// Text is the file contents with CRLF endings normalised to LF.
	Text string
}

// Module is a third-party module linked into the release binaries.
type Module struct {
	Path     string
	Version  string
	Dir      string
	Files    []LicenseFile
	Licenses []string
}

// Report is the result of a scan: the Go toolchain license files and every
// third-party module with its license files and detected license names.
type Report struct {
	GoVersion string
	GoFiles   []LicenseFile
	Modules   []Module
}

// moduleAccum gathers one module across platforms: its version and directory
// must agree, while the compiled package directories are unioned.
type moduleAccum struct {
	version string
	dir     string
	pkgDirs map[string]struct{}
}

// Scan collects the third-party license inventory for moduleDir across
// platforms. An empty platforms slice means ReleasePlatforms.
func Scan(ctx context.Context, moduleDir string, platforms []Platform) (*Report, error) {
	if len(platforms) == 0 {
		platforms = ReleasePlatforms
	}
	goroot, goVersion, err := goEnv(ctx, moduleDir)
	if err != nil {
		return nil, err
	}
	mainPath, err := mainModulePath(ctx, moduleDir)
	if err != nil {
		return nil, err
	}

	accums := map[string]*moduleAccum{}
	for _, platform := range platforms {
		deps, err := listDeps(ctx, moduleDir, platform)
		if err != nil {
			return nil, err
		}
		if err := addDeps(accums, deps, mainPath); err != nil {
			return nil, err
		}
	}

	goFiles, err := goLicenseFiles(goroot)
	if err != nil {
		return nil, err
	}
	report := &Report{GoVersion: goVersion, GoFiles: goFiles}
	paths := make([]string, 0, len(accums))
	for path := range accums {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		acc := accums[path]
		module, err := scanModule(path, acc.version, acc.dir, sortedKeys(acc.pkgDirs))
		if err != nil {
			return nil, err
		}
		report.Modules = append(report.Modules, module)
	}
	return report, nil
}

// addDeps unions the third-party modules and package directories in deps into
// accums. The main module and standard library packages are skipped; a module
// seen on several platforms must resolve to the same version and directory.
func addDeps(accums map[string]*moduleAccum, deps []listedPackage, mainPath string) error {
	for _, dep := range deps {
		if dep.Standard || dep.Module == nil || dep.Module.Path == mainPath {
			continue
		}
		if dep.Module.Dir == "" {
			return fmt.Errorf("module %s has no source directory", dep.Module.Path)
		}
		acc := accums[dep.Module.Path]
		if acc == nil {
			acc = &moduleAccum{version: dep.Module.Version, dir: dep.Module.Dir, pkgDirs: map[string]struct{}{}}
			accums[dep.Module.Path] = acc
		} else if acc.version != dep.Module.Version || acc.dir != dep.Module.Dir {
			return fmt.Errorf("module %s resolved to %s in %s and to %s in %s across platforms",
				dep.Module.Path, acc.version, acc.dir, dep.Module.Version, dep.Module.Dir)
		}
		if dep.Dir != "" {
			acc.pkgDirs[dep.Dir] = struct{}{}
		}
	}
	return nil
}

// goEnv returns GOROOT and the Go toolchain version for moduleDir.
func goEnv(ctx context.Context, moduleDir string) (goroot, goVersion string, err error) {
	out, err := runGo(ctx, moduleDir, nil, "env", "GOROOT", "GOVERSION")
	if err != nil {
		return "", "", err
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 2 || lines[0] == "" || lines[1] == "" {
		return "", "", fmt.Errorf("go env GOROOT GOVERSION: unexpected output %q", string(out))
	}
	return lines[0], lines[1], nil
}

// mainModulePath returns the module path of moduleDir's main module.
func mainModulePath(ctx context.Context, moduleDir string) (string, error) {
	out, err := runGo(ctx, moduleDir, nil, "list", "-m", "-f", "{{.Path}}")
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", errors.New("go list -m: empty module path")
	}
	return path, nil
}

// listedPackage is the subset of `go list -deps -json` output that Scan needs.
type listedPackage struct {
	Dir      string
	Standard bool
	Module   *listedModule
}

// listedModule is the module of a listed package.
type listedModule struct {
	Path    string
	Version string
	Dir     string
}

// listDeps lists the dependency graph of moduleDir's packages for platform.
func listDeps(ctx context.Context, moduleDir string, platform Platform) ([]listedPackage, error) {
	env := []string{
		"GOOS=" + platform.GOOS,
		"GOARCH=" + platform.GOARCH,
		"CGO_ENABLED=0",
	}
	out, err := runGo(ctx, moduleDir, env, "list", "-deps", "-json=Dir,Standard,Module", "./...")
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	var deps []listedPackage
	for {
		var pkg listedPackage
		if err := dec.Decode(&pkg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decode go list output for %s: %w", platform, err)
		}
		deps = append(deps, pkg)
	}
	return deps, nil
}

// runGo runs the go command in dir with extraEnv appended to os.Environ and
// returns its stdout.
func runGo(ctx context.Context, dir string, extraEnv []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// scanModule builds the Module for one module path from its accumulated
// package directories.
func scanModule(path, version, dir string, packageDirs []string) (Module, error) {
	files, err := collectLicenseFiles(dir, packageDirs)
	if err != nil {
		return Module{}, fmt.Errorf("module %s: %w", path, err)
	}
	if len(files) == 0 {
		return Module{}, fmt.Errorf("module %s has no license or notice file", path)
	}
	return Module{
		Path:     path,
		Version:  version,
		Dir:      dir,
		Files:    files,
		Licenses: detectLicenses(files),
	}, nil
}

// goLicenseFiles returns the Go toolchain license files from goroot.
func goLicenseFiles(goroot string) ([]LicenseFile, error) {
	var files []LicenseFile
	for _, name := range []string{"LICENSE", "PATENTS"} {
		data, err := os.ReadFile(filepath.Join(goroot, name))
		if err != nil {
			if name != "LICENSE" && errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read Go %s: %w", name, err)
		}
		files = append(files, LicenseFile{Path: name, Text: normaliseText(string(data))})
	}
	return files, nil
}

// sortedKeys returns the keys of set sorted.
func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
