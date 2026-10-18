// Package supervisor runs the services (php-cgi, Apache, MySQL…) as hidden child processes.
// Every service lives in its own Windows job object with "kill on close": if clon exits or crashes,
// Windows ends the whole process tree, so no orphaned httpd.exe or mysqld.exe stays behind.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/nicotc/laraclon/internal/procutil"
)

// Spec describes one service process.
type Spec struct {
	Name        string
	Exe         string
	Args        []string
	Dir         string
	Env         []string // extra KEY=VALUE pairs
	LogPath     string
	Port        int                 // optional: readiness means this TCP port accepts connections
	Restart     bool                // restart when it exits on its own
	Stop        func(pid int) error // optional graceful stop request; the job is terminated after StopTimeout
	StopTimeout time.Duration
}

type State string

const (
	Starting State = "iniciando"
	Running  State = "en marcha"
	Stopping State = "deteniendo"
	Stopped  State = "detenido"
	Failed   State = "error"
)

// Status is a snapshot for the panel, the tray and `clon status`.
type Status struct {
	Name      string    `json:"name"`
	State     State     `json:"state"`
	PID       int       `json:"pid"`
	Port      int       `json:"port,omitempty"`
	Restarts  int       `json:"restarts"`
	Since     time.Time `json:"since"`
	LastError string    `json:"last_error,omitempty"`
	LogPath   string    `json:"log_path"`
}

type proc struct {
	spec      Spec
	mu        sync.Mutex
	state     State
	pid       int
	job       windows.Handle
	since     time.Time
	restarts  int
	lastError string
	stopping  bool
	exited    chan struct{}
}

// Supervisor owns all service processes.
type Supervisor struct {
	mu    sync.Mutex
	procs map[string]*proc
	// OnChange is called (without locks held) whenever a service changes state.
	OnChange func()
}

func New() *Supervisor { return &Supervisor{procs: map[string]*proc{}} }

// Start launches a service. Starting a running service is an error; use Restart.
func (s *Supervisor) Start(spec Spec) error {
	s.mu.Lock()
	p, ok := s.procs[spec.Name]
	if ok {
		p.mu.Lock()
		running := p.state == Running || p.state == Starting || p.state == Stopping
		if !running {
			p.stopping = true // cancels a pending automatic restart of the old entry
		}
		p.mu.Unlock()
		if running {
			s.mu.Unlock()
			return fmt.Errorf("%s ya está en marcha", spec.Name)
		}
	}
	p = &proc{spec: spec}
	s.procs[spec.Name] = p
	s.mu.Unlock()
	if spec.StopTimeout == 0 {
		p.spec.StopTimeout = 10 * time.Second
	}
	err := s.launch(p)
	s.changed()
	return err
}

func (s *Supervisor) changed() {
	if s.OnChange != nil {
		go s.OnChange()
	}
}

func (s *Supervisor) launch(p *proc) error {
	logf, err := openLog(p.spec.LogPath)
	if err != nil {
		return err
	}
	fmt.Fprintf(logf, "\r\n==== %s · %s %v ====\r\n", time.Now().Format("2006-01-02 15:04:05"), p.spec.Exe, p.spec.Args)

	cmd := exec.Command(p.spec.Exe, p.spec.Args...)
	cmd.Dir = p.spec.Dir
	cmd.Env = append(os.Environ(), p.spec.Env...)
	cmd.Stdout = logf
	cmd.Stderr = logf
	// Created suspended so it can join its job object before it starts children of its own.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW}
	if err := cmd.Start(); err != nil {
		logf.Close()
		p.setFailed(err)
		return fmt.Errorf("no se pudo iniciar %s: %w", p.spec.Name, err)
	}
	job, err := attachJob(cmd.Process.Pid)
	if err != nil {
		cmd.Process.Kill()
		logf.Close()
		p.setFailed(err)
		return fmt.Errorf("%s: %w", p.spec.Name, err)
	}
	if err := resume(uint32(cmd.Process.Pid)); err != nil {
		windows.TerminateJobObject(job, 1)
		windows.CloseHandle(job)
		logf.Close()
		p.setFailed(err)
		return fmt.Errorf("%s: %w", p.spec.Name, err)
	}

	p.mu.Lock()
	p.pid = cmd.Process.Pid
	p.job = job
	p.state = Starting
	p.since = time.Now()
	p.lastError = ""
	p.exited = make(chan struct{})
	exited := p.exited
	p.mu.Unlock()

	go s.watchReady(p, exited)
	go s.wait(p, cmd, logf, exited)
	return nil
}

// watchReady marks the service as running once its port answers (or right away without a port).
func (s *Supervisor) watchReady(p *proc, exited chan struct{}) {
	ready := true
	if p.spec.Port > 0 {
		ready = false
		deadline := time.Now().Add(90 * time.Second)
		for !ready && time.Now().Before(deadline) {
			select {
			case <-exited:
				return
			default:
			}
			conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p.spec.Port)), 300*time.Millisecond)
			if err == nil {
				conn.Close()
				ready = true
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	p.mu.Lock()
	if p.state == Starting {
		if ready {
			p.state = Running
		} else {
			p.lastError = fmt.Sprintf("el proceso sigue vivo pero no responde en el puerto %d", p.spec.Port)
		}
	}
	p.mu.Unlock()
	s.changed()
}

func (s *Supervisor) wait(p *proc, cmd *exec.Cmd, logf *os.File, exited chan struct{}) {
	err := cmd.Wait()
	ranFor := time.Since(p.since)
	p.mu.Lock()
	windows.CloseHandle(p.job)
	p.job = 0
	stopping := p.stopping
	if stopping {
		p.state = Stopped
	} else {
		p.state = Failed
		p.lastError = describeExit(err) + ": " + tail(p.spec.LogPath, 600)
	}
	p.pid = 0
	close(exited)
	p.mu.Unlock()
	fmt.Fprintf(logf, "\r\n==== terminó: %s ====\r\n", describeExit(err))
	logf.Close()
	s.changed()

	if stopping || !p.spec.Restart {
		return
	}
	// Restart with a growing pause; a service that keeps dying quickly is left stopped.
	p.mu.Lock()
	if ranFor > time.Minute {
		p.restarts = 0
	}
	p.restarts++
	n := p.restarts
	p.mu.Unlock()
	if n > 5 {
		return
	}
	time.Sleep(time.Duration(n*n) * 500 * time.Millisecond)
	p.mu.Lock()
	cancelled := p.stopping
	p.mu.Unlock()
	if !cancelled {
		s.launch(p)
		s.changed()
	}
}

// Stop asks a service to stop gracefully and ends its process tree after its timeout.
func (s *Supervisor) Stop(name string) error {
	s.mu.Lock()
	p, ok := s.procs[name]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	p.mu.Lock()
	p.stopping = true
	pid, job, exited := p.pid, p.job, p.exited
	if pid == 0 || exited == nil {
		p.state = Stopped
		p.mu.Unlock()
		return nil
	}
	p.state = Stopping
	p.mu.Unlock()
	s.changed()

	if p.spec.Stop != nil {
		if err := p.spec.Stop(pid); err != nil {
			windows.TerminateJobObject(job, 1)
		}
	} else {
		windows.TerminateJobObject(job, 1)
	}
	select {
	case <-exited:
	case <-time.After(p.spec.StopTimeout):
		p.mu.Lock()
		if p.job != 0 {
			windows.TerminateJobObject(p.job, 1)
		}
		p.mu.Unlock()
		<-exited
	}
	return nil
}

// StopMatching stops every service for which match returns true, in parallel.
func (s *Supervisor) StopMatching(match func(name string) bool) {
	s.mu.Lock()
	var names []string
	for n := range s.procs {
		if match(n) {
			names = append(names, n)
		}
	}
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, n := range names {
		wg.Add(1)
		go func(n string) { defer wg.Done(); s.Stop(n) }(n)
	}
	wg.Wait()
}

// StopAll stops everything.
func (s *Supervisor) StopAll() { s.StopMatching(func(string) bool { return true }) }

// PID returns the main process of a running service (0 when stopped).
func (s *Supervisor) PID(name string) int {
	s.mu.Lock()
	p, ok := s.procs[name]
	s.mu.Unlock()
	if !ok {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pid
}

// Statuses lists all known services, sorted by name.
func (s *Supervisor) Statuses() []Status {
	s.mu.Lock()
	list := make([]*proc, 0, len(s.procs))
	for _, p := range s.procs {
		list = append(list, p)
	}
	s.mu.Unlock()
	out := make([]Status, 0, len(list))
	for _, p := range list {
		p.mu.Lock()
		out = append(out, Status{Name: p.spec.Name, State: p.state, PID: p.pid, Port: p.spec.Port, Restarts: p.restarts, Since: p.since, LastError: p.lastError, LogPath: p.spec.LogPath})
		p.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// WaitRunning blocks until a service reports Running, fails, or ctx ends.
func (s *Supervisor) WaitRunning(ctx context.Context, name string) error {
	for {
		for _, st := range s.Statuses() {
			if st.Name != name {
				continue
			}
			switch st.State {
			case Running:
				return nil
			case Failed, Stopped:
				if st.LastError != "" {
					return errors.New(st.LastError)
				}
				return fmt.Errorf("%s se detuvo", name)
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s no respondió a tiempo", name)
		case <-time.After(150 * time.Millisecond):
		}
	}
}

func (p *proc) setFailed(err error) {
	p.mu.Lock()
	p.state = Failed
	p.lastError = err.Error()
	p.mu.Unlock()
}

func describeExit(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if hint := procutil.ExitHint(ee.ExitCode()); hint != "" {
			return hint
		}
		return fmt.Sprintf("código de salida %d", ee.ExitCode())
	}
	if err != nil {
		return err.Error()
	}
	return "código de salida 0"
}

// attachJob puts a (suspended) process in a new kill-on-close job object.
func attachJob(pid int) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

// resume starts the threads of a process created with CREATE_SUSPENDED.
func resume(pid uint32) error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snap)
	var te windows.ThreadEntry32
	te.Size = uint32(unsafe.Sizeof(te))
	resumed := 0
	for err = windows.Thread32First(snap, &te); err == nil; err = windows.Thread32Next(snap, &te) {
		if te.OwnerProcessID != pid {
			continue
		}
		th, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, te.ThreadID)
		if err != nil {
			continue
		}
		windows.ResumeThread(th)
		windows.CloseHandle(th)
		resumed++
	}
	if resumed == 0 {
		return fmt.Errorf("no se pudo reanudar el proceso %d", pid)
	}
	return nil
}

const maxLogSize = 10 << 20

// openLog appends to a service log, rotating it to .1 when it grows past 10 MB.
func openLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if st, err := os.Stat(path); err == nil && st.Size() > maxLogSize {
		os.Rename(path, path+".1")
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

// tail returns roughly the last n bytes of a log, for error messages.
func tail(path string, n int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	off := st.Size() - n
	if off < 0 {
		off = 0
	}
	f.Seek(off, io.SeekStart)
	b, _ := io.ReadAll(f)
	return string(b)
}
