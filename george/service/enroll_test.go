package service

import (
	"regexp"
	"testing"
	"time"

	gm "github.com/lejianwen/rustdesk-api/v2/george/model"
	"github.com/lejianwen/rustdesk-api/v2/model"
)

func intp(i int) *int { return &i }

func newKey(t *testing.T, s *Svc, c Ctx, in EnrollmentKeyInput) *EnrollmentKeyView {
	t.Helper()
	k, err := s.EnrollmentKeyCreate(c, in)
	if err != nil {
		t.Fatalf("criar código: %v", err)
	}
	return k
}

func enroll(t *testing.T, s *Svc, code, rid string) *EnrollOut {
	t.Helper()
	out, err := s.Enroll(Ctx{IP: "10.0.0.9"}, EnrollInput{Code: code, RustdeskID: rid, Uuid: "uuid-" + rid, Hostname: "NOTE-" + rid,
		Platform: "windows", Os: "Windows 11 Pro", Version: "0.2.0", Username: "maria"})
	if err != nil {
		t.Fatalf("enroll %s: %v", rid, err)
	}
	return out
}

func agentHB(t *testing.T, s *Svc, token string, pwVersion int) (*AgentHeartbeatOut, error) {
	t.Helper()
	d, err := s.DeviceByToken(token)
	if err != nil {
		return nil, err
	}
	return s.AgentHeartbeat(Ctx{IP: "10.0.0.9"}, d, AgentHeartbeatInput{Version: "0.2.1", Unattended: boolp(true), PasswordVersion: pwVersion})
}

func TestEnrollmentKeysCRUDAndPerms(t *testing.T) {
	s := testDB(t)
	owner := signup(t, s, "owner@ek.test", "EK Org")
	tech := addUser(t, s, owner, "tech@ek.test", gm.RoleTech)
	viewer := addUser(t, s, owner, "viewer@ek.test", gm.RoleViewer)
	other := signup(t, s, "owner@ek2.test", "EK Org 2")
	g, err := s.DeviceGroupCreate(owner, owner.User.OrgID, "Financeiro")
	if err != nil {
		t.Fatal(err)
	}

	k := newKey(t, s, owner, EnrollmentKeyInput{Label: "Lote 1", GroupID: g.ID, MaxUses: 50, ExpiresInDays: intp(30)})
	if !regexp.MustCompile(`^[A-HJKMNP-Z2-9]{4}-[A-HJKMNP-Z2-9]{4}-[A-HJKMNP-Z2-9]{4}$`).MatchString(k.Code) {
		t.Fatalf("formato do código: %q", k.Code)
	}
	if k.URL != "https://georgedesk.test/e/"+k.Code || k.GroupName != "Financeiro" || k.Status != "active" || k.ExpiresAt == nil {
		t.Fatalf("visão do código: %+v", k)
	}
	if _, err := s.EnrollmentKeyCreate(tech, EnrollmentKeyInput{Label: "x"}); codeOf(err) != "forbidden" {
		t.Fatalf("tech não cria código (got %v)", err)
	}
	if _, err := s.EnrollmentKeyCreate(other, EnrollmentKeyInput{GroupID: g.ID}); codeOf(err) != "not_found" {
		t.Fatalf("grupo de outra org (got %v)", err)
	}
	if _, err := s.EnrollmentKeyCreate(owner, EnrollmentKeyInput{ExpiresInDays: intp(0)}); codeOf(err) != "validation_error" {
		t.Fatalf("expires_in_days 0 inválido (got %v)", err)
	}
	rows, meta, err := s.EnrollmentKeys(tech, Page{})
	if err != nil || meta.Total != 1 || rows[0].Code != k.Code {
		t.Fatalf("tech lê códigos: %v %+v", err, meta)
	}
	if _, _, err := s.EnrollmentKeys(viewer, Page{}); codeOf(err) != "forbidden" {
		t.Fatalf("viewer não lê códigos (got %v)", err)
	}
	if rows, _, _ := s.EnrollmentKeys(other, Page{}); len(rows) != 0 {
		t.Fatal("códigos vazaram entre orgs")
	}
	if err := s.EnrollmentKeyRevoke(tech, k.ID); codeOf(err) != "forbidden" {
		t.Fatalf("tech não revoga (got %v)", err)
	}
	if err := s.EnrollmentKeyRevoke(other, k.ID); codeOf(err) != "not_found" {
		t.Fatalf("outra org não revoga (got %v)", err)
	}
	if err := s.EnrollmentKeyRevoke(owner, k.ID); err != nil {
		t.Fatal(err)
	}
	rows, _, _ = s.EnrollmentKeys(owner, Page{})
	if rows[0].Status != "revoked" || rows[0].RevokedAt == nil {
		t.Fatalf("código deveria estar revogado: %+v", rows[0])
	}
	for _, a := range []string{"enrollment_key.create", "enrollment_key.revoke"} {
		if logs, _, _ := s.AuditList(owner, owner.User.OrgID, a, 0, Page{}); len(logs) != 1 {
			t.Fatalf("auditoria %s ausente", a)
		}
	}
}

func TestEnrollInvalidKeys(t *testing.T) {
	s := testDB(t)
	owner := signup(t, s, "owner@inv.test", "Inv Org")
	revoked := newKey(t, s, owner, EnrollmentKeyInput{})
	if err := s.EnrollmentKeyRevoke(owner, revoked.ID); err != nil {
		t.Fatal(err)
	}
	expired := newKey(t, s, owner, EnrollmentKeyInput{ExpiresInDays: intp(1)})
	s.DB.Exec("UPDATE enrollment_keys SET expires_at = ? WHERE id = ?", time.Now().Add(-time.Minute), expired.ID)
	single := newKey(t, s, owner, EnrollmentKeyInput{MaxUses: 1})
	enroll(t, s, single.Code, "100000001")

	for name, code := range map[string]string{"inexistente": "AAAA-BBBB-CCCC", "malformado": "abc", "revogado": revoked.Code,
		"expirado": expired.Code, "esgotado": single.Code} {
		_, err := s.Enroll(Ctx{IP: "1.1.1.1"}, EnrollInput{Code: code, RustdeskID: "100000002", Uuid: "uuid-100000002"})
		e := AsError(err)
		if e.Status != 404 || e.Code != "enrollment_key_invalid" || e.Message != "código de vínculo inválido" {
			t.Errorf("%s: esperava 404 enrollment_key_invalid uniforme, got %+v", name, e)
		}
	}
	// código aceito sem hífens / minúsculo
	unlimited := newKey(t, s, owner, EnrollmentKeyInput{})
	raw := unlimited.Code[0:4] + unlimited.Code[5:9] + unlimited.Code[10:14]
	if _, err := s.Enroll(Ctx{}, EnrollInput{Code: " " + raw + " ", RustdeskID: "100000003", Uuid: "uuid-100000003"}); err != nil {
		t.Fatalf("código sem hífens deveria valer: %v", err)
	}
	// senha da instalação validada
	if _, err := s.Enroll(Ctx{}, EnrollInput{Code: unlimited.Code, RustdeskID: "100000004", Uuid: "u4", Password: "curta"}); codeOf(err) != "validation_error" {
		t.Fatalf("senha curta (got %v)", err)
	}
	var uses int
	s.DB.Raw("SELECT uses FROM enrollment_keys WHERE id = ?", unlimited.ID).Scan(&uses)
	if uses != 1 {
		t.Fatalf("falhas não devem consumir usos (uses=%d)", uses)
	}
}

func TestEnrollOrgIsolationAndReenroll(t *testing.T) {
	s := testDB(t)
	a := signup(t, s, "owner@ea.test", "Org A")
	b := signup(t, s, "owner@eb.test", "Org B")
	techA := addUser(t, s, a, "tech@ea.test", gm.RoleTech)
	gA, _ := s.DeviceGroupCreate(a, a.User.OrgID, "Financeiro")
	kA := newKey(t, s, a, EnrollmentKeyInput{GroupID: gA.ID})
	kB := newKey(t, s, b, EnrollmentKeyInput{})

	first, err := s.Enroll(Ctx{IP: "10.0.0.9"}, EnrollInput{Code: kA.Code, RustdeskID: "200000001", Uuid: "uuid-200000001", Hostname: "NOTE-EB101",
		Platform: "windows", Os: "Windows 11", Version: "0.2.0", Password: "senha-da-gpo-1"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Org.ID != a.User.OrgID || first.Org.Name != "Org A" || first.Device.Name != "NOTE-EB101" || first.Device.GroupName != "Financeiro" ||
		first.Password == nil || first.Password.Version != 1 || first.Password.Value != "senha-da-gpo-1" || first.HeartbeatInterval != 60 || len(first.DeviceToken) < 40 {
		t.Fatalf("resposta do vínculo: %+v", first)
	}
	// outra org → 409
	if _, err := s.Enroll(Ctx{}, EnrollInput{Code: kB.Code, RustdeskID: "200000001", Uuid: "uuid-200000001"}); codeOf(err) != "device_other_org" {
		t.Fatalf("esperava device_other_org, got %v", err)
	}
	// org B não enxerga
	if rows, _, _ := s.Devices(b, DeviceFilter{}, Page{}); len(rows) != 0 {
		t.Fatal("device da org A vazou para B")
	}
	if _, err := s.DeviceCredentialsGet(b, first.Device.ID); codeOf(err) != "not_found" {
		t.Fatalf("org B não lê credenciais (got %v)", err)
	}
	if _, err := s.DeviceRotatePassword(b, first.Device.ID); codeOf(err) != "not_found" {
		t.Fatalf("org B não rotaciona (got %v)", err)
	}
	// renomeia/move e re-vincula: mantém nome, setor e senha; token novo
	gA2, _ := s.DeviceGroupCreate(a, a.User.OrgID, "RH")
	if _, err := s.DeviceUpdate(a, first.Device.ID, DeviceUpdateInput{Name: strp("Maria - Financeiro"), GroupID: &gA2.ID}); err != nil {
		t.Fatal(err)
	}
	again, err := s.Enroll(Ctx{}, EnrollInput{Code: kA.Code, RustdeskID: "200000009", Uuid: "uuid-200000001", Hostname: "NOTE-EB101"})
	if err != nil {
		t.Fatal(err)
	}
	if again.Device.ID != first.Device.ID || again.Device.Name != "Maria - Financeiro" || again.Device.GroupName != "RH" ||
		again.Password.Value != "senha-da-gpo-1" || again.Password.Version != 1 || again.DeviceToken == first.DeviceToken {
		t.Fatalf("re-vínculo: %+v", again)
	}
	if _, err := s.DeviceByToken(first.DeviceToken); codeOf(err) != "device_token_invalid" {
		t.Fatalf("token antigo deveria morrer (got %v)", err)
	}
	if _, err := agentHB(t, s, again.DeviceToken, 1); err != nil {
		t.Fatal(err)
	}
	// tech da org vê e filtra por enrolled
	rows, meta, err := s.Devices(techA, DeviceFilter{Enrolled: boolp(true)}, Page{})
	if err != nil || meta.Total != 1 {
		t.Fatalf("tech deveria ver o PC da org: %v %+v", err, meta)
	}
	d := rows[0]
	if !d.Enrolled || d.Revoked || !d.Unattended || d.UserID != 0 || d.RustdeskID() != "200000009" || d.GroupName != "RH" ||
		!d.Online || d.LastAgentAt == 0 || d.EnrolledAt == nil || !d.PasswordApplied || d.PwVersion != 1 {
		t.Fatalf("campos do device: %+v", d)
	}
	if rows, _, _ := s.Devices(a, DeviceFilter{Enrolled: boolp(false)}, Page{}); len(rows) != 0 {
		t.Fatal("filtro enrolled=false")
	}
	var uses int
	s.DB.Raw("SELECT uses FROM enrollment_keys WHERE id = ?", kA.ID).Scan(&uses)
	if uses != 2 {
		t.Fatalf("uses=%d, esperado 2", uses)
	}
	for _, act := range []string{"device.enroll", "device.rename", "device.move"} {
		if logs, _, _ := s.AuditList(a, a.User.OrgID, act, 0, Page{}); len(logs) == 0 {
			t.Fatalf("auditoria %s ausente", act)
		}
	}
}

func TestPasswordRotationFlow(t *testing.T) {
	s := testDB(t)
	owner := signup(t, s, "owner@rot.test", "Rot Org")
	k := newKey(t, s, owner, EnrollmentKeyInput{})
	en := enroll(t, s, k.Code, "300000001")
	if en.Password.Version != 1 || len(en.Password.Value) != 14 {
		t.Fatalf("senha gerada: %+v", en.Password)
	}
	// PC ainda não aplicou → password volta
	hb, err := agentHB(t, s, en.DeviceToken, 0)
	if err != nil || hb.Password == nil || hb.Password.Value != en.Password.Value || hb.OrgName != "Rot Org" || hb.Name != "NOTE-300000001" {
		t.Fatalf("heartbeat v0: %v %+v", err, hb)
	}
	hb, _ = agentHB(t, s, en.DeviceToken, 1)
	if hb.Password != nil {
		t.Fatal("sem rotação, password deveria ser null")
	}
	v, err := s.DeviceRotatePassword(owner, en.Device.ID)
	if err != nil || v != 2 {
		t.Fatalf("rotate: %v %d", err, v)
	}
	dv, _ := s.Device(owner, en.Device.ID)
	if dv.PasswordApplied || dv.PwVersion != 2 {
		t.Fatalf("após rotacionar, password_applied=false (got %+v)", dv)
	}
	hb, _ = agentHB(t, s, en.DeviceToken, 1)
	if hb.Password == nil || hb.Password.Version != 2 || hb.Password.Value == en.Password.Value {
		t.Fatalf("heartbeat deveria entregar v2: %+v", hb.Password)
	}
	newPw := hb.Password.Value
	hb, _ = agentHB(t, s, en.DeviceToken, 2)
	if hb.Password != nil {
		t.Fatal("v2 aplicada: password null")
	}
	dv, _ = s.Device(owner, en.Device.ID)
	if !dv.PasswordApplied {
		t.Fatal("v2 deveria constar como aplicada")
	}
	cr, err := s.DeviceCredentialsGet(owner, en.Device.ID)
	if err != nil || cr.Password != newPw || cr.PasswordVersion != 2 || !cr.PasswordApplied || cr.RustdeskID != "300000001" {
		t.Fatalf("credenciais: %v %+v", err, cr)
	}
	// versão reportada maior que a da API é ignorada
	if _, err := agentHB(t, s, en.DeviceToken, 99); err != nil {
		t.Fatal(err)
	}
	var applied int
	s.DB.Raw("SELECT pw_applied_version FROM peers WHERE row_id = ?", en.Device.ID).Scan(&applied)
	if applied != 2 {
		t.Fatalf("pw_applied_version=%d", applied)
	}
	if logs, _, _ := s.AuditList(owner, owner.User.OrgID, "device.rotate_password", 0, Page{}); len(logs) != 1 {
		t.Fatal("auditoria device.rotate_password")
	}
}

func TestRevokeAndCredentialsPerms(t *testing.T) {
	s := testDB(t)
	owner := signup(t, s, "owner@rv.test", "Rv Org")
	admin := addUser(t, s, owner, "admin@rv.test", gm.RoleAdmin)
	tech := addUser(t, s, owner, "tech@rv.test", gm.RoleTech)
	viewer := addUser(t, s, owner, "viewer@rv.test", gm.RoleViewer)
	k := newKey(t, s, owner, EnrollmentKeyInput{})
	en := enroll(t, s, k.Code, "400000001")
	id := en.Device.ID

	// credenciais: tech (connect em PC da org) sim; viewer não
	if _, err := s.DeviceCredentialsGet(tech, id); err != nil {
		t.Fatalf("tech deveria ler credenciais: %v", err)
	}
	if _, err := s.DeviceCredentialsGet(viewer, id); codeOf(err) != "forbidden" {
		t.Fatalf("viewer não lê credenciais (got %v)", err)
	}
	if _, err := s.DeviceCredentialsGet(admin, id); err != nil {
		t.Fatal(err)
	}
	if logs, _, _ := s.AuditList(owner, owner.User.OrgID, "device.credentials.read", 0, Page{}); len(logs) != 2 {
		t.Fatalf("auditoria credentials.read: %d", len(logs))
	}
	// tech não administra
	if _, err := s.DeviceRotatePassword(tech, id); codeOf(err) != "forbidden" {
		t.Fatalf("tech não rotaciona (got %v)", err)
	}
	if err := s.DeviceRevoke(tech, id); codeOf(err) != "forbidden" {
		t.Fatalf("tech não revoga (got %v)", err)
	}
	if _, err := s.DeviceUpdate(tech, id, DeviceUpdateInput{Name: strp("x")}); codeOf(err) != "forbidden" {
		t.Fatalf("tech não renomeia PC da org (got %v)", err)
	}
	// device não vinculado: credenciais → 409
	plain := heartbeat(t, s, owner, "400000099")
	if _, err := s.DeviceCredentialsGet(owner, plain.Device.RowID); codeOf(err) != "device_not_enrolled" {
		t.Fatalf("device sem vínculo (got %v)", err)
	}
	// revoga
	if err := s.DeviceRevoke(admin, id); err != nil {
		t.Fatal(err)
	}
	if _, err := agentHB(t, s, en.DeviceToken, 1); codeOf(err) != "device_revoked" || AsError(err).Status != 410 {
		t.Fatalf("agente deveria receber 410 device_revoked (got %v)", err)
	}
	if _, err := s.DeviceCredentialsGet(owner, id); codeOf(err) != "device_revoked" {
		t.Fatalf("credenciais de revogado (got %v)", err)
	}
	if _, err := s.DeviceRotatePassword(owner, id); codeOf(err) != "device_revoked" {
		t.Fatalf("rotate de revogado (got %v)", err)
	}
	var pwEnc *string
	s.DB.Raw("SELECT pw_enc FROM peers WHERE row_id = ?", id).Scan(&pwEnc)
	if pwEnc != nil {
		t.Fatal("senha cifrada deveria ser apagada")
	}
	if rows, _, _ := s.Devices(tech, DeviceFilter{Enrolled: boolp(true)}, Page{}); len(rows) != 0 {
		t.Fatal("tech não deveria ver PC revogado")
	}
	dv, _ := s.Device(owner, id)
	if !dv.Revoked || dv.Unattended {
		t.Fatalf("admin vê revogado: %+v", dv)
	}
	// novo código re-vincula com senha nova
	again := enroll(t, s, k.Code, "400000001")
	if again.Device.ID != id || again.Password.Version != 2 || again.Password.Value == en.Password.Value {
		t.Fatalf("re-vínculo pós-revogação: %+v", again)
	}
	if _, err := agentHB(t, s, again.DeviceToken, 2); err != nil {
		t.Fatal(err)
	}
	if logs, _, _ := s.AuditList(owner, owner.User.OrgID, "device.revoke", 0, Page{}); len(logs) != 1 {
		t.Fatal("auditoria device.revoke")
	}
}

func TestEnrollPlanLimit(t *testing.T) {
	s := testDB(t)
	owner := signup(t, s, "owner@pl.test", "Plan Org")
	k := newKey(t, s, owner, EnrollmentKeyInput{})
	enroll(t, s, k.Code, "500000001")
	enroll(t, s, k.Code, "500000002")
	enroll(t, s, k.Code, "500000003")
	if _, err := s.Enroll(Ctx{}, EnrollInput{Code: k.Code, RustdeskID: "500000004", Uuid: "uuid-500000004"}); codeOf(err) != "plan_limit_reached" {
		t.Fatalf("4º PC no free deveria bater no limite (got %v)", err)
	}
	// re-vínculo de PC existente continua permitido
	enroll(t, s, k.Code, "500000002")
}

// O /api/sysinfo e /api/heartbeat do upstream atualizam peers com model.Peer (Updates
// só com campos não-zero): não podem apagar org/vínculo nem dar dono ao PC da org.
func TestUpstreamUpdatesKeepEnrollment(t *testing.T) {
	s := testDB(t)
	owner := signup(t, s, "owner@up.test", "Up Org")
	k := newKey(t, s, owner, EnrollmentKeyInput{})
	en := enroll(t, s, k.Code, "600000001")
	s.DB.Exec("UPDATE peers SET last_online_time = 1 WHERE row_id = ?", en.Device.ID)

	now := time.Now().Unix()
	if err := s.DB.Model(&model.Peer{RowId: en.Device.ID}).Updates(&model.Peer{RowId: en.Device.ID, Hostname: "NOTE-NEW",
		Os: "Windows 11", UserId: owner.User.ID, LastOnlineTime: now, LastOnlineIp: "10.0.0.10"}).Error; err != nil {
		t.Fatal(err)
	}
	d := &gm.Device{}
	s.DB.Where("row_id = ?", en.Device.ID).First(d)
	if d.OrgID != owner.User.OrgID || d.EnrolledAt == nil || d.DeviceTokenHash == nil || !d.HasPassword() || d.PwVersion != 1 {
		t.Fatalf("upstream apagou o vínculo: %+v", d)
	}
	if d.UserID != 0 {
		t.Fatalf("PC vinculado deve continuar sem dono (user_id=%d)", d.UserID)
	}
	if d.LastOnlineTime != now || d.Hostname != "NOTE-NEW" {
		t.Fatal("upstream deveria atualizar last_online_time/hostname")
	}
	if _, err := s.DeviceByToken(en.DeviceToken); err != nil {
		t.Fatalf("token deveria continuar válido: %v", err)
	}
}
