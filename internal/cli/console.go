package cli

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/ltsjoe/psclassic-launcher/internal/psc"
)

// console prints log lines and one progress line. On a terminal the progress line
// is redrawn in place; otherwise (for example in Heroic's game log) it is printed
// as a normal line now and then.
type console struct {
	mu     sync.Mutex
	w      io.Writer
	tty    bool
	status string
}

func newConsole(f *os.File) *console {
	return &console{w: f, tty: isTerminal(f)}
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func (c *console) Logf(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tty && c.status != "" {
		fmt.Fprint(c.w, "\r\033[K")
	}
	fmt.Fprintf(c.w, format+"\n", args...)
	if c.tty && c.status != "" {
		fmt.Fprint(c.w, c.status)
	}
}

// Status shows line as the progress line; "" ends it.
func (c *console) Status(line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.tty {
		if line != "" {
			fmt.Fprintln(c.w, line)
		}
		return
	}
	if line == "" {
		if c.status != "" {
			fmt.Fprint(c.w, "\r\033[K")
		}
		c.status = ""
		return
	}
	c.status = line
	fmt.Fprint(c.w, "\r\033[K"+line)
}

// interval is how often the progress line is shown.
func (c *console) interval() time.Duration {
	if c.tty {
		return time.Second
	}
	return 30 * time.Second
}

// progress turns updater progress into the console's progress line.
func (c *console) progress(p psc.Progress) {
	pct := 100.0
	if p.Total > 0 {
		pct = float64(p.Done) * 100 / float64(p.Total)
	}
	if p.FilesTotal == 0 {
		if p.Done == p.Total && !c.tty {
			return // the check is over; the next log line says how it went
		}
		c.Status(fmt.Sprintf("[%5.1f%%] Checking game files...", pct))
		return
	}
	rate := float64(p.Done) / max(time.Since(p.Start).Seconds(), 0.001)
	c.Status(fmt.Sprintf("[%5.1f%%] %s of %s, %s/s, %d of %d files",
		pct, humanBytes(p.Done), humanBytes(p.Total), humanBytes(int64(rate)), p.Files, p.FilesTotal))
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
