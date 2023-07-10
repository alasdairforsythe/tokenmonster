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
special_yaml = ""
nSpecial = 0

# Write a YAML vocabulary header
yaml = "charset: \"utf-8\"\ncapcode: 0\nnormalization: \"trim collapse\"\nregular:\n"

################################################################################
# 'trim' and 'collapse' normalizations are specific to the GPT2Tokenizer
#  The normalization parameters accepted by TokenMonster are as follows:
#  NFD, Lowercase, Accents, Quotemarks, Collapse, Trim, LeadingSpace, UnixLines
################################################################################

# Write the tokens into the YAML vocabulary (hex encoded just to be safe)
for _, id in regular_tokens.items():
    key = gpt2tokenizer.decode([id]) # get the decoded form of the token
    enc = key.encode() # convert it to bytes string
    enc = enc.hex() # convert it to hex
    if key in special_tokens: # Is it a special token?
        special_yaml += "  - id: " + str(id) + "\n    token: \"TokenMonsterHexEncode{" + enc + "}\"\n    encoded: true\n"
        nSpecial += 1
    else: # It's a regular token
        yaml += "  - id: " + str(id) + "\n    token: \"TokenMonsterHexEncode{" + enc + "}\"\n    encoded: true\n"

# Write the special tokens after the regular tokens
if nSpecial > 0:
    yaml += "special:\n" + special_yaml

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
