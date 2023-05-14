# tokenmonster
![logo](https://github.com/alasdairforsythe/tokenmonster/assets/77910352/6ad94a66-a428-40ed-a5f0-9e2652daef45)

Given a text dataset, a vocabulary-size and a maximum-token-length, tokenmonster selects the tokens that optimally represent your dataset at that vocabulary size by brute force. It can do this at reasonable speed (within 24 hours) on server hardware, at a cost of around $8. Prebuilt vocabularies are provided, as well as tools & libraries for tokenization and detokenization using the prebuilt or your own vocabularies.

[Test tokenmonster in your browser here.](https://bot.co/tokenmonster.html)

tokenmonster is a novel approach to tokenization with broad-ranging use potential, but its primary motivation is to increase the inference speed and context-length of large language models by choosing better tokens. By selecting more optimal tokens, text can be represented with 20-30% less tokens compared to other modern tokenizing methods, increasing the speed of inference, training and the length of text by 20-30%. The code-optimized tokenizers do even better, [see it for yourself](https://bot.co/tokenmonster.html).

I also believe that tokenmonster vocabularies will improve the comprehension of Large Language Models. For more details see [How and Why](#how-and-why).

### Features
- Longer text generation at faster speed
- Determines the optimal token combination for a greedy tokenizer (non-greedy support coming)
- Successfully identifies common phrases and figures of speech
- Works with all languages and formats, even binary
- Works well with HTML tags, sequential spaces, tabs, etc. without wasting context
- Does not require normalization or preprocessing of text
- Averages > 5 characters per token
- No GPU needed

### Greedy vs. Non-greedy
The current algorithm is a greedy algorithm (as are all other popular tokenization methods as far as I know). I have an idea for a non-greedy method that will add only 10% or so overhead to the tokenization process. I will test with this and if it's notably more efficient, I will replace the current greedy tokenizers with the ungreedy versions. All of that will be completed by the end of May.

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
There are virtually infinite possible tokens (around 1 billion unique tokens on my 840MB dataset with 30 max-token-length) and the decision to include in the vocabulary any of those tokens affects the decision for whether or not to include every other token. For example, the token *"wicked"* reduces the utility of the token *"wickedly"* but I would still need the token *"wick"*. If I include *"wick"*, and *"ed"* and *"edly"*, that affects the utility of *"supposedly"*. Perhaps then I should use *"suppos"* and my existing *"edly"*, but if I do that affects *"suppose"* which now needs 2 tokens, unless I include it too.

To make matters worse, the optimal combination of tokens is also dependant on the tokenization method. The standard is to use a greedy tokenization, which always chooses the longest matching token at the current position. For example, if I tokenize the string *"the cat ate tuna"* with tokens *"the | cat | ate | tuna | the cat | cat ate tuna"*, the greedy tokenizer will choose *"the cat | ate | tuna"*, which is less optimal than *"the | cat ate tuna"*.

Then consider the purpose of the tokenization. For a large language model, the purpose of tokenization is primarily to split the information contain therein into information-relevant building-blocks. This is the same purpose language serves to us. In fact, the language is already tokenized into words, it's probably not a coincidence that the average person, depending on their education has a vocabulary of between 20,000 - 50,000 words (the same as a LLM). Words are symbols that represent meaning, and they exist in layers, each layer building from the components beneath. The first layer is a letter, then words are made of letters, phrases are made of words, sentences are made of phrases, and so on. The meaning of a word is not directly related to the meaning of a letter. Likewise the meaning of a phrase usually originated with, but can have entirely deviated from, the meaning of the component words. The meaning of a sentence is separate again to it's component phrases. To clarify, if I say *"how's things?"*, I'm not asking specifically about "things", rather it's understood this is an expression referring to your life in general, and an invitation to begin a conversation, which may not even be about you or your things.

I believe that token selection can be improved from the current system of word-boundaries and subwords. Word-boundary tokenization requires the LLM to, within it's hidden layers, learn both the meaning of the word and every alternative meaning that words represents as a component of various expressions.

I reason that by assuming words properly represent language, the tokenization methods commonly used are sub-optimal for both the representation of the text and the representation of the meaning of the text. It's not a given that (and this is only an example to make a point), *I | like | cheese* is necessarily capturing the meaning better than *I l | ike | cheese*, it might be that the relationship between *"I l"* and "*ike*" in the context of cheese is more suitable — or it might not be. Ultimately we don't know, and most likely it doesn't matter because the whole point of the LLM is that it's capable of determining these relationships by itself, and if that's the case, it makes sense to use the most optimal tokenization stategy and let the LLM do it's job. Now it just so happens that words and subwords do appear to be very close to optimal tokenization, which I know because my optimal tokenizer selects those boundaries without any incentive to do so, but that is not to say that therefore tokens should represent words. It's likely that tokens should optimally represent the data as per the vocabulary size.

Papers published on tokenization largely subscribe to the school of thought that word boundaries are optimal in representing the meaning of the text. This sounds believable and it appears to have evidence because LLMs perform better when trained on words and stems rather than strictly following a formula, such as information gain. The issue is related to what you get from formulas like information gain. It'll give you the worst possible tokens, but they look nice, because it so happens that the worst tokens are the same tokens as the best tokens, if only another token were present or not in the vocab. This is why an almost good tokenizer performs very badly, and a common-sense tokenizer does a fairly good job, because this is intuitively obvious but not logically understood. *" recommen"* is useless if I have *" recommend"*, but potentially useful if I don't. None of the formulas account for this, and they can't really, because it's too complex. That means the only practical ways to solve this problem are either by training a neural net to do it, or brute force, the latter was easier to get going so that's what I did.

Summary of the problem:
1. There are infinite variables because any choice to include in the vocab any specific token affects the choice of whether to include every other token.
2. Even if it were possible to calculate the optimal tokens, the text-to-token tokenization (e.g. greedy, non-greedy) itself is not optimal.
3. Therefore a token selection method optimized for the text-to-token tokenization is more optimal than a "perfect" method, even if that perfection were possible, which it's not.
4. It's possible to get what looks like a good tokenization method from information gain and other methods but in practice these are less optimal than common-sense selection.
5. This is because a close to optimal vocabulary can be particularly bad, e.g. *"recommen"* and *"ded"* are useless if I have "recommend" token but if I don't they're vital.
6. There are then only 3 possible solutions to an impossible problem (a) common sense, (b) neural net, (c) brute force.

*tokenmonster* solves this problem with brute force by targeted a specific tokenization method, beginning with every potential vocabulary, and using a distillation method to reduce these potentials down to the single most optimal vocabulary. In simplified terms it does the following:
- Generates all possible tokens in the dataset (1 billion)
- Delete all tokens that have no more than x occurrences (20 million)
- Generates random vocabularies of vocab_size
- Tokenizes the dataset with the random vocabulary of vocab_size
- Deletes the 1% worst tokens from the random vocabulary
- Repeat hundreds of thousands of times
- When vocab_size is reached, resurrect potential tokens
- Keep doing this until a more optimal vocabulary cannot be found 500 times in a row

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

As for how I did this, it really comes down to the fact that I happened to have an 80-core ARM server with 256 GB of RAM doing nothing, combined with my obsession for efficiency. However, the key to what makes it possible to test millions of different vocabularies against 840MB of text, in fairly short timeframe is my [pansearch data structure](https://github.com/alasdairforsythe/pansearch). Too many times I'd asked myself the age old question *"what dictionary is faster than a hashmap, but uses less memory than a list?"*, to which the answer is: encode strings into 64-bit integers, stored in buckets according to length, and then unroll all of the loops.
