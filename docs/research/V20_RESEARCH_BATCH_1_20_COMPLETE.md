# V20 research completion: prompt entries 1–20

**New source-reviewed distinct canonical upstreams in this batch: 3.** The count includes only repositories for which an implementation source and exact license file were inspected. For Loglizer, a benchmark script was also inspected. No mirrors, docs-only reviews, repo landing pages alone, or unavailable sources are counted. Repository activity was not independently verified for these three, so no maintenance inference is made. This remains a focused source review, not a full dependency/security/legal audit.

## Newly reviewed distinct upstreams

| Prompt entry / canonical repo | Exact primary files inspected | Observed architecture/data flow | Transferable NLI concept; behavior limits | License/security and confidence |
|---|---|---|---|---|
| 5. [`logpai/loglizer`](https://github.com/logpai/loglizer) | [`loglizer/models/PCA.py`](https://github.com/logpai/loglizer/blob/master/loglizer/models/PCA.py), [`loglizer/dataloader.py`](https://github.com/logpai/loglizer/blob/master/loglizer/dataloader.py), [`benchmarks/HDFS_bechmark.py`](https://github.com/logpai/loglizer/blob/master/benchmarks/HDFS_bechmark.py), [`LICENSE`](https://raw.githubusercontent.com/logpai/loglizer/master/LICENSE) | Labeled/structured log data loader feeds feature extraction and anomaly models. README explicitly says own logs require rewriting the loader. Benchmark script evaluates anomaly models on HDFS; it is not a parser benchmark and its README numbers were not reproduced. | Keep anomaly detection downstream of parsing, and make dataset adapters explicit. This source set does not establish unknown-format parsing, multiline behavior, or schema drift handling. | Exact MIT license inspected. Python/scikit-learn and other requirements were not audited. Confidence: medium for these narrow observations. |
| 6. [`logpai/deep-loglizer`](https://github.com/logpai/deep-loglizer) | [`deeploglizer/models/lstm.py`](https://raw.githubusercontent.com/logpai/deep-loglizer/main/deeploglizer/models/lstm.py), [`LICENSE`](https://raw.githubusercontent.com/logpai/deep-loglizer/main/LICENSE), repo file listing/README (no test directory listed). | PyTorch sequence model embeds event features, feeds a configurable unidirectional/bidirectional LSTM, optionally attends over fixed windows, and predicts next-log or anomaly labels. | Event-sequence anomaly modeling is a separate downstream concern from parser correctness. This is not a parser; no claims about malformed raw records or drift recovery. | Apache-2.0 file inspected. Requirements/dependency licenses and dataset terms were not audited. No tests were located in the repo’s top-level inventory, so no test code is counted. Confidence: medium. |
| 11. [`SRT-Lab/ULP`](https://github.com/SRT-Lab/ULP) | [`ULP.py`](https://github.com/SRT-Lab/ULP/blob/main/ULP.py), [`LICENSE`](https://github.com/SRT-Lab/ULP/blob/main/LICENSE), README and repository file listing. | Python batch parser groups messages then applies frequency analysis to separate invariant/template text from variable tokens. README describes benchmark inputs and evaluator; claimed aggregate speed/accuracy numbers were not independently reproduced. | Candidate template-discovery baseline only; preserve the raw record and separately evaluate field extraction/typing. No inspected evidence for multiline framing, parser drift management, or held-out promotion. | GitHub repo metadata identifies MIT and exact LICENSE file was opened; `regex`, pandas, numpy dependencies were not audited. Benchmark/test claims are not reproduced. Confidence: medium-low. |

## Status of all entries 1–20

| # | Candidate | Disposition in this completion |
|---:|---|---|
| 1 | `logpai/logparser` | Already source-reviewed in ledger R02; not counted again. |
| 2 | `logpai/Drain3` | Already source-reviewed in ledger R01; not counted again. |
| 3 | `logpai/loghub` | Dataset-only upstream. README and dataset license reviewed; no implementation/test source. Dataset use requires research/academic use, citation, and retaining its notice. Excluded from source-reviewed count. |
| 4 | `logpai/loghub-2.0` | Dataset/benchmark corpus. README and dataset license reviewed; its benchmark scripts bundle many other projects, so they are not counted as independent upstream reviews. Excluded from source-reviewed count. |
| 5 | `logpai/loglizer` | Newly reviewed; counted. |
| 6 | `logpai/deep-loglizer` | Newly reviewed; counted. |
| 7 | `logpai/AutoLog` | Repo README and MIT metadata show source under `/src`, but exact implementation/test/license text was not successfully opened. Not counted; no implementation claim. |
| 8 | `logpai/LILAC` | README describes LLM in-context parsing plus adaptive cache and explicitly requires an OpenAI API key. Exact implementation source/license was unavailable (LICENSE path returned 404); not counted. This external-key requirement is a privacy/security review item, not a claim about runtime data handling. |
| 9 | `BlueLionLogram/Logram` | Landing page reachable, but exact implementation/test/license file could not be opened. Not counted. |
| 10 | Prompt `SalmaMessaoudi/MoLFI` | Prompt URL is not canonical; canonical repository found at [`SNTSVV/MoLFI`](https://github.com/SNTSVV/MoLFI). README/source page were inspected, but exact test and LICENSE files could not be verified in this pass. Not counted. |
| 11 | `SRT-Lab/ULP` | Newly reviewed; counted. |
| 12 | `gaiusyu/Brain` | Source-reviewed in prior batch report `V20_RESEARCH_BATCH_12_40.md`; not counted again. |
| 13 | `nulog/nulog` | Source-reviewed in prior batch report; not counted again. README says copied benchmark algorithms/data from logpai/logparser, so those copies are not treated as new upstreams. |
| 14 | `IBM/Drain3` | Duplicate/canonical alias of `logpai/Drain3` (ledger R01); not counted. |
| 15 | `IBM/LogAnalysis` | Direct official source unavailable in prior check; no canonical source verified. Excluded. |
| 16 | `IBM/LogAnalysis-2` | Direct official source unavailable in prior check; no canonical source verified. Excluded. |
| 17 | `elastic/logstash` | Source-reviewed in prior batch report; not counted again. |
| 18 | `elastic/beats` | Source-reviewed in prior batch report; not counted again. |
| 19 | `elastic/go-grok` | Already source-reviewed in ledger R07; not counted again. |
| 20 | `elastic/elasticsearch` | Source-reviewed in prior batch report; not counted again. |

## Notes on evidence and exclusions

For LogHub and LogHub-2.0, the inspected license files grant dataset access for research/academic use with citation and notice conditions; they are not ordinary software licenses. Loghub-2.0 README reports very large resource/time requirements for the full multi-parser benchmark, but no benchmark was run here. For AutoLog, LILAC, Logram and MoLFI, landing-page claims are not upgraded to source findings. Exact missing/unavailable files are reasons for exclusion from the count, not evidence that the project is dead or invalid.

Overall, the three counted repos inform downstream anomaly modeling and template-discovery comparisons. None supplies evidence for EGPE approval, held-out isolation, production parser promotion, or safe rollback. Those remain NLI design requirements to implement and verify independently.
