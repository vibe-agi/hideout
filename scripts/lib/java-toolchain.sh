#!/usr/bin/env bash

# hideout_require_native_java verifies the JVM used by release gates before
# they spend time in TLC. It deliberately compares normalized host/JVM
# architectures instead of assuming that an arm64 hosted runner also selected
# an arm64 Java installation.
hideout_require_native_java() {
  local command java_properties host_arch java_arch java_specification
  local java_version

  for command in awk java uname; do
    if ! command -v "$command" >/dev/null 2>&1; then
      printf 'java-toolchain: missing required command: %s\n' "$command" >&2
      return 1
    fi
  done

  if ! java_properties="$(java -XshowSettings:properties -version 2>&1)"; then
    printf 'java-toolchain: could not inspect Java properties\n' >&2
    return 1
  fi
  host_arch="$(uname -m)"
  java_arch="$(
    printf '%s\n' "$java_properties" |
      awk -F= '
        $1 ~ /^[[:space:]]*os[.]arch[[:space:]]*$/ {
          gsub(/^[[:space:]]+|[[:space:]]+$/, "", $2)
          print $2
          exit
        }
      '
  )"
  java_specification="$(
    printf '%s\n' "$java_properties" |
      awk -F= '
        $1 ~ /^[[:space:]]*java[.]specification[.]version[[:space:]]*$/ {
          gsub(/^[[:space:]]+|[[:space:]]+$/, "", $2)
          print $2
          exit
        }
      '
  )"
  java_version="$(
    printf '%s\n' "$java_properties" |
      awk -F= '
        $1 ~ /^[[:space:]]*java[.]version[[:space:]]*$/ {
          gsub(/^[[:space:]]+|[[:space:]]+$/, "", $2)
          print $2
          exit
        }
      '
  )"

  if [ -z "$java_arch" ] || [ -z "$java_specification" ] ||
    [ -z "$java_version" ]; then
    printf 'java-toolchain: Java identity properties are incomplete\n' >&2
    return 1
  fi
  if [ "$java_specification" != "21" ]; then
    printf 'java-toolchain: Java specification=%s, want=21\n' \
      "$java_specification" >&2
    return 1
  fi
  case "$host_arch:$java_arch" in
    arm64:aarch64 | arm64:arm64 | aarch64:aarch64 | aarch64:arm64)
      ;;
    x86_64:amd64 | x86_64:x86_64 | amd64:amd64 | amd64:x86_64)
      ;;
    *)
      printf 'java-toolchain: translated JVM refused: host=%s java=%s\n' \
        "$host_arch" "$java_arch" >&2
      return 1
      ;;
  esac

  HIDEOUT_JAVA_HOST_ARCH="$host_arch"
  HIDEOUT_JAVA_ARCH="$java_arch"
  HIDEOUT_JAVA_SPECIFICATION="$java_specification"
  HIDEOUT_JAVA_VERSION="$java_version"
  printf \
    'java-toolchain: result=passed hostArch=%s javaArch=%s specification=%s version=%s native=true\n' \
    "$host_arch" "$java_arch" "$java_specification" "$java_version"
}

hideout_java_toolchain_self_test() {
  local output

  output="$(
    (
      java() {
        printf '%s\n' \
          '    java.specification.version = 21' \
          '    java.version = 21.0.11' \
          '    os.arch = aarch64' >&2
      }
      uname() { printf '%s\n' arm64; }
      hideout_require_native_java
    )
  )" || {
    printf 'java-toolchain-self-test: native fixture was rejected\n' >&2
    return 1
  }
  case "$output" in
    *'hostArch=arm64 javaArch=aarch64 specification=21'*'native=true'*) ;;
    *)
      printf 'java-toolchain-self-test: native identity was not retained\n' >&2
      return 1
      ;;
  esac

  if output="$(
    (
      java() {
        printf '%s\n' \
          '    java.specification.version = 21' \
          '    java.version = 21.0.11' \
          '    os.arch = x86_64' >&2
      }
      uname() { printf '%s\n' arm64; }
      hideout_require_native_java
    ) 2>&1
  )"; then
    printf 'java-toolchain-self-test: translated fixture was accepted\n' >&2
    return 1
  fi
  case "$output" in
    *'translated JVM refused: host=arm64 java=x86_64'*) ;;
    *)
      printf 'java-toolchain-self-test: translated diagnostic was lost\n' >&2
      return 1
      ;;
  esac

  if output="$(
    (
      java() {
        printf '%s\n' \
          '    java.specification.version = 22' \
          '    java.version = 22.0.2' \
          '    os.arch = aarch64' >&2
      }
      uname() { printf '%s\n' arm64; }
      hideout_require_native_java
    ) 2>&1
  )"; then
    printf 'java-toolchain-self-test: wrong Java version was accepted\n' >&2
    return 1
  fi
  case "$output" in
    *'Java specification=22, want=21'*) ;;
    *)
      printf 'java-toolchain-self-test: version diagnostic was lost\n' >&2
      return 1
      ;;
  esac

  printf 'java-toolchain-self-test: passed\n'
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  set -euo pipefail
  case "${1:-}" in
    '')
      hideout_require_native_java
      ;;
    --self-test)
      [ "$#" -eq 1 ] || {
        printf 'Usage: scripts/lib/java-toolchain.sh [--self-test]\n' >&2
        exit 2
      }
      hideout_java_toolchain_self_test
      ;;
    *)
      printf 'Usage: scripts/lib/java-toolchain.sh [--self-test]\n' >&2
      exit 2
      ;;
  esac
fi
