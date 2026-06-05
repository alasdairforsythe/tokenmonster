# tokenmonster-cpp

C++20 runtime loader, tokenizer, serializer, deserializer, and detokenizer for existing TokenMonster `.vocab` files.

This package ports the Go runtime path for `github.com/alasdairforsythe/tokenmonster`: loading existing vocabularies, normalization, tokenization, serialized tokenization, deserialization, decoding, and streaming decoding. It intentionally does not port trainer code, YAML import/export, vocabulary mutation, Python bindings, JavaScript bindings, or generated sort packages.

The runtime backend includes the C++ translation of `github.com/alasdairforsythe/pansearch` used by TokenMonster, including the packed length buckets, bloom filters, direct maps, and binary-search fallback. TokenMonster normalization is included internally and uses ICU for the Unicode operations. Capcode is consumed as the separate `capcode-cpp` library.

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

## Benchmarks

These results compare the C++ runtime with the Go runtime on the same 1 MiB corpus and the same vocabularies. The run used Go 1.24.5, GCC 13.3.0, Release C++ builds, CPU 0 pinning, and roughly 2 seconds per benchmark operation. Throughput is MB/s; lower ns/op is faster.

`encode_tokenize` is the full public encode/tokenize path, including normalization and Capcode. `tokenize_normalized` measures tokenization after normalization/Capcode has already been applied.

| Vocab | Impl | Normalize | Tokenize Normalized | Encode + Tokenize | Decode Tokens |
| --- | --- | ---: | ---: | ---: | ---: |
| 1024 | C++ | 182.5 MB/s, 5,746,463.8 ns/op | 38.0 MB/s, 35,247,809.0 ns/op | 26.0 MB/s, 40,332,668.8 ns/op | 144.7 MB/s, 7,248,951.5 ns/op |
| 1024 | Go | 136.6 MB/s, 7,674,030.8 ns/op | 39.9 MB/s, 33,538,184.1 ns/op | 26.2 MB/s, 40,062,848.9 ns/op | 128.6 MB/s, 8,156,132.3 ns/op |
| 32000 | C++ | 183.7 MB/s, 5,708,398.9 ns/op | 50.3 MB/s, 26,596,849.2 ns/op | 33.1 MB/s, 31,694,785.3 ns/op | 186.1 MB/s, 5,634,112.7 ns/op |
| 32000 | Go | 134.5 MB/s, 7,796,254.9 ns/op | 46.6 MB/s, 28,726,305.1 ns/op | 29.7 MB/s, 35,350,296.8 ns/op | 163.0 MB/s, 6,433,362.3 ns/op |

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
- `vocab.tokenize(data)` mirrors Go `Tokenize`.
- `vocab.count(data)` mirrors Go `Count`.
- `vocab.tokenize_serialized(data, encoding_length)` mirrors Go serialized tokenization for 2, 3, or 4 byte token IDs.
- `vocab.deserialize(data, encoding_length)` mirrors Go deserialization.
- `vocab.decode(tokens)` mirrors Go `Decode`.
- `vocab.decode_serialized(data, encoding_length)` mirrors Go serialized decode.
- `vocab.new_decoder()` creates a streaming decoder for chunked token or serialized-token input.
- `vocab.tokens_detailed()`, `vocab.special_tokens()`, `vocab.tokens()`, `vocab.id_to_token(id)`, `vocab.token_to_id(token)`, and `vocab.denormalize(token)` expose loaded vocabulary metadata.

## Scope

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
- Python, JavaScript, or Go bindings.
- Generated sort packages; C++ standard sorting is used where needed.

## Smoke Test

```sh
./build/tokenmonster_smoke /path/to/file.vocab "This is a test."
```

## License

MIT, matching the upstream project.
