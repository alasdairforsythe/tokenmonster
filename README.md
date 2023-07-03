# TokenMonster

TokenMonster is a highly optimized, state-of-the-art tokenization library, enabling language models to run faster, cheaper, smarter and generate longer streams of text.

<img width="661" alt="tokenmonster" src="https://github.com/alasdairforsythe/tokenmonster/assets/77910352/1136330a-bf25-4a17-8edb-06b90fffb236">

TokenMonster is an ungreedy tokenizer and vocabulary generator, built from the ground up using custom data structures and branchless logic.

TokenMonster can train and generate an optimal vocabulary on a 1GB dataset within 24 hours on a typical desktop. 440 [prebuilt vocabularies](#prebuilt-vocabularies) are provided, as well as tools to train your own vocabularies & implementations in Go, Python & Javascript for tokenization and detokenization using the prebuilt or your own vocabularies.

You can [test TokenMonster in your browser here](https://bot.co/tokenmonster/), tokenizing live in native Javascript.

TokenMonster is a novel approach to tokenization with broad-ranging use potential, but its primary motivation is to increase the inference speed and context-length of large language models. By using a more optimal vocabulary and a better tokenization algorithm, text can be represented with 35% fewer tokens compared to other modern tokenizing methods, increasing the speed of inference, training and the length of text by 35%. The code-optimized tokenizers do even better, [see for yourself](https://bot.co/tokenmonster/).

## Features
- Outperforms other tokenization algorithms in every area ([benchmark](./benchmark))
- Selects the optimal vocabulary
- 5 optimization modes to choose from: `unfiltered`, `clean`, `balanced`, `consistent`, `strict`
- Ungreedy: follows up to 6 parallel branches at a time
- Fast: follows 6 branches faster than other algorithms can follow 1 ([benchmark](./benchmark))
- Supports UTF-8, UTF-16 and binary
- Successfully identifies words, subwords, common phrases and figures of speech by itself
- Works with HTML tags, sequential spaces, tabs, etc. without wasting context
- Can be trained on any language
- Reliably achieves over 7 chr/token (depending on vocabulary size & optimization mode)
- Vocabulary files can be modified and resized even after training
- Add, delete and edit existing vocabularies
- Full support for "special" and "single-byte" tokens
- Optional UNK token
- 420 prebuilt vocabularies ready for use

## Table of Contents

* Usage [Go](./go/) | [Python](./python/) | [Javascript](./javascript/) | [Training](./training/)
* [Benchmark](./benchmark)
* [Prebuilt Vocabularies](#prebuilt-vocabularies)
* [Optimization Modes](#optimization-modes)
* [Datasets](#datasets)
* [Capcode](#capcode)
* [Normalization](#normalization)
* [Which Vocabulary To Choose](#which-vocabulary-to-choose)
* [How does it work and how is it different from BPE?](#how-does-it-work-and-how-is-it-different-from-bpe)
* [The Ungreedy Tokenization Algorithm](#the-ungreedy-tokenization-algorithm)
* [Support & Consultation](#support--consultation)

## Prebuilt Vocabularies

440 vocabularies are planned or have already been built. Download them from [Hugging Face](https://huggingface.co/alasdairforsythe/tokenmonster).

Choose a dataset from:
`code` `english` `englishcode` `fiction`

Choose a vocab size from:
`1024` `2048` `4096` `8000` `16000` `24000` `32000` `40000` `50256` `65536` `100256`

Choose an [optimization mode](#optimization-modes) from:
`unfiltered` `clean` `balanced` `consistent` `strict`

For a capcode disabled vocabulary add:
`nocapcode`

And finally add the version number:
`v1`

Examples: `fiction-24000-consistent-v1` `code-4096-clean-nocapcode-v1`

## Optimization Modes

All the optimization modes are lossless. The stricter the optimization mode (higher number), the more tokens will be used to tokenize the same text, but it'll be much easier for the language model to learn because the grammar is simpler. Less strict (lower number), more text can be represented with fewer tokens, but the language model will have to learn a more complicated grammar.

`0 unfiltered` allows the training process to freely determine the tokens. `clean` is preferred in almost every case, because `unfiltered` tends to result in overfitting, especially for code as it results in tokens for things like `\n\t\t\t\tif (`. Use `unfiltered` for tokenizing language or data that does not use spaces as word boundaries.

`1 clean` introduces filters to avoid overitting. It forces the vocabulary to begin words with a space, and limits the way in which whitespace can be combined with other characters.

`2 balanced` prioritizes whole words and attempts to dissuade the vocabulary from doing things that are difficult to learn, such as using a delete forward marker at the end of a token.

`3 consistent` is a looser version of `strict`. It aims to limit the number of different tokens that can represent the same word or phrase, and doesn't allow for open-close delimeters to be combined with words. Numbers also become limited to fewer variants.

`4 strict` aims to have only 1 token per word, no matter how it is encoded. For example `However`, ` however,` and `HOWEVER!` will all use the same ` however` token, in combination with other tokens that indicate it's spacing and capitalization.

## Datasets

The datasets used for generating the prebuilt vocabularies are all available on [Hugging Face](https://huggingface.co/datasets/alasdairforsythe/text-english-code-fiction-nonfiction). The sources and scripts used to generate these datasets are included in the training directory.

The training data mostly came from Red Pajamas [1B Token Sample](https://huggingface.co/datasets/togethercomputer/RedPajama-Data-1T-Sample). However, to reduce formal English and emphasize other languages, informal writing and code, c4_sample & cc_sample were cropped to 100MB, and [Reddit conversations](https://huggingface.co/datasets/SophieTr/reddit_clean) data were added (also cropped to 100MB.)

Additionally, equally weighted code samples of 2MB per language (code_2mb) and 10MB per language (code_10mb) were added for 30 different programming languages to ensure all programming languages have representation. The source of this is [codeparrot/github-code](https://huggingface.co/datasets/codeparrot/github-code). To ensure a range of coding styles, I allowed only 1 file per GitHub repository, and per file a maximum of 200 lines selected from the middle of the file.

Given the evolving nature of writing styles, I felt that book_sample.txt, which consists of out-of-copyright books, was not a good representation of contemporary fiction. To better represent a more modern style, I curated fiction.txt and fiction_100mb.txt by throwing together a few other datasets and cleaning it up.

Note: fiction_100mb.txt is a subset of fiction.txt, and code_2mb.txt is a subset of code_10mb.txt.

#### english

| Filename                 | Filesize  |
|--------------------------|-----------|
| arxiv_sample.txt         | 88,925,569  |
| book_sample.txt          | 108,069,616 |
| c4_sample.txt            | 100,560,318 |
| cc_2023-06_sample.txt    | 100,852,231 |
| fiction_100mb.txt        | 94,235,489  |
| stackexchange_sample.txt | 71,940,138  |
| wikipedia_sample.txt     | 79,181,873  |
| reddit.txt               | 100,027,565 |
|                          | **743,792,799** |

#### englishcode

| Filename                 | Filesize  |
|--------------------------|-----------|
| arxiv_sample.txt         | 88,925,569  |
| book_sample.txt          | 108,069,616 |
| c4_sample.txt            | 100,560,318 |
| cc_2023-06_sample.txt    | 100,852,231 |
| code_2mb.txt             | 62,895,904  |
| fiction_100mb.txt        | 94,235,489  |
| github_sample.txt        | 191,123,094 |
| stackexchange_sample.txt | 71,940,138  |
| wikipedia_sample.txt     | 79,181,873  |
| reddit.txt               | 100,027,565 |
|                          | **997,811,797** |

#### fiction

| Filename                 | Filesize  |
|--------------------------|-----------|
| book_sample.txt          | 108,069,616 |
| fiction.txt              | 357,119,086  |
| reddit.txt               | 100,027,565 |
|                          | **565,216,267** |

#### code

| Filename                 | Filesize  |
|--------------------------|-----------|
| code_10mb.txt            | 314,006,799 |
| github_sample.txt        | 191,123,094 |
| stackexchange_sample.txt | 71,940,138  |
|                          | **577,070,031** |

The following programming and markup languages are represented in both "englishcode" and "code" vocabularies:
1. Assembly
2. Batchfile
3. C
4. C#
5. C++
6. CMake
7. CSS
8. Dockerfile
9. FORTRAN
10. Go
11. Haskell
12. HTML
13. Java
14. JavaScript
15. Julia
16. Lua
17. Makefile
18. Markdown
19. PHP
20. Perl
21. PowerShell
22. Python
23. Ruby
24. Rust
25. SQL
26. Scala
27. Shell
28. TypeScript
29. TeX
30. Visual Basic

## Capcode

[Capcode](https://github.com/alasdairforsythe/capcode) is an alternative encoding for uppercase in UTF-8 text, supporting all UTF-8 characters. It's completely lossless, changing the way in which capital letters are encoded so they can share tokens with lowercase letters but without losing any information. In theory, capcode makes it easier for a model to learn the meaning of words. Additionally, capcode makes for more efficient tokenization because it frees up so many tokens that would otherwise be used for uppercase variants of already existing lowercase tokens.

## Normalization

TokenMonster is designed to be plug-and-play, taking care of normalization concerns for you. UTF-8 and UTF-16 vocabularies are automatically NFD normalized and encoded Little Endian regardless of architecture. When tokenizing, the exact same transformations are applied transparently, so you can pass a string to either UTF-8 or UTF-16 vocabularies, with or without capcode, and on either Little or Big Endian architecture, and it will be processed correctly.

No normalizations are applied to charset "None" vocabularies. If you're not sure which to choose, UTF-8 is preferred.

## Which Vocabulary To Choose

There is a sweet spot for a vocabulary size, and it is probably around `24000` per "language" included in the vocabulary. This is true even for large models.

In the first version of TokenMonster, the lowest vocabulary size was `32000`. In the second version I introduced `24000`. In the third version, I went as low as `1024`. I found I could keep going lower, and not suffer much reduction in compression. I recommend you compare them yourself on the [TokenMonster Tester](https://bot.co/tokenmonster/) webpage to get a feeling for it.

It's my opinion that the 100K vocab size used by OpenAI is too large, unless you intend to support at least 3 languages in the same vocabulary. More is not better. At 100K the vocabulary has "spare" tokens. I'm defining having "spare" tokens as the point at which the vocabulary begins to allocate tokens to long and specific sequences, such as (real examples) "limitations under the License" and "#### According to". This does not happen at lower vocab sizes, but it does happen at 100K vocab size in English, which implies that the optimal vocabulary has already been reached and it's now just compressing frequently occurring strings.

I would advise then, that you can attempt to keep the vocabulary size fairly low in most cases and either be happy with a smaller and faster model, or increase the embedded space accordingly, or both.

In regards to optimization modes, `strict` is the one to go for if your model is limited by its own size or largely undertrained. If it's a small model that isn't that clever, and you want to get the most out of it, choose `strict` because it'll probably result in a smarter model given the simpler vocabulary. On the other hand, if you're training something serious with enough training data so that each token is exposed to a variety of contexts in order to learn it's more complex grammar, you probably want to go for `clean` or `balanced`.

## How does it work and how is it different from BPE?

Byte-Pair-Encoding starts with single byte tokens and merges frequently occuring tokens together iteratively, growing the vocabulary out of single characters. TokenMonster takes an entirely different approach, beginning with all possible tokens, and distilling the vocabulary down to the vocab size using a method inspired by chemical distillation. TokenMonster thereby does not run into the issue BPE has, that once a branch is chosen, it's assumed to be beneficial, and although it can later be pruned, the alternative branch that might have performed better has already been lost.

The secret sauce that enables TokenMonster to outperform other algorithms is made from:
1. The distillation method is an effective means of separating that which is wanted from that which is not, without losing any of the cream.
2. The training process targets the tokenization method being used. The vocabulary is generated to be optimal for the specific tokenization algorithm, which is a necessary step for optimal tokenization.

In simplified terms it does the following:
- Generates all possible tokens in the dataset (40 billion in 1 GB of text)
- Deletes all tokens that have no more than 100 occurrences (4 million)
- Generates random vocabularies of vocab_size
- Tokenizes the dataset using the target tokenization algorithm with the random vocabulary
- Deletes the 1% "worst" scoring tokens
- Repeat hundreds of thousands of times
- When vocab_size is reached, resurrect potential tokens
- Keep doing this until a more optimal vocabulary cannot be found 1000 times in a row

TokenMonster does not need any information about the language or structure, and results in a neat list of words, subwords and common phrases. Sample:
```
a number of 
a series of 
a wonderful 
ability and 
able to get 
about being 
about their 
account for 
acknowledge 
acquisition 
addition to 
address the 
advertising 
affected by 
after being 
against the 
```

## The Ungreedy Tokenization Algorithm

TokenMonster uses an ungreedy tokenization method in which each token has up to 2 alternatives that are selected during training, which are subwords of itself. First the longest token that matches the next segment of text is selected in a greedy fashion. The alternative tokens are looked up on an index that is included in the vocabulary file. The longest token matching the following text segment is found for the original and its alternatives, giving 3 possible branches. If any of those do not end on a word boundary, a further branch is followed utilizing a forward delete token, which allows for words beginning with a space to be used as parts of other words. The 6 total branches are scored based on various rules, the optimal branch is chosen and the tokenization continues along that branch.

Because the training process targets the tokenization algorithm, the training is not only selecting for tokens but selecting for the relationship between tokens in the vocabulary.

## Support & Consultation

Use the "Discussions" tab for free support on how to use TokenMonster. You can also hire me for a paid consultation on how to get the best out of TokenMonster, or to generate a vocabulary for you according to your specific requirements.

.
