// Package q provides quick and dirty debugging output for tired programmers.
// Just type Q(foo)
// It's the fastest way to print a variable. Typing Q(foo) is easier than
// fmt.Printf("%#v whatever"). The output is easy on the eyes, with
// colorization, pretty-printing, and nice formatting. The output goes to
// the $TMPDIR/q log file, away from the noisy output of your program. Try it
// and give up using fmt.Printf() for good!
//
// For best results, import it like this:
// import . "github.com/y0ssar1an/q"
package q

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type color string

const (
	// ANSI color escape codes
	bold     color = "\033[1m"
	yellow   color = "\033[33m"
	cyan     color = "\033[36m"
	endColor color = "\033[0m" // "reset everything"

	maxLineWidth = 80
)

// The standard q logger
var std *logger

// logger writes pretty logs to the $TMPDIR/q file. It takes care of opening and
// closing the file. It is safe for concurrent use.
type logger struct {
	mu       sync.Mutex    // protects all the other fields
	buf      *bytes.Buffer // collects writes before they're flushed to the log file
	start    time.Time     // time of first write in the current log group
	timer    *time.Timer   // when it gets to 0, start a new log group
	lastFile string        // last file to call q.Q(). determines when to print header
	lastFunc string        // last function to call q.Q()
}

// init creates the standard logger.
func init() {
	// Starting with 0 time doesn't mean the timer is stopped, so we must
	// explicitly stop the timer.
	t := time.NewTimer(0)
	t.Stop()

	std = &logger{
		buf:   &bytes.Buffer{},
		timer: t,
	}
}

// header returns a formatted header string, e.g. [14:00:36 main.go main.main:122]
// if the 2s timer has expired, or the calling function or filename has changed.
// If none of those things are true, it returns an empty string.
func (l *logger) header(funcName, file string, line int) string {
	// Reset the 2s timer.
	timerExpired := l.resetTimer(2 * time.Second)

	if !timerExpired && funcName == l.lastFunc && file == l.lastFile {
		// Don't print a header line.
		return ""
	}

	l.lastFunc = funcName
	l.lastFile = file

	now := time.Now().UTC().Format("15:04:05")
	return fmt.Sprintf("[%s %s:%d %s]", now, file, line, funcName)
}

// resetTimer resets the logger's timer to the given time. It returns true if
// the timer had expired before it was reset.
func (l *logger) resetTimer(d time.Duration) (expired bool) {
	expired = !l.timer.Reset(d)
	if expired {
		l.start = time.Now()
	}
	return expired
}

// flush writes the logger's buffer to disk.
func (l *logger) flush() error {
	path := filepath.Join(os.TempDir(), "q")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, l.buf)
	l.buf.Reset()
	return err
}

// output writes to the log buffer. Each log message is prepended with a
// timestamp. Long lines are broken at 80 characters.
func (l *logger) output(args ...string) {
	timestamp := fmt.Sprintf("%.3fs", time.Since(l.start).Seconds())
	timestampWidth := len(timestamp) + 1 // +1 for padding space after timestamp
	timestamp = colorize(timestamp, yellow)

	// preWidth is the length of everything before the log message.
	fmt.Fprint(l.buf, timestamp, " ")

	// Subsequent lines have to be indented by the width of the timestamp.
	indent := strings.Repeat(" ", timestampWidth)
	padding := "" // padding is the space between args.
	lineArgs := 0 // number of args printed on the current log line.
	lineWidth := timestampWidth
	for _, arg := range args {
		argWidth := argWidth(arg)
		lineWidth += argWidth + len(padding)

		// Some names in name=value strings contain newlines. Insert indentation
		// after each newline so they line up.
		arg = strings.Replace(arg, "\n", "\n"+indent, -1)

		// Break up long lines. If this is first arg printed on the line
		// (lineArgs == 0), it makes no sense to break up the line.
		if lineWidth > maxLineWidth && lineArgs != 0 {
			fmt.Fprint(l.buf, "\n", indent)
			lineArgs = 0
			lineWidth = timestampWidth + argWidth
			padding = ""
		}
		fmt.Fprint(l.buf, padding, arg)
		lineArgs++
		padding = " "
	}

	fmt.Fprint(l.buf, "\n")
}

// Q pretty-prints the given arguments to the $TMPDIR/q log file.
func Q(v ...interface{}) {
	std.mu.Lock()
	defer std.mu.Unlock()

	// Flush the buffered writes to disk.
	defer std.flush()

	args := formatArgs(v...)
	funcName, file, line, err := getCallerInfo()
	if err != nil {
		std.output(args...) // no name=value printing
		return
	}

	// Print a header line if this q.Q() call is in a different file or
	// function than the previous q.Q() call, or if the 2s timer expired.
	// A header line looks like this: [14:00:36 main.go main.main:122].
	header := std.header(funcName, file, line)
	if header != "" {
		fmt.Fprint(std.buf, "\n", header, "\n")
	}

	// q.Q(foo, bar, baz) -> []string{"foo", "bar", "baz"}
	names, err := argNames(file, line)
	if err != nil {
		std.output(args...) // no name=value printing
		return
	}

	// Convert the arguments to name=value strings.
	args = prependArgName(names, args)
	std.output(args...)
}