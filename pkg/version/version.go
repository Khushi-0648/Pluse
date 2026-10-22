// Package version holds the single source of truth for the product name and version.
package version

// Version is the one place the version number is set. Build.bat reads it from here to
// name the release zip, so the app, the zip and the docs always agree.
var Version = "2.0.0"

const (
	Product = "Pulse"
	Tagline = "Messaging automation"
)
