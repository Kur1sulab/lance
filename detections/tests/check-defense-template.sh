#!/usr/bin/env bash
#
# Documentation-claim consistency gate for the inline Sigma correlation template
# in the defense guide (see check-defense-template.py for the assertions). It
# proves the four present-tense facts docs/defense-{zh,en}.md state about their
# base template — `sigma check` passes with zero errors, the correlation converts
# on splunk/eql/loki, and lucene/kusto reject it with "Backend does not support
# correlation rules" — and that the two languages carry a byte-identical Sigma rule
# body. The template lives as prose outside detections/sigma/ on purpose (it is a
# generic behavioural rule, not grounded in LANCE source), so no suite re-ran those
# claims; an upstream sigma-cli or backend change could make the prose silently
# false. This gate closes that gap.
#
# It is a gate, not a suite: run-all.sh runs it alongside the harness-sync and
# triage self-test gates, before the suite loop, and it does not appear in the
# per-suite summary. It is not a SUITES entry, a `.../run.sh` CI step, or a run.sh
# directory, so the eight detection suites stay eight and check-harness-sync never
# counts it.
#
# No host dependency beyond Docker: the pinned sigma-cli and its four backends are
# installed inside the container, the two defense guides are mounted read-only, and
# the extracted YAML is written only under the container's /tmp — nothing is
# installed on the host and nothing is written to the repo tree. The version and
# image overrides are inherited from the environment, matching the detection
# suites (PYTHON_IMAGE, SIGMA_CLI_VERSION).
#
# Network note. `sigma check` runs pySigma's ATTACK and D3FEND tag validators, which
# download their reference data on first use (~55 MB STIX bundle + ~4.8 MB ontology)
# and cache it under ~/.cache/pysigma. Where that download stalls — the normal case on
# CN links and offline runners — the check blocks with no output and this gate looks
# hung. Build detections/tests/harness-image/ once, which bakes both caches, and run with
# PYTHON_IMAGE=lance-detections:latest; the checker also bounds `sigma` at 300 s so the
# failure is reported as a named FAIL instead of a silent hang.
#
# Usage:   detections/tests/check-defense-template.sh
# Env:     PYTHON_IMAGE      (default python:3.12-slim; use lance-detections:latest offline)
#          SIGMA_CLI_VERSION (default 3.1.0 — the pinned reference version)
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../.." && pwd)"
PYTHON_IMAGE="${PYTHON_IMAGE:-python:3.12-slim}"
SIGMA_CLI_VERSION="${SIGMA_CLI_VERSION:-3.1.0}"

# Install the pinned sigma-cli plus the same four backends the sigma_backends suite
# uses (elasticsearch ships both the lucene and eql targets), then run the checker.
# Skip the install when the image already provides them (detections/tests/harness-image/
# does), and never let a pip failure pass silently: under `set -e` that aborts with no
# output, which reads as a hung gate instead of a network problem.
docker run --rm \
  -e LANCE_DOCS_DIR=/docs \
  -e SIGMA_CLI_VERSION="$SIGMA_CLI_VERSION" \
  -v "$REPO/docs:/docs:ro" \
  -v "$HERE:/src:ro" \
  "$PYTHON_IMAGE" sh -ec '
    if ! command -v sigma >/dev/null 2>&1; then
      pip install --quiet --disable-pip-version-check "sigma-cli==${SIGMA_CLI_VERSION}" >/dev/null 2>&1 \
        || { echo "FAIL  pip install sigma-cli==${SIGMA_CLI_VERSION} failed — no network? use PYTHON_IMAGE=lance-detections:latest" >&2; exit 2; }
      for plugin in splunk elasticsearch loki kusto; do
        sigma plugin install "$plugin" >/dev/null 2>&1 \
          || { echo "FAIL  sigma plugin install $plugin failed" >&2; exit 2; }
      done
    fi
    exec python3 /src/check-defense-template.py
  '
