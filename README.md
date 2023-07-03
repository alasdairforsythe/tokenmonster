# TokenMonster

TokenMonster is the world's most advanced tokenization library, enabling language models to run faster, cheaper, smarter and generate longer streams of text.

<img width="661" alt="tokenmonster" src="https://github.com/alasdairforsythe/tokenmonster/assets/77910352/1136330a-bf25-4a17-8edb-06b90fffb236">

TokenMonster is an ungreedy tokenizer and vocabulary builder, outperforming tiktoken by 35%. In fact, TokenMonster's smallest 24000 vocabulary consistently uses less tokens than tiktoken's largest 100256 vocabulary to tokenize the same text. [See benchmark](./benchmark).

TokenMonster can train and generate an optimal vocabulary on a 1GB dataset within 24 hours on a typical desktop. 440 [prebuilt vocabularies](#prebuilt-vocabularies) are provided, as well as tools to train your own vocabularies & implementations in Go, Python & Javascript for tokenization and detokenization using the prebuilt or your own vocabularies.

You can [test TokenMonster in your browser here](https://bot.co/tokenmonster/), tokenizing live in native Javascript.

TokenMonster is a novel approach to tokenization with broad-ranging use potential, but its primary motivation is to increase the inference speed and context-length of large language models. By selecting better tokens, text can be represented with 35% fewer tokens compared to other modern tokenizing methods, increasing the speed of inference, training and the length of text by 35%. The code-optimized tokenizers do even better, [see for yourself](https://bot.co/tokenmonster/).

I also believe that TokenMonster vocabularies will improve the comprehension of Large Language Models. For more details see [The Philosophy of Tokenization](#the-philosophy-of-tokenization).

## Features
- Outperforms other tokenization algorithms in every area ([benchmark](./benchmark))
- Selects the optimal vocabulary from a raw text file
- 5 optimization modes to choose from: `unfiltered`, `clean`, `balanced`, `consistent`, `strict`
- Ungreedy, can score and follow 6 parallel branches faster than other algorithms can follow one
- Faster than other tokenization algorithms ([benchmark](./benchmark))
- Supports UTF-8, UTF-16 and binary
- Successfully identifies words, subwords, common phrases and figures of speech by itself
- Works with HTML tags, sequential spaces, tabs, etc. without wasting context
- Can be trained on any language
- Can achieve 7 characters per token
- Vocabulary files can be modified and resized even after training
- Special tokens can be added at any time
- No GPU needed

## Table of Contents

* Usage [Go](./go/) | [Python](./python/) | [Javascript](./javascript/) | [Training](./training/)
* [Benchmark](./benchmark)
* [Prebuilt Vocabularies](#prebuilt-vocabularies)
* [Datasets](#datasets)
* [Capcode](#capcode)
* [Normalization](#normalization)
* [Which Vocabulary Size To Use](#which-vocabulary-size-to-use)
* [How does it work and how is it different from BPE?](#how-does-it-work-and-how-is-it-different-from-bpe)
* [The Ungreedy Tokenization Algorithm](#the-ungreedy-tokenization-algorithm)
* [The Philosophy of Tokenization](#the-philosophy-of-tokenization)
* [To Do](#to-do)


## Prebuilt Vocabularies
440 vocabularies are planned or have already been built. Download them from [Hugging Face](https://huggingface.co/alasdairforsythe/tokenmonster).

Choose a dataset from:
`code` `english` `englishcode` `fiction`

Choose a vocab size from:
`1024` `2048` `4096` `8000` `16000` `24000` `32000` `40000` `50256` `65536` `100256`

Choose an [optimization mode](training#-mode) from:
`unfiltered` `clean` `balanced` `consistent` `strict`

For a capcode disabled vocabulary add:
`nocapcode`

And finally add the version number:
`v1`

Examoles: `fiction-24000-consistent-v1` `code-4096-clean-nocapcode-v1`


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

No normalizations are applied to charset "None" vocabularies.

If you're not sure which to choose, UTF-8 is preferred.

## Which Vocabulary Size To Use

#### Quick Guide:

1. If your primary language has capital letters, use capcode. Otherwise do not use capcode.
2. If your primary language is English or code, use prebuilt vocabularies. Otherwise train your own vocabulary from a dataset in that language.
3. If you are targeting one primary language:
   - If parameter count is not an issue, choose 65536 vocab size.
   - To make a small fast model, choose 24000 vocab size.
   - For most models choose 32000 or 40000 vocab size.
4. If you are targeting two languages use 50256 or 65536 vocab size.
5. If you are targeting three or more languages, use 100256 vocab size.

I've spent a lot of time pouring over vocabularies of varying sizes and it's my opinion that the 100K vocab size used by OpenAI is too large, unless you intend to support multiple languages. More is not better because the model will have to learn all of those relationships. At 100K the vocabulary has "spare" tokens. I'm defining having "spare" tokens as the point at which the vocabulary begins to allocate tokens to long and specific sequences, such as (real examples) "limitations under the License" and "#### According to". This does not happen at lower vocab sizes, but it does happen at 100K vocab size in English, which implies that the optimal vocabulary has already been reached and now it's just compressing text. That does not mean that *all* words have tokens, nor should they. See [The Philosophy of Tokenization](#the-philosophy-of-tokenization) for *why* a word is or isn't tokenized.

The 30-40K vocabulary size is a much better choice for keeping the parameter count of your model down, or keeping it the same but having a smarter model. If keeping the number of parameters of the model down is not a concern, 50-60K is a good size. If you want to support Traditional Chinese, 100K is your friend, but for Latin scripts something around the 40K range is preferable. Fun fact: 20K - 50K range is also the vocabulary size of a person.

So if you're only using English, choose 32K vocab size if keeping the parameters low is of benefit to your model, and choose 65536 if you want to compress the text down as much as possible.

My prebuilt models can be used as-is for English and code. The 100K English model does a pretty good job of tokenizating some other languages that were present in the wikipedia_sample dataset from Red Pajamas, such as French and Italian. However, if you intend your model to use another language, I would recommend to train the vocabulary from scratch on a dataset within your target language(s). It takes around 1 day to build a new vocab on an 80-Core server, so on a desktop perhaps a week.

## How does it work and how is it different from BPE?

Byte-Pair-Encoding starts with single byte tokens and merges frequently occuring tokens together iteratively, growing the vocabulary out of single characters. TokenMonster takes an entirely different approach, beginning with all possible tokens, and distilling the vocabulary down to the vocab size using a method inspired by chemical distillation. TokenMonster thereby does not run into the issue BPE has, that once a branch is chosen, it's assumed to be beneficial, and although it can later be pruned, the alternative branch that might have performed better has already been lost.

The secret sauce that enables TokenMonster to outperform other algorithms is made from:
1. The distillation method is an effective means of separating that which is wanted from that which is not, without losing any of the cream.
2. The training process targets the tokenization method being used. The vocabulary is generated to be optimal for the specific tokenization algorithm, which is a necessary step for optimal tokenization.

In simplified terms it does the following:
- Generates all possible tokens in the dataset (3 billion)
- Deletes all tokens that have no more than 100 occurrences (10 million)
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

TokenMonster uses an ungreedy tokenization method in which each token has a single alternative token that is selected during training, which is a subword of itself. First the longest token that matches the next segment of text is selected in a greedy fashion. The alternative token is looked up on an index that is included in the vocabulary file. The longest token matching the following text segment is found for both the original and its alternative, giving 2 possible branches. The preferred branch is calculated according to various rules and the tokenizing continues along that branch.

The advantage of this method is that it enables the tokenizer to look ahead, whilst running only marginally slower than the greedy tokenization method. Because the training process targets the tokenization algorithm, the training is not only selecting for tokens but selecting for the relationship between each token and their alternative.

## The Philosophy of Tokenization

Tokenization is a particularly fun problem because it's infinitely complicated, and yet intuitive. On the surface it seems obvious. Yet on deeper inspection, the difficulty, or I could even say "impossibility" of the problem becomes apparent.

There are virtually infinite possible tokens (3 billion unique tokens on my 904MB dataset with 32 max-token-length) and the decision to include in the vocabulary any of those tokens affects the decision for whether or not to include every other token. To illustrate my point consider this example: the token `wicked` reduces the utility of the token `wickedly` but I would still need the token `wick`. If I include `wick`, `ed` and `edly`, that affects the utility of `supposedly`. Perhaps then I should use `suppos` and my existing `edly`, but if I do that affects `suppose` which now needs 2 tokens, unless I include it too.

Even if the issue of every token affecting every other token were solved, the optimal combination of tokens (the optimal vocabulary) is entirely dependant on the tokenization algorithm used when actually tokenizing text with the already determined vocabulary. Typically a greedy tokenization is used, which always chooses the longest matching token at the current position. For example, if I tokenize the string `the cat ate tuna` with tokens `the` `cat` `ate` `tuna` `the cat` `cat ate tuna`, the greedy tokenizer will choose `the cat` `ate` `tuna` (because the first token is longer), which is less optimal than `the` `cat ate tuna`. Hence, if I were intending for a greedy algorithm to be used, the vocabulary should be different than if an ungreedy algorithm were used. Then within ungreedy algorithms there are again a virtually infinite number of choices for how to implement it. Optimal then, depends upon the tokenization algorithm, and we can't just pretend like x is a good vocabulary without talking about how the tokenization process itself is being applied.

How then to determine what should and shouldn't be allocated a token? Information theory can help, but it runs into it's own issues. The issue one comes across when trying to use a information gain or another formula to determine tokens is that it so happens that the worst tokens are the same tokens as the best tokens, if only another token were present or not in the vocab. It's for this reason that they did not solve the problem, and all good tokenizers, whether it be BPE or TokenMonster, use an iterative approach, which is to recalculate the effect of any change every time a change is made.

Consider the *purpose* of the tokenization. For a large language model, the purpose of tokenization is primarily to split the information contain therein into information-relevant building-blocks. This is the same purpose language serves to us. In fact, the language is already tokenized: words are tokens. Words are symbols that represent meaning, and they exist in layers, each layer building from the components beneath. The first layer is a letter, then words are made of letters, phrases are made of words, sentences are made of phrases, and so on. The meaning of a word is not directly related to the meaning of a letter. Likewise the meaning of a phrase usually originated with, but can have entirely deviated from, the meaning of the component words. The meaning of a sentence is separate again to it's component phrases. To clarify, if I say `how's things?`, I'm not asking specifically about "things", rather it's understood this is an expression referring to your life in general, and an invitation to begin a conversation, which may not even be about you or your things.

It's also not a given that word boundaries are even the best choice for tokens. That's an assumption. It's not a given that (and this is only an example to make a point), `I` `like` `cheese` is necessarily capturing the meaning better than `I l` `ike` `cheese`, it might be that the relationship between `I l` and `ike` in the context of cheese is more suitable — or it might not be. It's interesting to think about in the context of what the LLM itself sees, because it never truly sees the text either way. It only sees numbers. We know those numbers represent that text, but it does not know that. The LLM is learning relationships between those numbers, and their positional encodings, to one another. That being considered, it's not clear to me what exactly consistitutes a good boundary, and although I've seen papers claiming evidence for word-boundaries, I'm unconvinced that it's a fair assessment, because a good vocabulary *does* tokenize on word boundaries because that *does* produce better tokenization empirically. My point here being that while it is true that word-boundaries make for good token boundaries, that does not mean that word-boundaries *always* make for good token boundaries. For example, it's intuitively obvious that if we have a saying, which has a specific meaning that has deviated from it's component words, it would be *easier* for the LLM to determine it's meaning if it did not have to learn that this specific order of tokens means something different to what it would assume were it to see that phrase only as being built from the word components.

When discussing tokenization I've noticed that many people are concerned with "out of vocabulary" (OOV) words. I know from my previous work in layout detection and OCR that there are roughly 600,000 words in English, including the various tenses, legal and scientific terminology (allowing for Latin.) Should then all these words have tokens? The answer is: no. OOV words are not a problem. Any word can be built from subword tokens, and it's just not true that a word *should have* it's own token, any more than a common phrase should have its own token. Tokens are best allocated not on words, but on a heirarchy of meanings, and the goal of a vocabulary is to strike a delicate balance between *meaning* and compression. We want to condense the text into a predetermined number of building blocks that fully capture its nuances, whilst representing its entirity with the fewest building blocks. A word made of multiple subword tokens is pricesely the same thing as a phrase, or sentence, made from multiple word tokens. Word boundaries are in fact invisible to the language model.

Tokenization is an art form. It's the art of quantizing the qualitative. Its core objective lies in distilling the complexity of meaning with utmost efficiency. It stands at the crossroads where the quantitative world of rules and boxes intersects with the colorful tapestry of qualitative, multifaceted meaning. Here, the circle must be squared. The meaning must be captured and made to conform to our rules and put into our boxes, yet it must be done with respect and careful attention. Properly applied boxes and rules are distinctions that give form and function to the otherwise abstract and intangible.

I hope this brings some attention to the importance of tokenization in natural language processing.

## To Do

☐ Optimized C++ implementation

☐ Make a Python module wrapping the C++ implementation and put on PyPI
