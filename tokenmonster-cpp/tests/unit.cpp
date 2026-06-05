#include <cassert>
#include <cstring>
#include <filesystem>
#include <fstream>
#include <span>
#include <string>
#include <tokenmonster/tokenmonster.hpp>
#include <vector>

namespace {

void write_byte(std::ofstream& out, std::uint8_t v) {
  out.put(static_cast<char>(v));
}

void write_uint24(std::ofstream& out, std::uint32_t v) {
  write_byte(out, static_cast<std::uint8_t>(v));
  write_byte(out, static_cast<std::uint8_t>(v >> 8));
  write_byte(out, static_cast<std::uint8_t>(v >> 16));
}

void write_uint32(std::ofstream& out, std::uint32_t v) {
  write_byte(out, static_cast<std::uint8_t>(v));
  write_byte(out, static_cast<std::uint8_t>(v >> 8));
  write_byte(out, static_cast<std::uint8_t>(v >> 16));
  write_byte(out, static_cast<std::uint8_t>(v >> 24));
}

void write_float32(std::ofstream& out, float f) {
  std::uint32_t bits = 0;
  std::memcpy(&bits, &f, sizeof(bits));
  write_uint32(out, bits);
}

void write_bytes8(std::ofstream& out, std::string_view s) {
  write_byte(out, static_cast<std::uint8_t>(s.size()));
  out.write(s.data(), static_cast<std::streamsize>(s.size()));
}

void write_token(std::ofstream& out, std::string_view token, std::uint32_t id) {
  write_bytes8(out, token);
  write_byte(out, 0);                         // flag
  write_byte(out, 0);                         // nWords
  write_uint24(out, tokenmonster::does_not_exist);
  write_uint24(out, tokenmonster::does_not_exist);
  write_uint24(out, id);
  write_float32(out, 1.0F);
}

std::filesystem::path make_vocab() {
  auto path = std::filesystem::temp_directory_path() / "tokenmonster_cpp_minimal.vocab";
  std::ofstream out(path, std::ios::binary);
  assert(out);

  write_byte(out, 0);  // capcode
  write_byte(out, 0);  // charset none
  write_byte(out, 0);  // normalization
  write_byte(out, 5);  // mode
  write_byte(out, 0);  // reserve
  write_byte(out, 0);
  write_byte(out, 0);
  write_byte(out, 0);

  write_uint24(out, tokenmonster::does_not_exist);  // unk
  write_uint24(out, 4);                             // vocab size
  write_uint24(out, 4);                             // reverse entries
  write_uint24(out, 4);                             // info entries
  write_uint24(out, tokenmonster::does_not_exist);  // delete token
  write_byte(out, 2);                               // max token length

  write_token(out, " ", 0);
  write_token(out, "a", 1);
  write_token(out, "b", 2);
  write_token(out, "ab", 3);

  for (int i = 0; i < 256; ++i) write_byte(out, 0);
  write_uint24(out, 0);  // deleted tokens
  return path;
}

std::span<const std::uint8_t> bytes(std::string_view s) {
  return {reinterpret_cast<const std::uint8_t*>(s.data()), s.size()};
}

}  // namespace

int main() {
  auto vocab = tokenmonster::Vocab::load(make_vocab());
  assert(vocab.size() == 4);
  assert(vocab.max_token_length() == 2);

  auto result = vocab.tokenize(bytes("ab a z"));
  std::vector<std::uint32_t> expected{3, 0, 1, 0};
  assert(result.tokens == expected);
  assert(result.missing == 1);

  auto decoded = vocab.decode(result.tokens);
  assert(std::string(decoded.begin(), decoded.end()) == "ab a ");

  auto serialized = vocab.tokenize_serialized(bytes("ab"), 2);
  assert(serialized.encoding_length == 2);
  assert(serialized.bytes == std::vector<std::uint8_t>({3, 0}));
  assert(vocab.deserialize(serialized.bytes, 2) == std::vector<std::uint32_t>({3}));

  auto id = vocab.token_to_id(bytes("ab"));
  assert(id && *id == 3);
  auto tok = vocab.id_to_token(3);
  assert(tok && std::string(tok->begin(), tok->end()) == "ab");

  auto count = vocab.count(bytes("ab a z"));
  assert(count.tokens == 4);
  assert(count.missing == 1);

  auto decoder = vocab.new_decoder();
  auto stream_decoded = decoder.decode(std::vector<std::uint32_t>{3, 0, 1});
  assert(std::string(stream_decoded.begin(), stream_decoded.end()) == "ab a");
  auto stream_serialized = decoder.decode_serialized(std::vector<std::uint8_t>{0, 0}, 2);
  assert(std::string(stream_serialized.begin(), stream_serialized.end()) == " ");
  assert(decoder.flush().empty());
}
