package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	gm "github.com/lejianwen/rustdesk-api/v2/george/model"
	"gorm.io/gorm"
)

// Enqueue insere um job; com dedupe_key, devolve o job pendente existente.
func (s *Svc) Enqueue(orgID uint, typ, dedupe string, payload map[string]interface{}) (*gm.Job, error) {
	job := &gm.Job{OrgID: orgID, Type: typ, DedupeKey: dedupe, Payload: jsonMeta(payload), Result: jsonMeta(map[string]interface{}{}),
		Status: gm.JobPending, RunAt: time.Now(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	err := s.DB.Create(job).Error
	if err != nil && dedupe != "" && (strings.Contains(err.Error(), "idx_jobs_dedupe") || strings.Contains(strings.ToLower(err.Error()), "duplicate")) {
		ex := &gm.Job{}
		if e2 := s.DB.Where("type = ? AND dedupe_key = ? AND status IN ('pending','running')", typ, dedupe).First(ex).Error; e2 == nil {
			return ex, nil
		}
	}
	if err != nil {
		return nil, err
	}
	return job, nil
}

// Job devolve um job da org do ator.
func (s *Svc) Job(c Ctx, id uint) (*gm.Job, error) {
	j := &gm.Job{}
	if err := s.DB.Where("id = ?", id).First(j).Error; err != nil {
		return nil, ErrNotFound("job")
	}
	if j.OrgID != c.User.OrgID && !c.User.IsOps {
		return nil, ErrNotFound("job")
	}
	return j, nil
}

// PendingJobs conta jobs pendentes (métrica/health).
func (s *Svc) PendingJobs() int64 {
	var n int64
	s.DB.Model(&gm.Job{}).Where("status = ?", gm.JobPending).Count(&n)
	return n
}

// Handler processa um job e devolve o resultado.
type Handler func(ctx context.Context, job *gm.Job) (map[string]interface{}, error)

func (s *Svc) handlers() map[string]Handler {
	return map[string]Handler{
		gm.JobTypeSessionSummary: s.handleSessionSummary,
		gm.JobTypeEmail:          s.handleEmail,
	}
}

// RunWorker roda o loop da fila até o contexto encerrar.
func (s *Svc) RunWorker(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		for s.RunOne(ctx) {
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RunOne pega um job pendente (SKIP LOCKED) e o executa; devolve false se não havia.
func (s *Svc) RunOne(ctx context.Context) bool {
	job := gm.Job{}
	res := s.DB.Raw(`UPDATE jobs SET status = 'running', locked_at = now(), attempts = attempts + 1, updated_at = now()
		WHERE id = (SELECT id FROM jobs WHERE status = 'pending' AND run_at <= now() ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING *`).Scan(&job)
	if res.Error != nil || job.ID == 0 {
		return false
	}
	h, ok := s.handlers()[job.Type]
	var out map[string]interface{}
	var err error
	if !ok {
		err = errors.New("tipo de job desconhecido: " + job.Type)
	} else {
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("panic: %v", r)
				}
			}()
			out, err = h(ctx, &job)
		}()
	}
	upd := map[string]interface{}{"updated_at": time.Now(), "locked_at": nil}
	if err == nil {
		upd["status"] = gm.JobDone
		upd["result"] = jsonMeta(out)
		upd["last_error"] = ""
	} else {
		upd["last_error"] = err.Error()
		if job.Attempts < 3 {
			upd["status"] = gm.JobPending
			upd["run_at"] = time.Now().Add(time.Duration(job.Attempts*job.Attempts) * 30 * time.Second)
		} else {
			upd["status"] = gm.JobFailed
		}
		s.Log.WithError(err).WithField("job_id", job.ID).WithField("type", job.Type).Warn("george: job falhou")
	}
	s.DB.Model(&gm.Job{}).Where("id = ?", job.ID).Updates(upd)
	return true
}

func (s *Svc) handleSessionSummary(ctx context.Context, job *gm.Job) (map[string]interface{}, error) {
	var p struct {
		SessionID  uint   `json:"session_id"`
		Transcript string `json:"transcript"`
	}
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return nil, err
	}
	sess := &gm.Session{}
	if err := s.DB.Where("id = ?", p.SessionID).First(sess).Error; err != nil {
		return nil, err
	}
	facts := s.sessionFacts(sess)
	if p.Transcript != "" {
		facts += "\n\nTranscrição/anotações:\n" + p.Transcript
	}
	provider := "template"
	summary := facts
	if s.AI.Enabled() {
		out, err := s.AI.Summarize(ctx, facts)
		if err != nil {
			s.DB.Model(sess).Updates(map[string]interface{}{"summary_status": "failed", "updated_at": time.Now()})
			return nil, err
		}
		summary, provider = out, "ai"
	}
	err := s.DB.Model(&gm.Session{}).Where("id = ?", sess.ID).Updates(map[string]interface{}{
		"summary": summary, "summary_status": "done", "updated_at": time.Now()}).Error
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"provider": provider, "chars": len(summary)}, nil
}

// handleEmail: sem SMTP configurado, só registra no log (ponto de extensão).
func (s *Svc) handleEmail(_ context.Context, job *gm.Job) (map[string]interface{}, error) {
	var p map[string]interface{}
	_ = json.Unmarshal(job.Payload, &p)
	s.Log.WithField("to", p["to"]).WithField("template", p["template"]).Info("george: e-mail (sem SMTP configurado; registrado apenas no log)")
	return map[string]interface{}{"delivered": false, "reason": "smtp_not_configured"}, nil
}

// ensure gorm import used in signatures
var _ = gorm.ErrRecordNotFound
