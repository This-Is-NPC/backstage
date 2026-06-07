BIN := bin/backstage

.PHONY: build test vet check run clean

build: ## compile the CLI to bin/backstage
	go build -o $(BIN) ./cmd/backstage

test: ## run unit tests
	go test ./...

vet: ## go vet
	go vet ./...

check: ## build + vet + test + tool-agnostic gate
	./scripts/check.sh

clean:
	rm -rf bin
