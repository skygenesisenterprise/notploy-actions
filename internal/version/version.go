// Package version holds the action version reported to Notploy.
package version

// Version identifies this build of the action in the Notploy `User-Agent`
// header and in informational log lines.
//
// Release builds may override it at link time:
//
//	go build -ldflags "-X github.com/skygenesisenterprise/notploy-actions/internal/version.Version=1.2.3"
var Version = "dev"
