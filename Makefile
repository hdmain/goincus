.PHONY: build install tidy test

BINARY ?= goincus
PREFIX ?= /usr/local
CONFIG_DIR ?= /etc/goincus
SHARE_DIR ?= /usr/share/goincus
SYSTEMD_DIR ?= /etc/systemd/system

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=dev" -o bin/$(BINARY) ./cmd/goincus

tidy:
	go mod tidy

test:
	go test ./...

install: build
	install -d $(DESTDIR)$(PREFIX)/bin
	install -d $(DESTDIR)$(CONFIG_DIR)
	install -d $(DESTDIR)$(SHARE_DIR)/migrations
	install -d $(DESTDIR)$(SYSTEMD_DIR)
	install -m 0755 bin/$(BINARY) $(DESTDIR)$(PREFIX)/bin/$(BINARY)
	install -m 0644 configs/config.yaml $(DESTDIR)$(CONFIG_DIR)/config.yaml
	install -m 0644 migrations/*.sql $(DESTDIR)$(SHARE_DIR)/migrations/
	install -m 0644 deploy/systemd/goincus.service $(DESTDIR)$(SYSTEMD_DIR)/goincus.service
	@echo "Installed. Run: sudo $(PREFIX)/bin/goincus init && systemctl enable --now goincus"
