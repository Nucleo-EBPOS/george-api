package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lejianwen/rustdesk-api/v2/global"
	gm "github.com/lejianwen/rustdesk-api/v2/george/model"
	"github.com/lejianwen/rustdesk-api/v2/george/service"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"github.com/spf13/cobra"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// importSqliteCmd copia o banco SQLite do rustdesk-api (produção atual) para o
// Postgres configurado em GEORGE_DB_URL, criando uma org padrão para os dados
// legados. Idempotente: linhas com o mesmo id são sobrescritas pela origem
// (inclusive o admin id=1 que o upstream semeia no primeiro boot do Postgres),
// e as colunas do George (org_id, role, ...) não são tocadas no upsert.
var importSqliteCmd = &cobra.Command{
	Use:     "import-sqlite [arquivo.db] [nome-da-org]",
	Example: "import-sqlite ./data/rustdeskapi.db \"George (legado)\"",
	Short:   "Importa o SQLite do upstream para o Postgres do George",
	Args:    cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		if global.DB.Dialector.Name() != "postgres" {
			global.Logger.Error("destino não é Postgres; defina GEORGE_DB_URL")
			os.Exit(1)
		}
		if _, err := os.Stat(args[0]); err != nil {
			global.Logger.Errorf("arquivo não encontrado: %s", args[0])
			os.Exit(1)
		}
		orgName := "George (legado)"
		if len(args) > 1 {
			orgName = args[1]
		}
		src, err := gorm.Open(sqlite.Open(args[0]+"?mode=ro"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			global.Logger.Error(err)
			os.Exit(1)
		}
		if err := importSqlite(src, global.DB, orgName); err != nil {
			global.Logger.Error("importação falhou: ", err)
			os.Exit(1)
		}
		global.Logger.Info("importação concluída")
	},
}

func init() {
	rootCmd.AddCommand(importSqliteCmd)
}

type importTable struct {
	name  string
	model interface{} // ponteiro para slice do modelo
	pk    string
}

func importSqlite(src, dst *gorm.DB, orgName string) error {
	tables := []importTable{
		{"groups", &[]model.Group{}, "id"},
		{"users", &[]model.User{}, "id"},
		{"user_tokens", &[]model.UserToken{}, "id"},
		{"device_groups", &[]model.DeviceGroup{}, "id"},
		{"address_book_collections", &[]model.AddressBookCollection{}, "id"},
		{"address_book_collection_rules", &[]model.AddressBookCollectionRule{}, "id"},
		{"tags", &[]model.Tag{}, "id"},
		{"address_books", &[]model.AddressBook{}, "row_id"},
		{"peers", &[]model.Peer{}, "row_id"},
		{"user_thirds", &[]model.UserThird{}, "id"},
		{"oauths", &[]model.Oauth{}, "id"},
		{"login_logs", &[]model.LoginLog{}, "id"},
		{"share_records", &[]model.ShareRecord{}, "id"},
		{"audit_conns", &[]model.AuditConn{}, "id"},
		{"audit_files", &[]model.AuditFile{}, "id"},
		{"server_cmds", &[]model.ServerCmd{}, "id"},
	}
	// sessão sem hooks do George (sessions/backfill rodam depois, em lote)
	raw := dst.Session(&gorm.Session{SkipHooks: true, Logger: logger.Default.LogMode(logger.Silent)})
	for _, t := range tables {
		if !src.Migrator().HasTable(t.name) {
			global.Logger.Warnf("origem sem tabela %s; pulando", t.name)
			continue
		}
		if err := src.Table(t.name).Find(t.model).Error; err != nil {
			return fmt.Errorf("%s: %w", t.name, err)
		}
		res := raw.Table(t.name).Omit(clause.Associations).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: t.pk}}, UpdateAll: true}).CreateInBatches(t.model, 200)
		if res.Error != nil {
			return fmt.Errorf("%s: %w", t.name, res.Error)
		}
		global.Logger.Infof("%-32s %d linhas copiadas", t.name, res.RowsAffected)
		if err := raw.Exec(fmt.Sprintf(`SELECT setval(pg_get_serial_sequence('%s', '%s'), COALESCE((SELECT MAX(%s) FROM %s), 0) + 1, false)`,
			t.name, t.pk, t.pk, t.name)).Error; err != nil {
			return fmt.Errorf("sequence %s: %w", t.name, err)
		}
	}

	// org padrão para tudo que veio sem org
	var org gm.Org
	raw.Where("slug = ?", service.Slugify(orgName)).First(&org)
	if org.ID == 0 {
		org = gm.Org{Name: orgName, Slug: service.Slugify(orgName), Status: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
		if err := raw.Create(&org).Error; err != nil {
			return err
		}
		var free gm.Plan
		raw.Where("code = ?", "free").First(&free)
		raw.Clauses(clause.OnConflict{DoNothing: true}).Create(&gm.Subscription{OrgID: org.ID, PlanID: free.ID, Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	}
	stmts := []string{
		// admin do upstream vira owner; demais, tech
		`UPDATE users SET org_id = ?, role = CASE WHEN is_admin THEN 'owner' ELSE 'tech' END WHERE org_id = 0`,
		`UPDATE groups SET org_id = ? WHERE org_id = 0`,
		`UPDATE device_groups SET org_id = ? WHERE org_id = 0`,
		`UPDATE peers p SET org_id = u.org_id FROM users u WHERE p.user_id = u.id AND p.org_id = 0`,
		`UPDATE peers SET org_id = ? WHERE org_id = 0`,
		`UPDATE address_books a SET org_id = u.org_id FROM users u WHERE a.user_id = u.id AND a.org_id = 0`,
		`UPDATE tags t SET org_id = u.org_id FROM users u WHERE t.user_id = u.id AND t.org_id = 0`,
		`UPDATE address_book_collections c SET org_id = u.org_id FROM users u WHERE c.user_id = u.id AND c.org_id = 0`,
		// sessões a partir do histórico de conexões
		`INSERT INTO sessions (org_id, device_id, peer_id, from_peer, from_name, conn_id, session_uuid, audit_conn_id, conn_type, ip, started_at, ended_at, created_at, updated_at)
		 SELECT COALESCE(p.org_id, 0), COALESCE(p.row_id, 0), a.peer_id, a.from_peer, a.from_name, a.conn_id, a.session_id, a.id, a.type, a.ip,
		        a.created_at, CASE WHEN a.close_time > 0 THEN to_timestamp(a.close_time) ELSE NULL END, a.created_at, a.created_at
		 FROM audit_conns a LEFT JOIN peers p ON p.id = a.peer_id
		 WHERE a.action = 'new' ON CONFLICT (audit_conn_id) DO NOTHING`,
	}
	for _, s := range stmts {
		var err error
		if strings.Contains(s, "?") {
			err = raw.Exec(s, org.ID).Error
		} else {
			err = raw.Exec(s).Error
		}
		if err != nil {
			return err
		}
	}
	global.Logger.Infof("dados legados atribuídos à org #%d (%s)", org.ID, org.Name)
	return nil
}
