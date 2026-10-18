package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/lejianwen/rustdesk-api/v2/george/ai"
	gm "github.com/lejianwen/rustdesk-api/v2/george/model"
	"github.com/lejianwen/rustdesk-api/v2/george/perm"
	"gorm.io/gorm"
)

// DeviceView é o dispositivo serializado para a API.
type DeviceView struct {
	gm.Device
	Name            string           `json:"name"`
	Online          bool             `json:"online"`
	Enrolled        bool             `json:"enrolled"`
	Revoked         bool             `json:"revoked"`
	PasswordApplied bool             `json:"password_applied"`
	GroupName       string           `json:"group_name"`
	Shares          []gm.DeviceShare `json:"shares,omitempty"`
}

func view(d gm.Device, shares []gm.DeviceShare) DeviceView {
	return DeviceView{Device: d, Name: d.Name(), Online: d.Online(), Enrolled: d.Enrolled(), Revoked: d.Revoked(),
		PasswordApplied: d.PasswordApplied(), Shares: shares}
}

// groupNames resolve group_name de um lote de dispositivos (uma query).
func (s *Svc) groupNames(ids []uint) map[uint]string {
	out := map[uint]string{}
	want := []uint{}
	for _, id := range ids {
		if id != 0 {
			want = append(want, id)
		}
	}
	if len(want) == 0 {
		return out
	}
	var rows []gm.DeviceGroup
	s.DB.Where("id IN ?", want).Find(&rows)
	for _, g := range rows {
		out[g.ID] = g.Name
	}
	return out
}

// viewOf monta a visão de um dispositivo com o nome do setor.
func (s *Svc) viewOf(d gm.Device, shares []gm.DeviceShare) DeviceView {
	v := view(d, shares)
	v.GroupName = s.groupNames([]uint{d.GroupID})[d.GroupID]
	return v
}

// DeviceFilter filtra a listagem.
type DeviceFilter struct {
	Q           string
	Online      *bool
	HostEnabled *bool
	GroupID     *uint
	Os          string
	Enrolled    *bool
}

func (s *Svc) sharesOf(deviceID uint) []gm.DeviceShare {
	var sh []gm.DeviceShare
	s.DB.Where("device_id = ?", deviceID).Order("id").Find(&sh)
	return sh
}

func toPermDevice(d *gm.Device, shares []gm.DeviceShare) *perm.Device {
	pd := &perm.Device{OrgID: d.OrgID, OwnerUserID: d.UserID, OrgOwned: d.OrgOwned()}
	for _, sh := range shares {
		pd.Shares = append(pd.Shares, perm.Share{TargetType: sh.TargetType, TargetID: sh.TargetID, Level: sh.Level})
	}
	return pd
}

// deviceScope limita a query ao que o ator pode ver.
func (s *Svc) deviceScope(c Ctx, q *gorm.DB) *gorm.DB {
	u := c.User
	if u.IsOps {
		return q
	}
	q = q.Where("peers.org_id = ?", u.OrgID)
	if perm.IsAdmin(u.Role) || u.Role == gm.RoleViewer {
		return q
	}
	return q.Where(`(peers.user_id = ? OR (peers.enrolled_at IS NOT NULL AND peers.revoked_at IS NULL) OR peers.row_id IN (
		SELECT device_id FROM device_shares WHERE org_id = ? AND (
			(target_type = 'user' AND target_id = ?) OR (target_type = 'group' AND target_id = ?))))`,
		u.ID, u.OrgID, u.ID, u.GroupID)
}

func applyFilter(q *gorm.DB, f DeviceFilter) *gorm.DB {
	if t := strings.TrimSpace(f.Q); t != "" {
		like := "%" + strings.ToLower(t) + "%"
		q = q.Where("(lower(peers.hostname) LIKE ? OR lower(peers.alias) LIKE ? OR lower(peers.username) LIKE ? OR lower(peers.os) LIKE ? OR peers.id LIKE ?)",
			like, like, like, like, like)
	}
	if f.Online != nil {
		cut := time.Now().Unix() - 90
		if *f.Online {
			q = q.Where("peers.last_online_time > ?", cut)
		} else {
			q = q.Where("peers.last_online_time <= ?", cut)
		}
	}
	if f.HostEnabled != nil {
		q = q.Where("peers.host_enabled = ?", *f.HostEnabled)
	}
	if f.GroupID != nil {
		q = q.Where("peers.group_id = ?", *f.GroupID)
	}
	if f.Enrolled != nil {
		if *f.Enrolled {
			q = q.Where("peers.enrolled_at IS NOT NULL")
		} else {
			q = q.Where("peers.enrolled_at IS NULL")
		}
	}
	if f.Os != "" {
		q = q.Where("(lower(peers.os) LIKE ? OR lower(peers.platform) LIKE ?)", "%"+strings.ToLower(f.Os)+"%", "%"+strings.ToLower(f.Os)+"%")
	}
	return q
}

// Devices lista dispositivos visíveis ao ator.
func (s *Svc) Devices(c Ctx, f DeviceFilter, p Page) ([]DeviceView, Meta, error) {
	p = p.Norm()
	q := applyFilter(s.deviceScope(c, s.DB.Model(&gm.Device{})), f)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, Meta{}, err
	}
	var rows []gm.Device
	if err := q.Order("peers.last_online_time DESC, peers.row_id").Offset(p.offset()).Limit(p.PerPage).Find(&rows).Error; err != nil {
		return nil, Meta{}, err
	}
	gids := make([]uint, 0, len(rows))
	for _, d := range rows {
		gids = append(gids, d.GroupID)
	}
	names := s.groupNames(gids)
	out := make([]DeviceView, 0, len(rows))
	for _, d := range rows {
		v := view(d, nil)
		v.GroupName = names[d.GroupID]
		out = append(out, v)
	}
	return out, p.meta(total), nil
}

// DeviceSearch é a busca natural (IA quando disponível, heurística caso contrário).
func (s *Svc) DeviceSearch(c Ctx, q string, p Page) ([]DeviceView, Meta, ai.SearchFilter, string, error) {
	var f ai.SearchFilter
	provider := "heuristic"
	if s.AI.Enabled() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if pf, err := s.AI.ParseSearch(ctx, q); err == nil {
			f, provider = pf, "ai"
		} else {
			s.Log.WithError(err).Warn("ia: busca natural falhou, usando heurística")
			f = ai.HeuristicSearch(q)
		}
	} else {
		f = ai.HeuristicSearch(q)
	}
	rows, meta, err := s.Devices(c, DeviceFilter{Q: f.Text, Online: f.Online, HostEnabled: f.HostEnabled, Os: f.Os}, p)
	return rows, meta, f, provider, err
}

// deviceWithPerm carrega o device e verifica a ação.
func (s *Svc) deviceWithPerm(c Ctx, id uint, action perm.Action) (*gm.Device, []gm.DeviceShare, error) {
	d := &gm.Device{}
	if err := s.DB.Where("row_id = ?", id).First(d).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrNotFound("dispositivo")
		}
		return nil, nil, err
	}
	shares := s.sharesOf(id)
	if !perm.Can(c.Actor(), action, perm.Resource{OrgID: d.OrgID, Device: toPermDevice(d, shares)}) {
		// fora da org responde 404 para não vazar existência
		if d.OrgID != c.User.OrgID && !c.User.IsOps {
			return nil, nil, ErrNotFound("dispositivo")
		}
		return nil, nil, ErrForbidden()
	}
	return d, shares, nil
}

// Device devolve um dispositivo.
func (s *Svc) Device(c Ctx, id uint) (*DeviceView, error) {
	d, shares, err := s.deviceWithPerm(c, id, perm.DeviceView)
	if err != nil {
		return nil, err
	}
	v := s.viewOf(*d, shares)
	return &v, nil
}

// DeviceUpdateInput é o PATCH do dispositivo.
type DeviceUpdateInput struct {
	Name        *string
	HostEnabled *bool
	GroupID     *uint
}

// DeviceUpdate renomeia, liga/desliga host ou move de grupo.
func (s *Svc) DeviceUpdate(c Ctx, id uint, in DeviceUpdateInput) (*DeviceView, error) {
	d, _, err := s.deviceWithPerm(c, id, perm.DeviceManage)
	if err != nil {
		return nil, err
	}
	upd := map[string]interface{}{}
	if in.Name != nil {
		upd["alias"] = strings.TrimSpace(*in.Name)
	}
	if in.HostEnabled != nil {
		upd["host_enabled"] = *in.HostEnabled
	}
	if in.GroupID != nil {
		if *in.GroupID != 0 {
			var n int64
			s.DB.Model(&gm.DeviceGroup{}).Where("id = ? AND org_id = ?", *in.GroupID, d.OrgID).Count(&n)
			if n == 0 {
				return nil, ErrNotFound("grupo")
			}
		}
		upd["group_id"] = *in.GroupID
	}
	if len(upd) > 0 {
		if err := s.DB.Model(&gm.Device{}).Where("row_id = ?", id).Updates(upd).Error; err != nil {
			return nil, err
		}
	}
	if in.HostEnabled != nil {
		action := "device.host_off"
		if *in.HostEnabled {
			action = "device.host_on"
		}
		s.Audit(c, d.OrgID, action, "device", id, map[string]interface{}{"rustdesk_id": d.ID})
	}
	if in.Name != nil {
		s.Audit(c, d.OrgID, "device.rename", "device", id, map[string]interface{}{"rustdesk_id": d.ID, "from": d.Alias, "alias": upd["alias"]})
	}
	if in.GroupID != nil {
		s.Audit(c, d.OrgID, "device.move", "device", id, map[string]interface{}{"rustdesk_id": d.ID, "from_group_id": d.GroupID, "group_id": *in.GroupID})
	}
	return s.Device(c, id)
}

// DeviceDelete remove o dispositivo da conta.
func (s *Svc) DeviceDelete(c Ctx, id uint) error {
	d, _, err := s.deviceWithPerm(c, id, perm.DeviceManage)
	if err != nil {
		return err
	}
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DELETE FROM device_shares WHERE device_id = ?", id).Error; err != nil {
			return err
		}
		if err := tx.Exec("UPDATE invites SET revoked_at = now() WHERE device_id = ? AND revoked_at IS NULL", id).Error; err != nil {
			return err
		}
		if d.Uuid != "" {
			tx.Exec("DELETE FROM user_tokens WHERE device_uuid = ?", d.Uuid)
		}
		return tx.Exec("DELETE FROM peers WHERE row_id = ?", id).Error
	})
	if err != nil {
		return err
	}
	s.Audit(c, d.OrgID, "device.delete", "device", id, map[string]interface{}{"rustdesk_id": d.ID})
	return nil
}

// ShareInput é o POST /devices/{id}/share.
type ShareInput struct {
	TargetType string
	TargetID   uint
	Level      string
}

// DeviceShare compartilha com usuário ou grupo da org.
func (s *Svc) DeviceShare(c Ctx, id uint, in ShareInput) (*gm.DeviceShare, error) {
	d, _, err := s.deviceWithPerm(c, id, perm.DeviceManage)
	if err != nil {
		return nil, err
	}
	if in.Level == "" {
		in.Level = gm.ShareConnect
	}
	if in.Level != gm.ShareConnect && in.Level != gm.ShareManage {
		return nil, ErrValidation("level deve ser connect ou manage", map[string]string{"field": "level"})
	}
	switch in.TargetType {
	case gm.ShareTargetUser:
		var n int64
		s.DB.Model(&User{}).Where("id = ? AND org_id = ?", in.TargetID, d.OrgID).Count(&n)
		if n == 0 {
			return nil, ErrNotFound("usuário da org")
		}
	case gm.ShareTargetGroup:
		var n int64
		s.DB.Table("groups").Where("id = ? AND org_id = ?", in.TargetID, d.OrgID).Count(&n)
		if n == 0 {
			return nil, ErrNotFound("grupo de usuários da org")
		}
	default:
		return nil, ErrValidation("target_type deve ser user ou group", map[string]string{"field": "target_type"})
	}
	sh := &gm.DeviceShare{OrgID: d.OrgID, DeviceID: id, TargetType: in.TargetType, TargetID: in.TargetID, Level: in.Level, CreatedBy: c.User.ID, CreatedAt: time.Now()}
	err = s.DB.Exec(`INSERT INTO device_shares (org_id, device_id, target_type, target_id, level, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT (device_id, target_type, target_id) DO UPDATE SET level = EXCLUDED.level`,
		sh.OrgID, sh.DeviceID, sh.TargetType, sh.TargetID, sh.Level, sh.CreatedBy, sh.CreatedAt).Error
	if err != nil {
		return nil, err
	}
	s.DB.Where("device_id = ? AND target_type = ? AND target_id = ?", id, in.TargetType, in.TargetID).First(sh)
	s.Audit(c, d.OrgID, "device.share", "device", id, map[string]interface{}{"target_type": in.TargetType, "target_id": in.TargetID, "level": in.Level})
	return sh, nil
}

// DeviceUnshare remove um compartilhamento.
func (s *Svc) DeviceUnshare(c Ctx, id, shareID uint) error {
	d, _, err := s.deviceWithPerm(c, id, perm.DeviceManage)
	if err != nil {
		return err
	}
	res := s.DB.Where("id = ? AND device_id = ?", shareID, id).Delete(&gm.DeviceShare{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound("compartilhamento")
	}
	s.Audit(c, d.OrgID, "device.unshare", "device", id, map[string]interface{}{"share_id": shareID})
	return nil
}

// HeartbeatInput é o que o cliente George envia periodicamente.
type HeartbeatInput struct {
	ID          string `json:"id"`
	Uuid        string `json:"uuid"`
	Hostname    string `json:"hostname"`
	Platform    string `json:"platform"`
	Os          string `json:"os"`
	Version     string `json:"version"`
	Username    string `json:"username"`
	HostEnabled *bool  `json:"host_enabled"`
}

// PendingInvite é um convite ativo vinculado ao dispositivo (com a senha de sessão, só para o host).
type PendingInvite struct {
	Code            string    `json:"code"`
	SessionPassword string    `json:"session_password"`
	ExpiresAt       time.Time `json:"expires_at"`
}

// HeartbeatOut é a resposta do heartbeat.
type HeartbeatOut struct {
	Device         DeviceView      `json:"device"`
	HostEnabled    bool            `json:"host_enabled"`
	PendingInvites []PendingInvite `json:"pending_invites"`
	ServerTime     time.Time       `json:"server_time"`
}

// Heartbeat registra/atualiza o dispositivo do usuário logado e devolve o estado do host.
func (s *Svc) Heartbeat(c Ctx, in HeartbeatInput) (*HeartbeatOut, error) {
	u := c.User
	if in.ID == "" && in.Uuid == "" {
		return nil, ErrValidation("informe id e/ou uuid do dispositivo", nil)
	}
	d := &gm.Device{}
	q := s.DB.Model(&gm.Device{})
	if in.Uuid != "" {
		q = q.Where("uuid = ?", in.Uuid)
	} else {
		q = q.Where("id = ?", in.ID)
	}
	err := q.First(d).Error
	now := time.Now()
	if errors.Is(err, gorm.ErrRecordNotFound) && in.Uuid != "" && in.ID != "" {
		err = s.DB.Where("id = ? AND uuid = ''", in.ID).First(d).Error
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		lim, err := s.Limits(u.OrgID)
		if err != nil {
			return nil, err
		}
		if !perm.Can(u.Actor(), perm.DeviceAdd, perm.Resource{OrgID: u.OrgID, Limits: lim}) {
			if lim != nil && lim.MaxDevices > 0 && lim.Devices >= lim.MaxDevices {
				return nil, ErrPlanLimit("dispositivos")
			}
			return nil, ErrForbidden()
		}
		host := true
		if in.HostEnabled != nil {
			host = *in.HostEnabled
		}
		d = &gm.Device{ID: in.ID, Uuid: in.Uuid, Hostname: in.Hostname, Platform: in.Platform, Os: in.Os,
			Version: in.Version, Username: in.Username, UserID: u.ID, OrgID: u.OrgID, HostEnabled: host,
			LastOnlineTime: now.Unix(), LastOnlineIP: c.IP}
		if err := s.DB.Exec(`INSERT INTO peers (id, uuid, hostname, platform, os, version, username, user_id, org_id, host_enabled,
			last_online_time, last_online_ip, cpu, memory, alias, group_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', '', 0, ?, ?)`,
			d.ID, d.Uuid, d.Hostname, d.Platform, d.Os, d.Version, d.Username, d.UserID, d.OrgID, d.HostEnabled,
			d.LastOnlineTime, d.LastOnlineIP, now, now).Error; err != nil {
			return nil, err
		}
		if err := s.DB.Where("uuid = ? AND id = ?", d.Uuid, d.ID).Order("row_id DESC").First(d).Error; err != nil {
			return nil, err
		}
		s.Audit(c, u.OrgID, "device.register", "device", d.RowID, map[string]interface{}{"rustdesk_id": d.ID, "hostname": d.Hostname})
	} else if err != nil {
		return nil, err
	} else {
		if d.OrgID != 0 && d.OrgID != u.OrgID && !u.IsOps {
			return nil, ErrConflict("device_other_org", "dispositivo registrado em outra organização")
		}
		if d.OrgID == u.OrgID && d.UserID != u.ID && !perm.Can(u.Actor(), perm.DeviceManage, perm.Resource{OrgID: d.OrgID, Device: toPermDevice(d, s.sharesOf(d.RowID))}) {
			return nil, ErrForbidden()
		}
		upd := map[string]interface{}{"last_online_time": now.Unix(), "last_online_ip": c.IP, "updated_at": now}
		if d.OrgID == 0 {
			upd["org_id"] = u.OrgID
		}
		if d.UserID == 0 && d.EnrolledAt == nil { // vinculado por enrollment é da org, não de um usuário
			upd["user_id"] = u.ID
		}
		if in.ID != "" && d.ID != in.ID {
			upd["id"] = in.ID
		}
		if in.Uuid != "" && d.Uuid == "" {
			upd["uuid"] = in.Uuid
		}
		for k, v := range map[string]string{"hostname": in.Hostname, "platform": in.Platform, "os": in.Os, "version": in.Version, "username": in.Username} {
			if v != "" {
				upd[k] = v
			}
		}
		if in.HostEnabled != nil && *in.HostEnabled != d.HostEnabled {
			upd["host_enabled"] = *in.HostEnabled
			action := "device.host_off"
			if *in.HostEnabled {
				action = "device.host_on"
			}
			s.Audit(c, u.OrgID, action, "device", d.RowID, map[string]interface{}{"rustdesk_id": d.ID, "source": "client"})
		}
		if err := s.DB.Model(&gm.Device{}).Where("row_id = ?", d.RowID).Updates(upd).Error; err != nil {
			return nil, err
		}
		if err := s.DB.Where("row_id = ?", d.RowID).First(d).Error; err != nil {
			return nil, err
		}
	}
	pend, err := s.PendingInvites(d.RowID)
	if err != nil {
		return nil, err
	}
	return &HeartbeatOut{Device: s.viewOf(*d, nil), HostEnabled: d.HostEnabled, PendingInvites: pend, ServerTime: now}, nil
}

// DeviceGroups lista grupos de dispositivos da org.
func (s *Svc) DeviceGroups(c Ctx, orgID uint, p Page) ([]gm.DeviceGroup, Meta, error) {
	if !perm.Can(c.Actor(), perm.DeviceView, perm.Resource{OrgID: orgID}) {
		return nil, Meta{}, ErrForbidden()
	}
	p = p.Norm()
	q := s.DB.Model(&gm.DeviceGroup{}).Where("org_id = ?", orgID)
	var total int64
	q.Count(&total)
	var rows []gm.DeviceGroup
	if err := q.Order("name").Offset(p.offset()).Limit(p.PerPage).Find(&rows).Error; err != nil {
		return nil, Meta{}, err
	}
	if rows == nil {
		rows = []gm.DeviceGroup{}
	}
	return rows, p.meta(total), nil
}

// DeviceGroupCreate cria grupo.
func (s *Svc) DeviceGroupCreate(c Ctx, orgID uint, name string) (*gm.DeviceGroup, error) {
	if !perm.Can(c.Actor(), perm.GroupManage, perm.Resource{OrgID: orgID}) {
		return nil, ErrForbidden()
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrValidation("nome obrigatório", map[string]string{"field": "name"})
	}
	g := &gm.DeviceGroup{OrgID: orgID, Name: name, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := s.DB.Create(g).Error; err != nil {
		return nil, err
	}
	s.Audit(c, orgID, "group.create", "group", g.ID, map[string]interface{}{"name": name})
	return g, nil
}

func (s *Svc) groupOf(c Ctx, orgID, id uint, action perm.Action) (*gm.DeviceGroup, error) {
	g := &gm.DeviceGroup{}
	if err := s.DB.Where("id = ? AND org_id = ?", id, orgID).First(g).Error; err != nil {
		return nil, ErrNotFound("grupo")
	}
	if !perm.Can(c.Actor(), action, perm.Resource{OrgID: orgID}) {
		return nil, ErrForbidden()
	}
	return g, nil
}

// DeviceGroupGet devolve um grupo.
func (s *Svc) DeviceGroupGet(c Ctx, orgID, id uint) (*gm.DeviceGroup, error) {
	return s.groupOf(c, orgID, id, perm.DeviceView)
}

// DeviceGroupUpdate renomeia.
func (s *Svc) DeviceGroupUpdate(c Ctx, orgID, id uint, name string) (*gm.DeviceGroup, error) {
	g, err := s.groupOf(c, orgID, id, perm.GroupManage)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrValidation("nome obrigatório", map[string]string{"field": "name"})
	}
	if err := s.DB.Model(g).Updates(map[string]interface{}{"name": name, "updated_at": time.Now()}).Error; err != nil {
		return nil, err
	}
	s.Audit(c, orgID, "group.update", "group", id, map[string]interface{}{"name": name})
	return g, nil
}

// DeviceGroupDelete apaga o grupo (dispositivos voltam para sem grupo).
func (s *Svc) DeviceGroupDelete(c Ctx, orgID, id uint) error {
	if _, err := s.groupOf(c, orgID, id, perm.GroupManage); err != nil {
		return err
	}
	return s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("UPDATE peers SET group_id = 0 WHERE group_id = ? AND org_id = ?", id, orgID).Error; err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM device_groups WHERE id = ?", id).Error; err != nil {
			return err
		}
		s.Audit(c, orgID, "group.delete", "group", id, nil)
		return nil
	})
}

// DeviceGroupAddDevices move dispositivos da org para o grupo.
func (s *Svc) DeviceGroupAddDevices(c Ctx, orgID, id uint, deviceIDs []uint) (int64, error) {
	if _, err := s.groupOf(c, orgID, id, perm.GroupManage); err != nil {
		return 0, err
	}
	if len(deviceIDs) == 0 {
		return 0, ErrValidation("device_ids obrigatório", map[string]string{"field": "device_ids"})
	}
	res := s.DB.Exec("UPDATE peers SET group_id = ? WHERE org_id = ? AND row_id IN ?", id, orgID, deviceIDs)
	if res.Error != nil {
		return 0, res.Error
	}
	s.Audit(c, orgID, "group.add_devices", "group", id, map[string]interface{}{"device_ids": deviceIDs, "updated": res.RowsAffected})
	return res.RowsAffected, nil
}
