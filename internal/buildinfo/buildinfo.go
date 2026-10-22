// Package buildinfo exposes the version stamped into the binary at build time:
//
//	go build -ldflags "-X bin/internal/buildinfo.Version=v0.1.0"
package buildinfo

// Version is "dev" for local builds.
var Version = "dev"
