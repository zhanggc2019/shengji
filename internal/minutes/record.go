package minutes

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const sampleRate = 16000
const maxPCMBytes = (1<<32 - 36 - 1) &^ 1

var errRecordingBusy = errors.New("已有录音或任务正在处理")
var errRecordState = errors.New("会议没有处于可执行该操作的录音状态")
var errNoAudio = errors.New("没有采集到可保存的音频")
var errNoCapture = errors.New("当前构建不支持本机音频采集")

// CaptureOptions controls mixing and the input sources used by the recorder.
type CaptureOptions struct {
	SystemAudio bool `json:"system_audio"`
	AutoGain    bool `json:"auto_gain"`
}

// Capture runs the native input until cancellation and reports startup readiness separately.
type Capture func(context.Context, CaptureOptions, chan<- error, func([]int16, []int16) error) error

type recordView struct {
	MeetingID  string  `json:"meeting_id,omitempty"`
	State      string  `json:"state"`
	Seconds    float64 `json:"seconds"`
	MicPeak    float64 `json:"mic_peak"`
	SystemPeak float64 `json:"system_peak"`
	Error      string  `json:"error,omitempty"`
}

type recording struct {
	mu         sync.Mutex
	file       *os.File
	amount     uint32
	syncAt     time.Time
	options    CaptureOptions
	view       recordView
	cancel     context.CancelFunc
	done       chan struct{}
	runErr     error
	micGain    float64
	systemGain float64
}

// wavHeader describes 16 kHz mono PCM, playable directly without a converter.
func wavHeader(size uint32) []byte {
	b := make([]byte, 44)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], 36+size)
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], sampleRate)
	binary.LittleEndian.PutUint32(b[28:], sampleRate*2)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], size)
	return b
}

// gainFor smooths optional automatic gain without amplifying digital silence.
func gainFor(samples []int16, prior float64) float64 {
	if len(samples) == 0 {
		return prior
	}
	var sum float64
	for _, sample := range samples {
		v := float64(sample)
		sum += v * v
	}
	rms := math.Sqrt(sum / float64(len(samples)))
	if rms < 50 {
		return prior
	}
	return prior*0.9 + min(4, max(0.25, 2800/rms))*0.1
}

// peakLevel normalizes a block for the live peak display.
func peakLevel(samples []int16) float64 {
	var peak float64
	for _, sample := range samples {
		peak = max(peak, math.Abs(float64(sample)))
	}
	return peak / 32768
}

// appendFrames writes one aligned block without keeping the recording in RAM.
func (r *recording) appendFrames(mic, system []int16) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.view.MicPeak = peakLevel(mic)
	r.view.SystemPeak = peakLevel(system)
	if r.view.State == "paused" {
		return nil
	}
	n := max(len(mic), len(system))
	if n == 0 {
		return nil
	}
	if uint64(r.amount)+uint64(n)*2 > maxPCMBytes {
		return errors.New("WAV 录音达到 4 GiB 上限")
	}
	if r.options.AutoGain {
		r.micGain = gainFor(mic, r.micGain)
		r.systemGain = gainFor(system, r.systemGain)
	}
	data := make([]byte, n*2)
	for i := range n {
		var v float64
		if i < len(mic) {
			v = float64(mic[i]) * r.micGain
		}
		if i < len(system) {
			v += float64(system[i]) * r.systemGain
			if i < len(mic) {
				v /= 2
			}
		}
		binary.LittleEndian.PutUint16(data[i*2:], uint16(int16(min(32767, max(-32768, v)))))
	}
	if _, err := r.file.Write(data); err != nil {
		return err
	}
	r.amount += uint32(len(data))
	r.view.Seconds = float64(r.amount) / (sampleRate * 2)
	if time.Since(r.syncAt) < time.Second {
		return nil
	}
	r.syncAt = time.Now()
	if _, err := r.file.WriteAt(wavHeader(r.amount), 0); err != nil {
		return err
	}
	return r.file.Sync()
}

// startRecording reserves the audio device and creates the recovery file before accepting samples.
func (a *App) startRecording(m Meeting, options CaptureOptions) error {
	if a.capture == nil {
		return errNoCapture
	}
	if a.recording != nil || m.HasAudio {
		return errRecordingBusy
	}
	active, err := a.jobActive(m.ID)
	if err != nil {
		return err
	}
	if active {
		return errRecordingBusy
	}
	f, err := os.OpenFile(filepath.Join(a.dir, "audio", m.ID+".wav.part"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			f.Close()
			os.Remove(f.Name())
		}
	}()
	if _, err = f.Write(wavHeader(0)); err != nil {
		return err
	}
	if _, err = a.db.Exec("UPDATE meetings SET recording='recording' WHERE id=?", m.ID); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(a.ctx)
	r := &recording{file: f, options: options, view: recordView{MeetingID: m.ID, State: "recording"}, cancel: cancel, done: make(chan struct{}), syncAt: time.Now(), micGain: 1, systemGain: 1}
	ready := make(chan error, 1)
	go func() {
		defer close(r.done)
		r.runErr = a.capture(ctx, options, ready, r.appendFrames)
	}()
	select {
	case err = <-ready:
	case <-r.done:
		err = r.runErr
		if err == nil {
			err = errNoAudio
		}
	}
	if err != nil {
		cancel()
		<-r.done
		_, _ = a.db.Exec("UPDATE meetings SET recording='error' WHERE id=?", m.ID)
		a.notify()
		return err
	}
	keep = true
	a.recording = r
	a.notify()
	return nil
}

// togglePause serializes UI commands with frame writing and preserves a gapless timeline.
func (a *App) togglePause(id, from, to string) error {
	r := a.recording
	if r == nil || r.view.MeetingID != id {
		return errRecordState
	}
	select {
	case <-r.done:
		return errRecordState
	default:
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.view.State != from {
		return errRecordState
	}
	if _, err := a.db.Exec("UPDATE meetings SET recording=? WHERE id=?", to, id); err != nil {
		return err
	}
	r.view.State = to
	a.notify()
	return nil
}

// stopRecording ensures that a client can only stop the meeting it has open.
func (a *App) stopRecording(id string) error {
	if a.recording == nil || a.recording.view.MeetingID != id {
		return errRecordState
	}
	return a.finishRecording()
}

// finishRecording closes the capture before publishing a repaired WAV and database metadata.
func (a *App) finishRecording() error {
	r := a.recording
	r.cancel()
	<-r.done
	closeErr := r.file.Close()
	a.recording = nil
	defer a.notify()
	if closeErr != nil {
		return closeErr
	}
	state := "stopped"
	if r.runErr != nil {
		state = "recovered"
	}
	err := a.publishWAV(r.view.MeetingID, state)
	if err != nil {
		return err
	}
	return r.runErr
}

// publishWAV repairs audio metadata before atomically revealing the local file.
func (a *App) publishWAV(id, state string) error {
	name := id + ".wav"
	target := filepath.Join(a.dir, "audio", name)
	staged := target + ".part"
	f, err := os.OpenFile(staged, os.O_RDWR, 0600)
	hasStage := err == nil
	if errors.Is(err, os.ErrNotExist) {
		f, err = os.OpenFile(target, os.O_RDWR, 0600)
	}
	if errors.Is(err, os.ErrNotExist) {
		_, updateErr := a.db.Exec("UPDATE meetings SET recording='error' WHERE id=?", id)
		return errors.Join(errNoAudio, updateErr)
	}
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	size := max(0, info.Size()-44) &^ 1
	if size == 0 {
		f.Close()
		if hasStage {
			_ = os.Remove(staged)
		}
		_, updateErr := a.db.Exec("UPDATE meetings SET recording='error' WHERE id=?", id)
		return errors.Join(errNoAudio, updateErr)
	}
	if uint64(size) > maxPCMBytes {
		f.Close()
		return errors.New("录音超出 WAV 大小上限")
	}
	if err = f.Truncate(size + 44); err == nil {
		_, err = f.WriteAt(wavHeader(uint32(size)), 0)
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if hasStage {
		if err = os.Rename(staged, target); err != nil {
			return err
		}
	}
	_, err = a.db.Exec("UPDATE meetings SET recording=?,audio=?,duration=? WHERE id=?", state, name, float64(size)/(sampleRate*2), id)
	return err
}

// recoverRecordings finishes partially written files left by an unclean exit.
func (a *App) recoverRecordings() error {
	rows, err := a.db.Query("SELECT id FROM meetings WHERE recording IN ('recording','paused')")
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if !validID(id) {
			rows.Close()
			return fmt.Errorf("invalid meeting ID: %q", id)
		}
		ids = append(ids, id)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, id := range ids {
		if err = a.publishWAV(id, "recovered"); err != nil && !errors.Is(err, errNoAudio) {
			return err
		}
	}
	return nil
}

// liveRecording returns a small snapshot without querying SQLite.
func (a *App) liveRecording() recordView {
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.recording
	if r == nil {
		return recordView{State: "idle"}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	value := r.view
	select {
	case <-r.done:
		if r.runErr != nil {
			value.State = "error"
			value.Error = r.runErr.Error()
		}
	default:
	}
	return value
}
