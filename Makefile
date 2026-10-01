TAGS   := novulkan,nox11
PREFIX ?= $(HOME)/.local

.PHONY: build run install uninstall vet

build:
	go build -tags $(TAGS) -trimpath -ldflags "-s -w" -o bin/omagram .

run: build
	./bin/omagram

vet:
	go vet -tags $(TAGS) ./...

install: build
	install -Dm755 bin/omagram $(PREFIX)/bin/omagram
	install -Dm644 omagram.desktop $(PREFIX)/share/applications/omagram.desktop

uninstall:
	rm -f $(PREFIX)/bin/omagram $(PREFIX)/share/applications/omagram.desktop
