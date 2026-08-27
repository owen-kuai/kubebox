# kubebox envd-proxy 镜像构建
#
# 用法：
#   docker build -f deploy/docker/envd-proxy.Dockerfile -t kubebox-envd-proxy:dev .
#
# envd-proxy 是公网数据面边界：校验短期 scope JWT、剥离凭证、注入 sandbox id +
# 派生 scope，反向代理到可信 envd HTTP 门面；内置控制面专用路由管理 API 与后端
# 健康探测。静态编译，非 root 运行。
FROM golang:1.26 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN go build -trimpath -ldflags="-s -w" -o /out/envd-proxy ./cmd/envd-proxy

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/envd-proxy /envd-proxy
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/envd-proxy"]
