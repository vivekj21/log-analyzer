package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// These are set at build time by the release build (see .goreleaser.yaml):
//
//	-ldflags "-X main.version=1.2.3 -X main.commit=abc1234 -X main.date=2026-01-01T00:00:00Z"
//
// date is the time of the commit the program was built from, so the same commit always builds the
// same program.
//
// A plain `go build` or `go run` leaves them empty; versionString then falls back to what Go
// recorded in the binary.
var (
	version = ""
	commit  = ""
	date    = ""
)

// versionString is what `log-analyzer --version` prints: one line, easy to compare with a release.
func versionString() string {
	v, c, d := version, commit, date
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			v = bi.Main.Version
		}
		for _, s := range bi.Settings {
			switch {
			case s.Key == "vcs.revision" && c == "":
				c = s.Value
			case s.Key == "vcs.time" && d == "":
				d = s.Value
			}
		}
	}
	if v == "" {
		v = "dev"
	}
	if len(c) > 7 {
		c = c[:7]
	}
	extra := ""
	if c != "" {
		extra += ", commit " + c
	}
	if d != "" {
		extra += ", committed " + d
	}
	return fmt.Sprintf("log-analyzer %s (%s %s/%s%s)", v, runtime.Version(), runtime.GOOS, runtime.GOARCH, extra)
}
