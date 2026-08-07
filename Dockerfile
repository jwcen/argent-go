FROM scratch

# modernc.org/sqlite 是纯 Go，无 cgo，最终二进制完全静态。
# scratch 是最小镜像：零基础系统，只有我们的二进制。

# 构建阶段（多阶段构建）
FROM golang:1.24-alpine AS builder

WORKDIR /build

# 缓存依赖
COPY go.mod go.sum ./
ENV GOTOOLCHAIN=local GOPROXY=https://goproxy.cn,direct GOSUMDB=off
RUN go mod download

# 编译
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o argent ./cmd/argent

# 运行阶段
FROM scratch

# 复制二进制
COPY --from=builder /build/argent /argent

# 复制 CA 证书（HTTPS 需要）
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

# 数据目录
VOLUME ["/data"]

EXPOSE 8889

ENTRYPOINT ["/argent"]
