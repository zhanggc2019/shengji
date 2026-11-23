package minutes

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// postVideo uploads a real fixture through the same multipart contract as the browser.
func postVideo(t *testing.T, handler http.Handler, fixture string) *httptest.ResponseRecorder {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("audio", fixture)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://localhost/api/import", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

// TestVideoImport verifies first-track extraction, playback and cleanup when there is no audio.
func TestVideoImport(t *testing.T) {
	if _, err := findFFmpeg(); err != nil {
		t.Skip("FFmpeg not available")
	}
	root := t.TempDir()
	app, err := Open(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	handler := app.Handler(nil, false)
	res := postVideo(t, handler, "video-with-audio.mp4")
	if res.Code != http.StatusCreated {
		t.Fatalf("import returned %d: %s", res.Code, res.Body)
	}
	var meeting Meeting
	if err = json.Unmarshal(res.Body.Bytes(), &meeting); err != nil {
		t.Fatal(err)
	}
	if meeting.Title != "video-with-audio" || !meeting.HasAudio || meeting.Duration < .4 || meeting.Duration > .7 {
		t.Fatalf("unexpected meeting: %+v", meeting)
	}
	audio := call(t, handler, http.MethodGet, "/api/meetings/"+meeting.ID+"/audio", nil, http.StatusOK)
	if len(audio) <= 44 || audioType("decoded.wav", audio) != ".wav" ||
		binary.LittleEndian.Uint16(audio[22:]) != 1 || binary.LittleEndian.Uint32(audio[24:]) != sampleRate ||
		bytes.Count(audio[44:], []byte{0}) == len(audio)-44 {
		t.Fatal("decoded WAV does not contain 16 kHz mono audio")
	}
	if res = postVideo(t, handler, "video-without-audio.mp4"); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("video with no audio returned %d: %s", res.Code, res.Body)
	}
	files, err := os.ReadDir(filepath.Join(root, "audio"))
	if err != nil || len(files) != 1 || files[0].Name() != meeting.ID+".wav" {
		t.Fatalf("unexpected files after import: %v, %v", files, err)
	}
}

// TestVideoImportNeedsFFmpeg verifies the dependency error and rollback for a portable build.
func TestVideoImportNeedsFFmpeg(t *testing.T) {
	t.Setenv("SHENGJI_FFMPEG", filepath.Join(t.TempDir(), "missing-ffmpeg"))
	root := t.TempDir()
	app, err := Open(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	res := postVideo(t, app.Handler(nil, false), "video-with-audio.mp4")
	if res.Code != http.StatusUnsupportedMediaType || !bytes.Contains(res.Body.Bytes(), []byte("FFmpeg")) {
		t.Fatalf("missing FFmpeg returned %d: %s", res.Code, res.Body)
	}
	files, err := os.ReadDir(filepath.Join(root, "audio"))
	if err != nil || len(files) != 0 {
		t.Fatalf("uploaded video was not cleaned up: %v, %v", files, err)
	}
}

// TestImportCapabilities distinguishes missing and broken executables, without exposing paths.
func TestImportCapabilities(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ffmpeg.exe")
	t.Setenv("SHENGJI_FFMPEG", path)
	app, err := Open(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	handler := app.Handler(nil, false)
	for _, want := range []string{"missing", "unusable"} {
		if want == "unusable" {
			if err = os.WriteFile(path, []byte("invalid executable"), 0700); err != nil {
				t.Fatal(err)
			}
		}
		var status map[string]string
		body := call(t, handler, http.MethodGet, "/api/import/capabilities", nil, http.StatusOK)
		if err = json.Unmarshal(body, &status); err != nil {
			t.Fatal(err)
		}
		if status["ffmpeg"] != want || status["platform"] != runtime.GOOS || len(status) != 2 {
			t.Fatalf("wanted %s capability, got %v", want, status)
		}
	}
}
