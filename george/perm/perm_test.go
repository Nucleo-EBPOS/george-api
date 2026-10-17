package perm

import (
	"testing"

	gm "github.com/lejianwen/rustdesk-api/v2/george/model"
)

func TestCanRolesAndIsolation(t *testing.T) {
	dev := &Device{OrgID: 1, OwnerUserID: 10}
	cases := []struct {
		name   string
		actor  Actor
		action Action
		res    Resource
		want   bool
	}{
		{"owner gerencia device da org", Actor{UserID: 1, OrgID: 1, Role: gm.RoleOwner}, DeviceManage, Resource{OrgID: 1, Device: dev}, true},
		{"admin gerencia org", Actor{UserID: 2, OrgID: 1, Role: gm.RoleAdmin}, OrgManage, Resource{OrgID: 1}, true},
		{"tech não gerencia org", Actor{UserID: 3, OrgID: 1, Role: gm.RoleTech}, OrgManage, Resource{OrgID: 1}, false},
		{"tech conecta no próprio device", Actor{UserID: 10, OrgID: 1, Role: gm.RoleTech}, DeviceConnect, Resource{OrgID: 1, Device: dev}, true},
		{"tech não conecta em device alheio sem share", Actor{UserID: 11, OrgID: 1, Role: gm.RoleTech}, DeviceConnect, Resource{OrgID: 1, Device: dev}, false},
		{"viewer vê mas não conecta", Actor{UserID: 12, OrgID: 1, Role: gm.RoleViewer}, DeviceView, Resource{OrgID: 1, Device: dev}, true},
		{"viewer não conecta", Actor{UserID: 12, OrgID: 1, Role: gm.RoleViewer}, DeviceConnect, Resource{OrgID: 1, Device: dev}, false},
		{"outra org não enxerga", Actor{UserID: 1, OrgID: 2, Role: gm.RoleOwner}, DeviceView, Resource{OrgID: 1, Device: dev}, false},
		{"device de outra org via resource forjado", Actor{UserID: 1, OrgID: 2, Role: gm.RoleOwner}, DeviceView, Resource{OrgID: 2, Device: dev}, false},
		{"ops vê qualquer org", Actor{UserID: 99, IsOps: true}, OrgManage, Resource{OrgID: 7}, true},
		{"owner não é ops", Actor{UserID: 1, OrgID: 1, Role: gm.RoleOwner}, Ops, Resource{OrgID: 1}, false},
		{"papel desconhecido nega", Actor{UserID: 1, OrgID: 1, Role: "root"}, DeviceView, Resource{OrgID: 1}, false},
	}
	for _, c := range cases {
		if got := Can(c.actor, c.action, c.res); got != c.want {
			t.Errorf("%s: Can=%v, esperado %v", c.name, got, c.want)
		}
	}
}

func TestCanShares(t *testing.T) {
	devConnect := &Device{OrgID: 1, OwnerUserID: 10, Shares: []Share{{TargetType: gm.ShareTargetUser, TargetID: 11, Level: gm.ShareConnect}}}
	devManage := &Device{OrgID: 1, OwnerUserID: 10, Shares: []Share{{TargetType: gm.ShareTargetGroup, TargetID: 5, Level: gm.ShareManage}}}
	tech := Actor{UserID: 11, OrgID: 1, Role: gm.RoleTech, GroupID: 5}

	if !Can(tech, DeviceConnect, Resource{OrgID: 1, Device: devConnect}) {
		t.Error("share connect deveria permitir conectar")
	}
	if Can(tech, DeviceManage, Resource{OrgID: 1, Device: devConnect}) {
		t.Error("share connect não deveria permitir gerenciar")
	}
	if !Can(tech, DeviceManage, Resource{OrgID: 1, Device: devManage}) {
		t.Error("share manage via grupo deveria permitir gerenciar")
	}
	other := Actor{UserID: 12, OrgID: 1, Role: gm.RoleTech, GroupID: 6}
	if Can(other, DeviceView, Resource{OrgID: 1, Device: devManage}) {
		t.Error("tech sem share não deveria ver o device")
	}
}

func TestCanPlanLimits(t *testing.T) {
	owner := Actor{UserID: 1, OrgID: 1, Role: gm.RoleOwner}
	if Can(owner, DeviceAdd, Resource{OrgID: 1, Limits: &Limits{MaxDevices: 3, Devices: 3}}) {
		t.Error("limite de devices atingido deveria negar")
	}
	if !Can(owner, DeviceAdd, Resource{OrgID: 1, Limits: &Limits{MaxDevices: 3, Devices: 2}}) {
		t.Error("abaixo do limite deveria permitir")
	}
	if !Can(owner, DeviceAdd, Resource{OrgID: 1, Limits: &Limits{MaxDevices: 0, Devices: 1000}}) {
		t.Error("ilimitado deveria permitir")
	}
	if Can(owner, UserAdd, Resource{OrgID: 1, Limits: &Limits{MaxTechs: 1, Techs: 1}}) {
		t.Error("limite de técnicos atingido deveria negar")
	}
	if Can(Actor{UserID: 2, OrgID: 1, Role: gm.RoleViewer}, DeviceAdd, Resource{OrgID: 1}) {
		t.Error("viewer não registra device")
	}
}
