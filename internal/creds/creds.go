// Package creds finds the Gemini API key.
//
// A key can come from a file (best: Kubernetes Secret volumes, Vault Agent, the Secrets Store CSI
// driver and "sops -d" can all write one) or from an environment variable. Each source is a
// Provider, so another source (for example a cloud secret manager) can be added later without
// touching the rest of the program.
//
// Rules:
//   - Sources are tried in the order given; the first one that holds a key wins.
//   - A source that is configured but broken (file missing, empty, wrong shape) is an ERROR.
//     It never falls through to the next source, because that could silently use a different key.
//   - The key's value never appears in an error or a warning.
package creds

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"strings"
)

// maxKeyFileBytes stops us reading a huge file that was pointed at by mistake.
const maxKeyFileBytes = 4096

// Provider is one place a key can come from.
type Provider interface {
	// Name says where the key came from, for logs. It never contains the key.
	Name() string
	// Lookup returns found=false when this source is simply not configured.
	// It returns an error when it IS configured but unusable.
	Lookup() (key string, found bool, err error)
}

// Resolve returns the key from the first provider that has one. With no key anywhere it returns
// "" and no error: the caller decides whether a key is needed.
func Resolve(providers ...Provider) (key, source string, err error) {
	for _, p := range providers {
		k, found, lerr := p.Lookup()
		if lerr != nil {
			return "", "", fmt.Errorf("%s: %w", p.Name(), lerr)
		}
		if found {
			return k, p.Name(), nil
		}
	}
	return "", "", nil
}

type envProvider struct {
	name   string
	getenv func(string) string
}

// EnvProvider reads the key from an environment variable. An empty variable counts as not set.
func EnvProvider(name string, getenv func(string) string) Provider {
	return envProvider{name: name, getenv: getenv}
}

func (p envProvider) Name() string { return "environment variable " + p.name }

func (p envProvider) Lookup() (string, bool, error) {
	v := strings.TrimSpace(p.getenv(p.name))
	return v, v != "", nil
}

type fileProvider struct {
	label string
	path  string
	warn  func(string)
}

// FileProvider reads the key from a file. label says how the path was given (a flag or a
// variable name), for logs. An empty path means this source is not configured. warn may be nil.
func FileProvider(label, path string, warn func(string)) Provider {
	return fileProvider{label: label, path: path, warn: warn}
}

func (p fileProvider) Name() string { return "file given by " + p.label }

func (p fileProvider) Lookup() (string, bool, error) {
	if p.path == "" {
		return "", false, nil
	}
	info, err := os.Stat(p.path)
	if err != nil {
		return "", false, fmt.Errorf("cannot read key file %q: %w", p.path, errors.Unwrap(err))
	}
	if info.IsDir() {
		return "", false, fmt.Errorf("%q is a folder, not a key file", p.path)
	}
	if info.Size() > maxKeyFileBytes {
		return "", false, fmt.Errorf("%q is %d bytes; a key file should hold only the key", p.path, info.Size())
	}
	if p.warn != nil && keyFileTooOpen(runtime.GOOS, info.Mode().Perm()) {
		p.warn(fmt.Sprintf("key file %q can be read by every user on this machine (mode %04o); use 0400 or 0440", p.path, info.Mode().Perm()))
	}
	raw, err := os.ReadFile(p.path)
	if err != nil {
		return "", false, fmt.Errorf("cannot read key file %q: %w", p.path, errors.Unwrap(err))
	}
	key := strings.TrimSpace(string(raw))
	if key == "" {
		return "", false, fmt.Errorf("key file %q is empty", p.path)
	}
	if strings.ContainsAny(key, " \t\r\n") {
		// Deliberately does not echo the content: it may be a secret of another kind.
		return "", false, fmt.Errorf("key file %q has spaces or several lines; it should hold only the key", p.path)
	}
	return key, true, nil
}

// keyFileTooOpen says whether a key file can be read by every user on the machine.
// Windows does not store Unix permission bits (Go reports a fixed 0666 or 0444 there), so the
// check cannot be done from the mode and is skipped: a warning would be wrong on every run.
func keyFileTooOpen(goos string, perm fs.FileMode) bool {
	if goos == "windows" {
		return false
	}
	return perm&0o007 != 0
}
