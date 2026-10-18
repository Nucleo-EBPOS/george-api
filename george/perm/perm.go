// Package perm é a checagem central de permissões: Can(actor, action, resource).
// É puro (sem banco) para ser testável; o service carrega os dados e chama aqui.
package perm

import gm "github.com/lejianwen/rustdesk-api/v2/george/model"

// Action é uma ação sobre um recurso.
type Action string

// Ações conhecidas.
const (
	DeviceView       Action = "device.view"
	DeviceConnect    Action = "device.connect"
	DeviceManage     Action = "device.manage"         // renomear, host on/off, apagar, compartilhar
	DeviceAdd        Action = "device.add"            // registrar novo dispositivo na org (limite do plano)
	DeviceAdminister Action = "device.administer"     // rotacionar senha / revogar vínculo (owner/admin)
	EnrollKeyView    Action = "enrollment_key.view"   // listar códigos de vínculo (tech lê)
	EnrollKeyManage  Action = "enrollment_key.manage" // criar/revogar códigos de vínculo (owner/admin)
	InviteCreate     Action = "invite.create"
	InviteManage     Action = "invite.manage" // listar/revogar convites de outros
	GroupManage      Action = "group.manage"
	SessionView      Action = "session.view"
	SessionViewAll   Action = "session.view_all"
	OrgManage        Action = "org.manage" // org, usuários, branding, sso
	OrgBilling       Action = "org.billing"
	AuditView        Action = "audit.view"
	UserAdd          Action = "user.add" // limite de técnicos do plano
	Ops              Action = "ops"
)

// Actor é quem age.
type Actor struct {
	UserID  uint
	OrgID   uint
	Role    string
	IsOps   bool
	GroupID uint // grupo de usuários (upstream groups.id) — alvo de compartilhamento
}

// Share é um compartilhamento já carregado do banco.
type Share struct {
	TargetType string
	TargetID   uint
	Level      string
}

// Device é o recurso dispositivo (campos relevantes à decisão).
type Device struct {
	OrgID       uint
	OwnerUserID uint
	Shares      []Share
	// OrgOwned: vinculado por enrollment (user_id = 0, é da org) e não revogado.
	// Técnicos da org veem e conectam; gerenciar continua owner/admin (ou share manage).
	OrgOwned bool
}

// Limits são os limites do plano + uso atual, usados em DeviceAdd/UserAdd.
type Limits struct {
	MaxDevices int // 0 = ilimitado
	MaxTechs   int
	Devices    int
	Techs      int
}

// Resource é um recurso genérico: OrgID obrigatório; Device/Limits opcionais.
type Resource struct {
	OrgID  uint
	Device *Device
	Limits *Limits
}

func roleRank(r string) int {
	switch r {
	case gm.RoleOwner:
		return 4
	case gm.RoleAdmin:
		return 3
	case gm.RoleTech:
		return 2
	case gm.RoleViewer:
		return 1
	}
	return 0
}

// IsAdmin diz se o papel administra a org.
func IsAdmin(role string) bool { return roleRank(role) >= 3 }

// shareLevel devolve o melhor nível de compartilhamento do ator sobre o device.
func shareLevel(a Actor, d *Device) string {
	best := ""
	for _, s := range d.Shares {
		match := (s.TargetType == gm.ShareTargetUser && s.TargetID == a.UserID) ||
			(s.TargetType == gm.ShareTargetGroup && a.GroupID != 0 && s.TargetID == a.GroupID)
		if !match {
			continue
		}
		if s.Level == gm.ShareManage {
			return gm.ShareManage
		}
		if s.Level == gm.ShareConnect {
			best = gm.ShareConnect
		}
	}
	return best
}

// Can decide. Regras:
//   - ops pode tudo em qualquer org;
//   - fora da própria org, nada;
//   - owner/admin: tudo na org (billing só owner/admin);
//   - tech: conecta/gerencia o que é seu ou foi compartilhado; vê e conecta (não gerencia)
//     nos dispositivos da org vinculados por enrollment; cria convites; lê códigos de vínculo;
//   - viewer: só leitura (lista de dispositivos e sessões).
//   - limites do plano valem para DeviceAdd/UserAdd (ops ignora).
func Can(a Actor, action Action, r Resource) bool {
	if a.IsOps {
		return true
	}
	if action == Ops {
		return false
	}
	if r.OrgID == 0 || a.OrgID != r.OrgID {
		return false
	}
	if r.Device != nil && r.Device.OrgID != r.OrgID {
		return false
	}
	rank := roleRank(a.Role)
	if rank == 0 {
		return false
	}
	switch action {
	case DeviceAdd:
		if rank < 2 {
			return false
		}
		return r.Limits == nil || r.Limits.MaxDevices == 0 || r.Limits.Devices < r.Limits.MaxDevices
	case UserAdd:
		if rank < 3 {
			return false
		}
		return r.Limits == nil || r.Limits.MaxTechs == 0 || r.Limits.Techs < r.Limits.MaxTechs
	case OrgManage, AuditView, GroupManage, InviteManage, SessionViewAll, EnrollKeyManage, DeviceAdminister:
		return rank >= 3
	case OrgBilling:
		return rank >= 3
	case InviteCreate, EnrollKeyView:
		return rank >= 2
	case SessionView:
		return rank >= 1
	case DeviceView:
		if rank >= 3 || rank == 1 {
			return true
		}
		return r.Device == nil || r.Device.OrgOwned || r.Device.OwnerUserID == a.UserID || shareLevel(a, r.Device) != ""
	case DeviceConnect:
		if rank >= 3 {
			return true
		}
		if rank < 2 || r.Device == nil {
			return false
		}
		return r.Device.OrgOwned || r.Device.OwnerUserID == a.UserID || shareLevel(a, r.Device) != ""
	case DeviceManage:
		if rank >= 3 {
			return true
		}
		if rank < 2 || r.Device == nil {
			return false
		}
		return r.Device.OwnerUserID == a.UserID || shareLevel(a, r.Device) == gm.ShareManage
	}
	return false
}
