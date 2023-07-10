## YAML Guide

TokenMonster supports YAML vocabularies for both creating custom vocabularies (vocabularies not trained by TokenMonster), and for editing existing TokenMonster vocabularies.
You can import and export any TokenMonster vocabulary to and from YAML format with `exportvocab` from the [training](./training/) directory, or with the [Python](./python/) and [Go](./go/) libraries.

See `example.yaml` for a sample of the TokenMonster YAML vocabulary format.

`convert_gpt2tokenizer.py` is a GPT2 Tokenizer from Hugging Face converted into a TokenMonster vocabulary. It runs faster, tokenizes better, and is a good example
of how to import a vocabulary into TokenMonster format using YAML as an intermediary.
