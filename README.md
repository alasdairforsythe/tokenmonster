# tokenmonster
![logo](https://github.com/alasdairforsythe/tokenmonster/assets/77910352/6ad94a66-a428-40ed-a5f0-9e2652daef45)
Given a text dataset, a vocabulary-size and a maximum-token-length, tokenmonster selects the tokens that optimally represent your dataset at that vocabulary size by brute force. It can do this at reasonable speed (within 24 hours) on server hardware, at a cost of around $8. Prebuilt vocabularies are provided, as well as tools & libraries for tokenization and detokenization using the prebuilt or your own vocabularies.

tokenmonster is a novel approach to tokenization with broad-ranging use potential, but its primary motivation is to increase the inference speed and context-length of large language models by choosing better tokens. By selecting more optimal tokens, text can be represented with 25-30% less tokens compared to other modern tokenizing methods, increasing the speed of inference, training and the length of text by 25-30%.

I also believe that tokenmonster vocabularies will improve the comprehension of Large Language Models. For more details see [How and Why](#how-and-why).

### Features
- Longer text generation at faster speed
- Determines the optimal token combination for a greedy tokenizer (could add support non-greedy)
- Successfully identifies common phrases and figures of speech
- Works with all languages and formats, even binary
- Quickly skims over HTML tags, sequential spaces, tabs, etc. without wasting context
- Does not require normalization or preprocessing of text
- Averages > 5 tokens per character
- No GPU needed

### Prebuilt Vocabularies
The following vocabularies have already been built:

| Name            | Vocab Size | Dataset Size | Dataset Source                                                       |
|-----------------|------------|--------------|----------------------------------------------------------------------|
| general-english | 65535      | 840 MB       | arxiv + book + c4 + cc + github + stackexchange + wikipedia + reddit |
| general-english | 32000      | "            | "                                                                    |
| code            | 65535      | 263 MB       | github + stackexchange                                               |
| code            | 32000      | "            | "                                                                    |

The training data mostly came from Red Pajamas [1B Token Sample](https://huggingface.co/datasets/togethercomputer/RedPajama-Data-1T-Sample). However, to reduce formal English and emphasize other languages, informal writing and code, I made the following modifications to the Red Pajamas sample: book_sample, c4_sample & cc_sample were cropped to 100MB, and [Reddit conversations](https://huggingface.co/datasets/SophieTr/reddit_clean) data were added (also cropped to 100MB.)

| Filename                 | Filesize  |
|--------------------------|-----------|
| arxiv_sample.txt         | 88,925,569  |
| book_sample.txt          | 108,069,616 |
| c4_sample.txt            | 100,560,318 |
| cc_2023-06_sample.txt    | 100,852,231 |
| github_sample.txt        | 191,123,094 |
| stackexchange_sample.txt | 71,940,138  |
| wikipedia_sample.txt     | 79,181,873  |
| reddit.txt               | 100,027,565 |

### How and Why
There are virtually infinite possible tokens (around 1 billion unique tokens on my 840MB dataset with 30 max-token-length) and the decision to include in the vocabulary any of those tokens affects the decision for whether or not include every other token. For example, the token *"wicked"* reduces the utility of the token *"wickedly"* but I would still need the token *"wick"*. If I include *"wick"*, and *"ed"* and *"edly"* that affects the utility of *"supposedly"*, perhaps then I should use *"suppos"* and my existing *"edly"*, but if I do that affects *"suppose"* which now needs 2 tokens unless I include it too.

To make matters worse, the optimial combination of tokens is also dependant on the tokenization method. The standard is to use a greedy tokenization, which means the longest matching token is chosen. For example, if I tokenize the string *"the cat ate tuna"* with tokens *"the | cat | ate | tuna | the cat | cat ate tuna"*, the greedy tokenizer will choose *"the cat | ate | tuna"* which is less optimal than *"the | cat ate tuna"*.

Then consider the purpose of the tokenization. For a large language model, the purpose of tokenization is primarily to split the information contain therein into information-relevant building-blocks. This is the same purpose language serves to us. These are symbols that represent meaning, and we have levels of these symbols. The first level is a letter, then words are made of letters, phrases are made of words, sentences are made of phrases, and so on. The meaning of a word is not directly related to the meaning of a letter. Likewise the meaning of a phrase usually orinated with, but can have entirely deviated from, the meaning of the component words. The meaning of a sentence is separate again to it's component phrases. To clarify, if I say *"how's things?"* I'm not asking specifically about "things" but it's understood that this is an expression referring to your life in general, and an invitation to begin a conversation, which may not even be about you or your things.

I believe the choice of word-boundaries and subwords for tokenizing Large Language Models is a mistake. The reason is obvious: it requires the LLM to, within it's hidden layers, learn both the meaning of the word and every alternative meaning that words repreesents as a component with a phrases, which is only loosely connected to its word-meaning. I reason that by assuming words properly represent language, the tokenization methods commonly used are sub-optimal for both the representation of the text and the representation of the meaning of the text.

*tokenmonster* solves this problem with brute force, in simplified terms it does the following:
- Generates all possible tokens in the dataset (1 billion)
- Delete all tokens that have no more than x occurrences (20 million)
- Generates random vocabularies of vocab_size
- Tokenizes the dataset with the random vocabulary of vocab_size
- Deletes the 1% worst tokens from each random vocabulary
- When vocab_size is reached, resurrect potential tokens
- Keep doing this until a more optimal vocabulary cannot be found

tokenmonster is not given any information about the language or stucture, it simply optimizes for the vocabulary of given size on the given dataset. The result is a neat list of words, subwords and common phrases. Sample from general-english-65535:
```general-english-65535
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
across the s
act that the
activities a
activities, 
addition to 
additional i
address the 
advertising 
affected by 
after being 
against the 
```
