# Prebuilt test image for all eight detection suites and the three gates.
#
# Why this image exists. Every suite's run.sh installs its Python dependency inside a
# throwaway `python:3.12-slim` container on each invocation. Two consequences:
#
#   1. speed      pip resolves sigma-cli plus four backend plugins per suite, 2-4 min
#                 each, eight times over.
#   2. correctness. `sigma check` runs pySigma's tag validators, and two of them fetch
#                 their reference data over the network on first use:
#                   ATTACKTagValidator -> enterprise-attack.json (~55 MB, MITRE ATT&CK STIX)
#                   D3FENDTagValidator -> d3fend.json (~4.8 MB, MITRE D3FEND ontology)
#                 Both cache under ~/.cache/pysigma/<set> with no expiry. Where that
#                 download stalls (CN links, offline runners) `sigma check` blocks with
#                 no output and check-defense-template.sh looks hung while asserting
#                 nothing. Baking the caches makes the gate hermetic: it passes with
#                 --network none.
#
# Build (fetch-refs.sh pulls the two JSON files first, through a mirror if needed):
#
#   cd detections/tests/harness-image
#   ./fetch-refs.sh
#   docker build -t lance-detections:latest .
#
# Use it for the whole harness:
#
#   PYTHON_IMAGE=lance-detections:latest detections/tests/run-all.sh
#
# The reference data is MITRE's (CC BY-SA 4.0 / Apache-2.0 upstream); the JSON files are
# build inputs only and are git-ignored (see .gitignore here).
ARG PYTHON_IMAGE=python:3.12-slim
FROM ${PYTHON_IMAGE}

# Versions mirror the defaults the suites pin, so a run against this image and a run
# against python:3.12-slim test the same software.
ARG SIGMA_CLI_VERSION=3.1.0
ARG SIGMAHQ_VALIDATORS_VERSION=0.21.0
ARG PYMISP_VERSION=2.5.34.4

ENV PIP_DISABLE_PIP_VERSION_CHECK=1 PIP_NO_CACHE_DIR=1

# sigma/sigma_backends need sigma-cli + the four backends; sigma_lint needs the SigmaHQ
# validators; sigma_match needs pysigma (pulled in by sigma-cli); misp needs pymisp;
# suricata's capture generator needs scapy.
RUN pip install --quiet \
      "sigma-cli==${SIGMA_CLI_VERSION}" \
      "pySigma-validators-sigmahq==${SIGMAHQ_VALIDATORS_VERSION}" \
      "pymisp==${PYMISP_VERSION}" \
      scapy \
 && for plugin in splunk elasticsearch loki kusto; do \
      sigma plugin install "$plugin" >/dev/null 2>&1 \
        || { echo "plugin install $plugin failed" >&2; exit 1; }; \
    done \
 && for backend in splunk eql loki kusto; do \
      sigma list targets | grep -qw "$backend" \
        || { echo "backend target $backend missing after plugin install" >&2; exit 1; }; \
    done \
 && sigma version

COPY enterprise-attack.json d3fend.json /tmp/ref/
RUN python3 -c "from sigma.data.mitre_attack import set_url as au, _load_mitre_attack_data as A; \
from sigma.data.mitre_d3fend import set_url as du, _load_mitre_d3fend_data as D; \
au('/tmp/ref/enterprise-attack.json'); a = A(); \
du('/tmp/ref/d3fend.json'); d = D(); \
print('cached ATT&CK', a['mitre_attack_version'], len(a['mitre_attack_techniques']), 'techniques;', \
      'D3FEND', d.get('mitre_d3fend_version', '?'), len(d.get('mitre_d3fend_tactics', {})), 'tactics')" \
 && rm -rf /tmp/ref
