package service

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/lejianwen/rustdesk-api/v2/george/auth"
	gm "github.com/lejianwen/rustdesk-api/v2/george/model"
	"github.com/lejianwen/rustdesk-api/v2/utils"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Tokens é o par devolvido no login/refresh.
type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// issueTokens cria access + refresh (nova família ou reuso de família).
func (s *Svc) issueTokens(c Ctx, u *User, family string) (*Tokens, error) {
	access, err := s.Signer.Access(u.ID, u.OrgID, u.Role, u.IsOps)
	if err != nil {
		return nil, err
	}
	raw, err := auth.RandomToken(32)
	if err != nil {
		return nil, err
	}
	if family == "" {
		if family, err = auth.RandomToken(12); err != nil {
			return nil, err
		}
	}
	rt := &gm.RefreshToken{
		UserID: u.ID, TokenHash: auth.HashToken(raw), Family: family,
		ExpiresAt: time.Now().Add(s.Cfg.RefreshTTL), IP: c.IP, UserAgent: c.UserAgent, CreatedAt: time.Now(),
	}
	if err := s.DB.Create(rt).Error; err != nil {
		return nil, err
	}
	return &Tokens{AccessToken: access, RefreshToken: raw, TokenType: "Bearer", ExpiresIn: int(s.Signer.TTL().Seconds())}, nil
}

// Login autentica por e-mail/senha (+ TOTP quando habilitado).
func (s *Svc) Login(c Ctx, email, password, totpCode string) (*Tokens, *User, error) {
	u, err := s.UserByEmail(email)
	if err != nil {
		// custo constante aproximado: hash mesmo sem usuário
		_, _ = auth.HashPassword(password)
		return nil, nil, ErrUnauthorized("invalid_credentials", "e-mail ou senha inválidos")
	}
	ok, newHash, err := utils.VerifyPassword(u.Password, password)
	if err != nil || !ok {
		s.Audit(c, u.OrgID, "auth.login_failed", "user", u.ID, map[string]interface{}{"email": u.Email})
		return nil, nil, ErrUnauthorized("invalid_credentials", "e-mail ou senha inválidos")
	}
	if newHash != "" { // migração transparente md5/bcrypt → argon2id
		s.DB.Table("users").Where("id = ?", u.ID).Update("password", newHash)
	}
	if !u.Enabled() {
		return nil, nil, ErrUnauthorized("user_disabled", "usuário desativado")
	}
	if u.TotpEnabled {
		if totpCode == "" {
			return nil, nil, ErrUnauthorized("mfa_required", "informe o código do autenticador (totp_code)")
		}
		secret, err := s.Cipher.Decrypt(u.TotpSecret)
		if err != nil || !auth.ValidateTotp(secret, totpCode) {
			s.Audit(c, u.OrgID, "auth.mfa_failed", "user", u.ID, nil)
			return nil, nil, ErrUnauthorized("mfa_invalid", "código do autenticador inválido")
		}
	}
	if ops := isOpsEmail(u.Email); ops != u.IsOps {
		s.DB.Table("users").Where("id = ?", u.ID).Update("is_ops", ops)
		u.IsOps = ops
	}
	c.User = u
	t, err := s.issueTokens(c, u, "")
	if err != nil {
		return nil, nil, err
	}
	s.Audit(c, u.OrgID, "auth.login", "user", u.ID, map[string]interface{}{"email": u.Email})
	return t, u, nil
}

// Refresh rotaciona o refresh token; reuso de token já rotacionado revoga a família.
func (s *Svc) Refresh(c Ctx, raw string) (*Tokens, *User, error) {
	if raw == "" {
		return nil, nil, ErrUnauthorized("invalid_refresh", "refresh_token obrigatório")
	}
	rt := &gm.RefreshToken{}
	if err := s.DB.Where("token_hash = ?", auth.HashToken(raw)).First(rt).Error; err != nil {
		return nil, nil, ErrUnauthorized("invalid_refresh", "refresh token inválido")
	}
	now := time.Now()
	if rt.RevokedAt != nil {
		// reuso: comprometido → derruba a família inteira
		s.DB.Model(&gm.RefreshToken{}).Where("family = ? AND revoked_at IS NULL", rt.Family).Update("revoked_at", now)
		s.Audit(c, 0, "auth.refresh_reuse", "user", rt.UserID, map[string]interface{}{"family": rt.Family})
		return nil, nil, ErrUnauthorized("refresh_reused", "refresh token já utilizado; faça login novamente")
	}
	if now.After(rt.ExpiresAt) {
		return nil, nil, ErrUnauthorized("refresh_expired", "refresh token expirado")
	}
	u, err := s.UserByID(rt.UserID)
	if err != nil || !u.Enabled() {
		return nil, nil, ErrUnauthorized("user_disabled", "usuário indisponível")
	}
	c.User = u
	t, err := s.issueTokens(c, u, rt.Family)
	if err != nil {
		return nil, nil, err
	}
	var newID uint
	s.DB.Raw("SELECT id FROM refresh_tokens WHERE token_hash = ?", auth.HashToken(t.RefreshToken)).Scan(&newID)
	s.DB.Model(rt).Updates(map[string]interface{}{"revoked_at": now, "replaced_by": newID})
	return t, u, nil
}

// Logout revoga o refresh token informado (e todos da família).
func (s *Svc) Logout(c Ctx, raw string) {
	if raw == "" {
		return
	}
	rt := &gm.RefreshToken{}
	if err := s.DB.Where("token_hash = ?", auth.HashToken(raw)).First(rt).Error; err != nil {
		return
	}
	s.DB.Model(&gm.RefreshToken{}).Where("family = ? AND revoked_at IS NULL", rt.Family).Update("revoked_at", time.Now())
	if c.User != nil {
		s.Audit(c, c.User.OrgID, "auth.logout", "user", c.User.ID, nil)
	}
}

// Forgot gera token de reset e enfileira o e-mail (sempre responde OK).
func (s *Svc) Forgot(c Ctx, email string) error {
	u, err := s.UserByEmail(email)
	if err != nil {
		return nil
	}
	raw, err := auth.RandomToken(32)
	if err != nil {
		return err
	}
	pr := &gm.PasswordReset{UserID: u.ID, TokenHash: auth.HashToken(raw), ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}
	if err := s.DB.Create(pr).Error; err != nil {
		return err
	}
	link := s.Cfg.PublicURL + "/app/reset?token=" + raw
	_, err = s.Enqueue(u.OrgID, gm.JobTypeEmail, "", map[string]interface{}{
		"to": u.Email, "template": "password_reset", "link": link,
	})
	s.Audit(c, u.OrgID, "auth.forgot", "user", u.ID, nil)
	return err
}

// Reset troca a senha com o token de reset (uso único) e revoga sessões.
func (s *Svc) Reset(c Ctx, raw, password string) error {
	if err := ValidatePassword(password); err != nil {
		return err
	}
	pr := &gm.PasswordReset{}
	if err := s.DB.Where("token_hash = ?", auth.HashToken(raw)).First(pr).Error; err != nil {
		return ErrUnauthorized("invalid_reset", "token de redefinição inválido")
	}
	if pr.UsedAt != nil || time.Now().After(pr.ExpiresAt) {
		return ErrUnauthorized("invalid_reset", "token de redefinição expirado ou já usado")
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	now := time.Now()
	return s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("users").Where("id = ?", pr.UserID).Update("password", hash).Error; err != nil {
			return err
		}
		if err := tx.Model(pr).Update("used_at", now).Error; err != nil {
			return err
		}
		tx.Model(&gm.RefreshToken{}).Where("user_id = ? AND revoked_at IS NULL", pr.UserID).Update("revoked_at", now)
		tx.Exec("DELETE FROM user_tokens WHERE user_id = ?", pr.UserID)
		u, _ := s.UserByID(pr.UserID)
		if u != nil {
			s.Audit(c, u.OrgID, "auth.reset", "user", u.ID, nil)
		}
		return nil
	})
}

// TotpSetup gera segredo (ainda desabilitado) e devolve otpauth URL.
func (s *Svc) TotpSetup(c Ctx) (secret, url string, err error) {
	u := c.User
	if u.TotpEnabled {
		return "", "", ErrConflict("totp_enabled", "2FA já está habilitado; desabilite antes de reconfigurar")
	}
	secret, url, err = auth.NewTotp(u.Email)
	if err != nil {
		return "", "", err
	}
	enc, err := s.Cipher.Encrypt(secret)
	if err != nil {
		return "", "", err
	}
	if err := s.DB.Table("users").Where("id = ?", u.ID).Update("totp_secret", enc).Error; err != nil {
		return "", "", err
	}
	return secret, url, nil
}

// TotpVerify confirma o código e liga o 2FA.
func (s *Svc) TotpVerify(c Ctx, code string) error {
	u, err := s.UserByID(c.User.ID)
	if err != nil {
		return err
	}
	secret, err := s.Cipher.Decrypt(u.TotpSecret)
	if err != nil || secret == "" {
		return ErrConflict("totp_not_setup", "chame /auth/2fa/setup antes")
	}
	if !auth.ValidateTotp(secret, code) {
		return ErrUnauthorized("mfa_invalid", "código inválido")
	}
	if err := s.DB.Table("users").Where("id = ?", u.ID).Update("totp_enabled", true).Error; err != nil {
		return err
	}
	s.Audit(c, u.OrgID, "auth.2fa.enable", "user", u.ID, nil)
	return nil
}

// TotpDisable desliga o 2FA (exige código válido ou senha).
func (s *Svc) TotpDisable(c Ctx, code, password string) error {
	u, err := s.UserByID(c.User.ID)
	if err != nil {
		return err
	}
	if !u.TotpEnabled {
		return nil
	}
	secret, _ := s.Cipher.Decrypt(u.TotpSecret)
	okCode := code != "" && auth.ValidateTotp(secret, code)
	okPw := false
	if password != "" {
		okPw, _, _ = utils.VerifyPassword(u.Password, password)
	}
	if !okCode && !okPw {
		return ErrUnauthorized("mfa_invalid", "informe um código TOTP válido ou a senha atual")
	}
	if err := s.DB.Table("users").Where("id = ?", u.ID).Updates(map[string]interface{}{"totp_enabled": false, "totp_secret": ""}).Error; err != nil {
		return err
	}
	s.Audit(c, u.OrgID, "auth.2fa.disable", "user", u.ID, nil)
	return nil
}

// MeView é a resposta de GET /me.
type MeView struct {
	User   *User       `json:"user"`
	Org    *gm.Org     `json:"org"`
	Plan   *gm.Plan    `json:"plan"`
	Limits interface{} `json:"usage"`
}

// Me monta a visão do usuário logado.
func (s *Svc) Me(c Ctx) (*MeView, error) {
	org, err := s.OrgByID(c.User.OrgID)
	if err != nil && c.User.OrgID != 0 {
		return nil, err
	}
	plan, _ := s.PlanOf(c.User.OrgID)
	lim, _ := s.Limits(c.User.OrgID)
	return &MeView{User: c.User, Org: org, Plan: plan, Limits: lim}, nil
}

// UserFromUpstreamToken aceita o access_token do cliente RustDesk (/api/login).
func (s *Svc) UserFromUpstreamToken(token string) (*User, error) {
	var row struct {
		UserID    uint
		ExpiredAt int64
	}
	if err := s.DB.Raw(`SELECT user_id, expired_at FROM user_tokens WHERE token = ? LIMIT 1`, token).Scan(&row).Error; err != nil {
		return nil, err
	}
	if row.UserID == 0 || row.ExpiredAt < time.Now().Unix() {
		return nil, errors.New("token do cliente inválido")
	}
	return s.UserByID(row.UserID)
}

// jsonMeta ajuda a serializar payloads.
func jsonMeta(v interface{}) datatypes.JSON {
	b, _ := json.Marshal(v)
	return datatypes.JSON(b)
}

// normEmail é utilitário compartilhado.
func normEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }
