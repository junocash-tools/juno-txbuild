.PHONY: build rust-build rust-test test test-unit test-integration test-e2e fmt tidy clean

TESTFLAGS ?=
TESTTIMEOUT ?= 30m

ifneq ($(JUNO_TEST_LOG),)
TESTFLAGS += -v
endif

BIN_DIR := bin
BIN := $(BIN_DIR)/juno-txbuild

RUST_MANIFEST := rust/txbuild/Cargo.toml

build: rust-build
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN) ./cmd/juno-txbuild

rust-build:
	cargo build --release --manifest-path $(RUST_MANIFEST)

rust-test:
	cargo test --manifest-path $(RUST_MANIFEST)

test-unit:
	CGO_ENABLED=0 go test $(TESTFLAGS) -timeout=$(TESTTIMEOUT) ./internal/logic

test-integration:
	$(MAKE) rust-build
	go test $(TESTFLAGS) -timeout=$(TESTTIMEOUT) -tags=integration ./...

test-e2e:
	$(MAKE) build
	go test $(TESTFLAGS) -timeout=$(TESTTIMEOUT) -tags=e2e ./...

test: test-unit test-integration test-e2e

fmt:
	gofmt -w .

tidy:
	go mod tidy

clean:
	rm -rf $(BIN_DIR)
	rm -rf rust/txbuild/target
