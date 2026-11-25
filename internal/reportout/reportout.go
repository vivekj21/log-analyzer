// Package reportout saves an incident report as a plain markdown file.
// It is the optional "save to a file" step: no git, no other tools needed.
package reportout

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// Slug turns a title into a short, safe part of a file name.
func Slug(s string) string {
	slug := strings.Trim(strings.ToLower(nonAlnum.ReplaceAllString(s, "-")), "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	if slug == "" {
		slug = "report"
	}
	return slug
}

// Save writes content as a markdown file and returns the path it wrote.
//
//   - If target is an existing folder, or ends with a path separator, a new file
//     named <date>-<time>-<slug>.md is created inside it (the folder is created
//     when missing). An existing file is never overwritten: a clash gets -2, -3 ...
//   - Otherwise target is the exact file to write. Its folder is created when
//     missing. An existing file with that name is replaced, because you named it.
//
// Folders are created private (0700) and files are private (0600): a report can
// hold masked but still sensitive evidence.
func Save(target, slug, content string, now time.Time) (string, error) {
	if strings.TrimSpace(target) == "" {
		return "", errors.New("no output path given")
	}
	isDir := strings.HasSuffix(target, "/") || strings.HasSuffix(target, string(os.PathSeparator))
	if fi, err := os.Stat(target); err == nil && fi.IsDir() {
		isDir = true
	}

	if !isDir {
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return "", fmt.Errorf("cannot create the folder for %s: %w", target, err)
		}
		if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
			return "", fmt.Errorf("cannot write %s: %w", target, err)
		}
		return target, nil
	}

	if err := os.MkdirAll(target, 0o700); err != nil {
		return "", fmt.Errorf("cannot create the folder %s: %w", target, err)
	}
	base := fmt.Sprintf("%s-%s", now.Format("2006-01-02-150405"), Slug(slug))
	for n := 1; n <= 999; n++ {
		name := base + ".md"
		if n > 1 {
			name = fmt.Sprintf("%s-%d.md", base, n)
		}
		path := filepath.Join(target, name)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("cannot write %s: %w", path, err)
		}
		_, werr := f.WriteString(content)
		cerr := f.Close()
		if werr != nil {
			return "", fmt.Errorf("cannot write %s: %w", path, werr)
		}
		if cerr != nil {
			return "", fmt.Errorf("cannot write %s: %w", path, cerr)
		}
		return path, nil
	}
	return "", fmt.Errorf("too many report files named %s in %s", base, target)
}
