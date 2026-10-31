package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// LoadEnvFile 读取 .env 形式的密钥文件并写入进程环境变量。
//
// 设计取舍：
//   - 已存在的进程环境变量优先（进程注入 > 文件），这样容器/CI 用 Secret 注入时
//     不会被仓库里的旧文件意外覆盖。
//   - 文件不存在不算错误：本地没配密钥也应该能起 dev 环境，由 validate() 决定是否拒绝启动。
//   - 不引入第三方库：格式足够简单，自己解析只有几十行，还能精确控制"值里的 # 不是注释"。
//
// 支持的写法：
//
//	KEY=value
//	export KEY=value
//	KEY="value with # and spaces"   // 引号内的 # 不当注释
//	# 注释行
func LoadEnvFile(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("打开密钥文件 %s 失败: %w", path, err)
	}
	defer f.Close()

	loaded := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue // 不是 KEY=VALUE 形式，静默跳过（可能是手写备注）
		}
		key := strings.TrimSpace(line[:eq])
		if !isEnvKey(key) {
			continue
		}
		value := parseEnvValue(strings.TrimSpace(line[eq+1:]))
		if _, exists := os.LookupEnv(key); exists {
			continue // 进程环境变量优先
		}
		if err := os.Setenv(key, value); err != nil {
			return loaded, fmt.Errorf("设置环境变量 %s 失败（第 %d 行）: %w", key, lineNo, err)
		}
		loaded++
	}
	if err := scanner.Err(); err != nil {
		return loaded, fmt.Errorf("读取密钥文件 %s 失败: %w", path, err)
	}
	return loaded, nil
}

// parseEnvValue 去掉成对引号；未加引号时截断行尾注释。
// 密钥里出现 # 的概率不低，所以加引号的值一律原样保留。
func parseEnvValue(raw string) string {
	if raw == "" {
		return ""
	}
	if q := raw[0]; (q == '"' || q == '\'') && len(raw) >= 2 && raw[len(raw)-1] == q {
		return raw[1 : len(raw)-1]
	}
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		return strings.TrimSpace(raw[:i])
	}
	return raw
}

func isEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		ok := c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (i > 0 && c >= '0' && c <= '9')
		if !ok {
			return false
		}
	}
	return true
}

// EnvFilePath 推导密钥文件路径：ENV_FILE 显式指定优先，否则取配置文件同目录的 .env。
func EnvFilePath(configPath string) string {
	if p := os.Getenv("ENV_FILE"); p != "" {
		return p
	}
	dir := "configs"
	if configPath != "" {
		dir = filepathDir(configPath)
	}
	if dir == "" {
		dir = "."
	}
	return dir + "/.env"
}

func filepathDir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			if i == 0 {
				return "/"
			}
			return path[:i]
		}
	}
	return "."
}
