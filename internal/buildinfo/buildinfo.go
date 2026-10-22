// Package buildinfo exposes the version stamped into the binary at build time:
//
//	go build -ldflags "-X bin/internal/buildinfo.Version=v0.1.0
//	                   -X bin/internal/buildinfo.Commit=1a2b3c4
//	                   -X bin/internal/buildinfo.Date=2026-10-22"
//
// Without ldflags the commit and date come from the Go toolchain's VCS stamp
// when available.
package buildinfo

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

func init() {
	if Commit != "" && Date != "" {
		return
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	for _, s := range info.Settings {
		switch {
		case s.Key == "vcs.revision" && Commit == "":
			Commit = s.Value
		case s.Key == "vcs.time" && Date == "":
			Date = s.Value
		}
	}
}

// String describes the build on one line, e.g.
// "v0.1.0 (commit 1a2b3c4, built 2026-10-22, go1.27.3 linux/amd64)".
func String() string {
	details := ""
	if Commit != "" {
		c := Commit
		if len(c) > 7 {
			c = c[:7]
		}
		details += "commit " + c + ", "
	}
	if Date != "" {
		d := Date
		if len(d) > 10 {
			d = d[:10]
		}
		details += "built " + d + ", "
	}
	return fmt.Sprintf("%s (%s%s %s/%s)", Version, details, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
