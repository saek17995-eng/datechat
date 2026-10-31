package config

import (
	"testing"
	"time"
)

func TestLoadDevConfig(t *testing.T) {
	cfg, err := Load("../../configs/config.dev.yaml")
	if err != nil {
		t.Fatalf("加载 dev 配置失败: %v", err)
	}

	if cfg.App.Env != "dev" {
		t.Errorf("env 期望 dev，实际 %s", cfg.App.Env)
	}
	if cfg.MySQL.Port != 3306 {
		t.Errorf("mysql 端口期望 3306，实际 %d", cfg.MySQL.Port)
	}
	if cfg.MySQL.Charset != "utf8mb4" {
		t.Errorf("字符集期望 utf8mb4，实际 %s", cfg.MySQL.Charset)
	}
	if cfg.JWT.AccessTTL.D() != 2*time.Hour {
		t.Errorf("access_ttl 期望 2h，实际 %s", cfg.JWT.AccessTTL)
	}
	if cfg.App.ShutdownTimeout.D() != 10*time.Second {
		t.Errorf("shutdown_timeout 期望 10s，实际 %s", cfg.App.ShutdownTimeout)
	}
	if cfg.IsProd() {
		t.Error("dev 配置不应被判定为生产环境")
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load("../../configs/config.not-exist.yaml"); err == nil {
		t.Fatal("配置文件不存在时应返回错误")
	}
}

func TestValidateRejectsWeakSecret(t *testing.T) {
	cfg := &Config{}
	cfg.MySQL.Host = "127.0.0.1"
	cfg.MySQL.User = "datechat"
	cfg.MySQL.Database = "datechat"
	cfg.Redis.Addrs = []string{"127.0.0.1:6379"}
	cfg.JWT.Secret = "too-short"

	if err := cfg.validate(); err == nil {
		t.Fatal("JWT 密钥过短时应校验失败")
	}
}

func TestMySQLDSN(t *testing.T) {
	cfg := MySQLConfig{
		Host:      "127.0.0.1",
		Port:      3306,
		User:      "datechat",
		Password:  "pwd",
		Database:  "datechat",
		Charset:   "utf8mb4",
		ParseTime: true,
	}

	dsn := cfg.DSN()
	if want := "datechat:pwd@tcp(127.0.0.1:3306)/datechat"; len(dsn) < len(want) || dsn[:len(want)] != want {
		t.Fatalf("DSN 前缀不正确: %s", dsn)
	}
	if masked := cfg.MaskedDSN(); masked == dsn {
		t.Fatal("MaskedDSN 必须隐藏口令")
	}
}
