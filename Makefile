GO ?= go
GOFMT ?= $(shell $(GO) env GOROOT)/bin/gofmt
.PHONY: build run test race vet fmt check integrity clear-catalogue sync docker
build:
	$(GO) build -trimpath -o bin/stationos ./cmd/stationos
run:
	$(GO) run ./cmd/stationos
test:
	$(GO) test ./...
race:
	$(GO) test -race ./...
vet:
	$(GO) vet ./...
fmt:
	$(GOFMT) -w $$(find . -name '*.go' -not -path './vendor/*')
check: test vet
	@test -z "$$($(GOFMT) -l $$(find . -name '*.go' -not -path './vendor/*'))"
integrity:
	$(GO) run ./cmd/stationos -task integrity
clear-catalogue:
	$(GO) run ./cmd/stationos -task clear-catalogue
sync:
	$(GO) run ./cmd/stationos -task sync -source "$(SOURCE)"
docker:
	docker build -t stationos:dev .
