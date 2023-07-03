package main

import (
	"os"
	"fmt"
	"flag"
	"bufio"
	"errors"
	"strconv"
	"strings"
	"unicode"
	"io/ioutil"
	"path/filepath"
	"encoding/json"
	"github.com/AlasdairF/Conv"
	"github.com/AlasdairF/Custom"
	"github.com/AlasdairF/Sort/Uint32Float32"
	"github.com/alasdairforsythe/tokenmonster/go"
)

func saveAsTxt(filename string, data [][]byte, delimiter string) error {
	fi, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer fi.Close()
	w := custom.NewWriter(fi)
	defer w.Close()
	for _, b := range data {
		w.Write(b)
		w.WriteString(delimiter)
	}
	return nil
}

func loadTokensFromFile(filename string) (bool, uint8, uint8, uint8, bool, [][]byte, []float32, error) {
	fi, err := os.Open(filename)
	if err != nil {
		return false, 0, 0, 0, false, nil, nil, err
	}
	defer fi.Close()
	r := custom.NewZlibReader(fi)
	_usingCapcode := r.ReadBool()
	_charsetFlag := r.ReadByte()
	_level := r.ReadByte()
	_reserve := r.ReadByte()
	_customIDs := r.ReadBool()
	l := int(r.ReadUint64())
	data := make([][]byte, l)
	for i:=0; i<l; i++ {
		data[i] = r.ReadBytes8()
	}
	// Make sure we're at the end
	var scores []float32
	if r.EOF() != nil {
		scores = make([]float32, l)
		for i:=0; i<l; i++ {
			scores[i] = r.ReadFloat32()
		}
		if r.EOF() != nil {
			return false, 0, 0, 0, false, nil, nil, errors.New(filename + ` not valid`)
		}
	}
	return _usingCapcode, _charsetFlag, _level, _reserve, _customIDs, data, scores, nil
}

func saveTokensToFile(filename string, data [][]byte, scores []float32, usingCapcode bool, charsetFlag uint8, level uint8, reserve uint8, customIDs bool) error {
	fi, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer fi.Close()
	w := custom.NewZlibWriter(fi)
	w.WriteBool(usingCapcode)
	w.WriteByte(charsetFlag)
	w.WriteByte(level)
	w.WriteByte(reserve)
	w.WriteBool(customIDs)
	defer w.Close()
	w.WriteUint64(uint64(len(data)))
	for _, b := range data {
		w.WriteBytes8(b)
	}
	if len(scores) == len(data) {
		for _, v := range scores {
			w.WriteFloat32(v)
		}
	}
	return nil
}

func die(msg string, showUsage bool) {
	fmt.Fprintln(os.Stderr, msg)
	if showUsage {
		flag.Usage()
	}
	os.Exit(1)
}

func main() {

	var resize int
	var inputFilename, outputFilename, textFilename, modifyFilename, inputVocab, addSingleBytes, tokensFilename, addSpecialToken, setUnk, exists string
	var usingCapcode, excludeOtherBytes, orderByScore bool
	var charsetFlag, level, reserve, reserve2 uint8
	var tokens, specialTokens, addTokens, deleteTokens [][]byte
	var scores []float32
	var err error
	var delimiter string = "\n"

	flag.StringVar(&inputVocab, "input-vocab", inputVocab, "an existing TokenMonster vocabulary file (optional)")
	flag.StringVar(&inputFilename, "input", inputFilename, "tokens file or directory from trainvocab, if directory it will load the best performing tokens file in the directory (optional)")
	flag.StringVar(&outputFilename, "output", outputFilename, "filename of the vocabulary to output (optional)")
	flag.StringVar(&textFilename, "output-txt", textFilename, "filename to export tokens in a text file for curiosity (optional)")
	flag.StringVar(&tokensFilename, "output-tokens", tokensFilename, "converts a vocabulary back to a tokens file that can be used with trainvocab (optional)")
	flag.StringVar(&modifyFilename, "input-json", modifyFilename, "filename of a JSON file containing tokens to add or delete, format: {\"add\":[\"ab\",\"cd\"],\"special\":[\"</eos>\"],\"delete\":[\"cheese\"]} (optional)")
	flag.StringVar(&addSingleBytes, "add-single-bytes", addSingleBytes, "enter \"256\", \"128\", \"ascii\" or \"utf8\" to add tokens for those individual bytes (optional)")
	flag.BoolVar(&excludeOtherBytes, "delete-single-bytes", excludeOtherBytes, "deletes all the single byte tokens except those specified from add-single-bytes (optional)")
	flag.StringVar(&delimiter, "delimiter", delimiter, "delimiter to use between each token for output-txt (optional)")
	flag.IntVar(&resize, "resize", resize, "resizes the vocabulary to this many tokens by deleting the worst scoring tokens (optional)")
	flag.BoolVar(&orderByScore, "order-by-score", orderByScore, "orders output-txt by token score (descending) instead of alphabetically (optional) (default false)")
	flag.StringVar(&addSpecialToken, "add-special-token", addSpecialToken, "a single special token to add to the vocabulary (optional)")
	flag.StringVar(&exists, "exists", exists, "check if a token exists in the vocabulary (optional)")
	flag.StringVar(&setUnk, "unk", setUnk, "set to true or false to enable or disable the UNK token (optional)")
	flag.Parse()
	if len(inputFilename) == 0 && len(modifyFilename) == 0 && len(inputVocab) == 0 {
		flag.Usage()
		os.Exit(0)
	}
	if len(inputFilename) > 0 && len(inputVocab) > 0 {
		die("You cannot input both a vocabulary and a tokens file at the same time.", true)
	}
	setUnk = strings.TrimSpace(strings.ToLower(setUnk))
	if len(setUnk) > 0 {
		setUnk = string(setUnk[0])
	}
	if len(addSingleBytes) > 0 {
		switch strings.ToLower(addSingleBytes) {
			case `256`:
				reserve |= 1 << 0
			case `128`:
				reserve |= 1 << 1
			case `utf8`:
				fallthrough
			case `utf-8`:
				reserve |= 1 << 2
			case `ascii`:
				reserve |= 1 << 3
			default:
				die("Error: add-single bytes must be one of \"256\", \"128\", \"ascii\" or \"utf8\"", true)
		}
	}
	if excludeOtherBytes {
		if reserve == 0 {
			reader := bufio.NewReader(os.Stdin)
			for {
				fmt.Printf("Your settings will delete all single byte tokens. Are you sure? (y/n)\n")
				text, _ := reader.ReadString('\n')
				text = strings.Replace(text, "\n", "", -1)
		
				if strings.ToLower(text) == "y" {
					fmt.Println("Confirmed")
					break
				} else if strings.ToLower(text) == "n" {
					fmt.Println("Closing")
					os.Exit(0)
				} else {
					fmt.Println("Please respond with 'y' or 'n'")
				}
			}
		}
		reserve |= 1 << 4
	}
	if delimiter != "\n" {
		if len(delimiter) >= 1 {
			b := delimiter[0]
			if b != '"' && b != '\'' && b != '`' {
				delimiter = "\"" + delimiter + "\""
			}
		}
		delimiter, err = strconv.Unquote(delimiter)
		if err != nil {
			die("Error parsing delimiter: " + err.Error(), false)
		}
	}
	if len(addSpecialToken) > 1 {
		reader := bufio.NewReader(os.Stdin)
		for {
			fmt.Printf("You've entered '%s' as your special token. Is this correct? (y/n)\n", addSpecialToken)
			text, _ := reader.ReadString('\n')
			text = strings.Replace(text, "\n", "", -1)
	
			if strings.ToLower(text) == "y" {
				specialTokens = append(specialTokens, []byte(addSpecialToken))
				fmt.Println("Confirmed")
				break
			} else if strings.ToLower(text) == "n" {
				fmt.Println("Closing")
				os.Exit(0)
			} else {
				fmt.Println("Please respond with 'y' or 'n'")
			}
		}
	}

	// Load tokens file
	if len(inputFilename) != 0 {
		fileInfo, err := os.Stat(inputFilename)
		if err != nil {
			die(err.Error(), false)
		}

		if fileInfo.IsDir() {
			files, err := ioutil.ReadDir(inputFilename)
			if err != nil {
				die(err.Error(), false)
			}

			if len(files) == 0 {
				die("Error: Directory "+inputFilename+" is empty.", false)
			}

			var firstFile string
			var compare string
			for _, file := range files {
				str := file.Name()
				if unicode.IsDigit(rune(str[0])) {
					for i, char := range str {
						if char == '_' {
							if str[0:i] < compare || len(compare) == 0 {
								compare = str[0:i]
								firstFile = str
								break
							}
							break
						}
					}
				}
			}
			inputFilename = filepath.Join(inputFilename, firstFile)
		}

		fmt.Println(`Loading`, inputFilename)
		usingCapcode, charsetFlag, level, reserve2, _, tokens, scores, err = loadTokensFromFile(inputFilename)
		if err != nil {
			die(err.Error(), false)
		}
		if len(scores) == 0 && resize > 0 {
			die("This tokens file cannot be resized because it's not yet been trained", false)
		}
	}

	// Load vocab
	vocab := new(tokenmonster.Vocab)
	var vocabLoaded bool
	if len(inputVocab) != 0 {
		fmt.Println(`Loading`, inputVocab)
		vocab, err = tokenmonster.Load(inputVocab)
		if err != nil {
			die(err.Error(), false)
		}
		vocabLoaded = true
	}

	// Parse modify file
	if len(modifyFilename) > 0 {
		fmt.Println(`Parsing`, modifyFilename)

		file, err := os.Open(modifyFilename)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Unable to open the file:", modifyFilename)
			os.Exit(1)
		}
		defer file.Close()
		
		// Read file
		data, err := ioutil.ReadAll(file)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error reading", modifyFilename, err)
			os.Exit(1)
		}
		
		// Parse JSON
		type JsonData struct {
			Special []string `json:"special,omitempty"`
			Add []string `json:"add,omitempty"`
			Delete []string `json:"delete,omitempty"`
			Capcode *bool `json:"capcode,omitempty"`
			Charset *string `json:"charset,omitempty"` 
			Mode *int `json:"mode,omitempty"`
			Reserve256Bytes *bool `json:"include-256-bytes,omitempty"`
			ReserveUTF8Bytes *bool `json:"include-utf8-bytes,omitempty"`
			ReserveASCIIBytes *bool `json:"include-ascii-bytes,omitempty"`
			Reserve128Bytes *bool `json:"include-128-bytes,omitempty"`
			ExcludeOtherBytes *bool `json:"exclude-other-bytes,omitempty"`
		}
		var jd JsonData
		err = json.Unmarshal(data, &jd)
		if err != nil {
			fmt.Fprintln(os.Stderr, "There is an error in the JSON formatting of the 'special' JSON file:", err)
			fmt.Fprintln(os.Stderr, "Example of correct formatting: { \"add\": [ \"abc\", \"def\" ], \"special\": [ \"</eos>\", \"</eof>\" ], \"delete\": [ \"cheese\" ] }")
			os.Exit(1)
		}
		if jd.Capcode != nil || jd.Mode != nil || jd.Charset != nil {
			if len(inputFilename) > 0 || len(inputVocab) > 0 {
				fmt.Fprintln(os.Stderr, `Error: JSON fields "capcode", "charset" & "mode" cannot be used with an existing vocabulary, they are only used when generating a full vocabulary from JSON`)
				os.Exit(1)
			}
		}
		if jd.Capcode != nil {
			usingCapcode = *jd.Capcode
		}
		if jd.Reserve256Bytes != nil {
			if *jd.Reserve256Bytes {
				reserve |= 1 << 0
			}
		}
		if jd.Reserve128Bytes != nil {
			if *jd.Reserve128Bytes {
				reserve |= 1 << 1
			}
		}
		if jd.ReserveUTF8Bytes != nil {
			if *jd.ReserveUTF8Bytes {
				reserve |= 1 << 2
			}
		}
		if jd.ReserveASCIIBytes != nil {
			if *jd.ReserveASCIIBytes {
				reserve |= 1 << 3
			}
		}
		if jd.ExcludeOtherBytes != nil {
			if *jd.ExcludeOtherBytes {
				reserve |= 1 << 4
			}
		}
		if jd.Mode != nil {
			level = uint8(*jd.Mode)
		}
		if jd.Charset != nil {
			switch strings.ToLower(*jd.Charset) {
				case `utf-8`:
					fallthrough
				case `utf8`:
					charsetFlag = 1
				case `utf-16`:
					fallthrough
				case `utf16`:
					charsetFlag = 2
			}
		}
		for _, s := range jd.Special {
			if len(s) <= 1 {
				fmt.Fprintln(os.Stderr, "Error: A special token must be at least 2 characters long")
				os.Exit(1)
			}
			specialTokens = append(specialTokens, []byte(s))
		}
		addTokens = make([][]byte, len(jd.Add))
		for i, s := range jd.Add {
			if err != nil {
				fmt.Fprintln(os.Stderr, "Error parsing tokens in modify file. Please check the encoding is correct")
				os.Exit(1)
			}
			addTokens[i] = []byte(s)
		}
		deleteTokens = make([][]byte, len(jd.Delete))
		for i, s := range jd.Add {
			if err != nil {
				fmt.Fprintln(os.Stderr, "Error parsing tokens in modify file. Please check the encoding is correct")
				os.Exit(1)
			}
			deleteTokens[i] = []byte(s)
		}
	}

	if vocabLoaded && (len(addTokens) > 0 || len(deleteTokens) > 0 || len(specialTokens) > 0 || len(addSingleBytes) > 0 || resize > 0 || len(tokens) > 0 || reserve != 0) {
		charsetFlag, usingCapcode, level = vocab.Charset(), vocab.Capcode(), vocab.Mode()
		vocabLoaded = false
	}
	if len(setUnk) != 0 {
		if setUnk == "t" || setUnk == "y" {
			vocab.EnableUnkToken()
		} else if setUnk == "f" || setUnk == "n" {
			vocab.DisableUnkToken()
		} else {
			fmt.Fprintln(os.Stderr, "The -unk flag accepts only 'true' or 'false'")
			os.Exit(1)
		}
	}
	if !vocabLoaded {
		vocab = vocab.PrivateGenerateVocab(tokens, scores, addTokens, deleteTokens, specialTokens, charsetFlag, usingCapcode, level, reserve|reserve2, resize)
	}
	usingCapcode = vocab.Capcode()
	charsetFlag = vocab.Charset()
	level = vocab.Mode()
	specialTokensList := vocab.SpecialTokens()
	numSpecialTokens := len(specialTokensList)
	reserved := vocab.NumReservedTokens()
	total := vocab.Len()
	num := (total - numSpecialTokens) - reserved
				 
	if usingCapcode {
		fmt.Println(`Capcode:                 Enabled`)
	} else {
		fmt.Println(`Capcode:                 Disabled`)
	}
	switch charsetFlag {
		case 0:
			fmt.Println(`Charset:                 None`)
		case 1:
			fmt.Println(`Charset:                 UTF-8`)
		case 2:
			fmt.Println(`Charset:                 UTF-16`)
		default:
			die(`Unrecognized encoding type`, false)
	}
	switch level {
		case 0:
			fmt.Println(`Optimization mode:       0 (unfiltered)`)
		case 1:
			fmt.Println(`Optimization mode:       1 (clean)`)
		case 2:
			fmt.Println(`Optimization mode:       2 (balanced)`)
		case 3:
			fmt.Println(`Optimization mode:       3 (consistent)`)
		case 4:
			fmt.Println(`Optimization mode:       4 (strict)`)
		default:
			fmt.Println(`This vocabulary was not trained with TokenMonster`)
	}	
	fmt.Println(`Maximum token length:   `, vocab.MaxTokenLength())
	fmt.Println(`Regular tokens:         `, num)
	fmt.Println(`Single character tokens:`, reserved)
	fmt.Println(`Special tokens:         `, numSpecialTokens)
	if numSpecialTokens > 0 {
		for _, v := range specialTokensList {
			fmt.Println(`                         [ID ` + conv.String(int(v.ID)) + `]`, string(v.TokenDecoded))
		}
	}
	if vocab.HasUnk() {
		fmt.Println(`UNK token:               Yes [ID ` + conv.String(int(vocab.Unk())) + `]`)
		total++
	} else {
		if (reserved < 256 && !usingCapcode) || reserved < 233 {
			fmt.Println(`UNK token:               No (can be added)`)
		} else {
			fmt.Println(`UNK token:               No (all bytes have tokens)`)
		}
	}
	fmt.Println(`Deleted tokens:         `, vocab.DeletedTokens())
	fmt.Println(`Total tokens:           `, total)
	fmt.Println()

	// Create the vocabulary file
	if len(outputFilename) > 0 {
		if !strings.HasSuffix(outputFilename, `.vocab`) {
			outputFilename += `.vocab`
		}
		if err = vocab.Save(outputFilename); err != nil {
			die(err.Error(), false)
		}
		fmt.Println(`Exported:`, outputFilename)
	}

	// Save the tokens as a text file for viewing
	if len(textFilename) > 0 {
		if orderByScore {
			infos := vocab.TokensDetailed()
			list := make([]sortUint32Float32.KeyVal, len(infos))
			for i, v := range infos {
				list[i] = sortUint32Float32.KeyVal{uint32(i), v.Score}
			}
			sortUint32Float32.Desc(list)
			tokens = make([][]byte, len(list))
			for i, v := range list {
				tokens[i] = infos[v.K].Token
			}
			err = saveAsTxt(textFilename, tokens, delimiter)
		} else {
			err = saveAsTxt(textFilename, vocab.Tokens(), delimiter)
		}
		if err != nil {
			die(err.Error(), false)
		}
		fmt.Println(`Exported:`, textFilename)
	}

	if len(tokensFilename) > 0 {
		infos := vocab.TokensDetailed()
		scores = make([]float32, len(infos))
		tokens = make([][]byte, len(infos))
		var hasScore bool
		for i, v := range infos {
			scores[i] = v.Score
			tokens[i] = v.Token
			if v.Score > 0 {
				hasScore = true
			}
		}
		if !hasScore {
			scores = nil
		}
		nReserve := vocab.NumReservedTokens()
		if (nReserve == 233 && vocab.Capcode()) || nReserve == 256 {
			reserve = 1
		} else if (nReserve == 220 && vocab.Capcode()) || nReserve == 243 {
			reserve = 2
		}
		err = saveTokensToFile(tokensFilename, tokens, scores, vocab.Capcode(), vocab.Charset(), vocab.Mode(), reserve, vocab.HasCustomIDs())
		if err != nil {
			die(err.Error(), false)
		}
		fmt.Println(`Exported:`, tokensFilename)
	}

	if len(exists) > 0 {
		fmt.Println(`Looking for token: '` + exists + `'`)
		tok := []byte(exists)
		tok2, _ := vocab.Normalize(tok)
		id, found := vocab.ID(tok)
		if found {
			fmt.Println("\tID:", id)
			fmt.Println("\t\tEncoded: '" + string(tok) + `'`)
			fmt.Println("\t\tDecoded: '" + string(vocab.Denormalize(tok)) + `'`)
		}
		id2, found2 := vocab.ID(tok2)
		if found2 {
			fmt.Println("\tID:", id2)
			fmt.Println("\t\tEncoded: '" + string(tok2) + `'`)
			fmt.Println("\t\tDecoded: '" + string(vocab.Denormalize(tok2)) + `'`)
		}
		if !found && !found2 {
			fmt.Println("\tNo tokens found")
		}
		fmt.Println()
	}
}
