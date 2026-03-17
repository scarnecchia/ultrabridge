//go:build !windows

package logging

import (
	"io"
	"log/slog"
	"log/syslog"
	"net/url"
)

func dialSyslog(addr string) io.Writer {
	u, err := url.Parse(addr)
	if err != nil {
		slog.Warn("invalid syslog address", "addr", addr, "error", err)
		return nil
	}
	w, err := syslog.Dial(u.Scheme, u.Host, syslog.LOG_INFO|syslog.LOG_DAEMON, "ultrabridge")
	if err != nil {
		slog.Warn("syslog connect failed", "addr", addr, "error", err)
		return nil
	}
	return w
}
