package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/george/config"
	"github.com/lejianwen/rustdesk-api/v2/george/migrate"
	"github.com/lejianwen/rustdesk-api/v2/george/service"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Mesmo esquema dos testes de service (Postgres real; pulado sem banco). CI roda com -p 1.
func testServer(t *testing.T) (*gin.Engine, *service.Svc) {
	t.Helper()
	dsn := os.Getenv("GEORGE_TEST_DB_URL")
	if dsn == "" {
		dsn = "postgres://george:george@127.0.0.1:5433/george_test?sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), DisableForeignKeyConstraintWhenMigrating: true})
	if err != nil {
		t.Skipf("postgres de teste indisponível: %v", err)
	}
	if sqlDB, err := db.DB(); err != nil || sqlDB.Ping() != nil {
		t.Skip("postgres de teste indisponível (ping)")
	}
	if err := db.Exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Version{}, &model.User{}, &model.UserToken{}, &model.Tag{}, &model.AddressBook{}, &model.Peer{},
		&model.Group{}, &model.UserThird{}, &model.Oauth{}, &model.LoginLog{}, &model.ShareRecord{}, &model.AuditConn{}, &model.AuditFile{},
		&model.AddressBookCollection{}, &model.AddressBookCollectionRule{}, &model.ServerCmd{}, &model.DeviceGroup{}); err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Up(db); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{JWTSecret: "test-secret", AccessTTL: 15 * time.Minute, RefreshTTL: time.Hour, InviteTTL: time.Hour,
		PublicURL: "https://georgedesk.test", AIModel: "x"}
	config.Current = cfg
	log := logrus.New()
	log.SetLevel(logrus.FatalLevel)
	s := service.New(db, cfg, log)
	if err := s.RegisterHooks(db); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	g := gin.New()
	Register(g, log)
	return g, s
}

func call(g *gin.Engine, method, path, authz string, body interface{}) (int, map[string]interface{}) {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	out := map[string]interface{}{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func errCode(m map[string]interface{}) string {
	if e, ok := m["error"].(map[string]interface{}); ok {
		c, _ := e["code"].(string)
		return c
	}
	return ""
}

func TestEnrollHTTPTokenSeparation(t *testing.T) {
	g, s := testServer(t)
	if _, _, err := s.Signup(service.Ctx{}, service.SignupInput{Email: "owner@http.test", Password: "senha-forte-123", OrgName: "HTTP Org"}); err != nil {
		t.Fatal(err)
	}
	tok, _, err := s.Login(service.Ctx{}, "owner@http.test", "senha-forte-123", "")
	if err != nil {
		t.Fatal(err)
	}
	user := "Bearer " + tok.AccessToken

	st, body := call(g, "POST", "/api/v1/enrollment-keys", user, map[string]interface{}{"label": "Lote", "max_uses": 0, "expires_in_days": nil})
	if st != 201 {
		t.Fatalf("criar código: %d %v", st, body)
	}
	code := body["data"].(map[string]interface{})["code"].(string)

	st, body = call(g, "POST", "/api/v1/enroll", "", map[string]interface{}{"code": "ZZZZ-ZZZZ-ZZZZ", "rustdesk_id": "1", "uuid": "u"})
	if st != 404 || errCode(body) != "enrollment_key_invalid" {
		t.Fatalf("código inválido: %d %v", st, body)
	}
	st, body = call(g, "POST", "/api/v1/enroll", "", map[string]interface{}{"code": code, "rustdesk_id": "700000001", "uuid": "uuid-7",
		"hostname": "NOTE-EB101", "platform": "windows", "os": "Windows 11", "version": "0.2.0", "username": "maria"})
	if st != 200 {
		t.Fatalf("enroll: %d %v", st, body)
	}
	data := body["data"].(map[string]interface{})
	devTok := data["device_token"].(string)
	devID := int(data["device"].(map[string]interface{})["id"].(float64))
	pw := data["password"].(map[string]interface{})
	if pw["version"].(float64) != 1 || pw["value"].(string) == "" || data["heartbeat_interval"].(float64) != 60 {
		t.Fatalf("resposta do enroll: %v", data)
	}

	// token de dispositivo nunca vale em rotas de usuário
	for _, h := range []string{"Device " + devTok, "Bearer " + devTok} {
		if st, body := call(g, "GET", "/api/v1/devices", h, nil); st != 401 {
			t.Fatalf("GET /devices com %q deveria ser 401, got %d %v", h[:6], st, body)
		}
		if st, _ := call(g, "GET", "/api/v1/me", h, nil); st != 401 {
			t.Fatalf("GET /me com token de dispositivo deveria ser 401, got %d", st)
		}
	}
	// JWT de usuário nunca vale no agente
	for _, h := range []string{user, "Device " + tok.AccessToken, ""} {
		if st, body := call(g, "POST", "/api/v1/agent/heartbeat", h, map[string]interface{}{"password_version": 0}); st != 401 || errCode(body) != "device_token_invalid" {
			t.Fatalf("agente com credencial de usuário deveria ser 401 device_token_invalid, got %d %v", st, body)
		}
	}
	st, body = call(g, "POST", "/api/v1/agent/heartbeat", "Device "+devTok, map[string]interface{}{"rustdesk_id": "700000001", "unattended": true, "password_version": 1})
	if st != 200 || body["data"].(map[string]interface{})["password"] != nil || body["data"].(map[string]interface{})["org_name"] != "HTTP Org" {
		t.Fatalf("heartbeat do agente: %d %v", st, body)
	}
	// listagem com campos novos
	st, body = call(g, "GET", "/api/v1/devices?enrolled=true", user, nil)
	rows := body["data"].([]interface{})
	if st != 200 || len(rows) != 1 {
		t.Fatalf("GET /devices?enrolled=true: %d %v", st, body)
	}
	d := rows[0].(map[string]interface{})
	for _, f := range []string{"enrolled", "enrolled_at", "unattended", "password_version", "password_applied", "revoked", "last_agent_at", "group_name", "online"} {
		if _, ok := d[f]; !ok {
			t.Errorf("campo %s ausente em /devices", f)
		}
	}
	if _, leak := d["password"]; leak {
		t.Error("senha nunca em listas")
	}
	if d["enrolled"] != true || d["unattended"] != true || d["password_applied"] != true {
		t.Fatalf("valores: %v", d)
	}
	// revoga → agente recebe 410
	if st, _ := call(g, "POST", "/api/v1/devices/"+strconv.Itoa(devID)+"/revoke", user, nil); st != 204 {
		t.Fatalf("revoke: %d", st)
	}
	if st, body := call(g, "POST", "/api/v1/agent/heartbeat", "Device "+devTok, map[string]interface{}{}); st != 410 || errCode(body) != "device_revoked" {
		t.Fatalf("revogado deveria ser 410 device_revoked, got %d %v", st, body)
	}
}
