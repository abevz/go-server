BINDIR ?= dist/bin
IMAGE ?= go-server:dev

.PHONY: build dist image test generate check-generated run-stdlib run-stdlib-tls run-huma run-openapi run-connect

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$(BINDIR)/" ./cmd/...

dist:
	sh scripts/package.sh

image:
	docker build -t "$(IMAGE)" .

generate:
	sh scripts/generate.sh

check-generated:
	sh scripts/generate.sh --check

test:
	go test -v -race ./...

run-stdlib:
	go run ./cmd/stdlib

run-stdlib-tls:
	go run ./cmd/stdlib-tls

run-huma:
	go run ./cmd/huma

run-openapi:
	go run ./cmd/openapi

run-connect:
	go run ./cmd/connect
