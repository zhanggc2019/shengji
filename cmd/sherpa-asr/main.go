//go:build windows && amd64 && cgo && sherpa

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	sherpa "github.com/k2-fsa/sherpa-onnx-go-windows"
	"github.com/zhanggc2019/shengji/internal/transcript"
)

type options struct {
	family   string
	dir      string
	audio    string
	language string
}

const pcmRate = 16000

// main runs one local inference request and writes provider-neutral segments to stdout.
func main() {
	var opt options
	flag.StringVar(&opt.family, "family", "sensevoice", "SenseVoice or Paraformer")
	flag.StringVar(&opt.dir, "model-dir", "", "directory containing the official ONNX model and tokens.txt")
	flag.StringVar(&opt.audio, "audio", "", "input PCM16 mono WAV")
	flag.StringVar(&opt.language, "language", "auto", "recognition language")
	flag.Parse()
	segments, err := recognize(opt)
	if err == nil {
		err = json.NewEncoder(os.Stdout).Encode(segments)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// modelFile prefers the official int8 export and falls back to the full precision package.
func modelFile(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", errors.New("请填写模型包目录")
	}
	for _, name := range []string{"model.int8.onnx", "model.onnx"} {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, nil
		}
	}
	return "", errors.New("模型目录缺少 model.int8.onnx 或 model.onnx")
}

// makeRecognizer loads a SenseVoice or Paraformer package using Sherpa's native API.
func makeRecognizer(opt options) (*sherpa.OfflineRecognizer, error) {
	model, err := modelFile(opt.dir)
	if err != nil {
		return nil, err
	}
	tokens := filepath.Join(opt.dir, "tokens.txt")
	info, err := os.Stat(tokens)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("模型目录缺少 tokens.txt")
	}
	config := sherpa.OfflineRecognizerConfig{
		FeatConfig:     sherpa.FeatureConfig{SampleRate: pcmRate, FeatureDim: 80},
		ModelConfig:    sherpa.OfflineModelConfig{Tokens: tokens, NumThreads: min(4, runtime.NumCPU()), Provider: "cpu"},
		DecodingMethod: "greedy_search",
	}
	switch opt.family {
	case "sensevoice":
		language := strings.TrimSpace(opt.language)
		if language == "" {
			language = "auto"
		}
		config.ModelConfig.SenseVoice = sherpa.OfflineSenseVoiceModelConfig{
			Model: model, Language: language, UseInverseTextNormalization: 1,
		}
	case "paraformer":
		config.ModelConfig.Paraformer = sherpa.OfflineParaformerModelConfig{Model: model}
	default:
		return nil, errors.New("本机模型仅支持 SenseVoice 和 Paraformer")
	}
	rec := sherpa.NewOfflineRecognizer(&config)
	if rec == nil {
		return nil, errors.New("无法加载 ONNX 模型，请核对模型家族与文件")
	}
	return rec, nil
}

// recognize reads WAV and limits inference to short spans of audio.
func recognize(opt options) ([]transcript.Segment, error) {
	wave := sherpa.ReadWave(opt.audio)
	if wave == nil || wave.SampleRate < 1 || len(wave.Samples) == 0 {
		return nil, errors.New("本机模式需要 16 位单声道 PCM WAV")
	}
	rec, err := makeRecognizer(opt)
	if err != nil {
		return nil, err
	}
	defer sherpa.DeleteOfflineRecognizer(rec)
	vadPath := filepath.Join(opt.dir, "silero_vad.onnx")
	if _, err = os.Stat(vadPath); err == nil && wave.SampleRate == pcmRate {
		return detectSpeech(rec, wave, vadPath)
	}
	// Short fixed spans bound model memory when the optional VAD file is absent.
	result := []transcript.Segment{}
	step := wave.SampleRate * 25
	for start := 0; start < len(wave.Samples); start += step {
		segment, err := decode(rec, wave.Samples[start:min(start+step, len(wave.Samples))], start, wave.SampleRate)
		if err != nil {
			return nil, err
		}
		if segment.Text != "" {
			result = append(result, segment)
		}
	}
	return result, nil
}

// detectSpeech follows Sherpa's VAD plus offline ASR pattern with bounded speech spans.
func detectSpeech(rec *sherpa.OfflineRecognizer, wave *sherpa.Wave, vadPath string) ([]transcript.Segment, error) {
	config := sherpa.VadModelConfig{
		SileroVad:  sherpa.SileroVadModelConfig{Model: vadPath, Threshold: 0.5, MinSilenceDuration: 0.35, MinSpeechDuration: 0.2, WindowSize: 512, MaxSpeechDuration: 25},
		SampleRate: pcmRate, NumThreads: 1, Provider: "cpu",
	}
	vad := sherpa.NewVoiceActivityDetector(&config, 30)
	if vad == nil {
		return nil, errors.New("无法加载 Silero VAD 模型")
	}
	defer sherpa.DeleteVoiceActivityDetector(vad)
	output := []transcript.Segment{}
	drain := func() error {
		for !vad.IsEmpty() {
			speech := vad.Front()
			vad.Pop()
			if len(speech.Samples) == 0 {
				continue
			}
			segment, err := decode(rec, speech.Samples, speech.Start, wave.SampleRate)
			if err != nil {
				return err
			}
			if segment.Text != "" {
				output = append(output, segment)
			}
		}
		return nil
	}
	const step = 512
	for start := 0; start < len(wave.Samples); start += step {
		samples := wave.Samples[start:min(start+step, len(wave.Samples))]
		if len(samples) < step {
			block := make([]float32, step)
			copy(block, samples)
			samples = block
		}
		vad.AcceptWaveform(samples)
		if err := drain(); err != nil {
			return nil, err
		}
	}
	vad.Flush()
	if err := drain(); err != nil {
		return nil, err
	}
	return output, nil
}

// decode converts inference output into timed segments and only uses tokens with genuine model timestamps.
func decode(rec *sherpa.OfflineRecognizer, samples []float32, offset, rate int) (transcript.Segment, error) {
	stream := sherpa.NewOfflineStream(rec)
	if stream == nil {
		return transcript.Segment{}, errors.New("无法创建 Sherpa-ONNX 识别流")
	}
	defer sherpa.DeleteOfflineStream(stream)
	stream.AcceptWaveform(rate, samples)
	rec.Decode(stream)
	raw := stream.GetResult()
	if raw == nil {
		return transcript.Segment{}, errors.New("推理未返回结果")
	}
	start, end := float64(offset)/float64(rate), float64(offset+len(samples))/float64(rate)
	text := strings.TrimSpace(raw.Text)
	if text == "" {
		return transcript.Segment{}, nil
	}
	return transcript.Segment{Text: text, Start: start, End: end, Words: timedTokens(raw, start, end)}, nil
}

// timedTokens keeps valid model token onsets and bounds inferred end points to the audio segment.
func timedTokens(raw *sherpa.OfflineRecognizerResult, start, end float64) []transcript.Word {
	words := []transcript.Word{}
	if len(raw.Tokens) != len(raw.Timestamps) {
		return words
	}
	for i, token := range raw.Tokens {
		label := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(token, "▁"), "@@"))
		t0 := float64(raw.Timestamps[i]) + start
		if label == "" || strings.HasPrefix(label, "<|") || strings.HasPrefix(label, "<blank") || math.IsNaN(t0) || t0 < start || t0 >= end {
			continue
		}
		t1 := end
		if i+1 < len(raw.Timestamps) {
			t1 = min(end, float64(raw.Timestamps[i+1])+start)
		}
		if i < len(raw.Durations) && raw.Durations[i] > 0 {
			t1 = min(end, t0+float64(raw.Durations[i]))
		}
		if t1 > t0 {
			words = append(words, transcript.Word{Text: label, Start: t0, End: t1})
		}
	}
	return words
}
