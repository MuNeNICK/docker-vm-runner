package logging

import (
	"fmt"
	"io"
	"os"
)

type Level string

const (
	Info    Level = "INFO"
	Warn    Level = "WARN"
	Error   Level = "ERROR"
	Success Level = "SUCCESS"
	Debug   Level = "DEBUG"
)

func Print(level Level, format string, args ...any) {
	defaultLogger.Print(level, format, args...)
}

type Logger struct {
	writer  io.Writer
	verbose bool
}

func New(writer io.Writer, verbose bool) *Logger {
	return &Logger{writer: writer, verbose: verbose}
}

func (l *Logger) Print(level Level, format string, args ...any) {
	if level == Debug && !l.verbose {
		return
	}
	fmt.Fprintf(l.writer, "[%s] %s\n", level, fmt.Sprintf(format, args...))
}

var defaultLogger = New(os.Stdout, false)
