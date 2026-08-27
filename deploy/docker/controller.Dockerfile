# kubebox controller 镜像构建
#
# 用法：
#   docker build -f deploy/docker/controller.Dockerfile -t kubebox-controller:dev .
#
# 多阶段：golang 构建 → distroless 静态运行镜像（非 root）
FROM golang:1.26 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN go build -trimpath -ldflags="-s -w" -o /out/controller ./cmd/controller

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/controller /controller
USER 65532:65532
ENTRYPOINT ["/controller"]
