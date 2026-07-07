#!/usr/bin/env bash
#
# sysml2json.sh — transform SysML v2 textual notation (.sysml) into the SysML v2
# API JSON that sysgo consumes, using the OMG SysML v2 Pilot Implementation's
# canonical SysML2JSON serializer (org.omg.sysml.xtext.util.SysML2JSON).
#
# This is real SysML tooling: it parses the textual notation against the
# standard libraries and emits the {payload,...} element envelope. sysgo then
# generates Go from that JSON.
#
# Usage:
#   scripts/sysml2json.sh <input.sysml> <output.json>
#
# Requirements: Java 17+ and curl. The pilot kernel distribution (which bundles
# the serializer jar and the standard libraries) is downloaded once and cached
# under ${SYSML_CACHE:-$HOME/.cache/sysml}.
set -euo pipefail

PILOT_RELEASE="${PILOT_RELEASE:-2026-04}"
KERNEL_VERSION="${KERNEL_VERSION:-0.59.0}"
CACHE="${SYSML_CACHE:-$HOME/.cache/sysml}"
KERNEL_DIR="$CACHE/jupyter-sysml-kernel-$KERNEL_VERSION"
JAR="$KERNEL_DIR/sysml/jupyter-sysml-kernel-$KERNEL_VERSION-all.jar"
LIB="$KERNEL_DIR/sysml/sysml.library"

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <input.sysml> <output.json>" >&2
  exit 2
fi
INPUT="$1"
OUTPUT="$2"

if [[ ! -f "$JAR" ]]; then
  echo "Downloading SysML v2 Pilot kernel $KERNEL_VERSION ($PILOT_RELEASE)..." >&2
  mkdir -p "$CACHE"
  url="https://github.com/Systems-Modeling/SysML-v2-Pilot-Implementation/releases/download/$PILOT_RELEASE/jupyter-sysml-kernel-$KERNEL_VERSION.zip"
  tmpzip="$(mktemp)"
  curl -sSL -o "$tmpzip" "$url"
  rm -rf "$KERNEL_DIR"
  mkdir -p "$KERNEL_DIR"
  unzip -q "$tmpzip" -d "$KERNEL_DIR"
  rm -f "$tmpzip"
fi

# Bundle the model with the scalar value library so external scalar types
# (Real/Integer/String/Boolean) are emitted as named elements rather than
# anonymous proxies. Add more library files here if your model uses them.
# The working path must contain NO '.', because the serializer derives the
# output file name by truncating the input path at its first '.'.
base="$(mktemp -d "${TMPDIR:-/tmp}/sysml2jsonXXXXXX")"
trap 'rm -rf "$base"' EXIT
work="$base/model"
mkdir -p "$work"
cp "$INPUT" "$work/"
cp "$LIB/Kernel Libraries/Kernel Data Type Library/ScalarValues.kerml" "$work/"

echo "Running SysML2JSON on $INPUT..." >&2
java -cp "$JAR" org.omg.sysml.xtext.util.SysML2JSON -l "$LIB" "$work" >/dev/null

# The serializer writes <dir>.json next to the input directory.
raw="$base/model.json"
mkdir -p "$(dirname "$OUTPUT")"
if command -v python3 >/dev/null 2>&1; then
  python3 -m json.tool "$raw" > "$OUTPUT"
else
  cp "$raw" "$OUTPUT"
fi
rm -f "$raw"
echo "Wrote $OUTPUT" >&2
