// Parity fingerprint harness: runs the full public surface over a battery of
// diverse inputs for each vocab and prints an fnv1a fingerprint per (vocab, op).
// Run before and after a change; the output must be byte-identical.
//   usage: tokenmonster_parity VOCAB [VOCAB...]
#include <cstdint>
#include <iostream>
#include <span>
#include <string>
#include <string_view>
#include <vector>

#include <tokenmonster/tokenmonster.hpp>

namespace {

std::uint64_t fnv1a(std::span<const std::uint8_t> data, std::uint64_t h = 14695981039346656037ULL) {
  for (auto b : data) { h ^= b; h *= 1099511628211ULL; }
  return h;
}
std::uint64_t mix(std::uint64_t h, std::uint64_t v) {
  for (int i = 0; i < 8; ++i) { h ^= (v & 0xFF); h *= 1099511628211ULL; v >>= 8; }
  return h;
}
std::span<const std::uint8_t> bytes(std::string_view s) {
  return {reinterpret_cast<const std::uint8_t*>(s.data()), s.size()};
}
std::span<const std::uint8_t> bytes(const std::vector<std::uint8_t>& v) {
  return {v.data(), v.size()};
}
std::span<const std::uint8_t> bytes32(const std::vector<std::uint32_t>& v) {
  return {reinterpret_cast<const std::uint8_t*>(v.data()), v.size() * 4};
}

std::vector<std::string> make_inputs() {
  std::vector<std::string> in;
  in.push_back("");
  in.push_back(" ");
  in.push_back("a");
  in.push_back("The Quick Brown Fox tests TokenMonster.");
  in.push_back("CamelCaseWords snake_case_words kebab-case-words MIXEDCaseABC xyzXYZ 1234567890.");
  in.push_back("function f(x) { return x.split(/\\s+/).map(y => y.toUpperCase()); }\n\n\n");
  in.push_back("SELECT user_id, created_at FROM events WHERE action = 'EncodeDecode';");
  in.push_back("  leading and   collapsed   spaces\r\nwindows\r\nlines\ttabs  ");
  in.push_back("Unicode: café naïve résumé — “curly quotes” ‘single’ \xE2\x80\x99 ellipsis…");
  in.push_back("CJK 日本語 中文 한국어 emoji 😀🔥🚀 combining é e\xCC\x81 ligature ﬁ");
  in.push_back("Ⓜ️ ＦＵＬＬＷＩＤＴＨ ¡Hola! ÀÉÎÕÜ ß Straße");
  in.push_back(std::string(500, 'x') + " " + std::string(300, 'A'));
  // A larger mixed blob to exercise the branch/delete-token lookaheads.
  std::string big;
  const std::string seed =
      "The Quick Brown Fox tests TokenMonster tokenization and detokenization.\n"
      "HTTP/2 status=200 path=/v1/chat/completions request_id=req_1234567890abcdef.\n"
      "JSON: {\"model\":\"englishcode-32000-consistent-v1\",\"temperature\":0}\n"
      "Café résumé 日本語 MIXEDCase snake_case  double  spaces\r\n\r\n";
  while (big.size() < 64000) big += seed;
  in.push_back(big);
  return in;
}

std::uint64_t fingerprint_vocab(const tokenmonster::Vocab& v,
                                const std::vector<std::string>& inputs) {
  std::uint64_t h = 1469598103934665603ULL;
  h = mix(h, static_cast<std::uint64_t>(v.size()));
  h = mix(h, static_cast<std::uint64_t>(v.max_token_length()));
  h = mix(h, static_cast<std::uint64_t>(v.highest_token_id()));
  for (const auto& s : inputs) {
    auto in = bytes(s);

    auto norm = v.normalize(in);
    h = fnv1a(bytes(norm), h);

    auto tok = v.tokenize(in);
    h = fnv1a(bytes32(tok.tokens), h);
    h = mix(h, static_cast<std::uint64_t>(tok.missing));

    auto cnt = v.count(in);
    h = mix(h, static_cast<std::uint64_t>(cnt.tokens));
    h = mix(h, static_cast<std::uint64_t>(cnt.missing));

    auto grd = v.tokenize_greedy(in);
    h = fnv1a(bytes32(grd.tokens), h);
    h = mix(h, static_cast<std::uint64_t>(grd.missing));

    for (std::uint8_t enc : {2, 3, 4}) {
      auto ser = v.tokenize_serialized(in, enc);
      h = fnv1a(bytes(ser.bytes), h);
      h = mix(h, ser.encoding_length);
      h = mix(h, static_cast<std::uint64_t>(ser.missing));
      auto dec = v.decode_serialized(ser.bytes, ser.encoding_length);
      h = fnv1a(bytes(dec), h);
      auto deser = v.deserialize(ser.bytes, ser.encoding_length);
      h = fnv1a(bytes32(deser), h);
    }

    auto decoded = v.decode(tok.tokens);
    h = fnv1a(bytes(decoded), h);

    // Streaming decoder split into three chunks.
    auto dec = v.new_decoder();
    std::uint64_t stream = 0;
    std::size_t n = tok.tokens.size();
    std::size_t a = n / 3, b = 2 * n / 3;
    std::vector<std::uint32_t> c1(tok.tokens.begin(), tok.tokens.begin() + a);
    std::vector<std::uint32_t> c2(tok.tokens.begin() + a, tok.tokens.begin() + b);
    std::vector<std::uint32_t> c3(tok.tokens.begin() + b, tok.tokens.end());
    auto s1 = dec.decode(c1);
    auto s2 = dec.decode(c2);
    auto s3 = dec.decode(c3);
    auto sf = dec.flush();
    stream = fnv1a(bytes(s1), stream);
    stream = fnv1a(bytes(s2), stream);
    stream = fnv1a(bytes(s3), stream);
    stream = fnv1a(bytes(sf), stream);
    h = mix(h, stream);
  }
  return h;
}

}  // namespace

int main(int argc, char** argv) {
  if (argc < 2) {
    std::cerr << "usage: tokenmonster_parity VOCAB [VOCAB...]\n";
    return 2;
  }
  auto inputs = make_inputs();
  std::uint64_t overall = 1469598103934665603ULL;
  for (int i = 1; i < argc; ++i) {
    try {
      auto v = tokenmonster::Vocab::load(argv[i]);
      auto fp = fingerprint_vocab(v, inputs);
      overall = mix(overall, fp);
      std::cout << argv[i] << "\t" << fp << "\n";
    } catch (const std::exception& e) {
      std::cout << argv[i] << "\tERROR: " << e.what() << "\n";
    }
  }
  std::cout << "OVERALL\t" << overall << "\n";
  return 0;
}
