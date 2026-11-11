// Command owner-memory-backfill imports eligible owner facts from one profile's
// private SQLite DB into the dedicated shared store. It does not copy secrets
// or other chats and keeps the original DB unchanged.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/costa/polypod/internal/memory"
	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)

type profile struct {
	Database struct {
		Path string `yaml:"path"`
	} `yaml:"database"`
	Data struct {
		OwnerMemoryDir         string `yaml:"owner_memory_dir"`
		OwnerMemoryImportLocal bool   `yaml:"owner_memory_import_local"`
	} `yaml:"data"`
	WhatsApp struct {
		OwnerChat string `yaml:"owner_chat"`
	} `yaml:"whatsapp"`
}

func run(configPath string) (int, error) {
	if configPath == "" {
		return 0, errors.New("-config is required")
	}
	absoluteConfig, err := filepath.Abs(configPath)
	if err != nil {
		return 0, err
	}
	data, err := os.ReadFile(absoluteConfig)
	if err != nil {
		return 0, err
	}
	var cfg profile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return 0, err
	}
	if cfg.Database.Path == "" || cfg.Data.OwnerMemoryDir == "" || cfg.WhatsApp.OwnerChat == "" {
		return 0, errors.New("database.path, data.owner_memory_dir and whatsapp.owner_chat are required")
	}
	dbPath := cfg.Database.Path
	if !filepath.IsAbs(dbPath) {
		dbPath = filepath.Join(filepath.Dir(absoluteConfig), dbPath)
	}
	dsn := (&url.URL{Scheme: "file", Path: dbPath, RawQuery: "mode=ro&_pragma=busy_timeout(5000)"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return 0, err
	}
	overlay, err := memory.NewOwnerOverlayStore(memory.NewSQLiteStore(db), cfg.Data.OwnerMemoryDir, cfg.WhatsApp.OwnerChat)
	if err != nil {
		return 0, err
	}
	backupDir := filepath.Join(filepath.Dir(dbPath), "owner-memory-backups")
	return overlay.BackfillOwnerFacts(context.Background(), backupDir, cfg.Data.OwnerMemoryImportLocal)
}

func main() {
	configPath := flag.String("config", "", "profile YAML path")
	flag.Parse()
	count, err := run(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "owner memory backfill failed:", err)
		os.Exit(1)
	}
	fmt.Printf("owner memories imported: %d\n", count)
}
