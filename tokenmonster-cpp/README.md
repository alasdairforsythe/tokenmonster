# tokenmonster-cpp

Included:

- TokenMonster runtime vocabulary loading.
- Tokenization and counting for loaded vocabularies.
- Serialized tokenization, deserialization, and decoding.
- Streaming decode state.
- Internal pansearch runtime backend.
- Internal TokenMonster normalization runtime.
- External `capcode-cpp` dependency for Capcode and NoCapcode.

Not included:

- Vocabulary training.
- YAML vocabulary import/export.
- Vocabulary mutation or resizing.

## Benchmarks

These results compare the C++ runtime with the Go runtime on the same 1 MiB corpus and the same vocabularies. The C++ figures are the median of three CPU-pinned runs (`taskset -c 0`) at 3 s per benchmark operation, built with GCC 13.3.0 and the default `Release` optimization (`-O3`), on an AMD Ryzen 9 7950X3D. The Go figures use Go 1.24.5 and are unchanged. Throughput is MB/s; lower ns/op is faster.

`encode_tokenize` is the full public encode/tokenize path, including normalization and Capcode. `tokenize_normalized` measures tokenization after normalization/Capcode has already been applied. The `1024` vocab is `english-1024-strict-nocapcode-v1` (no Capcode) and the `32000` vocab is `english-32000-consistent-v1` (Capcode); the no-Capcode normalize and decode paths are faster because they skip Capcode encode/decode.

| Vocab | Impl | Normalize | Tokenize Normalized | Encode + Tokenize | Decode Tokens |
| --- | --- | ---: | ---: | ---: | ---: |
| 1024 | C++ | 397.4 MB/s, 2,638,521.0 ns/op | 46.9 MB/s, 25,941,349.2 ns/op | 41.7 MB/s, 25,138,477.5 ns/op | 689.9 MB/s, 1,519,867.2 ns/op |
| 1024 | Go | 136.6 MB/s, 7,674,030.8 ns/op | 39.9 MB/s, 33,538,184.1 ns/op | 26.2 MB/s, 40,062,848.9 ns/op | 128.6 MB/s, 8,156,132.3 ns/op |
| 32000 | C++ | 347.5 MB/s, 3,017,394.1 ns/op | 67.5 MB/s, 19,827,657.1 ns/op | 51.3 MB/s, 20,446,145.6 ns/op | 394.9 MB/s, 2,655,188.0 ns/op |
| 32000 | Go | 134.5 MB/s, 7,796,254.9 ns/op | 46.6 MB/s, 28,726,305.1 ns/op | 29.7 MB/s, 35,350,296.8 ns/op | 163.0 MB/s, 6,433,362.3 ns/op |

### Gemma tokenizer: TokenMonster vs. the reference implementations

`gemma4` is TokenMonster's import of the Gemma tokenizer (262,004 tokens, no-Capcode). It is benchmarked in both TokenMonster modes — greedy (`tokenize_greedy`, longest match at each position) and the default non-greedy (`tokenize`) — against both reference implementations of the same tokenizer: SentencePiece and HuggingFace.

All four run single-threaded on the same 1 MiB corpus (byte-for-byte identical, verified by checksum), pinned to core 0, median of three 3 s runs.

| Tokenizer | Throughput | Tokens (1 MiB) |
| --- | ---: | ---: |
| TokenMonster — greedy (`tokenize_greedy`) | **182.9 MB/s**, 5,733,658.7 ns/op | **349,522** |
| TokenMonster — non-greedy (`tokenize`) | 93.3 MB/s, 11,240,482.8 ns/op | 349,522 |
| SentencePiece (`sentencepiece` 0.2.1) | 3.2 MB/s, 329,501,406.4 ns/op | 351,680 |
| HuggingFace `tokenizers` 0.23.1 (Rust) | 4.1 MB/s, 257,520,023.5 ns/op | 351,680 |

TokenMonster is 45–57× faster than either reference implementation. In this case the token counts are different because BPE follows a fixed merge order, while TokenMonster in greedy mode takes the longest valid match. In this case, it's not a drop-in replacement on a model trained with BPE because the model was trained on the BPE tokens, so better not confuse it with a different segmentation. The comparison is to demonstrate the speed difference. `tokenmonster_greedy` is the correct algorithm for imported vocabularies (same tokens, faster speed), the non-greedy algorithm benefiting TokenMonster-trained vocabularies.

### Compiler options

`CMAKE_BUILD_TYPE=Release` compiles with `-O3 -DNDEBUG`. We swept the optimization flags on the `32000` (Capcode) vocab; each cell is the median of three CPU-pinned runs, MB/s, higher is better. The `1024` (no-Capcode) vocab shows the same ordering.

| Flags (in place of the `Release` default) | Normalize | Tokenize Normalized | Encode + Tokenize | Decode Tokens |
| --- | ---: | ---: | ---: | ---: |
| `-O2` (recommended) | 345.5 | 67.8 | 51.5 | **468.6** |
| `-O3` (`Release` default) | 348.5 | 67.7 | 51.4 | 394.0 |
| `-O3 -march=native` | 303.3 | 66.3 | 49.5 | 445.7 |
| `-O2 -march=native` | 330.8 | 66.8 | 50.5 | 442.9 |
| `-O3 -flto` | 322.4 | 66.7 | 50.1 | 465.0 |

Recommendation for this toolchain:

- **Prefer `-O2`.** It matches `-O3` on tokenization and encoding, stays within ~1% on normalization, and is markedly faster on decode (+19% here for the Capcode vocab, +8% for the no-Capcode vocab), where `-O3` was the slowest of everything tested. Select it with:
  ```sh
  cmake -S . -B build -DCMAKE_BUILD_TYPE=Release \
    -DCMAKE_CXX_FLAGS_RELEASE="-O2 -DNDEBUG"
  ```
- **Avoid `-march=native`.** It regresses normalization (up to −13%) and tokenization/encoding (−2 to −4%); its decode gain over `-O3` is smaller than what plain `-O2` already delivers.
- **Avoid `-flto`.** It regresses normalization and tokenization, slows the build, and its decode gain does not offset the loss.

These effects are specific to GCC 13.3.0 on this CPU — in particular the `-O3` decode regression is compiler/microarchitecture dependent. The default `Release` (`-O3`) build is a safe baseline; measure on your own target before locking in a flag. The headline table above uses that default `-O3` build.

## API

The public API mirrors the copied Go runtime surface while using C++ containers and spans.

```cpp
#include <tokenmonster/tokenmonster.hpp>

#include <cstdint>
#include <iostream>
#include <span>
#include <string>

std::span<const std::uint8_t> bytes(std::string_view s) {
  return {reinterpret_cast<const std::uint8_t*>(s.data()), s.size()};
}

int main() {
  auto vocab = tokenmonster::Vocab::load("english-32000-consistent-v1.vocab");

  auto encoded = vocab.tokenize(bytes("This is a test."));
  auto decoded = vocab.decode(encoded.tokens);

  auto serialized = vocab.tokenize_serialized(bytes("This is a test."));
  auto restored = vocab.decode_serialized(serialized.bytes, serialized.encoding_length);

  auto decoder = vocab.new_decoder();
  auto chunk = decoder.decode(encoded.tokens);

  std::cout << "tokens=" << encoded.tokens.size()
            << " missing=" << encoded.missing << "\n";
}
```

Main types and methods:

- `tokenmonster::Vocab::load(path)` loads an existing `.vocab`.
- `vocab.normalize(data)` runs TokenMonster normalization and Capcode/NoCapcode encoding for that vocab.
- `vocab.tokenize(data)` mirrors Go `Tokenize` (ungreedy: follows the trained alternative subword branches and scores them).
- `vocab.count(data)` mirrors Go `Count`.
- `vocab.tokenize_serialized(data, encoding_length)` mirrors Go serialized tokenization for 2, 3, or 4 byte token IDs.
- `vocab.tokenize_greedy(data)`, `vocab.count_greedy(data)`, and `vocab.tokenize_serialized_greedy(data, encoding_length)` are greedy variants that emit only the longest matching token at each position, ignoring the alternative branches. Not part of the ported Go runtime, so results intentionally differ from `tokenize`/Go.
- `vocab.deserialize(data, encoding_length)` mirrors Go deserialization.
- `vocab.decode(tokens)` mirrors Go `Decode`.
- `vocab.decode_serialized(data, encoding_length)` mirrors Go serialized decode.
- `vocab.new_decoder()` creates a streaming decoder for chunked token or serialized-token input.
- `vocab.tokens_detailed()`, `vocab.special_tokens()`, `vocab.tokens()`, `vocab.id_to_token(id)`, `vocab.token_to_id(token)`, and `vocab.denormalize(token)` expose loaded vocabulary metadata.

## Requirements

- C++20 compiler
- CMake 3.20+
- ICU development libraries discoverable through `pkg-config` (`icu-uc`, `icu-i18n`)
- `capcode-cpp`, either installed or available as a sibling checkout at `../capcode-cpp`

On Debian/Ubuntu:

```sh
sudo apt-get install cmake pkg-config libicu-dev
```

## Build

With this directory next to `capcode-cpp`:

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -j
ctest --test-dir build --output-on-failure
```

`Release` builds with `-O3`. On the toolchain we tested, `-O2` decoded noticeably faster at no cost elsewhere — see [Compiler options](#compiler-options).

With `capcode-cpp` somewhere else:

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release \
  -DTOKENMONSTER_CPP_CAPCODE_SOURCE_DIR=/path/to/capcode-cpp
cmake --build build -j
```

With an installed `capcode_cpp` package:

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -j
```

## Install

```sh
cmake --install build --prefix /usr/local
```

Then consume it from CMake:

```cmake
find_package(tokenmonster_cpp CONFIG REQUIRED)
target_link_libraries(your_target PRIVATE tokenmonster::tokenmonster)
```

## Smoke Test

```sh
./build/tokenmonster_smoke /path/to/file.vocab "This is a test."
```

## License

MIT, matching the upstream project.
