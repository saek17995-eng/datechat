# 多阶段构建：编译期用完整 golang 镜像，运行期只带一个静态二进制。
# 运行镜像里不放编译工具链，既缩小攻击面，也让镜像从 ~1GB 降到 ~20MB。
FROM golang:1.22-alpine AS builder

WORKDIR /src
# 先只拷依赖清单：代码没变时这一层命中缓存，不用每次重新下载模块。
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO 关掉才能跑在 alpine 上；-trimpath 去掉本地路径，避免把开发机目录结构带进产物。
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/migrate ./cmd/migrate \
 && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/bench ./cmd/bench \
 && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/cleanup ./cmd/cleanup

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -u 10001 app

WORKDIR /app
COPY --from=builder /out/api /app/api
COPY --from=builder /out/migrate /app/migrate
COPY --from=builder /out/bench /app/bench
COPY --from=builder /out/cleanup /app/cleanup
COPY configs /app/configs
COPY migrations /app/migrations

# 不用 root 跑：容器被攻破时也拿不到宿主权限。
USER 10001
EXPOSE 8080

# 启动顺序：先跑迁移再起服务。
# 迁移放在容器里而不是单独 Job，是为了避免"服务起来了但表还是旧的"这种半更新状态；
# 代价是多副本时会并发跑迁移，因此 cmd/migrate 内部对同一版本做了幂等保护。
# 用 shell 形式：需要先迁移再起服务，exec 形式不认 &&。
CMD ["/bin/sh", "-c", "/app/migrate -c /app/configs/config.prod.yaml up && exec /app/api -c /app/configs/config.prod.yaml"]
