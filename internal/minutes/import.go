package minutes

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

var errMediaUnavailable = errors.New("提取视频音轨需要 FFmpeg。请在服务端安装或配置 SHENGJI_FFMPEG")
var errDecodedTooLarge = errors.New("视频解码后的音频超过 500 MiB")

// audioType validates audio and video containers by both name and signature.
func audioType(name string, head []byte) string {
	ext := strings.ToLower(filepath.Ext(name))
	has := func(prefix string) bool { return bytes.HasPrefix(head, []byte(prefix)) }
	switch ext {
	case ".wav":
		if len(head) >= 12 && has("RIFF") && string(head[8:12]) == "WAVE" {
			return ext
		}
	case ".mp3":
		if has("ID3") || (len(head) >= 2 && head[0] == 0xff && head[1]&0xe0 == 0xe0) {
			return ext
		}
	case ".m4a", ".mp4":
		if len(head) >= 8 && string(head[4:8]) == "ftyp" {
			return ext
		}
	case ".mov", ".m4v":
		if len(head) >= 8 {
			switch string(head[4:8]) {
			case "ftyp", "moov", "mdat", "wide", "free":
				return ext
			}
		}
	case ".ogg", ".opus":
		if has("OggS") {
			return ext
		}
	case ".flac":
		if has("fLaC") {
			return ext
		}
	case ".webm", ".mkv":
		if has("\x1a\x45\xdf\xa3") {
			return ext
		}
	case ".avi":
		if len(head) >= 12 && has("RIFF") && string(head[8:12]) == "AVI " {
			return ext
		}
	}
	return ""
}

// waveDuration estimates standard PCM WAV duration for the meeting list.
func waveDuration(head []byte, size int64) float64 {
	if len(head) < 44 || string(head[12:16]) != "fmt " || string(head[36:40]) != "data" {
		return 0
	}
	rate := binary.LittleEndian.Uint32(head[28:])
	if rate == 0 {
		return 0
	}
	return float64(min(int64(binary.LittleEndian.Uint32(head[40:])), max(0, size-44))) / float64(rate)
}

// upload streams a bounded file to local storage and publishes the meeting only after the file is durable.
func (a *App) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAudioBytes+(1<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		fail(w, 400, "请选择一个音频或视频文件")
		return
	}
	id := newID()
	var title, filename, saved, mode string
	var duration float64
	keep := false
	defer func() {
		if !keep && saved != "" {
			os.Remove(saved)
		}
	}()
	for {
		part, readErr := reader.NextPart()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			fail(w, 400, "读取上传失败或文件太大")
			return
		}
		switch part.FormName() {
		case "mode":
			value, err := io.ReadAll(io.LimitReader(part, 21))
			if err != nil || len(value) > 20 {
				fail(w, 400, "记录模式无效")
				return
			}
			mode = string(value)
		case "title":
			value, err := io.ReadAll(io.LimitReader(part, 2049))
			if err != nil || len(value) > 2048 {
				fail(w, 400, "会议标题无效")
				return
			}
			title = string(value)
		case "audio":
			if saved != "" || part.FileName() == "" {
				fail(w, 400, "每次上传请求仅支持一个音视频文件")
				return
			}
			filename = part.FileName()
			head, err := io.ReadAll(io.LimitReader(part, 512))
			if err != nil {
				fail(w, 400, "无法读取音视频文件")
				return
			}
			ext := audioType(filename, head)
			if ext == "" {
				fail(w, 415, "音视频格式不受支持")
				return
			}
			saved = filepath.Join(a.dir, "audio", id+ext)
			f, err := os.OpenFile(saved, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				saved = ""
				serverFailure(w, err)
				return
			}
			n, copyErr := io.Copy(f, io.LimitReader(io.MultiReader(bytes.NewReader(head), part), maxAudioBytes+1))
			if copyErr == nil {
				copyErr = f.Sync()
			}
			if err = errors.Join(copyErr, f.Close()); err != nil {
				serverFailure(w, err)
				return
			}
			if n > maxAudioBytes {
				fail(w, 413, "文件大小超过 500 MiB")
				return
			}
			if ext == ".wav" {
				duration = waveDuration(head, n)
			}
		}
		part.Close()
	}
	if saved == "" {
		fail(w, 400, "请选择一个音频或视频文件")
		return
	}
	if strings.TrimSpace(title) == "" {
		title = strings.TrimSuffix(filename, filepath.Ext(filename))
	}
	title, ok := titleOf(title)
	if !ok {
		fail(w, 400, "会议标题须为 1–255 个字符")
		return
	}
	mode, ok = recordMode(mode)
	if !ok {
		fail(w, 400, "记录模式无效")
		return
	}
	if format := videoDemuxer(filepath.Ext(saved)); format != "" {
		source := saved
		destination := filepath.Join(a.dir, "audio", id+".wav")
		wav, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			serverFailure(w, err)
			return
		}
		defer os.Remove(source)
		saved = destination
		duration, err = extractVideoAudio(r.Context(), source, format, wav)
		if err != nil {
			wav.Close()
			if r.Context().Err() != nil {
				return
			}
			var diskError *os.PathError
			switch {
			case errors.As(err, &diskError):
				serverFailure(w, err)
			case errors.Is(err, errMediaUnavailable):
				fail(w, 415, err.Error())
			case errors.Is(err, errDecodedTooLarge):
				fail(w, 413, err.Error())
			default:
				log.Printf("提取视频音轨失败: %v", err)
				fail(w, 422, "无法提取视频音轨。请确认视频包含系统支持的音轨")
			}
			return
		}
		if err = errors.Join(wav.Sync(), wav.Close()); err != nil {
			serverFailure(w, err)
			return
		}
	}
	if r.Context().Err() != nil {
		return
	}
	m, err := a.createMeeting(id, title, filepath.Base(saved), mode, duration)
	if err != nil {
		serverFailure(w, err)
		return
	}
	keep = true
	respond(w, 201, m)
}
