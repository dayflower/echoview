package main

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// stderrLogger writes CLI diagnostics without mixing them into command output.
type stderrLogger struct {
	output io.Writer
	quiet  bool
	debug  bool
	now    func() time.Time
	mu     sync.Mutex
}

func newStderrLogger(output io.Writer, quiet, debug bool) *stderrLogger {
	return newStderrLoggerAt(output, quiet, debug, time.Now)
}

func newStderrLoggerAt(output io.Writer, quiet, debug bool, now func() time.Time) *stderrLogger {
	return &stderrLogger{output: output, quiet: quiet, debug: debug, now: now}
}

func (l *stderrLogger) Waitf(format string, values ...any) {
	l.normalf("waiting", format, values...)
}

func (l *stderrLogger) Infof(format string, values ...any) {
	l.normalf("info", format, values...)
}

func (l *stderrLogger) Warnf(format string, values ...any) {
	l.normalf("warning", format, values...)
}

func (l *stderrLogger) Debugf(format string, values ...any) {
	if !l.debug {
		return
	}
	l.writef("debug", format, values...)
}

func (l *stderrLogger) normalf(level, format string, values ...any) {
	if l.quiet {
		return
	}
	l.writef(level, format, values...)
}

func (l *stderrLogger) writef(level, format string, values ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = fmt.Fprintf(l.output, "%s [%s] %s\n", l.now().Format(time.RFC3339Nano), level, fmt.Sprintf(format, values...))
}
