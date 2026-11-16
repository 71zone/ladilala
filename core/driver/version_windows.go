package driver

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// binaryVersion reads the version resource of the executable. chrome.exe
// prints nothing for --version on Windows, so the resource is the only source.
func binaryVersion(path string) (string, error) {
	size, err := windows.GetFileVersionInfoSize(path, nil)
	if err != nil {
		return "", fmt.Errorf("driver: version info size of %s: %w", path, err)
	}
	buf := make([]byte, size)
	if err := windows.GetFileVersionInfo(path, 0, size, unsafe.Pointer(&buf[0])); err != nil {
		return "", fmt.Errorf("driver: version info of %s: %w", path, err)
	}
	var info *windows.VS_FIXEDFILEINFO
	var n uint32
	if err := windows.VerQueryValue(unsafe.Pointer(&buf[0]), `\`, unsafe.Pointer(&info), &n); err != nil {
		return "", fmt.Errorf("driver: fixed version info of %s: %w", path, err)
	}
	return fmt.Sprintf("%d.%d.%d.%d", info.FileVersionMS>>16, info.FileVersionMS&0xffff, info.FileVersionLS>>16, info.FileVersionLS&0xffff), nil
}
