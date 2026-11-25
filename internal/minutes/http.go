package minutes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const maxAudioBytes = 500 << 20

// respond sends a JSON response so both entry points share identical error and data formats.
func respond(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// fail reports a user-facing request error.
func fail(w http.ResponseWriter, status int, message string) {
	respond(w, status, map[string]string{"error": message})
}

// serverFailure records internal errors without exposing paths or provider credentials.
func serverFailure(w http.ResponseWriter, err error) {
	log.Printf("处理请求失败: %v", err)
	fail(w, 500, "本地操作失败，请检查应用日志")
}

// readJSON limits payload size and rejects multiple JSON values in one request.
func readJSON(w http.ResponseWriter, r *http.Request, data any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(data); err != nil || d.Decode(new(any)) != io.EOF {
		fail(w, 400, "请求内容无效")
		return false
	}
	return true
}

// titleOf normalizes a title for display and export.
func titleOf(value string) (string, bool) {
	value = strings.TrimSpace(value)
	return value, value != "" && utf8.RuneCountInString(value) <= 255 && !strings.ContainsAny(value, "\r\n\x00")
}

// lookup validates the meeting identifier before opening any database or audio path.
func (a *App) lookup(w http.ResponseWriter, r *http.Request) (Meeting, bool) {
	id := r.PathValue("id")
	if !validID(id) {
		fail(w, 404, "会议不存在")
		return Meeting{}, false
	}
	m, err := a.getMeeting(id)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "会议不存在")
		return m, false
	}
	if err != nil {
		serverFailure(w, err)
		return m, false
	}
	return m, true
}

// meetings returns a page of recordings matching the optional text search.
func (a *App) meetings(w http.ResponseWriter, r *http.Request) {
	p, _ := strconv.Atoi(r.URL.Query().Get("page"))
	list, err := a.listMeetings(strings.TrimSpace(r.URL.Query().Get("q")), 50, max(0, min(p, 1_000_000))*50)
	if err != nil {
		serverFailure(w, err)
		return
	}
	respond(w, 200, list)
}

// create begins a new meeting without acquiring the audio device.
func (a *App) create(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Title string `json:"title"`
		Mode  string `json:"mode"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	title, ok := titleOf(input.Title)
	if !ok {
		fail(w, 400, "会议标题须为 1–255 个字符")
		return
	}
	mode, ok := recordMode(input.Mode)
	if !ok {
		fail(w, 400, "记录模式无效")
		return
	}
	m, err := a.createMeeting(newID(), title, "", mode, 0)
	if err != nil {
		serverFailure(w, err)
		return
	}
	respond(w, 201, m)
}

// meeting returns the full library entry including transcript and task states.
func (a *App) meeting(w http.ResponseWriter, r *http.Request) {
	if m, ok := a.lookup(w, r); ok {
		respond(w, 200, m)
	}
}

// update edits the title and notes as one database write.
func (a *App) update(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Title    *string  `json:"title"`
		Notes    *string  `json:"notes"`
		Duration *float64 `json:"duration"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	m, ok := a.lookup(w, r)
	if !ok {
		return
	}
	if input.Title != nil {
		m.Title, ok = titleOf(*input.Title)
		if !ok {
			fail(w, 400, "会议标题须为 1–255 个字符")
			return
		}
	}
	if input.Notes != nil {
		m.Notes = *input.Notes
	}
	if input.Duration != nil {
		if *input.Duration <= 0 || !m.HasAudio {
			fail(w, 400, "音频时长无效")
			return
		}
		if m.Duration == 0 {
			m.Duration = *input.Duration
		}
	}
	if _, err := a.db.Exec("UPDATE meetings SET title=?,notes=?,duration=? WHERE id=?", m.Title, m.Notes, m.Duration, m.ID); err != nil {
		serverFailure(w, err)
		return
	}
	a.notify()
	respond(w, 200, m)
}

// delete removes an inactive meeting and its audio file with rollback on database failure.
func (a *App) delete(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	m, ok := a.lookup(w, r)
	if !ok || !a.mutationAllowed(w, m.ID) {
		return
	}
	path := filepath.Join(a.dir, "audio", m.audio)
	staged := path + ".delete"
	moved := false
	if m.HasAudio {
		if err := os.Rename(path, staged); err == nil {
			moved = true
		} else if !errors.Is(err, os.ErrNotExist) {
			serverFailure(w, err)
			return
		}
	}
	if _, err := a.db.Exec("DELETE FROM meetings WHERE id=?", m.ID); err != nil {
		if moved {
			err = errors.Join(err, os.Rename(staged, path))
		}
		serverFailure(w, err)
		return
	}
	if moved {
		if err := os.Remove(staged); err != nil {
			log.Printf("删除暂存音频失败: %v", err)
		}
	}
	a.notify()
	w.WriteHeader(204)
}

// mutationAllowed protects a meeting while capture or an external job owns its data.
func (a *App) mutationAllowed(w http.ResponseWriter, id string) bool {
	active, err := a.jobActive(id)
	if err != nil {
		serverFailure(w, err)
		return false
	}
	if active || (a.recording != nil && a.recording.view.MeetingID == id) {
		fail(w, 409, "请等当前录音或任务完成")
		return false
	}
	return true
}

// record dispatches one state transition for the active recorder.
func (a *App) record(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	var opts CaptureOptions
	if action == "start" && r.ContentLength != 0 && !readJSON(w, r, &opts) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	m, ok := a.lookup(w, r)
	if !ok {
		return
	}
	var err error
	switch action {
	case "start":
		err = a.startRecording(m, opts)
	case "pause":
		err = a.togglePause(m.ID, "recording", "paused")
	case "resume":
		err = a.togglePause(m.ID, "paused", "recording")
	case "stop":
		err = a.stopRecording(m.ID)
	default:
		fail(w, 404, "未知录音操作")
		return
	}
	if err != nil {
		switch {
		case errors.Is(err, errRecordingBusy), errors.Is(err, errRecordState), errors.Is(err, errNoAudio):
			fail(w, 409, err.Error())
		case errors.Is(err, errNoCapture):
			fail(w, 501, err.Error())
		default:
			fail(w, 500, err.Error())
		}
		return
	}
	m, err = a.getMeeting(m.ID)
	if err != nil {
		serverFailure(w, err)
		return
	}
	respond(w, 200, m)
}

// enqueue records long-running work before returning its ID.
func (a *App) enqueue(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Language string `json:"language"`
	}
	if r.ContentLength != 0 && !readJSON(w, r, &input) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	m, ok := a.lookup(w, r)
	if !ok || !a.mutationAllowed(w, m.ID) {
		return
	}
	kind := r.PathValue("kind")
	switch kind {
	case "transcription":
		if !m.HasAudio {
			fail(w, 409, "请先录制或导入音频")
			return
		}
	case "summary":
		if m.Mode == modeTranscript {
			fail(w, 409, "纯转录记录仅支持转录")
			return
		}
		if len(m.Segments) == 0 {
			fail(w, 409, "请先生成转录")
			return
		}
	default:
		fail(w, 404, "未知任务类型")
		return
	}
	if utf8.RuneCountInString(input.Language) > 20 {
		fail(w, 400, "转录语言无效")
		return
	}
	config, err := a.settings()
	if err != nil {
		serverFailure(w, err)
		return
	}
	if kind == "transcription" && config.ASRProvider == "sherpa" && strings.TrimSpace(config.ASRModelDir) == "" {
		fail(w, 409, "请先在模型设置填写本机转录模型包目录")
		return
	}
	snapshot, err := json.Marshal(config)
	if err != nil {
		serverFailure(w, err)
		return
	}
	res, err := a.db.Exec("INSERT INTO jobs(meeting_id,kind,language,config) VALUES(?,?,?,?)", m.ID, kind, input.Language, string(snapshot))
	if err != nil {
		serverFailure(w, err)
		return
	}
	id, err := res.LastInsertId()
	if err != nil {
		serverFailure(w, err)
		return
	}
	a.notify()
	respond(w, 202, Job{ID: id, MeetingID: m.ID, Kind: kind, State: "queued", Language: input.Language})
}

// editSegment preserves the segment time range and clears obsolete word timestamps after text edits.
func (a *App) editSegment(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Text          *string `json:"text"`
		Speaker       *string `json:"speaker"`
		RenameSpeaker bool    `json:"rename_speaker"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	m, ok := a.lookup(w, r)
	if !ok || !a.mutationAllowed(w, m.ID) {
		return
	}
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || index < 0 || index >= len(m.Segments) {
		fail(w, 404, "转录片段不存在")
		return
	}
	s := &m.Segments[index]
	if input.Text != nil {
		value := strings.TrimSpace(*input.Text)
		if value == "" {
			fail(w, 400, "转录文本不能为空")
			return
		}
		if value != s.Text {
			s.Text = value
			s.Words = nil
		}
	}
	if input.Speaker != nil {
		if m.Mode == modeTranscript && strings.TrimSpace(*input.Speaker) != "" {
			fail(w, 400, "纯转录记录仅保存文本")
			return
		}
		value := strings.TrimSpace(*input.Speaker)
		if utf8.RuneCountInString(value) > 100 {
			fail(w, 400, "发言人名称过长")
			return
		}
		old := s.Speaker
		s.Speaker = value
		if input.RenameSpeaker && old != "" {
			for i := range m.Segments {
				if m.Segments[i].Speaker == old {
					m.Segments[i].Speaker = value
				}
			}
		}
	}
	raw, err := json.Marshal(m.Segments)
	if err == nil {
		_, err = a.db.Exec("UPDATE meetings SET segments=? WHERE id=?", string(raw), m.ID)
	}
	if err != nil {
		serverFailure(w, err)
		return
	}
	a.notify()
	respond(w, 200, m)
}

// audio serves a file with HTTP range support for the native audio player.
func (a *App) audio(w http.ResponseWriter, r *http.Request) {
	m, ok := a.lookup(w, r)
	if !ok {
		return
	}
	if !m.HasAudio {
		fail(w, 404, "会议暂无音频")
		return
	}
	f, err := os.Open(filepath.Join(a.dir, "audio", m.audio))
	if errors.Is(err, os.ErrNotExist) {
		fail(w, 404, "音频文件不存在")
		return
	}
	if err != nil {
		serverFailure(w, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		serverFailure(w, err)
		return
	}
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": m.Title + filepath.Ext(m.audio)}))
	}
	http.ServeContent(w, r, m.audio, info.ModTime(), f)
}

// export formats the saved record as text, Markdown, or JSON.
func (a *App) export(w http.ResponseWriter, r *http.Request) {
	m, ok := a.lookup(w, r)
	if !ok {
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "md"
	}
	if format != "txt" && format != "md" && format != "json" {
		fail(w, 400, "不支持该导出格式")
		return
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": m.Title + "." + format}))
	if format == "json" {
		respond(w, 200, m)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if format == "md" {
		fmt.Fprintf(w, "# %s\n\n## 转录\n\n", m.Title)
	} else {
		fmt.Fprintf(w, "%s\n\n转录\n\n", m.Title)
	}
	if len(m.Segments) != 0 {
		model := "未记录"
		if m.TranscriptionSource != nil {
			model = m.TranscriptionSource.Label
		}
		fmt.Fprintf(w, "转录模型：%s\n", model)
		if duration := m.Duration; duration > 0 {
			fmt.Fprintf(w, "音频时长：%.3f 秒\n", duration)
		}
		if metrics := m.TranscriptionMetrics; metrics != nil {
			fmt.Fprintf(w, "转录耗时：%.3f 秒\n", float64(metrics.ElapsedMS)/1000)
		}
		fmt.Fprintln(w)
	}
	for _, segment := range m.Segments {
		if segment.Speaker != "" && m.Mode != modeTranscript {
			fmt.Fprintf(w, "[%0.2fs] %s: %s\n", segment.Start, segment.Speaker, segment.Text)
		} else {
			fmt.Fprintf(w, "[%0.2fs] %s\n", segment.Start, segment.Text)
		}
	}
	if m.Notes != "" {
		fmt.Fprintf(w, "\n笔记\n%s\n", m.Notes)
	}
	if m.Summary != "" {
		fmt.Fprintf(w, "\n摘要\n%s\n", m.Summary)
	}
}

// serveASRProfiles provides vendor presets without exposing any authentication.
func serveASRProfiles(w http.ResponseWriter, r *http.Request) { respond(w, 200, asrProfiles) }

// serveSummaryProfiles lists built-in summary providers without exposing credentials.
func serveSummaryProfiles(w http.ResponseWriter, _ *http.Request) { respond(w, 200, summaryProfiles) }

// getSettings exposes model settings and credential status without returning secrets.
func (a *App) getSettings(w http.ResponseWriter, r *http.Request) {
	config, err := a.settings()
	if err != nil {
		serverFailure(w, err)
		return
	}
	a.respondSettings(w, r, config)
}

// putSettings validates model connection details before use by the worker.
func (a *App) putSettings(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Settings
		ASRKey *string `json:"asr_api_key"`
		LLMKey *string `json:"llm_api_key"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if (input.ASRKey != nil || input.LLMKey != nil) && !canManageCredentials(r) {
		fail(w, 403, "请在运行声记的电脑上打开本机工作台修改 API Key")
		return
	}
	config := input.Settings
	if profile, ok := summaryProfile(config.Provider); ok && profile.url != "" {
		config.LLMURL = ""
	}
	if err := validateSettings(config); err != nil {
		fail(w, 400, err.Error())
		return
	}
	var changes []credentialChange
	for _, field := range []struct {
		scope string
		input *string
	}{
		{config.asrCredentialScope(), input.ASRKey},
		{config.summaryCredentialScope(), input.LLMKey},
	} {
		if field.input == nil {
			continue
		}
		key := strings.TrimSpace(*field.input)
		if field.scope == "" || !validAPIKey(key) {
			fail(w, 400, "该服务的 API Key 无效")
			return
		}
		changes = append(changes, credentialChange{scope: field.scope, key: key})
	}
	if err := a.saveSettings(config, changes...); err != nil {
		serverFailure(w, err)
		return
	}
	a.notify()
	a.respondSettings(w, r, config)
}

func (a *App) respondSettings(w http.ResponseWriter, r *http.Request, config Settings) {
	if r.URL.Query().Get("credentials") != "1" {
		respond(w, 200, config)
		return
	}
	status := credentialStatus{CanManage: canManageCredentials(r)}
	if status.CanManage {
		if err := status.read(a.db, config); err != nil {
			serverFailure(w, err)
			return
		}
	}
	respond(w, 200, struct {
		Settings
		Credentials credentialStatus `json:"credentials"`
	}{config, status})
}

type browserEvent struct {
	Revision  uint64     `json:"revision"`
	Recording recordView `json:"recording"`
}

// events coalesces snapshots and bounds writes when the browser stops consuming them.
func (a *App) events(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithCancel(conn.CloseRead(r.Context()))
	defer cancel()
	stop := context.AfterFunc(a.ctx, cancel)
	defer stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var previous browserEvent
	lastSent := time.Time{}
	for {
		event := browserEvent{a.revision.Load(), a.liveRecording()}
		if event != previous || time.Since(lastSent) >= 20*time.Second {
			writeCtx, done := context.WithTimeout(ctx, 2*time.Second)
			err = wsjson.Write(writeCtx, conn, event)
			done()
			if err != nil {
				return
			}
			previous, lastSent = event, time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
