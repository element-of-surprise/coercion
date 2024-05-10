// Package embedded provides embeded files for multiple packages.
package embedded

import "embed"

//go:embed imgs/* tmpl/*
var FS embed.FS

// This is used to generate various embeded files for different platforms.
// Those are handled in embedded_[os]_[arch].go files.

// Note: Could be improved with UPX compression. https://github.com/upx/upx/
//go:generate go build -ldflags "-s -w" -o reporter/reporter reporter/reporter.go

//go:embed reporter/reporter

// Reporter provides a binary file that will read the reports from a sub-directory.
var Reporter []byte
