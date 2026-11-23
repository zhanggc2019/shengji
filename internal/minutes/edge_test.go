package minutes

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// TestRouteCompatibility checks the HTTP behaviors that both application entry points rely on.
func TestRouteCompatibility(t *testing.T) {
	a, err := Open(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	handler := a.Handler(fstest.MapFS{
		"index.html": {Data: []byte("<h1>声记</h1>")},
		"app.css":    {Data: []byte("body{}")},
	}, false)
	server := httptest.NewServer(handler)
	defer server.Close()

	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodGet, "/", "<h1>声记</h1>", http.StatusOK},
		{http.MethodGet, "/app.css", "body{}", http.StatusOK},
		{http.MethodHead, "/app.css", "", http.StatusOK},
		{http.MethodHead, "/api/health", "", http.StatusOK},
		{http.MethodGet, "/api/meetings/%2Faudio", "会议不存在", http.StatusNotFound},
		{http.MethodGet, "/api/unknown", "", http.StatusNotFound},
		{http.MethodDelete, "/api/health", "", http.StatusMethodNotAllowed},
	} {
		req, err := http.NewRequestWithContext(t.Context(), tc.method, server.URL+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var body bytes.Buffer
		_, readErr := body.ReadFrom(res.Body)
		res.Body.Close()
		if readErr != nil || res.StatusCode != tc.status || !strings.Contains(body.String(), tc.body) {
			t.Errorf("%s %s: status=%d body=%q error=%v", tc.method, tc.path, res.StatusCode, body.String(), readErr)
		}
		if tc.method == http.MethodHead && body.Len() != 0 {
			t.Errorf("HEAD %s returned a body", tc.path)
		}
		if tc.status == http.StatusMethodNotAllowed && !strings.Contains(res.Header.Get("Allow"), http.MethodGet) {
			t.Errorf("%s %s: missing GET in Allow header", tc.method, tc.path)
		}
		if res.Header.Get("X-Content-Type-Options") != "nosniff" || res.Header.Get("Content-Security-Policy") == "" {
			t.Errorf("%s %s: missing security headers", tc.method, tc.path)
		}
	}
}

// TestLANRequestBoundary exercises LAN access and the origin and address checks.
func TestLANRequestBoundary(t *testing.T) {
	a, err := Open(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	lan, local := a.Handler(nil, true), a.Handler(nil, false)
	for _, tc := range []struct {
		name, host, peer, origin string
		handler                  http.Handler
		status                   int
	}{
		{"same origin on LAN", "192.168.1.10:9011", "192.168.1.20:55000", "http://192.168.1.10:9011", lan, http.StatusCreated},
		{"same origin on loopback", "127.0.0.1:9011", "127.0.0.1:55000", "http://127.0.0.1:9011", lan, http.StatusCreated},
		{"loopback mode", "192.168.1.10:9011", "127.0.0.1:55000", "", local, http.StatusForbidden},
		{"public peer", "192.168.1.10:9011", "203.0.113.20:55000", "", lan, http.StatusForbidden},
		{"unexpected hostname", "outside.example:9011", "192.168.1.20:55000", "", lan, http.StatusForbidden},
		{"cross origin on LAN", "192.168.1.10:9011", "192.168.1.20:55000", "http://192.168.1.30:9011", lan, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://"+tc.host+"/api/meetings", strings.NewReader(`{"title":"局域网测试"}`))
			req.RemoteAddr = tc.peer
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("X-Forwarded-For", "192.168.1.20")
			res := httptest.NewRecorder()
			tc.handler.ServeHTTP(res, req)
			if res.Code != tc.status {
				t.Fatalf("status=%d, want %d, body=%s", res.Code, tc.status, res.Body)
			}
		})
	}
}

// TestSettingsAndMetadata exercises the form data stored by the browser.
func TestSettingsAndMetadata(t *testing.T) {
	a, err := Open(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	h := a.Handler(nil, false)
	var profiles []ASRProfile
	if err := json.Unmarshal(call(t, h, "GET", "/api/asr/profiles", nil, 200), &profiles); err != nil || len(profiles) != 3 ||
		profiles[0].ID != "openai" || profiles[1].ID != "groq" || profiles[2].ID != "sherpa" || !profiles[2].Local {
		t.Fatalf("ASR profiles: %+v %v", profiles, err)
	}
	crossSite := httptest.NewRequest("GET", "http://localhost/api/health", nil)
	crossSite.Header.Set("Sec-Fetch-Site", "cross-site")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, crossSite)
	if r.Code != 403 {
		t.Fatalf("cross-site GET: %d", r.Code)
	}
	settings := defaultSettings
	settings.ASRURL = "https://speech.example/v1"
	settings.Provider = "claude"
	settings.LLMModel = "claude-test"
	call(t, h, "PUT", "/api/settings", settings, 200)
	if data := call(t, h, "GET", "/api/settings", nil, 200); !bytes.Contains(data, []byte("claude-test")) {
		t.Fatal(string(data))
	}
	settings.Provider = "unknown"
	call(t, h, "PUT", "/api/settings", settings, 400)
	settings = defaultSettings
	settings.ASRProvider = legacyASRProvider
	call(t, h, "PUT", "/api/settings", settings, 400)
	settings = defaultSettings
	settings.LLMModel = ""
	call(t, h, "PUT", "/api/settings", settings, 400)
	var m Meeting
	if err = json.Unmarshal(call(t, h, "POST", "/api/meetings", map[string]string{"title": "原始标题"}, 201), &m); err != nil {
		t.Fatal(err)
	}
	path := "/api/meetings/" + m.ID
	call(t, h, "PATCH", path, map[string]string{"title": "新标题"}, 200)
	if got := getMeeting(t, h, path).Title; got != "新标题" {
		t.Fatal(got)
	}
	call(t, h, "PATCH", path, map[string]string{"title": ""}, 400)
	call(t, h, "PATCH", path+"/segments/10", map[string]string{"text": "无效"}, 404)
	call(t, h, "GET", path+"/export?format=pdf", nil, 400)
	call(t, h, "GET", path+"/audio", nil, 404)
	call(t, h, "GET", "/api/meetings/invalid", nil, 404)
	call(t, h, "POST", path+"/jobs/transcription", nil, 409)
	call(t, h, "POST", path+"/jobs/unknown", nil, 404)
	call(t, h, "POST", path+"/record/start", nil, 501)
	call(t, h, "POST", path+"/record/stop", nil, 409)
	call(t, h, "POST", path+"/record/unknown", nil, 404)
	call(t, h, "POST", "/api/import", nil, 400)
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("audio", "not-audio.wav")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte("invalid"))
	writer.Close()
	req := httptest.NewRequest("POST", "http://localhost/api/import", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != 415 {
		t.Fatal(res.Code)
	}
}

// TestCaptureFailureAndRange verifies unavailable devices and seekable audio.
func TestCaptureFailureAndRange(t *testing.T) {
	root := t.TempDir()
	failing := func(_ context.Context, _ CaptureOptions, _ chan<- error, _ func([]int16, []int16) error) error {
		return errors.New("设备断开")
	}
	a, err := Open(root, failing, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := a.Handler(nil, false)
	var m Meeting
	if err = json.Unmarshal(call(t, h, "POST", "/api/meetings", map[string]string{"title": "设备测试"}, 201), &m); err != nil {
		t.Fatal(err)
	}
	path := "/api/meetings/" + m.ID
	if data := call(t, h, "POST", path+"/record/start", nil, 500); !bytes.Contains(data, []byte("设备断开")) {
		t.Fatal(string(data))
	}
	a.Close()
	samples := make(chan func([]int16, []int16) error, 1)
	a, err = Open(root, fakeCapture(samples), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	h = a.Handler(nil, false)
	call(t, h, "POST", path+"/record/start", nil, 200)
	<-samples
	call(t, h, "POST", path+"/record/stop", nil, 409)
	call(t, h, "POST", path+"/record/start", map[string]bool{"auto_gain": true}, 200)
	push := <-samples
	a.recording.syncAt = time.Now().Add(-2 * time.Second)
	if err = push([]int16{1000, 0, -1000}, nil); err != nil {
		t.Fatal(err)
	}
	call(t, h, "POST", path+"/record/stop", nil, 200)
	req := httptest.NewRequest("GET", "http://localhost"+path+"/audio?download=1", nil)
	req.Header.Set("Range", "bytes=0-3")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != 206 || res.Body.String() != "RIFF" || !strings.Contains(res.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal(res.Code, res.Body.String())
	}
	dup, err := Open(root, nil, nil)
	if err == nil {
		dup.Close()
		t.Fatal("second process could write the active library")
	}
	call(t, h, "DELETE", path, nil, 204)
	if matches, _ := filepath.Glob(filepath.Join(root, "audio", m.ID+"*")); len(matches) != 0 {
		t.Fatal(matches)
	}
}

// TestNormalizeASREdges handles segment-only, text-only and invalid timestamp responses.
func TestNormalizeASREdges(t *testing.T) {
	var result asrResult
	data := []byte("{\"text\":\"整段内容\",\"duration\":2,\"words\":[{\"word\":\"整段\",\"start\":0.3,\"end\":0.9},{\"word\":\"无效\",\"start\":5,\"end\":0}]}")
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	segments := normalizeASR(result)
	if len(segments) != 1 || segments[0].End != 2 || len(segments[0].Words) != 1 {
		t.Fatal(segments)
	}
	for _, url := range []string{"", "ftp://example.com", "https://key@host/v1", "https://host?q=1"} {
		s := defaultSettings
		s.LLMURL = url
		if validateSettings(s) == nil {
			t.Fatalf("accepted %s", url)
		}
	}
}

type observeAI struct {
	seen chan Settings
	hold bool
}

// Transcribe optionally waits for shutdown so the same durable job can be observed after restart.
func (p observeAI) Transcribe(ctx context.Context, c Settings, _ string, _ string) ([]Segment, error) {
	p.seen <- c
	if p.hold {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return []Segment{{Text: "恢复任务", End: 0.02}}, nil
}

// Summarize is unused by the restart scenario.
func (p observeAI) Summarize(_ context.Context, _ Settings, _ []Segment) (string, error) {
	return "", errors.New("unexpected summary")
}

// TestQueuedProviderSurvivesRestart protects meetings from silent rerouting after a settings change.
func TestQueuedProviderSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	observed := make(chan Settings, 2)
	a, err := Open(root, nil, observeAI{seen: observed, hold: true})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	h := a.Handler(nil, false)
	cfg := defaultSettings
	cfg.ASRProvider, cfg.ASRURL, cfg.ASRModel = "groq", "https://api.groq.com/openai", "whisper-large-v3-turbo"
	call(t, h, "PUT", "/api/settings", cfg, 200)
	id := newID()
	name := id + ".wav"
	if err = os.WriteFile(filepath.Join(root, "audio", name), append(wavHeader(640), make([]byte, 640)...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = a.createMeeting(id, "旧任务", name, modeMeeting, 0.02); err != nil {
		t.Fatal(err)
	}
	path := "/api/meetings/" + id
	call(t, h, "POST", path+"/jobs/transcription", nil, 202)
	select {
	case first := <-observed:
		if first.ASRProvider != "groq" {
			t.Fatalf("queued provider changed: %+v", first)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	call(t, h, "PUT", "/api/settings", defaultSettings, 200)
	a.Close()
	reopened, err := Open(root, nil, observeAI{seen: observed})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	h = reopened.Handler(nil, false)
	meeting := waitJob(t, h, path, "completed")
	if chosen := <-observed; chosen.ASRProvider != "groq" || chosen.ASRModel != cfg.ASRModel {
		t.Fatalf("restart rerouted the task: %+v", chosen)
	}
	if source := meeting.TranscriptionSource; source == nil || source.Provider != "groq" || source.Model != cfg.ASRModel {
		t.Fatalf("transcript source did not follow the completed job: %+v", source)
	}
	for _, format := range []string{"md", "txt", "json"} {
		if data := call(t, h, "GET", path+"/export?format="+format, nil, 200); !bytes.Contains(data, []byte(cfg.ASRModel)) {
			t.Fatalf("%s export omitted the original ASR model", format)
		}
	}
}

// TestUpgradeSettingsV1 verifies older saved settings and schema can still be read.
func TestUpgradeSettingsV1(t *testing.T) {
	root := t.TempDir()
	a, err := Open(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.Close()
	old, err := sql.Open("sqlite", filepath.Join(root, "oddminutes.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = old.Exec(`ALTER TABLE jobs DROP COLUMN config;
		ALTER TABLE jobs DROP COLUMN elapsed_ms; ALTER TABLE jobs DROP COLUMN audio_seconds;
		ALTER TABLE meetings DROP COLUMN mode; PRAGMA user_version=1`); err != nil {
		old.Close()
		t.Fatal(err)
	}
	if _, err = old.Exec("INSERT INTO settings(id,value) VALUES(1,?)", `{"asr_url":"http://localhost:9002","asr_model":"whisper-1","llm_model":"old-model"}`); err != nil {
		old.Close()
		t.Fatal(err)
	}
	if err = old.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = Open(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	cfg, err := a.settings()
	if err != nil || cfg.ASRProvider != "sherpa" || cfg.ASRURL != "" || cfg.ASRModel != "sensevoice" || cfg.LLMModel != "old-model" {
		t.Fatalf("v1 settings: %+v %v", cfg, err)
	}
	var version int
	if err = a.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 3 {
		t.Fatalf("schema version: %d, %v", version, err)
	}
}

func TestRetiredASRSettingsAndJobs(t *testing.T) {
	root := t.TempDir()
	observed := make(chan Settings, 1)
	a, err := Open(root, nil, observeAI{seen: observed})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	legacy := defaultSettings
	legacy.ASRProvider, legacy.ASRModel, legacy.ASRURL = legacyASRProvider, "whisper-1", "http://localhost:9002"
	legacy.LLMModel = "existing-summary-model"
	if err = a.saveSettings(legacy); err != nil {
		t.Fatal(err)
	}
	id := newID()
	if _, err = a.createMeeting(id, "旧会议", id+".wav", modeMeeting, 1); err != nil {
		t.Fatal(err)
	}
	const segments = `[{"text":"旧转录","start":0,"end":1}]`
	if _, err = a.db.Exec("UPDATE meetings SET segments=? WHERE id=?", segments, id); err != nil {
		t.Fatal(err)
	}
	snapshot, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.Exec(`INSERT INTO jobs(meeting_id,kind,state,config) VALUES
		(?, 'transcription', 'completed', ?), (?, 'transcription', 'running', ?)`, id, string(snapshot), id, string(snapshot)); err != nil {
		t.Fatal(err)
	}
	a.Close()

	reopened, err := Open(root, nil, observeAI{seen: observed})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	cfg, err := reopened.settings()
	if err != nil || cfg.ASRProvider != "sherpa" || cfg.ASRModel != "sensevoice" || cfg.ASRURL != "" || cfg.LLMModel != legacy.LLMModel {
		t.Fatalf("migrated settings: %+v, %v", cfg, err)
	}
	meeting := waitJob(t, reopened.Handler(nil, false), "/api/meetings/"+id, "failed")
	if len(observed) != 0 || !strings.Contains(meeting.Jobs[0].Error, "原转录服务已移除") {
		t.Fatalf("retired job attempted transcription or lost its failure reason: %+v", meeting.Jobs[0])
	}
	if len(meeting.Segments) != 1 || meeting.Segments[0].Text != "旧转录" ||
		meeting.TranscriptionSource == nil || meeting.TranscriptionSource.Label != "OddASR · whisper-1" {
		t.Fatalf("historical transcript or source changed: %+v", meeting)
	}
}
