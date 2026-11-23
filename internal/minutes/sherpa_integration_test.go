//go:build windows && sherpa_integration

package minutes

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"unicode"

	"github.com/zhanggc2019/shengji/internal/configenv"
)

// TestSherpaOfficialModels exercises the offline worker through the same upload and job APIs as the browser.
func TestSherpaOfficialModels(t *testing.T) {
	provider := &HTTPAI{NativeASR: configenv.Get("SHENGJI_OFFLINE_WORKER")}
	for _, entry := range []struct{ family, modelEnv, wavEnv string }{
		{"sensevoice", "SHENGJI_SENSEVOICE_DIR", "SHENGJI_SENSEVOICE_WAV"},
		{"paraformer", "SHENGJI_PARAFORMER_DIR", "SHENGJI_PARAFORMER_WAV"},
	} {
		t.Run(entry.family, func(t *testing.T) {
			dir, wav := configenv.Get(entry.modelEnv), configenv.Get(entry.wavEnv)
			if dir == "" || wav == "" || provider.NativeASR == "" {
				t.Fatal("set the official model, sample WAV and worker paths")
			}
			data, err := os.ReadFile(wav)
			if err != nil {
				t.Fatal(err)
			}
			app, err := Open(t.TempDir(), nil, provider)
			if err != nil {
				t.Fatal(err)
			}
			defer app.Close()
			h := app.Handler(nil, false)
			config := defaultSettings
			config.ASRProvider, config.ASRModel, config.ASRModelDir = "sherpa", entry.family, dir
			call(t, h, "PUT", "/api/settings", config, 200)
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			part, err := writer.CreateFormFile("audio", filepath.Base(wav))
			if err != nil {
				t.Fatal(err)
			}
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
				t.Fatalf("upload: %d %s", res.Code, res.Body)
			}
			var meeting Meeting
			if err = json.Unmarshal(res.Body.Bytes(), &meeting); err != nil {
				t.Fatal(err)
			}
			path := "/api/meetings/" + meeting.ID
			call(t, h, "POST", path+"/jobs/transcription", map[string]string{"language": "zh"}, 202)
			meeting = waitJob(t, h, path, "completed")
			if source := meeting.TranscriptionSource; source == nil || source.Provider != "sherpa" || source.Model != entry.family {
				t.Fatalf("incorrect local transcript model: %+v", source)
			}
			var chinese, words int
			for _, segment := range meeting.Segments {
				if segment.Start < 0 || segment.End <= segment.Start {
					t.Fatal(segment)
				}
				if segment.Speaker != "" {
					t.Fatalf("local ASR unexpectedly assigned a speaker: %q", segment.Speaker)
				}
				words += len(segment.Words)
				for _, character := range segment.Text {
					if unicode.Is(unicode.Han, character) {
						chinese++
					}
				}
			}
			if chinese < 2 || words < 1 {
				t.Fatalf("model did not return timed Chinese transcription for the reference sample: %+v", meeting.Segments)
			}
			t.Logf("segments=%d chinese_characters=%d timed_tokens=%d", len(meeting.Segments), chinese, words)
		})
	}
}
