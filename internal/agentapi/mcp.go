package agentapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zhanggc2019/shengji/internal/buildinfo"
	"github.com/zhanggc2019/shengji/internal/minutes"
)

type transcribeInput struct {
	Path     string `json:"path" jsonschema:"Path relative to asr_info.input_dir. Example: meeting.wav."`
	Language string `json:"language,omitempty" jsonschema:"Language code such as zh or en. Omit or use auto to detect."`
}

// mcpHandler exposes the active settings and one stateless file-to-text workflow.
func (s *service) mcpHandler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "shengji-asr", Version: buildinfo.Version()}, nil)
	noExternalInteractions := false
	mcp.AddTool(server, &mcp.Tool{
		Name:        "asr_info",
		Description: "Get the selected transcription model, accepted formats, size limit and audio input directory.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &noExternalInteractions},
	}, s.infoTool)
	mcp.AddTool(server, &mcp.Tool{
		Name: "asr_transcribe",
		Description: "Transcribe audio in the local ASR input directory. Returns text, timed segments and model information. " +
			"Uses the server's configured ASR service and does not create a meeting.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.transcribeTool)
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 64 << 10, PropagateRequestCancellation: true,
	})
}

// infoTool reports the model and input directory to an MCP client.
func (s *service) infoTool(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, asrStatus, error) {
	status, err := s.status()
	return nil, status, err
}

// transcribeTool reads an audio file confined to the input directory and returns a structured transcript.
func (s *service) transcribeTool(ctx context.Context, _ *mcp.CallToolRequest, input transcribeInput) (*mcp.CallToolResult, minutes.AudioTranscription, error) {
	var empty minutes.AudioTranscription
	if !filepath.IsLocal(input.Path) || filepath.Clean(input.Path) == "." {
		return nil, empty, errors.New("path 必须是 ASR 输入目录下的相对文件路径")
	}
	root, err := os.OpenRoot(s.inputDir)
	if err != nil {
		return nil, empty, fmt.Errorf("打开 ASR 输入目录: %w", err)
	}
	defer root.Close()
	file, err := root.Open(input.Path)
	if err != nil {
		return nil, empty, fmt.Errorf("读取 ASR 输入文件 %q: %w", input.Path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, empty, err
	}
	if !info.Mode().IsRegular() {
		return nil, empty, errors.New("path 必须指向普通音频文件")
	}
	if info.Size() > minutes.MaxTranscriptionBytes {
		return nil, empty, minutes.ErrASRAudioTooLarge
	}
	result, err := s.app.TranscribeAudio(ctx, file, info.Name(), input.Language)
	return nil, result, err
}
