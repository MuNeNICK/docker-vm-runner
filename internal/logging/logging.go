package logging

import "fmt"

type Level string

const (
	Info    Level = "INFO"
	Warn    Level = "WARN"
	Error   Level = "ERROR"
	Success Level = "SUCCESS"
	Debug   Level = "DEBUG"
)

func Print(level Level, format string, args ...any) {
	fmt.Printf("[%s] %s\n", level, fmt.Sprintf(format, args...))
}
