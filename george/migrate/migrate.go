// Package migrate aplica as migrações SQL numeradas em migrations/ (Postgres).
// Cada arquivo roda numa transação e é registrado em schema_migrations.
package migrate

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

//go:embed migrations/*.sql
var files embed.FS

// Migration é um arquivo numerado NNNN_nome.sql.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// List devolve as migrações embutidas em ordem crescente.
func List() ([]Migration, error) {
	entries, err := fs.ReadDir(files, "migrations")
	if err != nil {
		return nil, err
	}
	out := make([]Migration, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		idx := strings.Index(name, "_")
		if idx <= 0 {
			return nil, fmt.Errorf("migração sem número: %s", name)
		}
		v, err := strconv.Atoi(name[:idx])
		if err != nil {
			return nil, fmt.Errorf("migração com número inválido: %s", name)
		}
		b, err := files.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		out = append(out, Migration{Version: v, Name: strings.TrimSuffix(name, ".sql"), SQL: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// Applied devolve as versões já aplicadas.
func Applied(db *gorm.DB) (map[int]bool, error) {
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INT PRIMARY KEY, name TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`).Error; err != nil {
		return nil, err
	}
	var versions []int
	if err := db.Raw(`SELECT version FROM schema_migrations`).Scan(&versions).Error; err != nil {
		return nil, err
	}
	m := make(map[int]bool, len(versions))
	for _, v := range versions {
		m[v] = true
	}
	return m, nil
}

// Up aplica todas as migrações pendentes; devolve quantas aplicou.
func Up(db *gorm.DB) (int, error) {
	list, err := List()
	if err != nil {
		return 0, err
	}
	applied, err := Applied(db)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range list {
		if applied[m.Version] {
			continue
		}
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec(m.SQL).Error; err != nil {
				return fmt.Errorf("migração %s: %w", m.Name, err)
			}
			return tx.Exec(`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
				m.Version, m.Name, time.Now()).Error
		})
		if err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
