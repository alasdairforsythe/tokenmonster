## Benchmarks

This page consists of the benchmarks from the previous version of TokenMonster. The new version is 10x faster. I will update this page with the new benchmarks when the vocabularies have finished being trained.

## Old Benchmarks

The following tables show the number of tokens it took to tokenize each test dataset (fewer is better) with both tiktoken and then TokenMonster. The percentage is performance relative to tiktoken (higher is better). All the benchmarking scripts are available in the files, and links to the datasets are below.

#### Vocab Size 100256
|                                     | the_pile       | github         | evov_instruct | total          |
|-------------------------------------|----------------|----------------|---------------|----------------|
| tiktoken cl100k_base                | 320446256 +0%  | 371531983 +0%  | 30131982 +0%  | 722110221 +0%  |
| tokenmonster english-100256-capcode | **243860445 +32%** | 267760547 +39% | **22061974 +37%** | 533682966 +35% |
| tokenmonster english-100256         | 246129464 +30% | 269909578 +38% | 22354408 +35% | 538393450 +34% |
| tokenmonster code-100256-capcode    | 291714415 +10% | **241735648 +54%** | 24771080 +22% | 558221143 +29% |
| tokenmonster code-100256            | 295035719 +9%  | 242439606 +53% | 25086094 +20% | 562561419 +28% |
| tokenmonster english-32000-capcode  | 289148386 +11% | 314766168 +18% | 26286333 +15% | 630200887 +15% |
| tokenmonster english-24000-capcode  | 302203947 +6%  | 330848326 +12% | 27628200 +9%  | 660680473 +9%  |

Note that tokenmonster 24000-capcode vocabulary tokenizes better than tiktoken's 100256 vocabulary.

#### Vocab Size 50256

|                                     | the_pile       | github         | evov_instruct | total          |
|-------------------------------------|----------------|----------------|---------------|----------------|
| tiktoken p50k_base                  | 347805446 +0%  | 487665377 +0%  | 32464655 +0%  | 867935478 +0%  |
| tokenmonster english-50256-capcode  | **269479690 +29%** | 294455227 +66% | **24442600 +33%** | 588377517 +48% |
| tokenmonster english-50256          |                |                |               |                |
| tokenmonster code-50256-capcode     |                |                |               |                |
| tokenmonster code-50256             | 327110612 +6%  | **267733120 +82%** | 28236624 +15% | 623080356 +39% |

This table is not yet complete because some of the vocabularies are still being trained.

### Single-Threaded Speed

This table gives some rough figures for average speed of the "tokenization" as returned by the benchmarking scripts. Note that "detokenization" is very straightforward and is close to instant for every implementation. Tiktoken wins on speed of tokenization. This is partly due to it being a greedy algorithm, and partly due to their Python library being written in Rust. My Go implementation performs around 1/3 of the speed of tiktoken, and my Python implementation is around 1/3 again as it's written in native Python. That's not a major concern as tokenization time is not a bottleneck in any use case I know of. However, for the sake of optimization, I intend to write a C++ implementation and export a Python package on that, which I expect will bring it up to around 2.5 MB / second. TokenMonster will never be quite as fast as tiktoken because I'm using an ungreedy algorithm, which is always considering at least 2 branches and therefore is always going to have to do twice as many computations.

|                                     | average           |
|-------------------------------------|-------------------|
| tiktoken p50k_base                  | 6.2 MB / second   |
| tiktoken cl100k_base                | 4.8 MB / second   |
| tokenmonster (Go)                   | 1.7 MB / second   |
| tokenmonster (Python)               | 0.5 MB / second   |

## Test Datasets

For a fair test, the benchmarks were performed on datasets that the TokenMonster vocabularies had not previously seen.

`the_pile` is the test dataset from [The Pile](https://the-eye.eu/public/AI/pile/), with the text extracting using [extract_text_from_jsonl_parquet.py](/training). It represents general text. Extracted size is 1,526 MB.

`github` is [this random file](https://data.together.xyz/redpajama-data-1T/v1.0.0/github/filtered_a777da5620f1467f8df3616b17d533dc.sampled.jsonl) (1.7 GB direct download) from [urls.txt](https://data.together.xyz/redpajama-data-1T/v1.0.0/urls.txt) from [Red Pajama](https://huggingface.co/datasets/togethercomputer/RedPajama-Data-1T). It was also extracted using [extract_text_from_jsonl_parquet.py](/training). It represents code. Extracted size is 1,313 MB.

`evov_instruct` is a bunch of chat & instruct finetunes from WizardLM's [alpaca_evol_instruct_70k.json](https://huggingface.co/datasets/WizardLM/evol_instruct_70k/tree/main). This was used as-is and respresents chatbot conversational text. Extracted size is 137 MB.
