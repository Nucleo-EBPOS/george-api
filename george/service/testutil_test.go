package service

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/lejianwen/rustdesk-api/v2/george/config"
	"github.com/lejianwen/rustdesk-api/v2/george/migrate"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Os testes de regra rodam contra um Postgres real (migrações de verdade).
// GEORGE_TEST_DB_URL aponta para ele; sem conexão, os testes são pulados.
func testDB(t *testing.T) *Svc {
	t.Helper()
	dsn := os.Getenv("GEORGE_TEST_DB_URL")
	if dsn == "" {
		dsn = "postgres://george:george@127.0.0.1:5433/george_test?sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent),
		DisableForeignKeyConstraintWhenMigrating: true})
	if err != nil {
		t.Skipf("postgres de teste indisponível: %v", err)
	}
	if sqlDB, err := db.DB(); err != nil || sqlDB.Ping() != nil {
		t.Skip("postgres de teste indisponível (ping)")
	}
	// esquema do zero: upstream AutoMigrate + migrações George
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
	log.SetLevel(logrus.ErrorLevel)
	s := New(db, cfg, log)
	if err := s.RegisterHooks(db); err != nil {
		t.Fatal(err)
	}
	return s
}

func signup(t *testing.T, s *Svc, email, org string) Ctx {
	t.Helper()
	u, _, err := s.Signup(Ctx{IP: "127.0.0.1"}, SignupInput{Email: email, Password: "senha-forte-123", OrgName: org})
	if err != nil {
		t.Fatalf("signup %s: %v", email, err)
	}
	return Ctx{User: u, IP: "127.0.0.1"}
}

func addUser(t *testing.T, s *Svc, owner Ctx, email, role string) Ctx {
	t.Helper()
	u, _, err := s.OrgUserCreate(owner, owner.User.OrgID, CreateUserInput{Email: email, Role: role, Password: "senha-forte-123"})
	if err != nil {
		t.Fatalf("add user %s: %v", email, err)
	}
	return Ctx{User: u, IP: "127.0.0.1"}
}

func heartbeat(t *testing.T, s *Svc, c Ctx, id string) *HeartbeatOut {
	t.Helper()
	out, err := s.Heartbeat(c, HeartbeatInput{ID: id, Uuid: "uuid-" + id, Hostname: "host-" + id, Platform: "windows", Version: "1.4"})
	if err != nil {
		t.Fatalf("heartbeat %s: %v", id, err)
	}
	return out
}

func contextBG() context.Context { return context.Background() }
