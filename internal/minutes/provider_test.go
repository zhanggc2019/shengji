package minutes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHTTPProviders verifies the real request formats against local provider doubles.
func TestHTTPProviders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/audio/transcriptions":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			f, header, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			defer f.Close()
			if header.Filename != "test.wav" || r.FormValue("language") != "zh" {
				t.Errorf("bad form: %s %s", header.Filename, r.FormValue("language"))
			}
			w.Write([]byte("{\"segments\":[{\"text\":\"例会\",\"speaker\":2,\"start\":0,\"end\":1}],\"words\":[{\"word\":\"会议\",\"start\":0.1,\"end\":0.2}]}"))
		case "/v1/chat/completions", "/api/chat", "/v1/messages":
			var in map[string]any
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in["model"] != "test-model" {
				t.Errorf("invalid model request: %v %v", in, err)
			}
			w.Write([]byte("{\"message\":{\"content\":\"本地摘要\"},\"choices\":[{\"message\":{\"content\":\"兼容摘要\"}}],\"content\":[{\"text\":\"Claude 摘要\"}]}"))
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	p := &HTTPAI{}
	path := filepath.Join(t.TempDir(), "test.wav")
	if err := os.WriteFile(path, wavHeader(0), 0600); err != nil {
		t.Fatal(err)
	}
	config := Settings{ASRProvider: "openai", ASRURL: server.URL + "/v1", ASRModel: "test-model", LLMURL: server.URL, LLMModel: "test-model", Provider: "openai"}
	segments, err := p.Transcribe(context.Background(), config, path, "zh")
	if err != nil || len(segments) != 1 || segments[0].Speaker != "2" || len(segments[0].Words) != 1 {
		t.Fatalf("%+v %v", segments, err)
	}
	for provider, want := range map[string]string{"ollama": "本地摘要", "openai": "兼容摘要", "claude": "Claude 摘要"} {
		config.Provider = provider
		reply, err := p.Summarize(context.Background(), config, segments)
		if err != nil || reply != want {
			t.Fatalf("%s: %q, %v", provider, reply, err)
		}
	}
}

// TestSummaryPresets checks fixed vendor routing, model selection and credential isolation.
func TestSummaryPresets(t *testing.T) {
	for _, vendor := range []struct{ id, url, env string }{
		{"deepseek", "https://api.deepseek.com/chat/completions", "SHENGJI_DEEPSEEK_API_KEY"},
		{"zhipu", "https://open.bigmodel.cn/api/paas/v4/chat/completions", "SHENGJI_ZHIPU_API_KEY"},
		{"siliconflow", "https://api.siliconflow.cn/v1/chat/completions", "SHENGJI_SILICONFLOW_API_KEY"},
	} {
		t.Run(vendor.id, func(t *testing.T) {
			profile, ok := summaryProfile(vendor.id)
			if !ok || len(profile.Models) < 2 || profile.KeyEnv != vendor.env {
				t.Fatalf("missing choices for %s", vendor.id)
			}
			config := defaultSettings
			config.Provider, config.LLMModel = vendor.id, profile.Models[1]
			if err := validateSettings(config); err != nil {
				t.Fatal(err)
			}
			config.LLMURL = "http://other.example/v1"
			client := &HTTPAI{LLMKey: "legacy-key"}
			t.Setenv(vendor.env, "")
			if _, err := client.summaryRequest(t.Context(), config, "摘要"); err == nil || !strings.Contains(err.Error(), "模型设置") {
				t.Fatalf("expected provider key setup error: %v", err)
			}
			t.Setenv(vendor.env, "vendor-key")
			req, err := client.summaryRequest(t.Context(), config, "摘要")
			if err != nil {
				t.Fatal(err)
			}
			defer req.Body.Close()
			var payload map[string]any
			if err = json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if req.URL.String() != vendor.url || req.Header.Get("Authorization") != "Bearer vendor-key" || payload["model"] != config.LLMModel {
				t.Fatalf("wrong provider URL or model: %s %v", req.URL, payload["model"])
			}
			config.summaryKey = "saved-key"
			saved, err := client.summaryRequest(t.Context(), config, "摘要")
			if err != nil {
				t.Fatal(err)
			}
			defer saved.Body.Close()
			if saved.Header.Get("Authorization") != "Bearer saved-key" {
				t.Fatal("provider did not use the saved key")
			}
		})
	}
}

// TestChunkAndAudioFormats covers model chunk limits and accepted audio signatures.
func TestChunkAndAudioFormats(t *testing.T) {
	chunks := transcriptChunks([]Segment{{Text: strings.Repeat("字", 12600)}})
	if len(chunks) != 3 {
		t.Fatalf("%d chunks", len(chunks))
	}
	for _, chunk := range chunks {
		if len([]rune(chunk)) > 6000 {
			t.Fatal("chunk too large")
		}
	}
	for _, item := range []struct{ name, signature, want string }{
		{"voice.wav", "RIFF\x00\x00\x00\x00WAVE", ".wav"}, {"voice.MP3", "ID3", ".mp3"}, {"voice.ogg", "OggS", ".ogg"},
		{"voice.flac", "fLaC", ".flac"}, {"voice.m4a", "\x00\x00\x00\x00ftyp", ".m4a"}, {"voice.webm", "\x1a\x45\xdf\xa3", ".webm"},
		{"voice.wav", "not audio", ""}, {"malware.exe", "ID3", ""},
	} {
		if got := audioType(item.name, []byte(item.signature)); got != item.want {
			t.Errorf("%s = %q want %q", item.name, got, item.want)
		}
	}
}
