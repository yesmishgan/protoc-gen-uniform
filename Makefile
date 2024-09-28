LOCAL_BIN:=$(CURDIR)/bin

.PHONY: build
build:
	go build -o ${LOCAL_BIN}/protoc-gen-bomboglot

.PHONY: example
example: build
	protoc \
		--plugin=protoc-gen-bomboglot=$(LOCAL_BIN)/protoc-gen-bomboglot \
		--bomboglot_out=./test/out \
		`find ./test -name "*.proto"`

GOLANGCI_BIN := $(LOCAL_BIN)/golangci-lint
GOLANGCI_TAG ?= 1.55.2

install-lint: export GOBIN := $(LOCAL_BIN)
install-lint: ## Установить golangci-lint в текущую директорию с исполняемыми файлами
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@v$(GOLANGCI_TAG)

.lint: install-lint
	$(GOLANGCI_BIN) run \
		--sort-results \
		--max-issues-per-linter=1000 \
		--max-same-issues=1000 \
		./...

lint: .lint