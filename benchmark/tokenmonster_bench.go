package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"time"
	"github.com/alasdairforsythe/tokenmonster/go"
)

func main() {
	// Check if a command-line argument is provided
	if len(os.Args) < 3 {
		fmt.Println("Usage: ./tokenmonster_bench vocabfile textfile")
		return
	}

	vocab, err := tokenmonster.Load(os.Args[1])
	if err != nil {
		fmt.Println(err)
		return
	}

	var totalSize float64
	var totalelapsed float64
	var totaltokens int

	for i:=2; i<len(os.Args); i++ {
		fmt.Println(os.Args[i])
		// Read the file content into memory
		content, err := ioutil.ReadFile(os.Args[i])
		if err != nil {
			fmt.Printf("Error reading file: %s\n", err)
			return
		}

		startTime := time.Now()
		tokens, _, err := vocab.Tokenize(content)
		elapsed := time.Since(startTime)
		totaltokens += len(tokens)

		// Print the elapsed time
		fmt.Printf("Tokens: %d\n", len(tokens))
		fmt.Printf("Elapsed time: %s\n", elapsed)

		// Calculate speed in MB/s
		contentSizeMB := float64(len(content)) / 1024 / 1024
		elapsedSeconds := elapsed.Seconds()
		speed := contentSizeMB / elapsedSeconds
		totalSize += contentSizeMB
		totalelapsed += elapsedSeconds

		fmt.Printf("Speed: %.2f MB/s\n", speed)
	}

		// Total
		fmt.Printf("Total tokens: %d\n", totaltokens)
		fmt.Printf("Total elapsed time: %s\n", totalelapsed)
		fmt.Printf("Total speed: %.2f MB/s\n", totalSize / totalelapsed)

	
}
