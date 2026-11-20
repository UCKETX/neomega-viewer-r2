# 以下命令在 apps/neomega-viewer-r2 目录执行。
#
# 本地构建并启动 Viewer（macOS / Linux）
#   make build && ./dist/neOmega-R2 -listen 127.0.0.1:2412
# make build 使用工作区里的源码构建网页、离线播放器和可执行文件。启动后打开 http://127.0.0.1:2412。
#
# 本地一次编译并打包四个平台，产物位于 dist/local/
#   make build-all
#
# 用本地提交触发 GitHub Actions 发布
# 先提交要发布的 Viewer 改动。发布工作流和 release-deps.lock 都随这个提交一起检出。
#   git fetch origin --tags
#   make tag VERSION=v0.1.1
#   git push origin refs/tags/v0.1.1
# tag 指向本地当前提交，推送 tag 会触发发布构建，tag 名就是产物的版本号。
# 省略 VERSION 时，make tag 会按最新的本地正式 tag 递增补丁号，并打印新版本。
# 如果发布也要采用本机的依赖版本，先把依赖提交推到各自的 GitHub 仓库，再把这些提交写入锁文件并提交。
# 当本机依赖与各自远端默认分支一致时，可以用 make update-deps 自动更新锁文件。

NAME := neOmega-R2
R2 := ../../neomega-r2/r2
TSC := $(R2)/node_modules/typescript/bin/tsc
NPM_LOCK := $(R2)/node_modules/.package-lock.json
RELEASE_LOCK := release-deps.lock
RELEASE_REPOS := neomega-r2 neomega-celler neomega-blocks neomega-nbt neomega-packfs neomega-converter-core apps/neomega-converter
RELEASE_TARGETS := darwin/arm64 windows/amd64 linux/amd64 linux/arm64

VERSION ?=
export VERSION
export VIEWER_TAG := $(shell git describe --tags --exact-match --match 'v[0-9]*' HEAD 2>/dev/null)

.PHONY: build build-all web test deps update-deps version tag release

build: web
	mkdir -p dist
	go build -trimpath -ldflags "-X main.version=$${VERSION:-$${VIEWER_TAG:-dev}}" -o "dist/$(NAME)$$(go env GOEXE)" ./cmd/viewer

$(NPM_LOCK): $(R2)/package-lock.json
	npm ci --prefix "$(R2)"

web: $(NPM_LOCK)
	node "$(R2)/scripts/build.mjs"
	node web/dawnlight/build.mjs "$(R2)"
	node "$(TSC)" -p web
	node "$(TSC)" -p internal/snapshot/web
	node internal/snapshot/web/build.mjs

test: web
	go test ./...

# 下载锁定的依赖。临时目录等检出成功后再转为正式目录，下载失败后可以重试。
deps:
	@set -eu; \
	while read -r repo revision; do \
		dest="../../$$repo"; \
		if [ -e "$$dest" ]; then \
			if [ "$$(git -C "$$dest" rev-parse HEAD)" = "$$revision" ]; then continue; fi; \
			printf '已有依赖 %s。请在空工作区使用 make deps 检出锁定版本。\n' "$$repo" >&2; \
			exit 1; \
		fi; \
		mkdir -p "$$(dirname "$$dest")"; \
		scratch=$$(mktemp -d "$$dest.XXXXXX"); \
		trap 'rm -rf "$$scratch"' EXIT; \
		git init --quiet "$$scratch"; \
		git -C "$$scratch" fetch --quiet --depth=1 "https://github.com/CMA2401PT/$${repo##*/}.git" "$$revision"; \
		git -C "$$scratch" checkout --quiet --detach FETCH_HEAD; \
		mv "$$scratch" "$$dest"; \
		trap - EXIT; \
	done < "$(RELEASE_LOCK)"

# 全部远端版本都查询成功后才替换锁文件。
update-deps:
	@set -eu; \
	scratch=$$(mktemp './.release-deps.XXXXXX'); \
	trap 'rm -f "$$scratch"' EXIT; \
	for repo in $(RELEASE_REPOS); do \
		ref=$$(git ls-remote --exit-code "https://github.com/CMA2401PT/$${repo##*/}.git" HEAD); \
		printf '%s %s\n' "$$repo" "$${ref%%[[:space:]]*}"; \
	done > "$$scratch"; \
	mv "$$scratch" "$(RELEASE_LOCK)"

version:
	@printf '%s\n' "$${VERSION:-$${VIEWER_TAG:-dev}}"

# 以最新的正式版本为基准递增补丁号，也可用 VERSION 指定版本。
tag:
	@set -eu; \
	version="$$VERSION"; \
	if [ -z "$$version" ]; then \
		version=$$(git tag --list --sort=-version:refname | awk -F. \
			'/^v[0-9]+\.[0-9]+\.[0-9]+$$/ { printf "%s.%s.%d\n", $$1, $$2, $$3 + 1; found = 1; exit } END { if (!found) print "v0.1.0" }'); \
	fi; \
	git tag -a "$$version" -m "$(NAME) $$version"; \
	printf '%s\n' "$$version"

# 本机开发包与发布包共用平台列表和构建参数，各自写入独立目录。
build-all: PACKAGE_DIR := dist/local
build-all: export PACKAGE_VERSION = $(or $(VERSION),dev)
release: PACKAGE_DIR := dist/release
release: export PACKAGE_VERSION = $(or $(VERSION),$(VIEWER_TAG))
build-all release: web
	@set -eu; \
	version="$$PACKAGE_VERSION"; \
	if [ -z "$$version" ]; then \
		printf '请在版本标签上构建，或通过 VERSION 指定发布版本。\n' >&2; \
		exit 1; \
	fi; \
	scratch=$$(mktemp -d); \
	trap 'rm -rf "$$scratch"' EXIT; \
	mkdir -p "$$scratch/packages"; \
	for target in $(RELEASE_TARGETS); do \
		os=$${target%/*}; \
		arch=$${target#*/}; \
		binary="$(NAME)"; \
		platform="$$os"; \
		case "$$os" in \
			windows) binary="$$binary.exe";; \
			darwin) platform=macos;; \
		esac; \
		archive="$$scratch/packages/$(NAME)-$$version-$$platform-$$arch"; \
		GOWORK=off CGO_ENABLED=0 GOOS="$$os" GOARCH="$$arch" go build -mod=readonly -trimpath \
			-ldflags "-s -w -X main.version=$$version" -o "$$scratch/$$binary" ./cmd/viewer; \
		if [ "$$os" = windows ]; then \
			(cd "$$scratch" && zip -q "$$archive.zip" "$$binary"); \
		else \
			COPYFILE_DISABLE=1 tar -C "$$scratch" -czf "$$archive.tar.gz" "$$binary"; \
		fi; \
	done; \
	mkdir -p "$(PACKAGE_DIR)"; \
	mv "$$scratch"/packages/* "$(PACKAGE_DIR)/"
