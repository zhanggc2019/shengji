//go:build windows && wasapi_integration

package minutes

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// playTestTone briefly plays a quiet known waveform for the loopback assertion.
func playTestTone() bool {
	const size = sampleRate * 2 * 3 / 4
	buffer := append(wavHeader(size), make([]byte, size)...)
	for i := range size / 2 {
		env := min(1, float64(min(i, size/2-i))/256)
		value := int16(1700 * env * math.Sin(2*math.Pi*550*float64(i)/sampleRate))
		binary.LittleEndian.PutUint16(buffer[44+i*2:], uint16(value))
	}
	played, _, _ := syscall.NewLazyDLL("winmm.dll").NewProc("PlaySoundW").Call(
		uintptr(unsafe.Pointer(&buffer[0])), 0, 0x0006,
	)
	runtime.KeepAlive(buffer)
	return played != 0
}

// TestWindowsWASAPI verifies the deployed capture implementation with real input and output endpoints.
func TestWindowsWASAPI(t *testing.T) {
	a, err := Open(t.TempDir(), WindowsCapture, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	h := a.Handler(nil, false)
	var m Meeting
	if err = json.Unmarshal(call(t, h, "POST", "/api/meetings", map[string]string{"title": "Windows 实机录音"}, 201), &m); err != nil {
		t.Fatal(err)
	}
	path := "/api/meetings/" + m.ID
	call(t, h, "POST", path+"/record/start", map[string]bool{"system_audio": true}, 200)
	sound := make(chan bool, 1)
	go func() { sound <- playTestTone() }()
	var peak float64
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		peak = max(peak, a.liveRecording().SystemPeak)
		time.Sleep(10 * time.Millisecond)
	}
	if !<-sound || peak <= 0.001 {
		t.Fatalf("test tone was not captured from system output (peak=%f)", peak)
	}
	call(t, h, "POST", path+"/record/pause", nil, 200)
	time.Sleep(200 * time.Millisecond)
	call(t, h, "POST", path+"/record/resume", nil, 200)
	time.Sleep(200 * time.Millisecond)
	call(t, h, "POST", path+"/record/stop", nil, 200)
	data := call(t, h, "GET", path+"/audio", nil, 200)
	if len(data) < sampleRate*2 || binary.LittleEndian.Uint32(data[24:]) != sampleRate {
		t.Fatalf("invalid WAV length %d", len(data))
	}
	t.Logf("sample_rate=%d wav_bytes=%d system_peak=%.3f", sampleRate, len(data), peak)
}
