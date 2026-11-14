// Package secrets scans a project tree for credentials before a deploy.
//
// The scanner is a small set of regular expressions. It is a starting point,
// not a complete secret-detection product.
//
// TODO: respect an allowlist file, score entropy, and skip generated
// bundles more carefully. The regex pass is enough to refuse an obvious
// commit of a cloud token.
package secrets

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const maxFileBytes = 512 << 10

// Finding is one match. The matched text is redacted.
type Finding struct {
	Path     string
	Line     int
	Rule     string
	Redacted string
}

// ScanError aborts a deploy.
type ScanError struct {
	Findings []Finding
}

func (e *ScanError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "secret scan found %d match(es); deploy aborted", len(e.Findings))
	for _, f := range e.Findings {
		fmt.Fprintf(&b, "\n  %s:%d %s %s", f.Path, f.Line, f.Rule, f.Redacted)
	}
	b.WriteString("\nset the value with `shipyard secrets set` instead of committing it")
	return b.String()
}

type rule struct {
	name string
	re   *regexp.Regexp
}

var rules = []rule{
	{name: "aws-access-key", re: regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{name: "github-token", re: regexp.MustCompile(`ghp_[A-Za-z0-9]{20,}`)},
	{name: "github-fine-grained-token", re: regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`)},
	{name: "slack-token", re: regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`)},
	{name: "google-api-key", re: regexp.MustCompile(`AIza[0-9A-Za-z_\-]{35}`)},
	{name: "private-key", re: regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`)},
	{name: "assigned-secret", re: regexp.MustCompile(`(?i)\b(?:api[_-]?key|secret|password|passwd|token)\b\s*[:=]\s*['"]?[A-Za-z0-9+/=_\-]{12,}`)},
}

var skipDirs = map[string]struct{}{
	".git":         {},
	"node_modules": {},
	".next":        {},
	"dist":         {},
	"build":        {},
	"__pycache__":  {},
	".venv":        {},
	"venv":         {},
	"vendor":       {},
	"coverage":     {},
}

var skipFiles = map[string]struct{}{
	".env.example":      {},
	"package-lock.json": {},
	"yarn.lock":         {},
	"pnpm-lock.yaml":    {},
	"go.sum":            {},
	"cargo.lock":        {},
}

var skipExt = map[string]struct{}{
	".md":    {},
	".png":   {},
	".jpg":   {},
	".jpeg":  {},
	".gif":   {},
	".webp":  {},
	".ico":   {},
	".pdf":   {},
	".zip":   {},
	".gz":    {},
	".woff":  {},
	".woff2": {},
}

// Scan walks root and returns redacted findings.
func Scan(root string) ([]Finding, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("scan secrets: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("scan secrets: %s is not a directory", root)
	}
	var findings []Finding
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if path != root {
				if _, skip := skipDirs[d.Name()]; skip {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		name := d.Name()
		if _, skip := skipFiles[name]; skip {
			return nil
		}
		if _, skip := skipExt[strings.ToLower(filepath.Ext(name))]; skip {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		found, err := scanFile(path, filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		findings = append(findings, found...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		if findings[i].Line != findings[j].Line {
			return findings[i].Line < findings[j].Line
		}
		return findings[i].Rule < findings[j].Rule
	})
	return findings, nil
}

func scanFile(path, rel string) ([]Finding, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", rel, err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", rel, err)
	}
	if len(data) > maxFileBytes || strings.Contains(string(data), "\x00") {
		return nil, nil
	}
	var findings []Finding
	for i, line := range strings.Split(string(data), "\n") {
		for _, rule := range rules {
			match := rule.re.FindString(line)
			if match == "" {
				continue
			}
			findings = append(findings, Finding{
				Path:     rel,
				Line:     i + 1,
				Rule:     rule.name,
				Redacted: redact(match),
			})
		}
	}
	return findings, nil
}

func redact(match string) string {
	match = strings.TrimSpace(match)
	if len(match) <= 4 {
		return "****"
	}
	return match[:2] + "… (" + fmt.Sprintf("%d", len(match)) + " chars)"
}
