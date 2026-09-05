//go:build !linux && !darwin && !freebsd && !windows

package hostinfo

// The platforms this project does not build for. Everything here reports
// nothing, which is the same "not reported" a supported platform gives for
// a field it cannot answer -- see the package comment. The file exists so
// the package still compiles under GOOS values the release does not ship,
// which is what `go build ./...` on a contributor's machine may well be.

func machineID() string { return "" }
func osName() string    { return "" }
func ttyName() string   { return sshTTY() }
