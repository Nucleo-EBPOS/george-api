// Package model define as entidades próprias do George (tabelas criadas pelas
// migrações SQL em george/migrate/migrations). As tabelas do upstream (users,
// peers, groups, ...) continuam em /model; aqui ficam só as extensões.
package model

import (
	"time"

	"gorm.io/datatypes"
)

// Papéis de organização e constantes correlatas.
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleTech   = "tech"
	RoleViewer = "viewer"

	ShareTargetUser  = "user"
	ShareTargetGroup = "group"
	ShareConnect     = "connect"
	ShareManage      = "manage"

	JobPending = "pending"
	JobRunning = "running"
	JobDone    = "done"
	JobFailed  = "failed"

	JobTypeSessionSummary = "session_summary"
	JobTypeEmail          = "email"
)

// ValidRole diz se a string é um papel de org válido.
func ValidRole(r string) bool {
	switch r {
	case RoleOwner, RoleAdmin, RoleTech, RoleViewer:
		return true
	}
	return false
}

// Org é a empresa/tenant.
type Org struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	Status    int       `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Org) TableName() string { return "orgs" }

// UserExt expõe as colunas que o George acrescentou em users (mesma tabela).
type UserExt struct {
	ID          uint   `gorm:"primaryKey;column:id"`
	OrgID       uint   `gorm:"column:org_id"`
	Role        string `gorm:"column:role"`
	IsOps       bool   `gorm:"column:is_ops"`
	TotpSecret  string `gorm:"column:totp_secret"`
	TotpEnabled bool   `gorm:"column:totp_enabled"`
}

func (UserExt) TableName() string { return "users" }

// Device é a visão George da tabela peers do upstream (mesma tabela).
type Device struct {
	RowID          uint   `gorm:"primaryKey;column:row_id" json:"id"`
	ID             string `gorm:"column:id" json:"rustdesk_id"`
	Hostname       string `json:"hostname"`
	Alias          string `json:"alias"`
	Os             string `json:"os"`
	Platform       string `json:"platform"`
	Version        string `json:"version"`
	Username       string `json:"username"`
	Uuid           string `json:"uuid"`
	Cpu            string `json:"cpu"`
	Memory         string `json:"memory"`
	UserID         uint   `gorm:"column:user_id" json:"owner_user_id"`
	OrgID          uint   `gorm:"column:org_id" json:"org_id"`
	GroupID        uint   `gorm:"column:group_id" json:"group_id"`
	HostEnabled    bool   `gorm:"column:host_enabled" json:"host_enabled"`
	LastOnlineTime int64  `gorm:"column:last_online_time" json:"last_seen"`
	LastOnlineIP   string `gorm:"column:last_online_ip" json:"last_ip"`
}

func (Device) TableName() string { return "peers" }

// Name devolve alias ou hostname.
func (d Device) Name() string {
	if d.Alias != "" {
		return d.Alias
	}
	return d.Hostname
}

// Online considera visto nos últimos 90s.
func (d Device) Online() bool {
	return d.LastOnlineTime > 0 && time.Now().Unix()-d.LastOnlineTime < 90
}

// DeviceGroup é a visão George de device_groups (mesma tabela).
type DeviceGroup struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	OrgID     uint      `gorm:"column:org_id" json:"org_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (DeviceGroup) TableName() string { return "device_groups" }

// RefreshToken rotativo (guardado como hash).
type RefreshToken struct {
	ID         uint      `gorm:"primaryKey"`
	UserID     uint      `gorm:"column:user_id"`
	TokenHash  string    `gorm:"column:token_hash"`
	Family     string    `gorm:"column:family"`
	ExpiresAt  time.Time `gorm:"column:expires_at"`
	RevokedAt  *time.Time
	ReplacedBy uint `gorm:"column:replaced_by"`
	IP         string
	UserAgent  string
	CreatedAt  time.Time
}

func (RefreshToken) TableName() string { return "refresh_tokens" }

// PasswordReset é o token de redefinição (hash).
type PasswordReset struct {
	ID        uint `gorm:"primaryKey"`
	UserID    uint
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}

func (PasswordReset) TableName() string { return "password_resets" }

// Invite é um convite por link.
type Invite struct {
	ID                 uint       `gorm:"primaryKey" json:"id"`
	OrgID              uint       `gorm:"column:org_id" json:"org_id"`
	Code               string     `json:"code"`
	CreatedBy          uint       `json:"created_by"`
	DeviceID           uint       `gorm:"column:device_id" json:"device_id"`
	SessionPasswordEnc string     `gorm:"column:session_password_enc" json:"-"`
	ExpiresAt          time.Time  `json:"expires_at"`
	MaxUses            int        `json:"max_uses"`
	Uses               int        `json:"uses"`
	RevokedAt          *time.Time `json:"revoked_at,omitempty"`
	LastConsumedAt     *time.Time `json:"last_consumed_at,omitempty"`
	LastConsumedIP     string     `gorm:"column:last_consumed_ip" json:"-"`
	Note               string     `json:"note"`
	CreatedAt          time.Time  `json:"created_at"`
}

func (Invite) TableName() string { return "invites" }

// Status calcula o estado do convite.
func (i Invite) Status(now time.Time) string {
	switch {
	case i.RevokedAt != nil:
		return "revoked"
	case i.Uses >= i.MaxUses:
		return "consumed"
	case now.After(i.ExpiresAt):
		return "expired"
	}
	return "active"
}

// DeviceShare compartilha um dispositivo com usuário ou grupo.
type DeviceShare struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	OrgID      uint      `gorm:"column:org_id" json:"org_id"`
	DeviceID   uint      `gorm:"column:device_id" json:"device_id"`
	TargetType string    `json:"target_type"`
	TargetID   uint      `gorm:"column:target_id" json:"target_id"`
	Level      string    `json:"level"`
	CreatedBy  uint      `json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`
}

func (DeviceShare) TableName() string { return "device_shares" }

// AuditLog é uma linha de auditoria.
type AuditLog struct {
	ID         uint           `gorm:"primaryKey" json:"id"`
	OrgID      uint           `gorm:"column:org_id" json:"org_id"`
	ActorID    uint           `gorm:"column:actor_id" json:"actor_id"`
	ActorName  string         `json:"actor_name"`
	Action     string         `json:"action"`
	TargetType string         `json:"target_type"`
	TargetID   string         `gorm:"column:target_id" json:"target_id"`
	IP         string         `gorm:"column:ip" json:"ip"`
	UserAgent  string         `json:"user_agent"`
	RequestID  string         `gorm:"column:request_id" json:"request_id"`
	Meta       datatypes.JSON `json:"meta"`
	CreatedAt  time.Time      `json:"created_at"`
}

func (AuditLog) TableName() string { return "audit_logs" }

// Session é uma sessão de acesso remoto.
type Session struct {
	ID            uint       `gorm:"primaryKey" json:"id"`
	OrgID         uint       `gorm:"column:org_id" json:"org_id"`
	DeviceID      uint       `gorm:"column:device_id" json:"device_id"`
	PeerID        string     `gorm:"column:peer_id" json:"peer_id"`
	FromPeer      string     `json:"from_peer"`
	FromName      string     `json:"from_name"`
	FromUserID    uint       `gorm:"column:from_user_id" json:"from_user_id"`
	ConnID        int64      `gorm:"column:conn_id" json:"conn_id"`
	SessionUuid   string     `gorm:"column:session_uuid" json:"session_uuid"`
	AuditConnID   uint       `gorm:"column:audit_conn_id" json:"-"`
	ConnType      int        `json:"conn_type"`
	IP            string     `gorm:"column:ip" json:"ip"`
	StartedAt     time.Time  `json:"started_at"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
	Bytes         int64      `json:"bytes"`
	RecordingRef  string     `json:"recording_ref"`
	Summary       string     `json:"summary"`
	SummaryStatus string     `json:"summary_status"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

func (Session) TableName() string { return "sessions" }

// OrgBranding é o white-label da org.
type OrgBranding struct {
	OrgID        uint      `gorm:"primaryKey;column:org_id" json:"org_id"`
	ProductName  string    `json:"product_name"`
	LogoURL      string    `gorm:"column:logo_url" json:"logo_url"`
	PrimaryColor string    `json:"primary_color"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (OrgBranding) TableName() string { return "org_branding" }

// OrgSso é a configuração OIDC da org (segredo cifrado).
type OrgSso struct {
	OrgID           uint      `gorm:"primaryKey;column:org_id" json:"org_id"`
	Enabled         bool      `json:"enabled"`
	Issuer          string    `json:"issuer"`
	ClientID        string    `gorm:"column:client_id" json:"client_id"`
	ClientSecretEnc string    `gorm:"column:client_secret_enc" json:"-"`
	Scopes          string    `json:"scopes"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (OrgSso) TableName() string { return "org_sso" }

// Plan é um plano comercial.
type Plan struct {
	ID            uint   `gorm:"primaryKey" json:"id"`
	Code          string `json:"code"`
	Name          string `json:"name"`
	MaxDevices    int    `json:"max_devices"`
	MaxTechs      int    `json:"max_techs"`
	PriceCents    int    `json:"price_cents"`
	StripePriceID string `gorm:"column:stripe_price_id" json:"-"`
}

func (Plan) TableName() string { return "plans" }

// Subscription liga org a plano (ids Stripe preparados).
type Subscription struct {
	ID                   uint       `gorm:"primaryKey" json:"id"`
	OrgID                uint       `gorm:"column:org_id" json:"org_id"`
	PlanID               uint       `gorm:"column:plan_id" json:"plan_id"`
	Status               string     `json:"status"`
	StripeCustomerID     string     `gorm:"column:stripe_customer_id" json:"-"`
	StripeSubscriptionID string     `gorm:"column:stripe_subscription_id" json:"-"`
	CurrentPeriodEnd     *time.Time `json:"current_period_end,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

func (Subscription) TableName() string { return "subscriptions" }

// StripeEvent garante idempotência do webhook.
type StripeEvent struct {
	ID          string `gorm:"primaryKey"`
	Type        string
	ReceivedAt  time.Time
	ProcessedAt *time.Time
}

func (StripeEvent) TableName() string { return "stripe_events" }

// Job é um item da fila simples.
type Job struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	OrgID     uint           `gorm:"column:org_id" json:"org_id"`
	Type      string         `json:"type"`
	DedupeKey string         `gorm:"column:dedupe_key" json:"-"`
	Payload   datatypes.JSON `json:"payload"`
	Status    string         `json:"status"`
	Attempts  int            `json:"attempts"`
	LastError string         `json:"last_error"`
	Result    datatypes.JSON `json:"result"`
	RunAt     time.Time      `gorm:"column:run_at" json:"run_at"`
	LockedAt  *time.Time     `json:"-"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

func (Job) TableName() string { return "jobs" }
