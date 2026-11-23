package minutes

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// videoDemuxer limits FFmpeg to the container already validated by the uploader.
func videoDemuxer(ext string) string {
	switch ext {
	case ".mp4", ".mov", ".m4v":
		return "mov"
	case ".mkv":
		return "matroska"
	case ".avi":
		return "avi"
	default:
		return ""
	}
}

// findFFmpeg supports a configured executable, a portable sidecar, or the system PATH.
func findFFmpeg() (string, error) {
	if path := os.Getenv("SHENGJI_FFMPEG"); path != "" {
		return exec.LookPath(path)
	}
	if app, err := os.Executable(); err == nil {
		name := "ffmpeg"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		if local, err := exec.LookPath(filepath.Join(filepath.Dir(app), name)); err == nil {
			return local, nil
		}
	}
	return exec.LookPath("ffmpeg")
}

// importCapabilities checks whether the service can launch FFmpeg without exposing its path.
func importCapabilities(w http.ResponseWriter, r *http.Request) {
	status := "missing"
	if path, err := findFFmpeg(); err == nil {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, path, "-version")
		hideNativeConsole(cmd)
		status = "ready"
		if err = cmd.Run(); err != nil {
			status = "unusable"
		}
	}
	respond(w, http.StatusOK, map[string]string{"ffmpeg": status, "platform": runtime.GOOS})
}

// extractVideoAudio decodes the first audio track into a bounded, seekable PCM WAV.
func extractVideoAudio(ctx context.Context, input, format string, output *os.File) (float64, error) {
	ffmpeg, err := findFFmpeg()
	if err != nil {
		return 0, errMediaUnavailable
	}
	if _, err = output.Write(wavHeader(0)); err != nil {
		return 0, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	arguments := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-xerror", "-protocol_whitelist", "file", "-f", format}
	if format == "mov" {
		arguments = append(arguments, "-enable_drefs", "0")
	}
	arguments = append(arguments, "-i", input, "-map", "0:a:0", "-vn", "-sn", "-dn",
		"-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", "-f", "s16le", "pipe:1")
	cmd := exec.CommandContext(ctx, ffmpeg, arguments...)
	hideNativeConsole(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	pcm, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err = cmd.Start(); err != nil {
		return 0, err
	}
	const limit = maxAudioBytes - 44
	size, copyErr := io.Copy(output, io.LimitReader(pcm, limit+1))
	if copyErr != nil || size > limit {
		cancel()
	}
	decodeErr := cmd.Wait()
	switch {
	case copyErr != nil:
		return 0, copyErr
	case size > limit:
		return 0, errDecodedTooLarge
	case decodeErr != nil:
		return 0, fmt.Errorf("FFmpeg: %w: %s", decodeErr, strings.TrimSpace(stderr.String()))
	case size == 0:
		return 0, errNoAudio
	case size%2 != 0:
		return 0, fmt.Errorf("PCM 样本长度无效: %d", size)
	}
	if _, err = output.WriteAt(wavHeader(uint32(size)), 0); err != nil {
		return 0, err
	}
	return float64(size) / (sampleRate * 2), nil
}
