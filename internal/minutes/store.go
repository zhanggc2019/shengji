package minutes

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zhanggc2019/shengji/internal/transcript"

	"modernc.org/sqlite"
	sqliteLib "modernc.org/sqlite/lib"
)

// Word is a timed token from any supported transcription provider.
type Word = transcript.Word

// Segment is the result shape shared by remote and local providers.
type Segment = transcript.Segment

// Meeting holds the user-visible metadata, transcript, and summary.
type Meeting struct {
	Mode                 string                `json:"mode"`
	ID                   string                `json:"id"`
	Title                string                `json:"title"`
	Notes                string                `json:"notes"`
	CreatedAt            string                `json:"created_at"`
	HasAudio             bool                  `json:"has_audio"`
	Duration             float64               `json:"duration"`
	Recording            string                `json:"recording"`
	Segments             []Segment             `json:"segments"`
	TranscriptionSource  *TranscriptionSource  `json:"transcription_source,omitempty"`
	TranscriptionMetrics *TranscriptionMetrics `json:"transcription_metrics,omitempty"`
	Summary              string                `json:"summary"`
	Jobs                 []Job                 `json:"jobs"`
	audio                string
}

const (
	modeMeeting    = "meeting"
	modeTranscript = "transcript"
)

// recordMode defaults older clients to meeting mode and rejects unknown modes.
func recordMode(mode string) (string, bool) {
	if mode == "" {
		mode = modeMeeting
	}
	return mode, mode == modeMeeting || mode == modeTranscript
}

// TranscriptionSource identifies the model configured for the successful transcription.
type TranscriptionSource struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Label    string `json:"label"`
}

// TranscriptionMetrics records the audio length and wall-clock processing time for one result.
type TranscriptionMetrics struct {
	AudioSeconds float64 `json:"audio_seconds,omitempty"`
	ElapsedMS    int64   `json:"elapsed_ms"`
}

// Job is a durable transcription or summary request.
type Job struct {
	ID                   int64                 `json:"id"`
	MeetingID            string                `json:"meeting_id"`
	Kind                 string                `json:"kind"`
	State                string                `json:"state"`
	Language             string                `json:"language"`
	Error                string                `json:"error,omitempty"`
	TranscriptionSource  *TranscriptionSource  `json:"transcription_source,omitempty"`
	TranscriptionMetrics *TranscriptionMetrics `json:"transcription_metrics,omitempty"`
	config               string
}

// Settings stores model connection details that can be shown in the browser.
type Settings struct {
	ASRProvider string `json:"asr_provider"`
	ASRModelDir string `json:"asr_model_dir"`
	ASRURL      string `json:"asr_url"`
	ASRModel    string `json:"asr_model"`
	LLMURL      string `json:"llm_url"`
	LLMModel    string `json:"llm_model"`
	Provider    string `json:"provider"`
}

// App owns a database, one recorder, and one persistent job worker.
type App struct {
	db        *sql.DB
	lock      *sql.DB
	dir       string
	capture   Capture
	ai        AI
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	wake      chan struct{}
	revision  atomic.Uint64
	mu        sync.Mutex
	recording *recording
	closeOnce sync.Once
}

const schema = `
CREATE TABLE IF NOT EXISTS meetings (
	id TEXT PRIMARY KEY, title TEXT NOT NULL, notes TEXT NOT NULL DEFAULT '', mode TEXT NOT NULL DEFAULT 'meeting',
	created_at TEXT NOT NULL, audio TEXT NOT NULL DEFAULT '', duration REAL NOT NULL DEFAULT 0,
	recording TEXT NOT NULL DEFAULT 'idle', segments TEXT NOT NULL DEFAULT '[]', summary TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS jobs (
	id INTEGER PRIMARY KEY, meeting_id TEXT NOT NULL REFERENCES meetings(id) ON DELETE CASCADE,
	kind TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'queued', language TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT '', config TEXT NOT NULL DEFAULT '',
	audio_seconds REAL, elapsed_ms INTEGER
);
CREATE INDEX IF NOT EXISTS jobs_meeting ON jobs(meeting_id, id);
CREATE UNIQUE INDEX IF NOT EXISTS jobs_one_active ON jobs(meeting_id) WHERE state IN ('queued','running');
CREATE TABLE IF NOT EXISTS settings (id INTEGER PRIMARY KEY CHECK(id=1), value TEXT NOT NULL);

`

var defaultSettings = Settings{
	ASRProvider: "sherpa", ASRModel: "sensevoice",
	LLMURL: "http://127.0.0.1:11434", LLMModel: "qwen3:8b", Provider: "ollama",
}

// Open initializes the local library and resumes interrupted work.
func Open(root string, capture Capture, ai AI) (*App, error) {
	if err := os.MkdirAll(filepath.Join(root, "audio"), 0700); err != nil {
		return nil, err
	}
	lock, err := libraryLock(root)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "oddminutes.sqlite3"))
	if err != nil {
		lock.Close()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{db: db, lock: lock, dir: root, capture: capture, ai: ai, ctx: ctx, cancel: cancel, done: make(chan struct{}), wake: make(chan struct{}, 1)}
	if a.ai == nil {
		a.ai = &HTTPAI{}
	}
	if err = a.initialize(); err != nil {
		cancel()
		db.Close()
		lock.Close()
		return nil, err
	}
	go a.work()
	a.notify()
	return a, nil
}

// libraryLock takes a SQLite write reservation in a separate file for the lifetime of the process.
func libraryLock(root string) (*sql.DB, error) {
	lock, err := sql.Open("sqlite", filepath.Join(root, "session.lock"))
	if err != nil {
		return nil, err
	}
	lock.SetMaxOpenConns(1)
	if _, err = lock.Exec("BEGIN IMMEDIATE"); err != nil {
		lock.Close()
		var sqliteError *sqlite.Error
		if errors.As(err, &sqliteError) && sqliteError.Code()&0xff == sqliteLib.SQLITE_BUSY {
			return nil, errors.New("此会议库正在被另一个声记实例使用。请从系统托盘打开已有窗口，或退出旧版后再打开新版。")
		}
		return nil, fmt.Errorf("数据目录正在使用或不可写: %w", err)
	}
	return lock, nil
}

// initialize provisions the database and reconciles durable state before serving clients.
func (a *App) initialize() error {
	if _, err := a.db.Exec("PRAGMA busy_timeout=4000; PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON"); err != nil {
		return err
	}
	var version int
	if err := a.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > 3 {
		return fmt.Errorf("数据库版本 %d 高于当前程序支持的版本", version)
	}
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(schema); err != nil {
		return err
	}
	if version == 1 {
		if _, err = tx.Exec("ALTER TABLE jobs ADD COLUMN config TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if version == 1 || version == 2 {
		if _, err = tx.Exec(`ALTER TABLE jobs ADD COLUMN audio_seconds REAL;
			ALTER TABLE jobs ADD COLUMN elapsed_ms INTEGER;
			ALTER TABLE meetings ADD COLUMN mode TEXT NOT NULL DEFAULT 'meeting'`); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("PRAGMA user_version=3"); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE jobs SET state='queued', error='' WHERE state='running'"); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return a.recoverRecordings()
}

// Close flushes any active recording and returns interrupted jobs to the queue.
func (a *App) Close() {
	a.closeOnce.Do(func() {
		a.mu.Lock()
		if a.recording != nil {
			_ = a.finishRecording()
		}
		a.mu.Unlock()
		a.cancel()
		<-a.done
		a.db.Close()
		a.lock.Close()
	})
}

// newID creates an identifier safe for use as a filename.
func newID() string {
	var b [16]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// validID checks that IDs from URLs or persisted data are safe local names.
func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

// createMeeting inserts a fresh meeting with its optional uploaded audio.
func (a *App) createMeeting(id, title, audio, mode string, duration float64) (Meeting, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := a.db.Exec("INSERT INTO meetings(id,title,created_at,audio,mode,duration) VALUES (?,?,?,?,?,?)", id, title, now, audio, mode, duration)
	if err != nil {
		return Meeting{}, err
	}
	a.notify()
	return a.getMeeting(id)
}

// getMeeting loads the meeting and its recent jobs.
func (a *App) getMeeting(id string) (Meeting, error) {
	var m Meeting
	var raw, sourceConfig string
	var audioSeconds sql.NullFloat64
	var elapsed sql.NullInt64
	err := a.db.QueryRow(`SELECT m.id,m.title,m.mode,m.notes,m.created_at,m.audio,m.duration,m.recording,m.segments,m.summary,
		COALESCE(source.config,''),source.audio_seconds,source.elapsed_ms
		FROM meetings m LEFT JOIN jobs source ON source.id=(SELECT id FROM jobs
			WHERE meeting_id=m.id AND kind='transcription' AND state='completed' ORDER BY id DESC LIMIT 1)
		WHERE m.id=?`, id).
		Scan(&m.ID, &m.Title, &m.Mode, &m.Notes, &m.CreatedAt, &m.audio, &m.Duration, &m.Recording, &raw, &m.Summary,
			&sourceConfig, &audioSeconds, &elapsed)
	if err != nil {
		return m, err
	}
	m.HasAudio = m.audio != ""
	if err = json.Unmarshal([]byte(raw), &m.Segments); err != nil {
		return m, err
	}
	if len(m.Segments) != 0 {
		m.TranscriptionSource = transcriptionSource(sourceConfig)
		m.TranscriptionMetrics = readTranscriptionMetrics(audioSeconds, elapsed, m.Duration)
	}
	m.Jobs = []Job{}
	rows, err := a.db.Query("SELECT id,meeting_id,kind,state,language,error,config,audio_seconds,elapsed_ms FROM jobs WHERE meeting_id=? ORDER BY id DESC LIMIT 12", id)
	if err != nil {
		return m, err
	}
	defer rows.Close()
	for rows.Next() {
		var job Job
		if err = rows.Scan(&job.ID, &job.MeetingID, &job.Kind, &job.State, &job.Language, &job.Error,
			&job.config, &audioSeconds, &elapsed); err != nil {
			return m, err
		}
		if job.Kind == "transcription" && job.State == "completed" {
			job.TranscriptionSource = transcriptionSource(job.config)
			job.TranscriptionMetrics = readTranscriptionMetrics(audioSeconds, elapsed, m.Duration)
		}
		m.Jobs = append(m.Jobs, job)
	}
	return m, rows.Err()
}

// readTranscriptionMetrics fills a late browser duration without inventing timing for old jobs.
func readTranscriptionMetrics(audio sql.NullFloat64, elapsed sql.NullInt64, duration float64) *TranscriptionMetrics {
	if !elapsed.Valid {
		return nil
	}
	if audio.Float64 > 0 {
		duration = audio.Float64
	}
	return &TranscriptionMetrics{AudioSeconds: duration, ElapsedMS: elapsed.Int64}
}

// listMeetings searches titles, notes and transcript text without loading recordings.
func (a *App) listMeetings(query string, limit, offset int) ([]Meeting, error) {
	rows, err := a.db.Query(`SELECT id,title,mode,created_at,audio,duration,recording FROM meetings
		WHERE ?='' OR instr(lower(title), lower(?))>0 OR instr(lower(notes), lower(?))>0
		OR EXISTS(SELECT 1 FROM json_each(segments) AS s WHERE instr(lower(json_extract(s.value,'$.text')),lower(?))>0)
		ORDER BY created_at DESC,id LIMIT ? OFFSET ?`, query, query, query, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Meeting{}
	for rows.Next() {
		var m Meeting
		if err = rows.Scan(&m.ID, &m.Title, &m.Mode, &m.CreatedAt, &m.audio, &m.Duration, &m.Recording); err != nil {
			return nil, err
		}
		m.HasAudio = m.audio != ""
		list = append(list, m)
	}
	return list, rows.Err()
}

// settings reads the visible configuration and defaults a new library to local services.
func (a *App) settings() (Settings, error) {
	var raw string
	err := a.db.QueryRow("SELECT value FROM settings WHERE id=1").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultSettings, nil
	}
	settings := defaultSettings
	// Version 1 configurations with no ASR provider used OddASR.
	settings.ASRProvider, settings.ASRURL, settings.ASRModel = "oddasr", "http://127.0.0.1:9002", "whisper-1"
	if err == nil {
		err = json.Unmarshal([]byte(raw), &settings)
	}
	return settings, err
}

// saveSettings writes the validated model configuration.
func (a *App) saveSettings(config Settings) error {
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	_, err = a.db.Exec("INSERT INTO settings(id,value) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET value=excluded.value", string(raw))
	return err
}

// notify advances the browser revision and wakes the durable job worker.
func (a *App) notify() {
	a.revision.Add(1)
	select {
	case a.wake <- struct{}{}:
	default:
	}
}
