// Package embedded provides embeded files for multiple packages.
package embedded

import "embed"

//go:embed imgs/* tmpl/*
var FS embed.FS

// This is used to generate various embeded files for different platforms.
// Those are handled in embedded_[os]_[arch].go files.

// Note: Could be improved with UPX compression. https://github.com/upx/upx/
//go:generate env GOOS=linux GOARCH=arm64 go build -ldflags "-s -w" -o reporter/reporter_linux_arm64 reporter/reporter.go
//go:generate env GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o reporter/reporter_linux_amd64 reporter/reporter.go
//go:generate env GOOS=darwin GOARCH=arm64 go build -ldflags "-s -w" -o reporter/reporter_darwin_arm64 reporter/reporter.go
//go:generate env GOOS=darwin GOARCH=amd64 go build -ldflags "-s -w" -o reporter/reporter_darwin_amd64 reporter/reporter.go
//go:generate env GOOS=windows GOARCH=amd64 go build -ldflags "-s -w" -o reporter/reporter_windows_amd64.exe reporter/reporter.go

//go:embed reporter/reporter_linux_arm64
var ReporterLinuxArm64 []byte

//go:embed reporter/reporter_linux_amd64
var ReporterLinuxAmd64 []byte

//go:embed reporter/reporter_darwin_arm64
var ReporterDarwinArm64 []byte

//go:embed reporter/reporter_darwin_amd64
var ReporterDarwinAmd64 []byte

//go:embed reporter/reporter_windows_amd64.exe
var ReporterWindowsAmd64 []byte
