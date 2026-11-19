#!/usr/bin/env bash
#
# Download the two MITRE reference files the image build bakes into the cache.
#
# Both URLs are slow or unreachable from CN networks when hit directly, so both can be
# overridden. ATTACK_URL accepts a mirror prefix-wrapped URL (the gh-proxy style), the
# default here already is one; D3FEND_URL only has the vendor URL, which needs a proxy.
#
# Usage:
#   ./fetch-refs.sh                                  # gh-proxy mirror + direct d3fend
#   HTTPS_PROXY=http://127.0.0.1:7897 ./fetch-refs.sh
#   ATTACK_URL=https://…/enterprise-attack.json D3FEND_URL=https://…/d3fend.json ./fetch-refs.sh
#
# Idempotent: an existing file that already parses as JSON is left alone.
set -euo pipefail

cd "$(dirname "$0")"

ATTACK_URL="${ATTACK_URL:-https://gh-proxy.com/https://raw.githubusercontent.com/mitre-attack/attack-stix-data/master/enterprise-attack/enterprise-attack.json}"
D3FEND_URL="${D3FEND_URL:-https://d3fend.mitre.org/ontologies/d3fend.json}"

fetch() {
  local url="$1" out="$2" min_bytes="$3"
  if [ -f "$out" ] && python3 -c "import json,sys; json.load(open(sys.argv[1]))" "$out" 2>/dev/null; then
    echo "ok    $out already present ($(wc -c <"$out" | tr -d ' ') bytes)"
    return
  fi
  echo "get   $out  <- $url"
  curl -fsSL --retry 3 --retry-delay 2 --max-time 600 -o "$out.part" "$url"
  local size
  size="$(wc -c <"$out.part" | tr -d ' ')"
  if [ "$size" -lt "$min_bytes" ]; then
    echo "FAIL  $out: got $size bytes, expected at least $min_bytes — partial download" >&2
    rm -f "$out.part"
    exit 1
  fi
  python3 -c "import json,sys; json.load(open(sys.argv[1]))" "$out.part"
  mv "$out.part" "$out"
  echo "ok    $out ($size bytes, valid JSON)"
}

fetch "$ATTACK_URL" enterprise-attack.json 50000000
fetch "$D3FEND_URL" d3fend.json 3000000

echo "done — now: docker build -t lance-sigma:3.1.0 ."
