#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

NUM_DOCS="${1:-100000}"

echo "=== BSON Decoding Benchmark ==="
echo "Number of documents: $NUM_DOCS"
echo ""

# Build fixture generator
echo "Building fixture generator..."
(cd fixture-generator && cargo build --release 2>/dev/null)

# Build Rust decoder
echo "Building Rust decoder..."
(cd rust-decoder && cargo build --release 2>/dev/null)

# Build Go decoder
echo "Building Go decoder..."
(cd go-decoder && go build -o decoder . 2>/dev/null)

# Generate fixture
echo "Generating fixture with $NUM_DOCS documents..."
./fixture-generator/target/release/fixture-generator "$NUM_DOCS" > fixtures/test_data.bson

FIXTURE_SIZE=$(ls -lh fixtures/test_data.bson | awk '{print $5}')
echo "Fixture size: $FIXTURE_SIZE"
echo ""

# Check if hyperfine is available
if command -v hyperfine &> /dev/null; then
    echo "Running benchmark with hyperfine..."
    echo ""
    hyperfine \
        --warmup 3 \
        --runs 10 \
        --export-markdown benchmark_results.md \
        --command-name "Go (mongo-driver v2.4.1)" \
        './go-decoder/decoder < fixtures/test_data.bson > /dev/null' \
        --command-name "Rust (bson crate)" \
        './rust-decoder/target/release/rust-decoder < fixtures/test_data.bson > /dev/null'

    echo ""
    echo "Results saved to benchmark_results.md"
else
    echo "hyperfine not found, using 'time' command..."
    echo ""

    echo "--- Go (mongo-driver v2.4.1) ---"
    time ./go-decoder/decoder < fixtures/test_data.bson > /dev/null
    echo ""

    echo "--- Rust (bson crate) ---"
    time ./rust-decoder/target/release/rust-decoder < fixtures/test_data.bson > /dev/null
fi
