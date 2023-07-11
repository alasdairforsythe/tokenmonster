#
#   This is an example of importing GPT2Tokenizer into TokenMonster
#   using YAML as an intermediary vocabulary.
#

import tokenmonster
from transformers import GPT2Tokenizer

# Initialize the GPT-2 tokenizer from Hugging Face
gpt2tokenizer = GPT2Tokenizer.from_pretrained("gpt2")

# Get the dictionary
regular_tokens = gpt2tokenizer.get_vocab()

# Get map of special tokens
special_tokens = {value: True for value in list(gpt2tokenizer.special_tokens_map.values())}

# Write a YAML vocabulary header for GPT2 Tokenizer
yaml = (
    "charset: \"utf-8\"\n"
    "capcode: 0\n"
    "normalization: \"none\"\n"
    "tokens:\n"
)

# Write the tokens into the YAML vocabulary (hex encoded to avoid handling escape sequences)
special_tokens = []
for _, id in regular_tokens.items():
    token = gpt2tokenizer.decode([id]) # get the decoded form of the token
    token_bytes = token.encode() # convert to bytes string
    yaml_line = (
        "  - id: " + str(id) + "\n" +
        '    token: "TokenMonsterHexEncode{' + token_bytes.hex() + '}"\n' +
        "    encoded: true\n"
    )
    if token in special_tokens: # Is it a special token?
        special_tokens.append(yaml_line)
    else: # It's a regular token
        yaml += yaml_line

# Write the special tokens after the regular tokens
if len(special_tokens) > 0:
    yaml += "special:\n" + ''.join(special_tokens)

# Import the YAML vocabulary into TokenMonster
vocab = tokenmonster.new(yaml)

# Test it
tokens = vocab.tokenize("If this prints then it was successfully tokenized and decoded again with the TokenMonster vocabulary.")
decoded = vocab.decode(tokens)
print(decoded)
print(tokens)

# Uncomment this to save it as a TokenMonster vocabulary:
# vocab.save("gpt2.vocab")

# You can then in future load it from file with:
# vocab.load("gpt2.vocab")
