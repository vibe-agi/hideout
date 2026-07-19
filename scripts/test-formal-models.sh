#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TLA_VERSION="1.7.4"
TLA_SHA256="936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88"
TLA_URL="https://github.com/tlaplus/tlaplus/releases/download/v${TLA_VERSION}/tla2tools.jar"
CACHE_ROOT="${HIDEOUT_TLA_CACHE:-${HOME:-/tmp}/.cache/hideout/tla}"
JAR="${TLA2TOOLS_JAR:-${CACHE_ROOT}/tla2tools-${TLA_VERSION}.jar}"

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  echo "formal-models: missing shasum or sha256sum" >&2
  exit 1
}

verify_jar() {
  local actual
  actual="$(sha256_file "$JAR")"
  if [[ "$actual" != "$TLA_SHA256" ]]; then
    echo "formal-models: tla2tools.jar digest mismatch: got $actual" >&2
    exit 1
  fi
}

if ! command -v java >/dev/null 2>&1; then
  echo "formal-models: Java is required to run TLC" >&2
  exit 1
fi

if [[ ! -f "$JAR" ]]; then
  if ! command -v curl >/dev/null 2>&1; then
    echo "formal-models: curl is required for the initial TLC download" >&2
    exit 1
  fi
  mkdir -p "$(dirname "$JAR")"
  candidate="${JAR}.download.$$"
  trap 'rm -f "$candidate"' EXIT
  curl --fail --location --silent --show-error "$TLA_URL" --output "$candidate"
  actual="$(sha256_file "$candidate")"
  if [[ "$actual" != "$TLA_SHA256" ]]; then
    echo "formal-models: downloaded tla2tools.jar digest mismatch: got $actual" >&2
    exit 1
  fi
  mv "$candidate" "$JAR"
  trap - EXIT
fi

verify_jar

models=(
  ResourceLifecycle
  ConfigurationLifecycle
  NetworkTransition
  RequestWorkflow
)

cd "$ROOT"
for model in "${models[@]}"; do
  metadir="${TMPDIR:-/tmp}/hideout-tlc-${model}-$$"
  rm -rf "$metadir"
  echo "formal-models: checking $model"
  java -XX:+UseParallelGC -cp "$JAR" tlc2.TLC \
    -deadlock \
    -workers 1 \
    -metadir "$metadir" \
    -config "formal/${model}.cfg" \
    "formal/${model}.tla"
  rm -rf "$metadir"
done

echo "formal-models: ${#models[@]} models passed"
