package minutes

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestASRBackends checks the request differences and shared segment contract for each provider.
func TestASRBackends(t *testing.T) {
	tests := []struct {
		provider, model, path, format, field, key string
	}{
		{"oddasr", "whisper-1", "/v1/audio/transcriptions", "verbose_json", "word", "odd-key"},
		{"groq", "whisper-large-v3-turbo", "/v1/audio/transcriptions", "verbose_json", "word", "groq-key"},
		{"openai", "gpt-transcribe", "/v1/audio/transcriptions", "json", "", "openai-key"},
		{"openai", "gpt-4o-transcribe-diarize", "/v1/audio/transcriptions", "diarized_json", "", "openai-key"},
		{"openai", "whisper-1", "/v1/audio/transcriptions", "verbose_json", "word", "openai-key"},
	}
	for _, test := range tests {
		t.Run(test.provider+"/"+test.model, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != test.path {
					t.Errorf("path = %s, want %s", r.URL.Path, test.path)
				}
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				for name, want := range map[string]string{"model": test.model, "response_format": test.format, "timestamp_granularities[]": test.field, "language": "zh"} {
					if got := r.FormValue(name); got != want {
						t.Errorf("%s = %q, want %q", name, got, want)
					}
				}
				wantAuth := ""
				if test.key != "" {
					wantAuth = "Bearer " + test.key
				}
				if got := r.Header.Get("Authorization"); got != wantAuth {
					t.Errorf("authorization did not match the selected profile")
				}
				fmt.Fprint(w, "{\"segments\":[{\"text\":\"纪要内容\",\"start\":0.2,\"end\":1.5,\"speaker\":\"A\"}]}")
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "voice.wav")
			if err := os.WriteFile(path, wavHeader(0), 0600); err != nil {
				t.Fatal(err)
			}
			c := defaultSettings
			c.ASRProvider = test.provider
			c.ASRURL = server.URL
			c.ASRModel = test.model
			if err := validateSettings(c); err != nil {
				t.Fatal(err)
			}
			p := &HTTPAI{ASRKey: "odd-key", ASRKeys: map[string]string{"groq": "groq-key", "openai": "openai-key"}}
			segments, err := p.Transcribe(context.Background(), c, path, "zh")
			if err != nil || len(segments) != 1 || segments[0].End != 1.5 {
				t.Fatalf("bad response: %+v %v", segments, err)
			}
		})
	}
}

// TestASRProviderSettings validates the on-machine endpoint and discovery metadata.
func TestASRProviderSettings(t *testing.T) {
	c := defaultSettings
	c.ASRProvider, c.ASRModel, c.ASRURL, c.ASRModelDir = "sherpa", "sensevoice", "", "models"
	if err := validateSettings(c); err != nil {
		t.Fatal(err)
	}
	c.ASRModel = "unsupported"
	if err := validateSettings(c); err == nil {
		t.Fatal("unsupported on-device model family accepted")
	}
	c.ASRProvider = "unknown"
	if err := validateSettings(c); err == nil {
		t.Fatal("unknown backend accepted")
	}
}

// TestCloudWAVPartition verifies cloud limits without requiring a codec or losing timestamp offsets.
func TestCloudWAVPartition(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		f, _, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer f.Close()
		wav, err := io.ReadAll(f)
		if err != nil || len(wav) > 84 || binary.LittleEndian.Uint32(wav[40:]) != uint32(len(wav)-44) {
			t.Errorf("bad partition: %d %v", len(wav), err)
		}
		fmt.Fprint(w, "{\"segments\":[{\"text\":\"内容\",\"start\":0,\"end\":0.001,\"words\":[{\"word\":\"内容\",\"start\":0,\"end\":0.001}]}]}")
	}))
	defer server.Close()
	f, err := os.Create(filepath.Join(t.TempDir(), "cloud.wav"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err = f.Write(append(wavHeader(100), make([]byte, 100)...)); err != nil {
		t.Fatal(err)
	}
	out, err := transcribeLongWAV(t.Context(), asrPlan{url: server.URL, limit: 84, format: "verbose_json"}, f, 144)
	if err != nil || hits != 3 || len(out) != 3 || out[2].Start != 0.0025 || out[2].Words[0].Start != out[2].Start {
		t.Fatalf("partitioned transcription: hits=%d, result=%+v, err=%v", hits, out, err)
	}
	if _, err = transcribeLongWAV(t.Context(), asrPlan{url: server.URL, limit: 84}, f, 130); err == nil {
		t.Fatal("malformed WAV accepted")
	}
}

// TestMissingOfflineWorker reports a useful error instead of falling back to a cloud provider.
func TestMissingOfflineWorker(t *testing.T) {
	p := HTTPAI{NativeASR: filepath.Join(t.TempDir(), "missing.exe")}
	_, err := p.Transcribe(t.Context(), Settings{ASRProvider: "sherpa", ASRModel: "sensevoice"}, "a.wav", "zh")
	if err == nil || !strings.Contains(err.Error(), "模型包目录") {
		t.Fatal(err)
	}
	_, err = p.Transcribe(t.Context(), Settings{ASRProvider: "sherpa", ASRModel: "sensevoice", ASRModelDir: t.TempDir()}, "a.wav", "zh")
	if err == nil || !strings.Contains(err.Error(), "未找到 Sherpa-ONNX") {
		t.Fatal(err)
	}
}
