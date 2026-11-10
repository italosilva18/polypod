package audio

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// PCMPartial, PCMFinal and PCMError are JSON event kinds from a resident
	// local worker. The ready line is consumed while the session starts.
	PCMPartial = "partial"
	PCMFinal   = "final"
	PCMError   = "error"
	pcmReady   = "ready"

	// Keep frames small so a stalled reader cannot pin a large write.
	maxPCMFrame = 16 << 10
)

// ErrPCMStreamUnsupported means the configured worker has no resident PCM
// session. Callers keep the one-shot Transcribe path.
var ErrPCMStreamUnsupported = errors.New("transcrição incremental não é suportada por este worker")

// PCMEvent is one stdout message from a resident local worker.
type PCMEvent struct {
	Kind string
	Text string
}

// PCMStream is one local faster-whisper process kept loaded for a dictation.
// EnqueuePCM copies mono 16 kHz PCM16 and returns without waiting for the
// worker pipe. CloseInput ends the audio. Events then carries partial text
// and one final transcript. Close releases the shared model lock and stops
// the process.
type PCMStream struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	events    chan PCMEvent
	procDone  chan error
	exited    chan struct{}
	wake      chan struct{}
	scanStop  chan struct{}
	writeDone chan struct{}
	dataReady chan struct{}
	release   func()
	stderr    *boundedOutput

	mu          sync.Mutex
	pending     []byte
	queued      int // bytes of pending already handed to the writer
	totalPCM    int // cumulative bytes accepted for this dictation
	inputClosed bool
	writeErr    error
	closeOnce   sync.Once
	stdinOnce   sync.Once
}

// StartPCMStream loads the configured local model once. It returns
// ErrPCMStreamUnsupported when the model is not a CTranslate2 directory or
// the worker rejects --pcm-stream, before audio is sent. The caller must
// Close the session, including after a successful Finish.
func (t *LocalTranscriber) StartPCMStream(ctx context.Context) (*PCMStream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Stat(t.model)
	if err != nil || !info.IsDir() {
		// openai-whisper checkpoints and scripts without a resident mode stay
		// on the one-shot path. Do not take the model lock for that probe.
		return nil, ErrPCMStreamUnsupported
	}
	// Bound the gate, cross-process model lock and ready handshake with one
	// startup deadline. The command itself retains the caller's context so a
	// long dictation is not killed merely because startup took some time.
	readyTimeout := t.timeout
	if readyTimeout <= 0 || readyTimeout > 45*time.Second {
		readyTimeout = 45 * time.Second
	}
	startupCtx, startupCancel := context.WithTimeout(ctx, readyTimeout)
	defer startupCancel()
	select {
	case localTranscriptionGate <- struct{}{}:
	case <-startupCtx.Done():
		return nil, startupCtx.Err()
	}
	releaseGate := func() { <-localTranscriptionGate }
	unlock, err := acquireModelLock(startupCtx, t.model)
	if err != nil {
		releaseGate()
		return nil, fmt.Errorf("aguardando modelo de transcrição local: %w", err)
	}
	release := func() {
		unlock()
		releaseGate()
	}
	// CommandContext is required because configureLocalProcess sets Cancel.
	// The caller keeps this context alive for the whole dictation; cancelling
	// it stops the resident worker. Microphone cancellation uses another context.
	cmd := exec.CommandContext(ctx, t.python, t.script, "--pcm-stream", "--model-path", t.model, "--language", t.language)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		release()
		return nil, err
	}
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		release()
		return nil, err
	}
	// We own this pipe. cmd.Wait never closes its read end while the scanner
	// drains a final JSON line, unlike Cmd.StdoutPipe.
	cmd.Stdout = stdoutWriter
	stderr := &boundedOutput{limit: maxDiagnosticBytes}
	cmd.Stderr = stderr
	configureLocalProcess(cmd)
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stdoutWriter.Close()
		release()
		return nil, fmt.Errorf("iniciar worker incremental: %w", err)
	}
	_ = stdoutWriter.Close()

	lines := make(chan string, 128)
	scanStop := make(chan struct{})
	go scanPCMLines(stdout, lines, scanStop)
	procDone := make(chan error, 1)
	go func() { procDone <- cmd.Wait() }()

	stop := func() (string, error) {
		close(scanStop)
		stopLocalProcess(cmd)
		select {
		case waitErr := <-procDone:
			return stderr.buf.String(), waitErr
		case <-time.After(3 * time.Second):
			return "", errors.New("worker incremental não encerrou")
		}
	}
	select {
	case line, ok := <-lines:
		if !ok {
			diag, waitErr := stop()
			release()
			if pcmStreamUnsupported(diag) {
				return nil, ErrPCMStreamUnsupported
			}
			if waitErr == nil {
				waitErr = errors.New("worker encerrou sem iniciar")
			}
			return nil, classifyLocalProcessError("worker", diag, waitErr)
		}
		kind, _, _ := parsePCMLine(line)
		if kind != pcmReady {
			diag, waitErr := stop()
			release()
			if pcmStreamUnsupported(line + "\n" + diag) {
				return nil, ErrPCMStreamUnsupported
			}
			if waitErr == nil {
				waitErr = errors.New("worker incremental não ficou pronto")
			}
			return nil, classifyLocalProcessError("worker", diag, waitErr)
		}
	case <-startupCtx.Done():
		_, _ = stop()
		release()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("modelo local não ficou pronto para transcrição incremental")
	}

	events := make(chan PCMEvent, 128)
	session := &PCMStream{
		cmd: cmd, stdin: stdin, events: events, procDone: procDone,
		exited: make(chan struct{}), wake: make(chan struct{}), scanStop: scanStop,
		writeDone: make(chan struct{}), dataReady: make(chan struct{}, 1),
		release: release, stderr: stderr,
	}
	go session.writeLoop()
	go session.readEvents(lines)
	return session, nil
}

func scanPCMLines(r io.ReadCloser, lines chan<- string, stop <-chan struct{}) {
	defer close(lines)
	defer r.Close()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64<<10), maxTranscriptBytes)
	for scanner.Scan() {
		select {
		case lines <- scanner.Text():
		case <-stop:
			return
		}
	}
}

func (s *PCMStream) readEvents(lines <-chan string) {
	defer close(s.exited)
	defer close(s.events)
	var terminal PCMEvent
readLines:
	for line := range lines {
		kind, text, ok := parsePCMLine(line)
		if !ok || kind == pcmReady {
			continue
		}
		switch kind {
		case PCMPartial:
			if terminal.Kind == "" && !s.emit(PCMEvent{Kind: kind, Text: text}) {
				break readLines
			}
		case PCMFinal:
			if terminal.Kind == "" {
				terminal = PCMEvent{Kind: kind, Text: text}
			}
		case PCMError:
			// A later worker error invalidates a final emitted too early.
			terminal = PCMEvent{Kind: kind, Text: text}
		}
	}
	var waitErr error
	waited := false
	select {
	case waitErr = <-s.procDone:
		waited = true
	case <-s.wake:
		select {
		case waitErr = <-s.procDone:
			waited = true
		case <-time.After(2 * time.Second):
		}
	}
	writerDone := false
	select {
	case <-s.writeDone:
		writerDone = true
	case <-time.After(2 * time.Second):
	}
	s.mu.Lock()
	writeErr := s.writeErr
	inputClosed := s.inputClosed
	s.mu.Unlock()
	if writeErr != nil {
		terminal = PCMEvent{Kind: PCMError, Text: fmt.Sprintf("envio de áudio ao worker falhou: %v", writeErr)}
	} else if terminal.Kind == PCMFinal && (!inputClosed || !writerDone || !waited || waitErr != nil) {
		terminal = PCMEvent{}
	}
	if terminal.Kind == "" {
		text := "transcrição incremental interrompida"
		if waited && waitErr != nil {
			text = classifyLocalProcessError("worker", s.stderr.buf.String(), waitErr).Error()
		}
		terminal = PCMEvent{Kind: PCMError, Text: text}
	}
	s.emit(terminal)
}

func (s *PCMStream) emit(ev PCMEvent) bool {
	select {
	case s.events <- ev:
		return true
	case <-s.wake:
		return false
	}
}

// Events emits partial transcripts and one final or error event. It is closed
// after the worker exits.
func (s *PCMStream) Events() <-chan PCMEvent {
	return s.events
}

// EnqueuePCM copies one contiguous block of mono PCM16 and wakes the writer.
// It does not write the worker pipe; a stalled worker must not block the caller.
func (s *PCMStream) EnqueuePCM(pcm []byte) error {
	if len(pcm) == 0 {
		return nil
	}
	if len(pcm)%2 != 0 {
		return errors.New("pcm de voz incompleto")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputClosed {
		return errors.New("sessão de voz já encerrada")
	}
	if s.writeErr != nil {
		return s.writeErr
	}
	if len(pcm) > maxPCMBytes-s.totalPCM {
		return errors.New("áudio excede duração máxima de cinco minutos")
	}
	s.pending = append(s.pending, pcm...)
	s.totalPCM += len(pcm)
	select {
	case s.dataReady <- struct{}{}:
	default:
	}
	return nil
}

// CloseInput tells the writer to flush queued PCM, send the end frame, and
// close the pipe. The final transcript still has to arrive on Events before
// the caller's deadline.
func (s *PCMStream) CloseInput() {
	s.mu.Lock()
	s.inputClosed = true
	s.mu.Unlock()
	select {
	case s.dataReady <- struct{}{}:
	default:
	}
}

// Close stops the worker and releases the model lock. It is safe to call twice
// and does not wait on a blocked pipe write: the process is killed first.
func (s *PCMStream) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.inputClosed = true
		s.mu.Unlock()
		close(s.wake)
		close(s.scanStop)
		stopLocalProcess(s.cmd)
		s.closeStdin()
		select {
		case <-s.exited:
		case <-time.After(3 * time.Second):
		}
		if s.release != nil {
			s.release()
			s.release = nil
		}
	})
}

func (s *PCMStream) writeLoop() {
	defer close(s.writeDone)
	defer s.closeStdin()
	for {
		s.mu.Lock()
		for s.queued >= len(s.pending) && !s.inputClosed && s.writeErr == nil {
			s.mu.Unlock()
			select {
			case <-s.dataReady:
			case <-s.wake:
				return
			}
			s.mu.Lock()
		}
		if s.writeErr != nil {
			s.mu.Unlock()
			return
		}
		if s.queued >= len(s.pending) && s.inputClosed {
			s.mu.Unlock()
			if err := writePCMFrame(s.stdin, nil); err != nil {
				s.mu.Lock()
				if s.writeErr == nil {
					s.writeErr = err
				}
				s.mu.Unlock()
			}
			return
		}
		n := len(s.pending) - s.queued
		if n > maxPCMFrame {
			n = maxPCMFrame
		}
		chunk := append([]byte(nil), s.pending[s.queued:s.queued+n]...)
		s.queued += n
		if s.queued == len(s.pending) {
			s.pending = nil
			s.queued = 0
		} else if s.queued > 256<<10 {
			s.pending = append([]byte(nil), s.pending[s.queued:]...)
			s.queued = 0
		}
		s.mu.Unlock()
		if err := writePCMFrame(s.stdin, chunk); err != nil {
			s.mu.Lock()
			if s.writeErr == nil {
				s.writeErr = err
			}
			s.mu.Unlock()
			return
		}
	}
}

func (s *PCMStream) closeStdin() {
	s.stdinOnce.Do(func() {
		if s.stdin != nil {
			_ = s.stdin.Close()
		}
	})
}

func writePCMFrame(w io.Writer, pcm []byte) error {
	frame := make([]byte, 4+len(pcm))
	binary.LittleEndian.PutUint32(frame[:4], uint32(len(pcm)))
	copy(frame[4:], pcm)
	return writeFull(w, frame)
}

func writeFull(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if n > 0 {
			p = p[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func parsePCMLine(line string) (kind, text string, ok bool) {
	var payload struct {
		Type  string `json:"type"`
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &payload); err != nil {
		return "", "", false
	}
	switch payload.Type {
	case pcmReady:
		return pcmReady, "", true
	case PCMPartial, PCMFinal:
		if !utf8.ValidString(payload.Text) {
			return "", "", false
		}
		return payload.Type, strings.TrimSpace(payload.Text), true
	case PCMError:
		text = strings.TrimSpace(payload.Error)
		if text == "" || !utf8.ValidString(text) {
			text = "transcrição incremental falhou"
		}
		return PCMError, text, true
	default:
		return "", "", false
	}
}

func pcmStreamUnsupported(diagnostic string) bool {
	lower := strings.ToLower(diagnostic)
	return strings.Contains(lower, "unrecognized argument") ||
		strings.Contains(lower, "unrecognized arguments")
}

func stopLocalProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if cmd.Cancel != nil {
		_ = cmd.Cancel()
		return
	}
	_ = cmd.Process.Kill()
}
