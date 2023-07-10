/*

	Merges two tokens files into one.
	./mergetokens input1 input2 merged

*/

package main

import (
	"os"
	"fmt"
	"errors"
	"github.com/AlasdairF/Custom"
	"github.com/alasdairforsythe/pansearch"
	"github.com/alasdairforsythe/capcode/go"
	"github.com/alasdairforsythe/norm"
)

var (
	usingCapcode uint8
	charsetFlag uint8
	normalizer norm.Normalizer
	level uint8
	reserve uint8
)

func applyCapcode(data []byte) []byte {
	if usingCapcode == 2 {
		return capcode.Encode(data)
	} else if usingCapcode == 1 {
		return capcode.NoCapcodeEncode(data)
	}
	return data
}

func normalize(data []byte) []byte {
	processed, err := normalizer.Normalize(data)
	if err == nil {
		return applyCapcode(processed)
	} else { // if failed try it the other way around
		if !normalizer.SpecifiedLowercase() {
			processed = applyCapcode(data)
			processed, err = normalizer.Normalize(processed)
			if err != nil {
				panic(err)
			}
		} else {
			panic(err)
		}
	}
	return processed
}

func saveTokensToFile(filename string, data [][]byte) error {
	fi, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer fi.Close()
	w := custom.NewZlibWriter(fi)
	defer w.Close()
	w.WriteByte(usingCapcode)
	w.WriteByte(charsetFlag)
	w.WriteByte(normalizer.Flag)
	w.WriteByte(level)
	w.WriteByte(reserve)
	w.WriteByte(0) // reserved
	w.WriteByte(0) // reserved
	w.WriteByte(0) // reserved
	w.WriteUint64(uint64(len(data)))
	for _, b := range data {
		w.WriteBytes8(b)
	}
	return nil
}

func loadTokensFromFile(filename string) (uint8, uint8, uint8, uint8, uint8, [][]byte, error) {
	fi, err := os.Open(filename)
	if err != nil {
		return 0, 0, 0, 0, 0, nil, err
	}
	defer fi.Close()
	r := custom.NewZlibReader(fi)
	_usingCapcode := r.ReadByte()
	_charsetFlag := r.ReadByte()
	_norm := r.ReadByte()
	_level := r.ReadByte()
	_reserve := r.ReadByte()
	r.ReadByte()
	r.ReadByte()
	r.ReadByte()
	l := int(r.ReadUint64())
	data := make([][]byte, l)
	for i:=0; i<l; i++ {
		data[i] = r.ReadBytes8()
	}
	// Make sure we're at the end
	if r.EOF() != nil { // it can be longer if it includes scores, so just do a quick sanity check
		if _charsetFlag > 2 || _level > 5 || len(data[0]) > 40 || len(data[1]) > 40 || len(data[len(data)-1]) > 40 {
			return 0, 0, 0, 0, 0, nil, errors.New(filename + ` not valid tokens file.`)
		}
	}
	return _usingCapcode, _charsetFlag, _norm, _level, _reserve, data, nil
}

func main() {
	if len(os.Args) <= 2 {
		fmt.Println(`Usage: ./mergetokens input1 input2 merged`)
	}
	var err error
	var outfile string
	var tokens1, tokens2 [][]byte
	usingCapcode, charsetFlag, normalizer.Flag, level, reserve, tokens1, err = loadTokensFromFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if os.Args[1] == os.Args[2] {
		fmt.Println(`Output filename must be a separate file to the input.`)
		os.Exit(1)
	}
	if len(os.Args) == 4 {
		if os.Args[2] == os.Args[3] {
			fmt.Println(`Output filename must be a separate file to the input.`)
			os.Exit(1)
		}
		outfile = os.Args[3]
		_, _, _, _, _, tokens2, err = loadTokensFromFile(os.Args[2])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	} else {
		outfile = os.Args[2]
	}
	counter := new(pansearch.Counter)
	for _, tok := range tokens1 {
		counter.Add(tok, 1)
	}
	for _, tok := range tokens2 {
		counter.Add(tok, 1)
	}
	if level < 4 {
		for _, v := range extraTokens {
			counter.Add(normalize([]byte(v)), 1)
			counter.Add(normalize([]byte(" " + string(v))), 1)
			if v[len(v)-1] == '/' {
				counter.Add([]byte(string(v) + "D"), 1)
			}
		}
	}
	counter.Build_Multithreaded()
	err = saveTokensToFile(outfile, counter.Keys())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(`Merged:`, outfile)
}

var extraTokens = []string{}
