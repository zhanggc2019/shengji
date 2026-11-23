package minutes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"time"
)

// AI implements the two provider operations used by the durable worker.
type AI interface {
	Transcribe(context.Context, Settings, string, string) ([]Segment, error)
	Summarize(context.Context, Settings, []Segment) (string, error)
}

// jobActive checks whether a meeting is being modified by the worker.
func (a *App) jobActive(id string) (bool, error) {
	var active bool
	err := a.db.QueryRow("SELECT EXISTS(SELECT 1 FROM jobs WHERE meeting_id=? AND state IN ('queued','running'))", id).Scan(&active)
	return active, err
}

// nextJob claims the oldest queued request before running external I/O.
func (a *App) nextJob() (Job, error) {
	var j Job
	err := a.db.QueryRow(`UPDATE jobs SET state='running' WHERE id=(SELECT id FROM jobs WHERE state='queued' ORDER BY id LIMIT 1)
		RETURNING id,meeting_id,kind,state,language,error,config`).Scan(&j.ID, &j.MeetingID, &j.Kind, &j.State, &j.Language, &j.Error, &j.config)
	return j, err
}

// work drains the durable queue serially to avoid unbounded requests and overlapping results.
func (a *App) work() {
	defer close(a.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-a.wake:
		case <-ticker.C:
		}
		for a.ctx.Err() == nil {
			j, err := a.nextJob()
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			if err != nil {
				log.Printf("读取任务失败: %v", err)
				break
			}
			a.revision.Add(1)
			if err = a.runJob(j); err != nil {
				log.Printf("保存任务 %d 状态失败: %v", j.ID, err)
				break
			}
			a.revision.Add(1)
		}
	}
}

// runJob validates the provider result before replacing any existing transcript or summary.
func (a *App) runJob(job Job) error {
	m, err := a.getMeeting(job.MeetingID)
	var segments []Segment
	var summary string
	if err == nil {
		var config Settings
		if job.config == "" {
			config, err = a.settings()
			if err == nil {
				var snapshot []byte
				snapshot, err = json.Marshal(config)
				job.config = string(snapshot)
			}
		} else {
			err = json.Unmarshal([]byte(job.config), &config)
		}
		if err == nil {
			switch job.Kind {
			case "transcription":
				if !m.HasAudio {
					err = errors.New("没有可转录的音频")
					break
				}
				started := time.Now()
				segments, err = a.ai.Transcribe(a.ctx, config, filepath.Join(a.dir, "audio", m.audio), job.Language)
				job.TranscriptionMetrics = &TranscriptionMetrics{AudioSeconds: m.Duration, ElapsedMS: time.Since(started).Milliseconds()}
				if m.Mode == modeTranscript {
					for i := range segments {
						segments[i].Speaker = ""
					}
				}
				if err == nil && len(segments) == 0 {
					err = errors.New("转录服务没有返回可用文本")
				}
			case "summary":
				summary, err = a.ai.Summarize(a.ctx, config, m.Segments)
				if err == nil && summary == "" {
					err = errors.New("模型没有返回摘要")
				}
			default:
				err = fmt.Errorf("未知任务类型 %q", job.Kind)
			}
		}
	}
	if a.ctx.Err() != nil {
		_, err = a.db.Exec("UPDATE jobs SET state='queued' WHERE id=?", job.ID)
		return err
	}
	if err != nil {
		_, updateErr := a.db.Exec("UPDATE jobs SET state='failed',error=? WHERE id=?", err.Error(), job.ID)
		return updateErr
	}
	return a.commitJob(job, segments, summary)
}

// commitJob persists the result and terminal status in the same transaction.
func (a *App) commitJob(job Job, segments []Segment, summary string) error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if job.Kind == "transcription" {
		var data []byte
		data, err = json.Marshal(segments)
		if err == nil {
			_, err = tx.Exec("UPDATE meetings SET segments=?,summary='' WHERE id=?", string(data), job.MeetingID)
		}
	} else {
		_, err = tx.Exec("UPDATE meetings SET summary=? WHERE id=?", summary, job.MeetingID)
	}
	if err != nil {
		return err
	}
	var seconds, elapsed any
	if metrics := job.TranscriptionMetrics; metrics != nil {
		seconds, elapsed = metrics.AudioSeconds, metrics.ElapsedMS
	}
	if _, err = tx.Exec("UPDATE jobs SET state='completed',error='',config=?,audio_seconds=?,elapsed_ms=? WHERE id=?",
		job.config, seconds, elapsed, job.ID); err != nil {
		return err
	}
	return tx.Commit()
}
