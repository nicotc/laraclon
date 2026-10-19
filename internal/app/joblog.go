package app

import (
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

// tailLines is how many output lines a job keeps for the panel.
const tailLines = 8

// ansi matches the color and cursor codes programs print for terminals.
var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// followLog keeps the last lines of a job's log in the job while it runs, so the panel can show
// what Composer, npm or Git are doing and when they last printed something. stop reads what is
// left and ends.
func (d *Daemon) followLog(j *Job, path string) (stop func()) {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		var offset int64
		var lines []string
		partial := ""
		read := func() {
			f, err := os.Open(path)
			if err != nil {
				return
			}
			defer f.Close()
			if _, err := f.Seek(offset, io.SeekStart); err != nil {
				return
			}
			data, _ := io.ReadAll(io.LimitReader(f, 1<<20))
			if len(data) == 0 {
				return
			}
			offset += int64(len(data))
			var fresh []string
			partial, fresh = splitOutput(partial + string(data))
			lines = lastOf(collapseBars(append(lines, fresh...)), tailLines)
			shown := lines
			if p := cleanLine(partial); p != "" { // a progress line still being rewritten
				shown = lastOf(append(append([]string(nil), lines...), p), tailLines)
			}
			now := time.Now()
			d.jobs.update(j, func(j *Job) { j.Tail, j.Output = shown, now })
		}
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				read()
				return
			case <-tick.C:
				read()
			}
		}
	}()
	return func() {
		close(done)
		<-finished
	}
}

// splitOutput cuts program output into finished lines; a carriage return also ends one (progress
// bars rewrite their line). The unfinished rest is returned apart.
func splitOutput(text string) (rest string, lines []string) {
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' || text[i] == '\r' {
			if l := cleanLine(text[start:i]); l != "" {
				lines = append(lines, l)
			}
			start = i + 1
		}
	}
	return text[start:], lines
}

// progressBar matches the bars Composer and others redraw as new lines when they write to a file:
// "85/139 [=================>----------]  61%".
var progressBar = regexp.MustCompile(`\[[=>#\-. ]{8,}\]`)

// collapseBars keeps only the last of consecutive progress bar lines.
func collapseBars(lines []string) []string {
	var out []string
	for _, l := range lines {
		if n := len(out); n > 0 && progressBar.MatchString(l) && progressBar.MatchString(out[n-1]) {
			out[n-1] = l
			continue
		}
		out = append(out, l)
	}
	return out
}

func cleanLine(s string) string {
	s = strings.TrimSpace(ansi.ReplaceAllString(s, ""))
	if len(s) > 220 {
		s = s[:217] + "…"
	}
	return s
}

func lastOf(list []string, n int) []string {
	if len(list) > n {
		return append([]string(nil), list[len(list)-n:]...)
	}
	return list
}
