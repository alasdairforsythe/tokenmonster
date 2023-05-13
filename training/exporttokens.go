package main

import (
	"os"
	"github.com/AlasdairF/Custom"
	"errors"
	"fmt"
)

func save_dict_txt(filename string, data [][]byte) error {
	fi, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer fi.Close()
	w := custom.NewWriter(fi)
	defer w.Close()
	for _, b := range data {
		w.Write(b)
		w.WriteByte('\n')
	}
	return nil
}

func load_dict(filename string) ([][]byte, error) {
	fi, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer fi.Close()
	r := custom.NewZlibReader(fi)
	l := int(r.ReadUint64())
	data := make([][]byte, l)
	for i:=0; i<l; i++ {
		data[i] = r.ReadBytes8()
	}
	if r.EOF() != nil {
		return nil, errors.New(`Not a valid structure.`)
	}
	return data, nil
}

func save_tokens(filename string, data [][]byte) error {
	fi, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer fi.Close()
	w := custom.NewWriter(fi)
	defer w.Close()
	w.WriteUint64(uint64(len(data)))
	for _, b := range data {
		w.WriteBytes8(b)
	}
	return nil
}

func main() {
	if len(os.Args) < 3 {
		panic(errors.New("Insufficient command line arguments"))
	}
	inputFilename := os.Args[1]
	outputFilename := os.Args[2]
	tokens, err := load_dict(inputFilename)
	if err != nil {
		panic(err)
	}
	fmt.Println(len(tokens), `tokens`)
	save_dict_txt(outputFilename + `.txt`, tokens)
	save_tokens(outputFilename + `.bin`, tokens)
}