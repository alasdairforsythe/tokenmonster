package main

/*

	This was used to strip non-Latin and invalid characters from the datasets used
	for benchmarking. There's no point testing the performance of the pretrained
	vocabs on those characters because they were specifically excluded from the
	vocabularies during training with -only-latin & -only-valid.

	Note that this doesn't mean the pretained vocabs can't tokenize these characters.
	They can still tokenize them with single byte tokens. The point is that it'd be silly
	to benchmark a vocabulary that was specifically trained for English, against a
	dataset containing Chinese.

*/

import (
	"os"
	"unicode/utf8"
	"unicode"
	"io/ioutil"
	"fmt"
)

const (
	runeError = '\uFFFD'
)

func main() {

	if len(os.Args) < 3 {
		fmt.Println("Usage: ./onlyvalidlatin input.txt output.txt")
		return
	}

	b, err := ioutil.ReadFile(os.Args[1])
	if err != nil {
		fmt.Printf("Error reading file: %s\n", err)
		return
	}

	var r rune
	var n, on int
	out := b

	for len(b) > 0 {
		r, n = utf8.DecodeRune(b)
		if r == runeError || (unicode.IsLetter(r) && !unicode.Is(unicode.Latin, r)) {
			b = b[n:]
			continue
		}
		switch n {
			case 1:
				out[on] = b[0]
				on++
			case 2:
				out[on] = b[0]
				out[on+1] = b[1]
				on+=2
			case 3:
				out[on] = b[0]
				out[on+1] = b[1]
				out[on+2] = b[2]
				on+=3
			case 4:
				out[on] = b[0]
				out[on+1] = b[1]
				out[on+2] = b[2]
				out[on+3] = b[3]
				on+=4
		}
		b = b[n:]
	}

	err = ioutil.WriteFile(os.Args[2], out[0:on], 0644)
	if err != nil {
		panic(err)
	}
	fmt.Println(`Done`, os.Args[2])
}