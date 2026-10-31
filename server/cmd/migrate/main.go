// Command migrate 是最小化的 SQL 迁移工具。
//
// 用法：
//
//	go run ./cmd/migrate -c configs/config.dev.yaml up
//	go run ./cmd/migrate -c configs/config.dev.yaml down
//	go run ./cmd/migrate -c configs/config.dev.yaml status
//
// 约定：迁移文件名形如 0001_init.up.sql / 0001_init.down.sql，按文件名字典序执行；
// 已应用的版本记录在 schema_migrations 表，重复执行是幂等的。
package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "github.com/go-sql-driver/mysql"

	"github.com/datechat/server/internal/config"
)

const createMigrationTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version    VARCHAR(64) NOT NULL COMMENT '迁移版本号，取文件名前缀',
	applied_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '应用时间',
	PRIMARY KEY (version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='迁移版本记录'`

func main() {
	var (
		configPath = flag.String("c", "", "配置文件路径；留空则按 APP_ENV 推导")
		dir        = flag.String("dir", "migrations", "迁移脚本目录")
	)
	flag.Parse()

	command := "up"
	if flag.NArg() > 0 {
		command = flag.Arg(0)
	}

	cfg := config.MustLoad(*configPath)

	db, err := sql.Open("mysql", cfg.MySQL.DSN())
	if err != nil {
		log.Fatalf("连接 MySQL 失败: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.Ping(); err != nil {
		log.Fatalf("MySQL 不可用（%s）: %v", cfg.MySQL.MaskedDSN(), err)
	}

	if _, err := db.Exec(createMigrationTable); err != nil {
		log.Fatalf("创建 schema_migrations 失败: %v", err)
	}

	switch command {
	case "up":
		if err := migrateUp(db, *dir); err != nil {
			log.Fatalf("迁移失败: %v", err)
		}
	case "down":
		if err := migrateDown(db, *dir); err != nil {
			log.Fatalf("回滚失败: %v", err)
		}
	case "status":
		if err := printStatus(db, *dir); err != nil {
			log.Fatalf("查询状态失败: %v", err)
		}
	default:
		log.Fatalf("未知命令 %q，可用: up | down | status", command)
	}
}

func migrateUp(db *sql.DB, dir string) error {
	files, err := migrationFiles(dir, ".up.sql")
	if err != nil {
		return err
	}
	applied, err := appliedVersions(db)
	if err != nil {
		return err
	}

	pending := 0
	for _, file := range files {
		version := versionOf(file, ".up.sql")
		if _, ok := applied[version]; ok {
			continue
		}
		log.Printf("→ 应用 %s", filepath.Base(file))
		if err := execFile(db, file, false); err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(file), err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, version); err != nil {
			return fmt.Errorf("记录版本 %s 失败: %w", version, err)
		}
		pending++
	}

	if pending == 0 {
		log.Println("no pending migration")
	} else {
		log.Printf("完成：共应用 %d 个迁移", pending)
	}
	return nil
}

func migrateDown(db *sql.DB, dir string) error {
	var version string
	err := db.QueryRow(`SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("没有可回滚的迁移")
	}
	if err != nil {
		return err
	}

	file := filepath.Join(dir, version+".down.sql")
	if _, statErr := os.Stat(file); statErr != nil {
		return fmt.Errorf("找不到回滚脚本 %s: %w", file, statErr)
	}

	log.Printf("← 回滚 %s", filepath.Base(file))
	// 回滚按语句逆序执行，保证先删依赖方再删被依赖方。
	if err := execFile(db, file, true); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(file), err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = ?`, version); err != nil {
		return fmt.Errorf("清除版本记录 %s 失败: %w", version, err)
	}

	log.Printf("完成：已回滚到 %s 之前", version)
	return nil
}

func printStatus(db *sql.DB, dir string) error {
	files, err := migrationFiles(dir, ".up.sql")
	if err != nil {
		return err
	}
	applied, err := appliedVersions(db)
	if err != nil {
		return err
	}

	fmt.Println("迁移状态：")
	for _, file := range files {
		version := versionOf(file, ".up.sql")
		state := "待应用"
		if _, ok := applied[version]; ok {
			state = "已应用"
		}
		fmt.Printf("  %-24s %s\n", version, state)
	}
	return nil
}

func migrationFiles(dir, suffix string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*"+suffix))
	if err != nil {
		return nil, fmt.Errorf("扫描迁移目录 %s 失败: %w", dir, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("目录 %s 下没有 %s 文件", dir, suffix)
	}
	sort.Strings(files)
	return files, nil
}

func appliedVersions(db *sql.DB) (map[string]struct{}, error) {
	rows, err := db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("读取迁移版本失败: %w", err)
	}
	defer func() { _ = rows.Close() }()

	applied := make(map[string]struct{})
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = struct{}{}
	}
	return applied, rows.Err()
}

func versionOf(path, suffix string) string {
	return strings.TrimSuffix(filepath.Base(path), suffix)
}

// execFile 逐条执行 SQL 文件。
// MySQL DDL 无法回滚，因此这里不加事务，失败时直接中断并提示是第几条语句。
func execFile(db *sql.DB, path string, reverse bool) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	statements := splitStatements(string(raw))
	if reverse {
		for i, j := 0, len(statements)-1; i < j; i, j = i+1, j-1 {
			statements[i], statements[j] = statements[j], statements[i]
		}
	}

	for i, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("第 %d 条语句执行失败: %w\nSQL: %s", i+1, err, truncate(statement, 400))
		}
	}
	log.Printf("   执行 %d 条语句", len(statements))
	return nil
}

// splitStatements 去掉整行 -- 注释后按分号切分。
// 当前迁移脚本不含存储过程/触发器（内部有分号），所以这个简化切分是安全的。
func splitStatements(raw string) []string {
	var builder strings.Builder
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		builder.WriteString(line)
		builder.WriteString("\n")
	}

	parts := strings.Split(builder.String(), ";")
	statements := make([]string, 0, len(parts))
	for _, part := range parts {
		if statement := strings.TrimSpace(part); statement != "" {
			statements = append(statements, statement)
		}
	}
	return statements
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
