# LANCE detection rules (Sigma / host · log · SIEM)

> 中文说明：本目录是 LANCE 检测集的主机·日志·SIEM 层，把 [防御与检测指南(docs/defense-zh.md)](../../docs/defense-zh.md)
> 第 4 节「检测规则」里的伪规则转换成可直接部署的 [Sigma](https://sigmahq.io) 规则。网络层由
> [`../suricata/`](../suricata/) 负责。所有规则仅限用于保护自己拥有或获得书面许可的系统，
> **仅限防御与检测目的**。整体检测包概要先看 [`../README.md`](../README.md)。

The host, log, and SIEM layer of the LANCE detection set. These [Sigma](https://sigmahq.io) rules
formalize the pseudo-rules in the defense guide ([中文](../../docs/defense-zh.md) ·
[English](../../docs/defense-en.md), section 4) into a vendor-neutral format you convert to your own
SIEM or EDR query language. Every indicator is grounded in a string or behaviour verified in this
repository's source, not inferred. The network layer lives under [`../suricata/`](../suricata/); the
full detection set — the ATT&CK coverage layer, the indicator CSV / MISP export, and the host-triage
script — is indexed in [`../README.md`](../README.md).

## Atomic rules

One rule, one observable fact. Convert them individually or as part of the whole tree.

- **[`lance_enrich_user_agent.yml`](lance_enrich_user_agent.yml)** — *LANCE Asset Enrichment Probe
  User-Agent*. Inbound `lance-enrich/1.0` User-Agent from asset enrichment (`enrich/enrich.go`).
  Target-side, supporting indicator. `level: high`.
- **[`lance_selfupdate_egress.yml`](lance_selfupdate_egress.yml)** — *LANCE Self-Update Egress
  User-Agent*. Outbound `lance-selfupdate` User-Agent from the self-update routine
  (`selfupdate/github.go`). Host/forensic egress indicator. `level: medium`.
- **[`lance_guard_audit_framing.yml`](lance_guard_audit_framing.yml)** — *LANCE Platform Guard
  Audit-Log Framing*. The platform-guard control marker written to the audit log on a blocked tool
  call (`guard/guard.go`). Host/forensic indicator. `level: high`.
- **[`lance_recording_proxy_ca.yml`](lance_recording_proxy_ca.yml)** — *LANCE Recording-Proxy MITM CA
  Certificate Artifact*. Creation of the recording proxy's MITM CA file under the
  `_ca/mitmproxy-ca-cert.pem` layout (`traffic/traffic.go`). Host/forensic artifact; the bare filename
  is shared with standalone mitmproxy, so it is a hunting lead. `level: medium`.
- **[`destructive_command_hunting.yml`](destructive_command_hunting.yml)** — *Destructive Command
  Execution (LANCE Guard-List Hunting)*. Destructive shell/DB commands mirroring the LANCE guard's
  built-in deny list (`db/db.go` seed). Generic hunting lead, **not** an LANCE signature. `level: medium`.

## Correlation rules (behaviour) — [`correlation/`](correlation/)

Static strings can be changed; behaviour is harder to hide. These Sigma **correlation** rules encode the
behaviour-based layer of the defense guide (sections 4.1–4.2 and 4.4). Each references an atomic rule
above by its `id`, so **convert the whole `sigma/` tree, not a single correlation file**, or the
reference will not resolve (the [Sigma test](../tests/sigma/) asserts exactly this dependency).

- **[`correlation/lance_enrich_scan_velocity.yml`](correlation/lance_enrich_scan_velocity.yml)** —
  *Enrichment Scan Velocity*. A burst of `lance-enrich/1.0` probes from one source in a short window
  (enrichment runs at concurrency 4 with no rate limit) — the velocity the single-request rule misses.
  `event_count`, `level: high`.
- **[`correlation/lance_enrich_fanout.yml`](correlation/lance_enrich_fanout.yml)** — *Enrichment
  Fan-Out*. One source carrying the enrichment User-Agent to many *distinct* hosts: machine-speed breadth
  across an asset list, where the distinct-host count, not request volume, is the tell. `value_count`,
  `level: high`.
- **[`correlation/lance_guard_block_burst.yml`](correlation/lance_guard_block_burst.yml)** —
  *Guard-Block Burst*. Repeated platform-guard control markers on one host — an actively engaged LANCE
  run tripping its own guard, not a document that merely quotes the marker. `event_count`, `level: high`.
- **[`correlation/lance_guard_marker_then_destructive.yml`](correlation/lance_guard_marker_then_destructive.yml)**
  — *Guard Marker With Destructive Command*. The guard marker and a destructive command co-occurring on
  one host within a window (defense guide §4.2, multi-stage): combining an LANCE-specific marker with the
  otherwise-generic destructive-command signal raises specificity. `temporal`, `level: high`.

Thresholds and windows are conservative defaults — tune them to your baseline. The pure web multi-stage
case (enumerate → probe → authenticate) still needs base rules specific to your environment, because that
pattern does not reduce to a single LANCE-unique User-Agent; a generic behavioural base template to start
from is in the [defense guide §4.2](../../docs/defense-en.md), kept out of this tested tree because it
cannot be grounded in LANCE source.

## Scope and honesty — read before deploying

- **Static indicators can be changed.** An operator can set a different User-Agent or clean up the CA
  file, so the absence of an atomic indicator does **not** mean safety. The durable signal is the
  behaviour the `correlation/` rules key on — one source chaining recon → enumeration → probing →
  auth/injection attempts, adapting to responses, running without pause.
- **The destructive-command rule is generic hunting.** It mirrors LANCE's guard deny list, but the same
  commands are run by legitimate administrators. Treat a hit as a lead, allow-list your environment, and
  do not attribute it to LANCE on its own.
- **Ports and schema are host-forensic, not Sigma.** The server default `:8787` and recording proxy
  `127.0.0.1:8788` (`cmd/lance/main.go`), and the PostgreSQL exploration-graph schema, are best checked on
  a suspected host, so they ship in the [indicator CSV](../indicators/) and the
  [host-triage script](../triage/) rather than as noisy rules.
- **`logsource` and field names are generic.** The rules use generic `category`/`product` log sources and
  field names (`cs-user-agent`, `CommandLine`, `TargetFilename`). Map them to your product's schema with a
  pipeline (`-p`) at convert time; see the backend notes below.

## Validate and convert

Validated with [sigma-cli](https://github.com/SigmaHQ/sigma-cli) (pySigma). From the repository root:

```sh
python3 -m venv .venv && . .venv/bin/activate
pip install sigma-cli

# structural + best-practice validation (expect: 0 errors, 0 issues)
sigma check detections/sigma/

# full SigmaHQ convention set with this rule set's documented baseline (expect: 0 issues)
pip install pySigma-validators-sigmahq
sigma check --validation-config detections/tests/sigma_lint/validators.yml detections/sigma/

# compile the WHOLE tree so the correlation rules resolve the atomic rules they reference by id
sigma plugin install splunk
sigma convert -t splunk --without-pipeline detections/sigma/
```

Backends vary in correlation support, so the `-t` choice matters: Splunk, Elasticsearch EQL, and Grafana
Loki convert the whole tree, while Elasticsearch Lucene, OpenSearch, and the Microsoft `kusto` backend
convert the five atomic rules only (express the window natively in the product). The measured per-backend
matrix and the `--without-pipeline` / `-p` field-mapping notes are in [`../README.md`](../README.md), and
they are reproduced by [`../tests/sigma_backends/`](../tests/sigma_backends/).

## Tests

Four reproducible suites under [`../tests/`](../tests/) cover these rules, each needing only Docker:
[`sigma/`](../tests/sigma/) (validation, whole-tree compilation, and that a correlation rule fails to
convert alone), [`sigma_match/`](../tests/sigma_match/) (the rules actually fire on malicious samples and
stay quiet on benign ones), [`sigma_backends/`](../tests/sigma_backends/) (portability across five
backends), and [`sigma_lint/`](../tests/sigma_lint/) (the full SigmaHQ validator baseline, 0 issues). See
[`../tests/README.md`](../tests/README.md).

## Contributing

Detection contributions are welcome. New rules should keep every indicator grounded in an observable fact,
state limitations in the `description`, pass the SigmaHQ validator baseline cleanly
(`sigma check --validation-config ../tests/sigma_lint/validators.yml .`), and avoid any content that reads
as attack guidance. See [`../../README.md`](../../README.md) and the network layer in
[`../suricata/`](../suricata/) / [`../README.md`](../README.md).
