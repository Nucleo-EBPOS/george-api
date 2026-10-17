package service

import (
	"errors"
	"fmt"
	"time"

	gm "github.com/lejianwen/rustdesk-api/v2/george/model"
	"github.com/lejianwen/rustdesk-api/v2/george/perm"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"gorm.io/gorm"
)

// Sessions lista sessões da org (admin/viewer: todas; tech: as dele ou de seus dispositivos).
func (s *Svc) Sessions(c Ctx, deviceID uint, p Page) ([]gm.Session, Meta, error) {
	u := c.User
	if !perm.Can(u.Actor(), perm.SessionView, perm.Resource{OrgID: u.OrgID}) {
		return nil, Meta{}, ErrForbidden()
	}
	p = p.Norm()
	q := s.DB.Model(&gm.Session{}).Where("org_id = ?", u.OrgID)
	if !perm.Can(u.Actor(), perm.SessionViewAll, perm.Resource{OrgID: u.OrgID}) && u.Role != gm.RoleViewer {
		q = q.Where("(from_user_id = ? OR device_id IN (SELECT row_id FROM peers WHERE user_id = ?))", u.ID, u.ID)
	}
	if deviceID != 0 {
		q = q.Where("device_id = ?", deviceID)
	}
	var total int64
	q.Count(&total)
	var rows []gm.Session
	if err := q.Order("id DESC").Offset(p.offset()).Limit(p.PerPage).Find(&rows).Error; err != nil {
		return nil, Meta{}, err
	}
	if rows == nil {
		rows = []gm.Session{}
	}
	return rows, p.meta(total), nil
}

// Session devolve uma sessão da org.
func (s *Svc) Session(c Ctx, id uint) (*gm.Session, error) {
	u := c.User
	sess := &gm.Session{}
	if err := s.DB.Where("id = ?", id).First(sess).Error; err != nil {
		return nil, ErrNotFound("sessão")
	}
	if sess.OrgID != u.OrgID && !u.IsOps {
		return nil, ErrNotFound("sessão")
	}
	if !perm.Can(u.Actor(), perm.SessionView, perm.Resource{OrgID: sess.OrgID}) {
		return nil, ErrForbidden()
	}
	return sess, nil
}

// SessionSummaryRequest enfileira a geração da nota (idempotente por sessão).
func (s *Svc) SessionSummaryRequest(c Ctx, id uint, transcript string) (*gm.Job, *gm.Session, error) {
	sess, err := s.Session(c, id)
	if err != nil {
		return nil, nil, err
	}
	payload := map[string]interface{}{"session_id": sess.ID}
	if transcript != "" {
		if len(transcript) > 20000 {
			transcript = transcript[:20000]
		}
		payload["transcript"] = transcript
	}
	job, err := s.Enqueue(sess.OrgID, gm.JobTypeSessionSummary, fmt.Sprintf("session:%d", sess.ID), payload)
	if err != nil {
		return nil, nil, err
	}
	s.DB.Model(sess).Updates(map[string]interface{}{"summary_status": "pending", "updated_at": time.Now()})
	sess.SummaryStatus = "pending"
	s.Audit(c, sess.OrgID, "session.summary_request", "session", sess.ID, map[string]interface{}{"job_id": job.ID})
	return job, sess, nil
}

// sessionFacts monta o texto factual para a IA (ou o resumo determinístico).
func (s *Svc) sessionFacts(sess *gm.Session) string {
	dev := &gm.Device{}
	s.DB.Where("row_id = ?", sess.DeviceID).First(dev)
	dur := "em andamento"
	if sess.EndedAt != nil {
		d := sess.EndedAt.Sub(sess.StartedAt).Round(time.Second)
		if d < 0 {
			d = 0 // close_time do upstream tem resolução de segundos
		}
		dur = d.String()
	}
	so := dev.Os
	if so == "" {
		so = dev.Platform
	}
	if so == "" {
		so = "desconhecido"
	}
	from := sess.FromName
	if from == "" {
		from = sess.FromPeer
	}
	return fmt.Sprintf("Sessão #%d. Dispositivo acessado: %s (ID %s, SO %s). Origem: %s (peer %s). Início: %s. Duração: %s. Tipo de conexão: %d. IP de origem: %s.",
		sess.ID, dev.Name(), sess.PeerID, so, from, sess.FromPeer, sess.StartedAt.Format(time.RFC3339), dur, sess.ConnType, sess.IP)
}

// RegisterHooks liga callbacks do gorm para ingerir audit_conns → sessions
// e preencher org_id nas tabelas do upstream sem alterar o código upstream.
func (s *Svc) RegisterHooks(db *gorm.DB) error {
	if err := db.Callback().Create().After("gorm:create").Register("george:after_create", s.afterCreate); err != nil {
		return err
	}
	return db.Callback().Update().After("gorm:update").Register("george:after_update", s.afterUpdate)
}

func (s *Svc) fresh(db *gorm.DB) *gorm.DB {
	return db.Session(&gorm.Session{NewDB: true, SkipHooks: true})
}

func (s *Svc) afterCreate(db *gorm.DB) {
	if db.Error != nil || db.Statement == nil {
		return
	}
	switch db.Statement.Table {
	case "audit_conns":
		if ac, ok := db.Statement.Dest.(*model.AuditConn); ok {
			s.ingestConn(s.fresh(db), ac)
		}
	case "peers", "address_books", "tags", "address_book_collections":
		s.backfillOrg(s.fresh(db), db.Statement.Table)
	}
}

func (s *Svc) afterUpdate(db *gorm.DB) {
	if db.Error != nil || db.Statement == nil {
		return
	}
	switch db.Statement.Table {
	case "audit_conns":
		if ac, ok := db.Statement.Dest.(*model.AuditConn); ok && ac.Id != 0 {
			s.closeConn(s.fresh(db), ac)
		}
	case "peers":
		s.backfillOrg(s.fresh(db), "peers")
	}
}

// backfillOrg copia users.org_id para linhas com user_id e org_id = 0.
func (s *Svc) backfillOrg(db *gorm.DB, table string) {
	err := db.Exec(fmt.Sprintf(`UPDATE %s t SET org_id = u.org_id FROM users u WHERE t.user_id = u.id AND t.org_id = 0 AND u.org_id <> 0`, table)).Error
	if err != nil {
		s.Log.WithError(err).Warn("george: backfill org_id em " + table)
	}
}

// ingestConn cria a sessão a partir de um audit/conn "new".
func (s *Svc) ingestConn(db *gorm.DB, ac *model.AuditConn) {
	if ac.Id == 0 || ac.Action != model.AuditActionNew {
		return
	}
	dev := &gm.Device{}
	_ = db.Where("id = ?", ac.PeerId).First(dev).Error
	var fromUser uint
	if ac.FromPeer != "" {
		db.Raw("SELECT user_id FROM peers WHERE id = ? ORDER BY row_id LIMIT 1", ac.FromPeer).Scan(&fromUser)
	}
	sess := gm.Session{OrgID: dev.OrgID, DeviceID: dev.RowID, PeerID: ac.PeerId, FromPeer: ac.FromPeer, FromName: ac.FromName,
		FromUserID: fromUser, ConnID: ac.ConnId, SessionUuid: ac.SessionId, AuditConnID: ac.Id, ConnType: ac.Type, IP: ac.Ip,
		StartedAt: time.Now(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	err := db.Exec(`INSERT INTO sessions (org_id, device_id, peer_id, from_peer, from_name, from_user_id, conn_id, session_uuid,
		audit_conn_id, conn_type, ip, started_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (audit_conn_id) DO NOTHING`,
		sess.OrgID, sess.DeviceID, sess.PeerID, sess.FromPeer, sess.FromName, sess.FromUserID, sess.ConnID, sess.SessionUuid,
		sess.AuditConnID, sess.ConnType, sess.IP, sess.StartedAt, sess.CreatedAt, sess.UpdatedAt).Error
	if err != nil {
		s.Log.WithError(err).Warn("george: falha ao ingerir sessão")
	}
}

// closeConn fecha a sessão (ou atualiza origem) quando o audit_conn é atualizado.
func (s *Svc) closeConn(db *gorm.DB, ac *model.AuditConn) {
	upd := map[string]interface{}{"updated_at": time.Now()}
	if ac.CloseTime > 0 {
		upd["ended_at"] = time.Unix(ac.CloseTime, 0)
	}
	if ac.FromPeer != "" {
		upd["from_peer"] = ac.FromPeer
		var fromUser uint
		db.Raw("SELECT user_id FROM peers WHERE id = ? ORDER BY row_id LIMIT 1", ac.FromPeer).Scan(&fromUser)
		if fromUser != 0 {
			upd["from_user_id"] = fromUser
		}
	}
	if ac.FromName != "" {
		upd["from_name"] = ac.FromName
	}
	if ac.SessionId != "" {
		upd["session_uuid"] = ac.SessionId
	}
	if err := db.Model(&gm.Session{}).Where("audit_conn_id = ?", ac.Id).Updates(upd).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		s.Log.WithError(err).Warn("george: falha ao fechar sessão")
	}
}
