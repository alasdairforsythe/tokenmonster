**[Click here for the complete documentation on pkg.go.dev.](https://pkg.go.dev/github.com/alasdairforsythe/tokenmonster/go)**

## Basic Usage

```
import "github.com/alasdairforsythe/tokenmonster/go"

func example() {

	vocab, err := tokenmonster.Load(vocabfilename)
	if err != nil {
		panic(err)
	}

	tokens, missing, err := vocab.Tokenize(text)
	if err != nil {
		panic(err)
	}
	
	decoder := vocab.NewDecoder()
	decoded_text := decoder.Decode(tokens)

}
```

`missing` is the number of bytes for which there were no tokens.

`text` must be a slice of bytes. If you are using UTF-16 encoding, that slice of bytes should be already UTF-16 encoded.

`decoded_text` will be also a slice of bytes in the charset encoding. If you are using UTF-8 encoding you can convert it to a string with `string()`.
