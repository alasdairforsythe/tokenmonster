## Go Usage

```
import "github.com/alasdairforsythe/tokenmonster/go"

func usage_example() {

	vocab, err := tokenmonster.LoadVocabFromFile(vocabfilename)
	if err != nil {
		panic(err)
	}

	tokens, missing, err := vocab.Tokenize(text)
	if err != nil {
		panic(err)
	}
	
	decoder := vocab.NewDecoder()
	decoded_text := decoder.Detokenize(tokens)
	
}

```

You should use a NewDecoder for each "reply" or "file" or whatever the thing is that you are detokenizing. You can pass the tokens into the Decoder together or one at a time, and it will detokenize it correctly. The Decoder object ensures you get a valid sequence of bytes for UTF-8 or UTF-16 encoding, and also to remember the capcode state. It's possible to pass a token to the Decoder and get an empty string in response, this is fine it just means that that token doesn't represent a printable character and it'll be along with the next token.

`missing` is the number of bytes for which there were no tokens, which will always be `0` with the prebuilt vocabularies because they use `-reserve-256-bytes` during training.

`text` must be a slice of bytes. If you are using UTF-16 encoding, that slice of bytes should be already UTF-16 encoded.

`decoded_text` will be also a slice of bytes in the charset encoding. If you are using UTF-8 encoding you can convert it to a string with `string()`.
