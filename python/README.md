## Python Usage

I haven't yet made a PyPI package, so for now you will just have to download tokenmonster.py file from here, and also capcode.py from [here](https://github.com/alasdairforsythe/capcode/tree/main/python).

To load a vocabulary:
```python
vocab = TokenMonster.load(local_path, remote_path)
```
remote_path is an optional argument. If the local_path does not exist it will attempt to download the vocabulary file from remote_path and then save it to local_path.

To tokenize some text:
```python
tokens = vocab.tokenize(text)
```
That will return an array of integers which are the token IDs representing the text.

Then to detokenize:
```python
decoder = vocab.decoder()
decoded_text = decoder.detokenize(tokens)
```
You should use a new `decoder` object for each "reply" or "file" or whatever the thing is that you are detokenizing. You can pass the tokens into the Decoder together or one at a time, and it will detokenize it correctly. The Decoder object ensures you get a valid sequence of bytes for UTF-8 or UTF-16 encoding, and also to remember the capcode state. It's possible to pass a token to the Decoder and get an empty string in response, this is fine it just means that that token doesn't represent a printable character and it'll be along with the next token.