package service

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lejianwen/rustdesk-api/v2/george/auth"
	"github.com/lejianwen/rustdesk-api/v2/george/config"
	gm "github.com/lejianwen/rustdesk-api/v2/george/model"
	"github.com/lejianwen/rustdesk-api/v2/george/perm"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"gorm.io/gorm"
)

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)
var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// Slugify gera slug de org.
func Slugify(name string) string {
	s := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if s == "" {
		s = "org"
	}
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

// SignupInput é o cadastro inicial (cria org + owner).
type SignupInput struct {
	Email    string
	Password string
	Name     string
	OrgName  string
}

// ValidatePassword aplica a política mínima.
func ValidatePassword(pw string) error {
	if len(pw) < 10 {
		return ErrValidation("senha deve ter ao menos 10 caracteres", map[string]string{"field": "password"})
	}
	return nil
}

// Signup cria organização, grupo upstream, assinatura free e usuário owner.
func (s *Svc) Signup(c Ctx, in SignupInput) (*User, *gm.Org, error) {
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if !emailRe.MatchString(in.Email) {
		return nil, nil, ErrValidation("e-mail inválido", map[string]string{"field": "email"})
	}
	if err := ValidatePassword(in.Password); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(in.OrgName) == "" {
		in.OrgName = in.Email
	}
	if strings.TrimSpace(in.Name) == "" {
		in.Name = strings.Split(in.Email, "@")[0]
	}
	var cnt int64
	s.DB.Model(&User{}).Where("lower(email) = ? OR username = ?", in.Email, in.Email).Count(&cnt)
	if cnt > 0 {
		return nil, nil, ErrConflict("email_taken", "e-mail já cadastrado")
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return nil, nil, err
	}
	org := &gm.Org{Name: strings.TrimSpace(in.OrgName), Status: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	user := &User{}
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		base := Slugify(org.Name)
		org.Slug = base
		for i := 2; ; i++ {
			var n int64
			tx.Model(&gm.Org{}).Where("slug = ?", org.Slug).Count(&n)
			if n == 0 {
				break
			}
			org.Slug = base + "-" + itoa(i)
		}
		if err := tx.Create(org).Error; err != nil {
			return err
		}
		grp := &model.Group{Name: org.Name, Type: model.GroupTypeDefault}
		if err := tx.Create(grp).Error; err != nil {
			return err
		}
		if err := tx.Table("groups").Where("id = ?", grp.Id).Update("org_id", org.ID).Error; err != nil {
			return err
		}
		isAdmin := false
		up := &model.User{
			Username: in.Email, Email: in.Email, Password: hash, Nickname: in.Name,
			GroupId: grp.Id, IsAdmin: &isAdmin, Status: model.COMMON_STATUS_ENABLE,
		}
		if err := tx.Create(up).Error; err != nil {
			return err
		}
		if err := tx.Table("users").Where("id = ?", up.Id).Updates(map[string]interface{}{
			"org_id": org.ID, "role": gm.RoleOwner, "is_ops": isOpsEmail(in.Email),
		}).Error; err != nil {
			return err
		}
		var free gm.Plan
		if err := tx.Where("code = ?", "free").First(&free).Error; err != nil {
			return err
		}
		if err := tx.Create(&gm.Subscription{OrgID: org.ID, PlanID: free.ID, Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now()}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", up.Id).First(user).Error
	})
	if err != nil {
		return nil, nil, err
	}
	c.User = user
	s.Audit(c, org.ID, "auth.signup", "org", org.ID, map[string]interface{}{"email": user.Email})
	return user, org, nil
}

func itoa(i int) string { return strconv.Itoa(i) }

func isOpsEmail(email string) bool { return config.IsOpsEmail(email) }

// OrgByID carrega a org.
func (s *Svc) OrgByID(id uint) (*gm.Org, error) {
	o := &gm.Org{}
	if err := s.DB.Where("id = ?", id).First(o).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound("organização")
		}
		return nil, err
	}
	return o, nil
}

// OrgUpdate altera nome da org.
func (s *Svc) OrgUpdate(c Ctx, orgID uint, name string) (*gm.Org, error) {
	if !perm.Can(c.Actor(), perm.OrgManage, perm.Resource{OrgID: orgID}) {
		return nil, ErrForbidden()
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrValidation("nome obrigatório", nil)
	}
	if err := s.DB.Model(&gm.Org{}).Where("id = ?", orgID).Updates(map[string]interface{}{"name": name, "updated_at": time.Now()}).Error; err != nil {
		return nil, err
	}
	s.Audit(c, orgID, "org.update", "org", orgID, map[string]interface{}{"name": name})
	return s.OrgByID(orgID)
}

// defaultGroupID devolve o grupo upstream da org (cria se faltar).
func (s *Svc) defaultGroupID(tx *gorm.DB, org *gm.Org) (uint, error) {
	var id uint
	tx.Raw("SELECT id FROM groups WHERE org_id = ? AND type = ? ORDER BY id LIMIT 1", org.ID, model.GroupTypeDefault).Scan(&id)
	if id != 0 {
		return id, nil
	}
	grp := &model.Group{Name: org.Name, Type: model.GroupTypeDefault}
	if err := tx.Create(grp).Error; err != nil {
		return 0, err
	}
	return grp.Id, tx.Table("groups").Where("id = ?", grp.Id).Update("org_id", org.ID).Error
}

// OrgUsers lista usuários da org.
func (s *Svc) OrgUsers(c Ctx, orgID uint, p Page) ([]User, Meta, error) {
	if !perm.Can(c.Actor(), perm.OrgManage, perm.Resource{OrgID: orgID}) {
		return nil, Meta{}, ErrForbidden()
	}
	p = p.Norm()
	q := s.DB.Model(&User{}).Where("org_id = ?", orgID)
	var total int64
	q.Count(&total)
	var rows []User
	if err := q.Order("id").Offset(p.offset()).Limit(p.PerPage).Find(&rows).Error; err != nil {
		return nil, Meta{}, err
	}
	if rows == nil {
		rows = []User{}
	}
	return rows, p.meta(total), nil
}

// CreateUserInput é o cadastro de membro pela org.
type CreateUserInput struct {
	Email    string
	Name     string
	Role     string
	Password string
}

// OrgUserCreate adiciona um membro; devolve senha temporária quando gerada.
func (s *Svc) OrgUserCreate(c Ctx, orgID uint, in CreateUserInput) (*User, string, error) {
	if !perm.Can(c.Actor(), perm.OrgManage, perm.Resource{OrgID: orgID}) {
		return nil, "", ErrForbidden()
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if !emailRe.MatchString(in.Email) {
		return nil, "", ErrValidation("e-mail inválido", map[string]string{"field": "email"})
	}
	if in.Role == "" {
		in.Role = gm.RoleTech
	}
	if !gm.ValidRole(in.Role) {
		return nil, "", ErrValidation("papel inválido", map[string]string{"field": "role"})
	}
	if in.Role == gm.RoleOwner && c.User.Role != gm.RoleOwner && !c.User.IsOps {
		return nil, "", ErrForbidden()
	}
	if in.Role != gm.RoleViewer {
		lim, err := s.Limits(orgID)
		if err != nil {
			return nil, "", err
		}
		if !perm.Can(c.Actor(), perm.UserAdd, perm.Resource{OrgID: orgID, Limits: lim}) {
			return nil, "", ErrPlanLimit("técnicos")
		}
	}
	var cnt int64
	s.DB.Model(&User{}).Where("lower(email) = ? OR username = ?", in.Email, in.Email).Count(&cnt)
	if cnt > 0 {
		return nil, "", ErrConflict("email_taken", "e-mail já cadastrado")
	}
	temp := ""
	if in.Password == "" {
		pw, err := auth.RandomCode(12)
		if err != nil {
			return nil, "", err
		}
		in.Password, temp = pw, pw
	} else if err := ValidatePassword(in.Password); err != nil {
		return nil, "", err
	}
	if in.Name == "" {
		in.Name = strings.Split(in.Email, "@")[0]
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return nil, "", err
	}
	org, err := s.OrgByID(orgID)
	if err != nil {
		return nil, "", err
	}
	user := &User{}
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		gid, err := s.defaultGroupID(tx, org)
		if err != nil {
			return err
		}
		isAdmin := false
		up := &model.User{Username: in.Email, Email: in.Email, Password: hash, Nickname: in.Name,
			GroupId: gid, IsAdmin: &isAdmin, Status: model.COMMON_STATUS_ENABLE}
		if err := tx.Create(up).Error; err != nil {
			return err
		}
		if err := tx.Table("users").Where("id = ?", up.Id).Updates(map[string]interface{}{
			"org_id": orgID, "role": in.Role, "is_ops": isOpsEmail(in.Email)}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", up.Id).First(user).Error
	})
	if err != nil {
		return nil, "", err
	}
	s.Audit(c, orgID, "org.user.create", "user", user.ID, map[string]interface{}{"email": user.Email, "role": in.Role})
	return user, temp, nil
}

// UpdateUserInput altera papel/status/nome.
type UpdateUserInput struct {
	Name   *string
	Role   *string
	Status *int
}

// OrgUserUpdate altera um membro.
func (s *Svc) OrgUserUpdate(c Ctx, orgID, userID uint, in UpdateUserInput) (*User, error) {
	if !perm.Can(c.Actor(), perm.OrgManage, perm.Resource{OrgID: orgID}) {
		return nil, ErrForbidden()
	}
	u, err := s.UserByID(userID)
	if err != nil || u.OrgID != orgID {
		return nil, ErrNotFound("usuário")
	}
	upd := map[string]interface{}{}
	if in.Name != nil {
		upd["nickname"] = strings.TrimSpace(*in.Name)
	}
	if in.Role != nil {
		if !gm.ValidRole(*in.Role) {
			return nil, ErrValidation("papel inválido", map[string]string{"field": "role"})
		}
		if (*in.Role == gm.RoleOwner || u.Role == gm.RoleOwner) && c.User.Role != gm.RoleOwner && !c.User.IsOps {
			return nil, ErrForbidden()
		}
		if u.Role == gm.RoleOwner && *in.Role != gm.RoleOwner && s.countRole(orgID, gm.RoleOwner) <= 1 {
			return nil, ErrConflict("last_owner", "a org precisa de ao menos um owner")
		}
		upd["role"] = *in.Role
	}
	if in.Status != nil {
		if *in.Status != 1 && *in.Status != 2 {
			return nil, ErrValidation("status deve ser 1 (ativo) ou 2 (desativado)", nil)
		}
		if u.ID == c.User.ID && *in.Status == 2 {
			return nil, ErrConflict("self_disable", "não é possível desativar a si mesmo")
		}
		upd["status"] = *in.Status
	}
	if len(upd) > 0 {
		if err := s.DB.Table("users").Where("id = ?", userID).Updates(upd).Error; err != nil {
			return nil, err
		}
		if st, ok := upd["status"]; ok && st == 2 {
			s.DB.Model(&gm.RefreshToken{}).Where("user_id = ? AND revoked_at IS NULL", userID).Update("revoked_at", time.Now())
		}
	}
	s.Audit(c, orgID, "org.user.update", "user", userID, map[string]interface{}{"changes": upd})
	return s.UserByID(userID)
}

func (s *Svc) countRole(orgID uint, role string) int64 {
	var n int64
	s.DB.Model(&User{}).Where("org_id = ? AND role = ? AND status = 1", orgID, role).Count(&n)
	return n
}

// OrgUserDelete remove um membro (desvincula dispositivos).
func (s *Svc) OrgUserDelete(c Ctx, orgID, userID uint) error {
	if !perm.Can(c.Actor(), perm.OrgManage, perm.Resource{OrgID: orgID}) {
		return ErrForbidden()
	}
	u, err := s.UserByID(userID)
	if err != nil || u.OrgID != orgID {
		return ErrNotFound("usuário")
	}
	if u.ID == c.User.ID {
		return ErrConflict("self_delete", "não é possível remover a si mesmo")
	}
	if u.Role == gm.RoleOwner && (c.User.Role != gm.RoleOwner && !c.User.IsOps) {
		return ErrForbidden()
	}
	if u.Role == gm.RoleOwner && s.countRole(orgID, gm.RoleOwner) <= 1 {
		return ErrConflict("last_owner", "a org precisa de ao menos um owner")
	}
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		for _, stmt := range []struct {
			sql  string
			args []interface{}
		}{
			{"DELETE FROM user_tokens WHERE user_id = ?", []interface{}{userID}},
			{"DELETE FROM refresh_tokens WHERE user_id = ?", []interface{}{userID}},
			{"DELETE FROM device_shares WHERE target_type = 'user' AND target_id = ?", []interface{}{userID}},
			{"DELETE FROM address_books WHERE user_id = ?", []interface{}{userID}},
			{"DELETE FROM tags WHERE user_id = ?", []interface{}{userID}},
			{"DELETE FROM user_thirds WHERE user_id = ?", []interface{}{userID}},
			{"UPDATE peers SET user_id = 0 WHERE user_id = ?", []interface{}{userID}},
			{"DELETE FROM users WHERE id = ?", []interface{}{userID}},
		} {
			if err := tx.Exec(stmt.sql, stmt.args...).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.Audit(c, orgID, "org.user.delete", "user", userID, map[string]interface{}{"email": u.Email})
	return nil
}

// Branding lê o white-label da org (qualquer membro pode ler).
func (s *Svc) Branding(orgID uint) (*gm.OrgBranding, error) {
	b := &gm.OrgBranding{OrgID: orgID}
	err := s.DB.Where("org_id = ?", orgID).First(b).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return b, nil
}

// BrandingUpdate grava o white-label.
func (s *Svc) BrandingUpdate(c Ctx, orgID uint, in gm.OrgBranding) (*gm.OrgBranding, error) {
	if !perm.Can(c.Actor(), perm.OrgManage, perm.Resource{OrgID: orgID}) {
		return nil, ErrForbidden()
	}
	in.OrgID = orgID
	in.UpdatedAt = time.Now()
	if in.PrimaryColor != "" && !regexp.MustCompile(`^#[0-9a-fA-F]{6}$`).MatchString(in.PrimaryColor) {
		return nil, ErrValidation("primary_color deve ser #RRGGBB", map[string]string{"field": "primary_color"})
	}
	err := s.DB.Exec(`INSERT INTO org_branding (org_id, product_name, logo_url, primary_color, updated_at)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT (org_id) DO UPDATE SET product_name = EXCLUDED.product_name,
		logo_url = EXCLUDED.logo_url, primary_color = EXCLUDED.primary_color, updated_at = EXCLUDED.updated_at`,
		orgID, in.ProductName, in.LogoURL, in.PrimaryColor, in.UpdatedAt).Error
	if err != nil {
		return nil, err
	}
	s.Audit(c, orgID, "org.branding.update", "org", orgID, map[string]interface{}{"product_name": in.ProductName})
	return s.Branding(orgID)
}

// SsoView é o SSO com segredo mascarado.
type SsoView struct {
	gm.OrgSso
	HasSecret bool `json:"has_client_secret"`
}

// Sso lê a config OIDC (segredo nunca sai).
func (s *Svc) Sso(c Ctx, orgID uint) (*SsoView, error) {
	if !perm.Can(c.Actor(), perm.OrgManage, perm.Resource{OrgID: orgID}) {
		return nil, ErrForbidden()
	}
	row := gm.OrgSso{OrgID: orgID, Scopes: "openid email profile"}
	err := s.DB.Where("org_id = ?", orgID).First(&row).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &SsoView{OrgSso: row, HasSecret: row.ClientSecretEnc != ""}, nil
}

// SsoInput é o PATCH do SSO.
type SsoInput struct {
	Enabled      *bool
	Issuer       *string
	ClientID     *string
	ClientSecret *string
	Scopes       *string
}

// SsoUpdate grava a config OIDC com segredo cifrado.
func (s *Svc) SsoUpdate(c Ctx, orgID uint, in SsoInput) (*SsoView, error) {
	cur, err := s.Sso(c, orgID)
	if err != nil {
		return nil, err
	}
	row := cur.OrgSso
	if in.Enabled != nil {
		row.Enabled = *in.Enabled
	}
	if in.Issuer != nil {
		row.Issuer = strings.TrimSpace(*in.Issuer)
	}
	if in.ClientID != nil {
		row.ClientID = strings.TrimSpace(*in.ClientID)
	}
	if in.Scopes != nil {
		row.Scopes = strings.TrimSpace(*in.Scopes)
	}
	if in.ClientSecret != nil {
		enc, err := s.Cipher.Encrypt(*in.ClientSecret)
		if err != nil {
			return nil, err
		}
		row.ClientSecretEnc = enc
	}
	if row.Enabled && (row.Issuer == "" || row.ClientID == "" || row.ClientSecretEnc == "") {
		return nil, ErrValidation("para habilitar o SSO informe issuer, client_id e client_secret", nil)
	}
	row.UpdatedAt = time.Now()
	err = s.DB.Exec(`INSERT INTO org_sso (org_id, enabled, issuer, client_id, client_secret_enc, scopes, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT (org_id) DO UPDATE SET enabled = EXCLUDED.enabled, issuer = EXCLUDED.issuer,
		client_id = EXCLUDED.client_id, client_secret_enc = EXCLUDED.client_secret_enc, scopes = EXCLUDED.scopes, updated_at = EXCLUDED.updated_at`,
		orgID, row.Enabled, row.Issuer, row.ClientID, row.ClientSecretEnc, row.Scopes, row.UpdatedAt).Error
	if err != nil {
		return nil, err
	}
	s.Audit(c, orgID, "org.sso.update", "org", orgID, map[string]interface{}{"enabled": row.Enabled, "issuer": row.Issuer})
	return s.Sso(c, orgID)
}

// OpsOrg é a linha da listagem de ops.
type OpsOrg struct {
	gm.Org
	Users   int64  `json:"users"`
	Devices int64  `json:"devices"`
	Plan    string `json:"plan"`
}

// OpsOrgs lista todas as orgs (só ops).
func (s *Svc) OpsOrgs(c Ctx, p Page) ([]OpsOrg, Meta, error) {
	if !perm.Can(c.Actor(), perm.Ops, perm.Resource{}) {
		return nil, Meta{}, ErrForbidden()
	}
	p = p.Norm()
	var total int64
	s.DB.Model(&gm.Org{}).Count(&total)
	var orgs []gm.Org
	if err := s.DB.Order("id").Offset(p.offset()).Limit(p.PerPage).Find(&orgs).Error; err != nil {
		return nil, Meta{}, err
	}
	out := make([]OpsOrg, 0, len(orgs))
	for _, o := range orgs {
		row := OpsOrg{Org: o}
		s.DB.Model(&User{}).Where("org_id = ?", o.ID).Count(&row.Users)
		s.DB.Model(&gm.Device{}).Where("org_id = ?", o.ID).Count(&row.Devices)
		s.DB.Raw(`SELECT p.code FROM subscriptions s JOIN plans p ON p.id = s.plan_id WHERE s.org_id = ?`, o.ID).Scan(&row.Plan)
		out = append(out, row)
	}
	return out, p.meta(total), nil
}
