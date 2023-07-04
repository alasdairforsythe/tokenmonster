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

### tokenmonsterserver

The Python library uses a subprocess called `tokenmonsterserver` which runs in the background to tokenize and decode, this is downloaded automatically the first time you use the library. The `tokenmonsterserver` file is located in the tokenmonster directory, which is `~/_tokenmonster` by default, but you can set it elsewhere with the `TokenMonster.set_local_directory` function before loading the first vocabulary.

### Help to integrate with Hugging Face

It's my intention for this library to integrate directly into Hugging Face Transformers. However, Hugging Face's tokenizer classes don't make much sense to me. If you can help explain to me which features are necessary and which are not, please start a discussion or issue on here.

.
## Full Documentation
1. [Usage](#usage)
2. TokenMonster Methods
    - [TokenMonster.\_\_init\_\_(path)](#tokenmonster__init__path)
    - [TokenMonster.\_\_len\_\_()](#tokenmonster__len__)
	- [vocab.save(fname)](#vocabsavefname)
3. Tokenization & Detokenization
	- [vocab.tokenize(text)](#vocabtokenizetext)
	- [vocab.decode(tokens)](#vocabdecodetokens)
    - [vocab.decoder()](#vocabdecoder)
    - [decoder.decode(tokens)](#decoderdecodetokens)
4. Vocabulary Information
    - [vocab.get_dictionary()](#vocabget_dictionary)
    - [vocab.capcode()](#vocabcapcode)
    - [vocab.charset()](#vocabcharset)
    - [vocab.unk_token_id()](#vocabunk_token_id)
    - [vocab.convert_ids_to_tokens(ids)](#vocabconvert_ids_to_tokensids)
    - [vocab.convert_ids_to_tokens_decoded(ids)](#vocabconvert_ids_to_tokens_decodedids)
    - [vocab.id_to_token(id)](#vocabid_to_tokenid)
    - [vocab.id_to_token_decoded(id)](#vocabid_to_token_decodedid)
	- [vocab.token_to_id(token)](#vocabtoken_to_idtoken)
    - [vocab.convert_tokens_to_ids(tokens)](#vocabconvert_tokens_to_idstokens)
5. Vocabulary Modification
    - [vocab.modify(add_special_tokens, add_regular_tokens=None, delete_tokens=None, resize=None, change_unk=None)](#vocabmodifyadd_special_tokens-add_regular_tokensnone-delete_tokensnone-resizenone-change_unknone)
    - [vocab.add_token(token)](#vocabadd_tokentoken)
    - [vocab.delete_token(token)](#vocabdelete_tokentoken)
    - [vocab.add_special_token(token)](#vocabadd_special_tokentoken)
    - [vocab.resize(size)](#vocabresizesize)
    - [vocab.enable_unk_token()](#vocabenable_unk_token)
    - [vocab.disable_unk_token()](#vocabdisable_unk_token)
6. TokenMonster Class Methods
    - [TokenMonster.set_local_directory(dir=None)](#tokenmonsterset_local_directorydirnone)
    - [TokenMonster.deserialize_tokens(binary_string)](#tokenmonsterdeserialize_tokensbinary_string)
    - [TokenMonster.serialize_tokens(integer_list)](#tokenmonsterserialize_tokensinteger_list)
    - [TokenMonster.disconnect()](#tokenmonsterdisconnect)

## Usage

The main class is `TokenMonster`, which is initialized with a vocabulary from a file, URL or prebuilt vocabulary name.

```python
vocab = TokenMonster("english-32000-balanced-v1")
tokens = vocab.tokenize(str)
decoded_string = vocab.decode(tokens)
```

## TokenMonster Methods

### TokenMonster.\_\_init\_\_(path)

Initialize the TokenMonster object with a vocabulary file or URL.

#### Parameters

- `path` (string): The path to the vocabulary file or URL.

#### Usage

```python
vocab = TokenMonster("filename")
```

### TokenMonster.\_\_len\_\_()

Get the size of the vocabulary.

#### Returns

- `int`: The size of the vocabulary.

#### Usage

```python
vocab = TokenMonster("filename")
number_of_tokens = len(vocab)
```

### vocab.save(fname)

Saves the current vocabulary to a file.

The working directory is not the Python working directory but the TokenMonster default directory.
Specify full filepath if you intend to save elsewhere.

#### Parameters

- `fname` (string): The filename to save the vocabulary to.

#### Returns

- `None`

#### Usage

```python
vocab.save("test.vocab")
```

## Tokenization & Detokenization

### vocab.tokenize(text)

Tokenizes a string into tokens according to the vocabulary.

You can pass a string or a list of strings. If you pass a list of strings they are tokenized
in parallel using as many threads as you supplied strings. Note that if you pass a string
it is converted to a binary string, so if you binary string in the first place, feel
free to pass that instead.

#### Parameters

- `text` (string or list of strings): A string or bytes string, or list of strings or bytes strings.

#### Returns

- `tokens` (int or list of int): The tokens to decode into a string.

#### Usage

```python
tokens = vocab.tokenize(text)
```

### vocab.decode(tokens)

Decodes tokens into a string.

Only use this "decode" method if you are decoding a complete "batch" or complete "conversation" in one go.
For decoding an incomplete batch sequentially (as the tokens become available) instead
use the decoder object.

#### Parameters

- `tokens` (int or list of int): The tokens to decode into a string.

#### Returns

- `string`: The composed string from the input tokens.

#### Usage

```python
decoded_string = vocab.decode(tokens)
```

### vocab.decoder()

Returns a new decoder instance used for decoding tokens into text.

#### Returns

- `TokenMonster.DecoderInstance`: A new decoder instance.

#### Usage

```python
decoder = vocab.decoder()
```

## Vocabulary Information

### vocab.get_dictionary()

Returns a dictionary of all tokens in the vocabulary.

This returns a list where the index of the list is the token ID and the content of each is
"token", "token_decoded", "type" and "score". Note that you should not attempt to use this to
interpret tokenized sequences because the capcode encoded tokens can change the way the next
tokens are decoded. Therefore you should always use one of the two "decode" methods.

#### Returns

- `list`: A list of dictionaries where the index is the token ID and each is a dictionary with the following keys:
  - `token` (string): The token including capcode encoding.
  - `token_decoded` (string): The same token decoded from its capcode form.
  - `type` (int): The type of token (0 = regular, 1 = byte, 2 = special, 3 = UNK).
  - `score` (float): The token's representation in the dataset used to train the vocabulary.

#### Usage

```python
tokens = vocab.get_dictionary()
```

### vocab.capcode()

Returns true if the vocabulary has capcode enabled.

#### Returns

- `bool`: True if capcode is enabled, False otherwise.

### vocab.charset()

Returns the character set used by the vocabulary.

#### Returns

- `string`: The character set used by the vocabulary. Possible values are "UTF-8", "UTF-16", or "None".

### vocab.unk_token_id()

Returns the ID of the UNK token, or 'None' type if there is no UNK token.

#### Returns

- `int or None`: The ID of the UNK token. None if there is no UNK token.

### vocab.convert_ids_to_tokens(ids)

Get the token string from any token ID, in its capcode-encoded form.

#### Parameters

- `ids` (int or list of ints): The token IDs.

#### Returns

- `list of strings`: The token strings corresponding to the input IDs. None type for any IDs that are not in the vocabulary.

### vocab.convert_ids_to_tokens_decoded(ids)

Get the token string from any token IDs, in its capcode-decoded form.

#### Parameters

- `ids` (int or list of ints): The token IDs.

#### Returns

- `list of strings`: The token strings corresponding to the input IDs. None type for any IDs that are not in the vocabulary.

### vocab.id_to_token(id)

Get the token string from a single token ID, in its capcode-encoded form.

#### Parameters

- `id` (int): The token ID.

#### Returns

- `string or None`: The token string corresponding to the input ID. None if the ID is not in the vocabulary.

### vocab.id_to_token_decoded(id)

Get the token string from a single token ID, in its capcode-decoded form.

#### Parameters

- `id` (int): The token ID.

#### Returns

- `string or None`: The token string corresponding to the input ID. None if the ID is not in the vocabulary.

### vocab.token_to_id(token)

Returns the ID of a single token.

This works for both capcode-encoded "raw" tokens and their decoded form.

#### Parameters

- `token` (string): The token to get the ID for.

#### Returns

- `int or None`: The ID of the token. None if the token is not in the vocabulary.

### vocab.convert_tokens_to_ids(tokens)

Returns the IDs of the corresponding tokens. 'None' for any not in the vocabulary.

This works for both capcode-encoded "raw" tokens and their decoded form.

#### Parameters

- `tokens` (string or list of strings): The tokens to convert to IDs.

#### Returns

- `list of ints`: The token IDs corresponding to the input tokens. None type for any tokens that are not in the vocabulary.

## Vocabulary Modification

### vocab.modify(add_special_tokens, add_regular_tokens=None, delete_tokens=None, resize=None, change_unk=None)

Modifies the vocabulary. Doing so produces a new vocabulary with entirely different
ID for each token, including special tokens. It therefore invalidates all decoder
objects associated with the model before modification.

Notes:
- Special tokens are special in that they cannot be skipped. All regular tokens
  that contain specials tokens within them are deleted.
- When resizing the vocabulary down, the worst performing tokens are deleted
  ensuring the vocabulary remains efficient.
- A vocabulary can also be resized up. If any tokens have been removed by deleting
  or resizing, they can be restored by resizing the vocabulary to be larger.
- After modifying you will need to "save" the vocabulary to a file or it'll be
  lost when the script ends.
- delete_tokens can be in either raw or decoded form.

#### Parameters

- `add_special_tokens` (string or list of strings): Special tokens to add to the vocabulary.
- `add_regular_tokens` (string or list of strings): Regular tokens to add to the vocabulary.
- `delete_tokens` (string or list of strings): Regular or Special tokens to delete.
- `resize` (int): Resizes the vocabulary to this size.
- `change_unk` (Boolean): If set, it enables or disables the UNK token.

#### Returns

- `int`: The new size of the vocabulary.

#### Usage

```python
# adds the special token <eos>
vocab.modify("<eos>")
# adds the special token <eos> and keep the vocabulary at the current size
vocab.modify("<eos>", None, None, len(vocab))
```

### vocab.add_token(token)

Add one or more regular tokens. This also changes the token IDs. See "modify".

#### Parameters

- `token` (string or list of strings): The regular tokens to add.

#### Returns

- `int`: The new size of the vocabulary.

### vocab.delete_token(token)

Delete one or more regular or special tokens. This also changes the token IDs. See "modify".
You can give the token in either its encoded or decoded form.

#### Parameters

- `token` (string or list of strings): The tokens to delete.

#### Returns

- `int`: The new size of the vocabulary.

### vocab.add_special_token(token)

Add one or more special tokens. This also changes the token IDs. See "modify".

#### Parameters

- `token` (string or list of strings): The special tokens to add.

#### Returns

- `int`: The new size of the vocabulary.

### vocab.resize(size)

Changes the size of the vocabulary. This also changes the token IDs. See "modify".

A vocabulary can be enlarged as well reduced in size. Only the worst performing
tokens are removed when reducing.

#### Parameters

- `size` (int): The new size of the vocabulary.

#### Returns

- `int`: The new size of the vocabulary.

### vocab.enable_unk_token()

Enables the UNK token.

The UNK token can be added or removed without affecting the rest of the vocabulary.
If enabled, the UNK token appears whenever there is a character that is not in the vocabulary.
Notethat the UNK token will not be enabled if all possible characters have tokens.
Use get_unk_token to retrieve the ID for the UNK token.

#### Returns

- `int`: The new size of the vocabulary.

### vocab.disable_unk_token()

Disables the UNK token.

The UNK token can be added or removed without affecting the rest of the vocabulary.
Without an UNK token, any character for which there is no token is ignored during tokenization.

#### Returns

- `int`: The new size of the vocabulary.

## TokenMonster.DecoderInstance

A nested class for decoding streams of tokens in sequence.

This class takes tokens and decodes them to generate human-readable strings.

## Usage

```python
vocab = TokenMonster("english-32000-balanced-v1")
decoder = vocab.decoder()
decoded_string = decoder.decode(tokens)
decoded_string += decoder.decode(more_tokens)
```

### decoder.decode(tokens)

A decoder object used for decoding token streams.

This decoder object is used instead of the vocabulary decode method when you are
decoding tokens in small segments, or one by one, that are part of a longer
stream of encoded tokens. A new decoder object should be used for each
stream, then deleted. If you are decoding all tokens in one call, instead of
in multiple calls, then you can use the vocabulary decode method directly.

#### Parameters

- `tokens` (int or list of ints): A token ID or list of token IDs.

#### Returns

- `string`: A human-readable string derived from the input tokens.

#### Usage

```python
vocab = TokenMonster("english-32000-balanced-v1")
decoder = vocab.Decoder()
decoded_string = decoder.decode(tokens)
decoded_string += decoder.decode(more_tokens)
```

## TokenMonster Class Methods

### TokenMonster.set_local_directory(dir=None)

Sets the local directory for TokenMonster.

If no directory is specified, the default directory is ~/\_tokenmonster

#### Parameters

- `dir` (string): The local directory to use.

### TokenMonster.deserialize_tokens(binary_string)

Deserializes a binary string back into a list of ints (tokens).
The encoding_length needs to be recorded separately.

#### Parameters

- `binary_string` (bytes): The binary string to deserialize.

#### Returns

- `list of ints`: The deserialized tokens.

### TokenMonster.serialize_tokens(integer_list)

Serializes tokens from a list of ints into a binary string.
The encoding_length needs to be recorded separately.

#### Parameters

- `integer_list` (list of ints): The tokens to serialize.

#### Returns

- `bytes`: The serialized binary string.

### TokenMonster.disconnect()

Disconnects and closes tokenmonsterserver.

#### Returns

- `None`
.
