package minutes

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxTranscriptionBytes is the audio input limit for an agent request.
const MaxTranscriptionBytes = maxAudioBytes

var (
	ErrASRAudioTooLarge = errors.New("音频文件超过 500 MiB")
	ErrASRAudioFormat   = errors.New("音频格式不受支持")
	ErrASRLanguage      = errors.New("转录语言须为不超过 20 个字符的语言代码或 auto")
	ErrASREmpty         = errors.New("转录服务没有返回可用文本")
)

// AudioTranscription contains a standalone transcript with times in seconds.
type AudioTranscription struct {
	Text     string               `json:"text"`
	Segments []Segment            `json:"segments"`
	Source   *TranscriptionSource `json:"source"`
	TranscriptionMetrics
}

// ActiveASR reports the model and input formats for standalone transcription.
type ActiveASR struct {
	Source  *TranscriptionSource `json:"source"`
	Formats []string             `json:"formats"`
}

// ActiveASR returns the selected ASR source and the formats accepted by the local service.
func (a *App) ActiveASR() (ActiveASR, error) {
	config, err := a.settings()
	if err != nil {
		return ActiveASR{}, err
	}
	formats := []string{"wav"}
	if config.ASRProvider != "sherpa" {
		formats = append(formats, "mp3", "m4a", "ogg", "opus", "flac", "webm")
	}
	return ActiveASR{Source: sourceForSettings(config), Formats: formats}, nil
}

// TranscribeAudio stages untrusted audio under a generated name and returns a transcript outside the meeting library.
func (a *App) TranscribeAudio(ctx context.Context, input io.Reader, filename, language string) (AudioTranscription, error) {
	language = strings.TrimSpace(language)
	if !utf8.ValidString(language) || utf8.RuneCountInString(language) > 20 {
		return AudioTranscription{}, ErrASRLanguage
	}
	config, err := a.settings()
	if err != nil {
		return AudioTranscription{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	stop := context.AfterFunc(a.ctx, cancel)
	defer stop()
	buffer := bufio.NewReader(input)
	head, err := buffer.Peek(512)
	if err != nil && !errors.Is(err, io.EOF) {
		return AudioTranscription{}, err
	}
	ext := audioType(filename, head)
	if ext == "" || videoDemuxer(ext) != "" {
		return AudioTranscription{}, ErrASRAudioFormat
	}
	if config.ASRProvider == "sherpa" && ext != ".wav" {
		return AudioTranscription{}, errors.Join(ErrASRAudioFormat, errors.New("本机 Sherpa 模型需要 16 位单声道 PCM WAV"))
	}
	file, err := os.CreateTemp("", "shengji-asr-*"+ext)
	if err != nil {
		return AudioTranscription{}, err
	}
	defer os.Remove(file.Name())
	size, copyErr := io.Copy(file, io.LimitReader(buffer, MaxTranscriptionBytes+1))
	if err = errors.Join(copyErr, file.Close()); err != nil {
		return AudioTranscription{}, err
	}
	if size > MaxTranscriptionBytes {
		return AudioTranscription{}, ErrASRAudioTooLarge
	}
	var duration float64
	if ext == ".wav" {
		duration = waveDuration(head, size)
	}
	segments, elapsed, err := a.transcribe(ctx, config, file.Name(), language)
	if err != nil {
		return AudioTranscription{}, err
	}
	if len(segments) == 0 {
		return AudioTranscription{}, ErrASREmpty
	}
	lines := make([]string, 0, len(segments))
	for _, segment := range segments {
		lines = append(lines, segment.Text)
	}
	return AudioTranscription{
		Text: strings.Join(lines, "\n"), Segments: segments, Source: sourceForSettings(config),
		TranscriptionMetrics: TranscriptionMetrics{AudioSeconds: duration, ElapsedMS: elapsed},
	}, nil
}

// transcribe serializes inference shared by library jobs and direct ASR calls.
func (a *App) transcribe(ctx context.Context, config Settings, filename, language string) ([]Segment, int64, error) {
	select {
	case a.asrSlot <- struct{}{}:
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	}
	defer func() { <-a.asrSlot }()
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	started := time.Now()
	segments, err := a.ai.Transcribe(ctx, config, filename, language)
	return segments, time.Since(started).Milliseconds(), err
}
