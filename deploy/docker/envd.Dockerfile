# kubebox envd 沙箱容器镜像
#
# 用法：
#   docker build -f deploy/docker/envd.Dockerfile -t kubebox-envd:dev .
#
# envd 作为隔离容器的 PID 1，运行 ProcessExecutor（gRPC :50051 + HTTP :8080），
# 根文件系统只读，/sandbox 为可写挂载点。基础镜像自带 /bin/sh（ProcessExecutor
# 通过 /bin/sh -c 执行命令），因此需要保留最小 shell 运行时。
FROM golang:1.26 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN go build -trimpath -ldflags="-s -w" -o /out/envd ./cmd/envd

# 最小运行镜像：保留 /bin/sh 供命令执行，非 root 运行，根文件系统只读。
# 生产由 RuntimeClass(gvisor/kata) + Pod securityContext 进一步收紧。
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/envd /usr/local/bin/envd
# 沙箱根目录：运行时以 emptyDir/临时卷挂载，进程内 MkdirAll 兜底。
RUN mkdir -p /sandbox && chmod 0755 /sandbox
USER 65532:65532
EXPOSE 50051 8080
ENTRYPOINT ["/usr/local/bin/envd"]
