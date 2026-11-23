package minutes

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

const blockFrames = sampleRate / 50

var performanceCounter = syscall.NewLazyDLL("kernel32.dll").NewProc("QueryPerformanceCounter")
var performanceFrequency = syscall.NewLazyDLL("kernel32.dll").NewProc("QueryPerformanceFrequency")

type audioEndpoint struct {
	client  *wca.IAudioClient
	reader  *wca.IAudioCaptureClient
	packets []pcmPacket
}

type pcmPacket struct {
	start   int64
	samples []int16
}

// queryPerformance reads Windows' clock used by both capture endpoints.
func queryPerformance(proc *syscall.LazyProc) int64 {
	var value int64
	proc.Call(uintptr(unsafe.Pointer(&value)))
	return value
}

// toReferenceTime converts QPC ticks to the 100 ns units returned in WASAPI packets.
func toReferenceTime(ticks, frequency int64) int64 {
	return ticks/frequency*10_000_000 + ticks%frequency*10_000_000/frequency
}

// openEndpoint lets Windows convert the device format to our common 16 kHz PCM format.
func openEndpoint(en *wca.IMMDeviceEnumerator, flow uint32, loopback bool) (*audioEndpoint, error) {
	var device *wca.IMMDevice
	if err := en.GetDefaultAudioEndpoint(flow, wca.EConsole, &device); err != nil {
		return nil, err
	}
	defer device.Release()
	var client *wca.IAudioClient
	if err := device.Activate(wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &client); err != nil {
		return nil, err
	}
	flags := uint32(wca.AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM | wca.AUDCLNT_STREAMFLAGS_SRC_DEFAULT_QUALITY)
	if loopback {
		flags |= wca.AUDCLNT_STREAMFLAGS_LOOPBACK
	}
	format := wca.WAVEFORMATEX{
		WFormatTag: 1, NChannels: 1, NSamplesPerSec: sampleRate, NAvgBytesPerSec: sampleRate * 2, NBlockAlign: 2, WBitsPerSample: 16,
	}
	if err := client.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, flags, 2_000_000, 0, &format, nil); err != nil {
		client.Release()
		return nil, err
	}
	var reader *wca.IAudioCaptureClient
	if err := client.GetService(wca.IID_IAudioCaptureClient, &reader); err != nil {
		client.Release()
		return nil, err
	}
	if err := client.Start(); err != nil {
		reader.Release()
		client.Release()
		return nil, err
	}
	return &audioEndpoint{client: client, reader: reader}, nil
}

// close stops the endpoint on the COM thread that created it.
func (e *audioEndpoint) close() {
	e.client.Stop()
	e.reader.Release()
	e.client.Release()
}

// drain copies only pending packets and releases WASAPI buffers immediately.
func (e *audioEndpoint) drain(origin, now int64) error {
	for {
		var pending uint32
		if err := e.reader.GetNextPacketSize(&pending); err != nil || pending == 0 {
			return err
		}
		var data *byte
		var frames, flags uint32
		var timestamp uint64
		if err := e.reader.GetBuffer(&data, &frames, &flags, nil, &timestamp); err != nil {
			return err
		}
		start := (int64(timestamp) - origin) * sampleRate / 10_000_000
		if timestamp == 0 || flags&wca.AUDCLNT_BUFFERFLAGS_TIMESTAMP_ERROR != 0 {
			start = (now-origin)*sampleRate/10_000_000 - int64(frames)
			if n := len(e.packets); n > 0 {
				previous := e.packets[n-1]
				start = max(start, previous.start+int64(len(previous.samples)))
			}
		}
		if data != nil && frames > 0 && flags&wca.AUDCLNT_BUFFERFLAGS_SILENT == 0 {
			source := unsafe.Slice((*int16)(unsafe.Pointer(data)), int(frames))
			pcm := append([]int16(nil), source...)
			e.packets = append(e.packets, pcmPacket{start: start, samples: pcm})
		}
		if err := e.reader.ReleaseBuffer(frames); err != nil {
			return err
		}
	}
}

// block extracts one time-aligned block and fills gaps with silence.
func (e *audioEndpoint) block(start int64, dest []int16) {
	clear(dest)
	end := start + int64(len(dest))
	keep := e.packets[:0]
	for _, packet := range e.packets {
		packetEnd := packet.start + int64(len(packet.samples))
		left, right := max(start, packet.start), min(end, packetEnd)
		if left < right {
			copy(dest[left-start:right-start], packet.samples[left-packet.start:right-packet.start])
		}
		if packetEnd > end {
			keep = append(keep, packet)
		}
	}
	clear(e.packets[len(keep):])
	e.packets = keep
}

// WindowsCapture reads the default microphone and optional render loopback on one COM thread.
func WindowsCapture(ctx context.Context, options CaptureOptions, ready chan<- error, write func([]int16, []int16) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		var oleErr *ole.OleError
		if !errors.As(err, &oleErr) || oleErr.Code() != 1 {
			return fmt.Errorf("启动 Windows 音频: %w", err)
		}
	}
	defer ole.CoUninitialize()
	var en *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &en); err != nil {
		return err
	}
	defer en.Release()
	mic, err := openEndpoint(en, wca.ECapture, false)
	if err != nil {
		return fmt.Errorf("打开默认麦克风: %w", err)
	}
	defer mic.close()
	var system *audioEndpoint
	var systemSamples []int16
	if options.SystemAudio {
		system, err = openEndpoint(en, wca.ERender, true)
		if err != nil {
			return fmt.Errorf("打开系统回环: %w", err)
		}
		defer system.close()
		systemSamples = make([]int16, blockFrames)
	}
	frequency := queryPerformance(performanceFrequency)
	if frequency <= 0 {
		return errors.New("无法读取 Windows 音频时钟")
	}
	origin := toReferenceTime(queryPerformance(performanceCounter), frequency)
	ready <- nil
	return captureBlocks(ctx, mic, system, systemSamples, frequency, origin, write)
}

// captureBlocks holds a short jitter buffer so the microphone and loopback share a timeline.
func captureBlocks(ctx context.Context, mic, system *audioEndpoint, systemSamples []int16, frequency, origin int64, write func([]int16, []int16) error) error {
	micSamples := make([]int16, blockFrames)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	var cursor int64
	emit := func(stopping bool) error {
		now := toReferenceTime(queryPerformance(performanceCounter), frequency)
		if err := mic.drain(origin, now); err != nil {
			return fmt.Errorf("读取麦克风: %w", err)
		}
		if system != nil {
			if err := system.drain(origin, now); err != nil {
				return fmt.Errorf("读取系统回环: %w", err)
			}
		}
		target := (now - origin) * sampleRate / 10_000_000
		if !stopping {
			target -= sampleRate * 60 / 1000
		}
		for cursor+blockFrames <= target {
			mic.block(cursor, micSamples)
			if system != nil {
				system.block(cursor, systemSamples)
			}
			if err := write(micSamples, systemSamples); err != nil {
				return err
			}
			cursor += blockFrames
		}
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return emit(true)
		case <-tick.C:
			if err := emit(false); err != nil {
				return err
			}
		}
	}
}
