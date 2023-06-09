#
#   This is an example of importing LLaMaTokenizer into TokenMonster
#   using YAML as an intermediary vocabulary.
#

import tokenmonster
import re
from transformers import LlamaTokenizer

# Convert LLaMa hex notation and strings to TokenMonster hex notation
pattern = r'<0x([0-9A-Fa-f]+)>'
def encode_llama_token(token, space_char):
    match = re.match(pattern, token)
    if match:
        hex_part = match.group(1)
        return "TokenMonsterHexEncode{" + hex_part + "}"
    token = token.replace(space_char, " ")
    token_bytes = token.encode()
    return "TokenMonsterHexEncode{" + token_bytes.hex() + "}"

# Initialize the LLaMa tokenizer from Hugging Face
llamatokenizer = LlamaTokenizer.from_pretrained("decapoda-research/llama-7b-hf")

# Tokenize a test string with the original LLaMa tokenizer
test_string = "If this prints then it was successfully tokenized and decoded again with the TokenMonster vocabulary."
tokens = llamatokenizer.tokenize(test_string)
token_ids = llamatokenizer.convert_tokens_to_ids(tokens)
print("Original token IDs:")
print(token_ids)
print(llamatokenizer.convert_ids_to_tokens(token_ids))

# Get the character that is used for a space in the LLaMa tokenizer
space_char = tokens[0][0] # Space is prefixed

# Get the dictionary
regular_tokens = llamatokenizer.get_vocab()

# Write a YAML vocabulary header for LLaMa Tokenizer
# LLaMa tokenizer expects a leading space
yaml = (
    "charset: \"utf-8\"\n"
    "capcode: 0\n"
    "normalization: \"LeadingSpace\"\n"
    "unk: true\n"
    "unk-id: 0\n"
    "tokens:\n"
)

# Write the tokens into the YAML vocabulary (hex encoded to avoid handling escape sequences)
special_tokens = []
for _, id in regular_tokens.items():
    # Skip the UNK token as that's defined in the header
    if id == 0:
        continue
    token = llamatokenizer.convert_ids_to_tokens([id])[0] # get the decoded form of the token
    token = encode_llama_token(token, space_char) 
    yaml_line = (
        "  - id: " + str(id) + "\n"
        '    token: "' + token + '"\n'
        "    encoded: true\n"
    )
    if id == 1 or id == 2: # Is it a special token? For LLaMa this is id 1 & 2
        special_tokens.append(yaml_line)
    else: # It's a regular token
        yaml += yaml_line

# Write the special tokens after the regular tokens
if len(special_tokens) > 0:
    yaml += "special:\n" + ''.join(special_tokens)

# Import the YAML vocabulary into TokenMonster
vocab = tokenmonster.new(yaml)

# Test it
token_ids = vocab.tokenize(test_string)
decoded = vocab.decode(token_ids)
print(decoded)
print("TokenMonster token IDs:")
print(token_ids)
print(vocab.convert_ids_to_tokens(token_ids))

# Uncomment this to save it as a TokenMonster vocabulary:
# vocab.save("llama.vocab")

# You can then in future load it from file with:
# vocab.load("llama.vocab")
