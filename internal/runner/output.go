package runner

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/munenick/docker-vm-runner/internal/config"
	"github.com/munenick/docker-vm-runner/internal/download"
	"github.com/munenick/docker-vm-runner/internal/hostinfo"
)

type OutputMode int

const (
	OutputAuto OutputMode = iota
	OutputTerminal
	OutputLog
)

type Output struct {
	Stdout io.Writer
	Stderr io.Writer
	Mode   OutputMode
}

func (o Output) Startup(cfg config.VM) {
	fmt.Fprintln(o.Stdout, "Docker VM Runner")
	fmt.Fprintln(o.Stdout)
	o.Section("Host", normalizedLines(hostinfo.Lines(hostinfo.Detect(workImageProbePath(cfg)))))
	fmt.Fprintln(o.Stdout)
	o.Section("VM: "+cfg.Distro, VMSummaryLines(cfg))
	fmt.Fprintln(o.Stdout)
}

func (o Output) Section(title string, lines []string) {
	if o.Mode == OutputTerminal {
		o.terminalSection(title, lines)
		return
	}
	o.logSection(title, lines)
}

func (o Output) Step(message string) {
	if o.Mode == OutputTerminal {
		fmt.Fprintf(o.Stderr, "● %s\n", message)
		return
	}
	o.Info(message)
}

func (o Output) Info(message string) {
	fmt.Fprintf(o.Stderr, "[INFO] %s\n", message)
}

func (o Output) Success(message string) {
	fmt.Fprintf(o.Stderr, "[SUCCESS] %s\n", message)
}

func (o Output) Warn(title string, details ...string) {
	fmt.Fprintf(o.Stderr, "[WARN] %s\n", title)
	for _, detail := range details {
		if strings.TrimSpace(detail) != "" {
			fmt.Fprintf(o.Stderr, "       %s\n", detail)
		}
	}
}

func (o Output) ConsoleAttach() {
	if o.Mode == OutputTerminal {
		o.Info("Attaching to VM console")
		fmt.Fprintln(o.Stderr, "       Press Ctrl+] to detach.")
		return
	}
	o.Info("Attaching to VM console. Press Ctrl+] to detach.")
}

func (o Output) HeadlessWait() {
	if o.Mode == OutputTerminal {
		o.Info("Running headless")
		fmt.Fprintln(o.Stderr, "       VM is running. Waiting until it shuts down.")
		return
	}
	o.Info("Running headless: waiting until VM shuts down")
}

func (o Output) DownloadProgress(progress download.Progress) {
	if progress.RetryDelay > 0 && progress.Err != nil {
		o.Warn(
			fmt.Sprintf("Download failed (attempt %d/%d), retrying in %s", progress.Attempt, progress.Attempts, progress.RetryDelay),
			progress.Err.Error(),
		)
		return
	}
	if o.Mode == OutputTerminal {
		o.terminalDownloadProgress(progress)
		return
	}
	o.logDownloadProgress(progress)
}

func (o Output) terminalSection(title string, lines []string) {
	width := sectionWidth(title, lines, 68)
	top := "┌─ " + title + " " + strings.Repeat("─", max(0, width-len(title)-3)) + "┐"
	bottom := "└" + strings.Repeat("─", width) + "┘"
	fmt.Fprintln(o.Stdout, top)
	for _, line := range lines {
		fmt.Fprintf(o.Stdout, "│ %-*s │\n", width-3, line)
	}
	fmt.Fprintln(o.Stdout, bottom)
}

func (o Output) logSection(title string, lines []string) {
	fmt.Fprintf(o.Stdout, "== %s ==\n", title)
	for _, line := range lines {
		fmt.Fprintln(o.Stdout, line)
	}
}

func (o Output) terminalDownloadProgress(progress download.Progress) {
	label := downloadLabel(progress)
	if progress.Written == 0 && !progress.Done {
		fmt.Fprintf(o.Stderr, "[INFO] %s\n", label)
		fmt.Fprintf(o.Stderr, "       %s\n\n", progress.URL)
		return
	}
	if progress.Done {
		fmt.Fprintf(o.Stderr, "\r       %s\n", terminalProgressLine(progress, true))
		o.Success(fmt.Sprintf("Downloaded %s in %s", downloadSubject(label), formatDuration(progress.Elapsed)))
		return
	}
	fmt.Fprintf(o.Stderr, "\r       %s", terminalProgressLine(progress, false))
}

func (o Output) logDownloadProgress(progress download.Progress) {
	label := downloadLabel(progress)
	if progress.Written == 0 && !progress.Done {
		fmt.Fprintf(o.Stderr, "[INFO] %s: %s\n", label, progress.URL)
		return
	}
	if progress.Done {
		fmt.Fprintf(o.Stderr, "[SUCCESS] Downloaded %s: %s in %s\n", downloadSubject(label), formatMiB(progress.Written), formatDuration(progress.Elapsed))
		return
	}
	if progress.Total > 0 {
		speed := downloadSpeed(progress)
		percent := float64(progress.Written) * 100 / float64(progress.Total)
		fmt.Fprintf(o.Stderr, "[INFO] %s: %s / %s (%.1f%%, %s/s, ETA %s)\n",
			label,
			formatMiB(progress.Written),
			formatMiB(progress.Total),
			percent,
			formatMiB(int64(speed)),
			eta(progress, speed),
		)
		return
	}
	speed := downloadSpeed(progress)
	fmt.Fprintf(o.Stderr, "[INFO] %s: %s downloaded (%s/s, elapsed %s)\n", label, formatMiB(progress.Written), formatMiB(int64(speed)), formatDuration(progress.Elapsed))
}

func terminalProgressLine(progress download.Progress, done bool) string {
	speed := downloadSpeed(progress)
	if progress.Total > 0 {
		percent := float64(progress.Written) * 100 / float64(progress.Total)
		if done {
			percent = 100
		}
		return fmt.Sprintf("%s  %5.1f%%   %s / %s   %s/s   ETA %s",
			unicodeProgressBar(progress.Written, progress.Total, 30),
			percent,
			formatMiB(progress.Written),
			formatMiB(progress.Total),
			formatMiB(int64(speed)),
			eta(progress, speed),
		)
	}
	return fmt.Sprintf("%s downloaded   %s/s   elapsed %s", formatMiB(progress.Written), formatMiB(int64(speed)), formatDuration(progress.Elapsed))
}

func downloadSpeed(progress download.Progress) float64 {
	if progress.Elapsed <= 0 {
		return 0
	}
	return float64(progress.Written) / progress.Elapsed.Seconds()
}

func unicodeProgressBar(written int64, total int64, width int) string {
	if width <= 0 {
		return ""
	}
	if total <= 0 {
		return strings.Repeat("░", width)
	}
	filled := int(float64(width) * float64(written) / float64(total))
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

func eta(progress download.Progress, speed float64) string {
	if progress.Total <= 0 || speed <= 0 || progress.Written >= progress.Total {
		return "00:00"
	}
	remaining := time.Duration(float64(progress.Total-progress.Written)/speed) * time.Second
	return formatDuration(remaining)
}

func downloadLabel(progress download.Progress) string {
	if strings.TrimSpace(progress.Label) != "" {
		return strings.TrimSpace(progress.Label)
	}
	return "Downloading"
}

func downloadSubject(label string) string {
	return strings.TrimSpace(strings.TrimPrefix(label, "Downloading "))
}

func normalizedLines(lines []string) []string {
	normalized := make([]string, 0, len(lines))
	for _, line := range lines {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			normalized = append(normalized, line)
			continue
		}
		normalized = append(normalized, fmt.Sprintf("%-8s %s", strings.TrimSpace(key), strings.TrimSpace(value)))
	}
	return normalized
}

func sectionWidth(title string, lines []string, minWidth int) int {
	width := len(title) + 4
	for _, line := range lines {
		if len(line)+3 > width {
			width = len(line) + 3
		}
	}
	if width < minWidth {
		width = minWidth
	}
	return width
}

func formatMiB(value int64) string {
	return fmt.Sprintf("%.1f MiB", float64(value)/(1024*1024))
}

func formatDuration(value time.Duration) string {
	if value < 0 {
		value = 0
	}
	totalSeconds := int(value.Round(time.Second).Seconds())
	minutes := totalSeconds / 60
	seconds := totalSeconds % 60
	return fmt.Sprintf("%02d:%02d", minutes, seconds)
}

func isTerminalWriter(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
