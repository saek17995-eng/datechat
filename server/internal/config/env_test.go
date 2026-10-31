package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("写 %s 失败: %v", p, err)
	}
	return p
}

func TestLoadEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, ".env", `# 注释行

JWT_SECRET=plain-value
export TENCENT_IM_SECRET_KEY="quoted # not comment"
TENCENT_SMS_SIGN_NAME='单引号值'
TENCENT_COS_BUCKET=datechat-1250000000   # 行尾注释
not a valid line
BAD-KEY=nope
`)

	keys := []string{"JWT_SECRET", "TENCENT_IM_SECRET_KEY", "TENCENT_SMS_SIGN_NAME", "TENCENT_COS_BUCKET", "BAD-KEY"}
	for _, k := range keys {
		os.Unsetenv(k)
	}
	t.Cleanup(func() {
		for _, k := range keys {
			os.Unsetenv(k)
		}
	})

	n, err := LoadEnvFile(path)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if n != 4 {
		t.Fatalf("期望注入 4 项（非法键名应被跳过），实际 %d", n)
	}

	cases := map[string]string{
		"JWT_SECRET":            "plain-value",
		"TENCENT_IM_SECRET_KEY": "quoted # not comment", // 引号内的 # 不是注释
		"TENCENT_SMS_SIGN_NAME": "单引号值",
		"TENCENT_COS_BUCKET":    "datechat-1250000000", // 未加引号则截断行尾注释
	}
	for k, want := range cases {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s = %q，期望 %q", k, got, want)
		}
	}
	if _, exists := os.LookupEnv("BAD-KEY"); exists {
		t.Error("非法键名 BAD-KEY 不应被注入")
	}
}

// 进程环境变量优先：容器里用 Secret 注入时，不能反过来被仓库里的旧 .env 覆盖。
func TestLoadEnvFileDoesNotOverrideExisting(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, ".env", "JWT_SECRET=from-file\n")
	t.Setenv("JWT_SECRET", "from-process")

	if _, err := LoadEnvFile(path); err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if got := os.Getenv("JWT_SECRET"); got != "from-process" {
		t.Fatalf("进程环境变量被覆盖: %q", got)
	}
}

// 文件不存在不应报错：没配密钥也要能起 dev 环境，由 validate 决定是否拒绝。
func TestLoadEnvFileMissingIsNotError(t *testing.T) {
	n, err := LoadEnvFile(filepath.Join(t.TempDir(), "nope.env"))
	if err != nil || n != 0 {
		t.Fatalf("缺失文件应静默跳过，实际 n=%d err=%v", n, err)
	}
}

// 端到端：yaml 里的 ${VAR} 应被 .env 中的值填上。
func TestLoadInjectsEnvFileIntoYAML(t *testing.T) {
	dir := t.TempDir()
	envPath := writeFile(t, dir, ".env", "TENCENT_IM_SECRET_KEY=secret-from-dotenv\n")
	yamlPath := writeFile(t, dir, "config.yaml", `
app:
  env: prod
  name: t
mysql:
  host: 127.0.0.1
  user: u
  database: d
redis:
  addrs: ["127.0.0.1:6379"]
jwt:
  secret: "prod-jwt-secret-0123456789abcdef0123"
security:
  data_key: "prod-data-key-0123456789abcdef0123"
  hmac_key: "prod-hmac-key-0123456789abcdef0123"
sms:
  provider: mock
im:
  sdk_app_id: 1400000000
  secret_key: "${TENCENT_IM_SECRET_KEY}"
  import_enabled: true
  callback_token: "prod-callback-token-0123456789"
realname:
  provider: mock
upload:
  provider: local
`)
	os.Unsetenv("TENCENT_IM_SECRET_KEY")
	t.Setenv("ENV_FILE", envPath)
	t.Cleanup(func() { os.Unsetenv("ENV_FILE") })

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	if cfg.IM.SecretKey != "secret-from-dotenv" {
		t.Fatalf("IM 密钥未从 .env 注入: %q", cfg.IM.SecretKey)
	}
	if cfg.EnvLoaded != 1 || cfg.EnvFile != envPath {
		t.Fatalf("EnvLoaded=%d EnvFile=%q 不符合预期", cfg.EnvLoaded, cfg.EnvFile)
	}
}

func TestRejectDevSecretInProd(t *testing.T) {
	for _, v := range []string{"", "dev-only-secret", "dev-aes-key", "change-me", "your-secret"} {
		if err := rejectDevSecret("jwt.secret", v); err == nil {
			t.Errorf("占位值 %q 应被拒绝", v)
		}
	}
	for _, v := range []string{"prod-jwt-secret-0123456789abcdef0123", "a", "1"} {
		if err := rejectDevSecret("jwt.secret", v); err != nil {
			t.Errorf("真实值 %q 不应被拒绝: %v", v, err)
		}
	}
}
