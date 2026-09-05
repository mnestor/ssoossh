package hostinfo

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// machineID is MachineGuid, written by Windows setup and stable for the
// life of the installation -- the closest thing the platform has to
// /etc/machine-id. It survives a rename, which is what the field is for.
//
// Read-only, and from HKLM, so an unprivileged client can read it but not
// change it. An error is not worth reporting: a locked-down machine that
// denies the read simply has no machine id, like any other platform that
// cannot answer.
func machineID() string {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return ""
	}
	defer func() { _ = key.Close() }()

	guid, _, err := key.GetStringValue("MachineGuid")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(guid)
}

// osName is the release the kernel reports: "Windows 10.0.26100".
//
// RtlGetVersion rather than the registry's ProductName, which has said
// "Windows 10" on Windows 11 since its release and would put a wrong answer
// on an audit record. The build number is what actually identifies a
// release.
func osName() string {
	v := windows.RtlGetVersion()
	if v == nil {
		// not covered: RtlGetVersion always returns a populated struct.
		return "Windows"
	}
	return fmt.Sprintf("Windows %d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
}

// ttyName: Windows consoles have no device name of the kind a tty field
// carries, and nothing here would be a terminal path an operator could join
// against a log.
func ttyName() string { return "" }
