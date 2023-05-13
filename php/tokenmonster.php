<?php

class TokenMonster {
    private $word2id;
    private $id2word;
    private $max_token_len;

    public function load($filename) {
        // Open the file
        $file = fopen($filename, 'r');

        // Read the first 8 bytes as an encoded integer
        $encodedInteger = fread($file, 8);
        $bytes = unpack('C*', $encodedInteger);
        $n = ($bytes[1] << 56) | ($bytes[2] << 48) | ($bytes[3] << 40) | ($bytes[4] << 32) | ($bytes[5] << 24) | ($bytes[6] << 16) | ($bytes[7] << 8) | $bytes[8];

        // Initialize an empty associated array
        $this->word2id = [];

        // Iterate $n times
        $max_token_len = 0;
        for ($i = 0; $i < $n; $i++) {
            // Read 1 byte and convert it to an integer
            $byte = fread($file, 1);
            $len = unpack('C', $byte)[1];

            // Read $len bytes as a string
            $str = fread($file, $len);
            $max_token_len = max($max_token_len, $len);

            // Set the key in the associated array to true
            $this->word2id[$str] = $i;
        }
        $this->id2word = array_keys($this->word2id);

        // Check if it's the end of the file
        if (!feof($file)) {
            // Close the file
            fclose($file);

            // Throw an exception
            throw new Exception("Invalid file.");
        }

        // Close the file
        fclose($file);
    }

    public function tokenize($text) {
        $tokens = [];
        $textLen = strlen($text);
        $i = 0;
        
        while ($i < $textLen) {
            $matchedToken = false;
            
            // Check for tokens starting from the maximum token length
            for ($len = $this->max_token_len; $len > 0; $len--) {
                if (($i + $len) <= $textLen) {
                    $substr = substr($text, $i, $len);
                    if (isset($this->array[$substr])) {
                        $tokens[] = $this->array[$substr];
                        $i += $len;
                        $matchedToken = true;
                        break;
                    }
                }
            }
            if (!$matchedToken) {
                $i++;
            }
        }
        
        return $tokens;
    }

    public function detokenize(array $tokens) {
        $text = '';
        foreach ($tokens as $id) {
            $text .= $this->id2word[$id];
        }
        return $text;
    }
}

/*
// Create an instance of the TokenMonster class
$tokenMonster = new TokenMonster();

// Load the token data from a file
$tokenMonster->load('file');

// Define a sample text to tokenize
$text = "Hello world! This is a test.";

// Tokenize the text
$tokens = $tokenMonster->tokenize($text);

// Output the tokens
echo "Tokens: " . implode(', ', $tokens) . "\n";

// Detokenize the tokens
$detokenizedText = $tokenMonster->detokenize($tokens);

// Output the detokenized text
echo "Detokenized Text: " . $detokenizedText . "\n";
*/