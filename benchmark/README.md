## Benchmark (Model Quality / Ground Truth)

[Click here for the results of pretraining 16 language models on different tokenizers](pretrain.md) including TokenMonster, GPT-2 & tiktoken p50k_base.

## Benchmark Performance (Tokenization)

[Click here for an interactive benchmark](https://bot.co/tokenmonster/benchmark.html) for all pretrained vocabularies, plus LLaMa Tokenizer, GPT2 Tokenizer, and tiktoken.

[Click here for a chart comparing optimization mode, chr/tok and vocab size](https://bot.co/tokenmonster/line.html) for all TokenMonster vocabularies.

## Benchmark Performance (Speed)

### Single threaded performance

|              | Range                |
|--------------|-----------------------|
| TokenMonster | 5.0 - 13 MB / second    |
| tiktoken     | 4.0 - 7.0 MB / second     |
| Hugging Face | 0.2 - 0.5 MB / second |

### TokenMonster vs. Hugging Face (LLaMa Tokenizer)

The LLaMa Tokenizer running on Hugging Face Transformers used `35210511` tokens to tokenize "instruct" and `38383671` for "scifi", at an average speed of 0.29 MB/s. The TokenMonster import of LLaMa Tokenizer (with the exact same vocabulary), used `35083428` tokens for "instruct" and `38124152` for scifi (0.5% less tokens) and ran at an average speed of 12.5 MB/s.

### TokenMonster vs. tiktoken (GPT2 Tokenizer)

Comparing to [tiktoken's own benchmarks](https://github.com/openai/tiktoken#performance), they are claiming 6.2 MB/s with GPT2 Tokenizer. I got the same performance for GPT2 on tiktoken. TokenMonsters' import of GPT2 Tokenizer performed at 13.3 MB/s on "instruct" and 11.3 MB/s on "the_pile".

<img src="https://github.com/alasdairforsythe/tokenmonster/assets/77910352/d3814067-75f4-4787-8367-7c0b094470ef" alt="chart" width="750" />

### Notes on performance

The Python TokenMonster implementation calls the Go implementation for tokenization, so they are the same speed, but Python has overhead due to serialization and deserialization. The real-world performance of TokenMonster trained vocabularies, i.e. not `gpt2` or `llama` (which are simpler vocabularies) is closer to the 5 - 7 MB/s range, which is comparable with the tiktoken performance. It's worth mentioning that tiktoken is tokenizing greedily whilst TokenMonster achieves comparable or better speed whilst calculating up to 6 branches at any point in time.

## Benchmark Datasets

For a fair test, the benchmarks were performed on datasets that the TokenMonster vocabularies had not previously seen.

`the_pile` is the test dataset from [The Pile](https://the-eye.eu/public/AI/pile/), with the text extracting using [extract_text_from_jsonl_parquet.py](/training). Size is 1.3 GB.

`github` is [this random file](https://data.together.xyz/redpajama-data-1T/v1.0.0/github/filtered_a777da5620f1467f8df3616b17d533dc.sampled.jsonl) (1.7 GB direct download) from [urls.txt](https://data.together.xyz/redpajama-data-1T/v1.0.0/urls.txt) from [Red Pajama](https://huggingface.co/datasets/togethercomputer/RedPajama-Data-1T). It was also extracted using [extract_text_from_jsonl_parquet.py](/training). It represents code. Extracted size is 1.5 GB.

`instruct` is a bunch of chat & instruct finetunes from WizardLM's [alpaca_evol_instruct_70k.json](https://huggingface.co/datasets/WizardLM/evol_instruct_70k/tree/main). This was used as-is and respresents chatbot conversational text. Size is 137 MB.

`scifi` is [Scifi Stories Text Corpus](https://www.kaggle.com/datasets/jannesklaas/scifi-stories-text-corpus). I thought sci-fi would be a good test for the fiction tokenizing capability because the training datasets don't contain much sci-fi. Size is 149 MB.

`github` and `the_pile` were further process with `onlyvalidlatin.go` to remove any invalid UTF-8 and non-Latin characters (e.g. Chinese). I made this decision because all of the pretrained vocabularies were trained with `-only-latin` and `-only-valid` parameters, hence they must use single byte tokens to tokenize any non-Latin characters. Because `github` and `the_pile` contained a lot of non-Latin script, whilst `scifi` and `instruct` did not, this would otherwise skew the benchmarks.

.
