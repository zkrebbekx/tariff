.PHONY: test cover fuzz bench lint fmt vet tidy clean help

test: ## Run unit tests with race detector
	go test -race -timeout 120s ./...
	cd examples && go build ./...

cover: ## Run tests and open an HTML coverage report
	go test -coverprofile=coverage.out -coverpkg=./... ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "wrote coverage.html"

fuzz: ## Fuzz each target for 30s
	go test -run=^$$ -fuzz=^FuzzAllocate$$ -fuzztime=30s .
	go test -run=^$$ -fuzz=^FuzzRateNoPanic$$ -fuzztime=30s .
	go test -run=^$$ -fuzz=^FuzzGraduatedMonotonic$$ -fuzztime=30s .

bench: ## Run benchmarks
	go test -run=^$$ -bench=. -benchmem ./...

lint: ## Run golangci-lint (must be installed)
	golangci-lint run

fmt: ## Format code
	gofmt -s -w .

vet: ## Vet both modules
	go vet ./...
	cd examples && go vet ./...

tidy: ## Tidy modules
	go mod tidy
	cd examples && go mod tidy

clean: ## Remove build artifacts
	rm -rf bin coverage.out coverage.html

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'
