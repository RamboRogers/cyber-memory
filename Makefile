BINARY     := cyber-memory
VERSION    ?= $(shell git describe --tags --dirty --always 2>/dev/null || echo dev)
LDFLAGS    := -X github.com/ramborogers/cyber-memory/internal/appinfo.Version='$(VERSION)'
BUILD_TAGS := ORT

# libtokenizers.a path — override via environment:
#   TOKENIZERS_LIB=/path/to/dir make build
TOKENIZERS_LIB ?= /usr/local/lib

.PHONY: build clean test deps tokenizers

## build: compile the binary (requires libtokenizers.a and libonnxruntime)
build:
	CGO_ENABLED=1 \
	CGO_LDFLAGS="-L$(TOKENIZERS_LIB) -ltokenizers" \
	go build -tags $(BUILD_TAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) .

## install: build and copy to /usr/local/bin
install: build
	install -m 0755 $(BINARY) /usr/local/bin/$(BINARY)

## tokenizers: build libtokenizers.a from source (requires Rust/cargo)
tokenizers:
	@echo "Building libtokenizers.a from source..."
	@TMPDIR=$$(mktemp -d) && \
	  git clone --depth 1 https://github.com/daulet/tokenizers.git $$TMPDIR && \
	  cd $$TMPDIR && cargo build --release && \
	  mkdir -p $(TOKENIZERS_LIB) && \
	  cp target/release/libtokenizers_ffi.a $(TOKENIZERS_LIB)/libtokenizers.a && \
	  echo "Installed: $(TOKENIZERS_LIB)/libtokenizers.a" && \
	  rm -rf $$TMPDIR

## test: run testable internal packages (no ORT runtime required)
test:
	go test ./internal/...

## clean: remove the binary
clean:
	rm -f $(BINARY)

## deps: tidy and download modules
deps:
	go mod tidy
	go mod download
