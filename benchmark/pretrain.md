# Pretraining 16 language models on different tokenizers

To examine the impact of varying vocabularies on language models, I pretrained and subsequently finetuned 16 models with distinct vocabularies. I trained 12 models using the NanoGPT SMALL architecture (based on GPT-2 SMALL), which consists of 12 attention heads, 12 layers, and an n_embd of 768, for approximately 400,000 iterations (or about 10 epochs). I trained 4 models on the GPT-2 MEDIUM setup, featuring 16 attention heads, 24 layers, and an n_embd of 1024, running for 600,000 iterations. All models were pretrained using [NanoGPT](https://github.com/karpathy/nanoGPT) and the OpenWebText dataset. For finetuning, I employed the instruct dataset from [baize-chatbot](https://github.com/project-baize/baize-chatbot/tree/main/data), supplemented with an additional 20,000 and 500,000 synthetically produced "dictionary" entries. In the near future, I plan to release the code, pretrained models, instruct tuned models, and the finetuning dataset.

The pretraining phase alone for all 16 models took a cumulative 147 days on 8x GPUs (equivalent to 1,176 GPU days) and cost $8,000. I don't have a GPU sponsor (and this is a free, open-source project) so that $8,000 came from my own pocket, which explains why I haven't done, and probably won't be doing, more tests on any inconclusive results.

## Summary of Findings

- Comparable TokenMonster vocabularies perform better than both GPT-2 Tokenizer and tiktoken p50k_base in all areas.
- Optimal vocabulary size is 32,000.
- Simpler vocabularies converge faster but do not necessarily produce better results.
- Higher compression (more chr/tok) does not negatively affect model quality alone.
- Vocabularies with multiple words per token have a 5% negative impact on SMLQA (Ground Truth) benchmark, but a 13% better chr/tok compression.
- Capcode takes longer to learn, but once the model has converged, does not appear to affect SMLQA (Ground Truth) or SQuAD (Data Extraction) benchmarks significantly in either direction.
- Validation loss and F1 score are both meaningless metrics when comparing different tokenizers.
- Flaws and complications in the tokenizer have a much greater impact the model's ability to learn facts than affect it's linguistic capability.

Based on the results, the recommended vocabulary is `englishcode-32000-consistent`. However, as mentioned above there is currently a tradeoff between the SMLQA Ground Truth accuracy of the model and the compression ratio when using the default TokenMonster setting of allowing for multiple words to be included in a single token, which increases the learning curve. I strongly believe that this tradeoff can be minimized and a "best of both" vocabulary achieved by forcing 80% of the vocabulary to be one-word and 20% to be multi-word. I hypothesize that this approach would perform equally in quality to the one-word vocabulary, while still realizing around 50% of the benefits in chr/tok from multi-word vocabularies.

To elaborate on the "flaws and complications in the tokenizer have a much greater impact the model's ability to learn facts than affect it's linguistic capability": it's an interesting feature of the training process, and also makes sense when you consider how the training works. I don't have proof for my reasoning other than it makes perfect sense. Essentially, because the pattern of linguistic fluency is more obvious to correct during backpropegation vs. linguistic facts (which are extremely nuanced and context-dependent), this means that any improvement made in the efficiency of the tokenizer, that has in itself nothing to do with truthfulness, has the knock-on effect of directly translating into improved fidelity of information, as seen in the SMLQA (Ground Truth) benchmark.

### Discussion on Vocab Size

Before running these tests I believed that 32,000 is the optimal vocabulary size, and the results confirm the same. `50256-balanced` performs only 1% better than `32000-balanced` on SMLQA (Ground Truth). Ideally I would like to prove this definitively by testing MEDIUM models of 80/20 vocabularies as discussed above in vocab sizes 24000, 32000, 50256 & 100256.

### Discussion on Optimization Mode

I tested `balanced`, `consistent` and `strict` optimization modes. These are TokenMonster specific modes that affect the ways in which punctuation and capcode markers can be combined with word tokens. My original prediction and intention was that `consistent` would perform better (being less complex) yet have slightly lower compression ratio.

The findings appear to corroborate this, though there are a few key observations to highlight. Firstly, `consistent` seems to outperform `balanced` by approximately 5% on the SMLQA (Ground Truth) benchmark. Conversely, it performs notably (28%) inferior on the SQuAD (Data Extraction) benchmark. However, the SQuAD benchmark exhibits substantial variability (with different results on repeated runs), leaving me unconvinced that this is a meaningful trend. I didn't test `balanced` vs `consistent` all the way to convergence, so it may represent only that `consistent` is easier to learn. In fact, it may be that `balanced` does better on SQuAD (Data Extraction) pricesly because it's more difficult to learn, and therefore less likely to hallucinate (this is speculative). Either way, the inconclusivity implies that it probably doesn't matter which one you choose for most cases, and that itself is an interesting discovery because it means that there is no obviously significant problem with combining punctuation and words in a single token. To date, all other tokenizers have assumed that punctuation should be separated from letters, but it's clear from the results here that word and punctuation can be merged in a single token without noticeable loss of quality. This corroborated by the medium sized `50256-consistent-oneword` which performs equally with `50256-strict-oneword-nocapcode` and better than `p50k_base`, despite having simple punctuation merged with word tokens (which the other two do not.)

Following on from that, there is a significant detriment with `strict` mode with capcode enabled. `50256-strict-oneword-nocapcode` scored 21.2 on SMLQA and 23.8 on SQuAD, as opposed to 16.8 and 20.0 for `50256-strict-oneword`. The reason is obvious: `strict` optimization mode prevents merging capcode markers with word tokens, resulting more tokens being required to represent identical text, which is reflected directly in the 8% discrepancy in chr/tok. In fact, `strict-nocapcode` is more similar to `consistent` than it is to `strict`, and indeed the MEDIUM model `50256-consistent-oneword` and `50256-strict-oneword-nocapcode` have almost equal values across all metrics.

The conclusion here is that, in most cases, the model does not have any difficulty learning the meaning of tokens that contain punctuation combined with words. That said, it does appear that grammatical accuracy is higher (less grammatical errors) for `consistent` as opposed to `balanced`. All considered, I'd recommend `consistent` across the board. `strict` should be only be used with capcode disabled.

### Discussion on Grammatical Accuracy

As mentioned above, it looks like grammatical accuracy is higher (less grammatical errors) for `consistent` as opposed to `balanced`. This is reflected in the very slight negative correlation between Chr/Tok and Grammar, as shown in the graph below. Otherwise the most notable point here is that both of the reference vocabularies `GPT-2 Tokenizer` and `tiktoken p50k_base` have terrible grammar (98.1% and 97.5%, respectively) compared to the equivalent TokenMonster `50256-strict-oneword-nocapcode` vocabularies (98.6% and 98.4%). I first thought this was just a coincidence, but running the sampling multiple times gives a result in the same range. The reason why is unclear.

<img width="639" alt="chrtok_grammar" src="https://github.com/alasdairforsythe/tokenmonster/assets/77910352/479a6bb0-6a77-445b-b6d7-4ea90f3bdda9">

### Discussion on MTLD

MTLD is a representation of linguistic diversity of the generated sample text. It appears to be highly correlated with the `n_embed` parameter, and not correlated with features you might expect, such as: vocabulary size, optimization mode, nor the maximum number of words per token. This can be seen particularly in `16000-balanced` (n_embd of 864) and `8000-consistent` (n_embd 900), which have the highest MTLD of the SMALL models and perform poorly in other areas.

In the MEDIUM models, the reference `p50k_base` has the highest MTLD of all at 43.85, whilst also having the lowest score on grammar. The reasons for this are unclear, but I would guess that it's the result of a somewhat exotic choice of training data.

### Discussion on SQuAD

The SQuAD benchmark tests the ability of the model to extract data from a paragraph of text by presenting the paragraph and then asking a question where the answer is included in the paragraph. The results for this do not make much sense, with no clear pattern or correlation to anything, including total model parameters. In fact, the `8000-balanced` model with 91M parameters scored better on SQuAD than `50256-consistent-oneword` with 354M parameters. Perhaps there were not enough examples of this style, and too many QA pairs in the instruct finetuning dataset. Or perhaps it's just not a very good benchmark.

### Discussion on SMLQA

[SMLQA](https://github.com/alasdairforsythe/slmqa) benchmark tests Ground Truth by asking general knowledge questions with objective answers, such as *"What country has the capital city Jakarta?"* and *"Who wrote the Harry Potter series of books?"*.

It's worth noting that the reference tokenizers `GPT-2 Tokenizer` and `p50k_base` performed quite well on this benchmark. So well, in fact, that I initially thought I'd wasted months of work of thousands of dollars just to prove that tiktoken has higher quality performance than TokenMonster. Instead, it turns out that the issue was related to the number of words per token. This is most evidenced in the MEDIUM models, which I will illustrate with a chart below.

<img width="746" alt="smlqa" src="https://github.com/alasdairforsythe/tokenmonster/assets/77910352/efd764c6-653e-4b91-804c-e2e6f859a849">

<img width="744" alt="chrtok" src="https://github.com/alasdairforsythe/tokenmonster/assets/77910352/c42de1fd-58e7-4fe0-a76f-0b1e46dc57bb">

As you can see, the one-word vocabularies perform slightly better than multiple words per token, which is the default for TokenMonster vocabularies.

Another important observation is that the vocabulary size directly affects the Ground Truth when the vocabulary size is below 32,000, even when the `n_embd` parameter of the model is adjusted to make up for the reduced size of the model. This to me was unintuitive, as I had expected `16000-balanced` with `n_embd 864` (121.34M parameters) and `8000-consistent` with `n_embd 900` (123.86M parameters) to do better than `50256-consistent` with `n_embd 768` (123.59M), but that was not the case — both performed considerably worse (13.7 & 15.1 vs. 16.4 for  `50256-consistent`). However, both of those 'adjusted' models were trained for the same wall time, which happened to result in pretraining for significantly fewer epochs (albeit in the same amount of time.)

## SMALL (12 heads, 12 layers)

Twelve models were trained on the default NanoGPT architecture, which is based on GPT-2 architecture of 12 attention heads and 12 layers, with an embedding size of 768. 
None of these models were trained to convergence, which in plain English means that the models were not trained to their maximum learning capacity. They were trained for 400,000 iterations, and it appears that 600,000 iterations are required for maximum learning. The reason for this was a simple matter of budget and the uncertainty of where the convergence point was.

### Models Trained (SMALL)

| |n_embd|Parameters|VRAM / GPU|It|N Iter|Batch size|Wall time|GPU|Val Loss|SMLQA (Truth)|SQuAD (Extraction)|MTLD|Grammar|Chr/Tok|Vocab Size|
|:----|:----|:----|:----|:----|:----|:----|:----|:----|:----|:----|:----|:----|:----|:----|:----|
|8000-consistent|768|91.10M|7.9 GB|1250ms|400000|12 x 40 x 1024|5 days 20 hours|8x RTX 3090|2.648080826|12.27489893|18.1|32.82396396|98.52717534|3.883843189|8000|
|8000-balanced|768|91.10M|7.9 GB|1250ms|400000|12 x 40 x 1024|5 days 20 hours|8x RTX 3090|2.678756475|11.90738699|23.2|30.91240069|98.11471077|3.895959904|8000|
|24000-consistent|768|103.39M|11.1 GB|700ms|400000|12 x 40 x 1024|3 days 6 hours|8x RTX 4090|3.252145767|13.96545388|24.7|30.98576192|98.30453326|4.852381727|24000|
|32000-balanced|768|109.53M|12.8 GB|1500ms|400000|12 x 40 x 1024|7 days 1 hour|8x RTX 3090|3.477502108|15.54575524|23.15|32.25141226|98.26163494|5.18559831|32000|
|16000-balanced|864|121.34M|19.6 GB|2125ms|260,000|12 x 40 x 1024|6 days 8 hours|8x RTX 3090|3.052534819|13.70819552|23.95|33.36356293|98.26130612|4.505310377|16000|
|50256-consistent|768|123.59M|16.6 GB|1350ms|400000|12 x 40 x 1024|6 days 7 hours|8x A5000|3.642035484|16.4277839|18.7|31.10665184|98.16432457|5.474811036|50257|
|50256-balanced|768|123.59M|16.6 GB|1345ms|400000|12 x 40 x 1024|6 days 8 hours|8x A5000|3.714226007|15.69276001|23.35|30.38047124|97.96901439|5.565095795|50257|
|50256-consistent-oneword|768|123.59M|16.6 GB|1350ms|400000|12 x 40 x 1024|6 days 8 hours|8x A5000|3.111525774|18.77986035|**26.8**|31.35451692|98.51316332|4.895867925|50257|
|50256-strict-oneword|768|123.59M|16.6 GB|1350ms|400000|12 x 40 x 1024|6 days 14 hours|4x RTX 4090|2.840897799|16.75854465|20.0|30.13856553|98.36107386|4.456877685|50257|
|50256-strict-oneword-nocapcode|768|123.59M|16.6 GB|1340ms|400000|12 x 40 x 1024|6 days 7 hours|8x A5000|3.010657549|**21.16868798**|23.8|31.19579591|98.59565279|4.829220914|50257|
|GPT-2 Tokenizer|768|123.59M|16.6 GB|1350ms|400000|12 x 40 x 1024|6 days 9 hours|8x A5000|2.913994789|17.60382212|21.9|31.05927676|98.10224791|4.557022257|50257|
|8000-consistent|900|123.86M|13.4 GB|1590ms|320,000|12 x 40 x 1024|6 days 11 hours|8x RTX 3090|2.626039028|15.1414921|21.3|33.32517158|98.59659716|3.891184804|8000|

### Pearson Correlation (SMALL)

| |Val Loss|SMLQA|SQuAD|MTLD|Grammar|Chr/Tok|
|:----|:----|:----|:----|:----|:----|:----|
|Val Loss|1|0.227425|0.182508|-0.336023|-0.526899|0.968563|
|SMLQA|0.227425|1|0.271232|-0.341433|0.276803|0.451193|
|SQuAD|0.182508|0.271232|1|-0.101449|-0.006909|0.23585|
|MTLD|-0.336023|-0.341433|-0.101449|1|0.453961|-0.437805|
|Grammar|-0.526899|0.276803|-0.006909|0.453961|1|-0.433383|
|Chr/Tok|0.968563|0.451193|0.23585|-0.437805|-0.433383|1|

### Key Insights SMALL

1. 32,000 is the optimal vocabulary size. From vocabulary size 8,000 to 32,000: increasing the vocabulary size improves the ground-truth accuracy of the model. Expanding the vocabulary size from 32,000 to 50,257, increases total model parameters accordingly but yields only a marginal 1% improvement in ground-truth accuracy. Beyond 32,000, the gains dimish quickly.

2. Bad tokenizer design affects model ground truth, but not grammatical correctness or linguistic diversity. Tokenizers characterized by more complex grammatical rules (e.g. multi-word tokens, combinations of words and punctuation, capcode encoding tokens, and smaller total vocabulary sizes) were found to underperform relative to simpler tokenizers on ground truth benchmarks within the 90M - 125M parameter range. However, this complexity in tokenizer design did not exert a statistically significant impact on either the linguistic diversity or the grammatical correctness of the generated text. Even a compact model, such as one with 90M parameters, is capable of effectively leveraging a more sophisticated tokenizer. A more complex vocabulary requires a more extended learning period, which subsequently reduces the time available for the acquisition of information relevant to ground truth. As none of these models were trained to completion, the potential for additional training to narrow this performance gap remains to be seen.

3. Validation Loss is not an effective metric for comparing models that utilize different tokenizers. Validation Loss is very strongly correlated (0.97 Pearson correlation) with the compression ratio (average number of characters per token) associated with a given tokenizer. To compare Loss values between tokenizers, it may be more effective to measure loss relative to characters rather than tokens, as the Loss value is directly proportionate to the average number of characters per token.

4. The F1 Score is not a suitable metric for evaluating language models that are trained to generate variable-length responses (which signal completion with an end-of-text token). This is due to the F1 formula's heavy penalization of longer text sequences. F1 Score favors models that produce shorter responses.

5. All models (starting from 90M parameters), in conjunction with all tested tokenizers (ranging from 8000 to 50257 in size), demonstrated the capacity to be fine-tuned to produce grammatically coherent answers. While these responses are often incorrect or hallucinated, they are articulated eloquently and exhibit an understanding of the relevant context.

6. Lexical diversity and grammatical accuracy of the generated text increase significantly when embedding size is increased, and have a small negative correlation with characters/token. This implies that a vocabulary with higher compression (greater chr/tok) makes it slightly more difficult to learn grammar and lexical diversity.

7. There is no statistically significant correlation between chr/tok and either SMLQA (Ground Truth) or SQuAD (Information Extraction) benchmarks when adjusting for model parameter size. This implies that a tokenizer with higher compression, does not negatively impact the model's performance.

8. Comparing "consistent" and "balanced" vocabularies, it appears that "consistent" vocabularies perform slightly better on SMLQA (Ground Truth) benchmark, but considerably worse on SQuAD (Information Extraction) benchmark. Although more data is needed to confirm this.

<img width="745" alt="chrtokvsvalloss" src="https://github.com/alasdairforsythe/tokenmonster/assets/77910352/8cce420d-56d2-4474-9861-72a9fc2c3296">

## MEDIUM (16 heads, 24 layers)

After training and benchmarking the SMALL models, it became evident that I was measuring the learning speed instead of the model's learning capacity. Additionally, I wasn't optimizing the GPU's compute potential, given that I was using the default NanoGPT parameters. To remedy this, I chose to explore four variations using 50257-token-sized tokenizers with a MEDIUM language model size. I adjusted the batch size from 12 to 36 and scaled down the block size from 1024 to 256, ensuring I utilized the full VRAM capability of the 24GB GPUs, and I ran these for 600,000 iterations instead of 400,000. The pretaining for each of these models took an average wall time of just over 18 days, three times longer than the 6 days spent on the SMALL models.

Having the models trained to convergence did significantly reduce the performance difference between simpler vocabularies and more complicated vocabularies. The benchmark results for both SMLQA (Ground Truth) and SQuAD (Data Extration) are very close. The main difference is that `50256-consistent` has a whopping 23.5% chr/tok advantage over `p50k_base`. There is however, a small performance cost on Ground Truth associated with the vocabularies that use multiple words per token, although this can probably be fixed using the method I discussed at the top of the page.

### Models Trained (MEDIUM)

| |n_embd|Parameters| |It|N Iter|Batch size|Wall time|GPU|Val Loss|SMLQA (Truth)|SQuAD (Extraction)|MTLD|Grammar|Chr/Tok|Vocab Size|
|:----|:----|:----|:----|:----|:----|:----|:----|:----|:----|:----|:----|:----|:----|:----|:----|
|p50k|1024|353.55M|23.4 GB|2770ms|600000|38 x 40 x 256|18 days 19 hours|8x A5000|2.771923304|43.25615583|21.85|43.85039908|97.48425559|4.427129628|50257|
|50256-consistent|1024|353.55M|23.4 GB|2925ms|600000|38 x 40 x 256|18 days 7 hours|8x RTX 3090|3.452251673|42.1168688|**24.3**|39.73988191|98.4779274|5.465660627|50257|
|50256-consistent-oneword|1024|353.55M|23.4 GB|2800ms|600000|38 x 40 x 256|18 days 22 hours|8x A5000|2.974983215|44.39544285|22.9|35.65898628|98.34348224|4.854392844|50257|
|50256-strict-oneword-nocapcode|1024|353.55M|23.4 GB|2650ms|600000|38 x 40 x 256|18 days 6 hours|8x RTX 3090|2.901269197|**44.83645718**|22.85|35.53644771|98.42621765|4.806753679|50257|

After 560,000 iterations all the models begin to converge, as you can see in this chart from the wandb logs from `50256-consistent`:

<img width="908" alt="medium-50256-consistent-wandb" src="https://github.com/alasdairforsythe/tokenmonster/assets/77910352/a0de00b5-540a-4199-89f7-a9ae85f02424">

### What Next?

The next stage would be to train and benchmark a MEDIUM model using `englishcode-32000-consistent` vocabulary with 80% one-word tokens and 20% multi-word tokens. This will either confirm or refute the predictions I've made above.

.
