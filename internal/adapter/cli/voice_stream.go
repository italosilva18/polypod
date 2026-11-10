package cli

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/costa/polypod/internal/audio"
)

const (
	// voiceTailInterval is how often the recorder offers newly written PCM.
	// It is not a measured transcription latency.
	voiceTailInterval   = 200 * time.Millisecond
	voiceTailRead       = 256 << 10
	voicePreviewInitial = 4 * time.Second
	voicePreviewGap     = 12 * time.Second
	voicePreviewMax     = 4
	// voiceReadyLimit caps how long dictation waits for a resident model
	// before using bounded WAV previews. Cold imports can be slow; this is
	// only a startup bound.
	voiceReadyLimit = 45 * time.Second
)

type streamOpen struct {
	stream *audio.PCMStream
	err    error
}

type recResult struct {
	data []byte
	err  error
}

type streamOutcome struct {
	text string
	err  error
	ok   bool
}

// dictationFeed tails the WAV the microphone is already writing. PCM is handed
// to a resident worker without touching its pipe on the recorder goroutine.
// If that worker never becomes usable, the same feed schedules the historical
// bounded WAV previews.
type dictationFeed struct {
	mu            sync.Mutex
	transcribeCtx context.Context
	transcribe    func(context.Context, []byte, string) (string, error)
	onPartial     func(string)
	partialWG     sync.WaitGroup

	stream     *audio.PCMStream
	finalCh    chan streamOutcome
	eventsDone chan struct{}
	finishing  bool
	failed     bool
	failWG     sync.WaitGroup
	offset     int
	dataOff    int
	enqueued   int

	preview       bool
	previewCount  int
	nextPreview   time.Time
	snapshots     chan []byte
	previewCancel context.CancelFunc
	previewDone   chan struct{}
}

func newDictationFeed(transcribeCtx context.Context, transcribe func(context.Context, []byte, string) (string, error), onPartial func(string)) *dictationFeed {
	return &dictationFeed{
		transcribeCtx: transcribeCtx,
		transcribe:    transcribe,
		onPartial:     onPartial,
		nextPreview:   time.Now().Add(voicePreviewInitial),
	}
}

func captureLocalDictation(recordCtx, transcribeCtx context.Context, duration time.Duration, onPartial func(string), local *audio.LocalTranscriber, transcribe func(context.Context, []byte, string) (string, error)) (string, error) {
	feed := newDictationFeed(transcribeCtx, transcribe, onPartial)
	defer feed.shutdown()

	startCtx, startCancel := context.WithCancel(transcribeCtx)
	defer startCancel()
	openCh := make(chan streamOpen)
	go func() {
		opened, err := local.StartPCMStream(startCtx)
		if startCtx.Err() != nil {
			if opened != nil {
				opened.Close()
			}
			return
		}
		select {
		case openCh <- streamOpen{stream: opened, err: err}:
		case <-startCtx.Done():
			if opened != nil {
				opened.Close()
			}
		}
	}()

	// Stopping transcription (process shutdown) must also release the microphone
	// even when the caller used a different record context.
	micCtx, micCancel := context.WithCancel(recordCtx)
	defer micCancel()
	go func() {
		select {
		case <-transcribeCtx.Done():
			micCancel()
		case <-micCtx.Done():
		}
	}()

	recCh := make(chan recResult, 1)
	go func() {
		data, err := recordMicrophoneProgress(micCtx, duration, feed.observe)
		recCh <- recResult{data: data, err: err}
	}()

	readyTimeout := local.Timeout()
	if readyTimeout <= 0 || readyTimeout > voiceReadyLimit {
		readyTimeout = voiceReadyLimit
	}
	readyTimer := time.NewTimer(readyTimeout)
	defer readyTimer.Stop()

	var (
		rec     recResult
		gotRec  bool
		gotOpen bool
	)
	for !gotRec || !gotOpen {
		var openC <-chan streamOpen
		var timerC <-chan time.Time
		var recC <-chan recResult
		if !gotOpen {
			openC = openCh
			timerC = readyTimer.C
		}
		if !gotRec {
			recC = recCh
		}
		select {
		case op := <-openC:
			gotOpen = true
			feed.attach(op)
		case <-timerC:
			gotOpen = true
			startCancel()
			feed.enablePreviews()
		case rec = <-recC:
			gotRec = true
			if !gotOpen {
				// A short recording should not wait for a cold model import.
				// The opener observes cancellation and closes a late stream.
				gotOpen = true
				startCancel()
			}
		}
	}

	if rec.err != nil {
		return "", rec.err
	}
	if err := transcribeCtx.Err(); err != nil {
		return "", err
	}
	feed.awaitResidentSettled()
	text, used, err := feed.finishResident(transcribeCtx, local.Timeout(), rec.data)
	if used && err == nil {
		return text, nil
	}
	if transcribeCtx.Err() != nil {
		return "", transcribeCtx.Err()
	}
	// The in-flight preview, if any, holds the model lock. Stop it before the
	// one-shot final pass, which has its own deadline inside Transcribe.
	feed.stopPreviews()
	return transcribe(transcribeCtx, rec.data, "audio/wav")
}

func (f *dictationFeed) attach(op streamOpen) {
	if op.err != nil || op.stream == nil {
		if op.stream != nil {
			op.stream.Close()
		}
		f.enablePreviews()
		return
	}
	f.mu.Lock()
	if f.failed || f.preview {
		f.mu.Unlock()
		op.stream.Close()
		return
	}
	f.stream = op.stream
	f.finalCh = make(chan streamOutcome, 1)
	eventsDone := make(chan struct{})
	f.eventsDone = eventsDone
	f.mu.Unlock()
	go f.readEvents(op.stream, eventsDone)
}

func (f *dictationFeed) readEvents(st *audio.PCMStream, eventsDone chan struct{}) {
	defer close(eventsDone)
	var result streamOutcome
	sent := false
	send := func() {
		if sent || f.finalCh == nil {
			return
		}
		sent = true
		f.finalCh <- result
	}
	for ev := range st.Events() {
		if result.ok || result.err != nil {
			continue
		}
		switch ev.Kind {
		case audio.PCMPartial:
			f.emitPartial(ev.Text)
		case audio.PCMFinal:
			f.mu.Lock()
			finishing := f.finishing
			f.mu.Unlock()
			if !finishing {
				continue
			}
			result = streamOutcome{text: ev.Text, ok: true}
		case audio.PCMError:
			msg := strings.TrimSpace(ev.Text)
			if msg == "" {
				msg = "transcrição incremental falhou"
			}
			result.err = errors.New(msg)
		}
		if result.ok || result.err != nil {
			send()
		}
	}
	if !result.ok && result.err == nil {
		result.err = errors.New("transcrição incremental interrompida")
	}
	send()
}

// observe copies newly written PCM toward the resident worker and, in fallback
// mode, schedules a bounded WAV preview. It reports whether another full chunk
// is still unread so the recorder can drain the finished file. The call does
// not write the worker pipe.
func (f *dictationFeed) observe(path string) bool {
	f.mu.Lock()
	st := f.stream
	preview := f.preview
	offset := f.offset
	dataOff := f.dataOff
	count := f.previewCount
	next := f.nextPreview
	f.mu.Unlock()

	more := false
	if st != nil {
		pcm, newOff, newData, err := readPCMTail(path, dataOff, offset)
		if err == nil {
			if newData != 0 {
				f.mu.Lock()
				if f.dataOff == 0 {
					f.dataOff = newData
				}
				f.mu.Unlock()
			}
			if len(pcm) > 0 {
				if err := st.EnqueuePCM(pcm); err != nil {
					f.failResident()
				} else {
					f.mu.Lock()
					if f.stream == st && f.offset == offset {
						f.offset = newOff
						f.enqueued += len(pcm)
						if newData != 0 {
							f.dataOff = newData
						}
						more = len(pcm) == voiceTailRead
					}
					f.mu.Unlock()
				}
			}
		}
	}

	if !preview || count >= voicePreviewMax || time.Now().Before(next) {
		return more
	}
	wav, err := readRecordingPreview(path)
	if err != nil {
		return more
	}
	f.mu.Lock()
	if f.preview && f.previewCount == count && f.previewCancel != nil {
		f.previewCount++
		f.nextPreview = time.Now().Add(voicePreviewGap)
		f.mu.Unlock()
		f.offerPreview(wav)
		return more
	}
	f.mu.Unlock()
	return more
}

func (f *dictationFeed) failResident() {
	f.mu.Lock()
	if f.failed {
		f.mu.Unlock()
		return
	}
	f.failed = true
	st := f.stream
	f.stream = nil
	f.failWG.Add(1)
	f.mu.Unlock()
	go func() {
		defer f.failWG.Done()
		if st != nil {
			st.Close()
		}
		f.enablePreviews()
	}()
}

func (f *dictationFeed) awaitResidentSettled() {
	f.mu.Lock()
	failed := f.failed
	f.mu.Unlock()
	if failed {
		f.failWG.Wait()
	}
}

func (f *dictationFeed) enablePreviews() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.preview {
		return
	}
	f.preview = true
	snapshots := make(chan []byte, 1)
	f.snapshots = snapshots
	ctx, cancel := context.WithCancel(f.transcribeCtx)
	f.previewCancel = cancel
	done := make(chan struct{})
	f.previewDone = done
	go f.previewLoop(ctx, snapshots, done)
}

func (f *dictationFeed) previewLoop(ctx context.Context, snapshots <-chan []byte, done chan struct{}) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case wav, ok := <-snapshots:
			if !ok || ctx.Err() != nil {
				return
			}
			text, err := f.transcribe(ctx, wav, "audio/wav")
			if err == nil && ctx.Err() == nil {
				f.emitPartial(text)
			}
		}
	}
}

func (f *dictationFeed) offerPreview(wav []byte) {
	f.mu.Lock()
	ch := f.snapshots
	active := f.previewCancel != nil
	f.mu.Unlock()
	if ch == nil || !active {
		return
	}
	select {
	case ch <- wav:
		return
	default:
	}
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- wav:
	default:
	}
}

func (f *dictationFeed) emitPartial(text string) {
	text = strings.TrimSpace(text)
	if text == "" || !utf8.ValidString(text) {
		return
	}
	f.mu.Lock()
	cb := f.onPartial
	if cb != nil {
		f.partialWG.Add(1)
	}
	f.mu.Unlock()
	if cb == nil {
		return
	}
	defer f.partialWG.Done()
	cb(text)
}

// finishResident waits for the worker's full-buffer transcript until timeout.
// A timeout or worker error is reported so the caller can fall back to the
// one-shot path. used is false when no resident worker is open.
func (f *dictationFeed) finishResident(ctx context.Context, timeout time.Duration, wav []byte) (string, bool, error) {
	f.mu.Lock()
	st := f.stream
	finalCh := f.finalCh
	enqueued := f.enqueued
	if st != nil {
		f.finishing = true
		f.stream = nil
	}
	f.mu.Unlock()
	if st == nil {
		return "", false, nil
	}
	// A final from a worker that received only a prefix is never a valid
	// transcription. The recorder's final WAV is the authority on its size.
	expected, ok := finalPCMBytes(wav)
	if !ok || expected == 0 || enqueued != expected {
		st.Close()
		return "", true, errors.New("worker incremental não recebeu todo o áudio; usando transcrição final")
	}
	st.CloseInput()
	if timeout <= 0 {
		timeout = time.Minute
	}
	finalCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var outcome streamOutcome
	select {
	case outcome = <-finalCh:
	case <-finalCtx.Done():
		st.Close()
		if ctx.Err() != nil {
			return "", true, ctx.Err()
		}
		return "", true, finalCtx.Err()
	}
	st.Close()
	if outcome.err != nil {
		return "", true, outcome.err
	}
	if !outcome.ok {
		return "", true, errors.New("transcrição incremental sem resultado")
	}
	return outcome.text, true, nil
}

func finalPCMBytes(wav []byte) (int, bool) {
	if len(wav) < 44 {
		return 0, false
	}
	prefix := wav[:min(len(wav), 4096)]
	offset, ok := wavDataStart(prefix)
	if !ok || offset > len(wav) {
		return 0, false
	}
	return (len(wav) - offset) &^ 1, true
}

func (f *dictationFeed) stopPreviews() {
	f.mu.Lock()
	cancel := f.previewCancel
	done := f.previewDone
	f.previewCancel = nil
	f.previewDone = nil
	f.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (f *dictationFeed) shutdown() {
	f.awaitResidentSettled()
	f.mu.Lock()
	f.onPartial = nil
	st := f.stream
	f.stream = nil
	eventsDone := f.eventsDone
	f.mu.Unlock()
	if st != nil {
		st.Close()
	}
	f.stopPreviews()
	f.partialWG.Wait()
	if eventsDone != nil {
		select {
		case <-eventsDone:
		case <-time.After(3 * time.Second):
		}
	}
}

// readPCMTail returns PCM16 bytes appended to the WAV since sent (a file
// offset). dataOff is the PCM start discovered from the header; pass 0 until
// it is known. A short read cap keeps the microphone loop from copying a
// multi-megabyte catch-up in one tick; the next tick continues at newSent.
func readPCMTail(path string, dataOff, sent int) (pcm []byte, newSent, newData int, err error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, sent, dataOff, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, sent, dataOff, err
	}
	if dataOff <= 0 {
		prefix := make([]byte, 1024)
		n, readErr := file.Read(prefix)
		if n == 0 {
			return nil, sent, 0, readErr
		}
		off, ok := wavDataStart(prefix[:n])
		if !ok {
			return nil, sent, 0, nil
		}
		dataOff = off
	}
	size := int(info.Size())
	if size < dataOff {
		return nil, sent, dataOff, nil
	}
	end := dataOff + ((size - dataOff) &^ 1)
	if sent < dataOff {
		sent = dataOff
	}
	if end <= sent {
		return nil, sent, dataOff, nil
	}
	if end-sent > voiceTailRead {
		end = sent + voiceTailRead
	}
	buf := make([]byte, end-sent)
	if _, err := file.ReadAt(buf, int64(sent)); err != nil {
		return nil, sent, dataOff, err
	}
	return buf, end, dataOff, nil
}

// wavDataStart locates the PCM payload. arecord publishes the data chunk
// before the samples exist and may leave its length at the maximum uint32
// until the process exits, so the chunk size is not trusted.
func wavDataStart(prefix []byte) (int, bool) {
	if len(prefix) < 12 || string(prefix[:4]) != "RIFF" || string(prefix[8:12]) != "WAVE" {
		return 0, false
	}
	offset := 12
	for offset+8 <= len(prefix) && offset <= 4096 {
		if string(prefix[offset:offset+4]) == "data" {
			return offset + 8, true
		}
		size := int(binary.LittleEndian.Uint32(prefix[offset+4 : offset+8]))
		if size < 0 || size > 1<<20 {
			return 0, false
		}
		step := 8 + size
		if size%2 == 1 {
			step++
		}
		if step < 8 || offset+step > len(prefix) {
			return 0, false
		}
		offset += step
	}
	return 0, false
}
