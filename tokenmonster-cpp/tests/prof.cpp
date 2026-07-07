// Single-mode profiling driver: isolates one hot path in a tight loop so that
// perf / callgrind attribute samples to just that path.
//   usage: tokenmonster_prof VOCAB MODE ITERS [corpus_bytes]
//   MODE = normalize | tokenize | tokenize_norm | decode | decode_raw
#include <cstdint>
#include <cstdlib>
#include <filesystem>
#include <iomanip>
#include <iostream>
#include <memory>
#include <optional>
#include <span>
#include <sstream>
#include <stdexcept>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

#define private public
#include <tokenmonster/tokenmonster.hpp>
#undef private

namespace {

std::span<const std::uint8_t> bytes(const std::vector<std::uint8_t>& d) {
  return {d.data(), d.size()};
}
std::span<const std::uint8_t> bytes(std::string_view d) {
  return {reinterpret_cast<const std::uint8_t*>(d.data()), d.size()};
}

std::vector<std::uint8_t> make_corpus(std::size_t target_bytes) {
  constexpr std::string_view seed =
      "The Quick Brown Fox tests TokenMonster tokenization and detokenization.\n"
      "function tokenize(input) { return input.split(/\\\\s+/).map(x => x.toUpperCase()); }\n"
      "HTTP/2 status=200 path=/v1/chat/completions request_id=req_1234567890abcdef.\n"
      "SQL: SELECT user_id, created_at FROM events WHERE action = 'EncodeDecode' ORDER BY 2 DESC;\n"
      "JSON: {\"model\":\"englishcode-32000-consistent-v1\",\"temperature\":0,\"stream\":false}\n"
      "CamelCaseWords snake_case_words kebab-case-words MIXEDCaseABC xyzXYZ 1234567890.\n\n";
  std::vector<std::uint8_t> corpus;
  corpus.reserve(target_bytes + seed.size());
  while (corpus.size() < target_bytes) corpus.insert(corpus.end(), seed.begin(), seed.end());
  corpus.resize(target_bytes);
  return corpus;
}

}  // namespace

int main(int argc, char** argv) {
  if (argc < 4) {
    std::cerr << "usage: tokenmonster_prof VOCAB MODE ITERS [corpus_bytes]\n";
    return 2;
  }
  const std::string mode = argv[2];
  const long iters = std::stol(argv[3]);
  const std::size_t corpus_bytes =
      argc > 4 ? static_cast<std::size_t>(std::stoull(argv[4])) : (1ULL << 20);

  auto vocab = tokenmonster::Vocab::load(argv[1]);
  auto corpus = make_corpus(corpus_bytes);
  auto normalized = vocab.normalize(bytes(corpus));
  auto tokenized = vocab.tokenize(bytes(corpus));

  std::uint64_t checksum = 0;
  for (long it = 0; it < iters; ++it) {
    if (mode == "normalize") {
      auto r = vocab.normalize(bytes(corpus));
      checksum += r.size() + r.front();
    } else if (mode == "tokenize") {
      auto r = vocab.tokenize(bytes(corpus));
      checksum += r.tokens.size() + r.tokens.front();
    } else if (mode == "tokenize_norm") {
      auto r = vocab.tokenize_normalized(normalized);
      checksum += r.tokens.size() + r.tokens.front();
    } else if (mode == "decode") {
      auto r = vocab.decode(tokenized.tokens);
      checksum += r.size() + r.front();
    } else if (mode == "decode_raw") {
      auto r = vocab.decode_raw(tokenized.tokens);
      checksum += r.size() + r.front();
    } else {
      std::cerr << "unknown mode: " << mode << "\n";
      return 2;
    }
  }
  std::cerr << "mode=" << mode << " iters=" << iters << " checksum=" << checksum << "\n";
  return 0;
}
