package agent

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// proc runs a CLI, feeding each stdout line to onLine and keeping the tail
// of stderr for error messages. Cancelling ctx kills the whole process group.
type proc struct {
	bin    string
	args   []string
	dir    string
	env    []string
	base   []string // environment to start from; nil = this process's (os.Environ)
	stdin  string
	onLine func(string)
	onErr  func(string)
}

func (p proc) run(ctx context.Context) (int, string, error) {
	cmd := exec.CommandContext(ctx, p.bin, p.args...)
	cmd.Dir = p.dir
	base := p.base
	if base == nil {
		base = os.Environ()
	}
	cmd.Env = append(append([]string{}, base...), p.env...)
	setProcGroup(cmd)
	cmd.Cancel = func() error { return killGroup(cmd) }
	cmd.WaitDelay = 3 * time.Second
	if p.stdin != "" {
		cmd.Stdin = strings.NewReader(p.stdin)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return -1, "", err
	}
	errp, err := cmd.StderrPipe()
	if err != nil {
		return -1, "", err
	}
	if err := cmd.Start(); err != nil {
		return -1, "", fmt.Errorf("start %s: %w", p.bin, err)
	}
	var tail tailBuf
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 0, 256*1024), 32*1024*1024)
		for sc.Scan() {
			p.onLine(sc.Text())
		}
		_, _ = io.Copy(io.Discard, out)
	}()
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(errp)
		for sc.Scan() {
			tail.add(sc.Text())
			if p.onErr != nil {
				p.onErr(sc.Text())
			}
		}
	}()
	wg.Wait()
	err = cmd.Wait()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	if ctx.Err() != nil {
		return code, tail.String(), ctx.Err()
	}
	if err != nil {
		msg := tail.String()
		if msg == "" {
			msg = err.Error()
		}
		return code, msg, fmt.Errorf("%s exited with code %d: %s", p.bin, code, short(msg, 400))
	}
	return code, tail.String(), nil
}

type tailBuf struct {
	mu    sync.Mutex
	lines []string
}

func (t *tailBuf) add(s string) {
	t.mu.Lock()
	t.lines = append(t.lines, s)
	if len(t.lines) > 30 {
		t.lines = t.lines[len(t.lines)-30:]
	}
	t.mu.Unlock()
}

func (t *tailBuf) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(strings.Join(t.lines, "\n"))
}
