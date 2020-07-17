SHELL = /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.DELETE_ON_ERROR:
MAKEFLAGS += --warn-undefined-variables
MAKEFLAGS += --no-builtin-rules

## Fix imports
imports:
	goimports -w .
.PHONY: imports

## Run gofmt
fmt:
	gofmt -w .
.PHONY: fmt

## Run golangci-lint
lint:
	go mod download && golangci-lint run
.PHONY: lint

## Install binary into $PATH
install:
	go install -gcflags "all=-trimpath=$(CURDIR);$(shell go env GOPATH)"
.PHONY: install

## Build binaries for all operating systems
build: build-linux build-darwin build-windows
.PHONY: build

## Build Linux binary
build-linux:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -gcflags "all=-trimpath=$(CURDIR);$(shell go env GOPATH)" -o obr-linux-amd64
.PHONY: build-linux

## Build MacOS binary
build-darwin:
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -gcflags "all=-trimpath=$(CURDIR);$(shell go env GOPATH)" -o obr-darwin-amd64
.PHONY: build-darwin

## Build windows binary
build-windows:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -gcflags "all=-trimpath=$(CURDIR);$(shell go env GOPATH)" -o obr-windows-amd64.exe
.PHONY: build-windows

# HELP
# Based on https://gist.github.com/prwhite/8168133#gistcomment-2278355.
help:
		@echo ''
		@echo 'Usage:'
		@echo '  make <target>'
		@echo ''
		@echo 'Targets:'
		@awk '/^[a-zA-Z\-\_0-9]+:|^# .*/ { \
				helpMessage = match(lastLine, /^## (.*)/); \
				if (helpMessage) { \
						helpCommand = substr($$1, 0, index($$1, ":")-1); \
						helpMessage = substr(lastLine, RSTART + 3, RLENGTH); \
						printf "  %-35s %s\n", helpCommand, helpMessage; \
				} else { \
						printf "\n"; \
				} \
		} \
		{ lastLine = $$0 }' $(MAKEFILE_LIST)
.PHONY: help
