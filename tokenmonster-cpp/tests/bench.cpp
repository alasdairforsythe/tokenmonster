#include <chrono>
#include <cstdint>
#include <filesystem>
#include <iomanip>
#include <iostream>
#include <memory>
#include <optional>
#include <span>
#include <stdexcept>
#include <string>
#include <string_view>
#include <utility>
#define private public
#include <tokenmonster/tokenmonster.hpp>
#undef private
#include <vector>

namespace {

using Clock = std::chrono::steady_clock;

std::span<const std::uint8_t> bytes(const std::vector<std::uint8_t>& data) {
  return {data.data(), data.size()};
}

std::span<const std::uint8_t> bytes(std::string_view data) {
  return {reinterpret_cast<const std::uint8_t*>(data.data()), data.size()};
}

std::uint64_t fnv1a(std::span<const std::uint8_t> data) {
  std::uint64_t hash = 14695981039346656037ULL;
  for (auto b : data) {
    hash ^= b;
    hash *= 1099511628211ULL;
  }
  return hash;
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
  while (corpus.size() < target_bytes) {
    corpus.insert(corpus.end(), seed.begin(), seed.end());
  }
  corpus.resize(target_bytes);
  return corpus;
}

template <typename Fn>
void run_bench(std::string_view name, double seconds, std::size_t bytes_per_iter, Fn&& fn) {
  std::uint64_t checksum = 0;
  for (int i = 0; i < 5; ++i) {
    checksum += fn();
  }

  const auto start = Clock::now();
  const auto deadline = start + std::chrono::duration_cast<Clock::duration>(
                                    std::chrono::duration<double>(seconds));
  std::uint64_t iterations = 0;
  do {
    checksum += fn();
    ++iterations;
  } while (Clock::now() < deadline);
  const auto elapsed = std::chrono::duration<double>(Clock::now() - start).count();
  const double ns_per_op = elapsed * 1.0e9 / static_cast<double>(iterations);
  const double mbps =
      (static_cast<double>(bytes_per_iter) * static_cast<double>(iterations)) /
      elapsed / 1000000.0;

  std::cout << name << '\t' << iterations << '\t' << std::fixed << std::setprecision(6)
            << elapsed << '\t' << std::setprecision(1) << ns_per_op << '\t'
            << std::setprecision(1) << mbps << '\t' << checksum << '\n';
}

}  // namespace

int main(int argc, char** argv) {
  if (argc < 2) {
    std::cerr << "usage: tokenmonster_bench VOCAB [seconds] [corpus_bytes]\n";
    return 2;
  }

  const double seconds = argc > 2 ? std::stod(argv[2]) : 2.0;
  const std::size_t corpus_bytes = argc > 3 ? static_cast<std::size_t>(std::stoull(argv[3]))
                                            : (1ULL << 20);

  try {
    auto vocab = tokenmonster::Vocab::load(argv[1]);
    auto corpus = make_corpus(corpus_bytes);
    auto tokenized = vocab.tokenize(bytes(corpus));
    auto decoded = vocab.decode(tokenized.tokens);

    std::cout << "impl\tcpp\n";
    std::cout << "vocab_size\t" << vocab.size() << '\n';
    std::cout << "corpus_bytes\t" << corpus.size() << '\n';
    std::cout << "corpus_fnv1a\t" << fnv1a(bytes(corpus)) << '\n';
    std::cout << "tokens\t" << tokenized.tokens.size() << '\n';
    std::cout << "missing\t" << tokenized.missing << '\n';
    std::cout << "decoded_bytes\t" << decoded.size() << '\n';
    std::cout << "decoded_fnv1a\t" << fnv1a(bytes(decoded)) << '\n';
    std::cout << "bench\titerations\tseconds\tns_per_op\tMB_per_s\tchecksum\n";

    run_bench("normalize", seconds, corpus.size(), [&]() {
      auto result = vocab.normalize(bytes(corpus));
      return fnv1a(bytes(result));
    });

    auto normalized = vocab.normalize(bytes(corpus));
    run_bench("tokenize_normalized", seconds, normalized.size(), [&]() {
      auto result = vocab.tokenize_normalized(bytes(normalized));
      std::uint64_t sum = static_cast<std::uint64_t>(result.tokens.size()) +
                          static_cast<std::uint64_t>(result.missing) * 131ULL;
      if (!result.tokens.empty()) {
        sum += result.tokens.front();
        sum += result.tokens.back();
      }
      return sum;
    });

    run_bench("encode_tokenize", seconds, corpus.size(), [&]() {
      auto result = vocab.tokenize(bytes(corpus));
      std::uint64_t sum = static_cast<std::uint64_t>(result.tokens.size()) +
                          static_cast<std::uint64_t>(result.missing) * 131ULL;
      if (!result.tokens.empty()) {
        sum += result.tokens.front();
        sum += result.tokens.back();
      }
      return sum;
    });

    run_bench("decode_tokens", seconds, decoded.size(), [&]() {
      auto result = vocab.decode(tokenized.tokens);
      std::uint64_t sum = static_cast<std::uint64_t>(result.size());
      if (!result.empty()) {
        sum += result.front();
        sum += result.back();
      }
      return sum;
    });
  } catch (const std::exception& err) {
    std::cerr << "error: " << err.what() << '\n';
    return 1;
  }
}
