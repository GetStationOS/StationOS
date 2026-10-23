GO ?= go
GOFMT ?= $(shell $(GO) env GOROOT)/bin/gofmt
.PHONY: build run reset test race vet fmt check integrity clear-catalogue sync docker test-ollama
build:
	$(GO) build -trimpath -o bin/stationos ./cmd/stationos
run:
	$(GO) run ./cmd/stationos
# Stop the local server before resetting its default database.
reset:
	@if command -v lsof >/dev/null 2>&1 && lsof -t -- data/stationos.db >/dev/null 2>&1; then \
		echo "Local database is in use. Stop StationOS before running make reset." >&2; \
		exit 1; \
	fi
	rm -f -- data/stationos.db data/stationos.db-wal data/stationos.db-shm data/stationos.db.secrets-key
	@echo "Local database reset. Run make run to start fresh."
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

# Opt-in native model evaluation; all station configuration uses isolated fixtures.
test-ollama:
	@test -n "$$STATIONOS_TEST_OLLAMA_URL" || (echo "Set STATIONOS_TEST_OLLAMA_URL to your development Ollama origin." >&2; exit 1)
	STATIONOS_TEST_TRANSCRIPTS="$${STATIONOS_TEST_TRANSCRIPTS:-/tmp/stationos-ollama}" $(GO) test -v ./internal/producer -run TestOllamaStationScenarios -count=1 -timeout 40m
