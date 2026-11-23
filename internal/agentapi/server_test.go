package agentapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zhanggc2019/shengji/internal/minutes"
)

type testBackend struct{}

// Transcribe checks that both transports pass the selected model and a staged WAV to the provider.
func (testBackend) Transcribe(_ context.Context, cfg minutes.Settings, path, language string) ([]minutes.Segment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if cfg.ASRModel != "sensevoice" || language != "zh" || !bytes.Equal(data, exampleWAV()) {
		return nil, errors.New("unexpected provider inputs")
	}
	return []minutes.Segment{{Text: "录音测试", End: 0.000125, Words: []minutes.Word{{Text: "录音", End: 0.0001}}}}, nil
}

// Summarize is outside the standalone ASR workflow.
func (testBackend) Summarize(context.Context, minutes.Settings, []minutes.Segment) (string, error) {
	return "", errors.New("unexpected summary")
}

// exampleWAV creates a standard 16 kHz mono PCM audio fixture without an external file.
func exampleWAV() []byte {
	data := make([]byte, 48)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], 40)
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], 16000)
	binary.LittleEndian.PutUint32(data[28:], 32000)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], 4)
	return data
}

// startTestServer opens an isolated library and mounts the actual browser and agent routes.
func startTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	root := t.TempDir()
	app, err := minutes.Open(root, nil, testBackend{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	inputDir := filepath.Join(root, "asr-input")
	handler, err := WithRoutes(app, inputDir, app.Handler(nil, false), false)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server, inputDir
}

// TestStandaloneASR verifies an actual MCP client, HTTP upload, and isolation from the meeting list.
func TestStandaloneASR(t *testing.T) {
	server, inputDir := startTestServer(t)
	path := filepath.Join(inputDir, "录音.wav")
	if err := os.WriteFile(path, exampleWAV(), 0600); err != nil {
		t.Fatal(err)
	}

	var info asrStatus
	readResponse(t, server.URL+"/api/asr/status", &info)
	if info.InputDir != inputDir || info.Source == nil || info.Source.Model != "sensevoice" || len(info.Formats) != 1 {
		t.Fatalf("unexpected ASR status: %+v", info)
	}

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "录音.wav")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(exampleWAV()); err != nil {
		t.Fatal(err)
	}
	if err = form.Close(); err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(server.URL+"/api/asr/transcriptions?language=zh", form.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	var result minutes.AudioTranscription
	decodeResponse(t, response, &result)
	if result.Text != "录音测试" || len(result.Segments) != 1 || len(result.Segments[0].Words) != 1 || result.AudioSeconds <= 0 {
		t.Fatalf("unexpected HTTP transcript: %+v", result)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 2 {
		t.Fatalf("MCP list tools: %v, %v", tools, err)
	}
	status, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "asr_info"})
	if err != nil || status.IsError || status.StructuredContent == nil {
		t.Fatalf("MCP status: %v, %v", status, err)
	}
	called, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "asr_transcribe", Arguments: map[string]any{
		"path": "录音.wav", "language": "zh",
	}})
	if err != nil || called.IsError || called.StructuredContent == nil {
		t.Fatalf("MCP transcription: %v, %v", called, err)
	}
	mcpResult, ok := called.StructuredContent.(map[string]any)
	if !ok || mcpResult["text"] != result.Text || len(called.Content) == 0 {
		t.Fatalf("unexpected MCP transcription: %+v", called)
	}
	escaped, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "asr_transcribe", Arguments: map[string]any{"path": "../outside.wav"}})
	if err != nil || !escaped.IsError {
		t.Fatalf("MCP accepted a path outside the input directory: %v, %v", escaped, err)
	}
	var meetings []minutes.Meeting
	readResponse(t, server.URL+"/api/meetings", &meetings)
	if len(meetings) != 0 {
		t.Fatal("standalone ASR created a meeting")
	}
}

// TestNetworkAccess verifies both listener scopes while preserving origin and Host checks.
func TestNetworkAccess(t *testing.T) {
	reply := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, tc := range []struct {
		peer, host, origin string
		allowLAN           bool
		want               int
	}{
		{"127.0.0.1:50000", "localhost:9011", "", false, 204},
		{"[::1]:50000", "[::1]:9011", "http://[::1]:9011", false, 204},
		{"192.168.1.5:50000", "192.168.1.2:9011", "", false, 403},
		{"192.168.1.5:50000", "192.168.1.2:9011", "", true, 204},
		{"192.168.1.5:50000", "192.168.1.2:9011", "http://192.168.1.2:9011", true, 204},
		{"192.168.1.5:50000", "192.168.1.2:9011", "https://invalid.example", true, 403},
		{"192.0.2.5:50000", "192.168.1.2:9011", "", true, 403},
		{"127.0.0.1:50000", "invalid.example:9011", "", true, 403},
	} {
		request := httptest.NewRequest(http.MethodPost, "http://"+tc.host+"/mcp", strings.NewReader("{}"))
		request.RemoteAddr = tc.peer
		request.Header.Set("Origin", tc.origin)
		recorder := httptest.NewRecorder()
		networkAccess(reply, tc.allowLAN).ServeHTTP(recorder, request)
		if recorder.Code != tc.want {
			t.Errorf("%+v: got %d", tc, recorder.Code)
		}
	}
}

// readResponse makes a simple GET request and decodes its successful response.
func readResponse(t *testing.T, url string, target any) {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	decodeResponse(t, response, target)
}

// decodeResponse asserts HTTP success before decoding the response body.
func decodeResponse(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected HTTP status: %s", response.Status)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}
