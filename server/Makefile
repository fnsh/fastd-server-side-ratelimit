GO ?= go
OUT ?= bin/server
PKG := ./cmd/main.go

.PHONY: all build clean

all: build

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w -extldflags "-static"' -o $(OUT) $(PKG)

clean:
	rm -f $(OUT)
