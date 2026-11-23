package minutes

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ASRProfile supplies the connection defaults displayed by both user interfaces.
type ASRProfile struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	URL   string `json:"url"`
	Model string `json:"model"`
	Local bool   `json:"local"`
}

var asrProfiles = []ASRProfile{
	{ID: "oddasr", Name: "OddASR", URL: "http://127.0.0.1:9002", Model: "whisper-1"},
	{ID: "openai", Name: "OpenAI 兼容", URL: "https://api.openai.com", Model: "gpt-transcribe"},
	{ID: "groq", Name: "Groq", URL: "https://api.groq.com/openai", Model: "whisper-large-v3-turbo"},
	{ID: "sherpa", Name: "本机转录（离线）", Model: "sensevoice", Local: true},
}

// transcriptionSource exposes only the model identity from the completed job's settings snapshot.
func transcriptionSource(snapshot string) *TranscriptionSource {
	var config Settings
	if snapshot == "" || json.Unmarshal([]byte(snapshot), &config) != nil {
		return nil
	}
	return sourceForSettings(config)
}

// sourceForSettings names the configured backend without exposing connection details.
func sourceForSettings(config Settings) *TranscriptionSource {
	if strings.TrimSpace(config.ASRModel) == "" {
		return nil
	}
	source := &TranscriptionSource{Provider: config.ASRProvider, Model: config.ASRModel}
	if source.Provider == "" {
		source.Provider = "oddasr"
	}
	provider, model := source.Provider, strings.Join(strings.Fields(source.Model), " ")
	for _, profile := range asrProfiles {
		if profile.ID == provider {
			provider = profile.Name
			break
		}
	}
	if source.Provider == "sherpa" {
		switch source.Model {
		case "sensevoice":
			model = "SenseVoice"
		case "paraformer":
			model = "Paraformer"
		}
	}
	source.Label = provider + " · " + model
	return source
}

type asrPlan struct {
	url      string
	key      string
	format   string
	model    string
	language string
	words    bool
	limit    int64
}

// transcriptionPlan isolates provider-specific output formats and credentials from the job worker.
func (p *HTTPAI) transcriptionPlan(c Settings, language string) (asrPlan, error) {
	plan := asrPlan{url: endpoint(c.ASRURL, "/v1/audio/transcriptions"), format: "verbose_json", model: c.ASRModel, language: language, words: true}
	switch c.ASRProvider {
	case "", "oddasr":
		plan.key = p.ASRKey
	case "openai":
		plan.key = p.ASRKeys["openai"]
		plan.limit = 24_000_000
		if c.ASRModel == "gpt-4o-transcribe-diarize" {
			plan.format, plan.words = "diarized_json", false
		} else if strings.HasPrefix(c.ASRModel, "gpt-") {
			plan.format, plan.words = "json", false
		}
	case "groq":
		plan.key = p.ASRKeys["groq"]
		plan.limit = 24_000_000
	default:
		return plan, errors.New("不支持的转录服务")
	}
	return plan, nil
}

// Transcribe chooses a remote endpoint or the optional native local worker.
func (p *HTTPAI) Transcribe(ctx context.Context, c Settings, path, language string) ([]Segment, error) {
	if c.ASRProvider == "sherpa" {
		return p.transcribeLocal(ctx, c, path, language)
	}
	plan, err := p.transcriptionPlan(c, language)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if plan.limit > 0 && info.Size() > plan.limit {
		return transcribeLongWAV(ctx, plan, f, info.Size())
	}
	return requestASR(ctx, plan, filepath.Base(path), f)
}

// sendAudio streams file sections rather than duplicating large recordings in memory.
func sendAudio(pipe *io.PipeWriter, w *multipart.Writer, source io.Reader, name string, plan asrPlan) {
	part, err := w.CreateFormFile("file", name)
	if err == nil {
		_, err = io.Copy(part, source)
	}
	if err == nil {
		err = w.WriteField("model", plan.model)
	}
	if err == nil {
		err = w.WriteField("response_format", plan.format)
	}
	if err == nil && plan.words {
		err = w.WriteField("timestamp_granularities[]", "word")
	}
	if err == nil && plan.words {
		err = w.WriteField("timestamp_granularities[]", "segment")
	}
	if err == nil && plan.format == "diarized_json" {
		err = w.WriteField("chunking_strategy", "auto")
	}
	if err == nil && plan.language != "" && plan.language != "auto" {
		err = w.WriteField("language", plan.language)
	}
	if err == nil {
		err = w.Close()
	}
	pipe.CloseWithError(err)
}

// requestASR sends one bounded audio part and normalizes the provider response.
func requestASR(ctx context.Context, plan asrPlan, name string, source io.Reader) ([]Segment, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	req, err := http.NewRequestWithContext(ctx, "POST", plan.url, reader)
	if err != nil {
		writer.Close()
		return nil, errors.New("转录服务地址无效")
	}
	form := multipart.NewWriter(writer)
	req.Header.Set("Content-Type", form.FormDataContentType())
	bearer(req, plan.key)
	go sendAudio(writer, form, source, name, plan)
	var data asrResult
	if err = providerJSON(req, &data); err != nil {
		return nil, err
	}
	return normalizeASR(data), nil
}

// transcribeLongWAV partitions native PCM WAVs for services with per-request file limits.
func transcribeLongWAV(ctx context.Context, plan asrPlan, f *os.File, size int64) ([]Segment, error) {
	head := make([]byte, 44)
	if _, err := f.ReadAt(head, 0); err != nil || size < 44 ||
		!bytes.Equal(head, wavHeader(binary.LittleEndian.Uint32(head[40:]))) ||
		int64(binary.LittleEndian.Uint32(head[40:])) != size-44 {
		return nil, errors.New("该 ASR 的单次上传上限为 24 MB。应用录制的 WAV 可自动分段，其他格式请先分割或改用本机模型")
	}
	step := (plan.limit - 44) &^ 1
	var all []Segment
	for offset := int64(0); offset < size-44; offset += step {
		amount := min(step, size-44-offset)
		reader := io.MultiReader(bytes.NewReader(wavHeader(uint32(amount))), io.NewSectionReader(f, 44+offset, amount))
		segments, err := requestASR(ctx, plan, "segment.wav", reader)
		if err != nil {
			return nil, fmt.Errorf("录音分段 %d 转录失败: %w", offset/step+1, err)
		}
		for i := range segments {
			delta := float64(offset) / (sampleRate * 2)
			segments[i].Start += delta
			segments[i].End += delta
			for j := range segments[i].Words {
				segments[i].Words[j].Start += delta
				segments[i].Words[j].End += delta
			}
		}
		all = append(all, segments...)
	}
	return all, nil
}

// transcribeLocal runs native inference in a child process so cancel or failure does not affect the recorder.
func (p *HTTPAI) transcribeLocal(ctx context.Context, c Settings, path, language string) ([]Segment, error) {
	if strings.TrimSpace(c.ASRModelDir) == "" {
		return nil, errors.New("请在「模型设置」下载本机转录模型并填写模型包目录")
	}
	exe := p.NativeASR
	if exe == "" {
		mainExe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		exe = filepath.Join(filepath.Dir(mainExe), "sherpa", "OddMinutes-sherpa.exe")
	}
	if _, err := os.Stat(exe); err != nil {
		return nil, errors.New("未找到 Sherpa-ONNX 本机组件，请安装离线版或配置 SHENGJI_SHERPA_EXE")
	}
	cmd := exec.CommandContext(ctx, exe, "--family", c.ASRModel, "--model-dir", c.ASRModelDir, "--audio", path, "--language", language)
	hideNativeConsole(cmd)
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	result, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		message := strings.TrimSpace(diagnostics.String())
		if message == "" {
			message = err.Error()
		}
		runes := []rune(message)
		message = string(runes[:min(len(runes), 500)])
		return nil, fmt.Errorf("Sherpa-ONNX 推理失败: %s", message)
	}
	var segments []Segment
	if err = json.Unmarshal(result, &segments); err != nil {
		return nil, errors.New("离线识别返回了无效结果")
	}
	return segments, nil
}
