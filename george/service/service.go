// Package service implementa as regras de negócio do George sobre o banco
// (orgs, auth, dispositivos, convites, sessões, auditoria, billing, jobs).
package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lejianwen/rustdesk-api/v2/george/ai"
	"github.com/lejianwen/rustdesk-api/v2/george/auth"
	"github.com/lejianwen/rustdesk-api/v2/george/config"
	gm "github.com/lejianwen/rustdesk-api/v2/george/model"
	"github.com/lejianwen/rustdesk-api/v2/george/perm"
	"github.com/sirupsen/logrus"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Svc agrega as dependências; use New() e guarde em S.
type Svc struct {
	DB     *gorm.DB
	Cfg    config.Config
	Signer *auth.Signer
	Cipher *auth.Cipher
	Log    *logrus.Logger
	AI     *ai.Client
}

// S é a instância global (inicializada por george.Init / testes).
var S *Svc

// New monta o serviço.
func New(db *gorm.DB, cfg config.Config, log *logrus.Logger) *Svc {
	enc := cfg.EncKey
	if enc == "" {
		enc = cfg.JWTSecret
	}
	S = &Svc{
		DB:     db,
		Cfg:    cfg,
		Signer: auth.NewSigner(cfg.JWTSecret, cfg.AccessTTL),
		Cipher: auth.NewCipher(enc),
		Log:    log,
		AI:     ai.New(cfg.AIKey, cfg.AIModel),
	}
	return S
}

// Error é o erro padronizado {error:{code,message,details}}.
type Error struct {
	Status  int         `json:"-"`
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Construtores de erro.
func ErrNotFound(what string) *Error {
	return &Error{Status: http.StatusNotFound, Code: "not_found", Message: what + " não encontrado"}
}
func ErrForbidden() *Error {
	return &Error{Status: http.StatusForbidden, Code: "forbidden", Message: "sem permissão para esta ação"}
}
func ErrUnauthorized(code, msg string) *Error {
	return &Error{Status: http.StatusUnauthorized, Code: code, Message: msg}
}
func ErrValidation(msg string, details interface{}) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: "validation_error", Message: msg, Details: details}
}
func ErrConflict(code, msg string) *Error {
	return &Error{Status: http.StatusConflict, Code: code, Message: msg}
}
func ErrGone(code, msg string) *Error { return &Error{Status: http.StatusGone, Code: code, Message: msg} }
func ErrPlanLimit(what string) *Error {
	return &Error{Status: http.StatusPaymentRequired, Code: "plan_limit_reached", Message: "limite do plano atingido: " + what}
}
func ErrInternal(err error) *Error {
	return &Error{Status: http.StatusInternalServerError, Code: "internal_error", Message: "erro interno", Details: err.Error()}
}
func ErrNotConfigured(what string) *Error {
	return &Error{Status: http.StatusNotImplemented, Code: what + "_not_configured", Message: what + " não configurado neste servidor"}
}

// AsError converte qualquer erro no formato padrão.
func AsError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound("registro")
	}
	return ErrInternal(err)
}

// Page é a paginação de entrada; Meta a de saída.
type Page struct {
	Page    int
	PerPage int
}

// Meta devolvida nas listas.
type Meta struct {
	Total   int64 `json:"total"`
	Page    int   `json:"page"`
	PerPage int   `json:"per_page"`
}

// Norm normaliza limites (1..200).
func (p Page) Norm() Page {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PerPage < 1 {
		p.PerPage = 20
	}
	if p.PerPage > 200 {
		p.PerPage = 200
	}
	return p
}

func (p Page) offset() int { return (p.Page - 1) * p.PerPage }

func (p Page) meta(total int64) Meta { return Meta{Total: total, Page: p.Page, PerPage: p.PerPage} }

// User é a linha de users com as colunas do upstream + George.
type User struct {
	ID          uint      `gorm:"primaryKey;column:id" json:"id"`
	Username    string    `json:"username"`
	Email       string    `json:"email"`
	Password    string    `json:"-"`
	Nickname    string    `json:"name"`
	Avatar      string    `json:"avatar"`
	GroupID     uint      `gorm:"column:group_id" json:"-"`
	Status      int       `json:"status"`
	OrgID       uint      `gorm:"column:org_id" json:"org_id"`
	Role        string    `json:"role"`
	IsOps       bool      `gorm:"column:is_ops" json:"is_ops"`
	TotpSecret  string    `gorm:"column:totp_secret" json:"-"`
	TotpEnabled bool      `gorm:"column:totp_enabled" json:"totp_enabled"`
	CreatedAt   time.Time `json:"created_at"`
}

// TableName aponta para a tabela do upstream.
func (User) TableName() string { return "users" }

// Enabled diz se a conta está ativa (status 1 do upstream).
func (u User) Enabled() bool { return u.Status == 1 }

// Actor converte o usuário no ator de permissões.
func (u User) Actor() perm.Actor {
	return perm.Actor{UserID: u.ID, OrgID: u.OrgID, Role: u.Role, IsOps: u.IsOps, GroupID: u.GroupID}
}

// Ctx carrega quem está agindo (para auditoria e permissões).
type Ctx struct {
	User      *User
	IP        string
	UserAgent string
	RequestID string
}

// Actor do contexto (vazio se anônimo).
func (c Ctx) Actor() perm.Actor {
	if c.User == nil {
		return perm.Actor{}
	}
	return c.User.Actor()
}

// UserByID carrega um usuário.
func (s *Svc) UserByID(id uint) (*User, error) {
	u := &User{}
	if err := s.DB.Where("id = ?", id).First(u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound("usuário")
		}
		return nil, err
	}
	return u, nil
}

// UserByEmail procura por e-mail ou username (minúsculo).
func (s *Svc) UserByEmail(email string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	u := &User{}
	err := s.DB.Where("lower(email) = ? OR username = ?", email, email).First(u).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound("usuário")
		}
		return nil, err
	}
	return u, nil
}

// Audit grava uma linha de auditoria (nunca falha a operação principal).
func (s *Svc) Audit(c Ctx, orgID uint, action, targetType string, targetID interface{}, meta map[string]interface{}) {
	if meta == nil {
		meta = map[string]interface{}{}
	}
	raw, _ := json.Marshal(meta)
	row := gm.AuditLog{
		OrgID: orgID, Action: action, TargetType: targetType, TargetID: fmt.Sprint(targetID),
		IP: c.IP, UserAgent: c.UserAgent, RequestID: c.RequestID, Meta: datatypes.JSON(raw), CreatedAt: time.Now(),
	}
	if c.User != nil {
		row.ActorID = c.User.ID
		row.ActorName = c.User.Email
	}
	if err := s.DB.Create(&row).Error; err != nil {
		s.Log.WithError(err).Warn("audit: falha ao gravar")
	}
}

// AuditList lista a auditoria da org.
func (s *Svc) AuditList(c Ctx, orgID uint, action string, actorID uint, p Page) ([]gm.AuditLog, Meta, error) {
	if !perm.Can(c.Actor(), perm.AuditView, perm.Resource{OrgID: orgID}) {
		return nil, Meta{}, ErrForbidden()
	}
	p = p.Norm()
	q := s.DB.Model(&gm.AuditLog{}).Where("org_id = ?", orgID)
	if action != "" {
		q = q.Where("action LIKE ?", action+"%")
	}
	if actorID > 0 {
		q = q.Where("actor_id = ?", actorID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, Meta{}, err
	}
	var rows []gm.AuditLog
	if err := q.Order("id DESC").Offset(p.offset()).Limit(p.PerPage).Find(&rows).Error; err != nil {
		return nil, Meta{}, err
	}
	if rows == nil {
		rows = []gm.AuditLog{}
	}
	return rows, p.meta(total), nil
}
