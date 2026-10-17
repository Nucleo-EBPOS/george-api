package service

import (
	"errors"
	"testing"
	"time"

	gm "github.com/lejianwen/rustdesk-api/v2/george/model"
	"github.com/lejianwen/rustdesk-api/v2/model"
)

func codeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestOrgIsolation(t *testing.T) {
	s := testDB(t)
	a := signup(t, s, "owner@a.test", "Org A")
	b := signup(t, s, "owner@b.test", "Org B")
	if a.User.OrgID == b.User.OrgID {
		t.Fatal("orgs deveriam ser distintas")
	}
	devA := heartbeat(t, s, a, "111111111")
	heartbeat(t, s, b, "222222222")

	rowsA, meta, err := s.Devices(a, DeviceFilter{}, Page{})
	if err != nil || meta.Total != 1 || rowsA[0].RustdeskID() != "111111111" {
		t.Fatalf("org A deveria ver só o próprio device: %v %+v", err, meta)
	}
	rowsB, _, _ := s.Devices(b, DeviceFilter{}, Page{})
	if len(rowsB) != 1 || rowsB[0].OrgID != b.User.OrgID {
		t.Fatal("org B deveria ver só o próprio device")
	}
	if _, err := s.Device(b, devA.Device.RowID); codeOf(err) != "not_found" {
		t.Fatalf("org B não pode ver device da org A (got %v)", err)
	}
	if _, err := s.DeviceUpdate(b, devA.Device.RowID, DeviceUpdateInput{HostEnabled: boolp(false)}); codeOf(err) != "not_found" {
		t.Fatalf("org B não pode alterar device da org A (got %v)", err)
	}
	// heartbeat de usuário da org B num device da org A → conflito
	if _, err := s.Heartbeat(b, HeartbeatInput{ID: "111111111", Uuid: "uuid-111111111"}); codeOf(err) != "device_other_org" {
		t.Fatalf("esperava device_other_org, got %v", err)
	}
	// usuários e auditoria
	usersA, _, _ := s.OrgUsers(a, a.User.OrgID, Page{})
	if len(usersA) != 1 || usersA[0].Email != "owner@a.test" {
		t.Fatal("org A deveria listar só seu owner")
	}
	if _, _, err := s.OrgUsers(a, b.User.OrgID, Page{}); codeOf(err) != "forbidden" {
		t.Fatal("org A não pode listar usuários da org B")
	}
	logsB, _, _ := s.AuditList(b, b.User.OrgID, "", 0, Page{})
	for _, l := range logsB {
		if l.OrgID != b.User.OrgID {
			t.Fatal("auditoria vazou entre orgs")
		}
	}
	// grupos upstream isolados por org (usado por /api/users e /api/peers)
	var gA, gB uint
	s.DB.Raw("SELECT group_id FROM users WHERE id = ?", a.User.ID).Scan(&gA)
	s.DB.Raw("SELECT group_id FROM users WHERE id = ?", b.User.ID).Scan(&gB)
	if gA == 0 || gA == gB {
		t.Fatal("cada org deveria ter seu grupo upstream")
	}
}

func (d DeviceView) RustdeskID() string { return d.ID }

func boolp(b bool) *bool { return &b }

func TestHostToggle(t *testing.T) {
	s := testDB(t)
	owner := signup(t, s, "owner@h.test", "Host Org")
	tech := addUser(t, s, owner, "tech@h.test", gm.RoleTech)
	viewer := addUser(t, s, owner, "viewer@h.test", gm.RoleViewer)

	hb := heartbeat(t, s, tech, "333333333")
	if !hb.HostEnabled {
		t.Fatal("host deveria nascer ligado")
	}
	id := hb.Device.RowID
	// dono desliga via API
	d, err := s.DeviceUpdate(tech, id, DeviceUpdateInput{HostEnabled: boolp(false)})
	if err != nil || d.HostEnabled {
		t.Fatalf("tech dono deveria desligar host: %v", err)
	}
	// heartbeat seguinte reflete OFF e não religa sozinho
	hb2 := heartbeat(t, s, tech, "333333333")
	if hb2.HostEnabled {
		t.Fatal("heartbeat não deveria religar host")
	}
	// cliente liga via heartbeat
	out, err := s.Heartbeat(tech, HeartbeatInput{ID: "333333333", Uuid: "uuid-333333333", HostEnabled: boolp(true)})
	if err != nil || !out.HostEnabled {
		t.Fatalf("cliente deveria ligar host pelo heartbeat: %v", err)
	}
	// viewer não gerencia
	if _, err := s.DeviceUpdate(viewer, id, DeviceUpdateInput{HostEnabled: boolp(false)}); codeOf(err) != "forbidden" {
		t.Fatalf("viewer não pode alternar host (got %v)", err)
	}
	// outro tech sem share: nem vê
	other := addUser(t, s, owner, "other@h.test", gm.RoleTech)
	if _, err := s.Device(other, id); codeOf(err) != "forbidden" {
		t.Fatalf("tech sem share não vê device (got %v)", err)
	}
	// share connect → vê e conecta, mas não gerencia
	if _, err := s.DeviceShare(owner, id, ShareInput{TargetType: gm.ShareTargetUser, TargetID: other.User.ID, Level: gm.ShareConnect}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Device(other, id); err != nil {
		t.Fatalf("com share deveria ver: %v", err)
	}
	if _, err := s.DeviceUpdate(other, id, DeviceUpdateInput{HostEnabled: boolp(false)}); codeOf(err) != "forbidden" {
		t.Fatalf("share connect não gerencia (got %v)", err)
	}
	// admin gerencia tudo
	if _, err := s.DeviceUpdate(owner, id, DeviceUpdateInput{HostEnabled: boolp(false), Name: strp("PC da recepção")}); err != nil {
		t.Fatal(err)
	}
	logs, _, _ := s.AuditList(owner, owner.User.OrgID, "device.host", 0, Page{})
	if len(logs) < 3 {
		t.Fatalf("esperava auditoria de host on/off, got %d", len(logs))
	}
	// limite do plano free (3 devices)
	heartbeat(t, s, owner, "444444444")
	heartbeat(t, s, owner, "555555555")
	if _, err := s.Heartbeat(owner, HeartbeatInput{ID: "666666666", Uuid: "uuid-6"}); codeOf(err) != "plan_limit_reached" {
		t.Fatalf("4º device no free deveria bater no limite (got %v)", err)
	}
}

func strp(s string) *string { return &s }

func TestInvites(t *testing.T) {
	s := testDB(t)
	owner := signup(t, s, "owner@i.test", "Invite Org")
	dev := heartbeat(t, s, owner, "777777777")
	servers := [3]string{"id:21116", "relay:21117", "key"}

	inv, err := s.InviteCreate(owner, InviteInput{DeviceID: dev.Device.RowID})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Code) != 6 || inv.Status != "active" || inv.MaxUses != 1 {
		t.Fatalf("convite inesperado: %+v", inv)
	}
	// público não vaza senha
	pub, err := s.InvitePublic(inv.Code)
	if err != nil || pub.RustdeskID != "777777777" || pub.Status != "active" {
		t.Fatalf("público: %v %+v", err, pub)
	}
	// host recebe a senha pendente no heartbeat
	hb := heartbeat(t, s, owner, "777777777")
	if len(hb.PendingInvites) != 1 || hb.PendingInvites[0].Code != inv.Code || hb.PendingInvites[0].SessionPassword == "" {
		t.Fatalf("host deveria receber convite pendente: %+v", hb.PendingInvites)
	}
	// consumo único
	guest := Ctx{IP: "203.0.113.9"}
	out, err := s.InviteConsume(guest, inv.Code, servers)
	if err != nil || out.SessionPassword != hb.PendingInvites[0].SessionPassword || out.RustdeskID != "777777777" {
		t.Fatalf("consumo: %v %+v", err, out)
	}
	if _, err := s.InviteConsume(guest, inv.Code, servers); codeOf(err) != "invite_consumed" {
		t.Fatalf("segundo consumo deveria falhar com invite_consumed (got %v)", err)
	}
	// após consumir, a senha some do banco e do heartbeat
	var enc string
	s.DB.Raw("SELECT session_password_enc FROM invites WHERE code = ?", inv.Code).Scan(&enc)
	if enc != "" {
		t.Fatal("senha de sessão deveria ser apagada após uso único")
	}
	if hb := heartbeat(t, s, owner, "777777777"); len(hb.PendingInvites) != 0 {
		t.Fatal("convite consumido não deveria ser pendente")
	}
	// expiração
	exp, _ := s.InviteCreate(owner, InviteInput{DeviceID: dev.Device.RowID, ExpiresIn: 60})
	s.DB.Exec("UPDATE invites SET expires_at = ? WHERE id = ?", time.Now().Add(-time.Minute), exp.ID)
	if _, err := s.InviteConsume(guest, exp.Code, servers); codeOf(err) != "invite_expired" {
		t.Fatalf("esperava invite_expired, got %v", err)
	}
	// revogação
	rev, _ := s.InviteCreate(owner, InviteInput{})
	if err := s.InviteRevoke(owner, rev.Code); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InviteConsume(guest, rev.Code, servers); codeOf(err) != "invite_revoked" {
		t.Fatalf("esperava invite_revoked, got %v", err)
	}
	// código inexistente / case-insensitive
	if _, err := s.InviteConsume(guest, "zzzzzz", servers); codeOf(err) != "not_found" {
		t.Fatalf("esperava not_found, got %v", err)
	}
	// multiuso
	multi, _ := s.InviteCreate(owner, InviteInput{MaxUses: 2})
	if _, err := s.InviteConsume(guest, multi.Code, servers); err != nil {
		t.Fatal(err)
	}
	if o, err := s.InviteConsume(guest, multi.Code, servers); err != nil || o.RemainingUses != 0 {
		t.Fatalf("segundo uso do multiuso deveria passar: %v", err)
	}
	if _, err := s.InviteConsume(guest, multi.Code, servers); codeOf(err) != "invite_consumed" {
		t.Fatal("terceiro uso deveria falhar")
	}
	// tech não cria convite para device que não acessa
	tech := addUser(t, s, owner, "tech@i.test", gm.RoleTech)
	if _, err := s.InviteCreate(tech, InviteInput{DeviceID: dev.Device.RowID}); codeOf(err) != "forbidden" {
		t.Fatalf("tech sem acesso não cria convite (got %v)", err)
	}
	// viewer não cria
	viewer := addUser(t, s, owner, "viewer@i.test", gm.RoleViewer)
	if _, err := s.InviteCreate(viewer, InviteInput{}); codeOf(err) != "forbidden" {
		t.Fatal("viewer não cria convite")
	}
	// auditoria registrou tudo
	logs, _, _ := s.AuditList(owner, owner.User.OrgID, "invite.", 0, Page{PerPage: 100})
	if len(logs) < 8 {
		t.Fatalf("auditoria de convites incompleta: %d", len(logs))
	}
}

func TestAuthTokens(t *testing.T) {
	s := testDB(t)
	c := Ctx{IP: "127.0.0.1"}
	signup(t, s, "owner@t.test", "Tok Org")
	if _, _, err := s.Login(c, "owner@t.test", "errada", ""); codeOf(err) != "invalid_credentials" {
		t.Fatal("senha errada deveria falhar")
	}
	tk, u, err := s.Login(c, "OWNER@t.test", "senha-forte-123", "")
	if err != nil || tk.AccessToken == "" || tk.RefreshToken == "" {
		t.Fatalf("login: %v", err)
	}
	claims, err := s.Signer.Parse(tk.AccessToken)
	if err != nil || claims.UserID != u.ID || claims.OrgID != u.OrgID || claims.Role != gm.RoleOwner {
		t.Fatalf("claims: %v %+v", err, claims)
	}
	// rotação
	tk2, _, err := s.Refresh(c, tk.RefreshToken)
	if err != nil || tk2.RefreshToken == tk.RefreshToken {
		t.Fatalf("refresh deveria rotacionar: %v", err)
	}
	// reuso do antigo derruba a família
	if _, _, err := s.Refresh(c, tk.RefreshToken); codeOf(err) != "refresh_reused" {
		t.Fatalf("reuso deveria ser detectado (got %v)", err)
	}
	if _, _, err := s.Refresh(c, tk2.RefreshToken); codeOf(err) == "" {
		t.Fatal("família inteira deveria estar revogada")
	}
	// senha ficou em argon2id
	var hash string
	s.DB.Raw("SELECT password FROM users WHERE id = ?", u.ID).Scan(&hash)
	if len(hash) < 10 || hash[:10] != "$argon2id$" {
		t.Fatalf("senha deveria ser argon2id: %s", hash[:10])
	}
	// 2FA
	uc := Ctx{User: u}
	secret, _, err := s.TotpSetup(uc)
	if err != nil || secret == "" {
		t.Fatal(err)
	}
	if err := s.TotpVerify(uc, "000000"); codeOf(err) != "mfa_invalid" {
		t.Fatal("código inválido deveria falhar")
	}
}

func TestSessionIngestionHook(t *testing.T) {
	s := testDB(t)
	owner := signup(t, s, "owner@s.test", "Sess Org")
	dev := heartbeat(t, s, owner, "888888888")
	// simula o que o upstream faz em POST /api/audit/conn
	ac := &model.AuditConn{Action: model.AuditActionNew, ConnId: 42, PeerId: "888888888", FromPeer: "999999999", FromName: "tecnico", Ip: "10.0.0.5", Type: 1}
	if err := s.DB.Create(ac).Error; err != nil {
		t.Fatal(err)
	}
	rows, meta, err := s.Sessions(owner, 0, Page{})
	if err != nil || meta.Total != 1 || rows[0].DeviceID != dev.Device.RowID || rows[0].OrgID != owner.User.OrgID || rows[0].EndedAt != nil {
		t.Fatalf("sessão não ingerida: %v %+v", err, rows)
	}
	ac.CloseTime = time.Now().Unix()
	if err := s.DB.Model(ac).Updates(ac).Error; err != nil {
		t.Fatal(err)
	}
	sess, _ := s.Session(owner, rows[0].ID)
	if sess.EndedAt == nil {
		t.Fatal("sessão deveria estar encerrada")
	}
	// resumo: enfileira (idempotente) e o worker processa sem IA (template)
	job, _, err := s.SessionSummaryRequest(owner, sess.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	job2, _, _ := s.SessionSummaryRequest(owner, sess.ID, "")
	if job2.ID != job.ID {
		t.Fatal("pedido de resumo deveria ser idempotente")
	}
	if !s.RunOne(contextBG()) {
		t.Fatal("worker deveria ter processado um job")
	}
	sess, _ = s.Session(owner, sess.ID)
	if sess.SummaryStatus != "done" || sess.Summary == "" {
		t.Fatalf("resumo não gerado: %+v", sess)
	}
	// outra org não vê a sessão
	other := signup(t, s, "owner@s2.test", "Sess Org 2")
	if _, err := s.Session(other, sess.ID); codeOf(err) != "not_found" {
		t.Fatal("sessão vazou entre orgs")
	}
}
