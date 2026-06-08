.PHONY: build test vet syntax

# Build the tdb binary.
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o tdb ./cmd/tdb

# Run the full test suite.
test:
	go test ./...

vet:
	go vet ./...

# Fetch keyword/function/operator/command data from the upstream syntax
# libraries (ace-autocompleter, redis-doc, monaco-sql-languages, apache/doris)
# and write the normalized JSON into internal/suggest/data/. Requires network;
# the generated files are committed, so normal builds/CI do NOT need this.
syntax:
	go run ./internal/suggest/gen
	gofmt -w internal/suggest/data 2>/dev/null || true
