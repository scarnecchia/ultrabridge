//go:build windows

package logging

import "io"

func dialSyslog(addr string) io.Writer {
	// syslog not available on Windows
	return nil
}
