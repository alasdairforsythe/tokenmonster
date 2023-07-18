## Python Usage

You can take the script from here or install it with PyPI:
```
pip install tokenmonster
```

### Basic usage

```python
import tokenmonster

# Optionally set the tokenmonster directory, otherwise it will use ~/_tokenmonster
tokenmonster.set_local_directory("/path/to/preferred")

# Load a vocabulary by name, filepath or URL
vocab = tokenmonster.load("english-24000-consistent-v1")

# Tokenize some text
text = "Some text to turn into token IDs."
tokens = vocab.tokenize(text)
```

Then to detokenize:
```python
decoder = vocab.decoder()
decoded_text = decoder.decode(tokens)
```

There is a `decode` function for both the vocabulary object `vocab.decode()`, and also the decoder object that is made with `vocab.decoder()`. The difference is that the decoder object is meant for when you are individually decoding a sequence of IDs that are part of the same generation sequence, e.g. decoding tokens as they are generating. If you already have the full sequence and intend to decode it all in one go, you can use `vocab.decode`.

It's possible to pass a token to the Decoder and get an empty string in response. This is fine, it means that token doesn't represent a full printable character, for example it's the first part of a multipart UTF-8 character, or it's capcode uppercase marker meant to influence the next token. It's for this reason that the decoder object is used.

### tokenmonsterserver

The Python library uses a subprocess called `tokenmonsterserver` which runs in the background to tokenize and decode, this is downloaded automatically the first time you use the library. The `tokenmonsterserver` file is located in the tokenmonster directory, which is `~/_tokenmonster` by default, but you can set it elsewhere with the `TokenMonster.set_local_directory` function before loading the first vocabulary.

### Parallel Processing

To tokenize with multiple threads, you can pass a list of strings to `tokenize` and each will be tokenized in parallel and returned together. What is **not currently supported** is sharing the same vocab instance between multiple threads, for example with Hugging Face `datasets` `.map(num_proc=x)` or similar.

.
## Full Documentation
1. [Usage](#usage)
2. Loading & Exporting
    - [tokenmonster.load(path)](#tokenmonsterloadpath)
    - [tokenmonster.new(yaml)](#tokenmonsternewyaml)
    - [vocab.save(fname)](#vocabsavefname)
    - [vocab.export_yaml(order_by_score=False)](#vocabexport_yamlorder_by_scorefalse)
3. Tokenization & Detokenization
    - [vocab.tokenize(text)](#vocabtokenizetext)
    - [vocab.tokenize_count(text)](#vocabtokenize_counttext)
    - [vocab.decode(tokens)](#vocabdecodetokens)
    - [vocab.decoder()](#vocabdecoder)
    - [decoder.decode(tokens)](#decoderdecodetokens)
4. Vocabulary Information
    - [len(vocab)](#lenvocab)
    - [vocab.get_dictionary()](#vocabget_dictionary)
    - [vocab.charset()](#vocabcharset)
    - [vocab.normalization()](#vocabnormalization)
    - [vocab.capcode()](#vocabcapcode)
    - [vocab.mode()](#vocabmode)
    - [vocab.unk_token_id()](#vocabunk_token_id)
    - [vocab.id_to_token(id)](#vocabid_to_tokenid)
    - [vocab.id_to_token_decoded(id)](#vocabid_to_token_decodedid)
    - [vocab.token_to_id(token)](#vocabtoken_to_idtoken)
5. Vocabulary Modification
    - [vocab.modify(add_special_tokens, add_regular_tokens=None, delete_tokens=None, resize=None, change_unk=None)](#vocabmodifyadd_special_tokens-add_regular_tokensnone-delete_tokensnone-resizenone-change_unknone-reset_token_idsfalse)
    - [vocab.add_token(token)](#vocabadd_tokentoken)
    - [vocab.delete_token(token)](#vocabdelete_tokentoken)
    - [vocab.delete_token_by_id(id)](#vocabdelete_token_by_idid)
    - [vocab.add_special_token(token)](#vocabadd_special_tokentoken)
    - [vocab.resize(size)](#vocabresizesize-reset_token_idsfalse)
    - [vocab.reset_token_ids()](#vocabreset_token_ids)
    - [vocab.enable_unk_token()](#vocabenable_unk_token)
    - [vocab.disable_unk_token()](#vocabdisable_unk_token)
6. Other
    - [del](#del)
    - [tokenmonster.set_local_directory(dir=None)](#tokenmonsterset_local_directorydirnone)
    - [tokenmonster.disconnect()](#tokenmonsterdisconnect)
    - [vocab.serialize_tokens(integer_list)](#vocabserialize_tokensinteger_list)
    - [vocab.deserialize_tokens(binary_string)](#vocabdeserialize_tokensbinary_string)

## Usage

```python
vocab = tokenmonster.load("english-32000-balanced-v1")
tokens = vocab.tokenize(str)
decoded_string = vocab.decode(tokens)
```

## TokenMonster Methods

### tokenmonster.load(path)

Loads a TokenMonster vocabulary from file, URL or by name.

#### Parameters

- `path` (string): A filepath, URL or pre-built vocabulary name.

#### Returns

- `Vocab`: An instance of tokenmonster.Vocab.

#### Usage

```python
vocab = tokenmonster.load("english-32000-balanced-v1")
```

### tokenmonster.new(yaml)

Creates a new vocabulary from a YAML string.
A sample YAML file can be found here: https://github.com/alasdairforsythe/tokenmonster/yaml_guide
You should save it in the vocab format with `vocab.save()` for future use.

#### Parameters

- `yaml` (string or bytes string): The YAML file.

#### Returns

- `Vocab`: An instance of tokenmonster.Vocab class.

#### Usage

```python
vocab = tokenmonster.new(yaml_string)
vocab.save(filename)
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

### vocab.export_yaml(order_by_score=False)

Exports the vocabulary as a YAML file, which is returned as a bytes string.

#### Parameters

- `order_by_score` (boolean): If true the tokens are order by score instead of alphabetically.

#### Returns

- `YAML` (bytes string): The vocabulary in YAML format.

#### Usage

```python
yaml = vocab.export_yaml()
with open(file_path, 'wb') as file:
  file.write(yaml)
```

## Tokenization & Detokenization

### vocab.tokenize(text)

Tokenizes a string into tokens according to the vocabulary.

You can pass a string or a list of strings. If you pass a list of strings they are tokenized
in parallel using as many threads as the list size. Note that if you pass a string
it is converted to a binary string, so if you have binary string in the first place, feel
free to pass that instead.

#### Parameters

- `text` (string or list of strings): A string or bytes string, or list of strings or bytes strings.

#### Returns

- `tokens` (numpy array or list of numpy array): The tokens IDs

#### Usage

```python
tokens = vocab.tokenize(text)
```

### vocab.tokenize_count(text)

Same as tokenize, but it returns only the number of tokens.

The number of tokens is the same as you would get from `tokenize`. If you want to count any characters
for which there are no tokens or single byte tokens, you should `enable_unk_token()`. It's okay to
enable `enable_unk_token()`, run `tokenize_count` and then `disable_unk_token()`.

#### Parameters

- `text` (string or list of strings): A string or bytes string, or list of strings or bytes strings.

#### Returns

- `n_tokens` (int or list of ints): The number of tokens for each input string

#### Usage

```python
number_of_tokens = vocab.tokenize_count(text)
```

### vocab.decode(tokens)

Decodes tokens into a string.

Only use this "decode" method if you are decoding a complete "batch" or complete "conversation" in one go.
For decoding an incomplete batch sequentially (as the tokens become available) instead
use the decoder object.

#### Parameters

- `tokens` (int, list of int, or numpy array): The tokens to decode into a string.

#### Returns

- `string`: The composed string from the input tokens.

#### Usage

```python
decoded_string = vocab.decode(tokens)
```

### vocab.decoder()

Returns a new decoder instance used for decoding tokens into text.

#### Returns

- `tokenmonster.DecoderInstance`: A new decoder instance.

#### Usage

```python
decoder = vocab.decoder()
```

## Vocabulary Information

### len(vocab)

Get the size of the vocabulary.

#### Returns

- `int`: The size of the vocabulary.

#### Usage

```python
vocab = tokenmonster.load("filename")
number_of_tokens = len(vocab)
```

### vocab.get_dictionary()

Returns a dictionary of all tokens in the vocabulary.

This returns a list of dictionaries with keys "id", "token", "token_decoded", "type" and "score".
Note that you should not attempt to use this to interpret tokenized sequences because the capcode
encoded tokens can change the way the next tokens are decoded. Therefore you should always use
one of the two "decode" methods.

#### Returns

- `list`: A list of dictionaries where the index is the token ID and each is a dictionary with the following keys:
  - `id` (int): The ID of the token.
  - `token` (string): The token including capcode encoding.
  - `token_decoded` (string): The same token decoded from its capcode form.
  - `type` (int): The type of token (0 = regular, 1 = byte, 2 = special, 3 = UNK).
  - `score` (float): The token's representation in the dataset used to train the vocabulary.

#### Usage

```python
tokens = vocab.get_dictionary()
```

### vocab.charset()

Returns the character set used by the vocabulary.

#### Returns

- `string`: The character set used by the vocabulary. Possible values are "UTF-8", "None".

### vocab.normalization()

Returns the normalization of the vocabulary.

#### Returns

- `string`: The normalization of the vocabulary. Possible values are "None", "NFD", "Lowercase", "Accents", "Quotemarks", "Collapse", "Trim", "LeadingSpace", "UnixLines".

### vocab.capcode()

Returns the capcode level of the vocabulary.
- 0 = disabled
- 1 = only deleteToken
- 2 = enabled

#### Returns

- `int`: The capcode level (0-2).

### vocab.mode()

Returns the optimization mode of the vocabulary.
- 0 = unfiltered
- 1 = clean
- 2 = balanced
- 3 = consistent
- 4 = strict
- 5 = (vocabulary was not trained with TokenMonster)

#### Returns

- `int`: The optimization mode (0-5).

### vocab.unk_token_id()

Returns the ID of the UNK token, or 'None' type if there is no UNK token.

#### Returns

- `int or None`: The ID of the UNK token. None if there is no UNK token.

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

## Vocabulary Modification

### vocab.modify(add_special_tokens, add_regular_tokens=None, delete_tokens=None, resize=None, change_unk=None, reset_token_ids=False)

Modifies the vocabulary. Doing so invalidates all decoder objects associated with the
model before modification.

Notes:
- Special tokens are special in that they cannot be skipped. All regular tokens
  that contain specials tokens within them are deleted.
- When resizing the vocabulary down, the worst performing tokens are deleted
  ensuring the vocabulary remains efficient. However, only regular tokens
  with a score > 0 are can be removed by resizing.
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
- `change_unk` (boolean): If set, it enables or disables the UNK token.
- `reset_token_ids` (boolean): If true the IDs are all reset starting from zero.

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

Add one or more regular tokens.

#### Parameters

- `token` (string or list of strings): The regular tokens to add.

#### Returns

- `int`: The new size of the vocabulary.

### vocab.delete_token(token)

Delete one or more regular or special tokens.
You can give the token in either its encoded or decoded form.

#### Parameters

- `token` (string or list of strings): The tokens to delete.

#### Returns

- `int`: The new size of the vocabulary.

### vocab.delete_token_by_id(id)

Delete one or more regular or special token by specifying the token ID.

#### Parameters

- `id` (int or list of ints): The IDs of the tokens to delete.

#### Returns

- `int`: The new size of the vocabulary.

### vocab.add_special_token(token)

Add one or more special tokens.

#### Parameters

- `token` (string or list of strings): The special tokens to add.

#### Returns

- `int`: The new size of the vocabulary.

### vocab.resize(size, reset_token_ids=False)

Changes the size of the vocabulary and optionally resets the token IDs.

A vocabulary can be enlarged as well reduced in size. Only the worst performing
tokens are removed when reducing.

Resizing only removes regular tokens that are not single byte token and have
score > 0. If there are not enough of these, the new size may not match
the target size.

#### Parameters

- `size` (int): The new size of the vocabulary.
- `reset_token_ids` (boolean): If true, the IDs of all tokens are reset from zero.

#### Returns

- `int`: The new size of the vocabulary.

### vocab.reset_token_ids()

Resets the token IDs to be sequential beginning from zero.

If tokens have been deleted from the vocabulary there will be gaps in the token IDs.
Resetting the token IDs removes these gaps but all tokens will have new IDs.

### vocab.enable_unk_token()

Enables the UNK token.

If enabled, the UNK token appears whenever there is a character that is not in the vocabulary.
Note that the UNK token will not be enabled if all possible characters have tokens.
Use `vocab.unk_token_id()` to retrieve the ID for the UNK token.

#### Returns

- `int`: The new size of the vocabulary.

### vocab.disable_unk_token()

Disables the UNK token.

Without an UNK token, any character for which there is no token is ignored during tokenization.

#### Returns

- `int`: The new size of the vocabulary.

## TokenMonster.DecoderInstance

A nested class for decoding streams of tokens in sequence.

This class takes tokens and decodes them to generate human-readable strings.

## Usage

```python
vocab = tokenmonster.load("english-32000-balanced-v1")
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

- `tokens` (int, list of ints, or numpy array): A token ID or list of token IDs.

#### Returns

- `string`: A human-readable string derived from the input tokens.

#### Usage

```python
vocab = tokenmonster.load("english-32000-balanced-v1")
decoder = vocab.decoder()
decoded_string = decoder.decode(tokens)
decoded_string += decoder.decode(more_tokens)
```

## Other

### del

Once you are finished with a vocab or decoder object, to free it from memory
use the `del` syntax. This is worthwhile if you are creating many
temporary decoder objects.

#### Usage

```python
vocab = tokenmonster.load("english-32000-balanced-v1")
del vocab
```

### tokenmonster.set_local_directory(dir=None)

Sets the local directory for TokenMonster.

If no directory is specified, the default directory is ~/\_tokenmonster

#### Parameters

- `dir` (string): The local directory to use.

#### Usage

```python
tokenmonster.set_local_directory("/path/to/preferred")
```

### tokenmonster.disconnect()

Disconnects and closes tokenmonsterserver.

#### Returns

- `None`

### vocab.serialize_tokens(integer_list)

Serializes tokens from a list of ints or numpy array into a binary string.
The `encoding_length` used is from vocab.encoding_length.

#### Parameters

- `integer_list` (list of ints or numpy array): The tokens to serialize.

#### Returns

- `bytes`: The serialized binary string.

### vocab.deserialize_tokens(binary_string)

Deserializes a binary string into a numpy array of token IDs.
The `encoding_length` used is from vocab.encoding_length.

#### Parameters

- `binary_string` (bytes): The binary string to deserialize.

#### Returns

- `np.array`: The deserialized tokens.
.
