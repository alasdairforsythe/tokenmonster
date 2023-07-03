## Python Usage

You can take the script from here or install it with PyPI:
```
pip install tokenmonster
```

### Basic usage

```python
import tokenmonster

# Optionally set the tokenmonster directory, otherwise it will use ~/_tokenmonster
TokenMonster.set_local_directory("/path/to/preferred")

# Load a vocabulary by name, filepath or URL
vocab = TokenMonster("english-24000-consistent-v1")

# Tokenize some text
text = "Some text to turn into token IDs."
tokens = vocab.tokenize(text)
```

Then to detokenize:
```python
decoder = vocab.decoder()
decoded_text = decoder.detokenize(tokens)
```

There is a `decode` function for both the vocabulary object (`vocab.decode()`), and also the decoder object that is made with `vocab.decoder()`. The difference is that the decoder object is meant for when you are individually decoding a sequence of IDs that are part of the same generation sequence, e.g. decoding tokens as they are generating. If you already have the full sequence and intend to decode it all in one go, you can use `vocab.decode`.

It's possible to pass a token to the Decoder and get an empty string in response, this is fine it just means that that token doesn't represent a full printable character, and it'll be along with the next token.

### Dependencies

The Python library uses a subprocess called `tokenmonsterserver` which runs in the background to tokenize and decode, this is downloaded automatically the first time you use the library. The `tokenmonsterserver` binary is located in the tokenmonster directory, which is `~/_tokenmonster` by default, but you can set it elsewhere with the `TokenMonster.set_local_directory` function before loading the first vocabulary.

### Help to integrate with Hugging Face

It's my intention for this library to integrate directly into Hugging Face Transformers. However, Hugging Face's tokenizer classes don't make much sense to me. If you can help explain to me which features are necessary and which are not, please start a discussion or issue on here.
