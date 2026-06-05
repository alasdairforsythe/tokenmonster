#include <iostream>
#include <tokenmonster/tokenmonster.hpp>

int main(int argc, char** argv) {
  if (argc < 2) {
    std::cerr << "usage: tokenmonster_smoke VOCAB [TEXT]\n";
    return 2;
  }

  try {
    auto vocab = tokenmonster::Vocab::load(argv[1]);
    std::cout << "vocab_size=" << vocab.size()
              << " max_token_length=" << vocab.max_token_length()
              << " charset=" << int(vocab.charset())
              << " capcode=" << int(vocab.capcode()) << "\n";

    if (argc >= 3) {
      std::string text = argv[2];
      auto bytes = std::span<const std::uint8_t>(
          reinterpret_cast<const std::uint8_t*>(text.data()), text.size());
      auto result = vocab.tokenize(bytes);
      auto decoded = vocab.decode(result.tokens);
      std::cout << "tokens=" << result.tokens.size()
                << " missing=" << result.missing
                << " decoded=" << std::string(decoded.begin(), decoded.end()) << "\n";
    }
  } catch (const std::exception& err) {
    std::cerr << "error: " << err.what() << "\n";
    return 1;
  }
}
