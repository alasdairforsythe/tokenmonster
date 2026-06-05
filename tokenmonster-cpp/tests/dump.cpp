#include <cstdint>
#include <cstdlib>
#include <iostream>
#include <span>
#include <string>
#include <tokenmonster/tokenmonster.hpp>
#include <vector>

namespace {

std::span<const std::uint8_t> bytes(std::string_view s) {
  return {reinterpret_cast<const std::uint8_t*>(s.data()), s.size()};
}

void print_hex(std::span<const std::uint8_t> data) {
  static constexpr char digits[] = "0123456789abcdef";
  for (auto b : data) {
    std::cout << digits[b >> 4] << digits[b & 15];
  }
}

void print_tokens(std::span<const std::uint32_t> tokens) {
  for (std::size_t i = 0; i < tokens.size(); ++i) {
    if (i != 0) std::cout << ",";
    std::cout << tokens[i];
  }
}

}  // namespace

int main(int argc, char** argv) {
  if (argc < 3) {
    std::cerr << "usage: tokenmonster_dump VOCAB TEXT...\n";
    return 2;
  }

  try {
    auto vocab = tokenmonster::Vocab::load(argv[1]);
    for (int arg = 2; arg < argc; ++arg) {
      std::string input = argv[arg];
      auto input_bytes = bytes(input);
      auto normalized = vocab.normalize(input_bytes);
      auto tokenized = vocab.tokenize(input_bytes);
      auto counted = vocab.count(input_bytes);
      auto serialized_auto = vocab.tokenize_serialized(input_bytes, 0);
      auto serialized2 = vocab.tokenize_serialized(input_bytes, 2);
      auto serialized3 = vocab.tokenize_serialized(input_bytes, 3);
      auto serialized4 = vocab.tokenize_serialized(input_bytes, 4);
      auto decoded = vocab.decode(tokenized.tokens);
      auto decoded_serialized_auto =
          vocab.decode_serialized(serialized_auto.bytes, serialized_auto.encoding_length);

      auto decoder = vocab.new_decoder();
      std::vector<std::uint32_t> first_half;
      std::vector<std::uint32_t> second_half;
      auto split = tokenized.tokens.size() / 2;
      first_half.assign(tokenized.tokens.begin(), tokenized.tokens.begin() + split);
      second_half.assign(tokenized.tokens.begin() + split, tokenized.tokens.end());
      auto stream1 = decoder.decode(first_half);
      auto stream2 = decoder.decode(second_half);
      auto stream_flush = decoder.flush();

      auto serialized_decoder = vocab.new_decoder();
      auto serialized_split =
          (serialized_auto.bytes.size() / serialized_auto.encoding_length / 2) *
          serialized_auto.encoding_length;
      auto serialized_stream1 = serialized_decoder.decode_serialized(
          std::span<const std::uint8_t>(serialized_auto.bytes).first(serialized_split),
          serialized_auto.encoding_length);
      auto serialized_stream2 = serialized_decoder.decode_serialized(
          std::span<const std::uint8_t>(serialized_auto.bytes).subspan(serialized_split),
          serialized_auto.encoding_length);
      auto serialized_stream_flush = serialized_decoder.flush();

      std::cout << "case=" << (arg - 2) << "\n";
      std::cout << "input_hex=";
      print_hex(input_bytes);
      std::cout << "\nnormalized_hex=";
      print_hex(normalized);
      std::cout << "\ntokens=";
      print_tokens(tokenized.tokens);
      std::cout << "\nmissing=" << tokenized.missing;
      std::cout << "\ncount=" << counted.tokens << "," << counted.missing;
      std::cout << "\nserialized_auto=" << int(serialized_auto.encoding_length) << ",";
      print_hex(serialized_auto.bytes);
      std::cout << "\nserialized2=";
      print_hex(serialized2.bytes);
      std::cout << "\nserialized3=";
      print_hex(serialized3.bytes);
      std::cout << "\nserialized4=";
      print_hex(serialized4.bytes);
      std::cout << "\ndecoded_hex=";
      print_hex(decoded);
      std::cout << "\ndecoded_serialized_auto_hex=";
      print_hex(decoded_serialized_auto);
      std::cout << "\nstream_hex=";
      print_hex(stream1);
      print_hex(stream2);
      print_hex(stream_flush);
      std::cout << "\nserialized_stream_hex=";
      print_hex(serialized_stream1);
      print_hex(serialized_stream2);
      print_hex(serialized_stream_flush);
      std::cout << "\n";
    }
  } catch (const std::exception& err) {
    std::cerr << "error: " << err.what() << "\n";
    return 1;
  }
}
