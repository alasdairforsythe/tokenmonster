package main

import (
	"os"
	"fmt"
	"flag"
	"bufio"
	"errors"
	"strings"
	"unicode"
	"io/ioutil"
	"path/filepath"
	_ "gopkg.in/yaml.v3"
	"github.com/AlasdairF/Conv"
	"github.com/AlasdairF/Custom"
	"github.com/alasdairforsythe/tokenmonster/go"
	"github.com/alasdairforsythe/norm"
)

func loadTokensFromFile(filename string) (uint8, uint8, uint8, uint8, uint8, [][]byte, []float32, [][]byte, error) {
	fi, err := os.Open(filename)
	if err != nil {
		return 0, 0, 0, 0, 0, nil, nil, nil, err
	}
	defer fi.Close()
	r := custom.NewZlibReader(fi)
	_usingCapcode := r.ReadByte()
	_charsetFlag := r.ReadByte()
	_normalize := r.ReadByte()
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
	var scores []float32
	var specialTokens [][]byte
	if r.EOF() != nil {
		scores = make([]float32, l)
		for i:=0; i<l; i++ {
			scores[i] = r.ReadFloat32()
		}
		if r.EOF() != nil {
			l = int(r.ReadUint32())
			specialTokens = make([][]byte, l)
			for i:=0; i<l; i++ {
				specialTokens[i] = r.ReadBytes8()
			}
			if r.EOF() != nil {
				return 0, 0, 0, 0, 0, nil, nil, nil, errors.New(filename + ` not valid`)
			}
		}
	}
	return _usingCapcode, _charsetFlag, _normalize, _level, _reserve, data, scores, specialTokens, nil
}

func saveTokensToFile(filename string, data [][]byte, scores []float32, usingCapcode uint8, charsetFlag uint8, normalize uint8, level uint8, reserve uint8, specialTokens [][]byte) error {
	fi, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer fi.Close()
	w := custom.NewZlibWriter(fi)
	w.WriteByte(usingCapcode)
	w.WriteByte(charsetFlag)
	w.WriteByte(normalize)
	w.WriteByte(level)
	w.WriteByte(reserve)
	w.WriteByte(0) //reserved
	w.WriteByte(0) //reserved
	w.WriteByte(0) //reserved
	defer w.Close()
	w.WriteUint64(uint64(len(data)))
	for _, b := range data {
		w.WriteBytes8(b)
	}
	if len(scores) == len(data) {
		for _, v := range scores {
			w.WriteFloat32(v)
		}
		if len(specialTokens) > 0 {
			w.WriteUint32(uint32(len(specialTokens)))
			for _, b := range specialTokens {
				w.WriteBytes8(b)
			}
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
	var inputFilename, outputFilename, inputYaml, outputYaml, inputVocab, addSingleBytes, tokensFilename, addSpecialToken, setUnk, exists string
	var excludeOtherBytes, orderByScore, resetTokenIds bool
	var charsetFlag, level, reserve, reserve2, usingCapcode, normalizeCode uint8
	var tokens, specialTokens, encodedSpecialTokens [][]byte
	var yaml []byte
	var scores []float32
	var err error

	flag.StringVar(&inputVocab, "input-vocab", inputVocab, "an existing TokenMonster vocabulary file (optional)")
	flag.StringVar(&inputFilename, "input", inputFilename, "tokens file or directory from trainvocab, if directory it will load the best performing tokens file in the directory (optional)")
	flag.StringVar(&outputFilename, "output", outputFilename, "filename of the vocabulary to output (optional)")
	flag.StringVar(&tokensFilename, "output-tokens", tokensFilename, "converts a vocabulary back to a tokens file that can be used with trainvocab (optional)")
	flag.StringVar(&inputYaml, "input-yaml", inputYaml, "filename of a YAML file containing modifications or a new vocabulary (optional)")
	flag.StringVar(&outputYaml, "output-yaml", inputYaml, "filename to export the vocabulary in YAML format (optional)")
	flag.StringVar(&addSingleBytes, "add-single-bytes", addSingleBytes, "enter \"256\", \"128\", \"ascii\", \"extended\" or \"utf8\" to add tokens for those individual bytes (optional)")
	flag.BoolVar(&excludeOtherBytes, "delete-single-bytes", excludeOtherBytes, "deletes all the single byte tokens except those specified from add-single-bytes (optional)")
	flag.IntVar(&resize, "resize", resize, "resizes the vocabulary to this many tokens by deleting the worst scoring tokens (optional)")
	flag.BoolVar(&orderByScore, "order-by-score", orderByScore, "orders output-txt by token score (descending) instead of alphabetically (optional) (default false)")
	flag.BoolVar(&resetTokenIds, "reset-token-ids", resetTokenIds, "resets the IDs of the tokens to be sequential from zero (optional) (default false)")
	flag.StringVar(&addSpecialToken, "add-special-token", addSpecialToken, "a single special token to add to the vocabulary (optional)")
	flag.StringVar(&exists, "exists", exists, "check if a token exists in the vocabulary (optional)")
	flag.StringVar(&setUnk, "unk", setUnk, "set to true or false to enable or disable the UNK token (optional)")
	flag.Parse()
	if len(inputFilename) == 0 && len(inputYaml) == 0 && len(inputVocab) == 0 {
		flag.Usage()
		os.Exit(0)
	}
	if len(inputFilename) > 0 && len(inputVocab) > 0 {
		die("You cannot input both a vocabulary and a tokens file at the same time.", true)
	}

	if len(inputYaml) > 0 {
		yaml, err = ioutil.ReadFile(inputYaml)
		if err != nil {
			die("Error reading input-yaml file: " + err.Error(), false)
		}
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
			case `extended`:
				reserve |= 1 << 4
			default:
				die("Error: add-single bytes must be one of \"256\", \"128\", \"ascii\", \"extended\" or \"utf8\"", true)
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
		reserve |= 1 << 5
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
		usingCapcode, charsetFlag, normalizeCode, level, reserve2, tokens, scores, encodedSpecialTokens, err = loadTokensFromFile(inputFilename)
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

	if vocabLoaded && (len(specialTokens) > 0 || len(addSingleBytes) > 0 || resize > 0 || len(tokens) > 0 || reserve != 0 || resetTokenIds) {
		charsetFlag, usingCapcode, level, normalizeCode = vocab.Charset(), vocab.Capcode(), vocab.Mode(), vocab.NormalizationCode()
		vocabLoaded = false
	}
	if setUnk == "t" || setUnk == "y" {
		vocab.EnableUnkToken()
	}
	if !vocabLoaded {
		var n norm.Normalizer
		n.Flag = normalizeCode
		err = vocab.PrivateGenerateVocab(yaml, tokens, scores, nil, nil, specialTokens, encodedSpecialTokens, charsetFlag, n.String(), usingCapcode, level, reserve|reserve2, resize, resetTokenIds)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error: " + err.Error())
			os.Exit(1)
		}
	}
	if setUnk == "f" || setUnk == "n" {
		vocab.DisableUnkToken()
	}
	usingCapcode = vocab.Capcode()
	charsetFlag = vocab.Charset()
	level = vocab.Mode()
	specialTokensList := vocab.SpecialTokens()
	numSpecialTokens := len(specialTokensList)
	numSingleBytes := vocab.NumSingleByteTokens()
	total := vocab.Len()
	numRegular := (total - numSpecialTokens) - numSingleBytes
	if vocab.HasUnk() {
		numRegular--
	}
	
	switch usingCapcode {
		case 0:
			fmt.Println(`Capcode:               0 (disabled)`)
		case 1:
			fmt.Println(`Capcode:               1 (deleteToken)`)
		case 2:
			fmt.Println(`Capcode:               2 (enabled)`)
		default:
			die(`capcode value is invalid`, false)
	}
	switch charsetFlag {
		case 0:
			fmt.Println(`Charset:               None`)
		case 1:
			fmt.Println(`Charset:               UTF-8`)
		case 2:
			fmt.Println(`Charset:               UTF-16`)
		default:
			die(`charset value is invalid`, false)
	}
	fmt.Println(`Normalization:         ` + vocab.Normalization())
	switch level {
		case 0:
			fmt.Println(`Optimization mode:     0 (unfiltered)`)
		case 1:
			fmt.Println(`Optimization mode:     1 (clean)`)
		case 2:
			fmt.Println(`Optimization mode:     2 (balanced)`)
		case 3:
			fmt.Println(`Optimization mode:     3 (consistent)`)
		case 4:
			fmt.Println(`Optimization mode:     4 (strict)`)
		default:
			fmt.Println(`Optimization mode:     N/A`)
	}	
	fmt.Println(`Maximum token length: `, vocab.MaxTokenLength())
	fmt.Println(`Regular tokens:       `, numRegular)
	fmt.Println(`Single byte tokens:   `, numSingleBytes)
	fmt.Println(`Special tokens:       `, numSpecialTokens)
	if numSpecialTokens > 0 {
		for _, v := range specialTokensList {
			fmt.Println(`                       [ID ` + conv.String(int(v.Id)) + `]`, string(v.TokenDecoded))
		}
	}
	if vocab.HasUnk() {
		fmt.Println(`UNK token:             Yes [ID ` + conv.String(int(vocab.Unk())) + `]`)
		total++
	} else {
		if (numSingleBytes < 256 && usingCapcode!=2) || numSingleBytes < 233 {
			fmt.Println(`UNK token:             No (can be added)`)
		} else {
			fmt.Println(`UNK token:             No (all bytes have tokens)`)
		}
	}
	fmt.Println(`Deleted tokens:       `, vocab.NumDeletedTokens())
	fmt.Println(`Total tokens:         `, vocab.Len())
	fmt.Println()

	// Create the vocabulary file
	if len(outputFilename) > 0 {
		if err = vocab.Save(outputFilename); err != nil {
			die(err.Error(), false)
		}
		fmt.Println(`Exported:`, outputFilename)
	}

	if len(tokensFilename) > 0 {
		infos := vocab.TokensDetailed()
		scores = make([]float32, len(infos))
		tokens = make([][]byte, len(infos))
		var specialTokens [][]byte
		for i, v := range infos {
			scores[i] = v.Score
			tokens[i] = v.Token
			if v.Type == 2 {
				specialTokens = append(specialTokens, v.Token)
			}
		}
		if len(scores) == 0 && len(specialTokens) == 0 {
			scores = nil
			specialTokens = nil
		}
		err = saveTokensToFile(tokensFilename, tokens, scores, vocab.Capcode(), vocab.Charset(), vocab.NormalizationCode(), vocab.Mode(), vocab.SingleBytesTrainingCode(), specialTokens)
		if err != nil {
			die(err.Error(), false)
		}
		fmt.Println(`Exported:`, tokensFilename)
	}

	if len(outputYaml) > 0 {
		fi, err := os.Create(outputYaml)
		if err != nil {
			die(`Unable to create file: ` + outputYaml, false)
		}
		defer fi.Close()
		vocab.ExportYAML(fi, orderByScore)
		fmt.Println(`Exported:`, outputYaml)
	}

	if len(exists) > 0 {
		fmt.Println(`Looking for token: '` + exists + `'`)
		tok := []byte(exists)
		tok2, _ := vocab.Normalize(tok)
		id, found := vocab.TokenToId(tok)
		if found {
			fmt.Println("\tID:", id)
			fmt.Println("\t\tEncoded: '" + string(tok) + `'`)
			fmt.Println("\t\tDecoded: '" + string(vocab.Denormalize(tok)) + `'`)
		}
		id2, found2 := vocab.TokenToId(tok2)
		if found2 && id2 != id {
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
