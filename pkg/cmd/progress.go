package cmd

import (
	"fmt"
	"io"
	"os"
	"time"
)

// progressReader wraps an io.Reader and renders transfer progress
// (percent, bytes, speed, ETA) to stderr while it is being consumed.
type progressReader struct {
	r     io.Reader
	label string
	total int64
	read  int64
	start time.Time
	last  time.Time
}

func newProgressReader(r io.Reader, total int64, label string) *progressReader {
	now := time.Now()
	return &progressReader{r: r, label: label, total: total, start: now, last: now}
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	p.read += int64(n)
	if time.Since(p.last) >= 200*time.Millisecond {
		p.last = time.Now()
		p.render()
	}
	return n, err
}

// render writes a single \r-terminated status line to stderr.
func (p *progressReader) render() {
	elapsed := time.Since(p.start).Seconds()
	speed := float64(p.read) / max(elapsed, 0.001)
	line := fmt.Sprintf("\r%s  %5.1f%%  %s / %s  %s/s",
		p.label, 100*float64(p.read)/max(float64(p.total), 1),
		humanBytes(p.read), humanBytes(p.total), humanBytes(int64(speed)))
	if speed > 0 && p.read < p.total {
		remaining := time.Duration(float64(p.total-p.read)/speed) * time.Second
		line += fmt.Sprintf("  ETA %s", remaining.Round(time.Second))
	}
	fmt.Fprintf(os.Stderr, "%-80s", line)
}

// finish prints the final summary line (call after a successful transfer).
func (p *progressReader) finish() {
	elapsed := time.Since(p.start)
	speed := float64(p.read) / max(elapsed.Seconds(), 0.001)
	fmt.Fprintf(os.Stderr, "\r%-80s\n",
		fmt.Sprintf("%s  done: %s in %s (%s/s)",
			p.label, humanBytes(p.read), elapsed.Round(time.Second), humanBytes(int64(speed))))
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
