package minecraft

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ZakharyB/meshcraft/internal/config"
)

// PaperAdapter manages one Paper server process for one world.
type PaperAdapter struct {
	cfg config.WorldConfig

	mu        sync.Mutex
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	startedAt time.Time
	exitCode  *int
	lastErr   string
	ready     bool
	readyCh   chan struct{}
	doneCh    chan struct{}

	logMu  sync.Mutex
	logBuf []string // ring, last 1000 lines
}

const logRingSize = 1000

// NewPaperAdapter creates an adapter for the given world config.
func NewPaperAdapter(w config.WorldConfig) *PaperAdapter {
	return &PaperAdapter{cfg: w}
}

// Edition implements RuntimeAdapter.
func (p *PaperAdapter) Edition() string { return "java" }

func (p *PaperAdapter) jarAbs() string {
	if filepath.IsAbs(p.cfg.JarPath) {
		return p.cfg.JarPath
	}
	return filepath.Join(p.cfg.Dir, p.cfg.JarPath)
}

func (p *PaperAdapter) javaBin() (string, error) {
	if p.cfg.JavaPath != "" {
		return p.cfg.JavaPath, nil
	}
	return exec.LookPath("java")
}

// InstallCheck verifies java and the Paper jar exist. It does NOT
// download anything implicitly; see EnsureRuntime for explicit install.
func (p *PaperAdapter) InstallCheck(ctx context.Context) error {
	java, err := p.javaBin()
	if err != nil {
		return fmt.Errorf("%w: java not found (install Java 21+ or set java_path)", ErrNotInstalled)
	}
	out, err := exec.CommandContext(ctx, java, "-version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: `java -version` failed: %v (%s)", ErrNotInstalled, err, strings.TrimSpace(string(out)))
	}
	if _, err := os.Stat(p.jarAbs()); err != nil {
		return fmt.Errorf("%w: paper jar missing at %s (download from papermc.io and place it there)", ErrNotInstalled, p.jarAbs())
	}
	return nil
}

// Properties reads the world's server.properties (empty map if absent).
func (p *PaperAdapter) Properties() (map[string]string, error) {
	out := map[string]string{}
	b, err := os.ReadFile(filepath.Join(p.cfg.Dir, "server.properties"))
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out, nil
}

// Configure writes eula.txt and a deterministic server.properties from
// the world config. Existing unrelated properties are preserved.
func (p *PaperAdapter) Configure() error {
	if err := os.MkdirAll(p.cfg.Dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(p.cfg.Dir, "eula.txt"), []byte("eula=true\n"), 0o644); err != nil {
		return err
	}
	props, err := p.Properties()
	if err != nil {
		return err
	}
	props["server-port"] = strconv.Itoa(p.cfg.Port)
	props["max-players"] = strconv.Itoa(p.cfg.MaxPlayers)
	props["online-mode"] = strconv.FormatBool(p.cfg.OnlineMode)
	props["motd"] = p.cfg.Name + " (MeshCraft)"
	props["level-name"] = "world"
	props["enable-query"] = "false"
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString("# Managed by MeshCraft — manual edits to managed keys will be overwritten\n")
	for _, k := range keys {
		sb.WriteString(k + "=" + props[k] + "\n")
	}
	return os.WriteFile(filepath.Join(p.cfg.Dir, "server.properties"), []byte(sb.String()), 0o644)
}

// Start launches the Paper process. Fails if already running.
func (p *PaperAdapter) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil {
		return fmt.Errorf("world %s: server already running", p.cfg.ID)
	}
	if err := p.InstallCheck(ctx); err != nil {
		return err
	}
	java, _ := p.javaBin()
	cmd := exec.Command(java,
		fmt.Sprintf("-Xmx%dM", p.cfg.MaxRAMMB),
		"-jar", filepath.Base(p.jarAbs()), "nogui")
	cmd.Dir = p.cfg.Dir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start paper: %w", err)
	}
	p.cmd = cmd
	p.stdin = stdin
	p.startedAt = time.Now()
	p.exitCode = nil
	p.lastErr = ""
	p.ready = false
	p.readyCh = make(chan struct{})
	p.doneCh = make(chan struct{})

	go p.pumpLogs(stdout)
	go p.pumpLogs(stderr)
	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		if cmd.ProcessState != nil {
			code := cmd.ProcessState.ExitCode()
			p.exitCode = &code
		}
		if err != nil && p.lastErr == "" {
			p.lastErr = err.Error()
		}
		p.cmd = nil
		p.stdin = nil
		p.ready = false
		close(p.doneCh)
		p.mu.Unlock()
	}()
	return nil
}

func (p *PaperAdapter) pumpLogs(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		p.logMu.Lock()
		p.logBuf = append(p.logBuf, line)
		if len(p.logBuf) > logRingSize {
			p.logBuf = p.logBuf[len(p.logBuf)-logRingSize:]
		}
		p.logMu.Unlock()
		// Paper readiness line: `Done (1.234s)! For help, type "help"`
		if strings.Contains(line, "Done (") && strings.Contains(line, ")! For help") {
			p.mu.Lock()
			if !p.ready && p.readyCh != nil {
				p.ready = true
				close(p.readyCh)
			}
			p.mu.Unlock()
		}
	}
}

// WaitReady blocks until Paper reports readiness or the timeout hits.
func (p *PaperAdapter) WaitReady(ctx context.Context, timeout time.Duration) error {
	p.mu.Lock()
	if p.cmd == nil {
		p.mu.Unlock()
		return fmt.Errorf("world %s: server not running", p.cfg.ID)
	}
	readyCh, doneCh := p.readyCh, p.doneCh
	p.mu.Unlock()

	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-readyCh:
		return nil
	case <-doneCh:
		return fmt.Errorf("world %s: server exited before becoming ready (%s)", p.cfg.ID, p.Status().LastError)
	case <-t.C:
		return fmt.Errorf("world %s: not ready after %s", p.cfg.ID, timeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Console sends one command line to the server's stdin.
func (p *PaperAdapter) Console(cmd string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stdin == nil {
		return fmt.Errorf("world %s: server not running", p.cfg.ID)
	}
	_, err := io.WriteString(p.stdin, strings.TrimRight(cmd, "\n")+"\n")
	return err
}

// Quiesce coordinates a consistent snapshot of a RUNNING server:
// save-off + save-all flush (ADR-0006 path b). Caller must Resume.
func (p *PaperAdapter) Quiesce(ctx context.Context) error {
	if err := p.Console("save-off"); err != nil {
		return err
	}
	if err := p.Console("save-all flush"); err != nil {
		return err
	}
	// Paper applies save-all synchronously to the console queue; give
	// large worlds a moment, but never block forever.
	select {
	case <-time.After(2 * time.Second):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Resume re-enables saving after Quiesce.
func (p *PaperAdapter) Resume() error { return p.Console("save-on") }

// Stop asks Paper to shut down gracefully ("stop" console command),
// escalating to Process.Kill after the grace period.
func (p *PaperAdapter) Stop(ctx context.Context, grace time.Duration) error {
	p.mu.Lock()
	if p.cmd == nil {
		p.mu.Unlock()
		return nil // already stopped; Stop is idempotent
	}
	doneCh := p.doneCh
	stdin := p.stdin
	proc := p.cmd.Process
	p.mu.Unlock()

	if stdin != nil {
		_, _ = io.WriteString(stdin, "stop\n")
	}
	t := time.NewTimer(grace)
	defer t.Stop()
	select {
	case <-doneCh:
		return nil
	case <-t.C:
		_ = proc.Kill()
		<-doneCh
		return fmt.Errorf("world %s: did not stop within %s; killed", p.cfg.ID, grace)
	case <-ctx.Done():
		_ = proc.Kill()
		return ctx.Err()
	}
}

// Status implements RuntimeAdapter.
func (p *PaperAdapter) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := Status{
		WorldID:   p.cfg.ID,
		Endpoint:  fmt.Sprintf("127.0.0.1:%d", p.cfg.Port),
		Ready:     p.ready,
		LastError: p.lastErr,
	}
	if p.cmd != nil {
		st.Running = true
		st.StartedAt = p.startedAt
		if p.cmd.Process != nil {
			st.PID = p.cmd.Process.Pid
		}
	}
	st.ExitCode = p.exitCode
	return st
}

// Logs returns the last n captured log lines.
func (p *PaperAdapter) Logs(n int) []string {
	p.logMu.Lock()
	defer p.logMu.Unlock()
	if n <= 0 || n > len(p.logBuf) {
		n = len(p.logBuf)
	}
	out := make([]string, n)
	copy(out, p.logBuf[len(p.logBuf)-n:])
	return out
}
