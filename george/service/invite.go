package service

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/lejianwen/rustdesk-api/v2/george/auth"
	gm "github.com/lejianwen/rustdesk-api/v2/george/model"
	"github.com/lejianwen/rustdesk-api/v2/george/perm"
	"gorm.io/gorm"
)

const (
	inviteCodeLen     = 6
	sessionPasswordLn = 10
	inviteMaxTTL      = 7 * 24 * time.Hour
)

// InviteInput é o POST /invites.
type InviteInput struct {
	DeviceID  uint
	ExpiresIn int // segundos; 0 = GEORGE_INVITE_TTL
	MaxUses   int
	Note      string
}

// InviteView é o convite serializado (sem a senha).
type InviteView struct {
	gm.Invite
	Status   string `json:"status"`
	URL      string `json:"url"`
	DeepLink string `json:"deep_link"`
}

func (s *Svc) inviteView(i gm.Invite) InviteView {
	return InviteView{Invite: i, Status: i.Status(time.Now()),
		URL: s.Cfg.PublicURL + "/app/invite/" + i.Code, DeepLink: "george://connect?code=" + i.Code}
}

// InviteCreate gera código único e senha de sessão (cifrada; entregue só no consumo/heartbeat).
func (s *Svc) InviteCreate(c Ctx, in InviteInput) (*InviteView, error) {
	u := c.User
	if !perm.Can(u.Actor(), perm.InviteCreate, perm.Resource{OrgID: u.OrgID}) {
		return nil, ErrForbidden()
	}
	if in.DeviceID != 0 {
		if _, _, err := s.deviceWithPerm(c, in.DeviceID, perm.DeviceConnect); err != nil {
			return nil, err
		}
	}
	ttl := s.Cfg.InviteTTL
	if in.ExpiresIn > 0 {
		ttl = time.Duration(in.ExpiresIn) * time.Second
	}
	if ttl > inviteMaxTTL {
		return nil, ErrValidation("expires_in máximo é 7 dias", map[string]string{"field": "expires_in"})
	}
	if in.MaxUses <= 0 {
		in.MaxUses = 1
	}
	if in.MaxUses > 100 {
		return nil, ErrValidation("max_uses máximo é 100", map[string]string{"field": "max_uses"})
	}
	pw, err := auth.RandomPassword(sessionPasswordLn)
	if err != nil {
		return nil, err
	}
	enc, err := s.Cipher.Encrypt(pw)
	if err != nil {
		return nil, err
	}
	inv := &gm.Invite{OrgID: u.OrgID, CreatedBy: u.ID, DeviceID: in.DeviceID, SessionPasswordEnc: enc,
		ExpiresAt: time.Now().Add(ttl), MaxUses: in.MaxUses, Note: strings.TrimSpace(in.Note), CreatedAt: time.Now()}
	for attempt := 0; attempt < 5; attempt++ {
		if inv.Code, err = auth.RandomCode(inviteCodeLen); err != nil {
			return nil, err
		}
		err = s.DB.Create(inv).Error
		if err == nil {
			break
		}
		if !strings.Contains(strings.ToLower(err.Error()), "unique") && !strings.Contains(err.Error(), "duplicate") {
			return nil, err
		}
	}
	if err != nil {
		return nil, err
	}
	s.Audit(c, u.OrgID, "invite.create", "invite", inv.ID, map[string]interface{}{"code": inv.Code, "device_id": inv.DeviceID, "expires_at": inv.ExpiresAt, "max_uses": inv.MaxUses})
	v := s.inviteView(*inv)
	return &v, nil
}

// Invites lista convites (admin: todos da org; tech: os próprios).
func (s *Svc) Invites(c Ctx, status string, p Page) ([]InviteView, Meta, error) {
	u := c.User
	if !perm.Can(u.Actor(), perm.InviteCreate, perm.Resource{OrgID: u.OrgID}) {
		return nil, Meta{}, ErrForbidden()
	}
	p = p.Norm()
	q := s.DB.Model(&gm.Invite{}).Where("org_id = ?", u.OrgID)
	if !perm.Can(u.Actor(), perm.InviteManage, perm.Resource{OrgID: u.OrgID}) {
		q = q.Where("created_by = ?", u.ID)
	}
	now := time.Now()
	switch status {
	case "active":
		q = q.Where("revoked_at IS NULL AND expires_at > ? AND uses < max_uses", now)
	case "expired":
		q = q.Where("revoked_at IS NULL AND expires_at <= ? AND uses < max_uses", now)
	case "consumed":
		q = q.Where("revoked_at IS NULL AND uses >= max_uses")
	case "revoked":
		q = q.Where("revoked_at IS NOT NULL")
	}
	var total int64
	q.Count(&total)
	var rows []gm.Invite
	if err := q.Order("id DESC").Offset(p.offset()).Limit(p.PerPage).Find(&rows).Error; err != nil {
		return nil, Meta{}, err
	}
	out := make([]InviteView, 0, len(rows))
	for _, r := range rows {
		out = append(out, s.inviteView(r))
	}
	return out, p.meta(total), nil
}

// InviteRevoke invalida um convite (por id numérico ou código).
func (s *Svc) InviteRevoke(c Ctx, idOrCode string) error {
	u := c.User
	inv := &gm.Invite{}
	q := s.DB.Where("org_id = ?", u.OrgID)
	if id, err := strconv.ParseUint(idOrCode, 10, 64); err == nil {
		q = q.Where("id = ?", id)
	} else {
		q = q.Where("code = ?", strings.ToUpper(strings.TrimSpace(idOrCode)))
	}
	if err := q.First(inv).Error; err != nil {
		return ErrNotFound("convite")
	}
	id := inv.ID
	if inv.CreatedBy != u.ID && !perm.Can(u.Actor(), perm.InviteManage, perm.Resource{OrgID: u.OrgID}) {
		return ErrForbidden()
	}
	if inv.RevokedAt == nil {
		now := time.Now()
		if err := s.DB.Model(inv).Updates(map[string]interface{}{"revoked_at": now, "session_password_enc": ""}).Error; err != nil {
			return err
		}
	}
	s.Audit(c, u.OrgID, "invite.revoke", "invite", id, map[string]interface{}{"code": inv.Code})
	return nil
}

// PublicInvite é o que o convidado vê antes de conectar.
type PublicInvite struct {
	Code        string    `json:"code"`
	Status      string    `json:"status"`
	ExpiresAt   time.Time `json:"expires_at"`
	Remaining   int       `json:"remaining_uses"`
	DeviceName  string    `json:"device_name,omitempty"`
	RustdeskID  string    `json:"rustdesk_id,omitempty"`
	ProductName string    `json:"product_name"`
	LogoURL     string    `json:"logo_url,omitempty"`
	Color       string    `json:"primary_color,omitempty"`
}

func (s *Svc) inviteByCode(code string) (*gm.Invite, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	inv := &gm.Invite{}
	if err := s.DB.Where("code = ?", code).First(inv).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound("convite")
		}
		return nil, err
	}
	return inv, nil
}

// InvitePublic devolve o mínimo para a tela do convidado.
func (s *Svc) InvitePublic(code string) (*PublicInvite, error) {
	inv, err := s.inviteByCode(code)
	if err != nil {
		return nil, err
	}
	out := &PublicInvite{Code: inv.Code, Status: inv.Status(time.Now()), ExpiresAt: inv.ExpiresAt, Remaining: inv.MaxUses - inv.Uses, ProductName: "George"}
	if out.Remaining < 0 {
		out.Remaining = 0
	}
	if inv.DeviceID != 0 {
		d := &gm.Device{}
		if s.DB.Where("row_id = ?", inv.DeviceID).First(d).Error == nil {
			out.DeviceName, out.RustdeskID = d.Name(), d.ID
		}
	}
	if b, _ := s.Branding(inv.OrgID); b != nil {
		if b.ProductName != "" {
			out.ProductName = b.ProductName
		}
		out.LogoURL, out.Color = b.LogoURL, b.PrimaryColor
	}
	return out, nil
}

// ConsumeOut é a entrega única ao convidado.
type ConsumeOut struct {
	Code            string `json:"code"`
	RustdeskID      string `json:"rustdesk_id,omitempty"`
	SessionPassword string `json:"session_password"`
	IDServer        string `json:"id_server"`
	RelayServer     string `json:"relay_server"`
	Key             string `json:"key"`
	RemainingUses   int    `json:"remaining_uses"`
}

// InviteConsume consome atomicamente (uso único por padrão) e entrega a senha de sessão.
func (s *Svc) InviteConsume(c Ctx, code string, servers [3]string) (*ConsumeOut, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	inv := gm.Invite{}
	now := time.Now()
	res := s.DB.Raw(`UPDATE invites SET uses = uses + 1, last_consumed_at = ?, last_consumed_ip = ?
		WHERE code = ? AND revoked_at IS NULL AND expires_at > ? AND uses < max_uses RETURNING *`,
		now, c.IP, code, now).Scan(&inv)
	if res.Error != nil {
		return nil, res.Error
	}
	if inv.ID == 0 {
		cur, err := s.inviteByCode(code)
		if err != nil {
			return nil, err
		}
		switch cur.Status(now) {
		case "revoked":
			return nil, ErrGone("invite_revoked", "convite revogado")
		case "consumed":
			return nil, ErrGone("invite_consumed", "convite já utilizado")
		case "expired":
			return nil, ErrGone("invite_expired", "convite expirado")
		}
		return nil, ErrGone("invite_unavailable", "convite indisponível")
	}
	pw, err := s.Cipher.Decrypt(inv.SessionPasswordEnc)
	if err != nil {
		return nil, err
	}
	if inv.Uses >= inv.MaxUses {
		s.DB.Model(&gm.Invite{}).Where("id = ?", inv.ID).Update("session_password_enc", "")
	}
	out := &ConsumeOut{Code: inv.Code, SessionPassword: pw, IDServer: servers[0], RelayServer: servers[1], Key: servers[2], RemainingUses: inv.MaxUses - inv.Uses}
	if inv.DeviceID != 0 {
		d := &gm.Device{}
		if s.DB.Where("row_id = ?", inv.DeviceID).First(d).Error == nil {
			out.RustdeskID = d.ID
		}
	}
	s.Audit(c, inv.OrgID, "invite.consume", "invite", inv.ID, map[string]interface{}{"code": inv.Code, "device_id": inv.DeviceID, "uses": inv.Uses})
	return out, nil
}

// PendingInvites devolve convites ativos de um dispositivo com a senha (para o host aplicar).
func (s *Svc) PendingInvites(deviceID uint) ([]PendingInvite, error) {
	out := []PendingInvite{}
	if deviceID == 0 {
		return out, nil
	}
	var rows []gm.Invite
	if err := s.DB.Where("device_id = ? AND revoked_at IS NULL AND expires_at > ? AND session_password_enc <> ''", deviceID, time.Now()).
		Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		pw, err := s.Cipher.Decrypt(r.SessionPasswordEnc)
		if err != nil {
			continue
		}
		out = append(out, PendingInvite{Code: r.Code, SessionPassword: pw, ExpiresAt: r.ExpiresAt})
	}
	return out, nil
}
