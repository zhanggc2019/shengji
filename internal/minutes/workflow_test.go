package minutes

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type testAI struct {
	failASR     atomic.Bool
	failSummary atomic.Bool
}

// Transcribe supplies timestamps so the complete edit and playback flow can be tested offline.
func (f *testAI) Transcribe(_ context.Context, _ Settings, _ string, _ string) ([]Segment, error) {
	if f.failASR.Load() {
		return nil, errors.New("测试转录失败")
	}
	return []Segment{{Text: "发布计划", Speaker: "发言人 1", Start: 0, End: 0.02, Words: []Word{{Text: "发布", Start: 0, End: 0.01}}}}, nil
}

// Summarize supplies a summary without contacting a real provider.
func (f *testAI) Summarize(_ context.Context, _ Settings, _ []Segment) (string, error) {
	if f.failSummary.Load() {
		return "", errors.New("测试摘要失败")
	}
	return "## 决策\n发布新版本", nil
}

// fakeCapture exposes a sample callback after signalling that the device is ready.
func fakeCapture(samples chan func([]int16, []int16) error) Capture {
	return func(ctx context.Context, _ CaptureOptions, ready chan<- error, push func([]int16, []int16) error) error {
		samples <- push
		ready <- nil
		<-ctx.Done()
		return nil
	}
}

// call exercises the normal HTTP handler and returns the response body.
func call(t *testing.T, h http.Handler, method, path string, body any, want int) []byte {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, "http://127.0.0.1"+path, reader)
	req.RemoteAddr = "127.0.0.1:55000"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != want {
		t.Fatalf("%s %s = %d; want %d: %s", method, path, res.Code, want, res.Body)
	}
	return res.Body.Bytes()
}

// getMeeting reads the full meeting from its public HTTP representation.
func getMeeting(t *testing.T, h http.Handler, path string) Meeting {
	t.Helper()
	var m Meeting
	if err := json.Unmarshal(call(t, h, "GET", path, nil, 200), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// waitJob waits for the worker's persisted terminal state.
func waitJob(t *testing.T, h http.Handler, path, want string) Meeting {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m := getMeeting(t, h, path)
		if len(m.Jobs) > 0 && m.Jobs[0].State == want {
			return m
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s did not reach %s", path, want)
	return Meeting{}
}

// TestMeetingWorkflow covers recording ownership, pause/resume, ASR, editing, search, export and persistence.
func TestMeetingWorkflow(t *testing.T) {
	root := t.TempDir()
	samples := make(chan func([]int16, []int16) error, 1)
	ai := &testAI{}
	app, err := Open(root, fakeCapture(samples), ai)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	h := app.Handler(nil, false)
	var meeting Meeting
	if err := json.Unmarshal(call(t, h, "POST", "/api/meetings", map[string]string{"title": " 项目例会 "}, 201), &meeting); err != nil {
		t.Fatal(err)
	}
	path := "/api/meetings/" + meeting.ID
	if meeting.Title != "项目例会" {
		t.Fatal(meeting.Title)
	}
	call(t, h, "POST", path+"/record/start", map[string]bool{"system_audio": true}, 200)
	push := <-samples
	call(t, h, "POST", path+"/record/start", nil, 409)
	call(t, h, "DELETE", path, nil, 409)
	mic, system := make([]int16, 320), make([]int16, 320)
	mic[0], system[0] = 1000, 2000
	if err := push(mic, system); err != nil {
		t.Fatal(err)
	}
	call(t, h, "POST", path+"/record/pause", nil, 200)
	call(t, h, "POST", path+"/record/pause", nil, 409)
	if err := push(mic, system); err != nil {
		t.Fatal(err)
	}
	call(t, h, "POST", path+"/record/resume", nil, 200)
	if err := push(mic, system); err != nil {
		t.Fatal(err)
	}
	call(t, h, "POST", path+"/record/stop", nil, 200)
	wav := call(t, h, "GET", path+"/audio", nil, 200)
	if len(wav) != 44+1280 || string(wav[:4]) != "RIFF" || binary.LittleEndian.Uint16(wav[44:]) == 0 {
		t.Fatalf("bad mixed WAV: %d bytes", len(wav))
	}
	call(t, h, "POST", path+"/jobs/summary", nil, 409)
	settings := defaultSettings
	settings.ASRModelDir = root
	call(t, h, "PUT", "/api/settings", settings, 200)
	call(t, h, "POST", path+"/jobs/transcription", map[string]string{"language": "zh"}, 202)
	m := waitJob(t, h, path, "completed")
	if len(m.Segments) != 1 || len(m.Segments[0].Words) != 1 {
		t.Fatalf("segments: %+v", m.Segments)
	}
	call(t, h, "PATCH", path+"/segments/0", map[string]string{"text": "下周发布", "speaker": "张工"}, 200)
	m = getMeeting(t, h, path)
	if len(m.Segments[0].Words) != 0 || m.Segments[0].Speaker != "张工" {
		t.Fatalf("edited segment: %+v", m.Segments[0])
	}
	if list := call(t, h, "GET", "/api/meetings?q=%E4%B8%8B%E5%91%A8", nil, 200); !bytes.Contains(list, []byte(meeting.ID)) {
		t.Fatal(string(list))
	}
	call(t, h, "PATCH", path, map[string]string{"notes": "备忘"}, 200)
	call(t, h, "POST", path+"/jobs/summary", nil, 202)
	m = waitJob(t, h, path, "completed")
	if !strings.Contains(m.Summary, "发布新版本") {
		t.Fatal(m.Summary)
	}
	for _, format := range []string{"txt", "md", "json"} {
		if data := call(t, h, "GET", path+"/export?format="+format, nil, 200); !bytes.Contains(data, []byte("下周发布")) {
			t.Fatal(string(data))
		}
	}
	app.Close()
	reopened, err := Open(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	m = getMeeting(t, reopened.Handler(nil, false), path)
	if !m.HasAudio || m.Notes != "备忘" || m.Summary == "" {
		t.Fatalf("reopened: %+v", m)
	}
	call(t, reopened.Handler(nil, false), "DELETE", path, nil, 204)
	call(t, reopened.Handler(nil, false), "GET", path, nil, 404)
}

// TestUploadAndFailedReplacement checks streaming uploads and that provider failures preserve existing results.
func TestUploadAndFailedReplacement(t *testing.T) {
	ai := &testAI{}
	app, err := Open(t.TempDir(), nil, ai)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	h := app.Handler(nil, false)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	err = writer.WriteField("title", "导入音频")
	if err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("audio", "test.wav")
	if err != nil {
		t.Fatal(err)
	}
	data := append(wavHeader(640), make([]byte, 640)...)
	if _, err = part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "http://localhost/api/import", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != 201 {
		t.Fatalf("import: %d %s", res.Code, res.Body)
	}
	var m Meeting
	if err = json.Unmarshal(res.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	path := "/api/meetings/" + m.ID
	if !m.HasAudio {
		t.Fatal(m)
	}
	settings := defaultSettings
	settings.ASRModelDir = t.TempDir()
	call(t, h, "PUT", "/api/settings", settings, 200)
	call(t, h, "POST", path+"/jobs/transcription", nil, 202)
	prior := waitJob(t, h, path, "completed")
	if prior.TranscriptionSource == nil {
		t.Fatal("expected the successful transcription to retain its model")
	}
	call(t, h, "POST", path+"/jobs/summary", nil, 202)
	want := waitJob(t, h, path, "completed").Summary
	ai.failSummary.Store(true)
	ai.failASR.Store(true)
	settings.ASRModel = "paraformer"
	call(t, h, "PUT", "/api/settings", settings, 200)
	call(t, h, "POST", path+"/jobs/transcription", nil, 202)
	m = waitJob(t, h, path, "failed")
	if len(m.Segments) != 1 {
		t.Fatal("failed ASR discarded old transcript")
	}
	if m.TranscriptionSource == nil || *m.TranscriptionSource != *prior.TranscriptionSource {
		t.Fatalf("failed ASR changed the source of the old transcript: %+v", m.TranscriptionSource)
	}
	call(t, h, "POST", path+"/jobs/summary", nil, 202)
	m = waitJob(t, h, path, "failed")
	if m.Summary != want {
		t.Fatal("failed summary discarded previous summary")
	}
}

// TestPureTranscriptMetrics keeps timing and model history while dropping speaker labels.
func TestPureTranscriptMetrics(t *testing.T) {
	root := t.TempDir()
	a, err := Open(root, nil, &testAI{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	h := a.Handler(nil, false)
	var initial Settings
	if err = json.Unmarshal(call(t, h, "GET", "/api/settings", nil, 200), &initial); err != nil {
		t.Fatal(err)
	}
	if initial.ASRProvider != "sherpa" || initial.ASRModel != "sensevoice" {
		t.Fatalf("unexpected default ASR: %+v", initial)
	}
	id := newID()
	name := id + ".wav"
	if err = os.WriteFile(filepath.Join(root, "audio", name), append(wavHeader(640), make([]byte, 640)...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = a.createMeeting(id, "独白", name, modeTranscript, 1.5); err != nil {
		t.Fatal(err)
	}
	path := "/api/meetings/" + id
	call(t, h, "POST", path+"/jobs/transcription", nil, 409)
	if m := getMeeting(t, h, path); len(m.Jobs) != 0 {
		t.Fatalf("unconfigured local ASR queued a job: %+v", m.Jobs)
	}
	initial.ASRModelDir = root
	for _, model := range []string{"sensevoice", "paraformer"} {
		initial.ASRModel = model
		call(t, h, "PUT", "/api/settings", initial, 200)
		call(t, h, "POST", path+"/jobs/transcription", nil, 202)
		m := waitJob(t, h, path, "completed")
		if m.Mode != modeTranscript || len(m.Segments) == 0 || m.Segments[0].Speaker != "" ||
			m.TranscriptionSource == nil || m.TranscriptionSource.Model != model ||
			m.TranscriptionMetrics == nil || m.TranscriptionMetrics.AudioSeconds != 1.5 {
			t.Fatalf("unexpected transcript: %+v", m)
		}
	}
	m := getMeeting(t, h, path)
	if len(m.Jobs) != 2 || m.Jobs[1].TranscriptionSource == nil || m.Jobs[1].TranscriptionSource.Model != "sensevoice" ||
		m.Jobs[1].TranscriptionMetrics == nil {
		t.Fatalf("history lost the earlier run: %+v", m.Jobs)
	}
	call(t, h, "POST", path+"/jobs/summary", nil, 409)
	call(t, h, "PATCH", path+"/segments/0", map[string]string{"speaker": "人工标注"}, 400)
	export := string(call(t, h, "GET", path+"/export?format=txt", nil, 200))
	for _, wanted := range []string{"Paraformer", "音频时长：1.500 秒", "转录耗时："} {
		if !strings.Contains(export, wanted) {
			t.Fatalf("export omitted %q: %s", wanted, export)
		}
	}
}

// TestWebSocketAndLocalBoundary covers a real WebSocket handshake and cross-origin checks.
func TestWebSocketAndLocalBoundary(t *testing.T) {
	app, err := Open(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	h := app.Handler(nil, false)
	server := httptest.NewServer(h)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	_, data, err := conn.Read(ctx)
	if err != nil || !bytes.Contains(data, []byte("revision")) {
		t.Fatalf("WS = %s, %v", data, err)
	}
	for _, example := range []struct{ url, origin string }{
		{"http://malicious.example/api/meetings", ""},
		{"http://localhost/api/meetings", "https://outside.example"},
	} {
		res := httptest.NewRecorder()
		req := httptest.NewRequest("POST", example.url, strings.NewReader("{}"))
		req.Header.Set("Origin", example.origin)
		h.ServeHTTP(res, req)
		if res.Code != 403 {
			t.Fatalf("cross-origin request = %d", res.Code)
		}
	}
	call(t, h, "POST", "/api/meetings", map[string]string{"title": ""}, 400)
	call(t, h, "PUT", "/api/settings", map[string]string{"asr_url": "file:///etc/passwd"}, 400)
}

// TestRecoverInterruptedWAV verifies the last recorded samples survive an unclean exit.
func TestRecoverInterruptedWAV(t *testing.T) {
	root := t.TempDir()
	app, err := Open(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := app.Handler(nil, false)
	var m Meeting
	if err = json.Unmarshal(call(t, h, "POST", "/api/meetings", map[string]string{"title": "崩溃恢复"}, 201), &m); err != nil {
		t.Fatal(err)
	}
	if _, err = app.db.Exec("UPDATE meetings SET recording='recording' WHERE id=?", m.ID); err != nil {
		t.Fatal(err)
	}
	app.Close()
	if err = os.WriteFile(filepath.Join(root, "audio", m.ID+".wav.part"), append(wavHeader(0), make([]byte, 640)...), 0600); err != nil {
		t.Fatal(err)
	}
	app, err = Open(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	h = app.Handler(nil, false)
	m = getMeeting(t, h, "/api/meetings/"+m.ID)
	if !m.HasAudio || m.Recording != "recovered" {
		t.Fatalf("recovery: %+v", m)
	}
	wav := call(t, h, "GET", "/api/meetings/"+m.ID+"/audio", nil, 200)
	if binary.LittleEndian.Uint32(wav[40:]) != 640 {
		t.Fatal("WAV header was not repaired")
	}
}
