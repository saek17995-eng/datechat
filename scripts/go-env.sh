#!/usr/bin/env bash
# Go 工具链环境。
#
# 本机没有系统级 Go，SDK 装在 conda 前缀里；依赖缓存统一放在仓库内的 .local，
# 避免污染 $HOME 也避免多项目互相抢缓存。
#
# 用法（每次调用 go 前先 source）：
#   source scripts/go-env.sh
#   cd server && go build ./...
#
# 注意：bash 的 shell 状态在不同命令之间不保留，所以不能只 export 一次。
export PATH="/Users/zhangchenyu/.local/bin:$PATH"
export GOROOT="/opt/anaconda3/pkgs/go-1.27.3-cgo_hfa1a970_0/go"
export GOPATH="/Users/zhangchenyu/WorkBuddy/2026-10-31-11-46-09/.local/gopath"
export GOCACHE="/Users/zhangchenyu/WorkBuddy/2026-10-31-11-46-09/.local/gocache"
export GOMODCACHE="$GOPATH/pkg/mod"
export GOFLAGS="-mod=mod"
export CGO_ENABLED=1
