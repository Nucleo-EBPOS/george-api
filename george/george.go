// Package george é o ponto de integração com o upstream (rustdesk-api):
// aplica migrações, liga hooks do gorm, fila de jobs e rotas /api/v1.
// O upstream chama só config.ApplyOverrides, Init e httpapi.Register.
package george

import (
	"context"
	"time"

	"github.com/lejianwen/rustdesk-api/v2/george/config"
	"github.com/lejianwen/rustdesk-api/v2/george/migrate"
	"github.com/lejianwen/rustdesk-api/v2/george/service"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// Init prepara o George depois de o upstream ter aberto o banco e rodado o AutoMigrate.
func Init(db *gorm.DB, log *logrus.Logger) error {
	cfg := config.Current
	if cfg.LogFormat == "json" {
		log.SetFormatter(&logrus.JSONFormatter{TimestampFormat: time.RFC3339})
	}
	if cfg.JWTSecret == "" {
		log.Warn("GEORGE_JWT_SECRET vazio: rotas /api/v1 de autenticação não funcionarão")
	}
	if db.Dialector.Name() != "postgres" {
		log.Warnf("george: banco %s não é Postgres; migrações do George exigem Postgres (GEORGE_DB_URL)", db.Dialector.Name())
		service.New(db, cfg, log)
		return nil
	}
	if cfg.AutoMigrate {
		n, err := migrate.Up(db)
		if err != nil {
			return err
		}
		log.WithField("applied", n).Info("george: migrações aplicadas")
	}
	svc := service.New(db, cfg, log)
	if err := svc.RegisterHooks(db); err != nil {
		return err
	}
	if cfg.Workers {
		go svc.RunWorker(context.Background(), 3*time.Second)
	}
	return nil
}
