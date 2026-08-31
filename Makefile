# kubebox 构建、测试与部署

GO       ?= go
DOCKER   ?= docker
KUBECTL  ?= kubectl

# 镜像 tag，生产发布前替换为版本号
IMAGE_TAG ?= dev

.PHONY: build test vet fmt tidy docker-build docker-build-controller docker-build-envd docker-build-envd-proxy deploy deploy-dry-run clean

## build: 编译所有二进制（./cmd/...）
build:
	$(GO) build ./...

## test: 运行全部单元测试
test:
	$(GO) test ./...

## vet: 静态检查
vet:
	$(GO) vet ./...

## fmt: 格式化所有 Go 源文件
fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

## tidy: 整理 go.mod / go.sum
tidy:
	$(GO) mod tidy

## docker-build: 构建全部镜像
docker-build: docker-build-controller docker-build-envd docker-build-envd-proxy

docker-build-controller:
	$(DOCKER) build -f deploy/docker/controller.Dockerfile -t kubebox-controller:$(IMAGE_TAG) .

docker-build-envd:
	$(DOCKER) build -f deploy/docker/envd.Dockerfile -t kubebox-envd:$(IMAGE_TAG) .

docker-build-envd-proxy:
	$(DOCKER) build -f deploy/docker/envd-proxy.Dockerfile -t kubebox-envd-proxy:$(IMAGE_TAG) .

## deploy-dry-run: 校验所有清单（不真正部署）
deploy-dry-run:
	$(KUBECTL) apply --dry-run=client -f deploy/kubernetes/

## deploy: 部署全部清单
deploy:
	$(KUBECTL) apply -f deploy/kubernetes/

## clean: 清理构建产物
clean:
	rm -f kubebox controller envd envd-proxy
