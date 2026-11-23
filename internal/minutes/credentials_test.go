package minutes

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModelCredentials(t *testing.T) {
	const asrKey, llmKey = "fixture-asr-key", "fixture-summary-key"
	const settingsPath = "/api/settings?credentials=1"
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/audio/transcriptions":
			if r.Header.Get("Authorization") != "Bearer "+asrKey {
				t.Error("transcription did not use the saved key")
			}
			w.Write([]byte(`{"text":"通过转录"}`))
		case "/v1/messages":
			if r.Header.Get("x-api-key") != llmKey {
				t.Error("Claude did not use the saved key")
			}
			w.Write([]byte(`{"content":[{"text":"通过纪要"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	root := t.TempDir()
	a, err := Open(root, nil, &HTTPAI{ASRKeys: map[string]string{"openai": "old-asr-key"}, LLMKey: "old-summary-key"})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	cfg := defaultSettings
	cfg.ASRProvider, cfg.ASRURL, cfg.ASRModel = "openai", provider.URL, "whisper-1"
	cfg.Provider, cfg.LLMURL, cfg.LLMModel = "claude", provider.URL, "model"
	input := struct {
		Settings
		ASRKey string `json:"asr_api_key"`
		LLMKey string `json:"llm_api_key"`
	}{cfg, asrKey, llmKey}
	h := a.Handler(nil, true)
	checkStatus := func(raw []byte, wantASR, wantLLM bool) {
		t.Helper()
		var result struct{ Credentials credentialStatus }
		if err := json.Unmarshal(raw, &result); err != nil || !result.Credentials.CanManage ||
			result.Credentials.ASRSaved != wantASR || result.Credentials.LLMSaved != wantLLM ||
			bytes.Contains(raw, []byte(asrKey)) || bytes.Contains(raw, []byte(llmKey)) {
			t.Fatal("settings did not return the expected credential status without secrets")
		}
	}
	checkStatus(call(t, h, "PUT", settingsPath, input, 200), true, true)
	checkStatus(call(t, h, "PUT", settingsPath, cfg, 200), true, true)

	var encrypted []byte
	if err = a.db.QueryRow("SELECT secret FROM model_credentials WHERE scope=?", cfg.asrCredentialScope()).Scan(&encrypted); err != nil ||
		bytes.Contains(encrypted, []byte(asrKey)) {
		t.Fatal("transcription credential was not stored encrypted")
	}
	other := cfg
	other.ASRURL += "/different"
	if key, err := a.credential(other.asrCredentialScope()); err != nil || key != "" {
		t.Fatal("transcription credential carried over to a different URL")
	}

	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("PUT", "http://192.168.1.10/api/settings", bytes.NewReader(raw))
	req.RemoteAddr = "192.168.1.20:55000"
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("LAN credential change status: %d", res.Code)
	}
	a.Close()

	reopened, err := Open(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	h = reopened.Handler(nil, true)
	checkStatus(call(t, h, "GET", settingsPath, nil, 200), true, true)
	wav := append(wavHeader(4), make([]byte, 4)...)
	result, err := reopened.TranscribeAudio(t.Context(), bytes.NewReader(wav), "audio.wav", "zh")
	if err != nil || result.Text != "通过转录" {
		t.Fatalf("transcription failed: %v", err)
	}
	id := newID()
	if _, err = reopened.createMeeting(id, "API Key 测试", "", modeMeeting, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.db.Exec("UPDATE meetings SET segments=? WHERE id=?", `[{"text":"原文","end":1}]`, id); err != nil {
		t.Fatal(err)
	}
	path := "/api/meetings/" + id
	call(t, h, "POST", path+"/jobs/summary", nil, 202)
	if m := waitJob(t, h, path, "completed"); m.Summary != "通过纪要" {
		t.Fatal("summary did not use the saved key")
	}
	var snapshot string
	if err = reopened.db.QueryRow("SELECT config FROM jobs WHERE meeting_id=?", id).Scan(&snapshot); err != nil ||
		bytes.Contains([]byte(snapshot), []byte(llmKey)) {
		t.Fatal("summary job did not keep credentials out of its settings snapshot")
	}
	checkStatus(call(t, h, "PUT", settingsPath, struct {
		Settings
		LLMKey string `json:"llm_api_key"`
	}{cfg, ""}, 200), true, false)
}
