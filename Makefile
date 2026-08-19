SHELL := /bin/sh

SINGBOX_TAGS := with_quic,with_utls,with_clash_api,with_v2ray_api,with_wireguard,with_gvisor
GOFLAGS := -tags=$(SINGBOX_TAGS)
LDFLAGS := -checklinkname=0
VERSION ?= dev
DIST := dist

.PHONY: all frontend api frontend-check test vet build run release clean

all: build

api:
	npm --prefix frontend run api:generate

frontend:
	npm --prefix frontend run build

frontend-check:
	npm --prefix frontend run api:lint
	npm --prefix frontend run build

test:
	go test $(GOFLAGS) ./...

vet:
	go vet $(GOFLAGS) ./...

build: frontend
	go build $(GOFLAGS) -ldflags "$(LDFLAGS) -X main.version=$(VERSION)" -o $(DIST)/sb-ops ./

run:
	go run $(GOFLAGS) ./

release: frontend
	mkdir -p $(DIST)/release
	GOOS=linux GOARCH=amd64 CGO_ENABLED=1 go build $(GOFLAGS) -ldflags "$(LDFLAGS) -X main.version=$(VERSION)" -o $(DIST)/release/sb-ops-linux-amd64 ./
	GOOS=linux GOARCH=arm64 CGO_ENABLED=1 CC=aarch64-linux-gnu-gcc go build $(GOFLAGS) -ldflags "$(LDFLAGS) -X main.version=$(VERSION)" -o $(DIST)/release/sb-ops-linux-arm64 ./
	(cd $(DIST)/release && sha256sum sb-ops-linux-* > SHA256SUMS)

clean:
	rm -rf $(DIST)
