GOLANGCI_VERSION=v1.10.2
# earlier versions of golangci-lint don't work in go 1.9

SHELL=/bin/bash

test: lint
	go test

lint:
	./bin/golangci-lint run ./...

init:
	curl -sfL https://install.goreleaser.com/github.com/golangci/golangci-lint.sh | bash -s $(GOLANGCI_VERSION)

.PHONY: init lint test
