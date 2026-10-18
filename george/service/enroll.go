package service

import (
	"crypto/rand"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/lejianwen/rustdesk-api/v2/george/auth"
	gm "github.com/lejianwen/rustdesk-api/v2/george/model"
	"github.com/lejianwen/rustdesk-api/v2/george/perm"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Enrollment de PCs (docs/specs/06-enrollment.md): códigos de vínculo da org,
// POST /enroll (público), heartbeat do agente (token de dispositivo) e
// administração (rotacionar senha, revogar, ler credenciais).

const (
	// HeartbeatInterval sugerido ao agente (segundos).
	HeartbeatInterval   = 60
	enrollCodeChars     = 12
	permanentPasswordLn = 14
	minPermanentPw      = 8
	maxPermanentPw      = 128
	maxKeyDays          = 3650
	deviceTokenBytes    = 32
	// alfabeto da senha permanente: letras e dígitos sem ambíguos (0/O, 1/I/l).
	pwAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789"
)

// ErrEnrollKeyInvalid é a resposta única para código inexistente/revogado/expirado/esgotado.
func ErrEnrollKeyInvalid() *Error {
	return &Error{Status: http.StatusNotFound, Code: "enrollment_key_invalid", Message: "código de vínculo inválido"}
}

// ErrDeviceTokenInvalid é 401 do agente.
func ErrDeviceTokenInvalid() *Error {
	return ErrUnauthorized("device_token_invalid", "token de dispositivo inválido")
}

// ErrDeviceRevoked é 410 do agente / credenciais de PC revogado.
func ErrDeviceRevoked() *Error { return ErrGone("device_revoked", "vínculo do dispositivo revogado") }

func errNotEnrolled() *Error {
	return ErrConflict("device_not_enrolled", "dispositivo não está vinculado à organização")
}

func randomFrom(alphabet string, n int) (string, error) {
	out := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range out {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = alphabet[v.Int64()]
	}
	return string(out), nil
}

// NormalizeEnrollCode aceita "abcd efgh jkmn", "ABCDEFGHJKMN" ou "ABCD-EFGH-JKMN" e
// devolve o formato canônico XXXX-XXXX-XXXX ("" se não tiver 12 caracteres válidos).
func NormalizeEnrollCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		switch {
		case r == '-' || r == ' ' || r == '\t':
			continue
		case strings.ContainsRune("ABCDEFGHJKMNPQRSTUVWXYZ23456789", r):
			b.WriteRune(r)
		default:
			return ""
		}
	}
	raw := b.String()
	if len(raw) != enrollCodeChars {
		return ""
	}
	return raw[0:4] + "-" + raw[4:8] + "-" + raw[8:12]
}

func newEnrollCode() (string, error) {
	raw, err := auth.RandomCode(enrollCodeChars)
	if err != nil {
		return "", err
	}
	return raw[0:4] + "-" + raw[4:8] + "-" + raw[8:12], nil
}

// ---------------------------------------------------------------------------
// Códigos de vínculo (admin)

// EnrollmentKeyView é o código serializado.
type EnrollmentKeyView struct {
	gm.EnrollmentKey
	GroupName string `json:"group_name"`
	Status    string `json:"status"`
	URL       string `json:"url"`
	DeepLink  string `json:"deep_link"`
}

func (s *Svc) keyView(k gm.EnrollmentKey, groupName string) EnrollmentKeyView {
	return EnrollmentKeyView{EnrollmentKey: k, GroupName: groupName, Status: k.Status(time.Now()),
		URL: s.Cfg.PublicURL + "/e/" + k.Code, DeepLink: "george://enroll/" + k.Code}
}

// EnrollmentKeyInput é o POST /enrollment-keys.
type EnrollmentKeyInput struct {
	Label         string
	GroupID       uint
	MaxUses       int
	ExpiresInDays *int // nil = não expira
}

// EnrollmentKeyCreate gera um código de vínculo (owner/admin).
func (s *Svc) EnrollmentKeyCreate(c Ctx, in EnrollmentKeyInput) (*EnrollmentKeyView, error) {
	u := c.User
	if !perm.Can(u.Actor(), perm.EnrollKeyManage, perm.Resource{OrgID: u.OrgID}) {
		return nil, ErrForbidden()
	}
	in.Label = strings.TrimSpace(in.Label)
	if len(in.Label) > 120 {
		return nil, ErrValidation("label máximo 120 caracteres", map[string]string{"field": "label"})
	}
	if in.MaxUses < 0 || in.MaxUses > 1000000 {
		return nil, ErrValidation("max_uses deve ser >= 0 (0 = ilimitado)", map[string]string{"field": "max_uses"})
	}
	groupName := ""
	if in.GroupID != 0 {
		g := &gm.DeviceGroup{}
		if err := s.DB.Where("id = ? AND org_id = ?", in.GroupID, u.OrgID).First(g).Error; err != nil {
			return nil, ErrNotFound("grupo")
		}
		groupName = g.Name
	}
	k := &gm.EnrollmentKey{OrgID: u.OrgID, Label: in.Label, GroupID: in.GroupID, MaxUses: in.MaxUses, CreatedBy: u.ID, CreatedAt: time.Now()}
	if in.ExpiresInDays != nil {
		if *in.ExpiresInDays < 1 || *in.ExpiresInDays > maxKeyDays {
			return nil, ErrValidation("expires_in_days deve estar entre 1 e 3650 (ou null)", map[string]string{"field": "expires_in_days"})
		}
		exp := time.Now().Add(time.Duration(*in.ExpiresInDays) * 24 * time.Hour)
		k.ExpiresAt = &exp
	}
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if k.Code, err = newEnrollCode(); err != nil {
			return nil, err
		}
		k.ID = 0
		err = s.DB.Create(k).Error
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
	s.Audit(c, u.OrgID, "enrollment_key.create", "enrollment_key", k.ID, map[string]interface{}{
		"label": k.Label, "group_id": k.GroupID, "max_uses": k.MaxUses, "expires_at": k.ExpiresAt})
	v := s.keyView(*k, groupName)
	return &v, nil
}

// EnrollmentKeys lista os códigos da org (owner/admin/tech).
func (s *Svc) EnrollmentKeys(c Ctx, p Page) ([]EnrollmentKeyView, Meta, error) {
	u := c.User
	if !perm.Can(u.Actor(), perm.EnrollKeyView, perm.Resource{OrgID: u.OrgID}) {
		return nil, Meta{}, ErrForbidden()
	}
	p = p.Norm()
	q := s.DB.Model(&gm.EnrollmentKey{}).Where("org_id = ?", u.OrgID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, Meta{}, err
	}
	var rows []gm.EnrollmentKey
	if err := q.Order("id DESC").Offset(p.offset()).Limit(p.PerPage).Find(&rows).Error; err != nil {
		return nil, Meta{}, err
	}
	gids := make([]uint, 0, len(rows))
	for _, k := range rows {
		gids = append(gids, k.GroupID)
	}
	names := s.groupNames(gids)
	out := make([]EnrollmentKeyView, 0, len(rows))
	for _, k := range rows {
		out = append(out, s.keyView(k, names[k.GroupID]))
	}
	return out, p.meta(total), nil
}

// EnrollmentKeyRevoke revoga (idempotente). Dispositivos já vinculados continuam.
func (s *Svc) EnrollmentKeyRevoke(c Ctx, id uint) error {
	u := c.User
	k := &gm.EnrollmentKey{}
	if err := s.DB.Where("id = ? AND org_id = ?", id, u.OrgID).First(k).Error; err != nil {
		return ErrNotFound("código de vínculo")
	}
	if !perm.Can(u.Actor(), perm.EnrollKeyManage, perm.Resource{OrgID: u.OrgID}) {
		return ErrForbidden()
	}
	if k.RevokedAt == nil {
		if err := s.DB.Model(&gm.EnrollmentKey{}).Where("id = ?", id).Update("revoked_at", time.Now()).Error; err != nil {
			return err
		}
	}
	s.Audit(c, u.OrgID, "enrollment_key.revoke", "enrollment_key", id, map[string]interface{}{"label": k.Label, "uses": k.Uses})
	return nil
}

// ---------------------------------------------------------------------------
// POST /enroll (público)

// EnrollInput é o corpo do POST /enroll.
type EnrollInput struct {
	Code       string `json:"code"`
	RustdeskID string `json:"rustdesk_id"`
	Uuid       string `json:"uuid"`
	Hostname   string `json:"hostname"`
	Platform   string `json:"platform"`
	Os         string `json:"os"`
	Version    string `json:"version"`
	Username   string `json:"username"`
	Password   string `json:"password"`
}

// AgentPassword é a senha permanente entregue ao PC.
type AgentPassword struct {
	Version int    `json:"version"`
	Value   string `json:"value"`
}

// EnrollDevice / EnrollOrg são os resumos devolvidos no vínculo.
type EnrollDevice struct {
	ID        uint   `json:"id"`
	Name      string `json:"name"`
	GroupID   uint   `json:"group_id"`
	GroupName string `json:"group_name"`
}

type EnrollOrg struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

// EnrollOut é a resposta do vínculo (device_token só aqui).
type EnrollOut struct {
	Device            EnrollDevice   `json:"device"`
	Org               EnrollOrg      `json:"org"`
	DeviceToken       string         `json:"device_token"`
	Password          *AgentPassword `json:"password"`
	HeartbeatInterval int            `json:"heartbeat_interval"`
}

func validPermanentPassword(pw string) error {
	if len(pw) < minPermanentPw || len(pw) > maxPermanentPw {
		return ErrValidation("password deve ter entre 8 e 128 caracteres", map[string]string{"field": "password"})
	}
	return nil
}

// Enroll vincula um PC à org do código. Re-vínculo (mesmo uuid, mesma org) gera token
// novo e devolve a senha atual; outra org → 409; plano cheio → 402.
func (s *Svc) Enroll(c Ctx, in EnrollInput) (*EnrollOut, error) {
	code := NormalizeEnrollCode(in.Code)
	if code == "" {
		return nil, ErrEnrollKeyInvalid()
	}
	in.RustdeskID, in.Uuid = strings.TrimSpace(in.RustdeskID), strings.TrimSpace(in.Uuid)
	if in.RustdeskID == "" || in.Uuid == "" {
		return nil, ErrValidation("informe rustdesk_id e uuid", map[string]string{"field": "rustdesk_id,uuid"})
	}
	if in.Password != "" {
		if err := validPermanentPassword(in.Password); err != nil {
			return nil, err
		}
	}
	token, err := auth.RandomToken(deviceTokenBytes)
	if err != nil {
		return nil, err
	}
	tokenHash := auth.HashToken(token)
	now := time.Now()
	var (
		out      *EnrollOut
		key      gm.EnrollmentKey
		dev      gm.Device
		reenroll bool
	)
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("code = ?", code).First(&key).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEnrollKeyInvalid()
			}
			return err
		}
		if key.Status(now) != "active" {
			return ErrEnrollKeyInvalid()
		}
		found := true
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("uuid = ?", in.Uuid).Order("row_id").First(&dev).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", in.RustdeskID).Order("row_id").First(&dev).Error
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			found, dev = false, gm.Device{}
		} else if err != nil {
			return err
		}
		if found && dev.OrgID != 0 && dev.OrgID != key.OrgID {
			return ErrConflict("device_other_org", "dispositivo vinculado a outra organização")
		}
		if !found || dev.OrgID == 0 {
			lim, err := s.Limits(key.OrgID)
			if err != nil {
				return err
			}
			if lim.MaxDevices > 0 && lim.Devices >= lim.MaxDevices {
				return ErrPlanLimit("dispositivos")
			}
		}
		reenroll = found && dev.EnrolledAt != nil && dev.RevokedAt == nil

		// senha permanente
		var pw string
		version := dev.PwVersion
		switch {
		case in.Password != "":
			pw = in.Password
			cur := ""
			if reenroll && dev.HasPassword() {
				cur, _ = s.Cipher.Decrypt(*dev.PwEnc)
			}
			if cur != pw || version == 0 {
				version++
			}
		case reenroll && dev.HasPassword():
			if pw, err = s.Cipher.Decrypt(*dev.PwEnc); err != nil {
				return err
			}
		default:
			if pw, err = randomFrom(pwAlphabet, permanentPasswordLn); err != nil {
				return err
			}
			version++
		}
		if version == 0 {
			version = 1
		}
		enc, err := s.Cipher.Encrypt(pw)
		if err != nil {
			return err
		}

		// setor: re-vínculo mantém; senão o do código (se ainda existir)
		groupID := uint(0)
		if found && dev.OrgID == key.OrgID {
			groupID = dev.GroupID
		}
		if (!reenroll || groupID == 0) && key.GroupID != 0 {
			var n int64
			tx.Model(&gm.DeviceGroup{}).Where("id = ? AND org_id = ?", key.GroupID, key.OrgID).Count(&n)
			if n > 0 {
				groupID = key.GroupID
			}
		}

		if !found {
			var rowID uint
			if err := tx.Raw(`INSERT INTO peers (id, uuid, hostname, platform, os, version, username, user_id, org_id, host_enabled,
				last_online_time, last_online_ip, cpu, memory, alias, group_id, created_at, updated_at,
				enrolled_at, enrollment_key_id, device_token_hash, pw_enc, pw_version, pw_applied_version, unattended, last_agent_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, true, ?, ?, '', '', '', ?, ?, ?, ?, ?, ?, ?, ?, 0, false, ?) RETURNING row_id`,
				in.RustdeskID, in.Uuid, in.Hostname, in.Platform, in.Os, in.Version, in.Username, key.OrgID,
				now.Unix(), c.IP, groupID, now, now, now, key.ID, tokenHash, enc, version, now.Unix()).Scan(&rowID).Error; err != nil {
				return err
			}
			if err := tx.Where("row_id = ?", rowID).First(&dev).Error; err != nil {
				return err
			}
		} else {
			upd := map[string]interface{}{
				"org_id": key.OrgID, "user_id": 0, "enrollment_key_id": key.ID, "device_token_hash": tokenHash,
				"pw_enc": enc, "pw_version": version, "revoked_at": nil, "group_id": groupID, "host_enabled": true,
				"last_online_time": now.Unix(), "last_online_ip": c.IP, "last_agent_at": now.Unix(), "updated_at": now,
			}
			if !reenroll {
				upd["enrolled_at"] = now
				upd["unattended"] = false
			}
			if dev.ID != in.RustdeskID {
				upd["id"] = in.RustdeskID
			}
			if dev.Uuid != in.Uuid {
				upd["uuid"] = in.Uuid
			}
			for k, v := range map[string]string{"hostname": in.Hostname, "platform": in.Platform, "os": in.Os, "version": in.Version, "username": in.Username} {
				if v != "" {
					upd[k] = v
				}
			}
			if err := tx.Model(&gm.Device{}).Where("row_id = ?", dev.RowID).Updates(upd).Error; err != nil {
				return err
			}
			if err := tx.Where("row_id = ?", dev.RowID).First(&dev).Error; err != nil {
				return err
			}
		}
		if err := tx.Exec(`UPDATE enrollment_keys SET uses = uses + 1 WHERE id = ?`, key.ID).Error; err != nil {
			return err
		}
		org := gm.Org{}
		tx.Where("id = ?", key.OrgID).First(&org)
		out = &EnrollOut{
			Device:            EnrollDevice{ID: dev.RowID, Name: dev.Name(), GroupID: dev.GroupID},
			Org:               EnrollOrg{ID: org.ID, Name: org.Name},
			DeviceToken:       token,
			Password:          &AgentPassword{Version: version, Value: pw},
			HeartbeatInterval: HeartbeatInterval,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out.Device.GroupName = s.groupNames([]uint{out.Device.GroupID})[out.Device.GroupID]
	s.Audit(c, key.OrgID, "device.enroll", "device", dev.RowID, map[string]interface{}{
		"key_id": key.ID, "hostname": dev.Hostname, "rustdesk_id": dev.ID, "ip": c.IP, "reenroll": reenroll,
		"password_version": dev.PwVersion, "password_from_install": in.Password != ""})
	return out, nil
}

// ---------------------------------------------------------------------------
// Agente do dispositivo (Authorization: Device <token>)

// DeviceByToken autentica o agente: 401 device_token_invalid / 410 device_revoked.
func (s *Svc) DeviceByToken(token string) (*gm.Device, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrDeviceTokenInvalid()
	}
	d := &gm.Device{}
	if err := s.DB.Where("device_token_hash = ?", auth.HashToken(token)).First(d).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrDeviceTokenInvalid()
		}
		return nil, err
	}
	if d.EnrolledAt == nil {
		return nil, ErrDeviceTokenInvalid()
	}
	if d.RevokedAt != nil {
		return nil, ErrDeviceRevoked()
	}
	return d, nil
}

// AgentHeartbeatInput é o corpo do POST /agent/heartbeat.
type AgentHeartbeatInput struct {
	RustdeskID      string `json:"rustdesk_id"`
	Uuid            string `json:"uuid"`
	Hostname        string `json:"hostname"`
	Os              string `json:"os"`
	Version         string `json:"version"`
	Unattended      *bool  `json:"unattended"`
	PasswordVersion int    `json:"password_version"`
}

// AgentHeartbeatOut é a resposta (password só quando há versão mais nova que a do PC).
type AgentHeartbeatOut struct {
	Name              string         `json:"name"`
	GroupName         string         `json:"group_name"`
	OrgName           string         `json:"org_name"`
	Password          *AgentPassword `json:"password"`
	HeartbeatInterval int            `json:"heartbeat_interval"`
}

// AgentHeartbeat atualiza o estado do PC e entrega senha nova quando houver rotação.
func (s *Svc) AgentHeartbeat(c Ctx, d *gm.Device, in AgentHeartbeatInput) (*AgentHeartbeatOut, error) {
	now := time.Now()
	upd := map[string]interface{}{"last_online_time": now.Unix(), "last_online_ip": c.IP, "last_agent_at": now.Unix(), "updated_at": now}
	if in.Unattended != nil {
		upd["unattended"] = *in.Unattended
	}
	if id := strings.TrimSpace(in.RustdeskID); id != "" && id != d.ID {
		upd["id"] = id
	}
	if u := strings.TrimSpace(in.Uuid); u != "" && d.Uuid == "" {
		upd["uuid"] = u
	}
	for k, v := range map[string]string{"hostname": in.Hostname, "os": in.Os, "version": in.Version} {
		if v != "" {
			upd[k] = v
		}
	}
	if in.PasswordVersion > d.PwAppliedVersion && in.PasswordVersion <= d.PwVersion {
		upd["pw_applied_version"] = in.PasswordVersion
	}
	// só atualiza se o token ainda for o mesmo e não revogado (corrida com revoke/re-vínculo)
	res := s.DB.Model(&gm.Device{}).Where("row_id = ? AND device_token_hash = ? AND revoked_at IS NULL", d.RowID, *d.DeviceTokenHash).Updates(upd)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, ErrDeviceRevoked()
	}
	if err := s.DB.Where("row_id = ?", d.RowID).First(d).Error; err != nil {
		return nil, err
	}
	out := &AgentHeartbeatOut{Name: d.Name(), HeartbeatInterval: HeartbeatInterval}
	out.GroupName = s.groupNames([]uint{d.GroupID})[d.GroupID]
	s.DB.Model(&gm.Org{}).Where("id = ?", d.OrgID).Select("name").Scan(&out.OrgName)
	if d.PwVersion > in.PasswordVersion && d.HasPassword() {
		pw, err := s.Cipher.Decrypt(*d.PwEnc)
		if err != nil {
			return nil, err
		}
		out.Password = &AgentPassword{Version: d.PwVersion, Value: pw}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Administração de dispositivos vinculados

func (s *Svc) enrolledWithPerm(c Ctx, id uint, action perm.Action) (*gm.Device, error) {
	d, _, err := s.deviceWithPerm(c, id, action)
	if err != nil {
		return nil, err
	}
	if d.EnrolledAt == nil {
		return nil, errNotEnrolled()
	}
	return d, nil
}

// DeviceRotatePassword gera nova senha permanente (o PC aplica no próximo heartbeat).
func (s *Svc) DeviceRotatePassword(c Ctx, id uint) (int, error) {
	d, err := s.enrolledWithPerm(c, id, perm.DeviceAdminister)
	if err != nil {
		return 0, err
	}
	if d.RevokedAt != nil {
		return 0, ErrDeviceRevoked()
	}
	pw, err := randomFrom(pwAlphabet, permanentPasswordLn)
	if err != nil {
		return 0, err
	}
	enc, err := s.Cipher.Encrypt(pw)
	if err != nil {
		return 0, err
	}
	var version int
	if err := s.DB.Raw(`UPDATE peers SET pw_enc = ?, pw_version = pw_version + 1, updated_at = now()
		WHERE row_id = ? AND revoked_at IS NULL RETURNING pw_version`, enc, id).Scan(&version).Error; err != nil {
		return 0, err
	}
	if version == 0 {
		return 0, ErrDeviceRevoked()
	}
	s.Audit(c, d.OrgID, "device.rotate_password", "device", id, map[string]interface{}{"rustdesk_id": d.ID, "password_version": version})
	return version, nil
}

// DeviceRevoke corta o vínculo: token deixa de valer (o agente recebe 410), senha apagada.
func (s *Svc) DeviceRevoke(c Ctx, id uint) error {
	d, err := s.enrolledWithPerm(c, id, perm.DeviceAdminister)
	if err != nil {
		return err
	}
	if d.RevokedAt == nil {
		// device_token_hash é mantido só para responder 410 ao agente (que então apaga token e senha);
		// nenhuma rota aceita o token de um dispositivo revogado.
		if err := s.DB.Model(&gm.Device{}).Where("row_id = ?", id).Updates(map[string]interface{}{
			"revoked_at": time.Now(), "pw_enc": nil, "unattended": false, "updated_at": time.Now()}).Error; err != nil {
			return err
		}
	}
	s.Audit(c, d.OrgID, "device.revoke", "device", id, map[string]interface{}{"rustdesk_id": d.ID, "hostname": d.Hostname})
	return nil
}

// DeviceCredentials é o GET /devices/{id}/credentials.
type DeviceCredentials struct {
	RustdeskID      string `json:"rustdesk_id"`
	Password        string `json:"password"`
	PasswordVersion int    `json:"password_version"`
	PasswordApplied bool   `json:"password_applied"`
}

// DeviceCredentialsGet entrega a senha permanente a quem pode conectar (auditado).
func (s *Svc) DeviceCredentialsGet(c Ctx, id uint) (*DeviceCredentials, error) {
	d, err := s.enrolledWithPerm(c, id, perm.DeviceConnect)
	if err != nil {
		return nil, err
	}
	if d.RevokedAt != nil {
		return nil, ErrDeviceRevoked()
	}
	if !d.HasPassword() {
		return nil, errNotEnrolled()
	}
	pw, err := s.Cipher.Decrypt(*d.PwEnc)
	if err != nil {
		return nil, err
	}
	s.Audit(c, d.OrgID, "device.credentials.read", "device", id, map[string]interface{}{"rustdesk_id": d.ID, "password_version": d.PwVersion})
	return &DeviceCredentials{RustdeskID: d.ID, Password: pw, PasswordVersion: d.PwVersion, PasswordApplied: d.PasswordApplied()}, nil
}
