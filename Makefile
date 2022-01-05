GOLANGCI_LINT_VERSION=v1.27.0

LINTER=./bin/golangci-lint
LINTER_VERSION_FILE=./bin/.golangci-lint-version-$(GOLANGCI_LINT_VERSION)

SHELL=/bin/bash

build:
	go build ./...

test:
	go test -race -v ./...

$(LINTER_VERSION_FILE):
	rm -f $(LINTER)
	curl -sfL https://install.goreleaser.com/github.com/golangci/golangci-lint.sh | bash -s $(GOLANGCI_LINT_VERSION)
	touch $(LINTER_VERSION_FILE)

lint: $(LINTER_VERSION_FILE)
	$(LINTER) run ./...

TEMP_TEST_OUTPUT=/tmp/sse-contract-test-service.log

build-contract-tests:
	@cd contract-tests && go mod tidy && go build

start-contract-test-service:
	@./contract-tests/contract-tests

start-contract-test-service-bg:
	@echo "Test service output will be captured in $(TEMP_TEST_OUTPUT)"
	@make start-contract-test-service >$(TEMP_TEST_OUTPUT) 2>&1 &

run-contract-tests:
	@curl -s https://raw.githubusercontent.com/launchdarkly/sse-contract-tests/v2.0.0/downloader/run.sh \
      | VERSION=v2 PARAMS="-url http://localhost:8000 -debug -stop-service-at-end" sh

contract-tests: build-contract-tests start-contract-test-service-bg run-contract-tests

.PHONY: build lint test build-contract-tests start-contract-test-service run-contract-tests contract-tests
