// Package shell provides bash command execution, background job tracking,
// separate stdout/stderr capture with truncation caps, process group signalling,
// and job lifecycle updates published over an event bus.
package shell

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/pubsub"
)

// JobStatus represents the current lifecycle stage of a Job.
type JobStatus string

const (
	// StatusRunning indicates the job is currently executing.
	StatusRunning JobStatus = "Running"
	// StatusCompleted indicates the job finished successfully with exit code 0.
	StatusCompleted JobStatus = "Completed"
	// StatusFailed indicates the job completed with a non-zero exit code.
	StatusFailed JobStatus = "Failed"
	// StatusKilled indicates the job was explicitly terminated via Kill.
	StatusKilled JobStatus = "Killed"
	// StatusTimeout indicates the job was terminated because it exceeded its timeout.
	StatusTimeout JobStatus = "Timeout"
)

// Job represents the state and captured output of an executed command.
type Job struct {
	ID        string
	Command   string
	StartedAt time.Time
	Status    JobStatus
	ExitCode  int
	Stdout    string
	Stderr    string
}

// RunOpts defines the optional parameters for executing a command.
type RunOpts struct {
	Cwd     string
	Timeout time.Duration
	Env     map[string]string
}

// MaxCaptureSize is the maximum size (10 MB) of captured stdout/stderr.
var MaxCaptureSize = 10 * 1024 * 1024

// Shell manages execution and tracking of bash processes.
type Shell struct {
	bus     *pubsub.Topic[pubsub.ShellJobPayload]
	jobs    sync.Map // Map of jobID string -> *jobState
	cleanup chan struct{}
}

// jobState tracks the runtime details of an active or finished job.
type jobState struct {
	mu              sync.RWMutex
	id              string
	command         string
	startedAt       time.Time
	status          JobStatus
	exitCode        int
	stdout          strings.Builder
	stderr          strings.Builder
	process         *os.Process
	doneChan        chan struct{}
	truncatedStdout bool
	truncatedStderr bool
	rawStdoutBytes  int64
	rawStderrBytes  int64
}

// New constructs a Shell manager with the given pubsub topic.
func New(bus *pubsub.Topic[pubsub.ShellJobPayload]) *Shell {
	s := &Shell{
		bus:     bus,
		cleanup: make(chan struct{}),
	}
	go s.startTTLWatcher()
	return s
}

// Shutdown stops any background maintenance routines.
func (s *Shell) Shutdown() {
	close(s.cleanup)
}

// generateJobID generates a random 8-character hex string.
func generateJobID() string {
	bytes := make([]byte, 4)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to timestamp if crypto/rand fails.
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
	}
	return hex.EncodeToString(bytes)
}

// Run executes the command synchronously, blocking until it finishes
// or the context/timeout expires.
func (s *Shell) Run(ctx context.Context, cmd string, opts RunOpts) (Job, error) {
	id, err := s.Start(ctx, cmd, opts)
	if err != nil {
		return Job{}, fmt.Errorf("starting run: %w", err)
	}

	stateRaw, ok := s.jobs.Load(id)
	if !ok {
		return Job{}, fmt.Errorf("job %s state not found", id)
	}
	state := stateRaw.(*jobState)

	select {
	case <-ctx.Done():
		_ = s.Kill(id)
		return s.Output(id)
	case <-state.doneChan:
		return s.Output(id)
	}
}

// Start spawns the command asynchronously in the background, returning immediately
// with a unique job ID.
func (s *Shell) Start(ctx context.Context, cmdStr string, opts RunOpts) (string, error) {
	jobID := generateJobID()
	state := &jobState{
		id:        jobID,
		command:   cmdStr,
		startedAt: time.Now(),
		status:    StatusRunning,
		doneChan:  make(chan struct{}),
	}
	s.jobs.Store(jobID, state)

	// Build the bash -c command.
	cmd := exec.Command("bash", "-c", cmdStr)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true, // Start in a new process group for clean signaling.
	}

	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}

	// Merge environment variables.
	if len(opts.Env) > 0 {
		env := os.Environ()
		for k, v := range opts.Env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		cmd.Env = env
	}

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		s.jobs.Delete(jobID)
		return "", fmt.Errorf("creating stdout pipe: %w", err)
	}

	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		s.jobs.Delete(jobID)
		return "", fmt.Errorf("creating stderr pipe: %w", err)
	}

	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter

	var wg sync.WaitGroup
	wg.Add(2)
	go s.streamPipe(stdoutReader, "stdout", state, &wg)
	go s.streamPipe(stderrReader, "stderr", state, &wg)

	if err := cmd.Start(); err != nil {
		_ = stdoutWriter.Close()
		_ = stderrWriter.Close()
		wg.Wait()
		s.jobs.Delete(jobID)
		return "", fmt.Errorf("starting process: %w", err)
	}

	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()

	state.mu.Lock()
	state.process = cmd.Process
	state.mu.Unlock()

	// Publish started event.
	s.publishUpdate(jobID, "", nil, false)

	// Wait for process in another goroutine.
	go func() {
		defer close(state.doneChan)

		errChan := make(chan error, 1)
		go func() {
			errChan <- cmd.Wait()
		}()

		var exitErr error
		var timeoutExpired bool

		timeoutChan := make(<-chan time.Time)
		if opts.Timeout > 0 {
			timer := time.NewTimer(opts.Timeout)
			defer timer.Stop()
			timeoutChan = timer.C
		}

		select {
		case exitErr = <-errChan:
			// Process exited normally.
		case <-timeoutChan:
			timeoutExpired = true
			if cmd.Process != nil {
				// Signal negative pid to kill the whole process group.
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
			exitErr = <-errChan
		case <-ctx.Done():
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
			exitErr = <-errChan
		}

		// Wait for pipes to finish draining.
		wg.Wait()

		state.mu.Lock()
		defer state.mu.Unlock()

		if timeoutExpired {
			state.status = StatusTimeout
			state.exitCode = -1
		} else if state.status == StatusRunning {
			if exitErr == nil {
				state.status = StatusCompleted
				state.exitCode = 0
			} else {
				var exitCodeErr *exec.ExitError
				if errors.As(exitErr, &exitCodeErr) {
					ws := exitCodeErr.Sys().(syscall.WaitStatus)
					if ws.Signaled() && (ws.Signal() == syscall.SIGKILL || ws.Signal() == syscall.SIGTERM) {
						state.status = StatusKilled
						state.exitCode = -1
					} else {
						state.status = StatusFailed
						state.exitCode = exitCodeErr.ExitCode()
					}
				} else {
					state.status = StatusFailed
					state.exitCode = -1
				}
			}
		}

		// Publish completion.
		s.publishUpdate(jobID, "", nil, true)
	}()

	return jobID, nil
}

// streamPipe drains the given pipe, respecting maximum capture caps, and
// publishes updates over the pubsub topic.
func (s *Shell) streamPipe(r io.Reader, stream string, state *jobState, wg *sync.WaitGroup) {
	defer wg.Done()
	if closer, ok := r.(io.Closer); ok {
		defer closer.Close()
	}

	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := buf[:n]

			state.mu.Lock()
			var builder *strings.Builder
			var truncated *bool
			var rawBytes *int64

			if stream == "stdout" {
				builder = &state.stdout
				truncated = &state.truncatedStdout
				rawBytes = &state.rawStdoutBytes
			} else {
				builder = &state.stderr
				truncated = &state.truncatedStderr
				rawBytes = &state.rawStderrBytes
			}

			*rawBytes += int64(n)

			if builder.Len() < MaxCaptureSize {
				remaining := MaxCaptureSize - builder.Len()
				if len(chunk) > remaining {
					builder.Write(chunk[:remaining])
					*truncated = true
				} else {
					builder.Write(chunk)
				}
			} else {
				*truncated = true
			}
			state.mu.Unlock()

			// Publish stream event.
			s.publishUpdate(state.id, stream, chunk, false)
		}

		if err != nil {
			break
		}
	}
}

// publishUpdate sends job updates to the event bus.
func (s *Shell) publishUpdate(jobID string, stream string, chunk []byte, done bool) {
	if s.bus == nil {
		return
	}
	s.bus.Publish(context.Background(), pubsub.ShellJobPayload{
		JobID:  jobID,
		Stream: stream,
		Chunk:  chunk,
		Done:   done,
	})
}
