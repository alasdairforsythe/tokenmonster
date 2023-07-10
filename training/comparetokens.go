/*

	Lists the difference between two tokens files.
	./comparetokens input1 input2

*/

package main

import (
	"os"
	"fmt"
	"github.com/AlasdairF/Custom"
	"github.com/alasdairforsythe/pansearch"
)

func loadTokensFromFile(filename string) ([][]byte, error) {
	fi, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer fi.Close()
	r := custom.NewZlibReader(fi)
	r.ReadByte()
	r.ReadByte()
	r.ReadByte()
	r.ReadByte()
	r.ReadByte()
	r.ReadByte()
	r.ReadByte()
	r.ReadByte()
	l := int(r.ReadUint64())
	data := make([][]byte, l)
	for i:=0; i<l; i++ {
		data[i] = r.ReadBytes8()
	}
	return data, nil
}

func main() {
	if len(os.Args) != 3 {
		fmt.Println(`Usage: ./compare one.tok two.tok`)
	}
	var err error
	var tokens1, tokens2 [][]byte
	tokens1, err = loadTokensFromFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	tokens2, err = loadTokensFromFile(os.Args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	counter := new(pansearch.Counter)
	for _, tok := range tokens1 {
		counter.Add(tok, 1)
	}
	counter.Build_Multithreaded()

	counter2 := new(pansearch.Counter)
	for _, tok := range tokens2 {
		counter2.Add(tok, 1)
	}
	counter2.Build_Multithreaded()
	
	fmt.Println(os.Args[1])
	var exists bool
	for _, b := range tokens1 {
		if _, exists = counter2.Find(b); !exists {
			fmt.Println("    '" + string(b)+`'`)
		}
	}
	fmt.Println()
	fmt.Println(os.Args[1])
	for _, b := range tokens2 {
		if _, exists = counter.Find(b); !exists {
			fmt.Println("    '" + string(b)+`'`)
		}
	}
	fmt.Println()
}
