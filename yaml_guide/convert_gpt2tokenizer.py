#
#   This is an example of importing GPT2Tokenizer into TokenMonster
#   using YAML as an intermediary vocabulary.
#

import json
import tokenmonster
from transformers import GPT2Tokenizer

# Initialize the GPT-2 tokenizer from Hugging Face
gpt2tokenizer = GPT2Tokenizer.from_pretrained("gpt2")

# Get the dictionary
# There is a character encoding issue so I'm going to load it from the JSON vocab
# regular_tokens = gpt2tokenizer.get_vocab()
with open('gpt2.json', 'r') as file:
    regular_tokens = json.load(file)

# Get map of special tokens
special_tokens = {value: True for value in list(gpt2tokenizer.special_tokens_map.values())}

# Determine GPT2 special characters
tokens = gpt2tokenizer.tokenize(' test')
space_char = tokens[0][0] # Space is prefixed
print("Space Character:", space_char)
tokens = gpt2tokenizer.tokenize('\n')
newline_char = tokens[0][0] # Space is prefixed
print("Newline Character:", newline_char)
tokens = gpt2tokenizer.tokenize('\r')
carriage_char = tokens[0][0] # Space is prefixed
print("Carriage return:", carriage_char)
tokens = gpt2tokenizer.tokenize('\t')
tab_char = tokens[0][0] # Space is prefixed
print("Tab Character:", tab_char)

# Write a YAML vocabulary header for GPT2 Tokenizer
yaml = (
    "charset: \"utf-8\"\n"
    "capcode: 0\n"
    "normalization: \"none\"\n"
    "tokens:\n"
)

# Write the tokens into the YAML vocabulary (hex encoded to avoid handling escape sequences)
special_tokens = []
n_tokens = 0
for original_token, id in regular_tokens.items():
    token = original_token.replace(space_char, ' ').replace(newline_char, '\n').replace(carriage_char, '\r').replace(tab_char, '\t')
    token_bytes = token.encode() # convert to bytes string
    token_hex = token_bytes.hex()

    yaml_line = (
        "  - id: " + str(id) + "\n"
        '    token: "TokenMonsterHexEncode{' + token_hex + '}"\n'
        "    encoded: true\n"
    )
    if token in special_tokens: # Is it a special token?
        special_tokens.append(yaml_line)
    else: # It's a regular token
        yaml += yaml_line
    
    n_tokens += 1

print("Number of tokens:", n_tokens)

# Write the special tokens after the regular tokens
if len(special_tokens) > 0:
    yaml += "special:\n" + ''.join(special_tokens)

# Import the YAML vocabulary into TokenMonster
vocab = tokenmonster.new(yaml)

# Test it
tokens = vocab.tokenize("If this prints then it was successfully tokenized and decoded again with the TokenMonster vocabulary.")
decoded = vocab.decode(tokens)
print(decoded)
print("Number of token:", len(vocab))

# Uncomment this to save it as a TokenMonster vocabulary:
vocab.save("gpt2.vocab")

# You can then in future load it from file with:
# vocab.load("gpt2.vocab")
