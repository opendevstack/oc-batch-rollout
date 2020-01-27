SHELL = /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.DELETE_ON_ERROR:
MAKEFLAGS += --warn-undefined-variables
MAKEFLAGS += --no-builtin-rules

imports:
	goimports -w .
.PHONY: imports

fmt:
	gofmt -w .
.PHONY: fmt

lint:
	go mod download && golangci-lint run
.PHONY: lint

build: build-linux build-darwin build-windows
.PHONY: build

build-linux:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o obr-linux-amd64
.PHONY: build-linux

build-darwin:
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -o obr-darwin-amd64
.PHONY: build-darwin

build-windows:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o obr-windows-amd64.exe
.PHONY: build-windows
